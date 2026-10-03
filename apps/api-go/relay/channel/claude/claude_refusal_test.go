package claude

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/LIghtJUNction/api.lmm.best/setting/model_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const refusalZeroOutputUsage = `{"input_tokens":100,"output_tokens":0,"cache_read_input_tokens":30,"cache_creation_input_tokens":50,"cache_creation":{"ephemeral_5m_input_tokens":20,"ephemeral_1h_input_tokens":30}}`

func setClaudeRefusalNoChargeForTest(t *testing.T, enabled bool) {
	t.Helper()
	settings := model_setting.GetClaudeSettings()
	previous := settings.RefusalNoOutputNoChargeEnabled
	settings.RefusalNoOutputNoChargeEnabled = enabled
	t.Cleanup(func() { settings.RefusalNoOutputNoChargeEnabled = previous })
}

func setClaudeStreamingTimeoutForTest(t *testing.T) {
	t.Helper()
	previous := constant.StreamingTimeout
	constant.StreamingTimeout = 300
	t.Cleanup(func() { constant.StreamingTimeout = previous })
}

func newClaudeRefusalTestContext(format types.RelayFormat, stream bool) (*gin.Context, *httptest.ResponseRecorder, *relaycommon.RelayInfo) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Set(common.RequestIdKey, "claude-refusal-test")
	info := &relaycommon.RelayInfo{
		StartTime:          time.Now(),
		OriginModelName:    "claude-sonnet-4-5",
		RelayFormat:        format,
		IsStream:           stream,
		ShouldIncludeUsage: true,
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "claude-sonnet-4-5",
		},
	}
	return c, recorder, info
}

func claudeRefusalTestResponse(body string, stream bool) *http.Response {
	contentType := "application/json"
	if stream {
		contentType = "text/event-stream"
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{contentType}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func claudeRefusalSSE(events ...string) string {
	var body strings.Builder
	for _, event := range events {
		fmt.Fprintf(&body, "event: %s\ndata: %s\n\n", gjson.Get(event, "type").String(), event)
	}
	return body.String()
}

func requireClaudeRefusalUsagePreserved(t *testing.T, usage *dto.Usage) {
	t.Helper()
	require.NotNil(t, usage)
	assert.Equal(t, 100, usage.PromptTokens)
	assert.Zero(t, usage.CompletionTokens)
	assert.Equal(t, 100, usage.TotalTokens)
	assert.Equal(t, 30, usage.PromptTokensDetails.CachedTokens)
	assert.Equal(t, 50, usage.PromptTokensDetails.CachedCreationTokens)
	assert.Equal(t, 20, usage.ClaudeCacheCreation5mTokens)
	assert.Equal(t, 30, usage.ClaudeCacheCreation1hTokens)
	require.NotNil(t, usage.BillingUsage)
	assert.Equal(t, dto.BillingUsageSourceClaudeMessages, usage.BillingUsage.Source)
	require.NotNil(t, usage.BillingUsage.ClaudeUsage)
	assert.Equal(t, 100, usage.BillingUsage.ClaudeUsage.InputTokens)
	assert.Zero(t, usage.BillingUsage.ClaudeUsage.OutputTokens)
	assert.Equal(t, 30, usage.BillingUsage.ClaudeUsage.CacheReadInputTokens)
	assert.Equal(t, 50, usage.BillingUsage.ClaudeUsage.CacheCreationInputTokens)
	assert.Equal(t, 20, usage.BillingUsage.ClaudeUsage.GetCacheCreation5mTokens())
	assert.Equal(t, 30, usage.BillingUsage.ClaudeUsage.GetCacheCreation1hTokens())
}

func requireClaudeRefusalResponsesBody(t *testing.T, body string) {
	t.Helper()
	assert.Equal(t, "response", gjson.Get(body, "object").String())
	assert.Equal(t, "incomplete", gjson.Get(body, "status").String())
	assert.Equal(t, "content_filter", gjson.Get(body, "incomplete_details.reason").String())
	assert.Empty(t, gjson.Get(body, "output").Array())
	assert.EqualValues(t, 180, gjson.Get(body, "usage.input_tokens").Int())
	assert.True(t, gjson.Get(body, "usage.output_tokens").Exists())
	assert.Zero(t, gjson.Get(body, "usage.output_tokens").Int())
	assert.EqualValues(t, 30, gjson.Get(body, "usage.input_tokens_details.cached_tokens").Int())
	assert.EqualValues(t, 50, gjson.Get(body, "usage.input_tokens_details.cached_creation_tokens").Int())
}

func TestClaudeRefusalNoOutputNonStreamBillingExemption(t *testing.T) {
	gin.SetMode(gin.TestMode)
	formats := []types.RelayFormat{types.RelayFormatClaude, types.RelayFormatOpenAI, types.RelayFormatOpenAIResponses}
	tests := []struct {
		name       string
		enabled    bool
		stopReason string
		content    string
		usage      string
		wantExempt bool
	}{
		{name: "default disabled", stopReason: "refusal", content: `[]`, usage: refusalZeroOutputUsage},
		{name: "enabled empty refusal", enabled: true, stopReason: "refusal", content: `[]`, usage: refusalZeroOutputUsage, wantExempt: true},
		{name: "ordinary empty response", enabled: true, stopReason: "end_turn", content: `[]`, usage: refusalZeroOutputUsage},
		{name: "missing usage", enabled: true, stopReason: "refusal", content: `[]`},
		{name: "missing output token count", enabled: true, stopReason: "refusal", content: `[]`, usage: `{"input_tokens":100}`},
		{name: "empty usage object", enabled: true, stopReason: "refusal", content: `[]`, usage: `{}`},
		{name: "positive output token count", enabled: true, stopReason: "refusal", content: `[]`, usage: `{"input_tokens":100,"output_tokens":1}`},
		{name: "text output", enabled: true, stopReason: "refusal", content: `[{"type":"text","text":"I cannot help."}]`, usage: refusalZeroOutputUsage},
		{name: "empty text block", enabled: true, stopReason: "refusal", content: `[{"type":"text","text":""}]`, usage: refusalZeroOutputUsage},
		{name: "thinking output", enabled: true, stopReason: "refusal", content: `[{"type":"thinking","thinking":"reasoning"}]`, usage: refusalZeroOutputUsage},
		{name: "tool output", enabled: true, stopReason: "refusal", content: `[{"type":"tool_use","id":"tool_1","name":"lookup","input":{}}]`, usage: refusalZeroOutputUsage},
	}

	for _, format := range formats {
		for _, tt := range tests {
			t.Run(fmt.Sprintf("%s/%s", format, tt.name), func(t *testing.T) {
				setClaudeRefusalNoChargeForTest(t, tt.enabled)
				c, recorder, info := newClaudeRefusalTestContext(format, false)
				body := fmt.Sprintf(`{"id":"msg_refusal","type":"message","role":"assistant","model":"claude-sonnet-4-5","content":%s,"stop_reason":%q`, tt.content, tt.stopReason)
				if tt.usage != "" {
					body += `,"usage":` + tt.usage
				}
				body += `}`

				gotUsage, apiErr := (&Adaptor{}).DoResponse(c, claudeRefusalTestResponse(body, false), info)
				require.Nil(t, apiErr)
				usage, ok := gotUsage.(*dto.Usage)
				require.True(t, ok)
				reason := common.GetContextKeyString(c, constant.ContextKeyBillingExemptReason)
				if tt.wantExempt {
					assert.Equal(t, "claude_refusal_no_output", reason)
					requireClaudeRefusalUsagePreserved(t, usage)
					if format == types.RelayFormatClaude {
						assert.JSONEq(t, body, recorder.Body.String())
					} else if format == types.RelayFormatOpenAI {
						assert.EqualValues(t, 180, gjson.Get(recorder.Body.String(), "usage.prompt_tokens").Int())
						assert.Zero(t, gjson.Get(recorder.Body.String(), "usage.completion_tokens").Int())
					} else if format == types.RelayFormatOpenAIResponses {
						requireClaudeRefusalResponsesBody(t, recorder.Body.String())
					}
				} else {
					assert.Empty(t, reason)
				}
				if tt.stopReason == "refusal" {
					assert.Equal(t, "claude_stop_reason=refusal", common.GetContextKeyString(c, constant.ContextKeyAdminRejectReason))
				}
			})
		}
	}
}

func TestClaudeRefusalNoChargePreservesBilledCategoriesAndFallbackAttempts(t *testing.T) {
	setClaudeRefusalNoChargeForTest(t, true)
	setClaudeStreamingTimeoutForTest(t)
	for _, category := range []struct {
		name, raw string
		wantFree  bool
	}{
		{name: "bio", raw: `"bio"`},
		{name: "frontier_llm", raw: `"frontier_llm"`},
		{name: "reasoning_extraction", raw: `"reasoning_extraction"`},
		{name: "cyber", raw: `"cyber"`, wantFree: true},
		{name: "general_harms", raw: `"general_harms"`, wantFree: true},
		{name: "null", raw: `null`, wantFree: true},
		{name: "malformed", raw: `{}`},
	} {
		for _, format := range []types.RelayFormat{types.RelayFormatClaude, types.RelayFormatOpenAI, types.RelayFormatOpenAIResponses} {
			for _, stream := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/stream=%t", category.name, format, stream), func(t *testing.T) {
					c, recorder, info := newClaudeRefusalTestContext(format, stream)
					details := `{"type":"refusal","category":` + category.raw + `}`
					body := `{"id":"msg_category","type":"message","role":"assistant","model":"claude-sonnet-4-5","content":[],"stop_reason":"refusal","stop_details":` + details + `,"usage":` + refusalZeroOutputUsage + `}`
					if stream {
						body = claudeRefusalSSE(
							`{"type":"message_start","message":{"id":"msg_category","model":"claude-sonnet-4-5","usage":`+refusalZeroOutputUsage+`}}`,
							`{"type":"message_delta","delta":{"stop_reason":"refusal","stop_details":`+details+`},"usage":{"output_tokens":0}}`,
							`{"type":"message_stop"}`,
						)
					}
					usage, apiErr := (&Adaptor{}).DoResponse(c, claudeRefusalTestResponse(body, stream), info)
					require.Nil(t, apiErr)
					requireClaudeRefusalUsagePreserved(t, usage.(*dto.Usage))
					require.Equal(t, category.wantFree, common.GetContextKeyString(c, constant.ContextKeyBillingExemptReason) != "")
					if format == types.RelayFormatClaude {
						require.Contains(t, recorder.Body.String(), `"category":`+category.raw)
					}
				})
			}
		}
	}
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("fallback_attempts/stream=%t", stream), func(t *testing.T) {
			c, _, info := newClaudeRefusalTestContext(types.RelayFormatClaude, stream)
			usage := `{"input_tokens":100,"output_tokens":0,"iterations":[{"type":"message","input_tokens":200,"output_tokens":5}]}`
			body := `{"type":"message","content":[],"stop_reason":"refusal","usage":` + usage + `}`
			if stream {
				body = claudeRefusalSSE(`{"type":"message_start","message":{"id":"msg_fallback","usage":`+usage+`}}`, `{"type":"message_delta","delta":{"stop_reason":"refusal"},"usage":{"output_tokens":0}}`, `{"type":"message_stop"}`)
			}
			_, apiErr := (&Adaptor{}).DoResponse(c, claudeRefusalTestResponse(body, stream), info)
			require.Nil(t, apiErr)
			require.Empty(t, common.GetContextKeyString(c, constant.ContextKeyBillingExemptReason))
		})
	}
}

func TestClaudeRefusalNoOutputStreamBillingExemption(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setClaudeStreamingTimeoutForTest(t)
	start := `{"type":"message_start","message":{"id":"msg_refusal","type":"message","role":"assistant","model":"claude-sonnet-4-5","content":[],"usage":` + refusalZeroOutputUsage + `}}`
	refusal := `{"type":"message_delta","delta":{"stop_reason":"refusal"},"usage":{"output_tokens":0}}`
	stop := `{"type":"message_stop"}`
	tests := []struct {
		name       string
		enabled    bool
		events     []string
		wantExempt bool
	}{
		{name: "default disabled", events: []string{start, refusal, stop}},
		{name: "enabled empty refusal", enabled: true, events: []string{start, refusal, stop}, wantExempt: true},
		{name: "missing all usage", enabled: true, events: []string{`{"type":"message_start","message":{"id":"msg_refusal","model":"claude-sonnet-4-5","content":[]}}`, `{"type":"message_delta","delta":{"stop_reason":"refusal"}}`, stop}},
		{name: "missing output token count", enabled: true, events: []string{`{"type":"message_start","message":{"id":"msg_refusal","model":"claude-sonnet-4-5","usage":{"input_tokens":100}}}`, `{"type":"message_delta","delta":{"stop_reason":"refusal"},"usage":{}}`, stop}},
		{name: "positive final output", enabled: true, events: []string{start, `{"type":"message_delta","delta":{"stop_reason":"refusal"},"usage":{"output_tokens":1}}`, stop}},
		{name: "positive initial output", enabled: true, events: []string{`{"type":"message_start","message":{"id":"msg_refusal","model":"claude-sonnet-4-5","usage":{"input_tokens":100,"output_tokens":1}}}`, refusal, stop}},
		{name: "empty text block", enabled: true, events: []string{start, `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`, refusal, stop}},
		{name: "text delta without start", enabled: true, events: []string{start, `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"blocked"}}`, refusal, stop}},
		{name: "thinking block", enabled: true, events: []string{start, `{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`, refusal, stop}},
		{name: "tool block", enabled: true, events: []string{start, `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"tool_1","name":"lookup","input":{}}}`, refusal, stop}},
		{name: "message start contains content", enabled: true, events: []string{`{"type":"message_start","message":{"id":"msg_refusal","model":"claude-sonnet-4-5","content":[{"type":"text","text":"blocked"}],"usage":{"input_tokens":100,"output_tokens":0}}}`, refusal, stop}},
		{name: "ordinary empty response", enabled: true, events: []string{start, `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":0}}`, stop}},
		{name: "earlier refusal superseded", enabled: true, events: []string{start, refusal, `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":0}}`, stop}},
		{name: "refusal without final delta", enabled: true, events: []string{`{"type":"message_start","stop_reason":"refusal","message":{"id":"msg_refusal","model":"claude-sonnet-4-5","content":[],"usage":{"input_tokens":100,"output_tokens":0}}}`, stop}},
		{name: "refusal without final stop reason", enabled: true, events: []string{`{"type":"message_start","stop_reason":"refusal","message":{"id":"msg_refusal","model":"claude-sonnet-4-5","content":[],"usage":{"input_tokens":100,"output_tokens":0}}}`, `{"type":"message_delta","usage":{"output_tokens":0}}`, stop}},
		{name: "refusal without message stop", enabled: true, events: []string{start, refusal}},
	}

	for _, format := range []types.RelayFormat{types.RelayFormatClaude, types.RelayFormatOpenAI, types.RelayFormatOpenAIResponses} {
		for _, tt := range tests {
			t.Run(fmt.Sprintf("%s/%s", format, tt.name), func(t *testing.T) {
				setClaudeRefusalNoChargeForTest(t, tt.enabled)
				c, recorder, info := newClaudeRefusalTestContext(format, true)
				gotUsage, apiErr := (&Adaptor{}).DoResponse(c, claudeRefusalTestResponse(claudeRefusalSSE(tt.events...), true), info)
				require.Nil(t, apiErr)
				usage, ok := gotUsage.(*dto.Usage)
				require.True(t, ok)
				reason := common.GetContextKeyString(c, constant.ContextKeyBillingExemptReason)
				if tt.wantExempt {
					assert.Equal(t, "claude_refusal_no_output", reason)
					requireClaudeRefusalUsagePreserved(t, usage)
					if format == types.RelayFormatClaude {
						assert.Contains(t, recorder.Body.String(), `"input_tokens":100`)
						assert.Contains(t, recorder.Body.String(), `"output_tokens":0`)
						assert.Contains(t, recorder.Body.String(), `"cache_creation_input_tokens":50`)
					} else if format == types.RelayFormatOpenAI {
						assert.Contains(t, recorder.Body.String(), `"prompt_tokens":180`)
						assert.Contains(t, recorder.Body.String(), `"completion_tokens":0`)
					} else if format == types.RelayFormatOpenAIResponses {
						var finalResponse string
						for _, line := range strings.Split(recorder.Body.String(), "\n") {
							data := strings.TrimPrefix(line, "data: ")
							if gjson.Get(data, "type").String() == "response.incomplete" {
								finalResponse = gjson.Get(data, "response").Raw
							}
						}
						require.NotEmpty(t, finalResponse, recorder.Body.String())
						requireClaudeRefusalResponsesBody(t, finalResponse)
					}
				} else {
					assert.Empty(t, reason)
				}
				if tt.name == "earlier refusal superseded" {
					assert.Empty(t, common.GetContextKeyString(c, constant.ContextKeyAdminRejectReason))
				}
			})
		}
	}
}

func TestClaudeRefusalStreamExemptionWaitsForFinalization(t *testing.T) {
	setClaudeRefusalNoChargeForTest(t, true)
	c, _, info := newClaudeRefusalTestContext(types.RelayFormatClaude, true)
	claudeInfo := &ClaudeResponseInfo{Usage: &dto.Usage{}}
	require.Nil(t, HandleStreamResponseData(c, info, claudeInfo, `{"type":"message_start","message":{"id":"msg_refusal","model":"claude-sonnet-4-5","usage":{"input_tokens":100,"output_tokens":0}}}`))
	require.Nil(t, HandleStreamResponseData(c, info, claudeInfo, `{"type":"message_delta","delta":{"stop_reason":"refusal"},"usage":{"output_tokens":0}}`))
	assert.Empty(t, common.GetContextKeyString(c, constant.ContextKeyBillingExemptReason))

	HandleStreamFinalResponse(c, info, claudeInfo)
	assert.Empty(t, common.GetContextKeyString(c, constant.ContextKeyBillingExemptReason), "message_stop is required")
	require.Nil(t, HandleStreamResponseData(c, info, claudeInfo, `{"type":"message_stop"}`))
	assert.Empty(t, common.GetContextKeyString(c, constant.ContextKeyBillingExemptReason))
	HandleStreamFinalResponse(c, info, claudeInfo)
	assert.Equal(t, "claude_refusal_no_output", common.GetContextKeyString(c, constant.ContextKeyBillingExemptReason))
}

func TestClaudeRefusalBillingEvidenceResetsForNextAttempt(t *testing.T) {
	setClaudeRefusalNoChargeForTest(t, true)
	c, _, info := newClaudeRefusalTestContext(types.RelayFormatClaude, false)
	refusal := `{"type":"message","content":[],"stop_reason":"refusal","usage":{"input_tokens":100,"output_tokens":0}}`
	_, apiErr := (&Adaptor{}).DoResponse(c, claudeRefusalTestResponse(refusal, false), info)
	require.Nil(t, apiErr)
	require.Equal(t, "claude_refusal_no_output", common.GetContextKeyString(c, constant.ContextKeyBillingExemptReason))

	regular := `{"type":"message","content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn","usage":{"input_tokens":100,"output_tokens":1}}`
	_, apiErr = (&Adaptor{}).DoResponse(c, claudeRefusalTestResponse(regular, false), info)
	require.Nil(t, apiErr)
	assert.Empty(t, common.GetContextKeyString(c, constant.ContextKeyBillingExemptReason))
	assert.Empty(t, common.GetContextKeyString(c, constant.ContextKeyAdminRejectReason))

	common.SetContextKey(c, constant.ContextKeyAdminRejectReason, "different_component_reason")
	_, apiErr = (&Adaptor{}).DoResponse(c, claudeRefusalTestResponse(regular, false), info)
	require.Nil(t, apiErr)
	assert.Equal(t, "different_component_reason", common.GetContextKeyString(c, constant.ContextKeyAdminRejectReason))
}
