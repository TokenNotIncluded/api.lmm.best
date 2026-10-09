package helper

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/pkg/servicetier"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func tierTestContext(t *testing.T, premium bool) (*gin.Context, *relaycommon.RelayInfo) {
	t.Helper()
	p := servicetier.DefaultPolicy()
	p.Enabled = true
	p.FastGroups = []string{"paid"}
	p.UltrafastGroups = []string{"paid"}
	encoded, err := json.Marshal(p)
	require.NoError(t, err)
	common.OptionMapRWMutex.Lock()
	old, existed := common.OptionMap[servicetier.PolicyOption]
	if common.OptionMap == nil {
		common.OptionMap = map[string]string{}
	}
	common.OptionMap[servicetier.PolicyOption] = string(encoded)
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		defer common.OptionMapRWMutex.Unlock()
		if existed {
			common.OptionMap[servicetier.PolicyOption] = old
		} else {
			delete(common.OptionMap, servicetier.PolicyOption)
		}
	})
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	info := &relaycommon.RelayInfo{UsingGroup: "paid", ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI, ChannelBaseUrl: "https://api.openai.com"}}
	if premium {
		data, err := os.ReadFile("../../pkg/servicetier/testdata/official-prices.md")
		require.NoError(t, err)
		catalog, err := servicetier.ParseCatalog(string(data), time.Now())
		require.NoError(t, err)
		info.ServiceTierQuote, err = servicetier.NewQuote(catalog, p, "gpt-6-astra", "ultrafast", "paid", .1, 1000, time.Now())
		require.NoError(t, err)
	}
	return c, info
}
func TestServiceTierGuardPinsOrdinaryRequestsAndBlocksPassthrough(t *testing.T) {
	c, info := tierTestContext(t, false)
	for _, body := range []string{`{"model":"gpt-6-astra"}`, `{"model":"gpt-6-astra","service_tier":"auto"}`} {
		result, err := ApplyServiceTierToJSON(c, info, "https://api.openai.com/v1/responses", "", http.Header{}, []byte(body))
		require.NoError(t, err)
		var data map[string]any
		require.NoError(t, json.Unmarshal(result, &data))
		require.Equal(t, "default", data["service_tier"])
	}
	for _, tier := range []string{"fast", "priority", "ultrafast"} {
		_, err := ApplyServiceTierToJSON(c, info, "https://api.openai.com/v1/responses", "", http.Header{}, []byte(`{"model":"gpt-6-astra","service_tier":"`+tier+`"}`))
		require.Error(t, err)
	}
	_, err := ApplyServiceTierToJSON(c, info, "https://api.openai.com/v1/responses", "", http.Header{"Openai-Service-Tier": []string{"ultrafast"}}, []byte(`{"model":"gpt-6-astra"}`))
	require.Error(t, err)
}
func TestServiceTierGuardRestoresReservedTierAndCapsOutput(t *testing.T) {
	c, info := tierTestContext(t, true)
	for _, path := range []string{"/v1/responses", "/v1/chat/completions"} {
		request := httptest.NewRequest(http.MethodPost, "https://api.openai.com"+path, strings.NewReader(`{"model":"gpt-6-astra","tools":[{"type":"function"}]}`))
		require.NoError(t, ApplyServiceTierToRequest(c, request, info))
		body, err := io.ReadAll(request.Body)
		require.NoError(t, err)
		var data map[string]any
		require.NoError(t, json.Unmarshal(body, &data))
		require.Equal(t, "ultrafast", data["service_tier"])
		key := "max_output_tokens"
		if strings.Contains(path, "chat") {
			key = "max_completion_tokens"
		}
		require.Equal(t, float64(1000), data[key])
		require.Equal(t, int64(len(body)), request.ContentLength)
		replay, err := request.GetBody()
		require.NoError(t, err)
		again, err := io.ReadAll(replay)
		require.NoError(t, err)
		require.Equal(t, body, again)
		_ = replay.Close()
	}
}
func TestServiceTierGuardRejectsChangedContract(t *testing.T) {
	c, info := tierTestContext(t, true)
	for _, body := range []string{
		`{"model":"other","service_tier":"ultrafast"}`, `{"model":"gpt-6-astra","service_tier":"fast"}`,
		`{"model":"gpt-6-astra","max_output_tokens":1001}`, `{"model":"gpt-6-astra","max_output_tokens":0}`,
		`{"model":"gpt-6-astra","n":2}`, `{"model":"gpt-6-astra","background":true}`,
		`{"model":"gpt-6-astra","tools":[{"type":"web_search"}]}`, `{"model":"gpt-6-astra","modalities":["audio"]}`,
	} {
		t.Run(body, func(t *testing.T) {
			_, err := ApplyServiceTierToJSON(c, info, "https://api.openai.com/v1/responses", "", http.Header{}, []byte(body))
			require.Error(t, err)
		})
	}
	for _, target := range []string{"https://eu.api.openai.com/v1/responses", "https://third-party.test/v1/responses"} {
		_, err := ApplyServiceTierToJSON(c, info, target, "", http.Header{}, []byte(`{"model":"gpt-6-astra"}`))
		require.Error(t, err)
	}
	info.UsingGroup = "normal"
	_, err := ApplyServiceTierToJSON(c, info, "https://api.openai.com/v1/responses", "", http.Header{}, []byte(`{"model":"gpt-6-astra"}`))
	require.Error(t, err)
}

func TestServiceTierGuardRejectsReservedQuoteAfterCrossChannelRetry(t *testing.T) {
	for _, tc := range []struct {
		name        string
		channelType int
		missingMeta bool
	}{
		{name: "Azure retry", channelType: constant.ChannelTypeAzure},
		{name: "custom retry", channelType: constant.ChannelTypeCustom},
		{name: "missing retry metadata", missingMeta: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var upstreamCalls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				upstreamCalls.Add(1)
				w.WriteHeader(http.StatusOK)
			}))
			defer upstream.Close()

			c, info := tierTestContext(t, true)
			reservedQuote := info.ServiceTierQuote
			if tc.missingMeta {
				info.ChannelMeta = nil
			} else {
				// Match the normal retry path: the first admission owns the quote,
				// while the new channel supplies its own metadata and overrides.
				common.SetContextKey(c, constant.ContextKeyChannelType, tc.channelType)
				common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, upstream.URL)
				info.InitChannelMeta(c)
			}
			require.Same(t, reservedQuote, info.ServiceTierQuote)
			body := `{"model":"gpt-6-astra","service_tier":"ultrafast"}`
			target := upstream.URL + "/v1/responses"
			request, err := http.NewRequest(http.MethodPost, target, strings.NewReader(body))
			require.NoError(t, err)
			guardErr := ApplyServiceTierToRequest(c, request, info)
			if guardErr == nil {
				response, sendErr := http.DefaultClient.Do(request)
				require.NoError(t, sendErr)
				require.NoError(t, response.Body.Close())
			} else {
				require.NoError(t, request.Body.Close())
			}
			require.Error(t, guardErr, "an unsupported retry must fail before upstream work")
			require.Zero(t, upstreamCalls.Load())
			// The JSON boundary is shared with Responses WebSocket creates.
			_, err = ApplyServiceTierToJSON(c, info, target, "", http.Header{}, []byte(body))
			require.Error(t, err)
		})
	}
}

func TestServiceTierGuardPreservesOrdinaryUnsupportedChannels(t *testing.T) {
	for _, channelType := range []int{constant.ChannelTypeAzure, constant.ChannelTypeCustom} {
		c, info := tierTestContext(t, false)
		common.SetContextKey(c, constant.ContextKeyChannelType, channelType)
		info.InitChannelMeta(c)
		body := `{"model":"ordinary-model","custom_parameter":true}`
		target := "https://compatible.example/v1/responses"
		patched, err := ApplyServiceTierToJSON(c, info, target, "", http.Header{}, []byte(body))
		require.NoError(t, err)
		require.Equal(t, body, string(patched))
		request := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
		require.NoError(t, ApplyServiceTierToRequest(c, request, info))
		outbound, err := io.ReadAll(request.Body)
		require.NoError(t, err)
		require.NoError(t, request.Body.Close())
		require.Equal(t, body, string(outbound))
	}
}
