// Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later
package controller

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/gin-gonic/gin"
)

func TestAssistantUIPreferenceBrowserEnvelope(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	result := executeAssistantUIPreferences(c, "set_ui_preferences", map[string]any{"mode": "dark", "currency": "CNY"}, 7, "private-session-binding")
	if result["ok"] != true || result["applied"] != false || result["status"] != "browser_action_prepared" {
		t.Fatalf("misleading preparation result: %#v", result)
	}
	raw, _ := json.Marshal(result)
	if strings.Contains(string(raw), "private-session-binding") || strings.Contains(string(raw), "action_id") {
		t.Fatal("browser binding leaked into model output")
	}
	value, ok := c.Get(assistantClientActionKey)
	if !ok {
		t.Fatal("browser action missing")
	}
	action := value.(map[string]any)
	if action["type"] != "workspace_action" || action["requires_confirmation"] != false || action["actor_user_id"] != 7 || action["actor_session_id"] != "private-session-binding" {
		t.Fatalf("incorrect actor or action envelope: %#v", action)
	}
	if len(action["action_id"].(string)) != 32 {
		t.Fatal("action ID must be a random 128-bit hex identifier")
	}
	if remaining := action["expires_at"].(int64) - time.Now().Unix(); remaining < 118 || remaining > 120 {
		t.Fatalf("unexpected lifetime: %d", remaining)
	}
	if preview := action["preview"].(map[string]string); len(preview) != 2 || preview["mode"] != "dark" || preview["currency"] != "CNY" {
		t.Fatalf("incorrect partial change: %#v", preview)
	}
}

func TestAssistantUIPreferenceCannotReplaceAnotherBrowserAction(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	original := map[string]any{"type": "account_action", "sentinel": true}
	c.Set(assistantClientActionKey, original)
	result := executeAssistantUIPreferences(c, "set_ui_preferences", map[string]any{"mode": "dark"}, 7, "session")
	if result["ok"] != false {
		t.Fatal("an existing browser action was not protected")
	}
	value, _ := c.Get(assistantClientActionKey)
	if value.(map[string]any)["sentinel"] != true {
		t.Fatal("an unrelated confirmation was overwritten")
	}
}

func TestAssistantUIPreferenceOptionsAndSessionBounds(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	result := executeAssistantUIPreferences(c, "get_ui_preference_options", nil, 7, "session")
	if result["ok"] != true || result["current_preferences_available"] != false {
		t.Fatal("option catalogue must not claim to read current browser state")
	}
	if _, exists := c.Get(assistantClientActionKey); exists {
		t.Fatal("reading choices prepared a mutation")
	}
	result["options"].(map[string][]string)["mode"][0] = "corrupted"
	if assistantUIPreferenceOptions["mode"][0] != "light" {
		t.Fatal("catalogue callers may not mutate shared options")
	}
	for _, name := range []string{"get_ui_preference_options", "set_ui_preferences", "restore_ui_preferences"} {
		for _, context := range []*gin.Context{nil, c} {
			result := executeAssistantUIPreferences(context, name, nil, 0, "")
			if result["ok"] != false {
				t.Fatal("sessionless action accepted")
			}
		}
	}
	c.Set("use_access_token", true)
	if executeAssistantUIPreferences(c, "set_ui_preferences", map[string]any{"mode": "dark"}, 7, "session")["ok"] != false {
		t.Fatal("non-browser token may not prepare UI changes")
	}
}

func TestAssistantUIPreferenceRegistration(t *testing.T) {
	definitions := assistantUIPreferenceToolDefinitions()
	if len(definitions) != 3 {
		t.Fatalf("expected three compact tools, got %d", len(definitions))
	}
	seen := map[string]bool{}
	for _, definition := range definitions {
		name := definition.Function.Name
		if seen[name] || !assistantWorkspaceTool(name) || !setting.AssistantToolKnown(name) {
			t.Fatalf("incomplete or duplicate registration: %s", name)
		}
		seen[name] = true
		if !assistantToolPermittedForContext(name, assistantUserContext{AccessLevel: "L0"}) {
			t.Fatalf("display preference tool unavailable to L0: %s", name)
		}
	}
}
