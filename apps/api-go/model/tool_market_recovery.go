package model

import (
	"context"
	"errors"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
)

// Recovery never contacts a provider or repeats business execution. A durable
// final outcome may complete settlement; an unconfirmed expired hold is freed.
func RecoverToolMarketCalls(ctx context.Context) (int, error) {
	var calls []ToolMarketCall
	q := DB.WithContext(ctx).Where("settlement_status = ? AND (resolve_by <= ? OR id IN (?))", "held", common.GetTimestamp(), DB.Model(&ToolMarketResult{}).Select("call_id"))
	if err := q.Order("resolve_by, id").Limit(100).Find(&calls).Error; err != nil {
		return 0, err
	}
	processed := 0
	var failures error
	for _, call := range calls {
		if err := ctx.Err(); err != nil {
			return processed, err
		}
		var outcome ToolMarketResult
		err := DB.WithContext(ctx).First(&outcome, "call_id = ?", call.ID).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return processed, err
		}
		if common.GetTimestamp() >= call.ResolveBy {
			err = ExpireToolMarketCall(call.ID)
		} else if err == nil {
			err = FinishToolMarketCall(call.ID, outcome.Success)
		} else {
			continue
		}
		if err != nil {
			failures = errors.Join(failures, err)
			continue
		}
		processed++
	}
	var expired []string
	if err := DB.WithContext(ctx).Model(&ToolMarketResult{}).Where("expires_at <= ?", common.GetTimestamp()).Order("expires_at").Limit(100).Pluck("call_id", &expired).Error; err != nil {
		return processed, err
	}
	if len(expired) > 0 {
		if err := DB.WithContext(ctx).Where("call_id IN ?", expired).Delete(&ToolMarketResult{}).Error; err != nil {
			return processed, err
		}
	}
	return processed, failures
}
