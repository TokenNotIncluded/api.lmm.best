// Copyright (c) 2025-2026 QuantumNous. All rights reserved.

// Package agent owns the provider-neutral protocol used by the built-in agent.
// HTTP, authorization, persistence, and product policy deliberately stay in
// their respective outer layers.
package agent

import (
	"encoding/json"
	"errors"
	"strings"
)

type Tool struct {
	Type     string   `json:"type"`
	Function Function `json:"function"`
}

type Function struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type Call struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function CallFunction `json:"function"`
}

type CallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type Message struct {
	Role       string `json:"role"`
	Content    string `json:"content,omitempty"`
	Name       string `json:"name,omitempty"`
	ToolCalls  []Call `json:"tool_calls,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
}

type Request struct {
	Model           string    `json:"model"`
	Messages        []Message `json:"messages"`
	Stream          bool      `json:"stream"`
	Temperature     float64   `json:"temperature"`
	MaxTokens       int       `json:"max_tokens"`
	ReasoningEffort string    `json:"reasoning_effort,omitempty"`
	Tools           []Tool    `json:"tools,omitempty"`
	ToolChoice      any       `json:"tool_choice,omitempty"`
}

type Response struct {
	Choices []Choice `json:"choices"`
}

type Choice struct {
	Message      ResponseMessage `json:"message"`
	FinishReason string          `json:"finish_reason,omitempty"`
}

type ResponseMessage struct {
	Role      string          `json:"role"`
	Content   json.RawMessage `json:"content"`
	ToolCalls []Call          `json:"tool_calls"`
}

func Parse(data []byte) (Response, error) {
	var response Response
	if len(data) == 0 {
		return response, errors.New("empty agent response")
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return response, err
	}
	return response, nil
}

// Text accepts the response content shapes used by OpenAI-compatible
// providers while discarding every non-text field.
func Text(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var texts []string
	if json.Unmarshal(raw, &texts) == nil {
		return strings.Join(texts, "")
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	var builder strings.Builder
	for _, part := range parts {
		if part.Type == "" || part.Type == "text" || part.Type == "output_text" {
			builder.WriteString(part.Text)
		}
	}
	return builder.String()
}

// Bytes returns the retained text size of a conversation. The serialized
// request receives a separate hard limit at the transport boundary.
func Bytes(messages []Message) int {
	total := 0
	for _, message := range messages {
		total += len(message.Role) + len(message.Content) + len(message.Name) + len(message.ToolCallID)
		for _, call := range message.ToolCalls {
			total += len(call.ID) + len(call.Type) + len(call.Function.Name) + len(call.Function.Arguments)
		}
	}
	return total
}
