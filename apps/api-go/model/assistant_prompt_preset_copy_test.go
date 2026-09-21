package model

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPromptPresetCopyMatchesSevenWebLocales(t *testing.T) {
	for _, locale := range []string{"en", "zh", "zh-TW", "fr", "ja", "ru", "vi"} {
		t.Run(locale, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("..", "..", "web", "src", "i18n", "locales", locale+".json"))
			require.NoError(t, err)
			var resource struct {
				Translation map[string]string `json:"translation"`
			}
			require.NoError(t, json.Unmarshal(data, &resource))
			for id, variants := range requiredPromptPresetCopy {
				require.Len(t, variants, 7, id)
				require.NotEmpty(t, variants[locale], id)
				assert.Equal(t, variants[locale], resource.Translation[variants["en"]], id)
			}
		})
	}
}

func TestPromptPresetLocalizedValidationAndAttribution(t *testing.T) {
	setupPromptPresetTestDB(t)
	seed, err := GetPromptPresets()
	require.NoError(t, err)
	generated, err := RefreshPromptPresets()
	require.NoError(t, err)
	// Test fallback resolution with an empty DB as well as a materialized set.
	for _, set := range []PromptPresetSet{generated, seed} {
		if set.Generation == 0 {
			require.NoError(t, DB.Where("generation > 0").Delete(&PromptPresetRow{}).Error)
		}
		for _, locale := range []string{"en", "zh", "zh-TW", "fr", "ja", "ru", "vi"} {
			localized := LocalizePromptPresets(set, locale)
			assert.Equal(t, set.Generation, localized.Generation)
			assert.Equal(t, set.Version, localized.Version)
			for index, preset := range localized.Presets {
				assert.Equal(t, set.Presets[index].Id, preset.Id)
				assert.Equal(t, requiredPromptPresetCopy[preset.Id][locale], preset.Prompt)
				assert.Equal(t, preset.Prompt, preset.Label)
				ref, err := ResolvePromptPreset(preset.Id, " \n"+preset.Prompt+"\t ")
				require.NoError(t, err, locale+"/"+preset.Id)
				assert.Equal(t, &PromptPresetRef{PresetId: preset.Id, Generation: set.Generation, Version: set.Version}, ref)
				_, err = ResolvePromptPreset(preset.Id, preset.Prompt+" edited")
				assert.ErrorIs(t, err, ErrPromptPresetNotFound)
				_, err = ResolvePromptPreset(preset.Id, localized.Presets[(index+1)%len(localized.Presets)].Prompt)
				assert.ErrorIs(t, err, ErrPromptPresetNotFound)
				_, err = ResolvePromptPreset("unknown", preset.Prompt)
				assert.ErrorIs(t, err, ErrPromptPresetNotFound)
			}
			// A locale-specific public read must never mutate canonical cached copy.
			for _, preset := range set.Presets {
				assert.Equal(t, requiredPromptPreset(preset.Id), preset)
			}
		}
	}
}

func TestPromptPresetLegacyCacheUsesNewSeed(t *testing.T) {
	setupPromptPresetTestDB(t)
	rows := make([]PromptPresetRow, 0, maxPromptPresets)
	for index, preset := range fallbackPromptPresets().Presets {
		rows = append(rows, PromptPresetRow{
			PresetId: preset.Id, Prompt: "请围绕旧模板说明权限边界。", Label: "旧模板",
			Generation: 17, Version: "aggregate-topic-v1", Position: index,
		})
	}
	require.NoError(t, DB.Create(&rows).Error)
	set, err := GetPromptPresets()
	require.NoError(t, err)
	assert.Equal(t, fallbackPromptPresets(), set, "all IDs exist but their copy version is stale")
	_, err = ResolvePromptPreset(rows[0].PresetId, rows[0].Prompt)
	assert.ErrorIs(t, err, ErrPromptPresetNotFound)

	generated, err := RefreshPromptPresets()
	require.NoError(t, err)
	assert.Greater(t, generated.Generation, rows[0].Generation)
	assert.Equal(t, PromptPresetVersion, generated.Version)
	for _, preset := range generated.Presets {
		assert.Equal(t, requiredPromptPreset(preset.Id), preset)
	}

	// A partially upgraded generation and a missing required starter are stale too.
	require.NoError(t, DB.Model(&PromptPresetRow{}).Where("generation = ? AND preset_id = ?", generated.Generation, generated.Presets[0].Id).Update("version", "aggregate-topic-v1").Error)
	set, err = GetPromptPresets()
	require.NoError(t, err)
	assert.Equal(t, fallbackPromptPresets(), set)
	require.NoError(t, DB.Where("generation = ? AND preset_id = ?", generated.Generation, generated.Presets[0].Id).Delete(&PromptPresetRow{}).Error)
	set, err = GetPromptPresets()
	require.NoError(t, err)
	assert.Equal(t, fallbackPromptPresets(), set)
}

func TestPromptPresetRequiredCopyIgnoresAggregateTopics(t *testing.T) {
	for _, candidate := range promptCandidates {
		if !candidate.Required {
			continue
		}
		preset, ok := generatePromptPreset(candidate, map[string]int64{"raw-secret@example.com": 100}, presetStats{RecommendationCount: 10, ApprovalCount: 5})
		require.True(t, ok)
		assert.Equal(t, candidate.PromptPreset, preset)
		assert.NotContains(t, preset.Prompt, "检查点")
	}
}

func TestPromptPresetLanguageAliasesAndUnknownFallback(t *testing.T) {
	for input, expected := range map[string]string{
		"zhCN": "zh", "zh-CN": "zh", "zh_Hans_CN": "zh",
		"zhTW": "zh-TW", "zh-TW": "zh-TW", "zh-Hant-HK": "zh-TW", "zh-HK": "zh-TW",
		"en-US": "en", "fr-FR": "fr", "ja-JP": "ja", "ru-RU": "ru", "vi-VN": "vi",
		"": "en", "unknown": "en",
	} {
		assert.Equal(t, expected, normalizePromptPresetLanguage(input), input)
	}
}
