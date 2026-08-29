package service

import (
	"fmt"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/logger"
	"github.com/LIghtJUNction/api.lmm.best/model"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"

	"github.com/gin-gonic/gin"
)

const (
	ViolationFeeCodePrefix     = "violation_fee."
	CSAMViolationMarker        = "Failed check: SAFETY_CHECK_TYPE"
	ContentViolatesUsageMarker = "Content violates usage guidelines"
)

func IsViolationFeeCode(code types.ErrorCode) bool {
	return strings.HasPrefix(string(code), ViolationFeeCodePrefix)
}

func HasCSAMViolationMarker(err *types.NewAPIError) bool {
	if err == nil {
		return false
	}
	if strings.Contains(err.Error(), CSAMViolationMarker) || strings.Contains(err.Error(), ContentViolatesUsageMarker) {
		return true
	}
	msg := err.ToOpenAIError().Message
	return strings.Contains(msg, CSAMViolationMarker) || strings.Contains(err.Error(), ContentViolatesUsageMarker)
}

// HasUsagePolicyViolationMarker is provider/model agnostic. The old helper
// name remains as a compatibility alias for callers outside this package.
func HasUsagePolicyViolationMarker(err *types.NewAPIError) bool {
	if err == nil {
		return false
	}
	if strings.Contains(err.Error(), CSAMViolationMarker) || strings.Contains(err.Error(), ContentViolatesUsageMarker) {
		return true
	}
	msg := err.ToOpenAIError().Message
	return strings.Contains(msg, CSAMViolationMarker) || strings.Contains(msg, ContentViolatesUsageMarker)
}

func WrapAsViolationFee(err *types.NewAPIError) *types.NewAPIError {
	if err == nil {
		return nil
	}
	oai := err.ToOpenAIError()
	oai.Type = string(types.ErrorCodeViolationFeeUsagePolicy)
	oai.Code = string(types.ErrorCodeViolationFeeUsagePolicy)
	return types.WithOpenAIError(oai, err.StatusCode, types.ErrOptionWithSkipRetry())
}

func WrapAsViolationFeeGrokCSAM(err *types.NewAPIError) *types.NewAPIError {
	return WrapAsViolationFee(err)
}

// NormalizeViolationFeeError ensures:
// - if the CSAM marker is present, error.code is set to a stable violation-fee code and skip-retry is enabled.
// - if error.code already has the violation-fee prefix, skip-retry is enabled.
//
// It must be called before retry decision logic.
func NormalizeViolationFeeError(err *types.NewAPIError) *types.NewAPIError {
	if err == nil {
		return nil
	}

	if HasUsagePolicyViolationMarker(err) {
		return WrapAsViolationFee(err)
	}

	if IsViolationFeeCode(err.GetErrorCode()) {
		oai := err.ToOpenAIError()
		return types.WithOpenAIError(oai, err.StatusCode, types.ErrOptionWithSkipRetry())
	}

	return err
}

func shouldChargeViolationFee(err *types.NewAPIError) bool {
	if err == nil {
		return false
	}
	if IsViolationFeeCode(err.GetErrorCode()) {
		return true
	}
	// In case some callers didn't normalize, keep a safety net.
	return HasUsagePolicyViolationMarker(err)
}

func calcViolationFeeQuota(amount, groupRatio float64) int {
	if amount <= 0 {
		return 0
	}
	if groupRatio <= 0 {
		return 0
	}
	quota := common.QuotaRound(amount * common.QuotaPerUnit * groupRatio)
	if quota <= 0 {
		return 0
	}
	return quota
}

// ChargeViolationFeeIfNeeded charges an additional fee after the normal flow finishes (including refund).
// It uses the group-selected global violation policy. Only the user's wallet
// quota is touched; token and subscription balances are not used for this
// punishment path.
func ChargeViolationFeeIfNeeded(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, apiErr *types.NewAPIError) bool {
	if ctx == nil || relayInfo == nil || apiErr == nil {
		return false
	}
	//if relayInfo.IsPlayground {
	//	return false
	//}
	if !shouldChargeViolationFee(apiErr) {
		return false
	}

	userGroup := strings.TrimSpace(relayInfo.UserGroup)
	if userGroup == "" {
		userGroup = strings.TrimSpace(relayInfo.UsingGroup)
	}
	policy, ok := operation_setting.ResolveViolationFeePolicy(userGroup)
	if !ok {
		return false
	}

	charge, err := model.ApplyViolationFee(model.ViolationFeeChargeInput{
		UserID:    relayInfo.UserId,
		RequestID: ctx.GetString(common.RequestIdKey),
		Policy:    policy,
		Group:     userGroup,
		ErrorCode: string(types.ErrorCodeViolationFeeUsagePolicy),
	})
	if err != nil {
		logger.LogError(ctx, fmt.Sprintf("failed to charge violation fee: %s", err.Error()))
		return false
	}
	if charge.AlreadyExist {
		return charge.Record.ChargedQuota > 0
	}
	if charge.Record.ChargedQuota <= 0 {
		return false
	}
	model.UpdateUserUsedQuotaAndRequestCount(relayInfo.UserId, charge.Record.ChargedQuota)

	useTimeSeconds := time.Now().Unix() - relayInfo.StartTime.Unix()
	tokenName := ctx.GetString("token_name")
	oai := apiErr.ToOpenAIError()

	other := map[string]any{
		"violation_fee":        true,
		"violation_fee_code":   string(types.ErrorCodeViolationFeeUsagePolicy),
		"fee_quota":            charge.Record.ChargedQuota,
		"requested_fee_quota":  charge.Record.RequestedQuota,
		"base_amount":          charge.Record.RequestedAmountUSD,
		"charged_amount":       charge.Record.ChargedAmountUSD,
		"group":                relayInfo.UsingGroup,
		"occurrence":           charge.Record.Occurrence,
		"period_ends_at":       charge.Record.PeriodEndsAt,
		"status_code":          apiErr.StatusCode,
		"upstream_error_type":  oai.Type,
		"upstream_error_code":  fmt.Sprintf("%v", oai.Code),
		"violation_fee_marker": CSAMViolationMarker,
	}

	model.RecordConsumeLog(ctx, relayInfo.UserId, model.RecordConsumeLogParams{
		ChannelId:      relayInfo.ChannelId,
		ModelName:      relayInfo.OriginModelName,
		TokenName:      tokenName,
		Quota:          charge.Record.ChargedQuota,
		Content:        "Violation fee charged",
		TokenId:        relayInfo.TokenId,
		UseTimeSeconds: int(useTimeSeconds),
		IsStream:       relayInfo.IsStream,
		Group:          relayInfo.UsingGroup,
		Other:          other,
	})

	return true
}
