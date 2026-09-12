package model

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func setupL1AutoReviewPostgres(t *testing.T) (*gorm.DB, User, User, DeveloperAccessRequest, setting.AssistantL1AutoReviewSettings) {
	t.Helper()
	previousDB, previousLogDB := DB, LOG_DB
	db := openIsolatedPostgresCacheTestDB(t, l1AutoReviewTestModels...)
	DB, LOG_DB = db, db
	usePostgresDatabaseType(t)
	t.Cleanup(func() { DB, LOG_DB = previousDB, previousLogDB })
	root, user, request, config := seedL1AutoReviewTest(t)
	return db, root, user, request, config
}

func TestL1AutoReviewManualLockOrderPostgres(t *testing.T) {
	db, root, user, request, _ := setupL1AutoReviewPostgres(t)
	blocker := db.Begin()
	require.NoError(t, blocker.Error)
	t.Cleanup(func() { _ = blocker.Rollback().Error })
	var pid int
	require.NoError(t, blocker.Raw("SELECT pg_backend_pid()").Scan(&pid).Error)
	require.NoError(t, lockForUpdate(blocker).First(&User{}, user.Id).Error)
	result := make(chan error, 1)
	go func() {
		_, err := ReviewDeveloperAccessRequest(root.Id, request.Id, true, "Manual approval of the concrete project")
		result <- err
	}()
	waitForPostgresBlocker(t, db, pid)
	probe := db.Begin()
	require.NoError(t, probe.Error)
	probeErr := probe.Clauses(clause.Locking{Strength: "UPDATE", Options: "NOWAIT"}).First(&DeveloperAccessRequest{}, request.Id).Error
	_ = probe.Rollback().Error
	require.NoError(t, probeErr, "manual review must not lock a request before its user")
	require.NoError(t, blocker.Rollback().Error)
	select {
	case err := <-result:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("manual review did not resume after the user lock was released")
	}
}

func TestL1AutoReviewConcurrentRestrictionsPostgres(t *testing.T) {
	for _, kind := range []string{"user restriction", "letter edit", "remote config disable"} {
		t.Run(kind, func(t *testing.T) {
			db, root, user, request, config := setupL1AutoReviewPostgres(t)
			blocker := db.Begin()
			require.NoError(t, blocker.Error)
			t.Cleanup(func() { _ = blocker.Rollback().Error })
			var pid int
			require.NoError(t, blocker.Raw("SELECT pg_backend_pid()").Scan(&pid).Error)
			if kind == "remote config disable" {
				require.NoError(t, lockForUpdate(blocker).Where("key = ?", setting.AssistantL1AutoReviewEnabledOptionKey).First(&Option{}).Error)
			} else {
				require.NoError(t, lockForUpdate(blocker).First(&User{}, user.Id).Error)
			}
			result := make(chan error, 1)
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			go func() {
				_, err := ApplyDeveloperAccessAutoReview(ctx, root.Id, request, config, true, "Approval generated before the concurrent change")
				result <- err
			}()
			waitForPostgresBlocker(t, db, pid)
			switch kind {
			case "user restriction":
				require.NoError(t, blocker.Model(&user).Update("trust_level_override", 0).Error)
			case "letter edit":
				require.NoError(t, blocker.Model(&request).Updates(map[string]any{"reason": "Different API project", "revision": gorm.Expr("revision + 1")}).Error)
			case "remote config disable":
				require.NoError(t, blocker.Model(&Option{}).Where("key = ?", setting.AssistantL1AutoReviewEnabledOptionKey).Update("value", "false").Error)
			}
			require.NoError(t, blocker.Commit().Error)
			select {
			case err := <-result:
				require.Error(t, err)
			case <-ctx.Done():
				t.Fatal("automatic review did not resume after the concurrent change")
			}
			require.NoError(t, db.First(&user, user.Id).Error)
			require.Zero(t, user.ConsoleActivatedAt)
			require.NoError(t, db.First(&request, request.Id).Error)
			require.Equal(t, DeveloperAccessRequestPending, request.Status)
			require.Empty(t, request.AdminNote)
		})
	}
}

func TestL1AutoReviewRacesManualDecisionPostgres(t *testing.T) {
	db, root, user, request, config := setupL1AutoReviewPostgres(t)
	start := make(chan struct{})
	results := make(chan error, 2)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	var workers sync.WaitGroup
	workers.Go(func() {
		<-start
		_, err := ApplyDeveloperAccessAutoReview(ctx, root.Id, request, config, true, "Automatic review reply")
		results <- err
	})
	workers.Go(func() {
		<-start
		_, err := ReviewDeveloperAccessRequest(root.Id, request.Id, true, "Manual review reply")
		results <- err
	})
	close(start)
	workers.Wait()
	close(results)
	accepted := 0
	for err := range results {
		if err == nil {
			accepted++
		} else {
			require.ErrorIs(t, err, ErrDeveloperAccessRequestReviewed)
		}
	}
	require.Equal(t, 1, accepted)
	require.NoError(t, db.First(&user, user.Id).Error)
	require.Positive(t, user.ConsoleActivatedAt)
	require.NoError(t, db.First(&request, request.Id).Error)
	require.Equal(t, DeveloperAccessRequestApproved, request.Status)
	var archives int64
	require.NoError(t, db.Model(&DeveloperAccessRecommendationArchive{}).Where("user_id = ?", user.Id).Count(&archives).Error)
	require.EqualValues(t, 1, archives)
}
