package openai

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/constant"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	relayconstant "github.com/LIghtJUNction/api.lmm.best/relay/constant"
	relaytypes "github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestConvertedStreamFirstVisibleOutputDisablesDeadline(t *testing.T) {
	previousTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = previousTimeout })
	previousMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(previousMode) })

	for _, format := range []relaytypes.RelayFormat{relaytypes.RelayFormatOpenAI, relaytypes.RelayFormatClaude, relaytypes.RelayFormatGemini} {
		t.Run(string(format), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			reader, writer := io.Pipe()
			downstream := &firstFlushWriter{ResponseRecorder: httptest.NewRecorder(), flushed: make(chan struct{})}
			c, _ := gin.CreateTestContext(downstream)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(ctx)
			info := &relaycommon.RelayInfo{
				ChannelMeta:          &relaycommon.ChannelMeta{UpstreamModelName: "gpt-test"},
				OriginModelName:      "gpt-test",
				IsStream:             true,
				RelayMode:            relayconstant.RelayModeChatCompletions,
				RelayFormat:          format,
				DisablePing:          true,
				ShouldIncludeUsage:   true,
				FirstResponseTimeout: 200 * time.Millisecond,
				ClaudeConvertInfo:    &relaycommon.ClaudeConvertInfo{},
			}
			done := make(chan *relaytypes.NewAPIError, 1)
			finished := make(chan struct{})
			t.Cleanup(func() {
				cancel()
				_ = reader.Close()
				_ = writer.Close()
				select {
				case <-finished:
				case <-time.After(2 * time.Second):
					t.Error("stream goroutine did not stop")
				}
			})
			go func() {
				defer close(finished)
				_, apiErr := OaiStreamHandler(c, info, &http.Response{
					StatusCode: http.StatusOK, Body: reader,
					Header: http.Header{"Content-Type": []string{"text/event-stream"}},
				})
				done <- apiErr
			}()
			first := `{"id":"chatcmpl-converted-deadline","object":"chat.completion.chunk","model":"gpt-test","choices":[{"index":0,"delta":{"role":"assistant","content":"hello"},"finish_reason":null}]}`
			_, err := fmt.Fprintf(writer, "data: %s\n\n", first)
			require.NoError(t, err)
			select {
			case <-downstream.flushed:
			case apiErr := <-done:
				t.Fatalf("stream ended before first output: %v", apiErr)
			case <-ctx.Done():
				t.Fatal("first output did not flush")
			}
			// A first-output deadline is not a total generation deadline. Once
			// hello was emitted, keep the stream alive while awaiting later data.
			select {
			case apiErr := <-done:
				t.Fatalf("visible output must disable the first-output deadline: %v", apiErr)
			case <-time.After(450 * time.Millisecond):
			case <-ctx.Done():
				t.Fatal("stream lifetime exceeded test deadline")
			}
			last := `{"id":"chatcmpl-converted-deadline","object":"chat.completion.chunk","model":"gpt-test","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`
			_, err = fmt.Fprintf(writer, "data: %s\n\ndata: [DONE]\n\n", last)
			require.NoError(t, err)
			require.NoError(t, writer.Close())
			select {
			case apiErr := <-done:
				require.Nil(t, apiErr)
			case <-ctx.Done():
				t.Fatal("complete stream did not finish")
			}
			require.True(t, info.FirstResponseObserved)
			require.Contains(t, downstream.Body.String(), "hello", "conversion must emit content, not just headers")
		})
	}
}
