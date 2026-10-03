package openai

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	relayconstant "github.com/LIghtJUNction/api.lmm.best/relay/constant"
	relaytypes "github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Observe the actual wire status, rather than only the handler's error value:
// forwarding an invisible role frame must not commit 200 before failover.
func TestOaiStreamRoleOnlyTimeoutKeepsHTTPUncommitted(t *testing.T) {
	previousTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = previousTimeout })
	upstreamClosed := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(upstreamClosed)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	t.Cleanup(upstream.Close)
	router := gin.New()
	router.POST("/v1/chat/completions", func(c *gin.Context) {
		request, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, upstream.URL, nil)
		if err != nil {
			c.Status(http.StatusInternalServerError)
			return
		}
		response, err := upstream.Client().Do(request)
		if err != nil {
			c.Status(http.StatusBadGateway)
			return
		}
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-test"},
			IsStream:    true, RelayMode: relayconstant.RelayModeChatCompletions,
			RelayFormat: relaytypes.RelayFormatOpenAI, DisablePing: true,
			FirstResponseTimeout: 100 * time.Millisecond,
		}
		_, apiErr := OaiStreamHandler(c, info, response)
		if apiErr != nil {
			c.Header("Content-Type", "application/json")
			c.JSON(apiErr.StatusCode, gin.H{"error": "upstream_timeout"})
		}
	})
	downstream := httptest.NewServer(router)
	t.Cleanup(downstream.Close)
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Post(downstream.URL+"/v1/chat/completions", "application/json", nil)
	require.NoError(t, err)
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusGatewayTimeout, response.StatusCode)
	require.Contains(t, response.Header.Get("Content-Type"), "application/json")
	require.NotContains(t, string(body), "assistant")
	select {
	case <-upstreamClosed:
	case <-time.After(time.Second):
		t.Fatal("timed-out upstream was not closed")
	}
}

func streamCommitGateway(t *testing.T, timeout time.Duration, upstreamHandler http.HandlerFunc) *httptest.Server {
	t.Helper()
	previous := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = previous })
	upstream := httptest.NewServer(upstreamHandler)
	t.Cleanup(upstream.Close)
	router := gin.New()
	router.POST("/v1/chat/completions", func(c *gin.Context) {
		request, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, upstream.URL, nil)
		if err != nil {
			c.Status(500)
			return
		}
		response, err := upstream.Client().Do(request)
		if err != nil {
			c.Status(502)
			return
		}
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-test"},
			IsStream:    true, RelayMode: relayconstant.RelayModeChatCompletions,
			RelayFormat: relaytypes.RelayFormatOpenAI, DisablePing: true,
			FirstResponseTimeout: timeout,
		}
		_, apiErr := OaiStreamHandler(c, info, response)
		if apiErr != nil {
			c.Header("Content-Type", "application/json")
			c.JSON(apiErr.StatusCode, gin.H{"error": "upstream_timeout"})
		}
	})
	downstream := httptest.NewServer(router)
	t.Cleanup(downstream.Close)
	return downstream
}

func TestOaiStreamDisabledTimeoutCommitsHeadersBeforeFrame(t *testing.T) {
	previous := common.OpenAIFirstOutputTimeout
	common.OpenAIFirstOutputTimeout = 0
	t.Cleanup(func() { common.OpenAIFirstOutputTimeout = previous })
	frameGate := make(chan struct{})
	gateway := streamCommitGateway(t, 0, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("X-Codex-Turn-State", "turn-state")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		select {
		case <-frameGate:
		case <-r.Context().Done():
			return
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ready\"}}]}\n\ndata: [DONE]\n\n")
	})
	client := &http.Client{Timeout: time.Second}
	response, err := client.Post(gateway.URL+"/v1/chat/completions", "application/json", nil)
	// Opening the gate only after Do returns proves no data frame was needed
	// for the real HTTP headers to arrive; release it even on a failed assertion.
	close(frameGate)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusOK, response.StatusCode)
	require.Equal(t, "text/event-stream", response.Header.Get("Content-Type"))
	require.Equal(t, "no-cache, no-transform", response.Header.Get("Cache-Control"))
	require.Equal(t, "no", response.Header.Get("X-Accel-Buffering"))
	require.Equal(t, "turn-state", response.Header.Get("X-Codex-Turn-State"))
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Contains(t, string(body), "ready")
}

func TestOaiStreamHeadersAndUsageOnlyTimeoutKeepHTTPUncommitted(t *testing.T) {
	for name, prefix := range map[string]string{
		"headers only": "",
		"usage only":   "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":13,\"completion_tokens\":7,\"total_tokens\":20}}\n\n",
	} {
		t.Run(name, func(t *testing.T) {
			gateway := streamCommitGateway(t, 100*time.Millisecond, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
				w.WriteHeader(http.StatusOK)
				io.WriteString(w, prefix)
				w.(http.Flusher).Flush()
				<-r.Context().Done()
			})
			response, err := (&http.Client{Timeout: time.Second}).Post(gateway.URL+"/v1/chat/completions", "application/json", nil)
			require.NoError(t, err)
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			require.NoError(t, err)
			require.Equal(t, http.StatusGatewayTimeout, response.StatusCode)
			require.Contains(t, response.Header.Get("Content-Type"), "application/json")
			require.NotContains(t, string(body), "prompt_tokens")
		})
	}
}

func TestOaiStreamNonSSEHeadersDoNotCommitSuccess(t *testing.T) {
	for _, timeout := range []time.Duration{0, 100 * time.Millisecond} {
		t.Run(timeout.String(), func(t *testing.T) {
			closed := make(chan struct{})
			gateway := streamCommitGateway(t, timeout, func(w http.ResponseWriter, r *http.Request) {
				defer close(closed)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				w.(http.Flusher).Flush()
				// Reject from the response headers, before a delayed JSON error
				// body can accidentally be treated as an empty SSE completion.
				<-r.Context().Done()
			})
			response, err := (&http.Client{Timeout: time.Second}).Post(gateway.URL+"/v1/chat/completions", "application/json", nil)
			require.NoError(t, err)
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			require.NoError(t, err)
			require.Equal(t, http.StatusBadGateway, response.StatusCode)
			require.Contains(t, response.Header.Get("Content-Type"), "application/json")
			require.NotContains(t, string(body), "[DONE]")
			select {
			case <-closed:
			case <-time.After(time.Second):
				t.Fatal("rejected non-SSE upstream was not closed")
			}
		})
	}
}

func TestOaiStreamFirstVisibleFramePreservesPrefixAndRetiresDeadline(t *testing.T) {
	for name, delta := range map[string]string{
		"content":   `{"content":"visible"}`,
		"reasoning": `{"reasoning_content":"visible"}`,
		"tool":      `{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"visible","arguments":"{}"}}]}`,
		"function":  `{"function_call":{"name":"visible","arguments":"{}"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			const prefix = "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n"
			gate := make(chan struct{})
			closed := make(chan struct{})
			gateway := streamCommitGateway(t, 100*time.Millisecond, func(w http.ResponseWriter, r *http.Request) {
				defer close(closed)
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, prefix)
				w.(http.Flusher).Flush()
				fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":%s}]}\n\n", delta)
				w.(http.Flusher).Flush()
				select {
				case <-gate:
				case <-r.Context().Done():
					return
				}
				fmt.Fprint(w, "data: [DONE]\n\n")
			})
			client := &http.Client{Timeout: 2 * time.Second}
			response, err := client.Post(gateway.URL+"/v1/chat/completions", "application/json", nil)
			if err != nil {
				close(gate)
			}
			require.NoError(t, err)
			defer response.Body.Close()
			require.Equal(t, http.StatusOK, response.StatusCode)
			// No new frame arrives while the original first-output timer expires.
			select {
			case <-closed:
				close(gate)
				t.Fatal("visible output did not retire the original deadline")
			case <-time.After(150 * time.Millisecond):
			}
			close(gate)
			body, err := io.ReadAll(response.Body)
			require.NoError(t, err)
			require.Contains(t, string(body), prefix)
			require.Contains(t, string(body), "visible")
			require.Contains(t, string(body), "[DONE]")
		})
	}
}

func TestOaiStreamClientDisconnectClosesCommittedUpstream(t *testing.T) {
	closed := make(chan struct{})
	gateway := streamCommitGateway(t, 0, func(w http.ResponseWriter, r *http.Request) {
		defer close(closed)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, gateway.URL+"/v1/chat/completions", nil)
	require.NoError(t, err)
	response, err := (&http.Client{Timeout: 2 * time.Second}).Do(request)
	require.NoError(t, err)
	response.Body.Close()
	cancel()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("client disconnect did not release upstream body")
	}
}
