package model

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func TestAdminInvalidateLocksUserBeforeSubscriptionPostgres(t *testing.T) {
	db := setupSubscriptionLockOrderPostgres(t, time.Now().Add(time.Hour).Unix())

	assertSubscriptionMutationWaitsForUser(t, db, func() error {
		_, err := AdminInvalidateUserSubscription(9101)
		return err
	})

	var subscription UserSubscription
	require.NoError(t, db.First(&subscription, 9101).Error)
	require.Equal(t, "cancelled", subscription.Status)
	var group string
	require.NoError(t, db.Model(&User{}).Where("id = ?", 9001).Select("group").Scan(&group).Error)
	require.Equal(t, "default", group)
}

func TestExpireDueSubscriptionsLocksUserBeforeSubscriptionPostgres(t *testing.T) {
	db := setupSubscriptionLockOrderPostgres(t, time.Now().Add(-time.Hour).Unix())

	assertSubscriptionMutationWaitsForUser(t, db, func() error {
		count, err := ExpireDueSubscriptionsContext(context.Background(), 200)
		if err != nil {
			return err
		}
		if count != 1 {
			return fmt.Errorf("expired %d subscriptions, want 1", count)
		}
		return nil
	})

	var subscription UserSubscription
	require.NoError(t, db.First(&subscription, 9101).Error)
	require.Equal(t, "expired", subscription.Status)
	var group string
	require.NoError(t, db.Model(&User{}).Where("id = ?", 9001).Select("group").Scan(&group).Error)
	require.Equal(t, "default", group)
}

func setupSubscriptionLockOrderPostgres(t *testing.T, endTime int64) *gorm.DB {
	t.Helper()
	if strings.TrimSpace(os.Getenv("TEST_POSTGRES_DSN")) == "" {
		t.Skip("set TEST_POSTGRES_DSN to run PostgreSQL subscription lock-order tests")
	}
	if os.Getenv("TEST_POSTGRES_ISOLATED_SCHEMA") != "1" {
		t.Skip("set TEST_POSTGRES_ISOLATED_SCHEMA=1 to acknowledge isolated test-schema creation")
	}

	previousDB, previousLogDB := DB, LOG_DB
	db := openIsolatedPostgresCacheTestDB(t, &User{}, &UserSubscription{})
	DB, LOG_DB = db, db
	usePostgresDatabaseType(t)
	t.Cleanup(func() { DB, LOG_DB = previousDB, previousLogDB })

	require.NoError(t, db.Create(&User{Id: 9001, Username: "lock-order", Group: "pro"}).Error)
	require.NoError(t, db.Create(&UserSubscription{
		Id:             9101,
		UserId:         9001,
		PlanId:         1,
		Status:         "active",
		AmountTotal:    100,
		EndTime:        endTime,
		UpgradeGroup:   "pro",
		PrevUserGroup:  "default",
		DowngradeGroup: "default",
	}).Error)
	return db
}

func assertSubscriptionMutationWaitsForUser(t *testing.T, db *gorm.DB, mutate func() error) {
	t.Helper()
	blocker := db.Begin()
	require.NoError(t, blocker.Error)
	blockerReleased := false
	t.Cleanup(func() {
		if !blockerReleased {
			_ = blocker.Rollback().Error
		}
	})
	var blockerPID int
	require.NoError(t, blocker.Raw("SELECT pg_backend_pid()").Scan(&blockerPID).Error)
	require.NoError(t, blocker.Clauses(clause.Locking{Strength: "UPDATE"}).
		Select("id").Where("id = ?", 9001).First(&User{}).Error)

	result := make(chan error, 1)
	go func() { result <- mutate() }()
	waitForPostgresBlocker(t, db, blockerPID)

	probe := db.Begin()
	require.NoError(t, probe.Error)
	var subscriptionID int
	probeError := probe.Raw(
		"SELECT id FROM user_subscriptions WHERE id = ? FOR UPDATE NOWAIT",
		9101,
	).Scan(&subscriptionID).Error
	_ = probe.Rollback().Error
	require.NoError(t, probeError, "subscription row was locked before the user row")
	require.Equal(t, 9101, subscriptionID)

	require.NoError(t, blocker.Rollback().Error)
	blockerReleased = true
	select {
	case err := <-result:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("subscription mutation did not resume after releasing the user lock")
	}
}

func waitForPostgresBlocker(t *testing.T, db *gorm.DB, blockerPID int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		var blocked bool
		err := db.WithContext(ctx).Raw(
			"SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE ? = ANY(pg_blocking_pids(pid)))",
			blockerPID,
		).Scan(&blocked).Error
		require.NoError(t, err)
		if blocked {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("subscription mutation did not block on the user row")
		case <-time.After(10 * time.Millisecond):
		}
	}
}
