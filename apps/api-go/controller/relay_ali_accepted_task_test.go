package controller

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/stretchr/testify/require"
)

type aliAcceptedTaskTransport func(*http.Request) (*http.Response, error)

func (f aliAcceptedTaskTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func aliAcceptedTaskResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}
func newAliAcceptedTaskFixture(t *testing.T, transport aliAcceptedTaskTransport) *drawingParityFixture {
	t.Helper()
	fixture := newDrawingParityFixtureWithProviderURL(t, common.GetTrustQuota(), http.StatusOK, "https://provider.invalid")
	fixture.channel.Type = constant.ChannelTypeAli
	// The transport handles every request; no provider or image URL is fetched.
	fixture.channel.StatusCodeMapping = common.GetPointer(`{"500":"502"}`)
	require.NoError(t, fixture.db.Save(&fixture.channel).Error)
	client, err := service.GetHttpClientWithProxySettings("", dto.ChannelSettings{})
	require.NoError(t, err)
	previousTransport, previousTimeout := client.Transport, client.Timeout
	previousRetries := common.RetryTimes
	client.Transport, client.Timeout = transport, 0
	common.RetryTimes = 2
	t.Cleanup(func() {
		client.Transport, client.Timeout = previousTransport, previousTimeout
		common.RetryTimes = previousRetries
	})
	return fixture
}

func TestAliAcceptedTaskStopsControllerSubmissionRetry(t *testing.T) {
	for _, tc := range []struct {
		name       string
		initial    string
		poll       string
		pollError  bool
		wantPolls  int
		wantTime   time.Duration
		wantStatus int
	}{
		{"poll_exhaustion_with_status_mapping", `{"output":{"task_id":"fixture-task"}}`, "", true, 20, 195 * time.Second, http.StatusBadGateway},
		// A provider channel-prefixed error code must not override accepted work.
		{"terminal_channel_code", `{"output":{"task_id":"fixture-task"}}`, `{"output":{"task_status":"FAILED","code":"channel:fixture","message":"fixture failure"}}`, false, 1, 5 * time.Second, http.StatusOK},
		{"accepted_id_with_message", `{"output":{"task_id":"fixture-task"},"message":"fixture message"}`, "", false, 0, 0, http.StatusBadGateway},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				posts, polls := 0, 0
				fixture := newAliAcceptedTaskFixture(t, func(r *http.Request) (*http.Response, error) {
					switch r.Method {
					case http.MethodPost:
						require.Equal(t, "/api/v1/services/aigc/text2image/image-synthesis", r.URL.Path)
						posts++
						return aliAcceptedTaskResponse(http.StatusOK, tc.initial), nil
					case http.MethodGet:
						require.Equal(t, "/api/v1/tasks/fixture-task", r.URL.Path)
						polls++
						if tc.pollError {
							return nil, errors.New("fixture poll transport failure")
						}
						return aliAcceptedTaskResponse(http.StatusOK, tc.poll), nil
					default:
						t.Fatalf("unexpected method %s", r.Method)
						return nil, errors.New("unexpected fixture request")
					}
				})
				started := time.Now()
				status, body := fixture.request(t, "api")
				require.Equal(t, tc.wantStatus, status, body)
				require.Equal(t, 1, posts, "recognized accepted work must never be submitted again")
				require.Equal(t, tc.wantPolls, polls)
				require.Equal(t, tc.wantTime, time.Since(started))
				// This patch changes retries only; existing failed-request refunds remain.
				require.Eventually(t, func() bool {
					var user model.User
					var token model.Token
					return fixture.db.First(&user, fixture.user.Id).Error == nil &&
						fixture.db.First(&token, fixture.token.Id).Error == nil &&
						user.Quota == fixture.user.Quota && token.RemainQuota == fixture.token.RemainQuota
				}, 3*time.Second, time.Millisecond)
			})
		})
	}
}

func TestAliPreAcceptanceFailureStillRetriesControllerSubmission(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		posts, polls := 0, 0
		fixture := newAliAcceptedTaskFixture(t, func(r *http.Request) (*http.Response, error) {
			if r.Method == http.MethodPost {
				posts++
				if posts == 1 {
					return aliAcceptedTaskResponse(http.StatusOK, `{"code":"TransientFailure","message":"fixture rejection"}`), nil
				}
				return aliAcceptedTaskResponse(http.StatusOK, `{"output":{"task_id":"fixture-task"}}`), nil
			}
			require.Equal(t, http.MethodGet, r.Method)
			polls++
			return aliAcceptedTaskResponse(http.StatusOK, `{"output":{"task_status":"SUCCEEDED","results":[{"url":"https://example.invalid/one.png"},{"url":"https://example.invalid/two.png"}]},"usage":{"image_count":2}}`), nil
		})
		status, body := fixture.request(t, "api")
		require.Equal(t, http.StatusOK, status, body)
		require.Contains(t, body, "https://example.invalid/one.png")
		require.Equal(t, 2, posts, "pre-acceptance rejection must remain retryable")
		require.Equal(t, 1, polls)
		var user model.User
		var token model.Token
		require.NoError(t, fixture.db.First(&user, fixture.user.Id).Error)
		require.NoError(t, fixture.db.First(&token, fixture.token.Id).Error)
		charge := fixture.user.Quota - user.Quota
		require.Positive(t, charge, "successful accepted work must still be charged")
		require.Equal(t, charge, fixture.token.RemainQuota-token.RemainQuota)
		require.Equal(t, charge, user.UsedQuota)
		require.Equal(t, charge, token.UsedQuota)
	})
}
