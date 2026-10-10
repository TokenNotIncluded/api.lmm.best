// Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later
package setting

import "testing"

func TestAssistantUIPreferencesPolicy(t *testing.T) {
	_, policy, err := NormalizeAssistantToolPolicy("")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"get_ui_preference_options", "set_ui_preferences", "restore_ui_preferences"} {
		if !AssistantToolKnown(name) {
			t.Fatalf("missing catalogue entry: %s", name)
		}
		count := 0
		for _, group := range AssistantToolCatalogue() {
			for _, tool := range group.Tools {
				if tool.Name == name {
					count++
				}
			}
		}
		if count != 1 {
			t.Fatalf("%s registered %d times", name, count)
		}
		for level := 0; level <= 6; level++ {
			if !policy.AllowedAtLevel(name, level) {
				t.Fatalf("%s unavailable at level %d", name, level)
			}
		}
		if policy.AllowedAtLevel(name, -1) || policy.AllowedAtLevel(name, 7) {
			t.Fatalf("out-of-range access for %s", name)
		}
		_, disabled, err := NormalizeAssistantToolPolicy(`{"version":1,"groups":{"ui_preferences":false},"tools":{}}`)
		if err != nil || disabled.Enabled(name) {
			t.Fatalf("group disable ignored: %s, %v", name, err)
		}
		_, disabled, err = NormalizeAssistantToolPolicy(`{"version":1,"groups":{},"tools":{"` + name + `":false}}`)
		if err != nil || disabled.Enabled(name) {
			t.Fatalf("tool disable ignored: %s, %v", name, err)
		}
	}
	if AssistantToolEffect("set_ui_preferences") == "read_only" || AssistantToolEffect("restore_ui_preferences") == "read_only" {
		t.Fatal("display changes must not use read-only execution or caching")
	}
}
