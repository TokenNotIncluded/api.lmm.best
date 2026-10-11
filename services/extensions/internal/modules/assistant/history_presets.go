// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package assistant

import "strings"

type PromptPreset struct {
	ID     string `json:"id"`
	Prompt string `json:"prompt"`
	Label  string `json:"label"`
	Source string `json:"source"`
}
type PromptPresetSet struct {
	Generation int64          `json:"generation"`
	Version    string         `json:"version"`
	Presets    []PromptPreset `json:"presets"`
}

// These are main's reviewed fallback seeds, not generated/custom presets or
// evidence that gift, discounts or access-review business is already available.
func HistoryPresets(language string) PromptPresetSet {
	language = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(language), "_", "-"))
	locale := "en"
	switch {
	case language == "zhtw" || language == "zh-tw" || language == "zh-hk" || language == "zh-mo" || strings.HasPrefix(language, "zh-hant"):
		locale = "zh-TW"
	case language == "zhcn" || language == "zh" || strings.HasPrefix(language, "zh-"):
		locale = "zh"
	default:
		for _, supported := range []string{"fr", "ja", "ru", "vi"} {
			if language == supported || strings.HasPrefix(language, supported+"-") {
				locale = supported
			}
		}
	}
	set := PromptPresetSet{Generation: 0, Version: "backend-seed-v3", Presets: []PromptPreset{}}
	for _, id := range []string{"ai_recommendation", "getting_started", "new_user_gift", "weekly_discount"} {
		copy := requiredPromptPresetCopy[id][locale]
		set.Presets = append(set.Presets, PromptPreset{id, copy, copy, "default"})
	}
	return set
}
