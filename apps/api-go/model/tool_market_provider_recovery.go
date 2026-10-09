package model

import (
	"context"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/internal/marketprovider"
	"gorm.io/gorm"
)

// Only a run ID returned by the trusted execution adapter may be attached.
// The association survives restarts and cannot be replaced by a status caller.
func BindToolMarketProviderRun(id, runID string) error {
	if !marketprovider.ValidRunID(runID) {
		return ErrToolMarketInput
	}
	return marketCallTx(id, func(tx *gorm.DB, call *ToolMarketCall) error {
		if call.ProviderQuote == nil || call.ProviderQuote.Provider != "monid" || call.ProviderQuote.TargetProvider == "" || call.ProviderQuote.TargetEndpoint == "" {
			return ErrToolMarketInput
		}
		if call.ProviderRunID != "" {
			if call.ProviderRunID == runID {
				return nil
			}
			return ErrToolMarketConflict
		}
		if call.SettlementStatus != "held" || call.ExecutionStatus != "running" || call.ResolveBy <= common.GetTimestamp() {
			return ErrToolMarketConflict
		}
		return tx.Model(call).Updates(map[string]any{"provider_run_id": runID, "provider_next_poll_at": common.GetTimestamp()}).Error
	})
}

func PendingToolMarketProviderCalls(ctx context.Context) ([]ToolMarketCall, error) {
	var calls []ToolMarketCall
	now := common.GetTimestamp()
	err := DB.WithContext(ctx).Where("settlement_status = ? AND provider_run_id <> '' AND provider_next_poll_at <= ? AND resolve_by > ?", "held", now, now).
		Order("provider_next_poll_at, id").Limit(12).Find(&calls).Error
	return calls, err
}

// A short compare-and-swap lease bounds read-only requests from concurrent
// workers. If a worker dies, another worker can continue after the lease.
func ClaimToolMarketProviderPoll(ctx context.Context, id string) (bool, error) {
	now := common.GetTimestamp()
	q := DB.WithContext(ctx).Model(&ToolMarketCall{}).Where("id = ? AND settlement_status = ? AND provider_run_id <> '' AND provider_next_poll_at <= ? AND resolve_by > ?", id, "held", now, now).
		UpdateColumn("provider_next_poll_at", now+15)
	return q.RowsAffected == 1, q.Error
}
