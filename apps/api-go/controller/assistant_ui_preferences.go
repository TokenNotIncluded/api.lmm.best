// Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later
package controller

import (
	"crypto/rand"
	"encoding/hex"
	"slices"
	"time"

	"github.com/gin-gonic/gin"
)

func assistantUIPreferencesTool(name string) bool {
	return name == "get_ui_preference_options" || name == "set_ui_preferences" || name == "restore_ui_preferences"
}

func assistantUIPreferenceToolDefinitions() []assistantOpenAIToolDefinition {
	properties := map[string]any{}
	for _, key := range []string{"mode", "theme", "language", "currency"} {
		properties[key] = map[string]any{"type": "string", "enum": slices.Clone(assistantUIPreferenceOptions[key])}
	}
	properties["temporary"] = map[string]any{"type": "boolean", "description": "True previews only mode/theme for eight seconds without saving. False or omitted saves the requested preferences."}
	parameters := objectSchema(properties, nil)
	parameters["anyOf"] = []any{
		map[string]any{"required": []string{"mode"}},
		map[string]any{"required": []string{"theme"}},
		map[string]any{"required": []string{"language"}},
		map[string]any{"required": []string{"currency"}},
	}
	return []assistantOpenAIToolDefinition{
		{Type: "function", Function: assistantOpenAIToolFunction{
			Name:        "get_ui_preference_options",
			Description: "List supported interface modes, theme presets, languages and balance display units. These are available choices, not the current browser settings. Currency changes affect display only, never billing, balances or settlement.",
			Parameters:  emptyObjectSchema(),
		}},
		{Type: "function", Function: assistantOpenAIToolFunction{
			Name:        "set_ui_preferences",
			Description: "Change only the current user's requested interface preferences in the active browser. Omitted fields stay unchanged. mode controls light/dark/system; theme selects a preset; language uses the listed interface code; currency is display only, auto follows language. Save only when the user asks for the change. A friendly, invited joke may use temporary=true for a single eight-second theme/mode preview, never language or currency. Do not flash or loop changes. The browser shows success/failure and Undo; preparation is not proof of application. Do not claim a changed balance, payment or completed switch from this result. Use restore_ui_preferences to undo the last assistant change.",
			Parameters:  parameters,
		}},
		{Type: "function", Function: assistantOpenAIToolFunction{
			Name:        "restore_ui_preferences",
			Description: "On the user's request, stop an active appearance preview or undo the last assistant preference change in this browser session. Preserve newer manual choices. This is not a factory reset. There may be nothing to restore after reloading; report the browser result rather than inventing success.",
			Parameters:  emptyObjectSchema(),
		}},
	}
}

// Called only after the workspace's live session, policy and level checks.
// Browser actions are separate from confirmed account/payment work: never
// overwrite an existing action or expose the session binding to the model.
func executeAssistantUIPreferences(c *gin.Context, name string, input map[string]any, actor int, sessionID string) map[string]any {
	if c == nil || actor <= 0 || sessionID == "" || c.GetBool("use_access_token") {
		return assistantWorkspaceFailure("session_required")
	}
	if !assistantUIPreferencesTool(name) {
		return assistantWorkspaceFailure("tool_not_allowed")
	}
	if name == "get_ui_preference_options" {
		if len(input) != 0 {
			return assistantWorkspaceFailure("invalid_input")
		}
		options := map[string][]string{}
		for key, values := range assistantUIPreferenceOptions {
			options[key] = slices.Clone(values)
		}
		return map[string]any{"ok": true, "options": options, "current_preferences_available": false, "currency_scope": "display_only", "preview_seconds": 8}
	}
	if _, exists := c.Get(assistantClientActionKey); exists {
		return assistantWorkspaceFailure("browser_action_already_prepared")
	}
	patch := map[string]string{}
	temporary := false
	if name == "set_ui_preferences" {
		var err error
		patch, temporary, err = parseAssistantUIPreferences(input)
		if err != nil {
			return map[string]any{"ok": false, "status": "invalid_input", "error": err.Error()}
		}
	} else if len(input) != 0 {
		return assistantWorkspaceFailure("invalid_input")
	}
	var randomID [16]byte
	if _, err := rand.Read(randomID[:]); err != nil {
		return assistantWorkspaceFailure("browser_action_unavailable")
	}
	c.Set(assistantClientActionKey, map[string]any{
		"type":                  "workspace_action",
		"tool":                  name,
		"requires_confirmation": false,
		"action_id":             hex.EncodeToString(randomID[:]),
		"actor_user_id":         actor,
		"actor_session_id":      sessionID,
		"expires_at":            time.Now().Add(2 * time.Minute).Unix(),
		"temporary":             temporary,
		"preview":               patch,
	})
	return map[string]any{
		"ok": true, "status": "browser_action_prepared", "applied": false,
		"message": "The display action is ready for the active browser. Its receipt will show application or failure. No balance, exchange rate, settlement or payment was changed. Do not report preparation as completed application.",
	}
}
