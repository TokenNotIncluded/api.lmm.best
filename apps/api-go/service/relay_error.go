package service

import (
	"fmt"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/logger"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"

	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-gonic/gin"
)

func ShouldRetryRelayError(c *gin.Context, apiErr *types.NewAPIError, retryTimes int) bool {
	if apiErr == nil || ShouldSkipRetryAfterChannelAffinityFailure(c) {
		return false
	}
	if apiErr.GetErrorCode() == types.ErrorCodeChannelUnsupportedEndpoint {
		return false
	}
	if c != nil && c.Request != nil && c.Request.Context().Err() != nil {
		return false
	}
	if c != nil {
		if common.GetContextKeyString(c, constant.ContextKeyTokenSpecificChannelId) != "" {
			return false
		}
	}
	if types.IsChannelError(apiErr) {
		return true
	}
	if retryTimes <= 0 {
		return false
	}
	if c != nil && apiErr.StatusCode == 400 &&
		(common.GetContextKeyBool(c, constant.ContextKeyUpstreamCapabilityMismatch) ||
			common.GetContextKeyBool(c, constant.ContextKeyUpstreamUnsupportedParameter)) {
		return true
	}
	if types.IsSkipRetryError(apiErr) {
		return false
	}
	if apiErr.GetErrorCode() == types.ErrorCodeUpstreamTimeout {
		return retryTimes > 0
	}
	code := apiErr.StatusCode
	if code >= 200 && code < 300 {
		return false
	}
	if code < 100 || code > 599 {
		return true
	}
	if operation_setting.IsAlwaysSkipRetryCode(apiErr.GetErrorCode()) {
		return false
	}
	return operation_setting.ShouldRetryByStatusCode(code)
}

// ShouldExcludeChannelForRetry reports whether the current channel should be
// omitted from the next attempt. This is intentionally request-scoped: a
// transient upstream 503 or an explicit capability mismatch must not trigger a
// persistent channel ban.
func ShouldExcludeChannelForRetry(c *gin.Context, _ *types.NewAPIError) bool {
	if c == nil {
		return false
	}
	return common.GetContextKeyBool(c, constant.ContextKeyUpstreamChannelFailure) ||
		common.GetContextKeyBool(c, constant.ContextKeyUpstreamCapabilityMismatch) ||
		common.GetContextKeyBool(c, constant.ContextKeyUpstreamUnsupportedParameter)
}

func ProcessChannelError(c *gin.Context, channelError types.ChannelError, apiErr *types.NewAPIError) {
	if apiErr == nil {
		return
	}
	logger.LogError(c, fmt.Sprintf("channel error (channel #%d, status code: %d): %s", channelError.ChannelId, apiErr.StatusCode, common.LocalLogPreview(apiErr.MaskSensitiveErrorWithStatusCode())))
	if ShouldDisableChannel(apiErr) && channelError.AutoBan && !ShouldExcludeChannelForRetry(c, apiErr) {
		gopool.Go(func() {
			DisableChannel(channelError, apiErr.ErrorWithStatusCode())
		})
	}

	if !constant.ErrorLogEnabled || !types.IsRecordErrorLog(apiErr) || c == nil {
		return
	}
	other := map[string]interface{}{
		"error_type":   apiErr.GetErrorType(),
		"error_code":   apiErr.GetErrorCode(),
		"status_code":  apiErr.StatusCode,
		"channel_id":   c.GetInt("channel_id"),
		"channel_name": c.GetString("channel_name"),
		"channel_type": c.GetInt("channel_type"),
	}
	if c.Request != nil && c.Request.URL != nil {
		other["request_path"] = c.Request.URL.Path
	}
	adminInfo := map[string]interface{}{"use_channel": c.GetStringSlice("use_channel")}
	if common.GetContextKeyBool(c, constant.ContextKeyChannelIsMultiKey) {
		adminInfo["is_multi_key"] = true
		adminInfo["multi_key_index"] = common.GetContextKeyInt(c, constant.ContextKeyChannelMultiKeyIndex)
	}
	AppendChannelAffinityAdminInfo(c, adminInfo)
	other["admin_info"] = adminInfo
	startTime := common.GetContextKeyTime(c, constant.ContextKeyRequestStartTime)
	if startTime.IsZero() {
		startTime = time.Now()
	}
	model.RecordErrorLog(c, c.GetInt("id"), c.GetInt("channel_id"), c.GetString("original_model"), c.GetString("token_name"), apiErr.MaskSensitiveErrorWithStatusCode(), c.GetInt("token_id"), int(time.Since(startTime).Seconds()), common.GetContextKeyBool(c, constant.ContextKeyIsStream), c.GetString("group"), other)
}
