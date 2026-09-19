package model

import (
	"context"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func acquisitionFunnelDB(t *testing.T) *gorm.DB {
	db := acquisitionDB(t)
	require.NoError(t, db.AutoMigrate(&Token{}, &DeveloperAccessRequest{}, &OAuthServerGrant{}, &L1OnboardingTodo{}))
	return db
}
func funnelUser(t *testing.T, db *gorm.DB, name string, at int64) User {
	user := User{Username: name, AffCode: name, CreatedAt: at, Role: common.RoleCommonUser}
	require.NoError(t, db.Create(&user).Error)
	require.NoError(t, db.Create(&AcquisitionAccount{UserID: user.Id, RegistrationSource: "github", RegistrationCampaign: "launch", RegistrationContent: "readme", CreatedAt: at, ConsentVersion: 2}).Error)
	require.NoError(t, db.Create(&AcquisitionConsent{UserID: user.Id, Allowed: true, Version: 2}).Error)
	return user
}
func funnelPayment(t *testing.T, db *gorm.DB, userID int, trade string, at int64) {
	require.NoError(t, db.Create(&TopUp{UserId: userID, TradeNo: trade, Status: common.TopUpStatusSuccess, SettledAmountMicros: 1000000, SettlementCurrency: "USD", PaymentProvider: "stripe", PaymentMethod: "stripe", CompleteTime: at}).Error)
}

func TestAcquisitionFirstPaymentSnapshotKeepsRuleAndEvidenceAfterCleanup(t *testing.T) {
	db := acquisitionFunnelDB(t)
	now := time.Now().Unix()
	ctx := context.Background()
	user := funnelUser(t, db, "snapshot", now-40*86400)
	require.NoError(t, db.Create(&AcquisitionConfig{ID: 1, StartedAt: now - 60*86400, LookbackDays: 30}).Error)
	require.NoError(t, db.Create(&AcquisitionAttributionPolicy{EffectiveAt: now - 50*86400, LookbackDays: 30}).Error)
	visitor := AcquisitionVisitor{ID: AcquisitionVisitorHash("snapshot-browser"), UserID: user.Id, CreatedAt: user.CreatedAt}
	require.NoError(t, db.Create(&visitor).Error)
	require.NoError(t, db.Create(&AcquisitionVisit{VisitorID: visitor.ID, Nonce: "source", Source: "forum", Evidence: "promotion_link", LinkID: "link", Campaign: "paid-campaign", ConsentVersion: 2, CreatedAt: now - 10*86400}).Error)
	require.NoError(t, db.Create(&AcquisitionVisit{VisitorID: visitor.ID, Nonce: "unknown", Source: "unknown", Evidence: "unavailable", ConsentVersion: 2, CreatedAt: now - 2*86400}).Error)
	funnelPayment(t, db, user.Id, "first", now-86400)
	require.NoError(t, ReconcileAcquisitionFirstPayments(ctx, now))
	var before AcquisitionFirstPayment
	require.NoError(t, db.First(&before, user.Id).Error)
	require.Equal(t, "forum", before.Source)
	require.True(t, before.Inferred)
	require.Equal(t, 30, before.LookbackDays)
	require.NoError(t, SetAcquisitionLookback(ctx, 1))
	require.NoError(t, db.Where("visitor_id = ?", visitor.ID).Delete(&AcquisitionVisit{}).Error)
	require.NoError(t, ReconcileAcquisitionFirstPayments(ctx, now+1))
	var after AcquisitionFirstPayment
	require.NoError(t, db.First(&after, user.Id).Error)
	require.Equal(t, before, after)
	require.NoError(t, RevokeAcquisitionAccount(ctx, user.Id))
	var count int64
	require.NoError(t, db.Model(&AcquisitionFirstPayment{}).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, ReconcileAcquisitionFirstPayments(ctx, now+2))
	require.NoError(t, db.Model(&AcquisitionFirstPayment{}).Count(&count).Error)
	require.Zero(t, count)
}

func TestAcquisitionPaymentBeforeRuleHistoryStaysUnknown(t *testing.T) {
	db := acquisitionFunnelDB(t)
	now := time.Now().Unix()
	user := funnelUser(t, db, "legacy-payment", now-86400)
	funnelPayment(t, db, user.Id, "before-rule", now-60)
	require.NoError(t, ReconcileAcquisitionFirstPayments(context.Background(), now))
	var snapshot AcquisitionFirstPayment
	require.NoError(t, db.First(&snapshot, user.Id).Error)
	require.Equal(t, "unknown", snapshot.Source)
	require.Equal(t, "rule_unrecorded", snapshot.Evidence)
}

func TestAcquisitionFunnelUsesFixedCohortWindowAndOAuthWithoutManualKey(t *testing.T) {
	db := acquisitionFunnelDB(t)
	now := time.Now().Unix()
	day := int64(86400)
	mature := funnelUser(t, db, "mature", now-40*day)
	young := funnelUser(t, db, "young", now-2*day)
	require.NoError(t, db.Create(&AcquisitionConfig{ID: 1, StartedAt: now - 50*day, LookbackDays: 30}).Error)
	require.NoError(t, db.Create(&AcquisitionActivityState{ID: 1, StartedAt: now - 50*day, ScannedThrough: now, UpdatedAt: now, Status: "ready"}).Error)
	require.NoError(t, db.Create(&OAuthServerGrant{ID: "grant", UserID: int64(mature.Id), CreatedAtMs: (mature.CreatedAt + day) * 1000}).Error)
	require.NoError(t, db.Create(&AcquisitionActivity{UserID: mature.Id, Day: mature.CreatedAt / day * day, FirstAt: mature.CreatedAt + 2*day, LastAt: mature.CreatedAt + 2*day}).Error)
	funnelPayment(t, db, mature.Id, "paid-before-config", mature.CreatedAt+day/2)
	funnelPayment(t, db, mature.Id, "repeat", mature.CreatedAt+3*day)
	funnelPayment(t, db, young.Id, "young-paid", now-day)
	// A later payment is outside this new user's 30-day cohort window.
	require.NoError(t, db.Create(&Token{UserId: young.Id, CreatedTime: now - day, OAuthManaged: false}).Error)
	report, err := getAcquisitionFunnelAt(context.Background(), now-45*day, now+1, 30, AcquisitionFunnelFilter{Source: "github", Campaign: "launch", Content: "readme"}, now)
	require.NoError(t, err)
	require.Len(t, report.Channels, 1)
	channel := report.Channels[0]
	require.EqualValues(t, 2, channel.Registrations)
	require.EqualValues(t, 1, channel.MatureAccounts)
	require.EqualValues(t, 1, channel.ObservingAccounts)
	stages := map[string]AcquisitionFunnelStage{}
	for _, stage := range channel.Stages {
		stages[stage.ID] = stage
	}
	require.EqualValues(t, 2, stages["credential_ready"].Observed)
	require.EqualValues(t, 1, stages["oauth_authorized"].Observed)
	require.EqualValues(t, 1, stages["api_key_created"].Observed)
	require.EqualValues(t, 1, stages["first_successful_request"].Observed)
	require.EqualValues(t, 1, stages["repeat_payment"].Observed)
	require.NotNil(t, stages["first_payment"].ConversionRate)
	require.Equal(t, 1.0, *stages["first_payment"].ConversionRate)
	require.EqualValues(t, 1, stages["first_payment"].TimingAccounts)
	require.Equal(t, float64(day/2), *stages["first_payment"].MeanSecondsFromRegistration)
	require.Nil(t, stages["access_approved"].ConversionRate)
	require.EqualValues(t, 2, stages["access_approved"].Unknown)
	filtered, err := getAcquisitionFunnelAt(context.Background(), now-45*day, now+1, 30, AcquisitionFunnelFilter{ConnectionMethod: "oauth"}, now)
	require.NoError(t, err)
	require.Len(t, filtered.Channels, 1)
	require.EqualValues(t, 1, filtered.Channels[0].Registrations)
}

func TestAcquisitionFunnelDoesNotCountPaymentAfterObservationWindow(t *testing.T) {
	db := acquisitionFunnelDB(t)
	now := time.Now().Unix()
	user := funnelUser(t, db, "late-conversion", now-40*86400)
	funnelPayment(t, db, user.Id, "after-thirty-days", user.CreatedAt+31*86400)
	report, err := getAcquisitionFunnelAt(context.Background(), user.CreatedAt, now, 30, AcquisitionFunnelFilter{}, now)
	require.NoError(t, err)
	for _, stage := range report.Channels[0].Stages {
		if stage.ID == "first_payment" {
			require.Zero(t, stage.Observed)
			require.EqualValues(t, 1, stage.NotObserved)
			require.NotNil(t, stage.ConversionRate)
			require.Zero(t, *stage.ConversionRate)
		}
	}
	require.Empty(t, report.FirstPaymentSources)
}

func TestAcquisitionFunnelDoesNotTurnLoggingGapsOrUnclassifiedCashIntoZero(t *testing.T) {
	db := acquisitionFunnelDB(t)
	now := time.Now().Unix()
	user := funnelUser(t, db, "gap", now-40*86400)
	require.NoError(t, db.Create(&AcquisitionActivityState{ID: 1, StartedAt: user.CreatedAt - 86400, ScannedThrough: now, Status: "ready"}).Error)
	require.NoError(t, db.Create(&AcquisitionActivityGap{FromAt: user.CreatedAt + 1, ThroughAt: user.CreatedAt + 2}).Error)
	require.NoError(t, db.Create(&TopUp{UserId: user.Id, TradeNo: "missing-settlement", Status: common.TopUpStatusSuccess, CompleteTime: user.CreatedAt + 3}).Error)
	report, err := getAcquisitionFunnelAt(context.Background(), user.CreatedAt, now, 30, AcquisitionFunnelFilter{}, now)
	require.NoError(t, err)
	for _, stage := range report.Channels[0].Stages {
		if stage.ID == "first_successful_request" || stage.ID == "first_payment" {
			require.EqualValues(t, 1, stage.Unknown)
			require.Zero(t, stage.NotObserved)
			require.Nil(t, stage.ConversionRate)
		}
	}
}

func TestAcquisitionMissingSettlementOrCompletionIsNotExpectedCash(t *testing.T) {
	for _, missing := range []string{"settlement", "completion"} {
		t.Run(missing, func(t *testing.T) {
			db := acquisitionFunnelDB(t)
			now := time.Now().Unix()
			user := funnelUser(t, db, "missing", now-40*86400)
			require.NoError(t, db.Create(&AcquisitionAttributionPolicy{EffectiveAt: user.CreatedAt - 1, LookbackDays: 30}).Error)
			payment := TopUp{UserId: user.Id, TradeNo: "unproven", Status: common.TopUpStatusSuccess, ExpectedAmountMicros: 9000000, SettledAmountMicros: 1000000, CreateTime: user.CreatedAt + 1, CompleteTime: user.CreatedAt + 2, SettlementCurrency: "USD", PaymentMethod: "stripe", PaymentProvider: "stripe"}
			if missing == "settlement" {
				payment.SettledAmountMicros = 0
			} else {
				payment.CompleteTime = 0
			}
			require.NoError(t, db.Create(&payment).Error)
			cash, err := GetAcquisitionReport(context.Background(), user.CreatedAt, now)
			require.NoError(t, err)
			require.Empty(t, cash.Payments)
			require.EqualValues(t, 1, cash.UnclassifiedPaymentRows)
			funnelPayment(t, db, user.Id, "later-proven", user.CreatedAt+3)
			require.NoError(t, ReconcileAcquisitionFirstPayments(context.Background(), now))
			var snapshot AcquisitionFirstPayment
			require.NoError(t, db.First(&snapshot, user.Id).Error)
			require.Zero(t, snapshot.FirstPaidAt)
			require.Equal(t, "payment_history_incomplete", snapshot.Evidence)
			report, err := getAcquisitionFunnelAt(context.Background(), user.CreatedAt, now, 30, AcquisitionFunnelFilter{}, now)
			require.NoError(t, err)
			for _, stage := range report.Channels[0].Stages {
				if stage.ID == "first_payment" || stage.ID == "repeat_payment" {
					require.EqualValues(t, 1, stage.Unknown)
					require.Nil(t, stage.ConversionRate)
				}
			}
		})
	}
}

func TestAcquisitionLateEarlierPaymentInvalidatesWithoutRewritingSnapshot(t *testing.T) {
	db := acquisitionFunnelDB(t)
	now := time.Now().Unix()
	user := funnelUser(t, db, "late-event", now-40*86400)
	require.NoError(t, db.Create(&AcquisitionAttributionPolicy{EffectiveAt: user.CreatedAt - 1, LookbackDays: 30}).Error)
	funnelPayment(t, db, user.Id, "originally-first", user.CreatedAt+100)
	require.NoError(t, ReconcileAcquisitionFirstPayments(context.Background(), now))
	var before AcquisitionFirstPayment
	require.NoError(t, db.First(&before, user.Id).Error)
	funnelPayment(t, db, user.Id, "arrived-late", user.CreatedAt+50)
	require.NoError(t, ReconcileAcquisitionFirstPayments(context.Background(), now+1))
	var after AcquisitionFirstPayment
	require.NoError(t, db.First(&after, user.Id).Error)
	require.Equal(t, before.FirstPaidAt, after.FirstPaidAt)
	require.Equal(t, before.Source, after.Source)
	require.Equal(t, now+1, after.HistoryConflictAt)
	require.Equal(t, user.CreatedAt+50, after.ConflictingPaidAt)
	report, err := getAcquisitionFunnelAt(context.Background(), user.CreatedAt, now, 30, AcquisitionFunnelFilter{}, now+1)
	require.NoError(t, err)
	require.Equal(t, "history_conflict", report.PaymentSnapshotStatus)
	require.Len(t, report.FirstPaymentSources, 1)
	require.Equal(t, "unknown", report.FirstPaymentSources[0].Source)
	require.Equal(t, "history_conflict", report.FirstPaymentSources[0].Evidence)
	require.Contains(t, report.Unavailable, "first_payment_history_conflict")
}

func TestAcquisitionOldAccountDoesNotAcquireFalseLifetimeFirstPayment(t *testing.T) {
	db := acquisitionFunnelDB(t)
	now := time.Now().Unix()
	user := funnelUser(t, db, "old-account", now-400*86400)
	require.NoError(t, db.Model(&AcquisitionAccount{}).Where("user_id = ?", user.Id).Update("created_at", now).Error)
	require.NoError(t, db.Create(&AcquisitionAttributionPolicy{EffectiveAt: now - 20*86400, LookbackDays: 30}).Error)
	funnelPayment(t, db, user.Id, "lifetime-first", now-380*86400)
	funnelPayment(t, db, user.Id, "recent-return", now-86400)
	require.NoError(t, ReconcileAcquisitionFirstPayments(context.Background(), now))
	var snapshot AcquisitionFirstPayment
	require.NoError(t, db.First(&snapshot, user.Id).Error)
	require.Zero(t, snapshot.FirstPaidAt)
	require.Equal(t, "historical_unrecorded", snapshot.Evidence)
}
