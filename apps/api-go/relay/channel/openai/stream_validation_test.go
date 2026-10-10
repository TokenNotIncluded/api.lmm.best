package openai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relay/helper"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type validationTrackedBody struct {
	io.Reader
	reads  int
	closed bool
}

func (b *validationTrackedBody) Read(data []byte) (int, error) { b.reads++; return b.Reader.Read(data) }
func (b *validationTrackedBody) Close() error                  { b.closed = true; return nil }

func TestOpenAIStreamRejectsExplicitNonSSEBeforeReadOrCommit(t *testing.T) {
	handlers := map[string]func(*gin.Context, *relaycommon.RelayInfo, *http.Response) (*dto.Usage, *types.NewAPIError){
		"chat":                       OaiStreamHandler,
		"responses":                  OaiResponsesStreamHandler,
		"chat to responses":          OaiChatToResponsesStreamHandler,
		"responses to chat":          OaiResponsesToChatStreamHandler,
		"buffered responses to chat": OaiResponsesToChatBufferedStreamHandler,
	}
	for name, handler := range handlers {
		for _, contentType := range []string{"application/json", "text/event-stream-malicious", "text/event-streaming; charset=utf-8"} {
			t.Run(name+"/"+contentType, func(t *testing.T) {
				writer := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(writer)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
				body := &validationTrackedBody{Reader: strings.NewReader(`{"error":"must not be forwarded"}`)}
				info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}, IsStream: true}
				usage, apiErr := handler(c, info, &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{contentType}}, Body: body})
				require.Nil(t, usage)
				require.NotNil(t, apiErr)
				require.Equal(t, http.StatusBadGateway, apiErr.StatusCode)
				require.Zero(t, body.reads)
				require.True(t, body.closed)
				require.False(t, c.Writer.Written())
				require.Empty(t, writer.Body.String())
				require.False(t, helper.HTTPStreamDownstreamFailed(c))
				require.True(t, service.ShouldRetryRelayError(c, apiErr, 1))
			})
		}
	}
}

func TestOaiStreamInvisiblePrefixIsBoundedBeforeCommit(t *testing.T) {
	previousTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = previousTimeout })
	for name, input := range map[string]string{
		"event count": strings.Repeat("data: {}\n\n", 1025),
		"bytes":       "data: {\"metadata\":\"" + strings.Repeat("x", 1<<20) + "\"}\n\n",
	} {
		t.Run(name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			body := &validationTrackedBody{Reader: strings.NewReader(input)}
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-test"}, FirstResponseTimeout: time.Second}
			usage, apiErr := OaiStreamHandler(c, info, &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: body})
			require.Nil(t, usage)
			require.NotNil(t, apiErr)
			require.Equal(t, http.StatusBadGateway, apiErr.StatusCode)
			require.False(t, c.Writer.Written())
			require.False(t, common.GetContextKeyBool(c, constant.ContextKeyHTTPStreamCommitted))
			require.True(t, body.closed)
		})
	}
}
