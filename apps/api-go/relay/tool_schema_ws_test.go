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
