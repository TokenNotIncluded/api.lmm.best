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

func TestConvertedRoleOnlyStreamStillRequiresVisibleOutput(t *testing.T) {
	previousTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = previousTimeout })
	for _, format := range []relaytypes.RelayFormat{relaytypes.RelayFormatClaude, relaytypes.RelayFormatGemini} {
		t.Run(string(format), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			reader, writer := io.Pipe()
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(ctx)
			info := &relaycommon.RelayInfo{
				ChannelMeta:          &relaycommon.ChannelMeta{UpstreamModelName: "gpt-test"},
				OriginModelName:      "gpt-test",
				IsStream:             true,
				RelayMode:            relayconstant.RelayModeChatCompletions,
				RelayFormat:          format,
				DisablePing:          true,
				FirstResponseTimeout: 200 * time.Millisecond,
				ClaudeConvertInfo:    &relaycommon.ClaudeConvertInfo{},
			}
			result := make(chan *relaytypes.NewAPIError, 1)
			finished := make(chan struct{})
			t.Cleanup(func() {
				cancel()
				_ = reader.Close()
				_ = writer.Close()
				select {
				case <-finished:
				case <-time.After(2 * time.Second):
					t.Error("role-only stream goroutine did not stop")
				}
			})
			go func() {
				defer close(finished)
				_, err := OaiStreamHandler(c, info, &http.Response{
					StatusCode: http.StatusOK, Body: reader,
					Header: http.Header{"Content-Type": []string{"text/event-stream"}},
				})
				result <- err
			}()
			_, err := fmt.Fprint(writer, "data: {\"id\":\"chatcmpl-role-only\",\"model\":\"gpt-test\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"\"},\"finish_reason\":null}]}\n\n")
			require.NoError(t, err)
			select {
			case err := <-result:
				require.ErrorContains(t, err, "upstream first response timeout")
			case <-ctx.Done():
				t.Fatal("an empty converted stream incorrectly disabled the first-output timer")
			}
			require.False(t, info.FirstResponseObserved)
		})
	}
}
