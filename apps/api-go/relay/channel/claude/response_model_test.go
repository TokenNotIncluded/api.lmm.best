package claude

import (
	"fmt"
	"testing"

	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/stretchr/testify/require"
)

func TestResponseModelClaudeProviderAndLegacyBillingModelStaySeparate(t *testing.T) {
	setClaudeStreamingTimeoutForTest(t)
	const returned = "claude-haiku-4-5-20251001"
	body := `{"id":"msg_1","type":"message","role":"assistant","model":"` + returned + `","content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn","usage":{"input_tokens":20,"output_tokens":4,"cache_read_input_tokens":7}}`
	sse := claudeRefusalSSE(
		`{"type":"message_start","message":{"id":"msg_1","role":"assistant","model":"`+returned+`","content":[],"usage":{"input_tokens":20,"output_tokens":0,"cache_read_input_tokens":7}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":4}}`,
		`{"type":"message_stop"}`,
	)
	for _, format := range []types.RelayFormat{types.RelayFormatClaude, types.RelayFormatOpenAI, types.RelayFormatOpenAIResponses} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", format, stream), func(t *testing.T) {
				c, writer, info := newClaudeRefusalTestContext(format, stream)
				info.OriginModelName = "client-sonnet"
				info.UpstreamModelName = "claude-sonnet-4-5"
				payload := body
				if stream {
					payload = sse
				}
				var usage interface{}
				usage, apiErr := (&Adaptor{}).DoResponse(c, claudeRefusalTestResponse(payload, stream), info)
				require.Nil(t, apiErr)
				require.NotNil(t, usage)
				require.Equal(t, &relaycommon.ResponseModel{RequestedModel: "client-sonnet", UpstreamModel: "claude-sonnet-4-5", ReturnedModel: returned}, info.ResponseModel)
				require.True(t, info.ResponseModel.Mismatch())
				// Native streaming historically sets the billing/conversion field to
				// the provider model. Diagnostics must not silently remove this.
				if stream && format == types.RelayFormatClaude {
					require.Equal(t, returned, info.UpstreamModelName)
				} else {
					require.Equal(t, "claude-sonnet-4-5", info.UpstreamModelName)
				}
				require.Contains(t, writer.Body.String(), "hello")
				require.NotContains(t, writer.Body.String(), "response_model")
				if !stream && format == types.RelayFormatClaude {
					require.Equal(t, body, writer.Body.String())
				}
				got := usage.(*dto.Usage)
				require.Equal(t, 20, got.PromptTokens)
				require.Equal(t, 4, got.CompletionTokens)
				require.Equal(t, 7, got.PromptTokensDetails.CachedTokens)
				require.Equal(t, 20, got.BillingUsage.ClaudeUsage.InputTokens)
				require.Equal(t, 4, got.BillingUsage.ClaudeUsage.OutputTokens)
			})
		}
	}
}
