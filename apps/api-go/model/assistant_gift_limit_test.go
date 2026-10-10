package model

import (
	"strconv"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func putAssistantGiftCap(t *testing.T, db *gorm.DB, cap int) {
	t.Helper()
	require.NoError(t, db.Clauses(clause.OnConflict{UpdateAll: true}).Create(&Option{Key: setting.AssistantNewUserGiftMaxCreditsOptionKey, Value: strconv.Itoa(cap)}).Error)
}

func TestAssistantGiftCreditCapLowerHigherAndDisabled(t *testing.T) {
	for _, tc := range []struct {
		name       string
		cap, grant int
		accepted   bool
	}{
		{"lower_at_limit", 100000, 100000, false}, {"lower_over_limit", 100000, 100001, false},
		{"higher_than_old_cap", 10000000, 9000000, true}, {"one_credit", 1, 1, false}, {"one_credit_below_bound", 2, 1, true},
		{"disabled_positive", 0, 1, false}, {"disabled_decline", 0, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := setupAssistantGiftTestDB(t)
			putAssistantGiftCap(t, db, tc.cap)
			user := newAssistantGiftUser(t, db, "cap-user", "cap@example.com")
			gift, created, err := DecideAssistantNewUserGiftCredits(user.Id, 1, tc.grant, "A concrete workflow for reviewing software changes.", 1, 80, "198.51.100.10")
			if !tc.accepted {
				require.Error(t, err)
				require.False(t, created)
				require.Nil(t, gift)
				if tc.cap == 0 {
					require.ErrorIs(t, err, ErrAssistantGiftDisabled)
				} else {
					require.ErrorIs(t, err, ErrAssistantGiftLimit)
				}
				for _, table := range []any{&AssistantNewUserGift{}, &AssistantGiftRiskMemory{}} {
					var count int64
					require.NoError(t, db.Model(table).Count(&count).Error)
					require.Zero(t, count)
				}
				return
			}
			require.NoError(t, err)
			require.True(t, created)
			require.Equal(t, tc.grant, gift.Quota)
			var stored User
			require.NoError(t, db.First(&stored, user.Id).Error)
			require.Zero(t, stored.Quota, "offers never auto-credit")
			_, replay, err := ClaimAssistantNewUserGift(user.Id)
			require.NoError(t, err)
			require.False(t, replay)
			require.NoError(t, db.First(&stored, user.Id).Error)
			require.Equal(t, tc.grant, stored.Quota)
		})
	}
}

func TestAssistantGiftClaimRechecksDurableLoweredCapWithoutRewritingOffer(t *testing.T) {
	db := setupAssistantGiftTestDB(t)
	putAssistantGiftCap(t, db, 9000000)
	original := setting.GetAssistantSettings().NewUserGiftMaxCredits
	require.NoError(t, setting.UpdateAssistantNewUserGiftMaxCredits("9000000"))
	t.Cleanup(func() { require.NoError(t, setting.UpdateAssistantNewUserGiftMaxCredits(original)) })
	user := newAssistantGiftUser(t, db, "pending-cap", "pending-cap@example.com")
	gift, _, err := DecideAssistantNewUserGiftCredits(user.Id, 1, 7000000, "A concrete workflow for reviewing software changes.", 1, 80, "198.51.100.10")
	require.NoError(t, err)
	for _, cap := range []int{1000000, 0} {
		putAssistantGiftCap(t, db, cap) // Another node's change; this process keeps its old cache.
		_, _, err = ClaimAssistantNewUserGift(user.Id)
		require.Error(t, err)
		if cap == 0 {
			require.ErrorIs(t, err, ErrAssistantGiftDisabled)
		} else {
			require.ErrorIs(t, err, ErrAssistantGiftLimit)
		}
		var stored AssistantNewUserGift
		require.NoError(t, db.First(&stored, gift.Id).Error)
		require.Equal(t, *gift, stored)
		var account User
		require.NoError(t, db.First(&account, user.Id).Error)
		require.Zero(t, account.Quota)
	}
	putAssistantGiftCap(t, db, 7000000) // Allow redemption of an older exact-cap offer.
	claimed, _, err := ClaimAssistantNewUserGift(user.Id)
	require.NoError(t, err)
	putAssistantGiftCap(t, db, 0)
	replay, already, err := ClaimAssistantNewUserGift(user.Id)
	require.NoError(t, err)
	require.True(t, already)
	require.Equal(t, *claimed, *replay)
	var account User
	require.NoError(t, db.First(&account, user.Id).Error)
	require.Equal(t, 7000000, account.Quota)
}

func TestAssistantGiftCapChecksEffectiveHistoricalCreditsOnceAndLeavesNewCreditsIntact(t *testing.T) {
	db := setupAssistantGiftTestDB(t)
	user := newAssistantGiftUser(t, db, "old-cap-gift", "old-cap-gift@example.com")
	gift := AssistantNewUserGift{UserId: user.Id, AmountCents: 100, Quota: 6710363, Status: AssistantGiftOffered, Reason: "Historical offered gift", CreatedAt: 123}
	require.NoError(t, db.Create(&gift).Error)
	cutoff := time.Now().Unix() - 1
	putFutureCreditAudit(t, db, []int{user.Id}, cutoff, []map[string]any{{"kind": "assistant_gift", "source_id": strconv.FormatInt(gift.Id, 10), "user_id": user.Id, "original_quota": gift.Quota, "rebased_quota": 1000000, "source": map[string]any{"quota": gift.Quota}}})
	putAssistantGiftCap(t, db, 1000001)
	_, _, err := ClaimAssistantNewUserGift(user.Id)
	require.NoError(t, err)
	_, already, err := ClaimAssistantNewUserGift(user.Id)
	require.NoError(t, err)
	require.True(t, already)
	var account User
	require.NoError(t, db.First(&account, user.Id).Error)
	require.Equal(t, 1000000, account.Quota)
	// A different user whose old wallet was rebased receives new raw credits,
	// with no second application of the historical divisor.
	newer := newAssistantGiftUser(t, db, "new-cap-gift", "new-cap-gift@example.com")
	plan := []byte(`{"user_ids":[` + strconv.Itoa(user.Id) + `,` + strconv.Itoa(newer.Id) + `],"snapshot_at":` + strconv.FormatInt(cutoff, 10) + `,"divisor":"6.710363","rounding":"half-away-from-zero","include_other_rights":true,"other_credit_bases":[{"kind":"assistant_gift","source_id":"` + strconv.FormatInt(gift.Id, 10) + `","user_id":` + strconv.Itoa(user.Id) + `,"original_quota":6710363,"rebased_quota":1000000,"source":{"quota":6710363}}]}`)
	require.NoError(t, db.Exec("UPDATE wallet_credit_rebases SET plan = ?", string(plan)).Error)
	created, _, err := DecideAssistantNewUserGiftCredits(newer.Id, 2, 1000000, "A concrete workflow for reviewing software changes.", 1, 80, "198.51.100.11")
	require.NoError(t, err)
	require.Greater(t, created.CreatedAt, cutoff)
	_, _, err = ClaimAssistantNewUserGift(newer.Id)
	require.NoError(t, err)
	account = User{}
	require.NoError(t, db.First(&account, newer.Id).Error)
	require.Equal(t, 1000000, account.Quota)
}

func TestAssistantGiftCapDefaultsPreserveLegacyGrantAndLegacyBridgeRespectsCap(t *testing.T) {
	db := setupAssistantGiftTestDB(t)
	common.QuotaPerUnit = 3.5
	require.NoError(t, common.SetCreditCurrencyBasis(decimal.NewFromInt(500000), decimal.RequireFromString("3.5")))
	cap, err := AssistantGiftMaxCreditsDB(db)
	require.NoError(t, err)
	require.Equal(t, 35, cap)
	putAssistantGiftCap(t, db, 10)
	user := newAssistantGiftUser(t, db, "legacy-cap", "legacy-cap@example.com")
	_, _, err = DecideAssistantNewUserGift(user.Id, 1, 1000, "A concrete workflow for reviewing software changes.", 1, 80, "198.51.100.10")
	require.ErrorIs(t, err, ErrAssistantGiftLimit)
}

func TestAssistantGiftCapOptionValidationAndPersistence(t *testing.T) {
	db := setupAssistantGiftTestDB(t)
	common.OptionMapRWMutex.Lock()
	previousMap := common.OptionMap
	common.OptionMap = make(map[string]string)
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previousMap
		common.OptionMapRWMutex.Unlock()
	})
	previous := setting.GetAssistantSettings().NewUserGiftMaxCredits
	t.Cleanup(func() { require.NoError(t, setting.UpdateAssistantNewUserGiftMaxCredits(previous)) })
	putAssistantGiftCap(t, db, 123)
	for _, bad := range []string{"", "-1", "1.5", "NaN", "Inf", "9007199254740992", "9223372036854775808"} {
		require.Error(t, ValidateOptionValue(setting.AssistantNewUserGiftMaxCreditsOptionKey, bad))
		require.Error(t, UpdateOptionsBulk(map[string]string{setting.AssistantNewUserGiftMaxCreditsOptionKey: bad, "Notice": "must-not-save"}))
		var stored Option
		require.NoError(t, db.First(&stored, "key = ?", setting.AssistantNewUserGiftMaxCreditsOptionKey).Error)
		require.Equal(t, "123", stored.Value)
		var count int64
		require.NoError(t, db.Model(&Option{}).Where("key = ?", "Notice").Count(&count).Error)
		require.Zero(t, count)
	}
	for _, good := range []string{"0", "1", "9000000", strconv.FormatInt(common.MaxWalletQuota, 10)} {
		require.NoError(t, UpdateOption(setting.AssistantNewUserGiftMaxCreditsOptionKey, good))
		var stored Option
		require.NoError(t, db.First(&stored, "key = ?", setting.AssistantNewUserGiftMaxCreditsOptionKey).Error)
		require.Equal(t, good, stored.Value)
		require.Equal(t, good, setting.GetAssistantSettings().NewUserGiftMaxCredits)
	}
}

func TestAssistantGiftMalformedDurableCapFailsClosed(t *testing.T) {
	db := setupAssistantGiftTestDB(t)
	user := newAssistantGiftUser(t, db, "malformed-cap", "malformed-cap@example.com")
	require.NoError(t, db.Create(&Option{Key: setting.AssistantNewUserGiftMaxCreditsOptionKey, Value: "NaN"}).Error)
	_, created, err := DecideAssistantNewUserGiftCredits(user.Id, 1, 1, "A concrete legitimate backend development workflow.", 1, 80, "198.51.100.10")
	require.Error(t, err)
	require.False(t, created)
	var count int64
	require.NoError(t, db.Model(&AssistantNewUserGift{}).Count(&count).Error)
	require.Zero(t, count)
}
