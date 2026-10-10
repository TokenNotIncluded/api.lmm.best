package claudemessages

import (
	"encoding/json"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/relayconvert/convmeta"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClaudeMessagesRequestToOpenAIChatUserMediaBlocks(t *testing.T) {
	textBlock := dto.ClaudeMediaMessage{Type: "text", Text: stringPtr("describe it")}
	tests := []struct {
		name      string
		block     dto.ClaudeMediaMessage
		wantType  string
		wantValue string
		wantParts int
	}{
		{
			name:      "url image keeps url",
			block:     dto.ClaudeMediaMessage{Type: "image", Source: &dto.ClaudeMessageSource{Type: "url", Url: "https://example.com/cat.png"}},
			wantType:  dto.ContentTypeImageURL,
			wantValue: "https://example.com/cat.png",
			wantParts: 2,
		},
		{
			name:      "base64 image becomes data url",
			block:     dto.ClaudeMediaMessage{Type: "image", Source: &dto.ClaudeMessageSource{Type: "base64", MediaType: "image/png", Data: "iVBORw0KGgo="}},
			wantType:  dto.ContentTypeImageURL,
			wantValue: "data:image/png;base64,iVBORw0KGgo=",
			wantParts: 2,
		},
		{
			name:      "image without source is skipped",
			block:     dto.ClaudeMediaMessage{Type: "image"},
			wantType:  dto.ContentTypeText,
			wantValue: "describe it",
			wantParts: 1,
		},
		{
			name:      "base64 document becomes file",
			block:     dto.ClaudeMediaMessage{Type: "document", Source: &dto.ClaudeMessageSource{Type: "base64", MediaType: "application/pdf", Data: "JVBERi0xLjQ="}},
			wantType:  dto.ContentTypeFile,
			wantValue: "data:application/pdf;base64,JVBERi0xLjQ=",
			wantParts: 2,
		},
		{
			name:      "text document becomes text",
			block:     dto.ClaudeMediaMessage{Type: "document", Source: &dto.ClaudeMessageSource{Type: "text", MediaType: "text/plain", Data: "meeting notes"}},
			wantType:  dto.ContentTypeText,
			wantValue: "meeting notes",
			wantParts: 2,
		},
		{
			name:      "url document remains unsupported",
			block:     dto.ClaudeMediaMessage{Type: "document", Source: &dto.ClaudeMessageSource{Type: "url", Url: "https://example.com/a.pdf"}},
			wantType:  dto.ContentTypeText,
			wantValue: "describe it",
			wantParts: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ClaudeMessagesRequestToOpenAIChat(dto.ClaudeRequest{
				Model: "claude-test",
				Messages: []dto.ClaudeMessage{{
					Role:    "user",
					Content: []dto.ClaudeMediaMessage{tt.block, textBlock},
				}},
			}, &convmeta.Values{})
			require.NoError(t, err)
			require.Len(t, got.Messages, 1)

			parts := got.Messages[0].ParseContent()
			require.Len(t, parts, tt.wantParts)
			first := parts[0]
			assert.Equal(t, tt.wantType, first.Type)
			switch tt.wantType {
			case dto.ContentTypeImageURL:
				require.NotNil(t, first.GetImageMedia())
				assert.Equal(t, tt.wantValue, first.GetImageMedia().Url)
			case dto.ContentTypeFile:
				require.NotNil(t, first.GetFile())
				assert.Equal(t, tt.wantValue, first.GetFile().FileData)
			case dto.ContentTypeText:
				assert.Equal(t, tt.wantValue, first.Text)
			}
		})
	}
}

func TestMessageImageURLUsesOpenAIJSONKeys(t *testing.T) {
	encoded, err := json.Marshal(dto.MessageImageUrl{
		Url:      "https://example.com/cat.png",
		Detail:   "high",
		MimeType: "image/png",
	})
	require.NoError(t, err)

	var fields map[string]any
	require.NoError(t, json.Unmarshal(encoded, &fields))
	assert.Equal(t, "https://example.com/cat.png", fields["url"])
	assert.Equal(t, "high", fields["detail"])
	assert.Equal(t, "image/png", fields["mime_type"])
	_, hasLegacyField := fields["MimeType"]
	assert.False(t, hasLegacyField)
}
