package service

import (
	"context"
	"fmt"
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
	subscriptionResetTickInterval = 1 * time.Minute
	subscriptionResetBatchSize    = 300
	subscriptionCleanupInterval   = 30 * time.Minute
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
