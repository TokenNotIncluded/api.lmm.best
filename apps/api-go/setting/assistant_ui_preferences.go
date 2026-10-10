// Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later
package setting

// Register built-in display tools during package initialization, before any
// request or lazy tool-set cache can run. Keep all three catalogue indexes in
// sync so discovery, configuration and execution share the same policy.
func init() {
	group := AssistantToolGroup{ID: "ui_preferences", Label: "Display preferences", Tools: []AssistantToolInfo{
		{"get_ui_preference_options", "Display preference choices", "List supported modes, themes, interface languages and balance display units; does not read browser state.", "read_only", "user"},
		{"set_ui_preferences", "Change display preferences", "Change theme, light/dark mode, language and currency display: 切换主题、配色、亮色、暗色、语言、币种。Playful appearance previews are temporary and reversible.", "server_guarded", "user"},
		{"restore_ui_preferences", "Restore display preferences", "Undo the last assistant display change without replacing newer manual choices.", "server_guarded", "user"},
	}}
	assistantToolCatalogue = append(assistantToolCatalogue, group)
	for _, tool := range group.Tools {
		assistantToolGroupsByName[tool.Name] = group.ID
		assistantToolEffects[tool.Name] = tool.Effect
	}
}
