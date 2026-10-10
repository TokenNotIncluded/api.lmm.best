// Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later
package controller

import "testing"

func TestAssistantUIPreferenceValues(t *testing.T) {
	for key, choices := range assistantUIPreferenceOptions {
		for _, value := range choices {
			t.Run(key+"/"+value, func(t *testing.T) {
				patch, temporary, err := parseAssistantUIPreferences(map[string]any{key: value})
				if err != nil || temporary || len(patch) != 1 || patch[key] != value {
					t.Fatalf("unexpected parsed preference: %v, %v, %v", patch, temporary, err)
				}
			})
		}
	}
}

func TestAssistantUIPreferencesRejectUnsafeOrAmbiguousInput(t *testing.T) {
	cases := []map[string]any{
		nil, {}, {"temporary": true}, {"mode": nil}, {"mode": ""},
		{"mode": "dark", "temporary": "true"}, {"mode": []string{"dark"}},
		{"currency": "EUR"}, {"language": "unknown"}, {"theme": "<script>"},
		{"mode": "dark", "user_id": 2}, {"settlement_currency": "USD"},
		{"mode": "dark", "css": "body{display:none}"},
		{"temporary": true, "currency": "USD"},
		{"temporary": true, "language": "en"},
	}
	for _, input := range cases {
		if _, _, err := parseAssistantUIPreferences(input); err == nil {
			t.Errorf("accepted invalid input: %#v", input)
		}
	}
}

func TestAssistantUIPreferencesPartialUpdateAndPreview(t *testing.T) {
	patch, temporary, err := parseAssistantUIPreferences(map[string]any{
		"mode": "dark", "theme": "ocean-breeze", "temporary": true,
	})
	if err != nil || !temporary || len(patch) != 2 {
		t.Fatalf("preview rejected: %v, %v, %v", patch, temporary, err)
	}
	if _, exists := patch["language"]; exists {
		t.Fatal("an omitted language must not be set")
	}
	patch, temporary, err = parseAssistantUIPreferences(map[string]any{
		"language": "zhCN", "currency": "auto", "temporary": false,
	})
	if err != nil || temporary || len(patch) != 2 {
		t.Fatalf("partial update rejected: %v, %v, %v", patch, temporary, err)
	}
}
