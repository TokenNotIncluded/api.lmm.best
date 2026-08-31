package model

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func subscriptionResetAuditCount(t *testing.T, action string) int64 {
	t.Helper()
	var count int64
	require.NoError(t, DB.Model(&Log{}).Where("other LIKE ?", "%\"action\":\""+action+"\"%").Count(&count).Error)
	return count
}

func installSubscriptionResetAuditFailure(t *testing.T) func() {
	t.Helper()
	name := "test:fail_subscription_reset_audit"
	require.NoError(t, DB.Callback().Create().Before("gorm:create").Register(name, func(tx *gorm.DB) {
		if tx.Statement != nil && tx.Statement.Table == "logs" {
			tx.AddError(fmt.Errorf("injected reset audit failure"))
		}
	}))
	return func() { require.NoError(t, DB.Callback().Create().Remove(name)) }
}

func seedResetSubscription(t *testing.T, userId, planId, subscriptionId int, used int64) (int64, int64) {
	t.Helper()
	now := GetDBTimestamp()
	endTime := now + 90*24*60*60
	nextResetTime := now + 12*24*60*60
	require.NoError(t, DB.Create(&User{
		Id: userId, Username: "reset-user-" + time.Unix(int64(userId), 0).Format("150405"),
		Password: "password", Status: 1, AffCode: fmt.Sprintf("reset-aff-%d", userId),
	}).Error)
	require.NoError(t, DB.Create(&SubscriptionPlan{
		Id: planId, Title: "Reset plan", PriceAmount: 1,
		DurationUnit: SubscriptionDurationMonth, DurationValue: 1,
		TotalAmount: 10_000, Enabled: true,
	}).Error)
	require.NoError(t, DB.Create(&UserSubscription{
		Id: subscriptionId, UserId: userId, PlanId: planId,
		AmountTotal: 10_000, AmountUsed: used, StartTime: now - 3600,
		EndTime: endTime, Status: "active", NextResetTime: nextResetTime,
	}).Error)
	return endTime, nextResetTime
}

func TestHardSubscriptionResetRequiresPreviewAndChangesOnlyQuota(t *testing.T) {
	truncateTables(t)
	endTime, nextResetTime := seedResetSubscription(t, 9701, 9702, 9703, 4321)

	input := AdminSubscriptionResetBatchInput{
		ActorUserId: 1, Mode: SubscriptionResetModeHard,
		Targets: []SubscriptionResetTarget{{UserId: 9701, PlanId: 9702}},
	}
	preview, err := AdminPreviewSubscriptionsReset(input)
	require.NoError(t, err)
	require.Equal(t, int64(4321), preview.QuotaToRestore)
	require.Equal(t, 1, preview.TargetCount)
	require.Equal(t, 1, preview.ActiveSubscriptions)
	require.NotEmpty(t, preview.Token)

	_, err = AdminResetSubscriptionsBatch(AdminSubscriptionResetBatchInput{ActorUserId: 1, Mode: SubscriptionResetModeHard})
	require.ErrorContains(t, err, "preview is required")

	operationId := "hard-reset-preview-contract"
	result, err := AdminResetSubscriptionsBatch(AdminSubscriptionResetBatchInput{
		ActorUserId: 1, OperationId: operationId, PreviewToken: preview.Token,
	})
	require.NoError(t, err)
	require.Equal(t, int64(4321), result.RestoredQuota)
	require.Equal(t, 1, result.ResetSubscriptions)

	var subscription UserSubscription
	require.NoError(t, DB.First(&subscription, 9703).Error)
	require.Zero(t, subscription.AmountUsed)
	require.Equal(t, endTime, subscription.EndTime)
	require.Equal(t, nextResetTime, subscription.NextResetTime)

	retry, err := AdminResetSubscriptionsBatch(AdminSubscriptionResetBatchInput{
		ActorUserId: 1, OperationId: operationId, PreviewToken: preview.Token,
	})
	require.NoError(t, err)
	require.Equal(t, result.RestoredQuota, retry.RestoredQuota)
	require.Equal(t, int64(1), subscriptionResetAuditCount(t, "subscription.reset.execute"))
	_, err = AdminResetSubscriptionsBatch(AdminSubscriptionResetBatchInput{
		ActorUserId: 1, OperationId: "different-operation", PreviewToken: preview.Token,
	})
	require.ErrorContains(t, err, "already been consumed")
}

func TestSoftSubscriptionResetIssuesExpiringBankedVoucher(t *testing.T) {
	truncateTables(t)
	endTime, nextResetTime := seedResetSubscription(t, 9711, 9712, 9713, 2468)

	preview, err := AdminPreviewSubscriptionsReset(AdminSubscriptionResetBatchInput{
		ActorUserId: 1, Mode: SubscriptionResetModeSoft,
		Targets: []SubscriptionResetTarget{{UserId: 9711, PlanId: 9712}},
	})
	require.NoError(t, err)
	require.Equal(t, int64(2468), preview.QuotaToRestore)
	require.Greater(t, preview.VoucherExpiresAt, GetDBTimestamp()+27*24*60*60)
	require.Less(t, preview.VoucherExpiresAt, GetDBTimestamp()+32*24*60*60)

	result, err := AdminResetSubscriptionsBatch(AdminSubscriptionResetBatchInput{
		ActorUserId: 1, OperationId: "soft-reset-preview-contract", PreviewToken: preview.Token,
	})
	require.NoError(t, err)
	require.Zero(t, result.RestoredQuota)
	require.Equal(t, 1, result.VouchersIssued)

	var subscription UserSubscription
	require.NoError(t, DB.First(&subscription, 9713).Error)
	require.Equal(t, int64(2468), subscription.AmountUsed)
	require.Equal(t, endTime, subscription.EndTime)
	require.Equal(t, nextResetTime, subscription.NextResetTime)

	duplicatePreview, err := AdminPreviewSubscriptionsReset(AdminSubscriptionResetBatchInput{
		ActorUserId: 1, Mode: SubscriptionResetModeSoft,
		Targets: []SubscriptionResetTarget{{UserId: 9711, PlanId: 9712}},
	})
	require.NoError(t, err)
	require.Len(t, duplicatePreview.Targets, 1)
	require.Equal(t, int64(1), duplicatePreview.Targets[0].BankedVoucherCount)

	vouchers, err := ListUserSubscriptionResetVouchers(9711)
	require.NoError(t, err)
	require.Len(t, vouchers, 1)
	require.Equal(t, SubscriptionResetVoucherAvailable, vouchers[0].Status)

	redeemed, err := RedeemUserSubscriptionResetVoucher(9711, vouchers[0].Id)
	require.NoError(t, err)
	require.Equal(t, int64(2468), redeemed.RestoredQuota)
	require.NoError(t, DB.First(&subscription, 9713).Error)
	require.Zero(t, subscription.AmountUsed)
	require.Equal(t, endTime, subscription.EndTime)
	require.Equal(t, nextResetTime, subscription.NextResetTime)

	replayed, err := RedeemUserSubscriptionResetVoucher(9711, vouchers[0].Id)
	require.NoError(t, err)
	require.Equal(t, redeemed, replayed)
	require.Equal(t, int64(1), subscriptionResetAuditCount(t, "subscription.reset.execute"))
	require.Equal(t, int64(1), subscriptionResetAuditCount(t, "subscription.reset.voucher_redeem"))
}

func TestSubscriptionResetVoucherListPrioritizesAvailableVouchers(t *testing.T) {
	truncateTables(t)
	seedResetSubscription(t, 9721, 9722, 9723, 10)
	now := GetDBTimestamp()
	available := SubscriptionResetVoucher{
		UserId: 9721, PlanId: 9722, OperationId: "available-voucher",
		Status: SubscriptionResetVoucherAvailable, ExpiresAt: now + 3600,
	}
	require.NoError(t, DB.Create(&available).Error)
	for index := 0; index < 101; index++ {
		require.NoError(t, DB.Create(&SubscriptionResetVoucher{
			UserId: 9721, PlanId: 9722, OperationId: fmt.Sprintf("redeemed-voucher-%d", index),
			Status: SubscriptionResetVoucherRedeemed, ExpiresAt: now + 3600, RedeemedAt: now,
		}).Error)
	}

	vouchers, err := ListUserSubscriptionResetVouchers(9721)
	require.NoError(t, err)
	require.Len(t, vouchers, 100)
	require.Equal(t, available.Id, vouchers[0].Id)
	require.Equal(t, SubscriptionResetVoucherAvailable, vouchers[0].Status)
}

func TestSubscriptionResetPreviewSupportsMultiplePlans(t *testing.T) {
	truncateTables(t)
	seedResetSubscription(t, 9721, 9722, 9723, 100)
	require.NoError(t, DB.Create(&SubscriptionPlan{
		Id: 9724, Title: "Second reset plan", PriceAmount: 1,
		DurationUnit: SubscriptionDurationMonth, DurationValue: 1,
		TotalAmount: 20_000, Enabled: true,
	}).Error)
	now := GetDBTimestamp()
	require.NoError(t, DB.Create(&UserSubscription{
		Id: 9725, UserId: 9721, PlanId: 9724, AmountTotal: 20_000,
		AmountUsed: 250, StartTime: now - 3600, EndTime: now + 3600,
		Status: "active", NextResetTime: now + 1800,
	}).Error)

	preview, err := AdminPreviewSubscriptionsReset(AdminSubscriptionResetBatchInput{
		ActorUserId: 1, Mode: SubscriptionResetModeHard,
		Targets: []SubscriptionResetTarget{
			{UserId: 9721, PlanId: 9722},
			{UserId: 9721, PlanId: 9724},
		},
	})
	require.NoError(t, err)
	require.Equal(t, 2, preview.PlanCount)
	require.Equal(t, 1, preview.UserCount)
	require.Equal(t, int64(350), preview.QuotaToRestore)
	require.Len(t, preview.Targets, 2)
}

func TestSubscriptionResetAllMatchingFiltersSpecificUsers(t *testing.T) {
	truncateTables(t)
	seedResetSubscription(t, 9711, 9712, 9713, 100)
	seedResetSubscription(t, 9714, 9715, 9716, 200)

	preview, err := AdminPreviewSubscriptionsReset(AdminSubscriptionResetBatchInput{
		ActorUserId: 1, Mode: SubscriptionResetModeHard, AllMatching: true,
		Filter: AdminSubscriptionResetEligibleFilter{UserIds: []int{9714}},
	})
	require.NoError(t, err)
	require.Equal(t, 1, preview.TargetCount)
	require.Equal(t, 9714, preview.Targets[0].UserId)
	require.Equal(t, int64(200), preview.QuotaToRestore)
}

func TestSubscriptionResetAllMatchingRejectsAmbiguousOrOversizedFilters(t *testing.T) {
	truncateTables(t)
	_, err := AdminPreviewSubscriptionsReset(AdminSubscriptionResetBatchInput{
		ActorUserId: 1, Mode: SubscriptionResetModeHard, AllMatching: true,
		Targets: []SubscriptionResetTarget{{UserId: 1, PlanId: 1}},
	})
	require.ErrorContains(t, err, "cannot be combined")

	planIds := make([]int, 101)
	for index := range planIds {
		planIds[index] = index + 1
	}
	_, err = AdminPreviewSubscriptionsReset(AdminSubscriptionResetBatchInput{
		ActorUserId: 1, Mode: SubscriptionResetModeHard, AllMatching: true,
		Filter: AdminSubscriptionResetEligibleFilter{PlanIds: planIds},
	})
	require.ErrorContains(t, err, "too many")

	_, err = AdminPreviewSubscriptionsReset(AdminSubscriptionResetBatchInput{
		ActorUserId: 1, Mode: SubscriptionResetModeHard, AllMatching: true,
		Filter: AdminSubscriptionResetEligibleFilter{PlanIds: []int{-1}},
	})
	require.ErrorContains(t, err, "invalid")

	_, err = AdminPreviewSubscriptionsReset(AdminSubscriptionResetBatchInput{
		ActorUserId: 1, Mode: SubscriptionResetModeHard, AllMatching: true,
		Filter: AdminSubscriptionResetEligibleFilter{PlanId: 1, PlanIds: []int{1}},
	})
	require.ErrorContains(t, err, "cannot be combined")

	_, err = AdminPreviewSubscriptionsReset(AdminSubscriptionResetBatchInput{
		ActorUserId: 1, Mode: SubscriptionResetModeHard, AllMatching: true,
		Filter: AdminSubscriptionResetEligibleFilter{Query: strings.Repeat("x", 201)},
	})
	require.ErrorContains(t, err, "too long")
}

func TestSubscriptionResetRejectsUsersWithoutActiveSubscription(t *testing.T) {
	truncateTables(t)
	require.NoError(t, DB.Create(&User{Id: 9731, Username: "no-subscription", Password: "password", Status: 1}).Error)
	require.NoError(t, DB.Create(&SubscriptionPlan{
		Id: 9732, Title: "Inactive plan", PriceAmount: 1,
		DurationUnit: SubscriptionDurationMonth, DurationValue: 1, TotalAmount: 100,
	}).Error)
	require.NoError(t, DB.Create(&UserSubscription{
		Id: 9733, UserId: 9731, PlanId: 9732, AmountTotal: 100, AmountUsed: 50,
		Status: "active", StartTime: GetDBTimestamp() - 60, EndTime: 0,
	}).Error)

	_, err := AdminPreviewSubscriptionsReset(AdminSubscriptionResetBatchInput{
		ActorUserId: 1, Mode: SubscriptionResetModeHard,
		Targets: []SubscriptionResetTarget{{UserId: 9731, PlanId: 9732}},
	})
	require.ErrorContains(t, err, "no active subscription users")
}

func TestSubscriptionResetRequiresClientOperationID(t *testing.T) {
	truncateTables(t)
	seedResetSubscription(t, 9741, 9742, 9743, 50)
	preview, err := AdminPreviewSubscriptionsReset(AdminSubscriptionResetBatchInput{
		ActorUserId: 1, Mode: SubscriptionResetModeHard,
		Targets: []SubscriptionResetTarget{{UserId: 9741, PlanId: 9742}},
	})
	require.NoError(t, err)

	_, err = AdminResetSubscriptionsBatch(AdminSubscriptionResetBatchInput{
		ActorUserId: 1, PreviewToken: preview.Token,
	})
	require.ErrorContains(t, err, "operation id is required")

	_, err = AdminResetSubscriptionsBatch(AdminSubscriptionResetBatchInput{
		ActorUserId: 1, PreviewToken: preview.Token, OperationId: strings.Repeat("x", 65),
	})
	require.ErrorContains(t, err, "operation id is too long")
}

func TestSubscriptionResetRejectsStalePreviewWithoutConsumingIt(t *testing.T) {
	truncateTables(t)
	seedResetSubscription(t, 9751, 9752, 9753, 100)
	preview, err := AdminPreviewSubscriptionsReset(AdminSubscriptionResetBatchInput{
		ActorUserId: 1, Mode: SubscriptionResetModeHard,
		Targets: []SubscriptionResetTarget{{UserId: 9751, PlanId: 9752}},
	})
	require.NoError(t, err)
	require.NoError(t, DB.Model(&UserSubscription{}).Where("id = ?", 9753).UpdateColumn("amount_used", 125).Error)

	_, err = AdminResetSubscriptionsBatch(AdminSubscriptionResetBatchInput{
		ActorUserId: 1, OperationId: "stale-preview", PreviewToken: preview.Token,
	})
	require.ErrorIs(t, err, ErrSubscriptionResetPreviewStale)

	var persistedPreview SubscriptionResetPreview
	require.NoError(t, DB.First(&persistedPreview, "token = ?", preview.Token).Error)
	require.Zero(t, persistedPreview.ConsumedAt)
	require.Empty(t, persistedPreview.OperationId)
	var subscription UserSubscription
	require.NoError(t, DB.First(&subscription, 9753).Error)
	require.Equal(t, int64(125), subscription.AmountUsed)
}

func TestSubscriptionResetRejectsSubscriptionsAddedAfterPreview(t *testing.T) {
	truncateTables(t)
	endTime, nextResetTime := seedResetSubscription(t, 9761, 9762, 9763, 100)
	preview, err := AdminPreviewSubscriptionsReset(AdminSubscriptionResetBatchInput{
		ActorUserId: 1, Mode: SubscriptionResetModeHard,
		Targets: []SubscriptionResetTarget{{UserId: 9761, PlanId: 9762}},
	})
	require.NoError(t, err)
	require.NoError(t, DB.Create(&UserSubscription{
		Id: 9764, UserId: 9761, PlanId: 9762, AmountTotal: 10_000, AmountUsed: 75,
		StartTime: GetDBTimestamp() - 60, EndTime: endTime, Status: "active", NextResetTime: nextResetTime,
	}).Error)

	_, err = AdminResetSubscriptionsBatch(AdminSubscriptionResetBatchInput{
		ActorUserId: 1, OperationId: "frozen-only", PreviewToken: preview.Token,
	})
	require.ErrorIs(t, err, ErrSubscriptionResetPreviewStale)

	var original, added UserSubscription
	require.NoError(t, DB.First(&original, 9763).Error)
	require.NoError(t, DB.First(&added, 9764).Error)
	require.Equal(t, int64(100), original.AmountUsed)
	require.Equal(t, int64(75), added.AmountUsed)
	var persistedPreview SubscriptionResetPreview
	require.NoError(t, DB.First(&persistedPreview, "token = ?", preview.Token).Error)
	require.Zero(t, persistedPreview.ConsumedAt)
	require.Empty(t, persistedPreview.OperationId)
}

func TestSubscriptionResetOperationReplaySurvivesPreviewExpiry(t *testing.T) {
	truncateTables(t)
	seedResetSubscription(t, 9771, 9772, 9773, 80)
	preview, err := AdminPreviewSubscriptionsReset(AdminSubscriptionResetBatchInput{
		ActorUserId: 1, Mode: SubscriptionResetModeHard,
		Targets: []SubscriptionResetTarget{{UserId: 9771, PlanId: 9772}},
	})
	require.NoError(t, err)
	input := AdminSubscriptionResetBatchInput{ActorUserId: 1, OperationId: "expiry-replay", PreviewToken: preview.Token}
	first, err := AdminResetSubscriptionsBatch(input)
	require.NoError(t, err)
	require.NoError(t, DB.Model(&SubscriptionResetPreview{}).Where("token = ?", preview.Token).UpdateColumn("expires_at", 1).Error)

	replayed, err := AdminResetSubscriptionsBatch(input)
	require.NoError(t, err)
	require.Equal(t, first, replayed)
}

func TestSubscriptionResetOperationIDCannotBindAnotherPreview(t *testing.T) {
	truncateTables(t)
	seedResetSubscription(t, 9781, 9782, 9783, 90)
	firstPreview, err := AdminPreviewSubscriptionsReset(AdminSubscriptionResetBatchInput{
		ActorUserId: 1, Mode: SubscriptionResetModeHard,
		Targets: []SubscriptionResetTarget{{UserId: 9781, PlanId: 9782}},
	})
	require.NoError(t, err)
	_, err = AdminResetSubscriptionsBatch(AdminSubscriptionResetBatchInput{
		ActorUserId: 1, OperationId: "shared-operation", PreviewToken: firstPreview.Token,
	})
	require.NoError(t, err)

	secondPreview, err := AdminPreviewSubscriptionsReset(AdminSubscriptionResetBatchInput{
		ActorUserId: 1, Mode: SubscriptionResetModeHard,
		Targets: []SubscriptionResetTarget{{UserId: 9781, PlanId: 9782}},
	})
	require.NoError(t, err)
	_, err = AdminResetSubscriptionsBatch(AdminSubscriptionResetBatchInput{
		ActorUserId: 1, OperationId: "shared-operation", PreviewToken: secondPreview.Token,
	})
	require.ErrorIs(t, err, ErrSubscriptionResetOperationConflict)
}

func TestSubscriptionResetAuditFailureRollsBackMutations(t *testing.T) {
	truncateTables(t)
	seedResetSubscription(t, 9791, 9792, 9793, 140)
	preview, err := AdminPreviewSubscriptionsReset(AdminSubscriptionResetBatchInput{
		ActorUserId: 1, Mode: SubscriptionResetModeHard,
		Targets: []SubscriptionResetTarget{{UserId: 9791, PlanId: 9792}},
	})
	require.NoError(t, err)

	removeFailure := installSubscriptionResetAuditFailure(t)
	_, err = AdminResetSubscriptionsBatch(AdminSubscriptionResetBatchInput{
		ActorUserId: 1, OperationId: "audit-failure-batch", PreviewToken: preview.Token,
	})
	removeFailure()
	require.ErrorContains(t, err, "injected reset audit failure")
	var subscription UserSubscription
	require.NoError(t, DB.First(&subscription, 9793).Error)
	require.Equal(t, int64(140), subscription.AmountUsed)
	var persistedPreview SubscriptionResetPreview
	require.NoError(t, DB.First(&persistedPreview, "token = ?", preview.Token).Error)
	require.Zero(t, persistedPreview.ConsumedAt)
	var operationCount, eventCount int64
	require.NoError(t, DB.Model(&SubscriptionResetOperation{}).Where("operation_id = ?", "audit-failure-batch").Count(&operationCount).Error)
	require.NoError(t, DB.Model(&SubscriptionResetEvent{}).Where("operation_id = ?", "audit-failure-batch").Count(&eventCount).Error)
	require.Zero(t, operationCount)
	require.Zero(t, eventCount)

	voucher := SubscriptionResetVoucher{
		UserId: 9791, PlanId: 9792, OperationId: "audit-failure-voucher",
		Status: SubscriptionResetVoucherAvailable, ExpiresAt: GetDBTimestamp() + 3600,
	}
	require.NoError(t, DB.Create(&voucher).Error)
	removeFailure = installSubscriptionResetAuditFailure(t)
	_, err = RedeemUserSubscriptionResetVoucher(9791, voucher.Id)
	removeFailure()
	require.ErrorContains(t, err, "injected reset audit failure")
	require.NoError(t, DB.First(&voucher, voucher.Id).Error)
	require.Equal(t, SubscriptionResetVoucherAvailable, voucher.Status)
	require.NoError(t, DB.First(&subscription, 9793).Error)
	require.Equal(t, int64(140), subscription.AmountUsed)
	require.NoError(t, DB.Model(&SubscriptionResetEvent{}).Where("voucher_id = ?", voucher.Id).Count(&eventCount).Error)
	require.Zero(t, eventCount)
}

func TestSubscriptionResetPreviewCleanupIsBoundedAndPreservesReplay(t *testing.T) {
	truncateTables(t)
	seedResetSubscription(t, 9801, 9802, 9803, 75)
	preview, err := AdminPreviewSubscriptionsReset(AdminSubscriptionResetBatchInput{
		ActorUserId: 1, Mode: SubscriptionResetModeHard,
		Targets: []SubscriptionResetTarget{{UserId: 9801, PlanId: 9802}},
	})
	require.NoError(t, err)
	input := AdminSubscriptionResetBatchInput{ActorUserId: 1, OperationId: "cleanup-replay", PreviewToken: preview.Token}
	first, err := AdminResetSubscriptionsBatch(input)
	require.NoError(t, err)

	now := GetDBTimestamp()
	stale := now - subscriptionResetPreviewRetentionSeconds - 60
	require.NoError(t, DB.Model(&SubscriptionResetPreview{}).Where("token = ?", preview.Token).
		Updates(map[string]any{"consumed_at": stale, "expires_at": stale}).Error)
	for _, candidate := range []SubscriptionResetPreview{
		{Token: "cleanup-active", ActorUserId: 1, Mode: SubscriptionResetModeHard, TargetsJSON: "[]", PayloadHash: "active", ExpiresAt: now + 600, CreatedAt: stale},
		{Token: "cleanup-recent-expired", ActorUserId: 1, Mode: SubscriptionResetModeHard, TargetsJSON: "[]", PayloadHash: "recent", ExpiresAt: now - subscriptionResetPreviewRetentionSeconds + 60, CreatedAt: stale},
		{Token: "cleanup-stale-expired", ActorUserId: 1, Mode: SubscriptionResetModeHard, TargetsJSON: "[]", PayloadHash: "stale", ExpiresAt: stale, CreatedAt: stale},
		{Token: "cleanup-consumed-without-operation", ActorUserId: 1, Mode: SubscriptionResetModeHard, TargetsJSON: "[]", PayloadHash: "unsafe", ExpiresAt: stale, ConsumedAt: stale, CreatedAt: stale},
	} {
		require.NoError(t, DB.Create(&candidate).Error)
	}

	deleted, err := CleanupSubscriptionResetPreviewsContext(context.Background(), 1)
	require.NoError(t, err)
	require.Equal(t, int64(1), deleted)
	deleted, err = CleanupSubscriptionResetPreviewsContext(context.Background(), 300)
	require.NoError(t, err)
	require.Equal(t, int64(1), deleted)
	for _, token := range []string{"cleanup-active", "cleanup-recent-expired", "cleanup-consumed-without-operation"} {
		var count int64
		require.NoError(t, DB.Model(&SubscriptionResetPreview{}).Where("token = ?", token).Count(&count).Error)
		require.Equal(t, int64(1), count, token)
	}
	var removed int64
	require.NoError(t, DB.Model(&SubscriptionResetPreview{}).Where("token IN ?", []string{preview.Token, "cleanup-stale-expired"}).Count(&removed).Error)
	require.Zero(t, removed)

	replayed, err := AdminResetSubscriptionsBatch(input)
	require.NoError(t, err)
	require.Equal(t, first, replayed)
	require.Equal(t, int64(1), subscriptionResetAuditCount(t, "subscription.reset.execute"))
	var operationCount, eventCount int64
	require.NoError(t, DB.Model(&SubscriptionResetOperation{}).Where("operation_id = ?", input.OperationId).Count(&operationCount).Error)
	require.NoError(t, DB.Model(&SubscriptionResetEvent{}).Where("operation_id = ?", input.OperationId).Count(&eventCount).Error)
	require.Equal(t, int64(1), operationCount)
	require.Equal(t, int64(1), eventCount)
}

func TestSubscriptionResetVoucherEmptyLockedSetDoesNotClaim(t *testing.T) {
	truncateTables(t)
	now := GetDBTimestamp()
	require.NoError(t, DB.Create(&User{Id: 9811, Username: "empty-lock", Password: "password", Status: 1}).Error)
	require.NoError(t, DB.Create(&SubscriptionPlan{Id: 9812, Title: "Empty lock", PriceAmount: 1, DurationUnit: SubscriptionDurationMonth, DurationValue: 1, TotalAmount: 100}).Error)
	voucher := SubscriptionResetVoucher{UserId: 9811, PlanId: 9812, OperationId: "empty-lock", Status: SubscriptionResetVoucherAvailable, ExpiresAt: now + 3600}
	require.NoError(t, DB.Create(&voucher).Error)

	_, err := RedeemUserSubscriptionResetVoucher(9811, voucher.Id)
	require.ErrorIs(t, err, ErrSubscriptionResetRequiresActiveSubscription)
	require.NoError(t, DB.First(&voucher, voucher.Id).Error)
	require.Equal(t, SubscriptionResetVoucherAvailable, voucher.Status)
	require.Zero(t, subscriptionResetAuditCount(t, "subscription.reset.voucher_redeem"))
}

func subscriptionResetDialectDB(t *testing.T, dialect string) *gorm.DB {
	t.Helper()
	config := &gorm.Config{DryRun: true, DisableAutomaticPing: true}
	switch dialect {
	case "mysql":
		db, err := gorm.Open(mysql.New(mysql.Config{
			DSN:                       "gorm:gorm@tcp(127.0.0.1:9910)/gorm?charset=utf8mb4&parseTime=True&loc=Local",
			SkipInitializeWithVersion: true,
		}), config)
		require.NoError(t, err)
		return db
	case "postgres":
		db, err := gorm.Open(postgres.New(postgres.Config{
			DSN:                  "host=127.0.0.1 port=9910 user=gorm dbname=gorm sslmode=disable",
			PreferSimpleProtocol: true,
		}), config)
		require.NoError(t, err)
		return db
	case "sqlite":
		return DB.Session(&gorm.Session{DryRun: true})
	default:
		t.Fatalf("unsupported test dialect %q", dialect)
		return nil
	}
}

func TestAdminSubscriptionSearchUsesDialectCompatibleUserIDCast(t *testing.T) {
	for _, testCase := range []struct {
		dialect string
		cast    string
	}{
		{dialect: "mysql", cast: "CHAR"},
		{dialect: "postgres", cast: "TEXT"},
		{dialect: "sqlite", cast: "TEXT"},
	} {
		t.Run(testCase.dialect, func(t *testing.T) {
			db := subscriptionResetDialectDB(t, testCase.dialect)
			statement := applyAdminSubscriptionSearch(db.Table("user_subscriptions AS us"), " 42 ").
				Find(&[]AdminSubscriptionRecord{}).Statement
			require.NoError(t, statement.Error)
			require.Contains(t, statement.SQL.String(), "CAST(us.user_id AS "+testCase.cast+")")
		})
	}
}

func TestSubscriptionResetTargetsJSONUsesPortableDialectTypes(t *testing.T) {
	for _, testCase := range []struct {
		dialect string
		want    string
	}{
		{dialect: "mysql", want: "LONGTEXT NOT NULL"},
		{dialect: "postgres", want: "TEXT NOT NULL"},
		{dialect: "sqlite", want: "TEXT NOT NULL"},
	} {
		t.Run(testCase.dialect, func(t *testing.T) {
			db := subscriptionResetDialectDB(t, testCase.dialect)
			statement := &gorm.Statement{DB: db}
			require.NoError(t, statement.Parse(&SubscriptionResetPreview{}))
			field := statement.Schema.LookUpField("TargetsJSON")
			require.NotNil(t, field)
			require.Equal(t, testCase.want, db.Migrator().FullDataTypeOf(field).SQL)
		})
	}
}

func TestSubscriptionResetMaximumPreviewExceedsMySQLTextCapacity(t *testing.T) {
	targets := make([]SubscriptionResetPreviewTarget, maxSubscriptionResetTargets)
	for index := range targets {
		targets[index] = SubscriptionResetPreviewTarget{
			UserId: index + 1,
			PlanId: index + 1,
			Subscriptions: []SubscriptionResetPreviewSubscription{{
				Id: index + 1, UserId: index + 1, PlanId: index + 1,
				AmountUsed: 1, Status: "active",
			}},
		}
	}
	payload, err := json.Marshal(targets)
	require.NoError(t, err)
	require.Greater(t, len(payload), 65_535)
	require.Equal(t, "LONGTEXT", SubscriptionResetTargetsJSON("").GormDBDataType(subscriptionResetDialectDB(t, "mysql"), nil))
}

func TestCheckedSubscriptionResetAddRejectsOverflow(t *testing.T) {
	_, err := checkedSubscriptionResetAdd(int64(^uint64(0)>>1), 1)
	require.ErrorContains(t, err, "supported range")
}

func TestAddOneCalendarMonthUTCClampsMonthEnd(t *testing.T) {
	for _, testCase := range []struct {
		name string
		from time.Time
		want time.Time
	}{
		{name: "common year", from: time.Date(2025, time.January, 31, 12, 30, 0, 0, time.UTC), want: time.Date(2025, time.February, 28, 12, 30, 0, 0, time.UTC)},
		{name: "leap year", from: time.Date(2024, time.January, 31, 12, 30, 0, 0, time.UTC), want: time.Date(2024, time.February, 29, 12, 30, 0, 0, time.UTC)},
		{name: "ordinary day", from: time.Date(2025, time.March, 15, 8, 0, 0, 0, time.UTC), want: time.Date(2025, time.April, 15, 8, 0, 0, 0, time.UTC)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			require.Equal(t, testCase.want.Unix(), addOneCalendarMonthUTC(testCase.from.Unix()))
		})
	}
}
