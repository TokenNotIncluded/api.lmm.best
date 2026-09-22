package claudemessages

import (
	"encoding/json"
	"strconv"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/relayconvert/convmeta"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClaudeMessagesRequestToOpenAIChatMapsEffortInCompatibilityMode(t *testing.T) {
	budget := 512
	claudeRequest := dto.ClaudeRequest{
		Model:        "gpt-4o-mini",
		OutputConfig: json.RawMessage(`{"effort":"high"}`),
		Thinking: &dto.Thinking{
			Type:         "enabled",
			BudgetTokens: &budget,
		},
	}

	info := &convmeta.Values{
		Options: &convmeta.Options{
			EnableMessagesToGPTCompatibility: true,
		},
	}

	openAIRequest, err := ClaudeMessagesRequestToOpenAIChat(claudeRequest, info)
	require.NoError(t, err)
	assert.Equal(t, "high", openAIRequest.ReasoningEffort)
	assert.True(t, len(openAIRequest.Reasoning) == 0)
}

func TestClaudeMessagesRequestToOpenAIChatMapsThinkingToEffortInCompatibilityMode(t *testing.T) {
	// Boundaries from the shared ladder. `max` is not declared by this
	// compatibility path, so the two largest budgets clamp to `xhigh` rather
	// than sending a level the target rejects.
	tests := []struct {
		budget int
		want   string
	}{
		{budget: 0, want: "none"},
		{budget: 1024, want: "low"},
		{budget: 4096, want: "medium"},
		{budget: 8192, want: "medium"},
		{budget: 8193, want: "high"},
		{budget: 15999, want: "high"},
		{budget: 16000, want: "xhigh"},
		{budget: 31999, want: "xhigh"},
		{budget: 32000, want: "xhigh"},
		{budget: 64000, want: "xhigh"},
	}

	for _, tt := range tests {
		t.Run(strconv.Itoa(tt.budget), func(t *testing.T) {
			claudeRequest := dto.ClaudeRequest{
				Model: "gpt-4o-mini",
				Thinking: &dto.Thinking{
					Type:         "enabled",
					BudgetTokens: intPtr(tt.budget),
				},
			}

			info := &convmeta.Values{
				Options: &convmeta.Options{
					EnableMessagesToGPTCompatibility: true,
				},
			}

			openAIRequest, err := ClaudeMessagesRequestToOpenAIChat(claudeRequest, info)
			require.NoError(t, err)
			assert.Equal(t, tt.want, openAIRequest.ReasoningEffort)
		})
	}
}

func TestClaudeMessagesRequestToOpenAIChatPreservesExplicitXHighEffort(t *testing.T) {
	claudeRequest := dto.ClaudeRequest{
		Model:        "gpt-4o-mini",
		OutputConfig: json.RawMessage(`{"effort":"xhigh"}`),
	}

	info := &convmeta.Values{
		Options: &convmeta.Options{
			EnableMessagesToGPTCompatibility: true,
		},
	}

	openAIRequest, err := ClaudeMessagesRequestToOpenAIChat(claudeRequest, info)
	require.NoError(t, err)
	assert.Equal(t, "xhigh", openAIRequest.ReasoningEffort)
}

func TestClaudeMessagesRequestToOpenAIChatClampsUndeclaredMaxEffort(t *testing.T) {
	claudeRequest := dto.ClaudeRequest{
		Model:        "gpt-4o-mini",
		OutputConfig: json.RawMessage(`{"effort":"max"}`),
	}

	info := &convmeta.Values{
		Options: &convmeta.Options{
			EnableMessagesToGPTCompatibility: true,
		},
	}

	openAIRequest, err := ClaudeMessagesRequestToOpenAIChat(claudeRequest, info)
	require.NoError(t, err)
	assert.Equal(t, "xhigh", openAIRequest.ReasoningEffort)
}

func TestClaudeMessagesRequestToOpenAIChatDoesNotMapEffortForNonGPTCompatibilityModels(t *testing.T) {
	claudeRequest := dto.ClaudeRequest{
		Model:        "claude-3-7-sonnet-20240229",
		OutputConfig: json.RawMessage(`{"effort":"high"}`),
	}

	info := &convmeta.Values{
		Options: &convmeta.Options{
			EnableMessagesToGPTCompatibility: false,
		},
	}

	openAIRequest, err := ClaudeMessagesRequestToOpenAIChat(claudeRequest, info)
	require.NoError(t, err)
	assert.Empty(t, openAIRequest.ReasoningEffort)
}

func TestClaudeMessagesRequestToOpenAIChatKeepsTextAndToolUseTogether(t *testing.T) {
	assistantContent := []dto.ClaudeMediaMessage{
		{
			Type: "text",
			Text: stringPtr("look up"),
		},
		{
			Type:         "tool_use",
			Id:           "tool_1",
			Name:         "lookup",
			Input:        map[string]interface{}{"q": "alpha"},
			CacheControl: nil,
		},
	}
	claudeRequest := dto.ClaudeRequest{
		Model: "gpt-4o-mini",
		Messages: []dto.ClaudeMessage{
			{
				Role:    "assistant",
				Content: assistantContent,
			},
		},
	}

	info := &convmeta.Values{
		Options: &convmeta.Options{
			EnableMessagesToGPTCompatibility: true,
		},
	}

	openAIRequest, err := ClaudeMessagesRequestToOpenAIChat(claudeRequest, info)
	require.NoError(t, err)
	require.Len(t, openAIRequest.Messages, 1)

	assistantMessage := openAIRequest.Messages[0]
	assert.Equal(t, "assistant", assistantMessage.Role)

	mediaContent := assistantMessage.ParseContent()
	require.Len(t, mediaContent, 1)
	assert.Equal(t, "text", mediaContent[0].Type)
	assert.Equal(t, "look up", mediaContent[0].Text)

	toolCalls := assistantMessage.ParseToolCalls()
	require.Len(t, toolCalls, 1)
	assert.Equal(t, "function", toolCalls[0].Type)
	assert.Equal(t, "lookup", toolCalls[0].Function.Name)
}

func TestClaudeMessagesRequestToOpenAIChatDropsThinkingHistoryBlocks(t *testing.T) {
	claudeRequest := dto.ClaudeRequest{
		Model:  "gpt-4o-mini",
		System: "sys prompt",
		Messages: []dto.ClaudeMessage{
			{
				Role: "assistant",
				Content: []dto.ClaudeMediaMessage{
					{
						Type:         "thinking",
						Text:         stringPtr("hidden reasoning"),
						CacheControl: json.RawMessage(`null`),
					},
					{
						Type:         "redacted_thinking",
						Text:         stringPtr("redacted"),
						CacheControl: json.RawMessage(`null`),
					},
				},
			},
		},
	}

	info := &convmeta.Values{
		Options: &convmeta.Options{
			EnableMessagesToGPTCompatibility: true,
		},
	}

	openAIRequest, err := ClaudeMessagesRequestToOpenAIChat(claudeRequest, info)
	require.NoError(t, err)
	require.Len(t, openAIRequest.Messages, 1)
	assert.Equal(t, "system", openAIRequest.Messages[0].Role)
}

func TestClaudeMessagesRequestToOpenAIChatToolResultContent(t *testing.T) {
	const imageData = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJ"
	const dataURL = "data:image/png;base64," + imageData
	imageBlock := dto.ClaudeMediaMessage{
		Type:   "image",
		Source: &dto.ClaudeMessageSource{Type: "base64", MediaType: "image/png", Data: imageData},
	}
	textBlock := func(text string) dto.ClaudeMediaMessage {
		return dto.ClaudeMediaMessage{Type: "text", Text: stringPtr(text)}
	}

	tests := []struct {
		name        string
		content     any
		wantContent string
		wantImages  []string
		wantJSON    bool
	}{
		{name: "string passes through", content: "done", wantContent: "done"},
		{
			name:        "text and image keep text on tool message",
			content:     []dto.ClaudeMediaMessage{textBlock("screenshot taken"), imageBlock},
			wantContent: "screenshot taken",
			wantImages:  []string{dataURL},
		},
		{
			name:        "image only uses placeholder",
			content:     []dto.ClaudeMediaMessage{imageBlock},
			wantContent: "[image]",
			wantImages:  []string{dataURL},
		},
		{
			name: "url image source is forwarded as is",
			content: []dto.ClaudeMediaMessage{{
				Type:   "image",
				Source: &dto.ClaudeMessageSource{Type: "url", Url: "https://example.com/shot.png"},
			}},
			wantContent: "[image]",
			wantImages:  []string{"https://example.com/shot.png"},
		},
		{
			name:        "text blocks join with newline",
			content:     []dto.ClaudeMediaMessage{textBlock("first"), textBlock(""), textBlock("second")},
			wantContent: "first\nsecond",
		},
		{
			name:        "unknown block keeps whole array",
			content:     []dto.ClaudeMediaMessage{textBlock("see doc"), {Type: "document", Content: "opaque"}},
			wantContent: `[{"type":"text","text":"see doc"},{"type":"document","content":"opaque"}]`,
			wantJSON:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ClaudeMessagesRequestToOpenAIChat(dto.ClaudeRequest{
				Model: "claude-test",
				Messages: []dto.ClaudeMessage{
					{Role: "user", Content: "take a screenshot"},
					{Role: "assistant", Content: []dto.ClaudeMediaMessage{
						{Type: "tool_use", Id: "toolu_1", Name: "screenshot", Input: map[string]any{}},
					}},
					{Role: "user", Content: []dto.ClaudeMediaMessage{
						{Type: "tool_result", ToolUseId: "toolu_1", Content: tt.content},
						textBlock("what do you see?"),
					}},
				},
			}, &convmeta.Values{})
			require.NoError(t, err)

			require.Len(t, got.Messages, 4)
			tool := got.Messages[2]
			assert.Equal(t, "tool", tool.Role)
			assert.Equal(t, "toolu_1", tool.ToolCallId)
			if tt.wantJSON {
				assert.JSONEq(t, tt.wantContent, tool.StringContent())
			} else {
				assert.Equal(t, tt.wantContent, tool.StringContent())
			}

			user := got.Messages[3]
			assert.Equal(t, "user", user.Role)
			parts := user.ParseContent()
			require.Len(t, parts, len(tt.wantImages)+1)
			for i, wantURL := range tt.wantImages {
				assert.Equal(t, dto.ContentTypeImageURL, parts[i].Type)
				require.NotNil(t, parts[i].GetImageMedia())
				assert.Equal(t, wantURL, parts[i].GetImageMedia().Url)
			}
			assert.Equal(t, dto.ContentTypeText, parts[len(parts)-1].Type)
			assert.Equal(t, "what do you see?", parts[len(parts)-1].Text)
		})
	}
}

func TestClaudeMessagesRequestToOpenAIChatKeepsParallelToolResultsContiguous(t *testing.T) {
	const dataURL = "data:image/png;base64,iVBORw0KGgo="
	got, err := ClaudeMessagesRequestToOpenAIChat(dto.ClaudeRequest{
		Model: "claude-test",
		Messages: []dto.ClaudeMessage{
			{Role: "user", Content: "screenshot and read the note"},
			{Role: "assistant", Content: []dto.ClaudeMediaMessage{
				{Type: "text", Text: stringPtr("Let me look.")},
				{Type: "tool_use", Id: "toolu_shot", Name: "screenshot", Input: map[string]any{}},
				{Type: "tool_use", Id: "toolu_read", Name: "read_file", Input: map[string]any{"path": "note.txt"}},
			}},
			{Role: "user", Content: []dto.ClaudeMediaMessage{
				{Type: "tool_result", ToolUseId: "toolu_shot", Content: []dto.ClaudeMediaMessage{{
					Type:   "image",
					Source: &dto.ClaudeMessageSource{Type: "base64", MediaType: "image/png", Data: "iVBORw0KGgo="},
				}}},
				{Type: "tool_result", ToolUseId: "toolu_read", Content: "note contents"},
			}},
		},
	}, &convmeta.Values{})
	require.NoError(t, err)

	roles := make([]string, 0, len(got.Messages))
	for _, message := range got.Messages {
		roles = append(roles, message.Role)
	}
	assert.Equal(t, []string{"user", "assistant", "tool", "tool", "user"}, roles)
	assert.Len(t, got.Messages[1].ParseToolCalls(), 2)
	assert.Equal(t, "[image]", got.Messages[2].StringContent())
	assert.Equal(t, "toolu_shot", got.Messages[2].ToolCallId)
	assert.Equal(t, "note contents", got.Messages[3].StringContent())
	assert.NotContains(t, got.Messages[2].StringContent()+got.Messages[3].StringContent(), "base64")

	parts := got.Messages[4].ParseContent()
	require.Len(t, parts, 1)
	assert.Equal(t, dto.ContentTypeImageURL, parts[0].Type)
	require.NotNil(t, parts[0].GetImageMedia())
	assert.Equal(t, dataURL, parts[0].GetImageMedia().Url)
}

func intPtr(v int) *int {
	return &v
}

func stringPtr(v string) *string {
	return &v
}
