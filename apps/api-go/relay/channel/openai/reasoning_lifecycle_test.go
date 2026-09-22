package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type cancelReasoningWriter struct {
	*httptest.ResponseRecorder
	cancel context.CancelFunc
}

func (w *cancelReasoningWriter) Write(data []byte) (int, error) {
	n, err := w.ResponseRecorder.Write(data)
	if strings.Contains(string(data), `"delta":"second"`) {
		w.cancel()
	}
	return n, err
}

func TestReasoningSegmentsThroughHTTPChatToResponsesHandler(t *testing.T) {
	previous := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = previous })
	for _, canceled := range []bool{false, true} {
		t.Run(fmt.Sprintf("canceled=%t", canceled), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			released := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(released)
				w.Header().Set("Content-Type", "text/event-stream")
				for _, choice := range []string{
					`{"delta":{"reasoning_content":"first"}}`,
					`{"delta":{},"finish_reason":"tool_calls"}`,
					`{"delta":{"reasoning_content":"second"}}`,
				} {
					fmt.Fprintf(w, "data: {\"id\":\"chat_test\",\"model\":\"gpt-test\",\"choices\":[%s]}\n\n", choice)
				}
				w.(http.Flusher).Flush()
				if canceled {
					<-r.Context().Done()
					return
				}
				fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			}))
			defer server.Close()
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
			require.NoError(t, err)
			response, err := server.Client().Do(request)
			require.NoError(t, err)
			c, recorder, _, info := newResponsesChatTestContext(t, "", true)
			if canceled {
				c, _ = gin.CreateTestContext(&cancelReasoningWriter{ResponseRecorder: recorder, cancel: cancel})
			}
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(ctx)
			_, apiErr := OaiChatToResponsesStreamHandler(c, info, response)
			if !canceled {
				require.Nil(t, apiErr)
				closed := map[string]dto.ResponsesOutput{}
				for _, line := range strings.Split(recorder.Body.String(), "\n") {
					if !strings.HasPrefix(line, "data: ") {
						continue
					}
					var event dto.ResponsesStreamResponse
					require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event))
					if event.Type == "response.output_item.done" && event.Item.Type == "reasoning" {
						closed[event.Item.ID] = *event.Item
					}
					if event.Type == "response.reasoning_summary_text.delta" {
						_, exists := closed[event.ItemID]
						require.False(t, exists)
					}
					if event.Type == "response.completed" {
						require.Len(t, event.Response.Output, 2)
						for index, item := range event.Response.Output {
							require.Equal(t, closed[item.ID], item)
							require.Equal(t, []string{"first", "second"}[index], item.Content[0].Text)
						}
					}
				}
				require.Len(t, closed, 2)
			} else {
				require.Contains(t, recorder.Body.String(), `"delta":"second"`)
				require.NotContains(t, recorder.Body.String(), "event: response.completed")
				require.Equal(t, 1, strings.Count(recorder.Body.String(), "event: response.output_item.done"), "cancellation must not manufacture a done event for the active segment")
			}
			select {
			case <-released:
			case <-time.After(2 * time.Second):
				t.Fatal("upstream request was not released")
			}
		})
	}
}
