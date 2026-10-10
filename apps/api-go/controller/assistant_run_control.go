// Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later
package controller

import (
	"encoding/json"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/gin-gonic/gin"
)

const (
	assistantLoadedToolsKey     = "assistant_loaded_tools"
	assistantConversationEndKey = "assistant_conversation_end"
	assistantDiscoveryBatchSize = 8
)

type assistantConversationEnd struct {
	Message string
	Reason  string
}

func assistantRunControlTools() []assistantOpenAIToolDefinition {
	return []assistantOpenAIToolDefinition{
		{Type: "function", Function: assistantOpenAIToolFunction{
			Name:        "discover_tools",
			Description: "Load argument definitions for 1-8 exact names from the available tool directory. Select only tools needed for the current task. Definitions appear in the next request; this does not execute the selected tools or authorize account actions. Already loaded tools need not be loaded again.",
			Parameters: objectSchema(map[string]any{
				"names": map[string]any{"type": "array", "minItems": 1, "maxItems": assistantDiscoveryBatchSize, "uniqueItems": true, "items": map[string]any{"type": "string", "minLength": 1, "maxLength": 80}},
			}, []string{"names"}),
		}},
		{Type: "function", Function: assistantOpenAIToolFunction{
			Name:        "end_conversation",
			Description: "Deliver a concise final message and immediately stop this run, including remaining tool calls. Use when the task is complete, the user asks to stop, or no useful next step is possible. State incomplete work and unconfirmed actions. This does not block future messages, restrict the account, cancel completed actions, or contact support. A normal final text answer also ends the run without this tool.",
			Parameters: objectSchema(map[string]any{
				"reason":  map[string]any{"type": "string", "enum": []string{"completed", "user_requested", "cannot_proceed"}},
				"message": map[string]any{"type": "string", "minLength": 1, "maxLength": 2000},
			}, []string{"reason", "message"}),
		}},
	}
}

// Definitions are a request cost, not an authorization boundary. Execution
// still checks the current actor, authoritative policy and business rules.
// Disabling discovery restores the full allowed catalogue, so the switch
// cannot silently disable unrelated tools.
func assistantToolsForAgentStep(c *gin.Context, available []assistantOpenAIToolDefinition, required string) ([]assistantOpenAIToolDefinition, string) {
	allowed := make(map[string]bool, len(available))
	for _, tool := range available {
		allowed[tool.Function.Name] = true
	}
	if !allowed["discover_tools"] {
		return available, ""
	}
	loaded, _ := c.Get(assistantLoadedToolsKey)
	selected, _ := loaded.(map[string]bool)
	tools := make([]assistantOpenAIToolDefinition, 0, len(available))
	for _, tool := range available {
		name := tool.Function.Name
		core := name == "discover_tools" || name == "end_conversation" || name == "calculate_math" || name == "get_service_facts" || name == "get_account_access"
		if core || selected[name] || name == required {
			tools = append(tools, tool)
		}
	}
	var directory strings.Builder
	directory.WriteString("\nAvailable tool directory (names only; not permissions). Use discover_tools for missing argument definitions, then call only necessary tools. Never repeat a completed read unless relevant state changed.\n")
	for _, group := range setting.AssistantToolCatalogue() {
		names := make([]string, 0, len(group.Tools))
		for _, tool := range group.Tools {
			if allowed[tool.Name] {
				names = append(names, tool.Name)
			}
		}
		if len(names) > 0 {
			directory.WriteString(group.Label + ": " + strings.Join(names, ", ") + "\n")
		}
	}
	return tools, directory.String()
}

func executeAssistantRunControl(c *gin.Context, name string, input map[string]any) map[string]any {
	invalid := func() map[string]any {
		return map[string]any{"ok": false, "status": "invalid_arguments", "error": "use the declared fields and bounded values for this tool"}
	}
	if c == nil {
		return map[string]any{"ok": false, "status": "context_unavailable"}
	}
	if name == "end_conversation" {
		message, messageOK := input["message"].(string)
		reason, reasonOK := input["reason"].(string)
		message = strings.TrimSpace(message)
		if len(input) != 2 || !messageOK || !reasonOK || message == "" || !utf8.ValidString(message) || utf8.RuneCountInString(message) > 2000 || strings.ContainsRune(message, 0) {
			return invalid()
		}
		if reason != "completed" && reason != "user_requested" && reason != "cannot_proceed" {
			return invalid()
		}
		c.Set(assistantConversationEndKey, assistantConversationEnd{Message: message, Reason: reason})
		return map[string]any{"ok": true, "status": "conversation_ended", "reason": reason, "can_send_message": true}
	}
	if name != "discover_tools" || len(input) != 1 {
		return invalid()
	}
	names, ok := input["names"].([]any)
	if !ok || len(names) < 1 || len(names) > assistantDiscoveryBatchSize {
		return invalid()
	}
	available := make(map[string]bool)
	for _, tool := range assistantToolDefinitionsForContext(assistantUserContextFromGin(c)) {
		available[tool.Function.Name] = true
	}
	requested := make([]string, 0, len(names))
	seen := make(map[string]bool, len(names))
	for _, raw := range names {
		name, ok := raw.(string)
		if !ok || len(name) == 0 || len(name) > 80 || seen[name] {
			return invalid()
		}
		if !available[name] {
			return map[string]any{"ok": false, "status": "tool_not_available", "error": "select only names in the current permitted directory; no definitions were loaded"}
		}
		seen[name] = true
		requested = append(requested, name)
	}
	loaded, _ := c.Get(assistantLoadedToolsKey)
	selected, _ := loaded.(map[string]bool)
	if selected == nil {
		selected = make(map[string]bool)
	}
	count := 0
	for _, name := range requested {
		if !selected[name] {
			count++
			selected[name] = true
		}
	}
	c.Set(assistantLoadedToolsKey, selected)
	// Do not echo schemas here as well as in the next request's tools field.
	return map[string]any{"ok": true, "loaded": requested, "loaded_count": count, "message": "Definitions available in the next tool selection. No selected tool was executed."}
}

func finishAssistantConversationEnd(c *gin.Context, settings setting.AssistantSettings) bool {
	value, exists := c.Get(assistantConversationEndKey)
	end, ok := value.(assistantConversationEnd)
	if !exists || !ok {
		return false
	}
	if assistantHumanSupportInterrupted(c) || assistantAgentRequestStopped(c) {
		return true
	}
	content, _ := json.Marshal(end.Message)
	response := assistantOpenAIResponse{Choices: []assistantOpenAIResponseChoice{{
		FinishReason: "stop",
		Message:      assistantOpenAIResponseMessage{Content: content},
	}}}
	body, _ := json.Marshal(response)
	if session := assistantStreamSessionFrom(c); session != nil {
		if err := session.resetContent(); err != nil {
			return true
		}
	}
	// No cache: a terminal receipt is local to this run, not reusable output.
	finishAssistantAgentAnswer(c, settings, "", true, http.StatusOK, body, response, false)
	return true
}

func assistantModelToolResultJSON(name string, result map[string]any) []byte {
	if ok, _ := result["ok"].(bool); ok && assistantVisualizationKind(name) != "" {
		if visual, valid := result["visualization"].(*assistantVisualization); valid && visual != nil {
			// The complete validated data remains in the browser trace and in
			// the original tool arguments. Do not send the same dataset twice.
			return assistantAgentToolResultJSON(map[string]any{
				"ok": true, "displayed": true, "kind": visual.Kind,
				"message": "Displayed in chat. The display does not verify data or perform account actions.",
			})
		}
	}
	return assistantAgentToolResultJSON(result)
}
