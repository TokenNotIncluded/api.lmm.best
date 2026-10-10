package palm

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relay/helper"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type commitFailureWriter struct {
	*httptest.ResponseRecorder
	flushes int
}

func (w *commitFailureWriter) FlushError() error {
	w.flushes++
	return io.ErrClosedPipe
}

type commitTrackedBody struct {
	reads  atomic.Int32
	closed atomic.Bool
}

func (b *commitTrackedBody) Read([]byte) (int, error) {
	b.reads.Add(1)
	return 0, io.EOF
}

func (b *commitTrackedBody) Close() error {
	b.closed.Store(true)
	return nil
}

func TestStreamHeaderCommitFailureClosesBodyBeforeReading(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(map[bool]string{false: "flush failure", true: "already canceled"}[canceled], func(t *testing.T) {
			writer := &commitFailureWriter{ResponseRecorder: httptest.NewRecorder()}
			c, _ := gin.CreateTestContext(writer)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if canceled {
				cancel()
			}
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(ctx)
			body := &commitTrackedBody{}
			response := &http.Response{Body: body}
			info := &relaycommon.RelayInfo{}
			apiErr, _ := palmStreamHandler(c, info, response)

			require.NotNil(t, apiErr)
			require.True(t, types.IsSkipRetryError(apiErr))
			require.True(t, body.closed.Load(), "failed commit must close the upstream body")
			require.Zero(t, body.reads.Load(), "failed commit must precede every upstream read")
			require.True(t, helper.HTTPStreamDownstreamFailed(c))
			require.False(t, common.GetContextKeyBool(c, constant.ContextKeyUpstreamChannelFailure))
			require.False(t, service.ShouldRetryRelayError(c, apiErr, 3))
			require.False(t, service.ShouldExcludeChannelForRetry(c, apiErr))
			require.Equal(t, relaycommon.StreamEndReasonClientGone, info.RateLimitStreamStatus.EndReason)
			require.Empty(t, writer.Body.String())
			if canceled {
				require.Zero(t, writer.flushes)
				require.ErrorIs(t, info.RateLimitStreamStatus.EndError, context.Canceled)
			} else {
				require.Equal(t, 1, writer.flushes)
				require.ErrorIs(t, info.RateLimitStreamStatus.EndError, io.ErrClosedPipe)
			}
		})
	}
}
