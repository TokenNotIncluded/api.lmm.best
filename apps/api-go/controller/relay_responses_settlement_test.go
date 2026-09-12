package controller

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

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
	}{
		{"text_eof", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello world\"}\n\n", true, -1},
		{"text_read_error", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello world\"}\n\n", true, -1},
		{"tool_eof", "data: {\"type\":\"response.function_call_arguments.delta\",\"delta\":\"hello world\"}\n\n", true, -1},
		{"created_unknown", "", false, 0},
		{"created_input_usage", "data: {\"type\":\"response.in_progress\",\"response\":{\"usage\":{\"input_tokens\":100,\"output_tokens\":0,\"total_tokens\":100}}}\n\n", true, 100},
		{"incomplete_usage", "data: {\"type\":\"response.incomplete\",\"response\":{\"usage\":{\"input_tokens\":100,\"output_tokens\":64,\"total_tokens\":164}}}\n\n", true, 100},
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
				fmt.Fprint(w, "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_fixture\"}}\n\n"+tc.events)
			}))
			defer upstream.Close()
			require.NoError(t, fixture.db.Model(&fixture.channel).Update("base_url", upstream.URL).Error)
			model.InvalidatePricingCache()
			engine := gin.New()
			engine.Use(middleware.BodyStorageCleanup())
			engine.POST("/v1/responses", middleware.TokenAuth(), middleware.RelayRequestAdmission(), middleware.Distribute(), func(c *gin.Context) { Relay(c, types.RelayFormatOpenAIResponses) })
			request := httptest.NewRequest(http.MethodPost, "/v1/responses?group=image-2", strings.NewReader(`{"model":"`+drawingParityModel+`","input":"test prompt","stream":true}`))
			request.Header.Set("Authorization", "Bearer "+fixture.token.Key)
			request.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, request)
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			require.EqualValues(t, 1, calls.Load(), "partial output must not replay despite RetryTimes=2")
			terminal := "response.failed"
			if tc.name == "incomplete_usage" {
				terminal = "response.incomplete"
			}
			require.Equal(t, 1, strings.Count(w.Body.String(), "event: "+terminal))
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
