package service

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestShouldRetryRelayErrorSpecificChannelSkipsChannelError(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("specific_channel_id", "1")
	apiErr := types.NewError(errors.New("channel failed"), types.ErrorCodeChannelNoAvailableKey)
	assert.False(t, ShouldRetryRelayError(c, apiErr, 1))
}

func TestShouldRetryRelayErrorRetiresOnlyExplicitHTTPStreamAttempt(t *testing.T) {
	for _, flag := range []constant.ContextKey{constant.ContextKeyHTTPStreamCommitted, constant.ContextKeyHTTPStreamDownstreamFailure} {
		t.Run(string(flag), func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			apiErr := types.NewError(errors.New("channel failed"), types.ErrorCodeChannelNoAvailableKey)
			common.SetContextKey(c, flag, true)
			assert.False(t, ShouldRetryRelayError(c, apiErr, 1))
		})
	}
	// A WebSocket handshake or internal recorder can already be written while
	// its upstream attempt still has a safe retry boundary.
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Writer.WriteHeaderNow()
	apiErr := types.NewErrorWithStatusCode(errors.New("upstream timeout"), types.ErrorCodeUpstreamTimeout, 504)
	assert.True(t, c.Writer.Written())
	assert.True(t, ShouldRetryRelayError(c, apiErr, 1))
}

func TestDownstreamStreamFailureDoesNotPenalizeProvider(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(c, constant.ContextKeyHTTPStreamDownstreamFailure, true)
	common.SetContextKey(c, constant.ContextKeyUpstreamChannelFailure, true)
	apiErr := types.NewErrorWithStatusCode(errors.New("downstream flush unavailable"), types.ErrorCodeBadResponse, 502)
	assert.False(t, ShouldExcludeChannelForRetry(c, apiErr))
	// No DB/cache setup is needed: a client transport failure must leave before
	// provider logging or asynchronous disable actions are invoked.
	ProcessChannelError(c, *types.NewChannelError(1, 1, "provider", false, "", true), apiErr)
}

func TestShouldRetryRelayErrorEmptySpecificChannelAllowsRetry(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("specific_channel_id", "")
	apiErr := types.NewError(errors.New("channel failed"), types.ErrorCodeChannelNoAvailableKey)
	assert.True(t, ShouldRetryRelayError(c, apiErr, 1))
}

func TestShouldRetryRelayErrorRetriesUpstreamTimeout(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	apiErr := types.NewErrorWithStatusCode(
		errors.New("upstream timed out"),
		types.ErrorCodeUpstreamTimeout,
		504,
	)
	assert.True(t, ShouldRetryRelayError(c, apiErr, 1))
}

func TestShouldRetryRelayErrorSkipsClientDisconnect(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil).WithContext(ctx)
	cancel()
	apiErr := types.NewErrorWithStatusCode(
		errors.New("client closed request"),
		types.ErrorCodeClientClosedRequest,
		499,
	)
	assert.False(t, ShouldRetryRelayError(c, apiErr, 1))
}
