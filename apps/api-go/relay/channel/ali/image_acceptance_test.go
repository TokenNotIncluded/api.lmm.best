package ali

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/iotest"
	"testing/synctest"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/stretchr/testify/require"
)

func TestAliImageAcceptanceBoundary(t *testing.T) {
	for _, tc := range []struct {
		name      string
		body      string
		syncModel bool
		readError bool
		accepted  bool
	}{
		{"async_id_and_message", `{"output":{"task_id":"fixture-task"},"message":"fixture error"}`, false, false, true},
		{"async_nonblank_id", `{"output":{"task_id":" task "},"message":"fixture error"}`, false, false, true},
		{"async_blank_id", `{"output":{"task_id":"  "},"message":"fixture error"}`, false, false, false},
		{"async_missing_id", `{"message":"fixture error"}`, false, false, false},
		{"malformed_body", `{"output":`, false, false, false},
		{"unreadable_body", "", false, true, false},
		{"sync_incidental_id", `{"output":{"task_id":"fixture-task"},"message":"fixture error"}`, true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, info := pollTestContext(context.Background())
			var body io.Reader = strings.NewReader(tc.body)
			if tc.readError {
				body = iotest.ErrReader(errors.New("fixture body read failure"))
			}
			resp := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(body)}
			apiErr, usage := aliImageHandler(&Adaptor{IsSyncImageModel: tc.syncModel}, c, resp, info)
			require.NotNil(t, apiErr)
			require.Nil(t, usage)
			require.Equal(t, tc.accepted, common.GetContextKeyBool(c, constant.ContextKeyUpstreamAsyncTaskAccepted))
			// Pre-acceptance parsing failures retain the existing error-code policy.
			wantRetry := !tc.accepted && apiErr.GetErrorCode() != types.ErrorCodeBadResponseBody
			require.Equal(t, wantRetry, service.ShouldRetryRelayError(c, apiErr, 1))
		})
	}
}

func TestAliAcceptedImageTerminalErrorsCannotRetry(t *testing.T) {
	client, err := service.GetHttpClientWithProxySettings("", dto.ChannelSettings{})
	require.NoError(t, err)
	original, timeout := client.Transport, client.Timeout
	t.Cleanup(func() { client.Transport, client.Timeout = original, timeout })
	client.Timeout = 0
	for _, status := range []string{"FAILED", "CANCELED", "UNKNOWN", ""} {
		t.Run(status, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				polls := 0
				client.Transport = pollTestTransport(func(r *http.Request) (*http.Response, error) {
					polls++
					require.Equal(t, "/api/v1/tasks/fixture-task", r.URL.Path)
					raw := fmt.Sprintf(`{"output":{"task_status":%q,"code":"channel:fixture","message":"fixture failure"}}`, status)
					return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(raw))}, nil
				})
				c, info := pollTestContext(context.Background())
				resp := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"output":{"task_id":"fixture-task"}}`))}
				started := time.Now()
				apiErr, usage := aliImageHandler(&Adaptor{}, c, resp, info)
				require.NotNil(t, apiErr)
				require.Nil(t, usage)
				require.Equal(t, 1, polls)
				require.Equal(t, 5*time.Second, time.Since(started))
				require.True(t, common.GetContextKeyBool(c, constant.ContextKeyUpstreamAsyncTaskAccepted))
				require.False(t, service.ShouldRetryRelayError(c, apiErr, 2))
			})
		})
	}
}

func TestAliAcceptedImageStillHonorsClientCancellation(t *testing.T) {
	client, err := service.GetHttpClientWithProxySettings("", dto.ChannelSettings{})
	require.NoError(t, err)
	original := client.Transport
	t.Cleanup(func() { client.Transport = original })
	polls := 0
	client.Transport = pollTestTransport(func(*http.Request) (*http.Response, error) {
		polls++
		return pollTestSuccess(), nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c, info := pollTestContext(ctx)
	resp := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"output":{"task_id":"fixture-task"}}`))}
	apiErr, usage := aliImageHandler(&Adaptor{}, c, resp, info)
	require.NotNil(t, apiErr)
	require.ErrorIs(t, apiErr, context.Canceled)
	require.Nil(t, usage)
	require.Zero(t, polls)
	require.True(t, common.GetContextKeyBool(c, constant.ContextKeyUpstreamAsyncTaskAccepted))
	require.False(t, service.ShouldRetryRelayError(c, apiErr, 2))
}
