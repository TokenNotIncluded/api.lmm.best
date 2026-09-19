package service

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAcquisitionSuccessRequiresDeliveredTextResponse(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{UserId: 1, TokenId: 2}
	require.False(t, acquisitionTextResponseSucceeded(c, info, 10, true, false))
	_, _ = c.Writer.Write([]byte(`{"content":"ok"}`))
	require.True(t, acquisitionTextResponseSucceeded(c, info, 10, true, false))
	require.False(t, acquisitionTextResponseSucceeded(c, info, 0, true, false))
	require.False(t, acquisitionTextResponseSucceeded(c, info, 10, false, false))
	require.False(t, acquisitionTextResponseSucceeded(c, info, 10, true, true))
	info.IsPlayground = true
	require.False(t, acquisitionTextResponseSucceeded(c, info, 10, true, false))
	info.IsPlayground = false
	info.IsStream = true
	require.False(t, acquisitionTextResponseSucceeded(c, info, 10, true, false))
	info.StreamStatus = relaycommon.NewStreamStatus()
	info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonEOF, nil)
	info.StreamStatus.RecordError("missing terminal event")
	require.False(t, acquisitionTextResponseSucceeded(c, info, 10, true, false))
	info.StreamStatus = relaycommon.NewStreamStatus()
	info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonDone, nil)
	require.True(t, acquisitionTextResponseSucceeded(c, info, 10, true, false))
	info.StreamStatus.RecordError("truncated")
	require.False(t, acquisitionTextResponseSucceeded(c, info, 10, true, false))
	info.StreamStatus = relaycommon.NewStreamStatus()
	info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonDone, errors.New("write failed"))
	require.False(t, acquisitionTextResponseSucceeded(c, info, 10, true, false))
	info.IsStream = false
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.Request = c.Request.WithContext(ctx)
	require.False(t, acquisitionTextResponseSucceeded(c, info, 10, true, false))
}
