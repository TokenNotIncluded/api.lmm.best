package model

import (
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func putFutureCreditAudit(t *testing.T, db *gorm.DB, userIDs []int, at int64, bases []map[string]any) {
	t.Helper()
	require.NoError(t, db.Exec("CREATE TABLE wallet_credit_rebases (migration_id TEXT PRIMARY KEY, plan TEXT NOT NULL)").Error)
	plan, err := json.Marshal(map[string]any{"user_ids": userIDs, "snapshot_at": at, "divisor": "6.710363", "rounding": "half-away-from-zero", "include_other_rights": true, "other_credit_bases": bases})
	require.NoError(t, err)
	require.NoError(t, db.Exec("INSERT INTO wallet_credit_rebases (migration_id,plan) VALUES (?,?)", "future-rights-test", string(plan)).Error)
}

func TestPublicRelayRebaseWithdrawsOldPoolOnceAndKeepsNewTips(t *testing.T) {
	installPublicRelayCreditFixture(t)
	require.NoError(t, common.SetCreditCurrencyBasis(decimal.NewFromInt(500000), decimal.NewFromInt(500000)))
	db := setupConsoleActivationTestDB(t)
	require.NoError(t, db.AutoMigrate(&PublicRelayContribution{}, &PublicRelayTip{}, &Log{}))
	owner := User{Username: "corrected-tip-owner", AffCode: "corrected-tip-owner", Quota: 100}
	require.NoError(t, db.Create(&owner).Error)
	item := PublicRelayContribution{UserId: owner.Id, Name: "corrected", Status: PublicRelayApproved, CreatedAt: 123, TipQuota: 67_103_730, WithdrawnQuota: 100}
	require.NoError(t, db.Create(&item).Error)
	oldTip := PublicRelayTip{ContributionId: item.Id, Quota: 67_103_730, CreatedAt: 124}
	require.NoError(t, db.Create(&oldTip).Error)
	putFutureCreditAudit(t, db, []int{owner.Id}, 1700000000, []map[string]any{{"kind": "public_relay_tip_pool", "source_id": strconv.Itoa(item.Id), "user_id": owner.Id, "original_quota": 67_103_630, "rebased_quota": 10_000_000,
		"source": map[string]any{"tip_quota": item.TipQuota, "withdrawn_quota": item.WithdrawnQuota}}})
	view, err := item.PublicView()
	require.NoError(t, err)
	require.EqualValues(t, 10_000_000, view.AvailableTipQuota)
	mine, err := ListUserPublicRelayContributions(owner.Id, 50)
	require.NoError(t, err)
	require.Len(t, mine, 1)
	require.EqualValues(t, 10_000_000, mine[0].AvailableTipQuota)
	admin, err := ListAdminPublicRelayContributions("approved", 50)
	require.NoError(t, err)
	require.Len(t, admin, 1)
	require.EqualValues(t, 10_000_000, admin[0].AvailableTipQuota)
	require.Equal(t, item.TipQuota, view.TipQuota, "the source receipt remains readable")
	// A new five-million-credit tip after migration must stay five million.
	require.NoError(t, db.Model(&item).UpdateColumn("tip_quota", item.TipQuota+5_000_000).Error)
	amount, err := WithdrawPublicRelayTips(item.Id, owner.Id, "default")
	require.NoError(t, err)
	require.EqualValues(t, 15_000_000, amount)
	var current User
	require.NoError(t, db.First(&current, owner.Id).Error)
	require.Equal(t, 15_000_100, current.Quota)
	var stored PublicRelayContribution
	require.NoError(t, db.First(&stored, item.Id).Error)
	require.Equal(t, stored.TipQuota, stored.WithdrawnQuota)
	require.NoError(t, db.Model(&stored).UpdateColumn("tip_quota", stored.TipQuota+5_000_000).Error)
	amount, err = WithdrawPublicRelayTips(item.Id, owner.Id, "default")
	require.NoError(t, err)
	require.EqualValues(t, 5_000_000, amount)
	_, err = WithdrawPublicRelayTips(item.Id, owner.Id, "default")
	require.ErrorIs(t, err, ErrPublicRelayInvalidInput)
	var unchanged PublicRelayTip
	require.NoError(t, db.First(&unchanged, oldTip.Id).Error)
	require.Equal(t, oldTip, unchanged)
}

func TestAssistantOfferedGiftRebasePreservesDecisionAndClaimsActualCredits(t *testing.T) {
	db := setupAssistantGiftTestDB(t)
	user := newAssistantGiftUser(t, db, "rebased-assistant-gift", "rebased-assistant@example.com")
	item := AssistantNewUserGift{UserId: user.Id, AmountCents: 100, Quota: 6_710_363, Status: AssistantGiftOffered, Reason: "Stored decision before correction", CreatedAt: 123}
	require.NoError(t, db.Create(&item).Error)
	putFutureCreditAudit(t, db, []int{user.Id}, time.Now().Unix(), []map[string]any{{"kind": "assistant_gift", "source_id": strconv.FormatInt(item.Id, 10), "user_id": user.Id, "original_quota": item.Quota, "rebased_quota": 1_000_000, "source": map[string]any{"quota": item.Quota}}})
	quota, err := AssistantGiftCreditQuota(&item)
	require.NoError(t, err)
	require.Equal(t, 1_000_000, quota)
	claimed, replay, err := ClaimAssistantNewUserGift(user.Id)
	require.NoError(t, err)
	require.False(t, replay)
	require.Equal(t, item.Quota, claimed.Quota)
	require.Equal(t, item.AmountCents, claimed.AmountCents)
	_, replay, err = ClaimAssistantNewUserGift(user.Id)
	require.NoError(t, err)
	require.True(t, replay)
	var current User
	require.NoError(t, db.First(&current, user.Id).Error)
	require.Equal(t, 1_000_000, current.Quota)
	quota, err = AssistantGiftCreditQuota(claimed)
	require.NoError(t, err)
	require.Equal(t, 1_000_000, quota)
}

func TestFutureGiftTemplateRebaseKeepsHistoricalClaimsAndUsesEffectiveGrant(t *testing.T) {
	db := setupGiftTestDB(t)
	now := time.Now().Unix()
	user := createGiftTestUser(t, "rebased-gift-recipient", 0, 0)
	require.NoError(t, db.Model(user).UpdateColumn("aff_code", "rebased-gift-recipient").Error)
	oldRecipient := createGiftTestUser(t, "historical-gift-recipient", 0, 0)
	item := createTestGift(t, 6_710_363, now-100, now+1000, 0, 0)
	oldClaim := GiftClaim{GiftId: item.Id, UserId: oldRecipient.Id, Quota: item.Quota, CreatedAt: now - 10}
	require.NoError(t, db.Create(&oldClaim).Error)
	putFutureCreditAudit(t, db, []int{user.Id, oldRecipient.Id}, now, []map[string]any{{"kind": "grant_gift", "source_id": strconv.Itoa(item.Id), "user_id": 0, "original_quota": item.Quota, "rebased_quota": 1_000_000, "source": map[string]any{"quota": item.Quota}}})
	views, err := GetAvailableGiftsForUser(user.Id)
	require.NoError(t, err)
	require.Equal(t, 1_000_000, views[0].Quota)
	claim, replay, err := ClaimGift(user.Id, item.Id)
	require.NoError(t, err)
	require.False(t, replay)
	require.Equal(t, 1_000_000, claim.Quota)
	_, replay, err = ClaimGift(user.Id, item.Id)
	require.NoError(t, err)
	require.True(t, replay)
	var stored Gift
	require.NoError(t, db.First(&stored, item.Id).Error)
	require.Equal(t, item.Quota, stored.Quota)
	var unchanged GiftClaim
	require.NoError(t, db.First(&unchanged, oldClaim.Id).Error)
	require.Equal(t, oldClaim, unchanged)
	views, err = GetAvailableGiftsForUser(oldRecipient.Id)
	require.NoError(t, err)
	require.Equal(t, item.Quota, views[0].Quota)
}

func TestMissingFutureGiftBasisRejectsOldGrantButAllowsNewSource(t *testing.T) {
	db := setupGiftTestDB(t)
	now := time.Now().Unix()
	user := createGiftTestUser(t, "missing-gift-basis", 0, 0)
	item := createTestGift(t, 100, now-100, now+1000, 0, 0)
	require.NoError(t, db.Model(item).UpdateColumn("created_at", now-10).Error)
	putFutureCreditAudit(t, db, []int{user.Id}, now, []map[string]any{})
	_, _, err := ClaimGift(user.Id, item.Id)
	require.ErrorIs(t, err, ErrWalletQuotaOutOfRange)
	var count int64
	require.NoError(t, db.Model(&GiftClaim{}).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, db.Model(item).UpdateColumn("created_at", now+1).Error)
	claim, _, err := ClaimGift(user.Id, item.Id)
	require.NoError(t, err)
	require.Equal(t, 100, claim.Quota)
}

func TestFutureGiftRoundedZeroKeepsSourceAndCannotCreditTwice(t *testing.T) {
	db := setupGiftTestDB(t)
	now := time.Now().Unix()
	user := createGiftTestUser(t, "zero-corrected-gift", 0, 0)
	item := createTestGift(t, 1, now-100, now+1000, 0, 0)
	putFutureCreditAudit(t, db, []int{user.Id}, now, []map[string]any{{"kind": "grant_gift", "source_id": strconv.Itoa(item.Id), "user_id": 0, "original_quota": 1, "rebased_quota": 0, "source": map[string]any{"quota": 1}}})
	claim, replay, err := ClaimGift(user.Id, item.Id)
	require.NoError(t, err)
	require.False(t, replay)
	require.Zero(t, claim.Quota)
	_, replay, err = ClaimGift(user.Id, item.Id)
	require.NoError(t, err)
	require.True(t, replay)
	var current User
	require.NoError(t, db.First(&current, user.Id).Error)
	require.Zero(t, current.Quota)
	var stored Gift
	require.NoError(t, db.First(&stored, item.Id).Error)
	require.Equal(t, 1, stored.Quota)
}
