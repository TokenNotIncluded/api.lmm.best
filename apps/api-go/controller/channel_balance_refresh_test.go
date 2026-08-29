package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func refreshTestChannel(id int) *model.Channel {
	return &model.Channel{Id: id, Status: common.ChannelStatusEnabled}
}

func TestRefreshChannelBalancesCapturesAndSanitizesProviderFailure(t *testing.T) {
	summary, err := refreshChannelBalancesContext(
		context.Background(),
		[]*model.Channel{refreshTestChannel(11)},
		func(context.Context, *model.Channel) (channelBalanceResult, error) {
			return channelBalanceResult{}, errors.New("provider rejected sk-secret-provider-key")
		},
		0,
	)

	require.NoError(t, err)
	require.Equal(t, 1, summary.Attempted)
	require.Equal(t, 0, summary.Updated)
	require.Equal(t, 1, summary.Failed)
	require.Equal(t, []channelBalanceRefreshFailure{{
		ChannelID: 11,
		Code:      "provider_error",
		Message:   "provider balance refresh failed",
	}}, summary.Failures)
	encoded, marshalErr := json.Marshal(summary)
	require.NoError(t, marshalErr)
	require.NotContains(t, string(encoded), "sk-secret-provider-key")
}

func TestRefreshChannelBalancesCapturesAndSanitizesDatabaseFailure(t *testing.T) {
	databaseErr := fmt.Errorf("%w: update channels set key='sk-secret-db-key'", model.ErrChannelBalanceUpdate)
	summary, err := refreshChannelBalancesContext(
		context.Background(),
		[]*model.Channel{refreshTestChannel(12)},
		func(context.Context, *model.Channel) (channelBalanceResult, error) {
			return channelBalanceResult{}, databaseErr
		},
		0,
	)

	require.NoError(t, err)
	require.Equal(t, 1, summary.Failed)
	require.Equal(t, []channelBalanceRefreshFailure{{
		ChannelID: 12,
		Code:      "database_error",
		Message:   "channel balance could not be saved",
	}}, summary.Failures)
	encoded, marshalErr := json.Marshal(summary)
	require.NoError(t, marshalErr)
	require.NotContains(t, string(encoded), "sk-secret-db-key")
}

func TestRefreshChannelBalancesReportsMixedOutcome(t *testing.T) {
	disabled := refreshTestChannel(4)
	disabled.Status = common.ChannelStatusManuallyDisabled
	multiKey := refreshTestChannel(5)
	multiKey.ChannelInfo.IsMultiKey = true
	calls := 0

	summary, err := refreshChannelBalancesContext(
		context.Background(),
		[]*model.Channel{
			refreshTestChannel(1),
			refreshTestChannel(2),
			refreshTestChannel(3),
			disabled,
			multiKey,
		},
		func(_ context.Context, channel *model.Channel) (channelBalanceResult, error) {
			calls++
			switch channel.Id {
			case 1:
				return channelBalanceResult{Balance: 10}, nil
			case 2:
				return channelBalanceResult{}, errors.New("provider unavailable")
			default:
				return channelBalanceResult{}, fmt.Errorf("%w: write failed", model.ErrChannelBalanceUpdate)
			}
		},
		0,
	)

	require.NoError(t, err)
	require.Equal(t, 3, calls)
	require.Equal(t, channelBalanceRefreshSummary{
		Attempted: 3,
		Updated:   1,
		Failed:    2,
		Failures: []channelBalanceRefreshFailure{
			{ChannelID: 2, Code: "provider_error", Message: "provider balance refresh failed"},
			{ChannelID: 3, Code: "database_error", Message: "channel balance could not be saved"},
		},
	}, summary)
}

func TestRefreshChannelBalancesReportsAllSuccess(t *testing.T) {
	summary, err := refreshChannelBalancesContext(
		context.Background(),
		[]*model.Channel{refreshTestChannel(21), refreshTestChannel(22)},
		func(context.Context, *model.Channel) (channelBalanceResult, error) {
			return channelBalanceResult{Balance: 5}, nil
		},
		0,
	)

	require.NoError(t, err)
	require.Equal(t, channelBalanceRefreshSummary{
		Attempted: 2,
		Updated:   2,
		Failures:  []channelBalanceRefreshFailure{},
	}, summary)
}

func TestRefreshChannelBalancesBoundsFailureDetailsWithoutDroppingCounts(t *testing.T) {
	channels := make([]*model.Channel, maxChannelBalanceRefreshFailures+7)
	for index := range channels {
		channels[index] = refreshTestChannel(index + 1)
	}

	summary, err := refreshChannelBalancesContext(
		context.Background(),
		channels,
		func(context.Context, *model.Channel) (channelBalanceResult, error) {
			return channelBalanceResult{}, errors.New("provider failure")
		},
		0,
	)

	require.NoError(t, err)
	require.Equal(t, len(channels), summary.Attempted)
	require.Equal(t, len(channels), summary.Failed)
	require.Len(t, summary.Failures, maxChannelBalanceRefreshFailures)
	require.Equal(t, 7, summary.FailuresOmitted)
	require.Equal(t, 1, summary.Failures[0].ChannelID)
	require.Equal(t, maxChannelBalanceRefreshFailures, summary.Failures[len(summary.Failures)-1].ChannelID)
}

func TestChannelBalanceRefreshLogMessageExposesCountsWithoutFailureDetails(t *testing.T) {
	summary := channelBalanceRefreshSummary{
		Attempted:       27,
		Updated:         3,
		Failed:          24,
		Failures:        []channelBalanceRefreshFailure{{ChannelID: 9, Code: "provider_error", Message: "sk-secret-must-not-be-logged"}},
		FailuresOmitted: 4,
	}

	message := channelBalanceRefreshLogMessage("automatic channel balance refresh", summary, true)
	require.Equal(
		t,
		"automatic channel balance refresh: attempted=27 updated=3 failed=24 failure_details_omitted=4 scan_error=true",
		message,
	)
	require.NotContains(t, message, "sk-secret-must-not-be-logged")
}

func TestWriteChannelBalanceRefreshResponseUsesCompatiblePartialAndFullFailureEnvelopes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name         string
		summary      channelBalanceRefreshSummary
		err          error
		wantStatus   int
		wantSuccess  bool
		wantDegraded bool
	}{
		{
			name:        "all success",
			summary:     channelBalanceRefreshSummary{Attempted: 2, Updated: 2, Failures: []channelBalanceRefreshFailure{}},
			wantStatus:  http.StatusOK,
			wantSuccess: true,
		},
		{
			name:         "partial failure remains compatible 200",
			summary:      channelBalanceRefreshSummary{Attempted: 2, Updated: 1, Failed: 1, Failures: []channelBalanceRefreshFailure{{ChannelID: 2, Code: "provider_error", Message: "provider balance refresh failed"}}},
			wantStatus:   http.StatusOK,
			wantDegraded: true,
		},
		{
			name:         "full provider failure",
			summary:      channelBalanceRefreshSummary{Attempted: 2, Failed: 2, Failures: []channelBalanceRefreshFailure{{ChannelID: 1, Code: "provider_error", Message: "provider balance refresh failed"}, {ChannelID: 2, Code: "provider_error", Message: "provider balance refresh failed"}}},
			wantStatus:   http.StatusBadGateway,
			wantDegraded: true,
		},
		{
			name:       "channel scan failure",
			summary:    channelBalanceRefreshSummary{Failures: []channelBalanceRefreshFailure{}},
			err:        errors.New("database unavailable"),
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			writeChannelBalanceRefreshResponse(c, test.summary, test.err)

			require.Equal(t, test.wantStatus, recorder.Code)
			var response struct {
				Success  bool                         `json:"success"`
				Degraded bool                         `json:"degraded"`
				Message  string                       `json:"message"`
				Data     channelBalanceRefreshSummary `json:"data"`
			}
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
			require.Equal(t, test.wantSuccess, response.Success)
			require.Equal(t, test.wantDegraded, response.Degraded)
			require.NotEmpty(t, response.Message)
			require.Equal(t, test.summary, response.Data)
		})
	}
}
