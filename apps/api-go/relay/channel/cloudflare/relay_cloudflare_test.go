package cloudflare

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type streamReadError struct{}

func (streamReadError) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestCloudflareStreamPublishesOutcomeWithoutChangingLegacyReturn(t *testing.T) {
	chunk := `data: {"choices":[{"delta":{"content":"hello"}}]}` + "\n\n"
	tests := []struct {
		name     string
		reader   io.Reader
		cancel   bool
		reason   relaycommon.StreamEndReason
		hasError bool
	}{
		{name: "done", reader: strings.NewReader(chunk + "data: [DONE]\n\n"), reason: relaycommon.StreamEndReasonDone},
		{name: "eof", reader: strings.NewReader(chunk), reason: relaycommon.StreamEndReasonEOF},
		{name: "invalid_json_before_done", reader: strings.NewReader("data: invalid\n\n" + chunk + "data: [DONE]\n\n"), reason: relaycommon.StreamEndReasonDone, hasError: true},
		{name: "upstream_error", reader: strings.NewReader("data: {\"error\":{\"message\":\"failed\"}}\n\ndata: [DONE]\n\n"), reason: relaycommon.StreamEndReasonDone, hasError: true},
		{name: "upstream_unsuccessful", reader: strings.NewReader("data: {\"success\":false}\n\ndata: [DONE]\n\n"), reason: relaycommon.StreamEndReasonDone, hasError: true},
		{name: "scanner_error", reader: io.MultiReader(strings.NewReader(chunk), streamReadError{}), reason: relaycommon.StreamEndReasonScannerErr, hasError: true},
		{name: "canceled", reader: strings.NewReader(""), cancel: true, reason: relaycommon.StreamEndReasonClientGone, hasError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			requestContext, cancel := context.WithCancel(context.Background())
			defer cancel()
			if test.cancel {
				cancel()
			}
			c.Request = httptest.NewRequest(http.MethodPost, "/relay", nil).WithContext(requestContext)
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "cloudflare-test"}}

			apiErr, usage := cfStreamHandler(c, info, &http.Response{Body: io.NopCloser(test.reader)})

			require.Nil(t, apiErr)
			require.NotNil(t, usage)
			assert.Nil(t, info.StreamStatus, "legacy billing must keep its original stream status")
			require.NotNil(t, info.RateLimitStreamStatus)
			assert.Equal(t, test.reason, info.RateLimitStreamStatus.EndReason)
			assert.Equal(t, test.hasError, info.RateLimitStreamStatus.HasErrors())
			if !test.cancel {
				assert.Contains(t, recorder.Body.String(), "data: [DONE]\n\n")
			}
		})
	}
}
