package relay

import (
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relay/helper"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/decisions"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
)

// This is a gateway memory ceiling, not an OpenAI protocol limit.
const decisionsResponseLimit = 32 << 20

func decisionsRequestError(err error) *types.NewAPIError {
	return types.NewErrorWithStatusCode(err, types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
}

func decisionsResponseError(err error) *types.NewAPIError {
	return types.NewErrorWithStatusCode(err, types.ErrorCodeBadResponse, http.StatusBadGateway, types.ErrOptionWithSkipRetry())
}

func DecisionsHelper(c *gin.Context, info *relaycommon.RelayInfo) *types.NewAPIError {
	info.InitChannelMeta(c)
	// Only explicitly native OpenAI channels are admitted in this first patch.
	// Never silently translate this protocol for another provider.
	if info.ChannelType != constant.ChannelTypeOpenAI {
		return decisionsRequestError(errors.New("Decisions requires an OpenAI channel with native /v1/decisions support"))
	}
	if err := helper.ValidateDecisionsPriceGroup(c, info); err != nil {
		return decisionsRequestError(err)
	}
	if err := helper.RefreshDecisionsChannelPrice(info); err != nil {
		return decisionsRequestError(err)
	}
	if apiErr := service.PrepareTieredBillingForSelectedGroup(c, info); apiErr != nil {
		return apiErr
	}
	original, ok := info.Request.(*dto.DecisionsRequest)
	if !ok || original == nil {
		return decisionsRequestError(errors.New("invalid native Decisions request"))
	}
	if len(info.ParamOverride) > 0 {
		// Overrides can change image/input cost after prepayment. Do not apply
		// chat-oriented rules or send an unreserved body to this native endpoint.
		return decisionsRequestError(errors.New("Decisions does not support channel parameter overrides in this patch; use model mapping only"))
	}
	request := *original
	if err := helper.ModelMappedHelper(c, info, &request); err != nil {
		return types.NewError(err, types.ErrorCodeChannelModelMappedError, types.ErrOptionWithSkipRetry())
	}
	adaptor := GetAdaptor(info.ApiType)
	if adaptor == nil {
		return decisionsRequestError(fmt.Errorf("invalid API type %d", info.ApiType))
	}
	adaptor.Init(info)
	data, err := common.Marshal(&request)
	if err != nil {
		return decisionsRequestError(err)
	}
	body, closer, err := relaycommon.NewOutboundJSONBody(data)
	if err != nil {
		return decisionsRequestError(err)
	}
	defer closer.Close()

	// The existing adaptor supplies channel credentials and transport policy.
	// Do not invoke any chat conversion or chat response parser.
	previousPath := info.RequestURLPath
	info.RequestURLPath = decisions.Endpoint
	defer func() { info.RequestURLPath = previousPath }()
	result, err := adaptor.DoRequest(c, info, body)
	if err != nil {
		var apiError *types.NewAPIError
		if errors.As(err, &apiError) {
			return apiError
		}
		return types.NewErrorWithStatusCode(err, types.ErrorCodeDoRequestFailed, http.StatusBadGateway, types.ErrOptionWithSkipRetry())
	}
	response, ok := result.(*http.Response)
	if !ok || response == nil || response.Body == nil {
		return decisionsResponseError(errors.New("missing Decisions HTTP response"))
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		apiError := service.RelayErrorHandler(c.Request.Context(), response, false)
		service.ResetStatusCode(apiError, c.GetString("status_code_mapping"))
		return apiError
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, decisionsResponseLimit+1))
	if err != nil {
		return decisionsResponseError(err)
	}
	if len(raw) > decisionsResponseLimit {
		return decisionsResponseError(errors.New("Decisions response exceeds gateway response limit"))
	}
	inputTokens, err := decisions.InputUsage(raw, request.Questions)
	if err != nil {
		return decisionsResponseError(err)
	}
	// Cache/output details remain in the native response, but cannot introduce
	// chat output charges or cache discounts into input-only settlement.
	usage := &dto.Usage{PromptTokens: inputTokens, CompletionTokens: 0, TotalTokens: inputTokens}
	info.ResponsesUsageReported = true // Explicit zero is authoritative, not missing.
	c.Header("Cache-Control", "no-store")
	if id := response.Header.Get("x-request-id"); id != "" {
		c.Header("x-upstream-request-id", id)
	}
	c.Data(response.StatusCode, "application/json", raw)
	// No retry after delivery. The existing owner settles/refunds the reserve.
	service.PostTextConsumeQuota(c, info, usage, []string{"Native Decisions; input-only; pricing key: " + helper.DecisionsPriceKey(info.OriginModelName)})
	return nil
}
