package relay

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/relay/channel"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relay/helper"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
)

func SystemOneHelper(c *gin.Context, info *relaycommon.RelayInfo) *types.NewAPIError {
	info.InitChannelMeta(c)
	if info.ChannelType != constant.ChannelTypeTypeSafe && info.ChannelType != constant.ChannelTypeNewAPI {
		return unsupportedEndpointAPIError(c, channel.NewUnsupportedEndpointError(constant.GetChannelTypeName(info.ChannelType), channel.EndpointSystemOne))
	}
	request, ok := info.Request.(*dto.SystemOneRequest)
	if !ok {
		return types.NewErrorWithStatusCode(fmt.Errorf("invalid request type, expected *dto.SystemOneRequest, got %T", info.Request), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	if err := helper.ModelMappedHelper(c, info, request); err != nil {
		return types.NewError(err, types.ErrorCodeChannelModelMappedError, types.ErrOptionWithSkipRetry())
	}
	adaptor := GetAdaptor(info.ApiType)
	if err := ensureAdaptorSupportsEndpoint(c, adaptor, channel.EndpointSystemOne); err != nil {
		return err
	}
	converter, ok := adaptor.(channel.SystemOneConverter)
	if !ok {
		return unsupportedEndpointAPIError(c, channel.NewUnsupportedEndpointError(adaptor.GetChannelName(), channel.EndpointSystemOne))
	}
	adaptor.Init(info)
	converted, err := converter.ConvertSystemOneRequest(c, info, request)
	if err != nil {
		return convertRequestAPIError(c, err)
	}
	jsonData, err := common.Marshal(converted)
	if err != nil {
		return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	if len(info.ParamOverride) > 0 {
		jsonData, err = relaycommon.ApplyParamOverrideWithRelayInfo(jsonData, info)
		if err != nil {
			return newAPIErrorFromParamOverride(err)
		}
		// Channel overrides are allowed, but cannot enable streaming, introduce
		// credentials, or switch to a model outside the vendor's declared list.
		var overridden dto.SystemOneRequest
		if err = common.Unmarshal(jsonData, &overridden); err == nil {
			if overridden.Model != info.UpstreamModelName {
				err = errors.New("System One model overrides must use channel model mapping")
			} else {
				converted, err = converter.ConvertSystemOneRequest(c, info, &overridden)
			}
		}
		if err != nil {
			return convertRequestAPIError(c, err)
		}
		jsonData, err = common.Marshal(converted)
		if err != nil {
			return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		}
		// Response validation follows questions actually sent upstream.
		info.Request = &overridden
		defer func() { info.Request = request }()
	}
	body, closer, err := relaycommon.NewOutboundJSONBody(jsonData)
	if err != nil {
		return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	defer closer.Close()
	response, err := adaptor.DoRequest(c, info, body)
	if err != nil {
		var apiErr *types.NewAPIError
		if errors.As(err, &apiErr) {
			return apiErr
		}
		return types.NewOpenAIError(err, types.ErrorCodeDoRequestFailed, http.StatusInternalServerError)
	}
	httpResponse, ok := response.(*http.Response)
	if !ok || httpResponse == nil {
		return types.NewErrorWithStatusCode(errors.New("invalid TypeSafe HTTP response"), types.ErrorCodeBadResponse, http.StatusBadGateway, types.ErrOptionWithSkipRetry())
	}
	defer httpResponse.Body.Close()
	if httpResponse.StatusCode < http.StatusOK || httpResponse.StatusCode >= http.StatusMultipleChoices {
		apiErr := service.RelayErrorHandler(c.Request.Context(), httpResponse, false)
		service.ResetStatusCode(apiErr, c.GetString("status_code_mapping"))
		return apiErr
	}
	usage, apiErr := adaptor.DoResponse(c, httpResponse, info)
	if apiErr != nil {
		return apiErr
	}
	usageDTO, ok := usage.(*dto.Usage)
	if !ok || usageDTO == nil {
		return types.NewErrorWithStatusCode(errors.New("invalid TypeSafe usage result"), types.ErrorCodeBadResponse, http.StatusBadGateway, types.ErrOptionWithSkipRetry())
	}
	// The accepted, validated result has already been delivered. Settlement
	// errors are logged by the billing owner and must not replay this request.
	service.PostTextConsumeQuota(c, info, usageDTO, nil)
	return nil
}
