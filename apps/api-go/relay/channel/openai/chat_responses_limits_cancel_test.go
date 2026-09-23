package openai

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/constant"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relay/helper"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestChatResponsesOversizedHTTPEvent(t *testing.T) {
	previousLimit, previousTimeout := constant.MaxResponseBodyMB, constant.StreamingTimeout
	constant.MaxResponseBodyMB, constant.StreamingTimeout = 1, 5
	t.Cleanup(func() {
		constant.MaxResponseBodyMB, constant.StreamingTimeout = previousLimit, previousTimeout
	})
	for _, started := range []bool{false, true} {
		t.Run(fmt.Sprintf("started=%t", started), func(t *testing.T) {
			released := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(released)
				w.Header().Set("Content-Type", "text/event-stream")
				if started {
					fmt.Fprint(w, chatResponseFrame(`{"index":0,"delta":{"content":"retained partial"}}`))
				}
				// Each line is small: exceed the assembled-event budget, not bufio's line limit.
				for range 1025 {
					if _, err := fmt.Fprintf(w, "data: %s\n", strings.Repeat("x", 1024)); err != nil {
						return
					}
				}
				w.(http.Flusher).Flush()
				<-r.Context().Done()
			}))
			defer server.Close()
			defer server.CloseClientConnections()
			client := server.Client()
			client.Timeout = 10 * time.Second
			response, err := client.Get(server.URL)
			require.NoError(t, err)
			c, recorder, _, info := newResponsesChatTestContext(t, "", true)
			_, apiErr := OaiChatToResponsesStreamHandler(c, info, response)
			require.Equal(t, relaycommon.StreamEndReasonScannerErr, info.StreamStatus.EndReason)
			require.ErrorIs(t, info.StreamStatus.EndError, helper.ErrSSEEventTooLarge)
			if started {
				require.Nil(t, apiErr)
				require.Equal(t, 1, strings.Count(recorder.Body.String(), "event: response.failed\n"))
				require.Contains(t, recorder.Body.String(), "retained partial")
				require.Contains(t, recorder.Body.String(), "upstream_stream_interrupted")
			} else {
				require.NotNil(t, apiErr)
				require.Equal(t, http.StatusBadGateway, apiErr.StatusCode)
				require.Empty(t, recorder.Body.String())
			}
			require.NotContains(t, recorder.Body.String(), "response.completed")
			require.NotContains(t, recorder.Body.String(), "response.output_item.done")
			require.NotContains(t, recorder.Body.String(), strings.Repeat("x", 64))
			select {
			case <-released:
			case <-time.After(2 * time.Second):
				t.Fatal("oversized upstream was not released")
			}
		})
	}
}

func TestChatResponsesDownstreamCancelClosesIndependentUpstream(t *testing.T) {
	previous := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = previous })
	released := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(released)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, chatResponseFrame(`{"index":0,"delta":{"reasoning_content":"second"}}`))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	defer server.CloseClientConnections()
	// The transport does not receive downstreamCtx. Only handler cleanup can close it.
	client := server.Client()
	client.Timeout = 10 * time.Second
	response, err := client.Get(server.URL)
	require.NoError(t, err)
	downstreamCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, recorder, _, info := newResponsesChatTestContext(t, "", true)
	c, _ := gin.CreateTestContext(&cancelReasoningWriter{ResponseRecorder: recorder, cancel: cancel})
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(downstreamCtx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		OaiChatToResponsesStreamHandler(c, info, response)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		_ = response.Body.Close()
		<-done
		t.Fatal("handler did not return promptly after downstream cancellation")
	}
	require.ErrorIs(t, downstreamCtx.Err(), context.Canceled)
	require.Contains(t, recorder.Body.String(), `"delta":"second"`)
	for _, terminal := range []string{"response.completed", "response.failed", "response.incomplete", "response.output_item.done", "[DONE]"} {
		require.NotContains(t, recorder.Body.String(), terminal)
	}
	select {
	case <-released:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not close independent upstream on cancellation")
	}
}
