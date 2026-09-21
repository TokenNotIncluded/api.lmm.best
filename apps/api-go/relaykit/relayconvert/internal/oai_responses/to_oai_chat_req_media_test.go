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

func TestResponsesRequestToChatCompletionsRequestHoistsToolOutputMediaForms(t *testing.T) {
	const dataURL = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="

	tests := []struct {
		name        string
		output      []map[string]any
		wantContent string
		wantParts   []string
	}{
		{
			name: "multiple text blocks join with newline",
			output: []map[string]any{
				{"type": "input_text", "text": "first"},
				{"type": "input_image", "image_url": dataURL},
				{"type": "input_text", "text": "second"},
			},
			wantContent: "first\nsecond",
			wantParts:   []string{dto.ContentTypeImageURL},
		},
		{
			name: "media only uses type-aware placeholder",
			output: []map[string]any{
				{"type": "input_image", "image_url": dataURL},
			},
			wantContent: "[image]",
			wantParts:   []string{dto.ContentTypeImageURL},
		},
		{
			name: "mixed media dedupes placeholder labels",
			output: []map[string]any{
				{"type": "input_image", "image_url": dataURL},
				{"type": "input_file", "file_id": "file_1"},
				{"type": "input_image", "image_url": dataURL},
			},
			wantContent: "[image] [file]",
			wantParts:   []string{dto.ContentTypeImageURL, dto.ContentTypeFile, dto.ContentTypeImageURL},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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
						"output":  tt.output,
					},
				}),
			})
			require.NoError(t, err)

			require.Len(t, got.Messages, 3)
			assert.Equal(t, "tool", got.Messages[1].Role)
			assert.Equal(t, tt.wantContent, got.Messages[1].StringContent())
			assert.NotContains(t, got.Messages[1].StringContent(), "base64")

			assert.Equal(t, "user", got.Messages[2].Role)
			parts := got.Messages[2].ParseContent()
			require.Len(t, parts, len(tt.wantParts))
			for i, wantType := range tt.wantParts {
				assert.Equal(t, wantType, parts[i].Type)
			}
		})
	}
}

func TestResponsesRequestToChatCompletionsRequestKeepsNonMediaToolOutputs(t *testing.T) {
	tests := []struct {
		name   string
		output any
		want   string
	}{
		{name: "object", output: map[string]any{"ok": true}, want: `{"ok":true}`},
		{name: "arbitrary array", output: []any{1, 2}, want: `[1,2]`},
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

func TestResponsesRequestToChatCompletionsRequestNormalizesTextOnlyToolOutputParts(t *testing.T) {
	got, err := ResponsesRequestToChatCompletionsRequest(&dto.OpenAIResponsesRequest{
		Model: "gpt-test",
		Input: mustRawMessage(t, []map[string]any{
			{
				"type":    "function_call_output",
				"call_id": "call_1",
				"output": []any{
					map[string]any{"type": "input_text", "text": "first"},
					map[string]any{"type": "output_text", "text": ""},
					map[string]any{"type": "text", "text": "second"},
				},
			},
		}),
	})
	require.NoError(t, err)
	require.Len(t, got.Messages, 1)
	assert.Equal(t, "tool", got.Messages[0].Role)
	assert.Equal(t, "call_1", got.Messages[0].ToolCallId)
	assert.Equal(t, "first\nsecond", got.Messages[0].StringContent())
}

func TestResponsesRequestToChatCompletionsRequestKeepsParallelToolOutputsContiguous(t *testing.T) {
	const dataURL = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="

	got, err := ResponsesRequestToChatCompletionsRequest(&dto.OpenAIResponsesRequest{
		Model: "gpt-test",
		Input: mustRawMessage(t, []map[string]any{
			{"type": "function_call", "call_id": "call_1", "name": "screenshot", "arguments": "{}"},
			{"type": "function_call", "call_id": "call_2", "name": "read_file", "arguments": "{}"},
			{
				"type":    "function_call_output",
				"call_id": "call_1",
				"output": []map[string]any{
					{"type": "input_image", "image_url": dataURL},
				},
			},
			{"type": "function_call_output", "call_id": "call_2", "output": "file contents"},
			{"role": "user", "content": "what do you see?"},
		}),
	})
	require.NoError(t, err)

	require.Len(t, got.Messages, 5)
	assert.Equal(t, "assistant", got.Messages[0].Role)
	assert.Len(t, got.Messages[0].ParseToolCalls(), 2)
	assert.Equal(t, "tool", got.Messages[1].Role)
	assert.Equal(t, "call_1", got.Messages[1].ToolCallId)
	assert.Equal(t, "[image]", got.Messages[1].StringContent())
	assert.Equal(t, "tool", got.Messages[2].Role)
	assert.Equal(t, "call_2", got.Messages[2].ToolCallId)
	assert.Equal(t, "file contents", got.Messages[2].StringContent())

	assert.Equal(t, "user", got.Messages[3].Role)
	media := got.Messages[3].ParseContent()
	require.Len(t, media, 1)
	assert.Equal(t, dto.ContentTypeImageURL, media[0].Type)
	assert.Equal(t, "user", got.Messages[4].Role)
	assert.Equal(t, "what do you see?", got.Messages[4].StringContent())
}
