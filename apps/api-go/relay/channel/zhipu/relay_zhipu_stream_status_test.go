package zhipu

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type zhipuStreamFailedWriter struct {
	*httptest.ResponseRecorder
	writes     int
	clientGone chan bool
}

func (w *zhipuStreamFailedWriter) Write([]byte) (int, error) {
	w.writes++
	return 0, io.ErrClosedPipe
}

func (w *zhipuStreamFailedWriter) CloseNotify() <-chan bool {
	return w.clientGone
}

func TestZhipuStreamWriteFailurePreservesLaterUsage(t *testing.T) {
	writer := &zhipuStreamFailedWriter{
		ResponseRecorder: httptest.NewRecorder(),
		clientGone:       make(chan bool),
	}
	c, _ := gin.CreateTestContext(writer)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{}
	body := "data: partial output\n" +
		"meta: {\"request_id\":\"request-test\",\"task_status\":\"SUCCESS\",\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":3,\"total_tokens\":15}}\n"
	usage, apiErr := zhipuStreamHandler(c, info, &http.Response{Body: io.NopCloser(strings.NewReader(body))})

	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	require.Equal(t, 12, usage.PromptTokens)
	require.Equal(t, 3, usage.CompletionTokens)
	require.Equal(t, 15, usage.TotalTokens)
	require.Nil(t, info.StreamStatus)
	require.NotNil(t, info.RateLimitStreamStatus)
	require.Equal(t, relaycommon.StreamEndReasonClientGone, info.RateLimitStreamStatus.EndReason)
	require.ErrorIs(t, info.RateLimitStreamStatus.EndError, io.ErrClosedPipe)
	require.True(t, info.RateLimitStreamStatus.HasErrors())
	require.Equal(t, 1, writer.writes)
}
