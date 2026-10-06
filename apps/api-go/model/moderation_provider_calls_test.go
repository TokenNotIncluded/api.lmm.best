package model

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestModerationProviderJournalFencesAttemptsAndRetainsPriorCallsAcrossRetry(t *testing.T) {
	db, user := setupModerationEffectsTestDB(t)
	job := claimModerationTestJob(t, moderationTestJob(user.Id, "journal", ModerationSourceRelayInput, "tolerant"), "worker")
	now := time.Now().Unix()
	first := ModerationProviderCall{Attempt: 1, BatchIndex: 1, ResponseID: "modr-first", RequestID: "req_first"}
	require.NoError(t, AppendModerationProviderCall(context.Background(), job.ID, "worker", first, now))
	require.NoError(t, AppendModerationProviderCall(context.Background(), job.ID, "worker", first, now), "the same batch receipt is idempotent")
	changed := first
	changed.ResponseID = "modr-other"
	require.ErrorIs(t, AppendModerationProviderCall(context.Background(), job.ID, "worker", changed, now), ErrModerationJobInvalid)
	require.ErrorIs(t, AppendModerationProviderCall(context.Background(), job.ID, "wrong-owner", first, now), ErrModerationLeaseLost)
	wrongAttempt := first
	wrongAttempt.Attempt = 2
	require.ErrorIs(t, AppendModerationProviderCall(context.Background(), job.ID, "worker", wrongAttempt, now), ErrModerationLeaseLost)
	require.ErrorIs(t, AppendModerationProviderCall(context.Background(), job.ID, "worker", first, job.LeaseUntil), ErrModerationLeaseLost)
	require.NoError(t, RetryModerationJob(context.Background(), job.ID, "worker", now, now, "provider unavailable"))
	claimed, err := ClaimModerationJob(context.Background(), "new-worker", now, 60)
	require.NoError(t, err)
	require.NotNil(t, claimed)
	require.Equal(t, 2, claimed.Attempts)
	second := ModerationProviderCall{Attempt: 2, BatchIndex: 1, ResponseID: "modr-first", RequestID: ""}
	require.NoError(t, AppendModerationProviderCall(context.Background(), job.ID, "new-worker", second, now))
	require.ErrorIs(t, AppendModerationProviderCall(context.Background(), job.ID, "worker", first, now), ErrModerationLeaseLost)
	require.NoError(t, CancelModerationJob(context.Background(), job.ID, "new-worker", "review disabled"))
	var stored ModerationJob
	require.NoError(t, db.First(&stored, job.ID).Error)
	require.Equal(t, []ModerationProviderCall{first, second}, stored.ProviderCalls())
	require.Empty(t, stored.Payload)
	require.ErrorIs(t, AppendModerationProviderCall(context.Background(), job.ID, "new-worker", second, now), ErrModerationLeaseLost)
}

func TestModerationProviderJournalRejectsSecretShapedAndOversizedIdentifiers(t *testing.T) {
	for _, id := range []string{"sk-secret", "sk_abcdefghijklmnopqrstuvwx", "rk-secret", "Bearer-secret", "AIza-secret", "alice@example.test", "https://example.test", "req\nheader", strings.Repeat("a", 129), "请求"} {
		require.Empty(t, ModerationProviderIdentifier(id), id)
	}
	for _, id := range []string{"modr-f9f42", "req_AbC-123", strings.Repeat("a", 128)} {
		require.Equal(t, id, ModerationProviderIdentifier(id))
	}
	job := ModerationJob{ProviderCallsJSON: `[{"attempt":1,"batch_index":1,"response_id":"sk-secret","request_id":"req_safe"}]`}
	require.Empty(t, job.ProviderCalls())
	job.ProviderCallsJSON = strings.Repeat(" ", moderationProviderJournalMaxBytes+1)
	require.Empty(t, job.ProviderCalls())
	require.Empty(t, ModerationSubjectIdentifier(strings.Repeat("G", 64)))
	require.Equal(t, strings.Repeat("a", 64), ModerationSubjectIdentifier(strings.Repeat("a", 64)))
}

func TestModerationProviderJournalStaleClockCannotWriteOrAcknowledgeAnExpiredLease(t *testing.T) {
	db, user := setupModerationEffectsTestDB(t)
	job := claimModerationTestJob(t, moderationTestJob(user.Id, "expired-journal", ModerationSourceRelayInput, "tolerant"), "worker")
	now := time.Now().Unix()
	call := ModerationProviderCall{Attempt: 1, BatchIndex: 1, ResponseID: "modr-first"}
	require.NoError(t, AppendModerationProviderCall(context.Background(), job.ID, "worker", call, now))
	require.NoError(t, db.Model(job).Update("lease_until", now-1).Error)
	require.ErrorIs(t, AppendModerationProviderCall(context.Background(), job.ID, "worker", call, now-10), ErrModerationLeaseLost, "an existing receipt must not return a stale-clock success")
	newCall := ModerationProviderCall{Attempt: 1, BatchIndex: 2, ResponseID: "modr-late"}
	require.ErrorIs(t, AppendModerationProviderCall(context.Background(), job.ID, "worker", newCall, now-10), ErrModerationLeaseLost)
	var stored ModerationJob
	require.NoError(t, db.First(&stored, job.ID).Error)
	require.Equal(t, []ModerationProviderCall{call}, stored.ProviderCalls())
}

func TestModerationProviderJournalLockWaitCrossingLeaseDeadlineRejectsReceipt(t *testing.T) {
	db, user := setupModerationEffectsTestDB(t)
	job := claimModerationTestJob(t, moderationTestJob(user.Id, "wait-journal", ModerationSourceRelayInput, "tolerant"), "worker")
	deadline := time.Now().Unix() + 1
	require.NoError(t, db.Model(job).Update("lease_until", deadline).Error)
	blocked := false
	require.NoError(t, db.Callback().Query().After("gorm:query").Register("test:journal-lock-wait", func(tx *gorm.DB) {
		if !blocked && tx.Statement.Table == "moderation_jobs" {
			blocked = true
			// Model the SELECT returning its row only after a lock wait has
			// consumed the remaining lease. No caller-time-only fence suffices.
			time.Sleep(time.Until(time.Unix(deadline, 0)) + 5*time.Millisecond)
		}
	}))
	t.Cleanup(func() { require.NoError(t, db.Callback().Query().Remove("test:journal-lock-wait")) })
	call := ModerationProviderCall{Attempt: 1, BatchIndex: 1, ResponseID: "modr-late"}
	require.ErrorIs(t, AppendModerationProviderCall(context.Background(), job.ID, "worker", call, deadline-1), ErrModerationLeaseLost)
	require.True(t, blocked)
	var stored ModerationJob
	require.NoError(t, db.First(&stored, job.ID).Error)
	require.Empty(t, stored.ProviderCalls())
}

func TestModerationProviderJournalPostgresLockWaitCrossesLeaseDeadline(t *testing.T) {
	db := openIsolatedPostgresCacheTestDB(t, &User{}, &ModerationJob{})
	previousDB := DB
	DB = db
	t.Cleanup(func() { DB = previousDB })
	usePostgresDatabaseType(t)
	user := User{Username: "journal-pg", AffCode: "journal-pg", Group: "default", Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&user).Error)
	job := claimModerationTestJob(t, moderationTestJob(user.Id, "journal-pg", ModerationSourceRelayInput, "tolerant"), "worker")
	deadline := time.Now().Unix() + 2
	require.NoError(t, db.Model(job).Update("lease_until", deadline).Error)
	blocker := db.Begin()
	require.NoError(t, blocker.Error)
	defer blocker.Rollback()
	var locked ModerationJob
	require.NoError(t, lockForUpdate(blocker).Where("id = ?", job.ID).First(&locked).Error)
	queryStarted := make(chan struct{})
	var once sync.Once
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("test:pg-journal-lock-wait", func(tx *gorm.DB) {
		if tx.Statement.Table == "moderation_jobs" {
			once.Do(func() { close(queryStarted) })
		}
	}))
	t.Cleanup(func() { require.NoError(t, db.Callback().Query().Remove("test:pg-journal-lock-wait")) })
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- AppendModerationProviderCall(ctx, job.ID, "worker", ModerationProviderCall{Attempt: 1, BatchIndex: 1, ResponseID: "modr-late"}, deadline-2)
	}()
	select {
	case <-queryStarted:
	case <-ctx.Done():
		t.Fatal("provider receipt did not reach the locked row")
	}
	time.Sleep(time.Until(time.Unix(deadline, 0)) + 10*time.Millisecond)
	require.NoError(t, blocker.Commit().Error)
	select {
	case err := <-result:
		require.ErrorIs(t, err, ErrModerationLeaseLost)
	case <-ctx.Done():
		t.Fatal("provider receipt remained blocked after the row lock was released")
	}
	var stored ModerationJob
	require.NoError(t, db.First(&stored, job.ID).Error)
	require.Empty(t, stored.ProviderCalls())
}

func TestModerationProviderTraceMigrationDoesNotFabricateHistoricalIdentifiers(t *testing.T) {
	db, user := setupModerationEffectsTestDB(t)
	require.NoError(t, db.Migrator().DropColumn(&ModerationJob{}, "SubjectIdentifier"))
	require.NoError(t, db.Migrator().DropColumn(&ModerationJob{}, "ProviderCallsJSON"))
	old := moderationTestJob(user.Id, "historical", ModerationSourceRelayInput, "tolerant")
	old.EventKey, old.Status, old.Payload = "historical-migration", ModerationJobCompleted, ""
	require.NoError(t, db.Omit("subject_identifier", "provider_calls_json").Create(old).Error)
	require.NoError(t, db.AutoMigrate(&ModerationJob{}))
	var stored ModerationJob
	require.NoError(t, db.First(&stored, old.ID).Error)
	require.Equal(t, ModerationJobCompleted, stored.Status)
	require.Empty(t, stored.SubjectIdentifier)
	require.Equal(t, "[]", stored.ProviderCallsJSON)
	require.Empty(t, stored.ProviderCalls())
}
