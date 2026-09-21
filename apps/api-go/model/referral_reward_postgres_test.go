package model

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Reuse the opt-in, randomly named PostgreSQL schema fixture. Never target
// application tables, and do not parallelize tests that replace model globals.
func setupReferralPostgresTest(t *testing.T) (*gorm.DB, User, User, User, TopUp, ExternalTopUpSettlement) {
	t.Helper()
	usePostgresDatabaseType(t)
	db := openIsolatedPostgresCacheTestDB(t, &User{}, &TopUp{}, &ReferralReward{},
		&ReferralLedgerEntry{}, &ReferralModerationEvent{}, &UserSession{}, &FinanceLedgerEntry{}, &Token{})
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)
	db = db.WithContext(ctx)
	previousDB, previousLogDB := DB, LOG_DB
	previousRedis, previousOptions := common.RedisEnabled, common.OptionMap
	DB, LOG_DB = db, db
	common.RedisEnabled = false
	common.OptionMap = map[string]string{}
	t.Cleanup(func() {
		DB, LOG_DB = previousDB, previousLogDB
		common.RedisEnabled, common.OptionMap = previousRedis, previousOptions
	})
	preserveRegistrationRewardSettings(t)
	common.QuotaForInviter = 1_000_000
	actor := User{Username: "pg-operator", AffCode: "pg-operator", Role: common.RoleRootUser, Status: common.UserStatusEnabled}
	inviter := User{Username: "pg-inviter", AffCode: "pg-inviter", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Quota: 777}
	require.NoError(t, db.Create(&actor).Error)
	require.NoError(t, db.Create(&inviter).Error)
	invitee, order, payment := createSettlementFixture(t, db, "pg")
	require.NoError(t, db.Model(&User{}).Where("id = ?", invitee.Id).Update("inviter_id", inviter.Id).Error)
	return db, actor, inviter, invitee, order, payment
}

func TestReferralPostgresConcurrentFirstPayments(t *testing.T) {
	db, _, inviter, invitee, order, payment := setupReferralPostgresTest(t)
	second := nextReferralPayment(t, db, order, payment, "pg-second")
	start := make(chan struct{})
	results := make(chan error, 4)
	var workers sync.WaitGroup
	// Both distinct orders and duplicate callbacks contend on the payer row.
	for _, event := range []ExternalTopUpSettlement{payment, second, payment, second} {
		workers.Add(1)
		go func(event ExternalTopUpSettlement) {
			defer workers.Done()
			<-start
			_, err := completeExternalTopUpOnDB(db.Session(&gorm.Session{NewDB: true}), event)
			results <- err
		}(event)
	}
	close(start)
	workers.Wait()
	close(results)
	for err := range results {
		require.NoError(t, err)
	}
	require.Equal(t, invitee.Quota+2*int(order.CreditedQuota), referralUser(t, db, invitee.Id).Quota)
	require.Equal(t, 1_000_000, referralUser(t, db, inviter.Id).AffQuota)
	var rewards, entries int64
	require.NoError(t, db.Model(&ReferralReward{}).Count(&rewards).Error)
	require.NoError(t, db.Model(&ReferralLedgerEntry{}).Count(&entries).Error)
	require.EqualValues(t, 1, rewards)
	require.EqualValues(t, 1, entries)
}

func TestReferralPostgresConcurrentModerationAndAppeal(t *testing.T) {
	db, actor, inviter, invitee, _, payment := setupReferralPostgresTest(t)
	_, err := CompleteExternalTopUp(payment)
	require.NoError(t, err)
	ban := referralBan(actor, invitee, "pg-ban", true)
	start := make(chan struct{})
	results := make(chan error, 6)
	var workers sync.WaitGroup
	for i := 0; i < cap(results); i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			results <- ModerateReferralUser(ban)
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	for err := range results {
		require.NoError(t, err)
	}
	require.Equal(t, -200_000, referralUser(t, db, inviter.Id).AffQuota)
	require.Equal(t, 777, referralUser(t, db, inviter.Id).Quota)
	var events, entries int64
	require.NoError(t, db.Model(&ReferralModerationEvent{}).Count(&events).Error)
	require.NoError(t, db.Model(&ReferralLedgerEntry{}).Count(&entries).Error)
	require.EqualValues(t, 1, events)
	require.EqualValues(t, 3, entries)
	require.NoError(t, ModerateReferralUser(ReferralModerationEvent{
		RequestId: "pg-appeal", ActorId: actor.Id, UserId: invitee.Id,
		Action: "restore_referral", Reason: "mistaken_ban", Evidence: "appeal verified",
	}))
	restored := referralUser(t, db, invitee.Id)
	fresh := newTestUserSession("pg-after-appeal", invitee.Id, common.GetTimestamp())
	fresh.UserAuthVersion = restored.AuthVersion
	require.NoError(t, db.Create(fresh).Error)
	require.NoError(t, ModerateReferralUser(ban))
	require.NoError(t, db.First(fresh, "sid = ?", fresh.SID).Error)
	require.Equal(t, UserSessionStatusActive, fresh.Status)
	require.Equal(t, common.UserStatusEnabled, referralUser(t, db, invitee.Id).Status)
	require.Equal(t, restored.AuthVersion, referralUser(t, db, invitee.Id).AuthVersion)
	require.Equal(t, 1_000_000, referralUser(t, db, inviter.Id).AffQuota)
	require.NoError(t, db.Model(&ReferralLedgerEntry{}).Count(&entries).Error)
	require.EqualValues(t, 5, entries)
}

func TestReferralPostgresLedgerFailureRollsBackPayment(t *testing.T) {
	db, _, inviter, invitee, order, payment := setupReferralPostgresTest(t)
	injected := errors.New("PostgreSQL referral ledger failure")
	callback := "test:postgres_referral_ledger_failure"
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "referral_ledger_entries" {
			tx.AddError(injected)
		}
	}))
	t.Cleanup(func() { _ = db.Callback().Create().Remove(callback) })
	_, err := CompleteExternalTopUp(payment)
	require.ErrorIs(t, err, injected)
	stored := referralUser(t, db, invitee.Id)
	require.Zero(t, stored.ReferralFirstTopUpId)
	require.Equal(t, invitee.Quota, stored.Quota)
	require.Zero(t, referralUser(t, db, inviter.Id).AffQuota)
	var pending TopUp
	require.NoError(t, db.First(&pending, order.Id).Error)
	require.Equal(t, common.TopUpStatusPending, pending.Status)
	var entries int64
	require.NoError(t, db.Model(&ReferralLedgerEntry{}).Count(&entries).Error)
	require.Zero(t, entries)
	require.NoError(t, db.Callback().Create().Remove(callback))
	_, err = CompleteExternalTopUp(payment)
	require.NoError(t, err)
	require.Equal(t, 1_000_000, referralUser(t, db, inviter.Id).AffQuota)
}
