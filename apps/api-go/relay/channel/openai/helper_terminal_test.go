package openai

import (
	"fmt"
	"testing"

	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/stretchr/testify/require"
)

func TestHandleLastResponseTerminalChunkForwarding(t *testing.T) {
	tests := []struct {
		name    string
		choices string
		forward bool
	}{
		{"pure usage", `[]`, false},
		{"null finish reason", `[{"index":0,"delta":{},"finish_reason":null}]`, false},
		{"empty finish reason", `[{"index":0,"delta":{},"finish_reason":""}]`, false},
		{"finish reason only", `[{"index":0,"delta":{},"finish_reason":"stop"}]`, true},
		{"tool call only", `[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"search","arguments":"{}"}}]}}]`, true},
		{"tool argument fragment", `[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"}"}}]}}]`, true},
		{"content", `[{"index":0,"delta":{"content":"done"}}]`, true},
		{"reasoning", `[{"index":0,"delta":{"reasoning_content":"reasoning"}}]`, true},
		{"later choice terminates", `[{"index":0,"delta":{}},{"index":1,"delta":{},"finish_reason":"length"}]`, true},
	}
	for _, tt := range tests {
		for _, includeUsage := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/include_usage=%t", tt.name, includeUsage), func(t *testing.T) {
				data := fmt.Sprintf(`{"id":"chatcmpl-1","created":123,"model":"test-model","system_fingerprint":"fp_test","choices":%s,"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`, tt.choices)
				info := &relaycommon.RelayInfo{ShouldIncludeUsage: includeUsage}
				var responseID, fingerprint, model string
				var createdAt int64
				usage := &dto.Usage{}
				containUsage, shouldSend := false, true

				err := handleLastResponse(data, &responseID, &createdAt, &fingerprint, &model, &usage, &containUsage, info, &shouldSend)

				require.NoError(t, err)
				require.Equal(t, includeUsage || tt.forward, shouldSend)
				require.True(t, containUsage)
				require.Equal(t, 10, usage.PromptTokens)
				require.Equal(t, 5, usage.CompletionTokens)
				require.Equal(t, 15, usage.TotalTokens)
				require.Equal(t, "chatcmpl-1", responseID)
				require.Equal(t, int64(123), createdAt)
				require.Equal(t, "test-model", model)
				require.Equal(t, "fp_test", fingerprint)
			})
		}
	}
}

func TestHandleLastResponseWithoutUsagePreservesDecision(t *testing.T) {
	for _, shouldSendInitially := range []bool{false, true} {
		t.Run(fmt.Sprintf("forward=%t", shouldSendInitially), func(t *testing.T) {
			var responseID, fingerprint, model string
			var createdAt int64
			previousUsage := &dto.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}
			usage := previousUsage
			containUsage, shouldSend := false, shouldSendInitially
			err := handleLastResponse(`{"choices":[{"delta":{},"finish_reason":"stop"}]}`, &responseID, &createdAt, &fingerprint, &model, &usage, &containUsage, &relaycommon.RelayInfo{}, &shouldSend)
			require.NoError(t, err)
			require.False(t, containUsage)
			require.Same(t, previousUsage, usage)
			require.Equal(t, shouldSendInitially, shouldSend)
		})
	}
}
