package relay

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/setting/model_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestResponsesWSToolSchemaNormalizationRespectsPassThrough(t *testing.T) {
	gin.SetMode(gin.TestMode)
	originalSetting := model_setting.GetGlobalSettings().PassThroughRequestEnabled
	t.Cleanup(func() { model_setting.GetGlobalSettings().PassThroughRequestEnabled = originalSetting })
	for _, tt := range []struct {
		name            string
		global, channel bool
		remaining       int
	}{{"normalized", false, false, 0}, {"global pass-through", true, false, 1}, {"channel pass-through", false, true, 1}} {
		t.Run(tt.name, func(t *testing.T) {
			model_setting.GetGlobalSettings().PassThroughRequestEnabled = tt.global
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("GET", "/v1/responses", nil)
			common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
			common.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{PassThroughBodyEnabled: tt.channel})
			req := dto.OpenAIResponsesRequest{Model: "gpt-test", Input: json.RawMessage(`"hello"`), Tools: json.RawMessage(`[{"type":"function","name":"lookup","parameters":{"required":null}}]`)}
			info := &relaycommon.RelayInfo{OriginModelName: "gpt-test", Request: &req}
			payload, apiErr := buildResponsesWSCreatePayload(c, info, req, nil)
			require.Nil(t, apiErr)
			require.Equal(t, tt.remaining, strings.Count(string(payload), `"required":null`))
			require.Contains(t, string(req.Tools), `"required":null`, "original request must survive retry")
		})
	}
}

func TestResponsesWSToolSchemaNormalizationKeepsNumericPrecision(t *testing.T) {
	gin.SetMode(gin.TestMode)
	originalSetting := model_setting.GetGlobalSettings().PassThroughRequestEnabled
	t.Cleanup(func() { model_setting.GetGlobalSettings().PassThroughRequestEnabled = originalSetting })
	for _, tt := range []struct {
		name            string
		global, channel bool
	}{{"normalized", false, false}, {"global pass-through", true, false}, {"channel pass-through", false, true}} {
		t.Run(tt.name, func(t *testing.T) {
			model_setting.GetGlobalSettings().PassThroughRequestEnabled = tt.global
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("GET", "/v1/responses", nil)
			common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
			common.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{PassThroughBodyEnabled: tt.channel})
			common.SetContextKey(c, constant.ContextKeyChannelParamOverride, map[string]any{"max_output_tokens": 7})
			req := dto.OpenAIResponsesRequest{
				Model: "gpt-test", ServiceTier: "flex",
				Input: json.RawMessage(`{"n":9007199254740993}`),
				Tools: json.RawMessage(`[{"type":"function","name":"lookup","parameters":{"required":null,"minimum":9007199254740993,"default":{"required":null,"n":9007199254740993}}}]`),
			}
			info := &relaycommon.RelayInfo{OriginModelName: "gpt-test", Request: &req}
			payload, apiErr := buildResponsesWSCreatePayload(c, info, req, nil)
			require.Nil(t, apiErr)
			require.Equal(t, 3, strings.Count(string(payload), "9007199254740993"), string(payload))
			require.NotContains(t, string(payload), "9007199254740992")
			require.Contains(t, string(payload), `"max_output_tokens":7`)
			if !tt.global && !tt.channel {
				require.NotContains(t, string(payload), `"service_tier"`)
			}
			var event map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(payload, &event))
			var tools []struct {
				Parameters map[string]json.RawMessage `json:"parameters"`
			}
			require.NoError(t, json.Unmarshal(event["tools"], &tools))
			require.JSONEq(t, `{"required":null,"n":9007199254740993}`, string(tools[0].Parameters["default"]))
		})
	}
}
