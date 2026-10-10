package dto

import (
	"encoding/json"
	"strings"
)

// ModerationTextForRequest selects the current user-authored text, independently
// of token counting and the retired literal guardrail. Never fall back to a
// request's security/token metadata: it can contain system prompts, tools and
// model-generated history that must not be attributed to the current user.
func ModerationTextForRequest(request Request) string {
	switch r := request.(type) {
	case *GeneralOpenAIRequest:
		if r == nil {
			return ""
		}
		for index := len(r.Messages) - 1; index >= 0; index-- {
			if r.Messages[index].Role == "user" {
				return moderationMessageContent(r.Messages[index].Content, false)
			}
		}
		if len(r.Messages) > 0 {
			return ""
		}
		if r.Prompt != nil {
			return moderationPlainInput(r.Prompt)
		}
		return moderationPlainInput(r.Input)
	case *OpenAIResponsesRequest:
		if r != nil {
			return moderationLastUserInput(r.Input, true, false)
		}
	case *ClaudeRequest:
		if r == nil {
			return ""
		}
		for index := len(r.Messages) - 1; index >= 0; index-- {
			if r.Messages[index].Role == "user" {
				return moderationMessageContent(r.Messages[index].Content, false)
			}
		}
	case *GeminiChatRequest:
		if r == nil {
			return ""
		}
		if len(r.Contents) > 0 {
			return moderationGeminiCurrentUser(r.Contents)
		}
		// Explicit batch requests contain separate current user turns. Do not
		// traverse arbitrary metadata or recursively nested batch structures.
		var parts []string
		for _, child := range r.Requests {
			parts = append(parts, moderationGeminiCurrentUser(child.Contents))
		}
		return joinSecurityText(parts)
	case *ImageRequest:
		if r != nil {
			return strings.TrimSpace(r.Prompt)
		}
	case *AudioRequest:
		if r != nil {
			// Speech input is text; instructions and reference audio/text are
			// application configuration, not the user's current utterance.
			return strings.TrimSpace(r.Input)
		}
	case *EmbeddingRequest:
		if r != nil {
			return moderationPlainInput(r.Input)
		}
	case *GeminiEmbeddingRequest:
		if r != nil && (r.Content.Role == "" || r.Content.Role == "user") {
			return moderationGeminiParts(r.Content.Parts)
		}
	case *GeminiBatchEmbeddingRequest:
		if r == nil {
			return ""
		}
		var parts []string
		for _, child := range r.Requests {
			parts = append(parts, ModerationTextForRequest(child))
		}
		return joinSecurityText(parts)
	case *RerankRequest:
		if r != nil {
			// Documents can be retrieved/tool context. Only the search query has
			// a declared current user input role.
			return strings.TrimSpace(r.Query)
		}
	}
	// Compaction snapshots, System One state/questions, standalone search raw
	// bodies and extension request types do not declare a current user turn.
	return ""
}

// ModerationTextFromRealtimeJSON accepts only client events with an explicitly
// declared user message. Session instructions, tool definitions/results, model
// outputs, audio and transcripts are never submitted as user-authored text.
func ModerationTextFromRealtimeJSON(raw []byte) string {
	var event map[string]json.RawMessage
	if json.Unmarshal(raw, &event) != nil {
		return ""
	}
	switch moderationJSONString(event["type"]) {
	case RealtimeEventTypeConversationCreate:
		return moderationUserMessage(event["item"], true)
	case RealtimeEventTypeResponseCreate:
		var response map[string]json.RawMessage
		if json.Unmarshal(event["response"], &response) == nil {
			return moderationLastUserInput(response["input"], false, true)
		}
	}
	return ""
}

func moderationPlainInput(value any) string {
	switch text := value.(type) {
	case string:
		return strings.TrimSpace(text)
	case []string:
		return joinSecurityText(text)
	case []any:
		parts := make([]string, 0, len(text))
		for _, item := range text {
			part, ok := item.(string)
			if !ok {
				return ""
			}
			parts = append(parts, part)
		}
		return joinSecurityText(parts)
	case json.RawMessage:
		var decoded any
		if json.Unmarshal(text, &decoded) == nil {
			return moderationPlainInput(decoded)
		}
	}
	return ""
}

func moderationMessageContent(content any, allowInputText bool) string {
	if text, ok := content.(string); ok {
		return strings.TrimSpace(text)
	}
	raw, err := json.Marshal(content)
	if err != nil {
		return ""
	}
	return moderationTextContent(raw, allowInputText)
}

func moderationTextContent(raw json.RawMessage, allowInputText bool) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return strings.TrimSpace(text)
	}
	var blocks []map[string]json.RawMessage
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, block := range blocks {
		kind := moderationJSONString(block["type"])
		if kind == "text" || (allowInputText && kind == "input_text") {
			parts = append(parts, moderationJSONString(block["text"]))
		}
	}
	return joinSecurityText(parts)
}

func moderationLastUserInput(raw json.RawMessage, allowString bool, requireMessageType bool) string {
	if allowString {
		var text string
		if json.Unmarshal(raw, &text) == nil {
			return strings.TrimSpace(text)
		}
	}
	var items []map[string]json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		return ""
	}
	for index := len(items) - 1; index >= 0; index-- {
		if moderationJSONString(items[index]["role"]) == "user" {
			item, err := json.Marshal(items[index])
			if err != nil {
				return ""
			}
			return moderationUserMessage(item, requireMessageType)
		}
	}
	return ""
}

func moderationUserMessage(raw json.RawMessage, requireMessageType bool) string {
	var item map[string]json.RawMessage
	if json.Unmarshal(raw, &item) != nil || moderationJSONString(item["role"]) != "user" {
		return ""
	}
	kind := moderationJSONString(item["type"])
	if kind != "message" && (requireMessageType || kind != "") {
		return ""
	}
	return moderationTextContent(item["content"], true)
}

func moderationJSONString(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) != nil {
		return ""
	}
	return text
}

func moderationGeminiCurrentUser(contents []GeminiChatContent) string {
	for index := len(contents) - 1; index >= 0; index-- {
		// The Gemini adaptor normalizes an omitted first role to user. Match
		// that accepted request shape without reclassifying later/model items.
		if contents[index].Role == "user" || (index == 0 && contents[index].Role == "") {
			return moderationGeminiParts(contents[index].Parts)
		}
	}
	return ""
}

func moderationGeminiParts(parts []GeminiPart) string {
	var texts []string
	for _, part := range parts {
		if part.Thought || part.FunctionCall != nil || part.FunctionResponse != nil || part.ExecutableCode != nil || part.CodeExecutionResult != nil || part.InlineData != nil || part.FileData != nil {
			continue
		}
		texts = append(texts, part.Text)
	}
	return joinSecurityText(texts)
}
