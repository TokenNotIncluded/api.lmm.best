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

	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/middleware"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/relayconvert"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Flush the real socket before reporting the delivery failure: the client must
// receive the selected prefix, while the relay must observe FlushError.
type sseConverterFlushFailureWriter struct {
	http.ResponseWriter
	failAt             int32
	flushes            atomic.Int32
	failed             atomic.Bool
	afterFailureWrites atomic.Int32
	onFailure          func()
}

func (w *sseConverterFlushFailureWriter) Write(p []byte) (int, error) {
	if w.failed.Load() {
		w.afterFailureWrites.Add(1)
	}
	return w.ResponseWriter.Write(p)
}

func (w *sseConverterFlushFailureWriter) FlushError() error {
	flush := w.flushes.Add(1)
	if err := http.NewResponseController(w.ResponseWriter).Flush(); err != nil {
		return err
	}
	if flush == w.failAt {
		w.failed.Store(true)
		w.onFailure()
		return io.ErrClosedPipe
	}
	return nil
}

func (w *sseConverterFlushFailureWriter) Flush() { _ = w.FlushError() }
func (w *sseConverterFlushFailureWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func TestSSECommitConverterClientFailurePreservesSettlement(t *testing.T) {
	for _, test := range []struct {
		name           string
		failAt         int32
		createdUsage   string
		wantCharge     bool
		wantPrompt     int
		wantCompletion int
	}{
		{name: "headers_only", failAt: 1},
		{name: "role_only", failAt: 2},
		// The existing OpenAI estimator rounds two words plus one space to 3.
		{name: "text_without_report", failAt: 3, wantCharge: true, wantCompletion: 3},
		{name: "text_with_explicit_zero_report", failAt: 3, createdUsage: `,"usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0}`},
		{name: "text_with_reported_usage", failAt: 3, createdUsage: `,"usage":{"input_tokens":7,"output_tokens":3,"total_tokens":10}`, wantCharge: true, wantPrompt: 7, wantCompletion: 3},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newSSECommitSettlementFixture(t, 0)
			previousPing := operation_setting.GetGeneralSetting().PingIntervalEnabled
			operation_setting.GetGeneralSetting().PingIntervalEnabled = false
			t.Cleanup(func() { operation_setting.GetGeneralSetting().PingIntervalEnabled = previousPing })

			var upstreamCalls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				call := upstreamCalls.Add(1)
				if r.URL.Path != "/v1/responses" {
					t.Errorf("unexpected converted upstream path %q", r.URL.Path)
					http.Error(w, "wrong route", http.StatusBadRequest)
					return
				}
				var request dto.OpenAIResponsesRequest
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil || len(request.Input) == 0 || request.Stream == nil || !*request.Stream {
					t.Errorf("Chat request was not converted to streaming Responses input: %v", err)
					http.Error(w, "wrong request", http.StatusBadRequest)
					return
				}
				createdUsage := ""
				if call == 1 {
					createdUsage = test.createdUsage
				}
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				w.(http.Flusher).Flush()
				fmt.Fprintf(w, "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_fixture\",\"model\":\"%s\",\"status\":\"in_progress\"%s}}\n\n", drawingParityModel, createdUsage)
				fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello world\"}\n\n"+
					"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_fixture\",\"status\":\"completed\",\"usage\":{\"input_tokens\":100,\"output_tokens\":2,\"total_tokens\":102}}}\n\ndata: [DONE]\n\n")
			}))
			t.Cleanup(upstream.Close)
			fixture.channel.Type = constant.ChannelTypeAdvancedCustom
			fixture.channel.BaseURL = &upstream.URL
			fixture.channel.SetOtherSettings(dto.ChannelOtherSettings{AdvancedCustom: &dto.AdvancedCustomConfig{
				Routes: []dto.AdvancedCustomRoute{{
					IncomingPath: "/v1/chat/completions", UpstreamPath: "/v1/responses",
					Converter: relayconvert.ConverterOpenAIChatToOpenAIResponses,
				}},
			}})
			require.NoError(t, fixture.db.Save(&fixture.channel).Error)
			model.InvalidatePricingCache()

			engine := gin.New()
			engine.Use(middleware.BodyStorageCleanup())
			engine.POST("/v1/chat/completions", middleware.TokenAuth(), middleware.RelayRequestAdmission(), middleware.ModelRequestRateLimit(), middleware.Distribute(), func(c *gin.Context) {
				Relay(c, types.RelayFormatOpenAI)
			})
			type reservation struct {
				wallet, token int
				err           error
			}
			reserved := make(chan reservation, 1)
			failureWriter := &sseConverterFlushFailureWriter{failAt: test.failAt}
			failureWriter.onFailure = func() {
				var user model.User
				var token model.Token
				err := fixture.db.First(&user, fixture.user.Id).Error
				if err == nil {
					err = fixture.db.First(&token, fixture.token.Id).Error
				}
				reserved <- reservation{fixture.user.Quota - user.Quota, fixture.token.RemainQuota - token.RemainQuota, err}
			}
			var gatewayCalls atomic.Int32
			firstFinished := make(chan struct{})
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if gatewayCalls.Add(1) == 1 {
					defer close(firstFinished)
					failureWriter.ResponseWriter = w
					engine.ServeHTTP(failureWriter, r)
					return
				}
				engine.ServeHTTP(w, r)
			}))
			t.Cleanup(func() { gateway.CloseClientConnections(); gateway.Close() })
			client := &http.Client{Timeout: 5 * time.Second}
			response, err := client.Do(sseCommitSettlementRequest(t, fixture, gateway.URL, "/v1/chat/completions"))
			require.NoError(t, err)
			wire, err := io.ReadAll(response.Body)
			require.NoError(t, response.Body.Close())
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, response.StatusCode, string(wire))
			require.Equal(t, "text/event-stream", response.Header.Get("Content-Type"))
			select {
			case <-firstFinished:
			case <-time.After(time.Second):
				t.Fatal("the failed converter request did not finish settlement")
			}
			select {
			case snapshot := <-reserved:
				require.NoError(t, snapshot.err)
				require.Positive(t, snapshot.wallet, "failure must happen after the actual SQLite reservation")
				require.Equal(t, snapshot.wallet, snapshot.token)
			default:
				t.Fatal("the selected real HTTP flush did not fail")
			}
			require.True(t, failureWriter.failed.Load())
			require.Equal(t, test.failAt, failureWriter.flushes.Load(), "no flush is allowed after the delivery failure")
			require.Zero(t, failureWriter.afterFailureWrites.Load(), "no downstream write is allowed after the delivery failure")
			require.EqualValues(t, 1, upstreamCalls.Load(), "RetryTimes=2 must not replay this committed HTTP attempt")
			for _, line := range strings.Split(string(wire), "\n") {
				if line == "" {
					continue
				}
				require.True(t, strings.HasPrefix(line, "data: "), "no controller JSON may be appended to SSE: %s", line)
				require.True(t, json.Valid([]byte(strings.TrimPrefix(line, "data: "))), "invalid converted SSE business frame: %s", line)
			}
			require.NotContains(t, string(wire), "[DONE]")
			require.NotContains(t, string(wire), `"error":`)
			require.NotContains(t, string(wire), `"finish_reason":"stop"`)
			require.NotContains(t, string(wire), `"usage":{`)
			if test.failAt == 1 {
				require.Empty(t, wire)
			} else {
				require.Contains(t, string(wire), `"role":"assistant"`)
				if test.failAt == 2 {
					require.NotContains(t, string(wire), "hello world")
				} else {
					require.Contains(t, string(wire), "hello world", "the real text flush must reach the client before returning ErrClosedPipe")
				}
			}

			if !test.wantCharge {
				requireSSECommitSettlementRefund(t, fixture)
			}
			failedUser, failedToken := sseCommitSettlementBalances(t, fixture)
			failedCharge := fixture.user.Quota - failedUser.Quota
			if test.wantCharge {
				require.Positive(t, failedCharge, "generated text must not be fully refunded on downstream failure")
				require.Less(t, failedCharge, 104, "the unread completed report or prepayment must not replace the partial usage")
			} else {
				require.Zero(t, failedCharge)
			}
			require.Equal(t, failedCharge, fixture.token.RemainQuota-failedToken.RemainQuota)
			require.Equal(t, failedCharge, failedUser.UsedQuota)
			require.Equal(t, failedCharge, failedToken.UsedQuota)
			var failureLogs []model.Log
			require.NoError(t, fixture.db.Where("user_id = ? AND type = ?", fixture.user.Id, model.LogTypeConsume).Find(&failureLogs).Error)
			require.Len(t, failureLogs, 1, "client failure must settle the accepted usage through the real billing path")
			require.Equal(t, failedCharge, failureLogs[0].Quota)
			require.Equal(t, test.wantPrompt, failureLogs[0].PromptTokens)
			require.Equal(t, test.wantCompletion, failureLogs[0].CompletionTokens)
			t.Logf("failed request: wallet/token charge=%d, prompt/completion=%d/%d, flushes=%d", failedCharge, failureLogs[0].PromptTokens, failureLogs[0].CompletionTokens, failureWriter.flushes.Load())

			// Even a charged partial failure releases the success-only slot.
			success, err := client.Do(sseCommitSettlementRequest(t, fixture, gateway.URL, "/v1/chat/completions"))
			require.NoError(t, err)
			successWire, err := io.ReadAll(success.Body)
			require.NoError(t, success.Body.Close())
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, success.StatusCode, string(successWire))
			require.Contains(t, string(successWire), "hello world")
			require.Contains(t, string(successWire), `"finish_reason":"stop"`)
			require.Equal(t, 1, strings.Count(string(successWire), "data: [DONE]\n\n"))
			require.EqualValues(t, 2, upstreamCalls.Load())
			settledUser, settledToken := sseCommitSettlementBalances(t, fixture)
			successCharge := failedUser.Quota - settledUser.Quota
			require.Positive(t, successCharge)
			require.Equal(t, successCharge, failedToken.RemainQuota-settledToken.RemainQuota)
			require.Equal(t, failedCharge+successCharge, settledUser.UsedQuota)
			require.Equal(t, failedCharge+successCharge, settledToken.UsedQuota)

			limited, err := client.Do(sseCommitSettlementRequest(t, fixture, gateway.URL, "/v1/chat/completions"))
			require.NoError(t, err)
			limitedWire, err := io.ReadAll(limited.Body)
			require.NoError(t, limited.Body.Close())
			require.NoError(t, err)
			require.Equal(t, http.StatusTooManyRequests, limited.StatusCode, string(limitedWire))
			require.EqualValues(t, 2, upstreamCalls.Load(), "the request after one success must be limited before upstream")
			limitedUser, limitedToken := sseCommitSettlementBalances(t, fixture)
			require.Equal(t, settledUser.Quota, limitedUser.Quota)
			require.Equal(t, settledUser.UsedQuota, limitedUser.UsedQuota)
			require.Equal(t, settledToken.RemainQuota, limitedToken.RemainQuota)
			require.Equal(t, settledToken.UsedQuota, limitedToken.UsedQuota)
		})
	}
}
