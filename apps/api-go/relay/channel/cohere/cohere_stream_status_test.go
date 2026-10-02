package cohere

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const cohereStatusText = "{\"event_type\":\"text-generation\",\"text\":\"partial output\"}\n"

func cohereStatusTerminal(reason string) string {
	return fmt.Sprintf("{\"event_type\":\"stream-end\",\"is_finished\":true,\"finish_reason\":%q,\"response\":{\"meta\":{\"billed_units\":{\"input_tokens\":13,\"output_tokens\":7}}}}\n", reason)
}

func newCohereStatusContext(writer http.ResponseWriter, ctx context.Context) (*gin.Context, *relaycommon.RelayInfo) {
	c, _ := gin.CreateTestContext(writer)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(ctx)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "command-r"}}
	info.SetEstimatePromptTokens(12)
	return c, info
}

func TestCohereStreamStatusTracksTerminalOutcome(t *testing.T) {
	for _, reason := range []string{"COMPLETE", "MAX_TOKENS", "ERROR", "ERROR_TOXIC"} {
		t.Run(reason, func(t *testing.T) {
			writer := httptest.NewRecorder()
			c, info := newCohereStatusContext(writer, context.Background())
			usage, apiErr := cohereStreamHandler(c, info, &http.Response{Body: io.NopCloser(strings.NewReader(cohereStatusText + cohereStatusTerminal(reason)))})
			require.Nil(t, apiErr)
			require.Nil(t, info.StreamStatus)
			require.Equal(t, 13, usage.PromptTokens)
			require.Equal(t, 7, usage.CompletionTokens)
			require.NotNil(t, info.RateLimitStreamStatus)
			require.Equal(t, relaycommon.StreamEndReasonEOF, info.RateLimitStreamStatus.EndReason)
			require.Equal(t, strings.HasPrefix(reason, "ERROR"), info.RateLimitStreamStatus.HasErrors())
			require.Equal(t, 1, strings.Count(writer.Body.String(), "data: [DONE]"))
		})
	}
}

type cohereStatusReadError struct{ err error }

func (r cohereStatusReadError) Read([]byte) (int, error) { return 0, r.err }

func TestCohereStreamStatusRejectsInterruptedAndInvalidResponses(t *testing.T) {
	readErr := errors.New("upstream transport interrupted")
	tests := []struct {
		name      string
		body      io.Reader
		endReason relaycommon.StreamEndReason
		measured  bool
	}{
		{"invalid JSON before terminal", strings.NewReader("invalid JSON\n" + cohereStatusTerminal("COMPLETE")), relaycommon.StreamEndReasonEOF, true},
		{"scanner error after terminal", io.MultiReader(strings.NewReader(cohereStatusText+cohereStatusTerminal("COMPLETE")), cohereStatusReadError{err: readErr}), relaycommon.StreamEndReasonScannerErr, true},
		{"truncated stream", strings.NewReader(cohereStatusText), relaycommon.StreamEndReasonEOF, false},
		{"empty stream", strings.NewReader(""), relaycommon.StreamEndReasonEOF, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writer := httptest.NewRecorder()
			c, info := newCohereStatusContext(writer, context.Background())
			usage, apiErr := cohereStreamHandler(c, info, &http.Response{Body: io.NopCloser(tt.body)})
			require.Nil(t, apiErr)
			require.Nil(t, info.StreamStatus)
			require.NotNil(t, usage)
			require.NotNil(t, info.RateLimitStreamStatus)
			require.Equal(t, tt.endReason, info.RateLimitStreamStatus.EndReason)
			if tt.endReason == relaycommon.StreamEndReasonScannerErr {
				require.ErrorIs(t, info.RateLimitStreamStatus.EndError, readErr)
			} else {
				require.True(t, info.RateLimitStreamStatus.HasErrors())
			}
			if tt.measured {
				require.Equal(t, 13, usage.PromptTokens)
				require.Equal(t, 7, usage.CompletionTokens)
			} else {
				require.Equal(t, 12, usage.PromptTokens)
			}
		})
	}
}

type cohereStatusFailedWriter struct {
	*httptest.ResponseRecorder
	failDone bool
	short    bool
	writes   int
}

func (w *cohereStatusFailedWriter) Write(data []byte) (int, error) {
	w.writes++
	if !w.failDone || strings.Contains(string(data), "data: [DONE]") {
		if w.short {
			return len(data) - 1, nil
		}
		return 0, io.ErrClosedPipe
	}
	return w.ResponseRecorder.Write(data)
}

func TestCohereStreamStatusWriteFailurePreservesUsage(t *testing.T) {
	for _, mode := range []string{"first event", "short write", "done event"} {
		t.Run(mode, func(t *testing.T) {
			writer := &cohereStatusFailedWriter{ResponseRecorder: httptest.NewRecorder(), failDone: mode == "done event", short: mode == "short write"}
			c, info := newCohereStatusContext(writer, context.Background())
			usage, apiErr := cohereStreamHandler(c, info, &http.Response{Body: io.NopCloser(strings.NewReader(cohereStatusText + cohereStatusTerminal("COMPLETE")))})
			require.Nil(t, apiErr)
			require.Nil(t, info.StreamStatus)
			require.Equal(t, 13, usage.PromptTokens)
			require.Equal(t, 7, usage.CompletionTokens)
			require.Equal(t, relaycommon.StreamEndReasonClientGone, info.RateLimitStreamStatus.EndReason)
			require.True(t, info.RateLimitStreamStatus.HasErrors())
			if writer.short {
				require.ErrorIs(t, info.RateLimitStreamStatus.EndError, io.ErrShortWrite)
			} else {
				require.ErrorIs(t, info.RateLimitStreamStatus.EndError, io.ErrClosedPipe)
			}
			if !writer.failDone {
				require.Equal(t, 1, writer.writes)
			}
		})
	}
}

type cohereStatusBlockingBody struct {
	readStarted chan struct{}
	closed      chan struct{}
	readOnce    sync.Once
	closeOnce   sync.Once
}

func (b *cohereStatusBlockingBody) Read([]byte) (int, error) {
	b.readOnce.Do(func() { close(b.readStarted) })
	<-b.closed
	return 0, io.ErrClosedPipe
}

func (b *cohereStatusBlockingBody) Close() error {
	b.closeOnce.Do(func() { close(b.closed) })
	return nil
}

func TestCohereStreamStatusCancellationClosesBlockedUpstream(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	body := &cohereStatusBlockingBody{readStarted: make(chan struct{}), closed: make(chan struct{})}
	defer body.Close()
	writer := httptest.NewRecorder()
	c, info := newCohereStatusContext(writer, ctx)
	result := make(chan struct {
		usage  *dto.Usage
		apiErr *types.NewAPIError
	}, 1)
	go func() {
		usage, apiErr := cohereStreamHandler(c, info, &http.Response{Body: body})
		result <- struct {
			usage  *dto.Usage
			apiErr *types.NewAPIError
		}{usage, apiErr}
	}()
	select {
	case <-body.readStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("scanner did not begin reading")
	}
	cancel()
	select {
	case outcome := <-result:
		require.Nil(t, outcome.apiErr)
		require.Nil(t, info.StreamStatus)
		require.NotNil(t, outcome.usage)
		require.Equal(t, relaycommon.StreamEndReasonClientGone, info.RateLimitStreamStatus.EndReason)
		require.ErrorIs(t, info.RateLimitStreamStatus.EndError, context.Canceled)
		require.Empty(t, writer.Body.String())
		select {
		case <-body.closed:
		default:
			t.Fatal("cancelled request left upstream body open")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled stream did not return")
	}
}
