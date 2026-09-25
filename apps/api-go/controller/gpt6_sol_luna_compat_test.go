package controller

import (
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGPT6SolLunaChatCompatibility(t *testing.T) {
	const sampling = `{"temperature":0.2,"top_p":0.8,"logprobs":true,"top_logprobs":5}`

	for _, tt := range []struct {
		name       string
		model      string
		effort     string
		wantParams string
	}{
		{name: "sol default drops sampling", model: "gpt-6-sol", wantParams: `{}`},
		{name: "sol explicit none keeps sampling", model: "gpt-6-sol", effort: "none", wantParams: sampling},
		{name: "luna default drops sampling", model: "gpt-6-luna", wantParams: `{}`},
		{name: "luna explicit none keeps sampling", model: "gpt-6-luna", effort: "none", wantParams: sampling},
		{name: "luna reasoning drops sampling", model: "gpt-6-luna", effort: "high", wantParams: `{}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			maxTokens := uint(32)
			request := &dto.GeneralOpenAIRequest{
				Model: tt.model,
				Messages: []dto.Message{
					{Role: "system", Content: "instruction"},
					{Role: "user", Content: "hi"},
				},
				MaxTokens:       &maxTokens,
				ReasoningEffort: tt.effort,
			}
			require.NoError(t, common.UnmarshalJsonStr(sampling, request))

			encoded := convertChatCompatibilityRequest(t, request, constant.ChannelTypeOpenAI, nil)

			var want map[string]any
			require.NoError(t, common.UnmarshalJsonStr(tt.wantParams, &want))
			want["model"] = tt.model
			want["messages"] = []dto.Message{
				{Role: "developer", Content: "instruction"},
				{Role: "user", Content: "hi"},
			}
			want["max_completion_tokens"] = 32
			if tt.effort != "" {
				want["reasoning_effort"] = tt.effort
			}

			wantJSON, err := common.Marshal(want)
			require.NoError(t, err)
			assert.JSONEq(t, string(wantJSON), string(encoded))
		})
	}
}
