package oairesponses

import (
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResponsesRequestToChatCompletionsRequestHoistsToolOutputMedia(t *testing.T) {
	const dataURL = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="

	got, err := ResponsesRequestToChatCompletionsRequest(&dto.OpenAIResponsesRequest{
		Model: "gpt-test",
		Input: mustRawMessage(t, []map[string]any{
			{
				"type":      "function_call",
				"call_id":   "call_1",
				"name":      "view_image",
				"arguments": "{}",
			},
			{
				"type":    "function_call_output",
				"call_id": "call_1",
				"output": []map[string]any{
					{"type": "input_text", "text": "screenshot taken"},
					{"type": "input_image", "image_url": dataURL},
				},
			},
		}),
	})
	require.NoError(t, err)

	require.Len(t, got.Messages, 3)
	assert.Equal(t, "tool", got.Messages[1].Role)
	assert.Equal(t, "call_1", got.Messages[1].ToolCallId)
	assert.Equal(t, "screenshot taken", got.Messages[1].StringContent())
	assert.NotContains(t, got.Messages[1].StringContent(), "base64")

	assert.Equal(t, "user", got.Messages[2].Role)
	parts := got.Messages[2].ParseContent()
	require.Len(t, parts, 1)
	assert.Equal(t, dto.ContentTypeImageURL, parts[0].Type)
	require.NotNil(t, parts[0].GetImageMedia())
	assert.Equal(t, dataURL, parts[0].GetImageMedia().Url)
}

func TestResponsesRequestToChatCompletionsRequestKeepsNonMediaToolOutputs(t *testing.T) {
	tests := []struct {
		name   string
		output any
		want   string
	}{
		{name: "object", output: map[string]any{"ok": true}, want: `{"ok":true}`},
		{name: "arbitrary array", output: []any{1, 2}, want: `[1,2]`},
		{name: "text-only content parts", output: []any{map[string]any{"type": "input_text", "text": "plain"}}, want: `[{"text":"plain","type":"input_text"}]`},
		{name: "unknown content part", output: []any{map[string]any{"type": "custom", "data": "x"}}, want: `[{"data":"x","type":"custom"}]`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResponsesRequestToChatCompletionsRequest(&dto.OpenAIResponsesRequest{
				Model: "gpt-test",
				Input: mustRawMessage(t, []map[string]any{
					{
						"type":    "function_call_output",
						"call_id": "call_1",
						"output":  tt.output,
					},
				}),
			})
			require.NoError(t, err)
			require.Len(t, got.Messages, 1)
			assert.Equal(t, "tool", got.Messages[0].Role)
			assert.Equal(t, "call_1", got.Messages[0].ToolCallId)
			assert.JSONEq(t, tt.want, got.Messages[0].StringContent())
		})
	}
}
