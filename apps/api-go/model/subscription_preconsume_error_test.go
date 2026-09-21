package model

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupSubscriptionPreConsumeErrorTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	previousDB := DB
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&SubscriptionPlan{}, &UserSubscription{}, &SubscriptionPreConsumeRecord{}))
	DB = db
	t.Cleanup(func() {
		DB = previousDB
		sqlDB, sqlErr := db.DB()
		if sqlErr == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func TestPreConsumeUserSubscriptionReturnsTypedNoActiveError(t *testing.T) {
	setupSubscriptionPreConsumeErrorTestDB(t)

	_, err := PreConsumeUserSubscription("request-no-active", 1001, "gpt-test", 0, 1)

	require.ErrorIs(t, err, ErrNoActiveSubscription)
	require.NotErrorIs(t, err, ErrSubscriptionQuotaInsufficient)
}

func TestPreConsumeUserSubscriptionReturnsTypedInsufficientQuotaError(t *testing.T) {
	db := setupSubscriptionPreConsumeErrorTestDB(t)
	plan := SubscriptionPlan{
		Title:            "limited",
		DurationUnit:     SubscriptionDurationDay,
		DurationValue:    1,
		Enabled:          true,
		TotalAmount:      5,
		QuotaResetPeriod: SubscriptionResetNever,
	}
	require.NoError(t, db.Create(&plan).Error)
	require.NoError(t, db.Create(&UserSubscription{
		UserId:      1002,
		PlanId:      plan.Id,
		AmountTotal: 5,
		AmountUsed:  4,
		StartTime:   time.Now().Add(-time.Hour).Unix(),
		EndTime:     time.Now().Add(time.Hour).Unix(),
		Status:      "active",
	}).Error)

	_, err := PreConsumeUserSubscription("request-insufficient", 1002, "gpt-test", 0, 2)

	require.ErrorIs(t, err, ErrSubscriptionQuotaInsufficient)
	require.NotErrorIs(t, err, ErrNoActiveSubscription)
}

func TestPreConsumeUserSubscriptionPreservesDatabaseErrors(t *testing.T) {
	db := setupSubscriptionPreConsumeErrorTestDB(t)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	_, err = PreConsumeUserSubscription("request-db-error", 1003, "gpt-test", 0, 1)

	require.Error(t, err)
	require.NotErrorIs(t, err, ErrNoActiveSubscription)
	require.NotErrorIs(t, err, ErrSubscriptionQuotaInsufficient)
	require.ErrorContains(t, err, "database is closed")
}
