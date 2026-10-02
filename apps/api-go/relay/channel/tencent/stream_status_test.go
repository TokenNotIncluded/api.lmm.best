package tencent

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type tencentStreamErrorReader struct {
	io.Reader
	err error
}

func (r tencentStreamErrorReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if errors.Is(err, io.EOF) {
		return n, r.err
	}
	return n, err
}

type tencentFailedStreamWriter struct {
	*httptest.ResponseRecorder
}

func (w tencentFailedStreamWriter) Write([]byte) (int, error) {
	return 0, io.ErrClosedPipe
}

func TestTencentStreamHandlerPublishesStreamOutcome(t *testing.T) {
	const event = `data: {"Choices":[{"Delta":{"Content":"partial"},"FinishReason":"stop"}]}` + "\n"
	readErr := errors.New("synthetic Tencent read failure")
	for _, test := range []struct {
		name   string
		data   string
		err    error
		cancel bool
		failed bool
		want   relaycommon.StreamEndReason
	}{
		{name: "normal EOF", data: event, want: relaycommon.StreamEndReasonEOF},
		{name: "invalid JSON", data: "data: {\n" + event, want: relaycommon.StreamEndReasonEOF},
		{name: "upstream error", data: `data: {"Error":{"Code":1,"Message":"synthetic upstream error"}}` + "\n" + event, want: relaycommon.StreamEndReasonEOF},
		{name: "read failed", data: event, err: readErr, want: relaycommon.StreamEndReasonScannerErr},
		{name: "client canceled", data: event, cancel: true, want: relaycommon.StreamEndReasonClientGone},
		{name: "write failed", data: event, failed: true, want: relaycommon.StreamEndReasonClientGone},
	} {
		t.Run(test.name, func(t *testing.T) {
			requestContext, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			if test.cancel {
				cancel()
			}
			var writer http.ResponseWriter = httptest.NewRecorder()
			if test.failed {
				writer = &tencentFailedStreamWriter{ResponseRecorder: httptest.NewRecorder()}
			}
			c, _ := gin.CreateTestContext(writer)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(requestContext)
			info := &relaycommon.RelayInfo{
				ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "tencent-hunyuan"},
			}
			var reader io.Reader = strings.NewReader(test.data)
			if test.err != nil {
				reader = tencentStreamErrorReader{Reader: reader, err: test.err}
			}
			response := &http.Response{Body: io.NopCloser(reader)}

			usage, apiErr := tencentStreamHandler(c, info, response)

			require.Nil(t, apiErr)
			require.Nil(t, info.StreamStatus)
			require.NotNil(t, usage)
			require.Positive(t, usage.CompletionTokens)
			require.Equal(t, test.want, info.RateLimitStreamStatus.EndReason)
			require.Equal(t, test.name != "normal EOF", info.RateLimitStreamStatus.HasErrors())
			if test.err != nil {
				require.ErrorIs(t, info.RateLimitStreamStatus.EndError, test.err)
			}
		})
	}
}
