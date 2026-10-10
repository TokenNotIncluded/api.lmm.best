package claude

import (
	"encoding/json"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/stretchr/testify/require"
)

func TestConvertClaudeRequestPreservesPerMessageOutputConfig(t *testing.T) {
	const body = `{"model":"claude-opus-5-5","max_tokens":4096,"messages":[{"role":"user","content":"Plan a migration"},{"role":"system","content":[],"output_config":{"effort":"high"}},{"role":"user","content":"Summarize it"}]}`

	var request dto.ClaudeRequest
	require.NoError(t, json.Unmarshal([]byte(body), &request))

	copied, err := common.DeepCopy(&request)
	require.NoError(t, err)

	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: request.Model},
	}
	converted, err := (&Adaptor{}).ConvertClaudeRequest(nil, info, copied)
	require.NoError(t, err)

	outbound, err := json.Marshal(converted)
	require.NoError(t, err)

	var parsed struct {
		Messages []struct {
			Role         string         `json:"role"`
			Content      any            `json:"content"`
			OutputConfig map[string]any `json:"output_config,omitempty"`
		} `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(outbound, &parsed))
	require.Len(t, parsed.Messages, 3)
	require.Equal(t, "system", parsed.Messages[1].Role)
	require.Equal(t, []any{}, parsed.Messages[1].Content)
	require.Equal(t, "high", parsed.Messages[1].OutputConfig["effort"])
	require.Nil(t, parsed.Messages[0].OutputConfig)
	require.Nil(t, parsed.Messages[2].OutputConfig)
}
