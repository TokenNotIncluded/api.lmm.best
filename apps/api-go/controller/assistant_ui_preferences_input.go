// Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later
package controller

import (
	"errors"
	"slices"
)

var assistantUIPreferenceOptions = map[string][]string{
	"mode":     {"light", "dark", "system"},
	"theme":    {"default", "anthropic", "simple-large", "underground", "rose-garden", "lake-view", "sunset-glow", "forest-whisper", "ocean-breeze", "lavender-dream"},
	"language": {"zhCN", "en", "fr", "ru", "ja", "vi", "zhTW"},
	"currency": {"auto", "USD", "CNY", "CREDIT"},
}

// Only display preferences belong here. Settlement currency, exchange rates,
// account IDs, permissions, CSS and executable content are never accepted.
func parseAssistantUIPreferences(input map[string]any) (map[string]string, bool, error) {
	patch := make(map[string]string)
	temporary := false
	for key, value := range input {
		if key == "temporary" {
			var ok bool
			temporary, ok = value.(bool)
			if !ok {
				return nil, false, errors.New("temporary must be a boolean")
			}
			continue
		}
		choices, known := assistantUIPreferenceOptions[key]
		text, ok := value.(string)
		if !known || !ok || !slices.Contains(choices, text) {
			return nil, false, errors.New("use only the listed display preference values")
		}
		patch[key] = text
	}
	if len(patch) == 0 {
		return nil, false, errors.New("choose at least one display preference")
	}
	if temporary {
		if _, present := patch["language"]; present {
			return nil, false, errors.New("temporary previews cannot change language")
		}
		if _, present := patch["currency"]; present {
			return nil, false, errors.New("temporary previews cannot change currency")
		}
	}
	return patch, temporary, nil
}
