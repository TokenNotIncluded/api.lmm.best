package relay

import (
	"net/http"
	"net/http/httptest"
	"testing"

	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestResponseModelWebSocketProviderFramesKeepFirstMismatch(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	info := &relaycommon.RelayInfo{OriginModelName: "client-model", ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"}}
	state := &responsesWSCallState{info: info, usage: &dto.Usage{PromptTokens: 20, CompletionTokens: 4, TotalTokens: 24}}
	session := &responsesWSSession{c: c, current: state}
	for _, frame := range []string{
		`{"type":"response.created","response":{"id":"resp_1","model":"openai/gpt-4o-2024-08-06"}}`,
		`{"type":"response.in_progress","response":{"id":"resp_1","model":"gpt-4o-mini"}}`,
		`{"type":"response.in_progress","response":{"id":"resp_1","model":"gpt-4o"}}`,
		`{"type":"response.in_progress","response":{"id":"resp_1"}}`,
	} {
		session.observeUpstreamMessage([]byte(frame))
	}
	require.Equal(t, &relaycommon.ResponseModel{RequestedModel: "client-model", UpstreamModel: "gpt-4o", ReturnedModel: "gpt-4o-mini"}, info.ResponseModel)
	require.Equal(t, "gpt-4o", info.UpstreamModelName)
	require.Equal(t, "resp_1", state.responseID)
	require.Equal(t, &dto.Usage{PromptTokens: 20, CompletionTokens: 4, TotalTokens: 24}, state.usage)
	other := service.GenerateTextOtherInfo(c, info, 1, 1, 1, 0, 1, 0, 1)
	require.Equal(t, *info.ResponseModel, other["response_model"])
}
