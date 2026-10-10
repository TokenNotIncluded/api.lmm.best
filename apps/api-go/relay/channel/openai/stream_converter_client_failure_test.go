package openai

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/relay/helper"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type geminiConverterFlushFailureWriter struct {
	*httptest.ResponseRecorder
	flushes int
}

func (w *geminiConverterFlushFailureWriter) FlushError() error {
	w.flushes++
	if w.flushes == 3 {
		return io.ErrClosedPipe
	}
	return nil
}

func TestOaiResponsesToGeminiFlushFailureRetainsUsageAndStops(t *testing.T) {
	previousTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = previousTimeout })
	c, _, response, info := newResponsesChatTestContext(t, "", true)
	writer := &geminiConverterFlushFailureWriter{ResponseRecorder: httptest.NewRecorder()}
	wrapped, _ := gin.CreateTestContext(writer)
	c.Writer = wrapped.Writer
	info.RelayFormat = types.RelayFormatGemini
	body := &validationTrackedBody{Reader: strings.NewReader(
		"data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_fixture\"}}\n\n" +
			"data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n" +
			"data: {\"type\":\"response.output_text.delta\",\"delta\":\" world\"}\n\n")}
	response.Body = body
	usage, apiErr := OaiResponsesToChatStreamHandler(c, info, response)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	require.Positive(t, usage.CompletionTokens)
	require.Zero(t, usage.PromptTokens)
	require.True(t, info.ResponseFailed)
	require.True(t, helper.HTTPStreamDownstreamFailed(c))
	require.Equal(t, 3, writer.flushes, "failed flush must stop scanning and finalization")
	require.True(t, body.closed)
	require.Contains(t, writer.Body.String(), "hello")
}
