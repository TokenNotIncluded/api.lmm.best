package model

import (
	"errors"
	"fmt"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestReferralRetryRepairsEachPostCommitFailureWithoutRepeatingLedger(t *testing.T) {
	for _, failure := range []string{"publish", "invalidate", "revoke"} {
		t.Run(failure, func(t *testing.T) {
			db, actor, inviter, invitee, _, payment := setupReferralTest(t)
			_, err := CompleteExternalTopUp(payment)
			require.NoError(t, err)
			invitee = referralUser(t, db, invitee.Id)
			old := newTestUserSession("retry-"+failure, invitee.Id, common.GetTimestamp())
			old.UserAuthVersion = invitee.AuthVersion
			require.NoError(t, db.Create(old).Error)
			injected := errors.New("post-commit failure")
			broken := true
			calls := map[string]int{}
			effects := referralModerationEffects{
				publish: func(id int) error {
					calls["publish"]++
					if broken && failure == "publish" {
						return injected
					}
					return PublishUserAuthCache(id)
				},
				invalidate: func(id int) error {
					calls["invalidate"]++
					if broken && failure == "invalidate" {
						return injected
					}
					return InvalidateUserTokensCache(id)
				},
				revoke: func(id int, version int64) (int64, error) {
					calls["revoke"]++
					if broken && failure == "revoke" {
						return 0, injected
					}
					return revokeReferralSessionsThroughVersion(id, version)
				},
			}
			ban := referralBan(actor, invitee, "retry-case-"+failure, true)
			require.ErrorIs(t, moderateReferralUserWithEffects(ban, effects), injected)
			committed := referralUser(t, db, invitee.Id)
			require.Equal(t, common.UserStatusDisabled, committed.Status)
			require.Equal(t, -200_000, referralUser(t, db, inviter.Id).AffQuota)
			var before int64
			require.NoError(t, db.Model(&ReferralLedgerEntry{}).Count(&before).Error)
			require.EqualValues(t, 3, before)
			broken = false
			require.NoError(t, moderateReferralUserWithEffects(ban, effects))
			require.Equal(t, 2, calls[failure], "failed effect must actually be retried")
			require.Equal(t, committed.AuthVersion, referralUser(t, db, invitee.Id).AuthVersion)
			require.Equal(t, -200_000, referralUser(t, db, inviter.Id).AffQuota)
			var after, events int64
			require.NoError(t, db.Model(&ReferralLedgerEntry{}).Count(&after).Error)
			require.Equal(t, before, after)
			require.NoError(t, db.Model(&ReferralModerationEvent{}).Count(&events).Error)
			require.EqualValues(t, 1, events)
			require.NoError(t, db.First(old, "sid = ?", old.SID).Error)
			require.Equal(t, UserSessionStatusRevoked, old.Status)
		})
	}
}

func TestReferralOldBanRetryDoesNotRevokePostAppealSession(t *testing.T) {
	db, actor, inviter, invitee, _, payment := setupReferralTest(t)
	_, err := CompleteExternalTopUp(payment)
	require.NoError(t, err)
	invitee = referralUser(t, db, invitee.Id)
	now := common.GetTimestamp()
	old := newTestUserSession("before-referral-ban", invitee.Id, now)
	old.UserAuthVersion = invitee.AuthVersion
	require.NoError(t, db.Create(old).Error)
	injected := errors.New("session cleanup unavailable")
	ban := referralBan(actor, invitee, "old-ban-retry", true)
	require.ErrorIs(t, moderateReferralUserWithEffects(ban, referralModerationEffects{
		publish: PublishUserAuthCache, invalidate: InvalidateUserTokensCache,
		revoke: func(int, int64) (int64, error) { return 0, injected },
	}), injected)
	require.NoError(t, ModerateReferralUser(ReferralModerationEvent{
		RequestId: "appeal-before-retry", ActorId: actor.Id, UserId: invitee.Id,
		Action: "restore_referral", Reason: "mistaken_ban", Evidence: "appeal upheld",
	}))
	restored := referralUser(t, db, invitee.Id)
	fresh := newTestUserSession("after-referral-appeal", invitee.Id, now)
	fresh.UserAuthVersion = restored.AuthVersion
	require.NoError(t, db.Create(fresh).Error)
	// Same-second creation deliberately proves the boundary is not a timestamp.
	require.Equal(t, old.CreatedAt, fresh.CreatedAt)
	require.NoError(t, ModerateReferralUser(ban))
	require.NoError(t, db.First(old, "sid = ?", old.SID).Error)
	require.NoError(t, db.First(fresh, "sid = ?", fresh.SID).Error)
	require.Equal(t, UserSessionStatusRevoked, old.Status)
	require.Equal(t, UserSessionStatusActive, fresh.Status)
	require.Equal(t, common.UserStatusEnabled, referralUser(t, db, invitee.Id).Status)
	require.Equal(t, restored.AuthVersion, referralUser(t, db, invitee.Id).AuthVersion)
	require.Equal(t, 1_000_000, referralUser(t, db, inviter.Id).AffQuota)
}

func TestReferralBoundedSessionCleanupResumesFailedBatch(t *testing.T) {
	db, _, _, invitee, _, _ := setupReferralTest(t)
	now := common.GetTimestamp()
	for i := 0; i < userSessionRevokeBatchSize+1; i++ {
		session := newTestUserSession(fmt.Sprintf("bounded-%04d", i), invitee.Id, now)
		require.NoError(t, db.Create(session).Error)
	}
	fresh := newTestUserSession("bounded-new-session", invitee.Id, now)
	fresh.UserAuthVersion = 3
	require.NoError(t, db.Create(fresh).Error)
	injected := errors.New("second cleanup batch failed")
	callback := "test:referral_cleanup_batch_failure"
	updates := 0
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "user_sessions" {
			updates++
			if updates == 2 {
				tx.AddError(injected)
			}
		}
	}))
	t.Cleanup(func() { _ = db.Callback().Update().Remove(callback) })
	count, err := revokeReferralSessionsThroughVersion(invitee.Id, 1)
	require.ErrorIs(t, err, injected)
	require.EqualValues(t, userSessionRevokeBatchSize, count)
	require.NoError(t, db.Callback().Update().Remove(callback))
	count, err = revokeReferralSessionsThroughVersion(invitee.Id, 1)
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
	require.NoError(t, db.First(fresh, "sid = ?", fresh.SID).Error)
	require.Equal(t, UserSessionStatusActive, fresh.Status)
	_, err = revokeReferralSessionsThroughVersion(invitee.Id, 0)
	require.ErrorIs(t, err, ErrUserSessionInvalid)
}
