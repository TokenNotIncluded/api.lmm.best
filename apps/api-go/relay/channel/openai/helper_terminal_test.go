package openai

import (
	"testing"

	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/stretchr/testify/require"
)

func TestHandleLastResponseTerminalChunkForwarding(t *testing.T) {
	tests := []struct {
		name       string
		data       string
		shouldSend bool
	}{
		{
			name:       "drop pure usage chunk",
			data:       `{"id":"chatcmpl-1","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`,
			shouldSend: false,
		},
		{
			name:       "preserve finish reason without content",
			data:       `{"id":"chatcmpl-1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`,
			shouldSend: true,
		},
		{
			name:       "preserve tool calls without content",
			data:       `{"id":"chatcmpl-1","choices":[{"index":0,"delta":{"tool_calls":[{"id":"call_1","type":"function","function":{"name":"search","arguments":"{}"}}]},"finish_reason":null}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`,
			shouldSend: true,
		},
		{
			name:       "preserve normal content",
			data:       `{"id":"chatcmpl-1","choices":[{"index":0,"delta":{"content":"done"},"finish_reason":null}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`,
			shouldSend: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := &relaycommon.RelayInfo{ShouldIncludeUsage: false}
			var responseID string
			var createdAt int64
			var systemFingerprint string
			var model string
			usage := &dto.Usage{}
			containStreamUsage := false
			shouldSendLastResp := true

			err := handleLastResponse(
				tt.data,
				&responseID,
				&createdAt,
				&systemFingerprint,
				&model,
				&usage,
				&containStreamUsage,
				info,
				&shouldSendLastResp,
			)

			require.NoError(t, err)
			require.True(t, containStreamUsage)
			require.Equal(t, tt.shouldSend, shouldSendLastResp)
		})
	}
}
