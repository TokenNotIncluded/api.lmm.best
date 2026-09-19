package model

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
)

func TestAcquisitionActivityReplayDedupAndRetention(t *testing.T) {
	db := acquisitionDB(t)
	require.NoError(t, db.AutoMigrate(&Log{}))
	oldLog, oldEnabled := LOG_DB, common.LogConsumeEnabled
	LOG_DB, common.LogConsumeEnabled = db, true
	t.Cleanup(func() { LOG_DB, common.LogConsumeEnabled = oldLog, oldEnabled })
	start := (time.Now().Unix()/86400 - 10) * 86400
	require.NoError(t, db.Create(&AcquisitionActivityState{ID: 1, StartedAt: start, ScannedThrough: start, Status: "starting"}).Error)
	user := User{Username: "activity", AffCode: "activity", CreatedAt: start + 1}
	require.NoError(t, db.Create(&user).Error)
	require.NoError(t, db.Create(&AcquisitionAccount{UserID: user.Id, ConsentVersion: 2, FirstVisitID: 1, RegistrationSource: "github", CreatedAt: start + 1}).Error)
	require.NoError(t, setAcquisitionConsent(db, user.Id, true))
	add := func(id string, at int64, other string) {
		require.NoError(t, db.Create(&Log{UserId: user.Id, RequestId: id, CreatedAt: at, Type: LogTypeConsume, TokenId: 2, Other: other}).Error)
	}
	add("numeric-marker", start+5, `{"acquisition_success_v1":1}`)
	add("string-marker", start+6, `{"acquisition_success_v1":"true"}`)
	add("original", start+20, `{"acquisition_success_v1":true}`)
	add("failed", start+21, `{"acquisition_success_v1":false}`)
	add("legacy", start+22, `{}`)
	add("nested", start+23, `{"untrusted":{"acquisition_success_v1":true}}`)
	require.NoError(t, ReconcileAcquisitionActivity(context.Background(), start+300))
	require.NoError(t, ReconcileAcquisitionActivity(context.Background(), start+360))
	var count int64
	require.NoError(t, db.Model(&AcquisitionActivity{}).Count(&count).Error)
	require.Equal(t, int64(1), count)
	var firstDay AcquisitionActivity
	require.NoError(t, db.First(&firstDay).Error)
	require.Equal(t, start+20, firstDay.FirstAt)
	// Repeating a request ID on another UTC day cannot manufacture retention.
	add("original", start+7*86400+30, `{"acquisition_success_v1":true}`)
	require.NoError(t, db.Model(&AcquisitionActivityState{}).Where("id=1").Update("scanned_through", start+7*86400).Error)
	require.NoError(t, ReconcileAcquisitionActivity(context.Background(), start+7*86400+300))
	require.NoError(t, db.Model(&AcquisitionActivity{}).Count(&count).Error)
	require.Equal(t, int64(1), count)
	add("return", start+7*86400+200, `{"acquisition_success_v1":true}`)
	require.NoError(t, ReconcileAcquisitionActivity(context.Background(), start+7*86400+300))
	_, rows, err := AcquisitionActivityReport(context.Background(), start, start+86400)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, int64(1), rows[0].ObservingAccounts)
	require.Nil(t, rows[0].RetentionRate)
	require.NoError(t, db.Model(&AcquisitionActivityState{}).Where("id=1").Update("scanned_through", start+8*86400).Error)
	_, rows, err = AcquisitionActivityReport(context.Background(), start, start+86400)
	require.NoError(t, err)
	require.Equal(t, int64(1), rows[0].MatureAccounts)
	require.Equal(t, int64(1), rows[0].RetainedAccounts)
	require.NotNil(t, rows[0].RetentionRate)
	require.Equal(t, float64(1), *rows[0].RetentionRate)
	common.LogConsumeEnabled = false
	require.NoError(t, ReconcileAcquisitionActivity(context.Background(), start+9*86400))
	state, rows, err := AcquisitionActivityReport(context.Background(), start, start+86400)
	require.NoError(t, err)
	require.True(t, state.Incomplete)
	require.Nil(t, rows[0].RetentionRate)
	require.NoError(t, RebuildAcquisitionActivity(context.Background()))
	require.NoError(t, db.First(state, 1).Error)
	require.Equal(t, start, state.ScannedThrough)
}

func TestAcquisitionActivityNeedsExpandedConsent(t *testing.T) {
	db := acquisitionDB(t)
	require.NoError(t, db.AutoMigrate(&Log{}))
	oldLog, oldEnabled := LOG_DB, common.LogConsumeEnabled
	LOG_DB, common.LogConsumeEnabled = db, true
	t.Cleanup(func() { LOG_DB, common.LogConsumeEnabled = oldLog, oldEnabled })
	start := time.Now().Unix() - 300
	require.NoError(t, db.Create(&AcquisitionActivityState{ID: 1, StartedAt: start, ScannedThrough: start, Status: "starting"}).Error)
	user := User{Username: "legacy-consent", AffCode: "legacy-consent", CreatedAt: start + 1}
	require.NoError(t, db.Create(&user).Error)
	require.NoError(t, db.Create(&AcquisitionAccount{UserID: user.Id, ConsentVersion: 1, FirstVisitID: 1, RegistrationSource: "github", CreatedAt: start + 1}).Error)
	require.NoError(t, db.Create(&Log{UserId: user.Id, RequestId: "legacy-permission", CreatedAt: start + 20, Type: LogTypeConsume, TokenId: 2, Other: `{"acquisition_success_v1":true}`}).Error)
	require.NoError(t, ReconcileAcquisitionActivity(context.Background(), start+300))
	var count int64
	require.NoError(t, db.Model(&AcquisitionActivity{}).Count(&count).Error)
	require.Zero(t, count)
	// Expanded consent permits replay of existing operational records, while
	// source attribution itself stays immutable.
	visitor := AcquisitionVisitorHash("upgrade")
	require.NoError(t, db.Create(&AcquisitionVisitor{ID: visitor, UserID: user.Id, CreatedAt: start}).Error)
	require.NoError(t, GrantAcquisitionConsent(context.Background(), user.Id))
	_, err := ObserveAcquisition(context.Background(), visitor, user.Id, AcquisitionInput{Consent: true, ConsentVersion: 2, Nonce: "0123456789abcdef0123456789abcdef", Landing: "/", Source: "new-source"}, nil)
	require.NoError(t, err)
	var account AcquisitionAccount
	require.NoError(t, db.First(&account, user.Id).Error)
	require.Equal(t, 2, account.ConsentVersion)
	require.Equal(t, "github", account.RegistrationSource)
	require.NoError(t, RebuildAcquisitionActivity(context.Background()))
	require.NoError(t, ReconcileAcquisitionActivity(context.Background(), start+300))
	require.NoError(t, db.Model(&AcquisitionActivity{}).Count(&count).Error)
	require.Equal(t, int64(1), count)
	require.NoError(t, RevokeAcquisitionAccount(context.Background(), user.Id))
	_, err = ObserveAcquisition(context.Background(), visitor, user.Id, AcquisitionInput{Consent: true, ConsentVersion: 2, Nonce: strings.Repeat("f", 32), Landing: "/"}, nil)
	require.Error(t, err)
	require.NoError(t, db.Model(&AcquisitionActivity{}).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, ReconcileAcquisitionActivity(context.Background(), start+360))
	require.NoError(t, db.Model(&AcquisitionActivity{}).Count(&count).Error)
	require.Zero(t, count)
}

func TestAcquisitionRetentionExcludesGapAffectedAccountsButRecoversForLaterCohorts(t *testing.T) {
	db := acquisitionDB(t)
	start := (time.Now().Unix()/86400 - 30) * 86400
	require.NoError(t, db.Create(&AcquisitionActivityState{ID: 1, StartedAt: start, ScannedThrough: start + 25*86400, Status: "ready", Incomplete: true, GapFrom: start + 10*86400, GapThrough: start + 21*86400}).Error)
	require.NoError(t, db.Create(&[]AcquisitionActivityGap{{FromAt: start + 10*86400, ThroughAt: start + 11*86400}, {FromAt: start + 20*86400, ThroughAt: start + 21*86400}}).Error)
	for i, day := range []int64{0, 8, 12} {
		user := User{Username: fmt.Sprintf("gap-%d", i), AffCode: fmt.Sprintf("gap-%d", i), CreatedAt: start + day*86400 + 1}
		require.NoError(t, db.Create(&user).Error)
		require.NoError(t, db.Create(&AcquisitionAccount{UserID: user.Id, ConsentVersion: 2, RegistrationSource: "github", CreatedAt: user.CreatedAt}).Error)
		require.NoError(t, setAcquisitionConsent(db, user.Id, true))
		for _, offset := range []int64{0, 7} {
			at := start + (day+offset)*86400
			require.NoError(t, db.Create(&AcquisitionActivity{UserID: user.Id, Day: at, FirstAt: at + 10, LastAt: at + 10}).Error)
		}
	}
	_, rows, err := AcquisitionActivityReport(context.Background(), start, start+20*86400)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, int64(3), rows[0].SuccessfulAccounts)
	require.Equal(t, int64(1), rows[0].IncompleteAccounts)
	require.Equal(t, int64(2), rows[0].MatureAccounts)
	require.NotNil(t, rows[0].RetentionRate)
	require.Equal(t, 1.0, *rows[0].RetentionRate)
}

func TestAcquisitionDetectsMissingReceiptBetweenObserverTicks(t *testing.T) {
	db := acquisitionDB(t)
	require.NoError(t, db.AutoMigrate(&Log{}))
	oldLog, oldEnabled := LOG_DB, common.LogConsumeEnabled
	LOG_DB = db
	oldFrom, oldThrough := acquisitionPendingGapFrom.Load(), acquisitionPendingGapThrough.Load()
	t.Cleanup(func() {
		LOG_DB, common.LogConsumeEnabled = oldLog, oldEnabled
		acquisitionPendingGapFrom.Store(oldFrom)
		acquisitionPendingGapThrough.Store(oldThrough)
	})
	acquisitionPendingGapFrom.Store(0)
	acquisitionPendingGapThrough.Store(0)
	now := time.Now().Unix()
	require.NoError(t, db.Create(&AcquisitionActivityState{ID: 1, StartedAt: now - 300, ScannedThrough: now - 300}).Error)
	common.LogConsumeEnabled = false
	RecordConsumeLog(nil, 1, RecordConsumeLogParams{Other: map[string]interface{}{"acquisition_success_v1": true}})
	common.LogConsumeEnabled = true
	require.NoError(t, ReconcileAcquisitionActivity(context.Background(), now+120))
	var state AcquisitionActivityState
	require.NoError(t, db.First(&state, 1).Error)
	require.True(t, state.Incomplete)
	require.GreaterOrEqual(t, state.GapFrom, now)
	require.Greater(t, state.GapThrough, state.GapFrom)
}
