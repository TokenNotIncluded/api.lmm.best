package model

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupOptionRefreshTest(t *testing.T) {
	t.Helper()
	setupPriceLockTest(t)
	previousAdvanced := currentAdvancedSecurityOptionValues()
	previousL1 := setting.GetAssistantL1AutoReviewSettings().OptionValues()
	previousModeration := setting.GetModerationSettings().OptionValues()
	optionUpdateMutex.Lock()
	previousDefaults := optionDefaultValues
	optionDefaultValues = map[string]string{"Notice": "", "DefaultOnly": "boot-default"}
	optionUpdateMutex.Unlock()
	t.Cleanup(func() {
		optionUpdateMutex.Lock()
		defer optionUpdateMutex.Unlock()
		optionDefaultValues = previousDefaults
		require.NoError(t, applyAdvancedSecurityOptionValues(previousAdvanced))
		require.NoError(t, applyAssistantL1AutoReviewOptionMap(previousL1))
		require.NoError(t, applyModerationOptionMap(previousModeration))
	})
}

func TestRefreshOptionsSnapshotReadsOtherInstanceCommitImmediately(t *testing.T) {
	setupOptionRefreshTest(t)
	require.NoError(t, DB.Create(&Option{Key: "Notice", Value: "committed on instance A"}).Error)
	common.OptionMapRWMutex.Lock()
	common.OptionMap["Notice"] = "old instance B cache"
	common.OptionMap["DeletedOnly"] = "stale removed option"
	common.OptionMapRWMutex.Unlock()

	snapshot, err := RefreshOptionsSnapshot(context.Background())
	require.NoError(t, err)
	require.Equal(t, "committed on instance A", snapshot["Notice"])
	require.Equal(t, "boot-default", snapshot["DefaultOnly"])
	require.NotContains(t, snapshot, "DeletedOnly")
	snapshot["Notice"] = "caller mutation"
	require.Equal(t, "committed on instance A", GetOptionsSnapshot()["Notice"])

	// A second process commits through the database without publishing into B.
	require.NoError(t, DB.Model(&Option{}).Where("key = ?", "Notice").Update("value", "next committed value").Error)
	require.Equal(t, "committed on instance A", GetOptionsSnapshot()["Notice"])
	snapshot, err = RefreshOptionsSnapshot(context.Background())
	require.NoError(t, err)
	require.Equal(t, "next committed value", snapshot["Notice"])
	require.Equal(t, "next committed value", GetOptionsSnapshot()["Notice"])

	require.NoError(t, DB.Where("key = ?", "Notice").Delete(&Option{}).Error)
	snapshot, err = RefreshOptionsSnapshot(context.Background())
	require.NoError(t, err)
	require.Equal(t, "", snapshot["Notice"], "a removed value restores its boot default")
}

func TestRefreshOptionsSnapshotFailureDoesNotPublishCachedSuccess(t *testing.T) {
	setupOptionRefreshTest(t)
	require.NoError(t, UpdateOption("Notice", "last valid state"))
	before := GetOptionsSnapshot()
	const callback = "test:option-refresh-query-failure"
	require.NoError(t, DB.Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
		tx.AddError(errors.New("injected database failure"))
	}))
	t.Cleanup(func() { _ = DB.Callback().Query().Remove(callback) })
	snapshot, err := RefreshOptionsSnapshot(context.Background())
	require.Error(t, err)
	require.Nil(t, snapshot, "failed authority reads cannot return stale values as success")
	require.Equal(t, before, GetOptionsSnapshot())

	require.NoError(t, DB.Callback().Query().Remove(callback))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	snapshot, err = RefreshOptionsSnapshot(ctx)
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, snapshot)
	require.Equal(t, before, GetOptionsSnapshot())
}

func TestRefreshOptionsSnapshotSerializesReadAndPublicationWithLocalWrites(t *testing.T) {
	setupOptionRefreshTest(t)
	require.NoError(t, UpdateOptionsBulk(map[string]string{"ModelPrice": `{"source":1}`, "ModelRatio": `{"source":2}`}))
	queryReady, releaseQuery := make(chan struct{}), make(chan struct{})
	var holdOnce sync.Once
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseQuery) }) }
	defer release()
	const callback = "test:option-refresh-query-pause"
	require.NoError(t, DB.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
		if _, ok := tx.Statement.Dest.(*[]*Option); ok {
			holdOnce.Do(func() {
				close(queryReady)
				<-releaseQuery
			})
		}
	}))
	defer func() { _ = DB.Callback().Query().Remove(callback) }()
	refreshDone := make(chan error, 1)
	go func() {
		_, err := RefreshOptionsSnapshot(context.Background())
		refreshDone <- err
	}()
	select {
	case <-queryReady:
	case <-time.After(5 * time.Second):
		t.Fatal("refresh did not reach its database query")
	}
	writeDone := make(chan error, 1)
	snapshotDone := make(chan map[string]string, 1)
	go func() {
		writeDone <- UpdateOptionsBulk(map[string]string{"ModelPrice": `{"source":3}`, "ModelRatio": `{"source":4}`})
	}()
	go func() { snapshotDone <- GetOptionsSnapshot() }()
	select {
	case <-writeDone:
		t.Fatal("a local write escaped the read/publication lock")
	case <-snapshotDone:
		t.Fatal("a reader observed a pending publication")
	case <-time.After(30 * time.Millisecond):
	}
	release()
	require.NoError(t, <-refreshDone)
	require.NoError(t, <-writeDone)
	observed := <-snapshotDone
	price, ratio := observed["ModelPrice"], observed["ModelRatio"]
	// Readers may precede or follow the queued writer, but cannot mix its batch.
	if price == `{"source":1}` {
		require.JSONEq(t, `{"source":2}`, ratio)
	} else {
		require.JSONEq(t, `{"source":3}`, price)
		require.JSONEq(t, `{"source":4}`, ratio)
	}
	final := GetOptionsSnapshot()
	require.JSONEq(t, `{"source":3}`, final["ModelPrice"])
	require.JSONEq(t, `{"source":4}`, final["ModelRatio"])
}

func TestRefreshOptionsSnapshotSerializesAdvancedSecurityWrites(t *testing.T) {
	setupOptionRefreshTest(t)
	oldRules := `{"version":1,"rules":[]}`
	newRules := `{"version":1,"rules":[{"id":"latest","enabled":true,"groups":["default"],"patterns":["blocked pattern"]}]}`
	require.NoError(t, UpdateAdvancedSecurityOptions(false, true, setting.AdvancedSecurityActionBlock, oldRules))
	queryReady, releaseQuery := make(chan struct{}), make(chan struct{})
	var holdOnce, releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseQuery) }) }
	defer release()
	const callback = "test:option-refresh-security-pause"
	require.NoError(t, DB.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
		if _, ok := tx.Statement.Dest.(*[]*Option); ok {
			holdOnce.Do(func() {
				close(queryReady)
				<-releaseQuery
			})
		}
	}))
	defer func() { _ = DB.Callback().Query().Remove(callback) }()
	refreshDone := make(chan map[string]string, 1)
	refreshError := make(chan error, 1)
	go func() {
		snapshot, err := RefreshOptionsSnapshot(context.Background())
		refreshDone <- snapshot
		refreshError <- err
	}()
	select {
	case <-queryReady:
	case <-time.After(5 * time.Second):
		t.Fatal("refresh did not reach its database query")
	}
	writeDone := make(chan error, 1)
	go func() {
		writeDone <- UpdateAdvancedSecurityOptions(true, false, setting.AdvancedSecurityActionAudit, newRules)
	}()
	select {
	case <-writeDone:
		t.Fatal("security write bypassed the option publication lock")
	case <-time.After(30 * time.Millisecond):
	}
	release()
	require.NoError(t, <-refreshError)
	require.Equal(t, "false", (<-refreshDone)[setting.AdvancedSecurityEnabledOptionKey])
	require.NoError(t, <-writeDone)
	current := setting.GetAdvancedSecuritySettings()
	require.True(t, current.Enabled)
	require.False(t, current.OnPrompt)
	require.Equal(t, setting.AdvancedSecurityActionAudit, current.Action)
	final := GetOptionsSnapshot()
	require.Equal(t, "true", final[setting.AdvancedSecurityEnabledOptionKey])
	require.JSONEq(t, newRules, final[setting.AdvancedSecurityRulesOptionKey])
}
