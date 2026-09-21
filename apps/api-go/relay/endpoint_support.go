package relay

import (
	"net/http"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/relay/channel"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/gin-gonic/gin"
)

func unsupportedEndpointAPIError(c *gin.Context, err error) *types.NewAPIError {
	common.SetContextKey(c, constant.ContextKeyUpstreamCapabilityMismatch, true)
	return types.NewErrorWithStatusCode(
		err,
		types.ErrorCodeChannelUnsupportedEndpoint,
		http.StatusBadRequest,
		types.ErrOptionWithSkipRetry(),
	)
}

func ensureAdaptorSupportsEndpoint(c *gin.Context, adaptor channel.Adaptor, endpoint channel.Endpoint) *types.NewAPIError {
	if channel.SupportsEndpoint(adaptor, endpoint) {
		return nil
	}
	name := ""
	if adaptor != nil {
		name = adaptor.GetChannelName()
	}
	return unsupportedEndpointAPIError(c, channel.NewUnsupportedEndpointError(name, endpoint))
}

func convertRequestAPIError(c *gin.Context, err error) *types.NewAPIError {
	if channel.IsUnsupportedEndpointError(err) {
		return unsupportedEndpointAPIError(c, err)
	}
	return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
}
