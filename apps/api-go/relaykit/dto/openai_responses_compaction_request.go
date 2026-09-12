package dto

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
)

type OpenAIResponsesCompactionRequest struct {
	Model              string          `json:"model"`
	Input              json.RawMessage `json:"input,omitempty"`
	Instructions       json.RawMessage `json:"instructions,omitempty"`
	PreviousResponseID string          `json:"previous_response_id,omitempty"`
	// Codex compact request parity:
	// https://github.com/openai/codex/commit/53d59722268dde82fb93c1f37964ce196c2a86d7
	// https://github.com/openai/codex/commit/5d6f23a27bf9c90709af527a7108c1c2eadf5123
	Tools                json.RawMessage `json:"tools,omitempty"`
	ParallelToolCalls    json.RawMessage `json:"parallel_tool_calls,omitempty"`
	Reasoning            *Reasoning      `json:"reasoning,omitempty"`
	ServiceTier          string          `json:"service_tier,omitempty"`
	PromptCacheKey       json.RawMessage `json:"prompt_cache_key,omitempty"`
	PromptCacheOptions   json.RawMessage `json:"prompt_cache_options,omitempty"`
	PromptCacheRetention json.RawMessage `json:"prompt_cache_retention,omitempty"`
	Text                 json.RawMessage `json:"text,omitempty"`
}

func (r *OpenAIResponsesCompactionRequest) GetTokenCountMeta() *types.TokenCountMeta {
	var parts []string
	var files []*types.FileMeta
	if len(r.Instructions) > 0 {
		parts = append(parts, string(r.Instructions))
	}
	if len(r.Input) > 0 {
		collectResponsesTokenInput(r.Input, &parts, &files)
	}
	if len(r.Tools) > 0 {
		parts = append(parts, string(r.Tools))
	}
	return &types.TokenCountMeta{
		CombineText: strings.Join(parts, "\n"),
		Files:       files,
	}
}

// Only traverse protocol content containers. Tool arguments and unknown items
// remain text, even when they happen to contain image-like JSON.
func collectResponsesTokenInput(raw json.RawMessage, texts *[]string, files *[]*types.FileMeta) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		*texts = append(*texts, text)
		return
	}
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) == nil {
		for _, item := range items {
			collectResponsesTokenInput(item, texts, files)
		}
		return
	}
	var item struct {
		Type     string          `json:"type"`
		Role     string          `json:"role"`
		Content  json.RawMessage `json:"content"`
		Text     string          `json:"text"`
		ImageURL json.RawMessage `json:"image_url"`
		FileURL  json.RawMessage `json:"file_url"`
		FileData string          `json:"file_data"`
		Detail   string          `json:"detail"`
	}
	if json.Unmarshal(raw, &item) == nil {
		switch item.Type {
		case "input_text", "output_text":
			*texts = append(*texts, item.Text)
			return
		case "input_image":
			if imageURL := responsesMediaURL(item.ImageURL); imageURL != "" {
				*files = append(*files, &types.FileMeta{FileType: types.FileTypeImage, Source: types.NewFileSourceFromData(imageURL, ""), Detail: item.Detail})
				return
			}
		case "input_file":
			data := responsesMediaURL(item.FileURL)
			if data == "" {
				data = item.FileData
			}
			if data != "" {
				*files = append(*files, &types.FileMeta{FileType: types.FileTypeFile, Source: types.NewFileSourceFromData(data, "")})
				return
			}
		case "", "message":
			if item.Role != "" && len(item.Content) > 0 {
				*texts = append(*texts, item.Role)
				collectResponsesTokenInput(item.Content, texts, files)
				return
			}
		}
	}
	*texts = append(*texts, string(raw))
}

func responsesMediaURL(raw json.RawMessage) string {
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return value
	}
	var object struct {
		URL string `json:"url"`
	}
	if json.Unmarshal(raw, &object) == nil {
		return object.URL
	}
	return ""
}

func (r *OpenAIResponsesCompactionRequest) IsStream(c *http.Request) bool {
	return false
}

func (r *OpenAIResponsesCompactionRequest) SetModelName(modelName string) {
	if modelName != "" {
		r.Model = modelName
	}
}
