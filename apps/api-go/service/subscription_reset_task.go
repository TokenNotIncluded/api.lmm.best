package service

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/leadership"
	"github.com/LIghtJUNction/api.lmm.best/logger"
	"github.com/LIghtJUNction/api.lmm.best/model"

	"github.com/bytedance/gopkg/util/gopool"
)

const (
	subscriptionResetTickInterval         = 1 * time.Minute
	subscriptionResetBatchSize            = 300
	subscriptionCleanupInterval           = 30 * time.Minute
	subscriptionBillingRecoveryRetryAfter = 1 * time.Minute
	subscriptionBillingRecoveryTimeout    = 15 * time.Second
	subscriptionBillingRecoveryBatchSize  = 50
)

var (
	subscriptionResetOnce    sync.Once
	subscriptionResetRunning atomic.Bool
	subscriptionCleanupLast  atomic.Int64
)

func StartSubscriptionQuotaResetTask() {
	subscriptionResetOnce.Do(func() {
		if common.IsMasterNode {
			gopool.Go(func() { RunSubscriptionQuotaResetTask(context.Background()) })
		}
	})
}

// StartSubscriptionMaintenanceScanWithContext starts PostgreSQL-guarded
// expiration, quota-reset, and cleanup scans. Followers only retry the lock.
func StartSubscriptionMaintenanceScanWithContext(ctx context.Context) error {
	return startPostgresLeaderTask(ctx, leadership.SubscriptionMaintenanceNamespace,
		"subscription maintenance", runSubscriptionMaintenanceAsLeader)
}

// RunSubscriptionMaintenanceScanWithLeadership runs synchronously so the
// process lifecycle can wait for lease release before closing PostgreSQL.
func RunSubscriptionMaintenanceScanWithLeadership(ctx context.Context) error {
	if !common.IsMasterNode {
		return nil
	}
	return runPostgresLeaderTask(ctx, leadership.SubscriptionMaintenanceNamespace,
		"subscription maintenance", runSubscriptionMaintenanceAsLeader)
}

func runSubscriptionMaintenanceAsLeader(ctx context.Context) {
	logger.LogInfo(ctx, fmt.Sprintf("subscription maintenance leader started: tick=%s", subscriptionResetTickInterval))
	runSubscriptionMaintenanceLoop(ctx)
}

// RunSubscriptionQuotaResetTask is the cancellable single-instance loop.
// Multi-slot deployments must use StartSubscriptionMaintenanceScanWithContext.
func RunSubscriptionQuotaResetTask(ctx context.Context) {
	if !common.IsMasterNode {
		return
	}
	logger.LogInfo(ctx, fmt.Sprintf("subscription quota reset task started: tick=%s", subscriptionResetTickInterval))
	runSubscriptionMaintenanceLoop(ctx)
}

func runSubscriptionMaintenanceLoop(ctx context.Context) {
	runSubscriptionQuotaResetOnceContext(ctx)
	ticker := time.NewTicker(subscriptionResetTickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runSubscriptionQuotaResetOnceContext(ctx)
		}
	}
}

func runSubscriptionQuotaResetOnce() {
	runSubscriptionQuotaResetOnceContext(context.Background())
}

func runSubscriptionQuotaResetOnceContext(ctx context.Context) {
	if !subscriptionResetRunning.CompareAndSwap(false, true) {
		return
	}
	defer subscriptionResetRunning.Store(false)
	if ctx.Err() != nil {
		return
	}
	runSubscriptionBillingRecoveryOnceContext(ctx)

	totalReset := 0
	totalExpired := 0
	for {
		n, err := model.ExpireDueSubscriptionsContext(ctx, subscriptionResetBatchSize)
		if err != nil {
			logger.LogWarn(ctx, fmt.Sprintf("subscription expire task failed: %v", err))
			return
		}
		if n == 0 {
			break
		}
		totalExpired += n
		if n < subscriptionResetBatchSize {
			break
		}
	}
	for {
		n, err := model.ResetDueSubscriptionsContext(ctx, subscriptionResetBatchSize)
		if err != nil {
			logger.LogWarn(ctx, fmt.Sprintf("subscription quota reset task failed: %v", err))
			return
		}
		if n == 0 {
			break
		}
		totalReset += n
		if n < subscriptionResetBatchSize {
			break
		}
	}
	lastCleanup := time.Unix(subscriptionCleanupLast.Load(), 0)
	if ctx.Err() == nil && time.Since(lastCleanup) >= subscriptionCleanupInterval {
		_, preConsumeErr := model.CleanupSubscriptionPreConsumeRecordsContext(ctx, 7*24*3600)
		_, previewErr := model.CleanupSubscriptionResetPreviewsContext(ctx, subscriptionResetBatchSize)
		if preConsumeErr == nil && previewErr == nil {
			subscriptionCleanupLast.Store(time.Now().Unix())
		}
	}
	if common.DebugEnabled && (totalReset > 0 || totalExpired > 0) {
		logger.LogDebug(ctx, "subscription maintenance: reset_count=%d, expired_count=%d", totalReset, totalExpired)
	}
}

func runSubscriptionBillingRecoveryOnceContext(ctx context.Context) {
	records, err := model.ListSubscriptionBillingRecoveryCandidates(ctx, subscriptionBillingRecoveryBatchSize, subscriptionBillingRecoveryRetryAfter)
	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("subscription billing recovery scan failed: %v", err))
		return
	}
	for _, record := range records {
		if ctx.Err() != nil {
			return
		}
		if err := model.MarkSubscriptionBillingRecoveryAttempt(ctx, record.Id); err != nil {
			logger.LogWarn(ctx, fmt.Sprintf("subscription billing recovery claim failed: id=%d err=%v", record.Id, err))
			continue
		}
		attemptCtx, cancel := context.WithTimeout(ctx, subscriptionBillingRecoveryTimeout)
		_, settleErr := model.SettleSubscriptionBillingContext(attemptCtx, record.RequestId, record.UserId, record.ActualQuota)
		cancel()
		if settleErr != nil {
			manual := record.RecoveryAttempts+1 >= model.SubscriptionBillingRecoveryMaxAttempts || strings.Contains(settleErr.Error(), "mismatch") || strings.Contains(settleErr.Error(), "period changed")
			_ = model.MarkSubscriptionBillingRecoveryFailure(ctx, record.Id, settleErr, manual)
			logger.LogWarn(ctx, fmt.Sprintf("subscription billing recovery failed: id=%d request_id=%s manual=%t err=%v", record.Id, record.RequestId, manual, settleErr))
			continue
		}
		_ = model.MarkSubscriptionBillingRecoverySuccess(ctx, record.Id)
		logger.LogInfo(ctx, fmt.Sprintf("subscription billing recovery settled: id=%d request_id=%s", record.Id, record.RequestId))
	}
}
