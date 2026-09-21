package relay

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/middleware"
	"github.com/LIghtJUNction/api.lmm.best/model"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestUnsupportedEndpointHandlersReturnDeterministicNonRetryableErrorsWithoutProviderRequests(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var providerRequests atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		providerRequests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer provider.Close()

	for _, tt := range []struct {
		name       string
		path       string
		format     types.RelayFormat
		request    dto.Request
		model      string
		call       func(*gin.Context, *relaycommon.RelayInfo) *types.NewAPIError
		wantClaude bool
	}{
		{
			name:       "claude messages",
			path:       "/v1/messages",
			format:     types.RelayFormatClaude,
			request:    &dto.ClaudeRequest{Model: "claude-model"},
			model:      "claude-model",
			call:       ClaudeHelper,
			wantClaude: true,
		},
		{
			name:    "rerank",
			path:    "/v1/rerank",
			format:  types.RelayFormatRerank,
			request: &dto.RerankRequest{Model: "rerank-model", Query: "q", Documents: []any{"d"}},
			model:   "rerank-model",
			call:    RerankHelper,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(response)
			c.Request = httptest.NewRequest(http.MethodPost, tt.path, nil)
			baseURL := provider.URL
			selected := &model.Channel{
				Id:      41,
				Type:    constant.ChannelTypeBaidu,
				Key:     "key",
				Status:  common.ChannelStatusEnabled,
				Name:    "baidu",
				BaseURL: &baseURL,
			}
			require.Nil(t, middleware.SetupContextForSelectedChannel(c, selected, tt.model))
			info, err := relaycommon.GenRelayInfo(c, tt.format, tt.request, nil)
			require.NoError(t, err)

			apiErr := tt.call(c, info)

			require.NotNil(t, apiErr)
			require.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
			require.Equal(t, types.ErrorCodeChannelUnsupportedEndpoint, apiErr.GetErrorCode())
			require.False(t, service.ShouldRetryRelayError(c, apiErr, 3))
			require.True(t, types.IsSkipRetryError(apiErr))
			if tt.wantClaude {
				envelope := apiErr.ToClaudeError()
				require.Equal(t, string(types.ErrorCodeChannelUnsupportedEndpoint), envelope.Code)
				require.Contains(t, envelope.Message, "does not support")
			} else {
				envelope := apiErr.ToOpenAIError()
				require.Equal(t, types.ErrorCodeChannelUnsupportedEndpoint, envelope.Code)
				require.Contains(t, envelope.Message, "does not support")
			}
		})
	}
	require.Zero(t, providerRequests.Load())
}
