package model

import (
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// The existing qualification gate selects TestReferralPostgres*. This uses
// its disposable, opt-in PostgreSQL schema, not a production database.
func TestReferralPostgresUnicodeEvidenceAndRetry(t *testing.T) {
	db, actor, inviter, invitee, _, payment := setupReferralPostgresTest(t)
	_, err := CompleteExternalTopUp(payment)
	require.NoError(t, err)
	ban := referralBan(actor, invitee, "pg-unicode-ban", true)
	for _, invalid := range []string{strings.Repeat("证", 1001), "review\x00evidence"} {
		ban.Evidence = invalid
		require.ErrorIs(t, ModerateReferralUser(ban), gorm.ErrInvalidData)
	}
	var count int64
	require.NoError(t, db.Model(&ReferralModerationEvent{}).Count(&count).Error)
	require.Zero(t, count)
	require.Equal(t, common.UserStatusEnabled, referralUser(t, db, invitee.Id).Status)
	require.Equal(t, 1_000_000, referralUser(t, db, inviter.Id).AffQuota)
	ban.Evidence = strings.Repeat("证", 1000)
	require.NoError(t, ModerateReferralUser(ban))
	require.NoError(t, ModerateReferralUser(ban))
	var stored ReferralModerationEvent
	require.NoError(t, db.Where("request_id = ?", ban.RequestId).First(&stored).Error)
	require.Equal(t, ban.Evidence, stored.Evidence)
	var lengths struct {
		Characters int
		Bytes      int
	}
	require.NoError(t, db.Raw("SELECT char_length(evidence) AS characters, octet_length(evidence) AS bytes FROM referral_moderation_events WHERE request_id = ?", ban.RequestId).Scan(&lengths).Error)
	require.Equal(t, 1000, lengths.Characters)
	require.Equal(t, 3000, lengths.Bytes)
	require.Equal(t, -200_000, referralUser(t, db, inviter.Id).AffQuota)
	appeal := ReferralModerationEvent{RequestId: "pg-unicode-appeal", ActorId: actor.Id, UserId: invitee.Id,
		Action: "restore_referral", Reason: "mistaken_ban", Evidence: strings.Repeat("🙂", 1000)}
	require.NoError(t, ModerateReferralUser(appeal))
	require.NoError(t, ModerateReferralUser(appeal))
	// Even an old ban replay after the Unicode appeal may not debit again.
	require.NoError(t, ModerateReferralUser(ban))
	require.Equal(t, common.UserStatusEnabled, referralUser(t, db, invitee.Id).Status)
	require.Equal(t, 1_000_000, referralUser(t, db, inviter.Id).AffQuota)
	require.Equal(t, 777, referralUser(t, db, inviter.Id).Quota)
	require.NoError(t, db.Model(&ReferralModerationEvent{}).Count(&count).Error)
	require.EqualValues(t, 2, count)
	require.NoError(t, db.Model(&ReferralLedgerEntry{}).Count(&count).Error)
	require.EqualValues(t, 5, count)
}
