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
		parts = append(parts, normalizeRawJSONForTokenCount(r.Instructions))
	}
	if len(r.Input) > 0 {
		collectResponsesTokenInput(r.Input, &parts, &files)
	}
	if len(r.Tools) > 0 {
		parts = append(parts, normalizeRawJSONForTokenCount(r.Tools))
	}
	return &types.TokenCountMeta{
		CombineText: strings.Join(parts, "\n"),
		Files:       files,
	}
}

// Only traverse protocol content containers. Tool arguments and unknown items
// remain text, even when they happen to contain image-like JSON.
func collectResponsesTokenInput(raw json.RawMessage, texts *[]string, files *[]*types.FileMeta) {
	if !json.Valid(raw) {
		*texts = append(*texts, normalizeRawJSONForTokenCount(raw))
		return
	}
	// Most opaque/tool inputs need no container index at all. This typed probe
	// is performed only once at the root, never recursively over subtrees.
	start := responsesJSONStart(raw, 0)
	if raw[start] != '[' {
		var message struct {
			Type, Role string
			Content    json.RawMessage
		}
		if raw[start] != '{' || json.Unmarshal(raw, &message) != nil || (message.Type != "" && message.Type != "message") || message.Role == "" || len(message.Content) == 0 {
			collectResponsesTokenItem(raw, texts, files)
			return
		}
	}
	containers := indexResponsesJSONContainers(raw)
	root := nextResponsesJSONValue(raw, start, 0, containers)
	pending := []responsesJSONValue{root}
	for len(pending) > 0 {
		value := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if raw[value.start] == '[' {
			begin := len(pending)
			position, next := responsesJSONStart(raw, value.start+1), value.container+1
			for raw[position] != ']' {
				child := nextResponsesJSONValue(raw, position, next, containers)
				pending = append(pending, child)
				position, next = responsesJSONStart(raw, child.end), child.next
			}
			for left, right := begin, len(pending)-1; left < right; left, right = left+1, right-1 {
				pending[left], pending[right] = pending[right], pending[left]
			}
			continue
		}
		if role, content, ok := responsesMessageContent(raw, containers, value); ok {
			*texts = append(*texts, role)
			pending = append(pending, content)
			continue
		}
		itemRaw := raw[value.start:value.end]
		if value == root {
			itemRaw = raw // Root fallback retains surrounding whitespace.
		}
		collectResponsesTokenItem(itemRaw, texts, files)
	}
}

// Terminal values are disjoint spans, so their original typed decoding does
// not reparse nested content and preserves field/type-error handling.
func collectResponsesTokenItem(raw json.RawMessage, texts *[]string, files *[]*types.FileMeta) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		*texts = append(*texts, text)
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
		case "reasoning":
			// encrypted_content is an opaque replay payload. Tokenizing its
			// ciphertext as ordinary text can turn a normal prompt into millions
			// of estimated tokens. Visible summary/content text is countable.
			var reasoning struct {
				Summary []struct {
					Text string `json:"text"`
				} `json:"summary"`
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			}
			if json.Unmarshal(raw, &reasoning) == nil {
				for _, part := range reasoning.Summary {
					*texts = append(*texts, part.Text)
				}
				for _, part := range reasoning.Content {
					*texts = append(*texts, part.Text)
				}
			}
			return
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

		}
	}
	*texts = append(*texts, normalizeRawJSONForTokenCount(raw))
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
