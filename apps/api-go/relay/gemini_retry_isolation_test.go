package relay

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/LIghtJUNction/api.lmm.best/setting/model_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGeminiPassthroughRetryDoesNotInheritChannelSystemPrompt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	global := model_setting.GetGlobalSettings()
	originalSetting := global.PassThroughRequestEnabled
	t.Cleanup(func() { global.PassThroughRequestEnabled = originalSetting })

	for _, globalPassthrough := range []bool{false, true} {
		t.Run(map[bool]string{false: "channel passthrough", true: "global passthrough"}[globalPassthrough], func(t *testing.T) {
			bodies := make(chan string, 2)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				}
				bodies <- string(body)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = io.WriteString(w, `{"error":{"message":"fixture failure"}}`)
			}))
			defer upstream.Close()

			raw := `{"contents":[{"role":"user","parts":[{"text":"hello"}]}],"systemInstruction":{"parts":[{"text":"original"}]},"tools":[{"functionDeclarations":[{"name":"lookup","parameters":{"required":null}}]}]}`
			var original dto.GeminiChatRequest
			require.NoError(t, json.Unmarshal([]byte(raw), &original))
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-test:generateContent", strings.NewReader(raw))
			c.Request.Header.Set("Content-Type", "application/json")
			t.Cleanup(func() { common.CleanupBodyStorage(c) })
			common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
			common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, upstream.URL)
			common.SetContextKey(c, constant.ContextKeyOriginalModel, "gemini-test")
			common.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{
				PassThroughBodyEnabled: !globalPassthrough,
				SystemPrompt:           "first-channel",
				SystemPromptOverride:   true,
			})
			info := &relaycommon.RelayInfo{OriginModelName: "gemini-test", RelayFormat: types.RelayFormatGemini, Request: &original}

			global.PassThroughRequestEnabled = globalPassthrough
			require.NotNil(t, GeminiHelper(c, info))
			require.Equal(t, "original", original.SystemInstructions.Parts[0].Text)
			require.Len(t, bodies, 1)
			require.JSONEq(t, raw, <-bodies, "first channel must forward the original raw body")

			global.PassThroughRequestEnabled = false
			common.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{})
			require.NotNil(t, GeminiHelper(c, info))
			require.Len(t, bodies, 1)
			retryBody := <-bodies
			require.NotContains(t, retryBody, "first-channel")
			require.Contains(t, retryBody, "original")
			require.NotContains(t, retryBody, `"required":null`, "converted retry must normalize schemas")
			require.Contains(t, string(original.Tools), `"required":null`, "retry must keep the original tools")
		})
	}
}
