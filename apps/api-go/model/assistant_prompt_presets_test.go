package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupPromptPresetTestDB(t *testing.T) {
	t.Helper()
	_ = setupAssistantLeadTestDB(t)
	require.NoError(t, DB.AutoMigrate(
		&PromptPresetRow{},
		&PromptPresetStat{},
		&PromptConversionRef{},
		&PromptConversationRef{},
	))
}

func TestPromptPresetFallbackAndAggregateAttribution(t *testing.T) {
	setupPromptPresetTestDB(t)

	set, err := GetPromptPresets()
	require.NoError(t, err)
	assert.EqualValues(t, 0, set.Generation)
	assert.Equal(t, fallbackPresetVersion, set.Version)
	require.Len(t, set.Presets, maxPromptPresets)
	assert.Equal(t, "ai_recommendation", set.Presets[0].Id)
	assert.Contains(t, set.Presets[0].Prompt, "推荐信")
	assert.Contains(t, promptPresetIDs(set.Presets), "new_user_gift")
	assert.Contains(t, promptPresetIDs(set.Presets), "weekly_discount")
	var weekly *PromptPreset
	for index := range set.Presets {
		if set.Presets[index].Id == "weekly_discount" {
			weekly = &set.Presets[index]
			break
		}
	}
	require.NotNil(t, weekly)
	assert.Equal(t, "这周充值有什么优惠？", weekly.Prompt)
	assert.NotEmpty(t, set.Presets[0].Prompt)

	attribution, err := ResolvePromptPreset(set.Presets[0].Id, set.Presets[0].Prompt)
	require.NoError(t, err)
	require.NoError(t, CountPresetClick(set.Presets[0].Id))
	require.NoError(t, CountPresetClick(set.Presets[0].Id))
	require.NoError(t, CountPresetConversation(*attribution, 37))
	conversationAttribution, err := ConversationPreset(37)
	require.NoError(t, err)
	assert.Equal(t, attribution, conversationAttribution)
	require.NoError(t, CountPresetRecommendation(*attribution, 71))
	require.NoError(t, CountPresetApproval(71))

	var stat PromptPresetStat
	require.NoError(t, DB.Where("preset_id = ?", set.Presets[0].Id).First(&stat).Error)
	assert.EqualValues(t, 2, stat.ClickCount)
	assert.EqualValues(t, 1, stat.ConversationCount)
	assert.EqualValues(t, 1, stat.RecommendationCount)
	assert.EqualValues(t, 1, stat.ApprovalCount)

	assert.False(t, DB.Migrator().HasColumn(&PromptPresetStat{}, "user_id"))
	assert.False(t, DB.Migrator().HasColumn(&PromptConversionRef{}, "user_id"))
	assert.False(t, DB.Migrator().HasColumn(&PromptConversionRef{}, "message"))
	assert.False(t, DB.Migrator().HasColumn(&PromptConversationRef{}, "user_id"))
	var attributionCount int64
	require.NoError(t, DB.Model(&PromptConversionRef{}).Count(&attributionCount).Error)
	assert.Zero(t, attributionCount)
}

func promptPresetIDs(presets []PromptPreset) map[string]struct{} {
	ids := make(map[string]struct{}, len(presets))
	for _, preset := range presets {
		ids[preset.Id] = struct{}{}
	}
	return ids
}

func TestCountPresetConversationIsIdempotent(t *testing.T) {
	setupPromptPresetTestDB(t)
	attribution := PromptPresetRef{PresetId: "ai_recommendation", Generation: 1, Version: PromptPresetVersion}

	require.NoError(t, CountPresetConversation(attribution, 37))
	require.NoError(t, CountPresetConversation(attribution, 37))

	var stat PromptPresetStat
	require.NoError(t, DB.Where("preset_id = ?", attribution.PresetId).First(&stat).Error)
	assert.EqualValues(t, 1, stat.ConversationCount)
	var refs int64
	require.NoError(t, DB.Model(&PromptConversationRef{}).Where("conversation_id = ?", 37).Count(&refs).Error)
	assert.EqualValues(t, 1, refs)
}

func TestPromptPresetValidationAndBoundedRefresh(t *testing.T) {
	setupPromptPresetTestDB(t)

	_, err := ResolvePromptPreset("getting_started", "client supplied replacement")
	assert.ErrorIs(t, err, ErrPromptPresetNotFound)
	assert.ErrorIs(t, CountPresetClick("unknown"), ErrPromptPresetNotFound)

	for range 3 {
		require.NoError(t, RecordAssistantFirstQuestion("请帮我估算模型 token 的费用和价格；邮箱 alice@example.com；key=sk-secret-token-123"))
	}
	generated, err := RefreshPromptPresets()
	require.NoError(t, err)
	assert.Positive(t, generated.Generation)
	require.NotEmpty(t, generated.Presets)
	assert.LessOrEqual(t, len(generated.Presets), maxPromptPresets)
	assert.Equal(t, "ai_recommendation", generated.Presets[0].Id)
	assert.Contains(t, generated.Presets[0].Prompt, "推荐信")
	require.GreaterOrEqual(t, len(generated.Presets), 2)
	assert.Contains(t, promptPresetIDs(generated.Presets), "getting_started", "dynamic starters must retain an onboarding entry")
	// The four required starters intentionally fill the bounded generated set.
	// Keep validating the existing pricing template directly so its privacy
	// guarantees remain covered even though it is not selected in this set.
	var pricingCandidate promptCandidate
	for _, candidate := range promptCandidates {
		if candidate.Id == "pricing_cost" {
			pricingCandidate = candidate
			break
		}
	}
	pricingPreset, ok := generatePromptPreset(pricingCandidate, map[string]int64{"费用估算": 1}, presetStats{})
	require.True(t, ok)
	pricing := &pricingPreset
	assert.Contains(t, pricing.Prompt, "费用")
	assert.NotContains(t, pricing.Prompt, "alice")
	assert.NotContains(t, pricing.Prompt, "secret")
	var weekly *PromptPreset
	for index := range generated.Presets {
		if generated.Presets[index].Id == "weekly_discount" {
			weekly = &generated.Presets[index]
			break
		}
	}
	require.NotNil(t, weekly)
	assert.Equal(t, "这周充值有什么优惠？", weekly.Prompt)
	assert.NotContains(t, weekly.Prompt, "alice")
	assert.NotContains(t, weekly.Prompt, "secret")
	assert.Contains(t, promptPresetIDs(generated.Presets), "new_user_gift")

	for range presetGenerations + 2 {
		_, err = RefreshPromptPresets()
		require.NoError(t, err)
	}
	var generations []int64
	require.NoError(t, DB.Model(&PromptPresetRow{}).
		Distinct("generation").Pluck("generation", &generations).Error)
	assert.LessOrEqual(t, len(generations), presetGenerations)
	var rows int64
	require.NoError(t, DB.Model(&PromptPresetRow{}).Count(&rows).Error)
	assert.LessOrEqual(t, rows, int64(presetGenerations*maxPromptPresets))
}
