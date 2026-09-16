package controller

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/middleware"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Exercise the real controller retry/refund defers and ResponsesHelper's real
// PostTextConsumeQuota with the existing isolated wallet/token SQL fixture.
func TestResponsesRelayPartialSettlement(t *testing.T) {
	service.InitTokenEncoders()
	for _, tc := range []struct {
		name, events string
		charged      bool
		prompt       int
		terminal     string
	}{
		{"gzip_completed", "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"usage\":{\"input_tokens\":100,\"output_tokens\":2,\"total_tokens\":102}}}\n\n", true, 100, "response.completed"},
		{"gzip_truncated", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello world\"}\n\n", true, -1, "response.failed"},
		{"gzip_created_truncated", "", false, 0, "response.failed"},
		{"text_eof", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello world\"}\n\n", true, -1, "response.failed"},
		{"text_read_error", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello world\"}\n\n", true, -1, "response.failed"},
		{"tool_eof", "data: {\"type\":\"response.function_call_arguments.delta\",\"delta\":\"hello world\"}\n\n", true, -1, "response.failed"},
		{"created_unknown", "", false, 0, "response.failed"},
		{"created_input_usage", "data: {\"type\":\"response.in_progress\",\"response\":{\"usage\":{\"input_tokens\":100,\"output_tokens\":0,\"total_tokens\":100}}}\n\n", true, 100, "response.failed"},
		{"incomplete_usage", "data: {\"type\":\"response.incomplete\",\"response\":{\"usage\":{\"input_tokens\":100,\"output_tokens\":64,\"total_tokens\":164}}}\n\n", true, 100, "response.incomplete"},
		{"completed_unknown", "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n", true, 0, "response.completed"},
		{"failed_unknown", "data: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\"}}\n\n", false, 0, "response.failed"},
		{"incomplete_unknown", "data: {\"type\":\"response.incomplete\",\"response\":{\"status\":\"incomplete\"}}\n\n", false, 0, "response.incomplete"},
		{"cancelled_unknown", "data: {\"type\":\"response.cancelled\",\"response\":{\"status\":\"cancelled\"}}\n\n", false, 0, "response.cancelled"},
		{"failed_input_usage", "data: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"usage\":{\"input_tokens\":100,\"output_tokens\":0,\"total_tokens\":100}}}\n\n", true, 100, "response.failed"},
		{"failed_text", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello world\"}\n\ndata: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\"}}\n\n", true, -1, "response.failed"},
		{"completed_failed_status", "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"failed\"}}\n\n", false, 0, "response.completed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newDrawingParityFixture(t, common.GetTrustQuota(), http.StatusOK)
			require.NoError(t, fixture.db.AutoMigrate(&model.PublicRelayPreference{}))
			previousRatios, err := json.Marshal(ratio_setting.GetModelRatioCopy())
			require.NoError(t, err)
			previousRetry, previousTimeout := common.RetryTimes, constant.StreamingTimeout
			common.RetryTimes, constant.StreamingTimeout = 2, 5
			t.Cleanup(func() {
				common.RetryTimes, constant.StreamingTimeout = previousRetry, previousTimeout
				require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(string(previousRatios)))
			})
			// Use token pricing, not the image fixture's per-request price.
			require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{}`))
			require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"`+drawingParityModel+`":1}`))
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				if tc.name == "text_read_error" {
					// Closing before the promised body length makes net/http
					// return unexpected EOF after delivering the partial frame.
					w.Header().Set("Content-Length", "999999")
				}
				payload := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_fixture\"}}\n\n" + tc.events
				if strings.HasPrefix(tc.name, "gzip_") {
					var compressed bytes.Buffer
					encoder := gzip.NewWriter(&compressed)
					_, _ = encoder.Write([]byte(payload))
					_ = encoder.Close()
					wire := compressed.Bytes()
					if strings.HasSuffix(tc.name, "truncated") {
						// Missing CRC/size trailer produces a real decoder EOF,
						// after the complete partial events have been decoded.
						wire = wire[:len(wire)-8]
					}
					w.Header().Set("Content-Encoding", "gzip")
					_, _ = w.Write(wire)
					return
				}
				fmt.Fprint(w, payload)
			}))
			defer upstream.Close()
			require.NoError(t, fixture.db.Model(&fixture.channel).Update("base_url", upstream.URL).Error)
			model.InvalidatePricingCache()
			engine := gin.New()
			engine.Use(middleware.BodyStorageCleanup())
			engine.POST("/v1/responses", middleware.TokenAuth(), middleware.RelayRequestAdmission(), middleware.Distribute(), func(c *gin.Context) { Relay(c, types.RelayFormatOpenAIResponses) })
			// Use real downstream HTTP framing as well as the real upstream
			// socket: a recorder cannot detect a broken response body decoder.
			gateway := httptest.NewServer(engine)
			defer gateway.Close()
			request, err := http.NewRequest(http.MethodPost, gateway.URL+"/v1/responses?group=image-2", strings.NewReader(`{"model":"`+drawingParityModel+`","input":"test prompt","stream":true}`))
			require.NoError(t, err)
			request.Header.Set("Authorization", "Bearer "+fixture.token.Key)
			request.Header.Set("Content-Type", "application/json")
			client := &http.Client{Timeout: 10 * time.Second}
			response, err := client.Do(request)
			require.NoError(t, err)
			raw, err := io.ReadAll(response.Body)
			require.NoError(t, response.Body.Close())
			require.NoError(t, err, "upstream interruption must not break downstream HTTP decoding")
			bodyText := string(raw)
			require.Equal(t, http.StatusOK, response.StatusCode, bodyText)
			require.Empty(t, response.Header.Get("Content-Encoding"))
			require.Contains(t, response.Header.Get("Cache-Control"), "no-transform")
			require.Equal(t, "no", response.Header.Get("X-Accel-Buffering"))
			require.EqualValues(t, 1, calls.Load(), "partial output must not replay despite RetryTimes=2")
			require.Equal(t, 1, strings.Count(bodyText, "event: "+tc.terminal))
			var user model.User
			var token model.Token
			require.NoError(t, fixture.db.First(&user, fixture.user.Id).Error)
			require.NoError(t, fixture.db.First(&token, fixture.token.Id).Error)
			charge := fixture.user.Quota - user.Quota
			if tc.charged {
				require.Positive(t, charge)
			} else {
				require.Zero(t, charge)
			}
			require.Equal(t, charge, fixture.token.RemainQuota-token.RemainQuota)
			require.Equal(t, charge, user.UsedQuota)
			require.Equal(t, charge, token.UsedQuota)
			var logs []model.Log
			require.NoError(t, fixture.db.Where("user_id = ? AND type = ?", user.Id, model.LogTypeConsume).Find(&logs).Error)
			require.Len(t, logs, 1)
			require.Equal(t, charge, logs[0].Quota)
			if tc.prompt >= 0 {
				require.Equal(t, tc.prompt, logs[0].PromptTokens)
			}
			if tc.name == "incomplete_usage" {
				require.Equal(t, 64, logs[0].CompletionTokens)
			}
		})
	}
}
