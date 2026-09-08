package openai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Native Responses providers own tool discovery. Parsing their output for
// billing must not rewrite namespace metadata or dynamic tool declarations.
func TestOaiResponsesHandlersPreserveDynamicTools(t *testing.T) {
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })

	searchCall := `{"type":"tool_search_call","id":"ts_1","call_id":"search_1","status":"completed","execution":"client","arguments":{"query":"read project files","limit":2}}`
	searchOutput := `{"type":"tool_search_output","id":"tso_1","call_id":"search_1","status":"completed","tools":[{"type":"namespace","name":"workspace","description":"Project files","tools":[{"type":"function","name":"read_file","description":"Read a file","parameters":{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]},"defer_loading":true}]}]}`
	functionCall := `{"type":"function_call","id":"fc_1","call_id":"call_1","status":"completed","namespace":"workspace","name":"read_file","arguments":"{\"path\":\"README.md\"}"}`
	responseBody := `{"id":"resp_tools","object":"response","model":"gpt-test","status":"completed","output":[` + searchCall + `,` + searchOutput + `,` + functionCall + `],"usage":{"input_tokens":17,"output_tokens":9,"total_tokens":26,"input_tokens_details":{"cached_tokens":5},"output_tokens_details":{"reasoning_tokens":3}}}`
	events := []string{
		`{"type":"response.output_item.added","sequence_number":0,"output_index":0,"item":` + searchCall + `}`,
		`{"type":"response.output_item.done","sequence_number":1,"output_index":0,"item":` + searchCall + `}`,
		`{"type":"response.output_item.done","sequence_number":2,"output_index":1,"item":` + searchOutput + `}`,
		`{"type":"response.output_item.done","sequence_number":3,"output_index":2,"item":` + functionCall + `}`,
		`{"type":"response.completed","sequence_number":4,"response":` + responseBody + `}`,
	}

	for _, streaming := range []bool{false, true} {
		name := "json"
		if streaming {
			name = "sse"
		}
		t.Run(name, func(t *testing.T) {
			body, contentType := responseBody, "application/json"
			if streaming {
				body = "data: " + strings.Join(events, "\n\ndata: ") + "\n\ndata: [DONE]\n\n"
				contentType = "text/event-stream"
			}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			c.Set(common.RequestIdKey, "native-responses-tools-test")
			info := &relaycommon.RelayInfo{
				OriginModelName: "gpt-test",
				RelayFormat:     types.RelayFormatOpenAIResponses,
				IsStream:        streaming,
				DisablePing:     true,
				ChannelMeta:     &relaycommon.ChannelMeta{UpstreamModelName: "gpt-test"},
			}
			resp := &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     http.Header{"Content-Type": []string{contentType}},
			}
			var usage *dto.Usage
			var apiErr *types.NewAPIError
			if streaming {
				usage, apiErr = OaiResponsesStreamHandler(c, info, resp)
			} else {
				usage, apiErr = OaiResponsesHandler(c, info, resp)
			}
			require.Nil(t, apiErr)
			require.NotNil(t, usage)
			require.Equal(t, 17, usage.PromptTokens)
			require.Equal(t, 9, usage.CompletionTokens)
			require.Equal(t, 26, usage.TotalTokens)
			require.Equal(t, 5, usage.PromptTokensDetails.CachedTokens)
			require.Equal(t, 3, usage.CompletionTokenDetails.ReasoningTokens)
			require.Equal(t, contentType, recorder.Header().Get("Content-Type"))
			if !streaming {
				require.Equal(t, responseBody, recorder.Body.String())
				return
			}
			var forwarded []string
			for _, line := range strings.Split(recorder.Body.String(), "\n") {
				if payload, ok := strings.CutPrefix(line, "data: "); ok {
					forwarded = append(forwarded, payload)
				}
			}
			require.Equal(t, events, forwarded)
		})
	}
}
