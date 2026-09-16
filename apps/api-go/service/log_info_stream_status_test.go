package service

import (
	"errors"
	"fmt"
	"testing"

	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"

	"github.com/stretchr/testify/require"
)

func TestAppendStreamStatusRedactsEndError(t *testing.T) {
	t.Parallel()

	secret := "read tcp 10.0.0.5:1234->203.0.113.9:443 api_key=secret"
	endErr := errors.New(secret)
	status := relaycommon.NewStreamStatus()
	status.SetEndReason(relaycommon.StreamEndReasonScannerErr, endErr)
	info := &relaycommon.RelayInfo{
		IsStream:     true,
		StreamStatus: status,
	}
	other := map[string]interface{}{}

	appendStreamStatus(info, other)

	streamInfo, ok := other["stream_status"].(map[string]interface{})
	require.True(t, ok)
	require.Equal(t, "error", streamInfo["status"])
	require.Equal(t, string(relaycommon.StreamEndReasonScannerErr), streamInfo["end_reason"])
	require.Equal(t, "stream_error", streamInfo["end_error"])
	require.NotContains(t, fmt.Sprint(streamInfo), secret)
	require.NotContains(t, fmt.Sprint(streamInfo), "10.0.0.5")
	require.NotContains(t, fmt.Sprint(streamInfo), "api_key")

	// Redaction is an observability boundary only. Control flow retains the
	// original error for errors.Is/errors.As and internal decisions.
	require.ErrorIs(t, info.StreamStatus.EndError, endErr)
	require.Equal(t, secret, info.StreamStatus.EndError.Error())
}
