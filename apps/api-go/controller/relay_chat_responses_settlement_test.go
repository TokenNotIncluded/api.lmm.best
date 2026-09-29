package controller

import (
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
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/relayconvert"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Exercise the real authenticated Responses route, channel selection, advanced
// custom Chat adaptor, and settlement defers using only isolated fixture data.
// A transport EOF changes the terminal protocol result, not usage accounting.
func TestChatResponsesEOFDoesNotReplayOrRefundPartialOutput(t *testing.T) {
	service.InitTokenEncoders()
	type accounting struct{ charge, prompt, completion int }
	for _, measured := range []bool{false, true} {
		var completed accounting
		for _, terminal := range []bool{true, false} {
			t.Run(fmt.Sprintf("measured_%t/finish_%t", measured, terminal), func(t *testing.T) {
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
				require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{}`))
				require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"`+drawingParityModel+`":1}`))

				var calls atomic.Int32
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					if r.URL.Path != "/v1/chat/completions" {
						t.Errorf("unexpected upstream route %q", r.URL.Path)
						http.Error(w, "wrong route", http.StatusBadRequest)
						return
					}
					var request dto.GeneralOpenAIRequest
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil || len(request.Messages) == 0 {
						t.Errorf("Responses request was not converted to Chat messages: %v", err)
						http.Error(w, "wrong request", http.StatusBadRequest)
						return
					}
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, "data: {\"id\":\"chat_fixture\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"hello world\"}}]}\n\n")
					if measured {
						fmt.Fprint(w, "data: {\"id\":\"chat_fixture\",\"choices\":[],\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":2,\"total_tokens\":102}}\n\n")
					}
					if terminal {
						fmt.Fprint(w, "data: {\"id\":\"chat_fixture\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
					}
					// Both fixtures close the real socket normally. Only the
					// completed control supplies an application finish reason.
				}))
				defer upstream.Close()
				fixture.channel.Type = constant.ChannelTypeAdvancedCustom
				fixture.channel.BaseURL = &upstream.URL
				fixture.channel.SetOtherSettings(dto.ChannelOtherSettings{AdvancedCustom: &dto.AdvancedCustomConfig{
					Routes: []dto.AdvancedCustomRoute{{
						IncomingPath: "/v1/responses",
						UpstreamPath: "/v1/chat/completions",
						Converter:    relayconvert.ConverterOpenAIResponsesToOpenAIChat,
					}},
				}})
				require.NoError(t, fixture.db.Save(&fixture.channel).Error)
				model.InvalidatePricingCache()

				engine := gin.New()
				engine.Use(middleware.BodyStorageCleanup())
				engine.POST("/v1/responses", middleware.TokenAuth(), middleware.RelayRequestAdmission(), middleware.Distribute(), func(c *gin.Context) {
					Relay(c, types.RelayFormatOpenAIResponses)
				})
				gateway := httptest.NewServer(engine)
				defer gateway.Close()
				request, err := http.NewRequest(http.MethodPost, gateway.URL+"/v1/responses?group=image-2", strings.NewReader(`{"model":"`+drawingParityModel+`","input":"test prompt","stream":true}`))
				require.NoError(t, err)
				request.Header.Set("Authorization", "Bearer "+fixture.token.Key)
				request.Header.Set("Content-Type", "application/json")
				client := &http.Client{Timeout: 10 * time.Second}
				response, err := client.Do(request)
				require.NoError(t, err)
				raw, readErr := io.ReadAll(response.Body)
				require.NoError(t, response.Body.Close())
				require.NoError(t, readErr)
				wire := string(raw)
				require.Equal(t, http.StatusOK, response.StatusCode, wire)
				require.Contains(t, wire, "hello world")
				require.EqualValues(t, 1, calls.Load(), "partial output must not trigger a retry despite RetryTimes=2")

				var user model.User
				var token model.Token
				require.NoError(t, fixture.db.First(&user, fixture.user.Id).Error)
				require.NoError(t, fixture.db.First(&token, fixture.token.Id).Error)
				charge := fixture.user.Quota - user.Quota
				require.Positive(t, charge, "a protocol failure must not refund already generated output")
				require.Equal(t, charge, fixture.token.RemainQuota-token.RemainQuota)
				require.Equal(t, charge, user.UsedQuota)
				require.Equal(t, charge, token.UsedQuota)
				var logs []model.Log
				require.NoError(t, fixture.db.Where("user_id = ? AND type = ?", user.Id, model.LogTypeConsume).Find(&logs).Error)
				require.Len(t, logs, 1)
				require.Equal(t, charge, logs[0].Quota)
				actual := accounting{charge, logs[0].PromptTokens, logs[0].CompletionTokens}
				if measured {
					require.Equal(t, 100, actual.prompt)
					require.Equal(t, 2, actual.completion)
				} else {
					// This route's existing local estimator has no prompt-token
					// estimate in the fixture. Do not expand its billing policy.
					require.Zero(t, actual.prompt)
					require.Positive(t, actual.completion)
				}
				if terminal {
					completed = actual
					require.Equal(t, 1, strings.Count(wire, "event: response.completed\n"))
					require.NotContains(t, wire, "event: response.failed\n")
				} else {
					require.Equal(t, completed, actual, "EOF classification must preserve measured usage and existing fallback estimation")
					require.NotContains(t, wire, "event: response.completed\n")
					require.Equal(t, 1, strings.Count(wire, "event: response.failed\n"))
					require.Contains(t, wire, "upstream_stream_interrupted")
				}
			})
		}
	}
}
