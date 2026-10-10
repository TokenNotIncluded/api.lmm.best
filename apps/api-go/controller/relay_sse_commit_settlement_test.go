package controller

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

var sseCommitSettlementUserID atomic.Int64

// These fixtures use the actual authenticated controller, SQLite reservations,
// and settlement with loopback HTTP on both sides. No billing callback is mocked.
func newSSECommitSettlementFixture(t *testing.T, firstOutputTimeout int) *drawingParityFixture {
	t.Helper()
	service.InitTokenEncoders()
	fixture := newDrawingParityFixture(t, common.GetTrustQuota(), http.StatusOK)
	require.NoError(t, fixture.db.AutoMigrate(&model.PublicRelayPreference{}, &model.SubscriptionPreConsumeRecord{}))

	// The process-local success window outlives each isolated SQL fixture. Give
	// this fixture's real owner a unique ID without altering authenticated identity.
	userID := 1_700_000_000 + int(sseCommitSettlementUserID.Add(1))
	require.NoError(t, fixture.db.Model(&model.User{}).Where("id = ?", fixture.user.Id).Update("id", userID).Error)
	fixture.user.Id = userID
	require.NoError(t, fixture.db.Model(&fixture.token).Update("user_id", userID).Error)
	fixture.token.UserId = userID

	previousRatios, err := json.Marshal(ratio_setting.GetModelRatioCopy())
	require.NoError(t, err)
	previousRetry, previousTimeout := common.RetryTimes, constant.StreamingTimeout
	previousFirstOutput := common.OpenAIFirstOutputTimeout
	previousEnabled := setting.ModelRequestRateLimitEnabled
	previousDuration := setting.ModelRequestRateLimitDurationMinutes
	previousTotal, previousSuccess := setting.ModelRequestRateLimitCount, setting.ModelRequestRateLimitSuccessCount
	previousGroups := setting.ModelRequestRateLimitGroup2JSONString()
	t.Cleanup(func() {
		common.RetryTimes, constant.StreamingTimeout = previousRetry, previousTimeout
		common.OpenAIFirstOutputTimeout = previousFirstOutput
		setting.ModelRequestRateLimitEnabled = previousEnabled
		setting.ModelRequestRateLimitDurationMinutes = previousDuration
		setting.ModelRequestRateLimitCount, setting.ModelRequestRateLimitSuccessCount = previousTotal, previousSuccess
		require.NoError(t, setting.UpdateModelRequestRateLimitGroupByJSONString(previousGroups))
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(string(previousRatios)))
	})
	common.RetryTimes, constant.StreamingTimeout = 2, 5
	common.OpenAIFirstOutputTimeout = firstOutputTimeout
	setting.ModelRequestRateLimitEnabled = true
	setting.ModelRequestRateLimitDurationMinutes = 1
	setting.ModelRequestRateLimitCount, setting.ModelRequestRateLimitSuccessCount = 0, 1
	require.NoError(t, setting.UpdateModelRequestRateLimitGroupByJSONString(`{}`))
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{}`))
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"`+drawingParityModel+`":1}`))
	return fixture
}

func sseCommitSettlementBalances(t *testing.T, fixture *drawingParityFixture) (model.User, model.Token) {
	t.Helper()
	var user model.User
	var token model.Token
	require.NoError(t, fixture.db.First(&user, fixture.user.Id).Error)
	require.NoError(t, fixture.db.First(&token, fixture.token.Id).Error)
	return user, token
}

func requireSSECommitSettlementRefund(t *testing.T, fixture *drawingParityFixture) {
	t.Helper()
	// Wallet/token refunds run through the real asynchronous refund worker.
	require.Eventually(t, func() bool {
		var user model.User
		var token model.Token
		return fixture.db.First(&user, fixture.user.Id).Error == nil &&
			fixture.db.First(&token, fixture.token.Id).Error == nil &&
			user.Quota == fixture.user.Quota && token.RemainQuota == fixture.token.RemainQuota &&
			user.UsedQuota == 0 && token.UsedQuota == 0
	}, 3*time.Second, 5*time.Millisecond, "the actual wallet/token reservation must be fully refunded")
	var chargedLogs int64
	require.NoError(t, fixture.db.Model(&model.Log{}).
		Where("user_id = ? AND type = ? AND quota <> 0", fixture.user.Id, model.LogTypeConsume).
		Count(&chargedLogs).Error)
	require.Zero(t, chargedLogs, "a header-only response must not record a consumption charge")
}

func sseCommitSettlementGateway(t *testing.T, fixture *drawingParityFixture, path string, format types.RelayFormat) *httptest.Server {
	t.Helper()
	engine := gin.New()
	engine.Use(middleware.BodyStorageCleanup())
	engine.POST(path, middleware.TokenAuth(), middleware.RelayRequestAdmission(), middleware.ModelRequestRateLimit(), middleware.Distribute(), func(c *gin.Context) {
		Relay(c, format)
	})
	return httptest.NewServer(engine)
}

func sseCommitSettlementRequest(t *testing.T, fixture *drawingParityFixture, gatewayURL, path string) *http.Request {
	t.Helper()
	input := `"input":"test prompt"`
	if path == "/v1/chat/completions" {
		input = `"messages":[{"role":"user","content":"test prompt"}]`
	}
	request, err := http.NewRequest(http.MethodPost, gatewayURL+path+"?group=image-2", strings.NewReader(
		`{"model":"`+drawingParityModel+`",`+input+`,"stream":true}`))
	require.NoError(t, err)
	request.Header.Set("Authorization", "Bearer "+fixture.token.Key)
	request.Header.Set("Content-Type", "application/json")
	return request
}

func TestSSECommitEmptyEOFFullyRefundsWithoutReplayOrSuccessSlot(t *testing.T) {
	fixture := newSSECommitSettlementFixture(t, 0)
	emptyGate := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(emptyGate) }) }
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := calls.Add(1)
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected converted upstream path %q", r.URL.Path)
			http.Error(w, "wrong route", http.StatusBadRequest)
			return
		}
		var request dto.GeneralOpenAIRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || len(request.Messages) == 0 || request.Stream == nil || !*request.Stream {
			t.Errorf("Responses request was not converted to streaming Chat messages: %v", err)
			http.Error(w, "wrong request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		if call == 1 {
			select {
			case <-emptyGate:
			case <-r.Context().Done():
			}
			return // A real, clean HTTP EOF without any SSE business frame.
		}
		fmt.Fprint(w, "data: {\"id\":\"chat_fixture\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello world\"}}]}\n\n"+
			"data: {\"id\":\"chat_fixture\",\"choices\":[],\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":2,\"total_tokens\":102}}\n\n"+
			"data: {\"id\":\"chat_fixture\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(func() { release(); upstream.Close() })
	fixture.channel.Type = constant.ChannelTypeAdvancedCustom
	fixture.channel.BaseURL = &upstream.URL
	fixture.channel.SetOtherSettings(dto.ChannelOtherSettings{AdvancedCustom: &dto.AdvancedCustomConfig{
		Routes: []dto.AdvancedCustomRoute{{
			IncomingPath: "/v1/responses", UpstreamPath: "/v1/chat/completions",
			Converter: relayconvert.ConverterOpenAIResponsesToOpenAIChat,
		}},
	}})
	require.NoError(t, fixture.db.Save(&fixture.channel).Error)
	model.InvalidatePricingCache()
	gateway := sseCommitSettlementGateway(t, fixture, "/v1/responses", types.RelayFormatOpenAIResponses)
	t.Cleanup(func() { release(); gateway.Close() })
	client := &http.Client{Timeout: 5 * time.Second}

	// Do must return the real 200 headers while upstream is still gated. A
	// recorder or opening the gate before Do returns would not prove early commit.
	response, err := client.Do(sseCommitSettlementRequest(t, fixture, gateway.URL, "/v1/responses"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = response.Body.Close() })
	require.Equal(t, http.StatusOK, response.StatusCode)
	require.Equal(t, "text/event-stream", response.Header.Get("Content-Type"))
	select {
	case <-emptyGate:
		t.Fatal("upstream EOF gate was opened before the client received headers")
	default:
	}
	reservedUser, reservedToken := sseCommitSettlementBalances(t, fixture)
	reserved := fixture.user.Quota - reservedUser.Quota
	require.Positive(t, reserved, "the real controller must reserve wallet quota before calling upstream")
	require.Equal(t, reserved, fixture.token.RemainQuota-reservedToken.RemainQuota)
	require.EqualValues(t, 1, calls.Load())

	release()
	wire, err := io.ReadAll(response.Body)
	require.NoError(t, response.Body.Close())
	require.NoError(t, err)
	require.Empty(t, wire, "empty EOF must finish committed SSE without an appended JSON error or a fabricated event")
	require.EqualValues(t, 1, calls.Load(), "RetryTimes=2 must not replay after the early HTTP commit")
	requireSSECommitSettlementRefund(t, fixture)

	// The failure must release the one success slot. A real completed request
	// then bills normally and consumes exactly that slot.
	success, err := client.Do(sseCommitSettlementRequest(t, fixture, gateway.URL, "/v1/responses"))
	require.NoError(t, err)
	successWire, err := io.ReadAll(success.Body)
	require.NoError(t, success.Body.Close())
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, success.StatusCode, string(successWire))
	require.Contains(t, string(successWire), "hello world")
	require.Equal(t, 1, strings.Count(string(successWire), "event: response.completed\n"))
	require.NotContains(t, string(successWire), "event: response.failed\n")
	require.EqualValues(t, 2, calls.Load())
	settledUser, settledToken := sseCommitSettlementBalances(t, fixture)
	charge := fixture.user.Quota - settledUser.Quota
	require.Positive(t, charge)
	require.Equal(t, charge, fixture.token.RemainQuota-settledToken.RemainQuota)
	require.Equal(t, charge, settledUser.UsedQuota)
	require.Equal(t, charge, settledToken.UsedQuota)
	var logs []model.Log
	require.NoError(t, fixture.db.Where("user_id = ? AND type = ? AND quota > 0", fixture.user.Id, model.LogTypeConsume).Find(&logs).Error)
	require.Len(t, logs, 1)
	require.Equal(t, charge, logs[0].Quota)
	require.Equal(t, 100, logs[0].PromptTokens)
	require.Equal(t, 2, logs[0].CompletionTokens)

	limited, err := client.Do(sseCommitSettlementRequest(t, fixture, gateway.URL, "/v1/responses"))
	require.NoError(t, err)
	limitedWire, err := io.ReadAll(limited.Body)
	require.NoError(t, limited.Body.Close())
	require.NoError(t, err)
	require.Equal(t, http.StatusTooManyRequests, limited.StatusCode, string(limitedWire))
	require.EqualValues(t, 2, calls.Load(), "the limited request must not reach upstream")
	limitedUser, limitedToken := sseCommitSettlementBalances(t, fixture)
	require.Equal(t, settledUser.Quota, limitedUser.Quota)
	require.Equal(t, settledUser.UsedQuota, limitedUser.UsedQuota)
	require.Equal(t, settledToken.RemainQuota, limitedToken.RemainQuota)
	require.Equal(t, settledToken.UsedQuota, limitedToken.UsedQuota)
}

func TestSSECommitFirstOutputTimeoutReturnsReal504AndRefund(t *testing.T) {
	fixture := newSSECommitSettlementFixture(t, 1)
	common.RetryTimes = 0 // Observe the current attempt's actual precommit 504.
	ready, closed := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		defer close(closed)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n")
		w.(http.Flusher).Flush()
		close(ready)
		<-r.Context().Done()
	}))
	t.Cleanup(upstream.Close)
	require.NoError(t, fixture.db.Model(&fixture.channel).Update("base_url", upstream.URL).Error)
	model.InvalidatePricingCache()
	gateway := sseCommitSettlementGateway(t, fixture, "/v1/chat/completions", types.RelayFormatOpenAI)
	t.Cleanup(func() { gateway.CloseClientConnections(); gateway.Close() })
	client := &http.Client{Timeout: 5 * time.Second}
	request := sseCommitSettlementRequest(t, fixture, gateway.URL, "/v1/chat/completions")
	type result struct {
		response *http.Response
		err      error
	}
	resultCh := make(chan result, 1)
	go func() {
		response, err := client.Do(request)
		resultCh <- result{response, err}
	}()
	select {
	case <-ready:
	case <-time.After(3 * time.Second):
		t.Fatal("authenticated upstream request did not start")
	}
	select {
	case early := <-resultCh:
		if early.response != nil {
			_ = early.response.Body.Close()
		}
		t.Fatalf("role-only output committed an early HTTP response: %v", early.err)
	default:
	}
	reservedUser, reservedToken := sseCommitSettlementBalances(t, fixture)
	reserved := fixture.user.Quota - reservedUser.Quota
	require.Positive(t, reserved)
	require.Equal(t, reserved, fixture.token.RemainQuota-reservedToken.RemainQuota)
	var final result
	select {
	case final = <-resultCh:
	case <-time.After(4 * time.Second):
		t.Fatal("first-output deadline did not return a real HTTP response")
	}
	require.NoError(t, final.err)
	defer final.response.Body.Close()
	wire, err := io.ReadAll(final.response.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusGatewayTimeout, final.response.StatusCode, string(wire))
	require.Contains(t, final.response.Header.Get("Content-Type"), "application/json")
	require.True(t, json.Valid(wire), "timeout must be one valid JSON document")
	require.Contains(t, string(wire), "upstream_timeout")
	require.NotContains(t, string(wire), "data:")
	require.NotContains(t, string(wire), "assistant")
	require.EqualValues(t, 1, calls.Load())
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("first-output timeout did not close the upstream HTTP response")
	}
	requireSSECommitSettlementRefund(t, fixture)
}
