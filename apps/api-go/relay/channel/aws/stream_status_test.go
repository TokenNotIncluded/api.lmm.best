package aws

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/stretchr/testify/require"
)

type awsStreamErrorReader struct {
	io.Reader
	err error
}

func (r awsStreamErrorReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if errors.Is(err, io.EOF) {
		return n, r.err
	}
	return n, err
}

type awsCancelOnFlushWriter struct {
	*httptest.ResponseRecorder
	cancel context.CancelFunc
}

func (w awsCancelOnFlushWriter) Flush() {
	w.ResponseRecorder.Flush()
	w.cancel()
}

type awsFailedStreamWriter struct {
	*httptest.ResponseRecorder
}

func (w awsFailedStreamWriter) Write([]byte) (int, error) {
	return 0, io.ErrClosedPipe
}

func TestAwsStreamHandlerRecordsReaderFailureWithoutChangingPartialUsage(t *testing.T) {
	readErr := errors.New("synthetic stream read failure")
	var body bytes.Buffer
	require.NoError(t, writeAwsStreamEvent(&body, `{"type":"message_start","message":{"id":"msg_test","type":"message","role":"assistant","model":"claude-test","content":[],"usage":{"input_tokens":100,"output_tokens":1}}}`))
	client := newAwsTestClient(awsHTTPClientFunc(func(request *http.Request) (*http.Response, error) {
		return newAwsStreamResponse(request, io.NopCloser(awsStreamErrorReader{
			Reader: bytes.NewReader(body.Bytes()), err: readErr,
		})), nil
	}))
	info := newAwsTestRelayInfo()
	c := newAwsTestContext(httptest.NewRecorder(), context.Background())

	apiErr, usage := awsStreamHandler(c, info, &Adaptor{AwsClient: client, AwsReq: newAwsStreamInput()})

	require.Nil(t, apiErr)
	require.Nil(t, info.StreamStatus)
	require.NotNil(t, usage)
	require.Equal(t, 100, usage.BillingUsage.ClaudeUsage.InputTokens)
	require.Equal(t, relaycommon.StreamEndReasonScannerErr, info.RateLimitStreamStatus.EndReason)
	require.ErrorIs(t, info.RateLimitStreamStatus.EndError, readErr)
	require.True(t, info.RateLimitStreamStatus.HasErrors())
}

func TestAwsStreamHandlerPublishesStreamOutcome(t *testing.T) {
	for _, test := range []struct {
		name   string
		cancel bool
		failed bool
	}{
		{name: "normal EOF"},
		{name: "client canceled", cancel: true},
		{name: "downstream write failed", failed: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			requestContext, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			var writer http.ResponseWriter = httptest.NewRecorder()
			if test.cancel {
				writer = &awsCancelOnFlushWriter{ResponseRecorder: httptest.NewRecorder(), cancel: cancel}
			} else if test.failed {
				writer = &awsFailedStreamWriter{ResponseRecorder: httptest.NewRecorder()}
			}
			var body bytes.Buffer
			for _, event := range []string{
				`{"type":"message_start","message":{"id":"msg_test","type":"message","role":"assistant","model":"claude-test","content":[],"usage":{"input_tokens":100,"output_tokens":1}}}`,
				`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}`,
				`{"type":"message_stop"}`,
			} {
				require.NoError(t, writeAwsStreamEvent(&body, event))
			}
			client := newAwsTestClient(awsHTTPClientFunc(func(request *http.Request) (*http.Response, error) {
				return newAwsStreamResponse(request, io.NopCloser(bytes.NewReader(body.Bytes()))), nil
			}))
			info := newAwsTestRelayInfo()
			c := newAwsTestContext(writer, requestContext)

			apiErr, usage := awsStreamHandler(c, info, &Adaptor{AwsClient: client, AwsReq: newAwsStreamInput()})

			require.Nil(t, apiErr)
			require.Nil(t, info.StreamStatus)
			require.NotNil(t, usage)
			require.Equal(t, 100, usage.BillingUsage.ClaudeUsage.InputTokens)
			require.Equal(t, test.cancel || test.failed, info.RateLimitStreamStatus.HasErrors())
			if test.cancel || test.failed {
				require.Equal(t, relaycommon.StreamEndReasonClientGone, info.RateLimitStreamStatus.EndReason)
			} else {
				require.Equal(t, relaycommon.StreamEndReasonEOF, info.RateLimitStreamStatus.EndReason)
			}
		})
	}
}

func TestAwsStreamHandlerRecordsResponseDecodeFailure(t *testing.T) {
	var body bytes.Buffer
	require.NoError(t, writeAwsStreamEvent(&body, `{`))
	client := newAwsTestClient(awsHTTPClientFunc(func(request *http.Request) (*http.Response, error) {
		return newAwsStreamResponse(request, io.NopCloser(strings.NewReader(body.String()))), nil
	}))
	info := newAwsTestRelayInfo()

	apiErr, usage := awsStreamHandler(newAwsTestContext(httptest.NewRecorder(), context.Background()), info,
		&Adaptor{AwsClient: client, AwsReq: newAwsStreamInput()})

	require.NotNil(t, apiErr)
	require.Nil(t, info.StreamStatus)
	require.Nil(t, usage)
	require.Equal(t, relaycommon.StreamEndReasonHandlerStop, info.RateLimitStreamStatus.EndReason)
	require.True(t, info.RateLimitStreamStatus.HasErrors())
}
