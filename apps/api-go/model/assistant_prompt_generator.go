package model

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type presetStats struct {
	PresetId            string
	ClickCount          int64
	ConversationCount   int64
	RecommendationCount int64
	ApprovalCount       int64
}

func matchPresetTopics(question string) []string {
	normalized := strings.ToLower(strings.TrimSpace(question))
	topics := make([]string, 0, 3)
	for _, rule := range topicRules {
		matched := false
		for _, term := range rule.Terms {
			if strings.Contains(normalized, term) {
				matched = true
				break
			}
		}
		if matched {
			topics = append(topics, rule.Topic)
		}
	}
	return topics
}

func rankPresetTopics(counts map[string]int64, fallback string) []string {
	type topicCount struct {
		topic string
		count int64
	}
	items := make([]topicCount, 0, len(counts))
	for topic, count := range counts {
		if count > 0 {
			items = append(items, topicCount{topic: topic, count: count})
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].count != items[j].count {
			return items[i].count > items[j].count
		}
		return items[i].topic < items[j].topic
	})
	topics := make([]string, 0, 2)
	for index := 0; index < len(items) && index < 2; index++ {
		topics = append(topics, items[index].topic)
	}
	if len(topics) == 0 {
		topics = append(topics, fallback)
	}
	return topics
}

func generatePromptPreset(candidate promptCandidate, topicCounts map[string]int64, stat presetStats) (PromptPreset, bool) {
	if _, ok := requiredPromptPresetCopy[candidate.Id]; ok {
		// Signals rank the required starters, but must not rewrite their reviewed
		// copy or append internal workflow instructions to a user's question.
		return requiredPromptPreset(candidate.Id), true
	}
	focus := strings.Join(rankPresetTopics(topicCounts, candidate.Label), "、")
	prompt := ""
	switch candidate.Intent {
	case AssistantIntentRecommendation:
		prompt = fmt.Sprintf("请围绕%s读取我当前唯一的 L1 推荐信，并根据这次对话直接帮我起草或完善；信息足够后在界面中让我确认。", focus)
	case AssistantIntentOnboarding:
		prompt = fmt.Sprintf("请围绕%s评估我的当前状态，并给出最短、可执行的入门步骤。", focus)
	case AssistantIntentClientSetup:
		prompt = fmt.Sprintf("请围绕%s帮我选择安全的配置方案，并按步骤说明需要核对的信息。", focus)
	case AssistantIntentCost:
		prompt = fmt.Sprintf("请结合%s解释计费方式，并给出一份可复核的费用估算方法。", focus)
	case AssistantIntentPlanPurchase:
		prompt = fmt.Sprintf("请围绕%s说明本周充值优惠码的规则，并告诉我如何通过有效交流争取本周一次折扣；不要提前承诺折扣。", focus)
	case AssistantIntentAPIKey:
		prompt = fmt.Sprintf("请围绕%s说明安全连接 API 的步骤、必要参数和常见错误。", focus)
	case AssistantIntentModels:
		prompt = fmt.Sprintf("请围绕%s，按质量、速度和成本比较当前可用方案。", focus)
	case AssistantIntentBounty:
		prompt = fmt.Sprintf("请围绕%s说明参与流程、权限边界和需要准备的真实材料。", focus)
	case AssistantIntentInvitation:
		prompt = fmt.Sprintf("请围绕%s说明一次性新用户礼包的规则、资格和下一步；不要承诺金额，等服务端完成评估。", focus)
	case AssistantIntentHumanSupport:
		prompt = fmt.Sprintf("请围绕%s帮我整理不含敏感信息的问题摘要和下一步联系途径。", focus)
	default:
		return PromptPreset{}, false
	}
	if stat.RecommendationCount > 0 || stat.ApprovalCount > 0 {
		prompt = strings.TrimSuffix(prompt, "。") + "，并指出完成下一步前的检查点。"
	}
	if strings.TrimSpace(candidate.Id) == "" || strings.TrimSpace(prompt) == "" ||
		utf8.RuneCountInString(prompt) > 240 || utf8.RuneCountInString(candidate.Label) > 120 {
		return PromptPreset{}, false
	}
	return PromptPreset{Id: candidate.Id, Prompt: prompt, Label: candidate.Label}, true
}

func prunePresetData(tx *gorm.DB, now int64) error {
	cutoff := now - presetRetentionDays*24*60*60
	if err := tx.Where("bucket_start < ?", cutoff).Delete(&PromptPresetStat{}).Error; err != nil {
		return err
	}
	if err := tx.Where("updated_at < ?", cutoff).Delete(&PromptConversionRef{}).Error; err != nil {
		return err
	}
	return tx.Where("updated_at < ?", cutoff).Delete(&PromptConversationRef{}).Error
}

// RefreshPromptPresets is safe to call only from a
// background worker or scheduled system task. It performs deterministic
// aggregate scoring and never invokes a model or stores a raw new event.
func RefreshPromptPresets() (PromptPresetSet, error) {
	now := common.GetTimestamp()
	since := now - presetSignalDays*24*60*60
	firstQuestions, err := ListAssistantFirstQuestionSummary(since)
	if err != nil {
		return PromptPresetSet{}, err
	}
	intentSummary, err := ListAssistantIntentSummary(since)
	if err != nil {
		return PromptPresetSet{}, err
	}
	intentCounts := make(map[string]int64)
	intentTopics := make(map[string]map[string]int64)
	for _, row := range firstQuestions {
		intent := ClassifyAssistantIntent(row.Question)
		intentCounts[intent] += row.Count * 2
		if intentTopics[intent] == nil {
			intentTopics[intent] = make(map[string]int64)
		}
		for _, topic := range matchPresetTopics(row.Question) {
			intentTopics[intent][topic] += row.Count
		}
	}
	for _, row := range intentSummary {
		intentCounts[row.Intent] += row.Count
	}
	var statRows []presetStats
	if err := DB.Model(&PromptPresetStat{}).
		Select("preset_id, SUM(click_count) AS click_count, SUM(conversation_count) AS conversation_count, SUM(recommendation_count) AS recommendation_count, SUM(approval_count) AS approval_count").
		Where("bucket_start >= ?", since).Group("preset_id").Scan(&statRows).Error; err != nil {
		return PromptPresetSet{}, err
	}
	stats := make(map[string]presetStats, len(statRows))
	for _, row := range statRows {
		stats[row.PresetId] = row
	}
	type scoredCandidate struct {
		candidate promptCandidate
		score     int64
		preset    PromptPreset
	}
	scored := make([]scoredCandidate, 0, len(promptCandidates))
	for _, candidate := range promptCandidates {
		stat := stats[candidate.Id]
		score := intentCounts[candidate.Intent]*1000 + stat.ClickCount + stat.ConversationCount*5 + stat.RecommendationCount*25 + stat.ApprovalCount*100
		if score <= 0 && !candidate.Required {
			continue
		}
		preset, ok := generatePromptPreset(candidate, intentTopics[candidate.Intent], stat)
		if !ok {
			continue
		}
		scored = append(scored, scoredCandidate{candidate: candidate, score: score, preset: preset})
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].candidate.Required != scored[j].candidate.Required {
			return scored[i].candidate.Required
		}
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		return scored[i].candidate.Order < scored[j].candidate.Order
	})
	selected := make([]scoredCandidate, 0, maxPromptPresets)
	seenIntents := make(map[string]struct{})
	for _, item := range scored {
		if _, exists := seenIntents[item.candidate.Intent]; exists {
			continue
		}
		selected = append(selected, item)
		seenIntents[item.candidate.Intent] = struct{}{}
		if len(selected) == maxPromptPresets {
			break
		}
	}
	if len(selected) == 0 {
		// Cold start stays on the backend seed response; never materialize seeds
		// as if they were generated from aggregate signals.
		if err := DB.Transaction(func(tx *gorm.DB) error {
			return prunePresetData(tx, now)
		}); err != nil {
			return PromptPresetSet{}, err
		}
		return GetPromptPresets()
	}

	var generated PromptPresetSet
	err = DB.Transaction(func(tx *gorm.DB) error {
		var latest int64
		if err := tx.Model(&PromptPresetRow{}).Select("COALESCE(MAX(generation), 0)").Scan(&latest).Error; err != nil {
			return err
		}
		generation := now
		if generation <= latest {
			generation = latest + 1
		}
		rows := make([]PromptPresetRow, 0, maxPromptPresets)
		presets := make([]PromptPreset, 0, maxPromptPresets)
		for position, item := range selected {
			preset := item.preset
			presets = append(presets, preset)
			rows = append(rows, PromptPresetRow{
				PresetId: preset.Id, Prompt: preset.Prompt, Label: preset.Label, Intent: item.candidate.Intent,
				Generation: generation, Version: PromptPresetVersion, Position: position, CreatedAt: now,
			})
		}
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "generation"}, {Name: "preset_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"prompt", "label", "intent", "position", "version"}),
		}).Create(&rows).Error; err != nil {
			return err
		}
		var generations []int64
		if err := tx.Model(&PromptPresetRow{}).Distinct("generation").
			Order("generation DESC").Pluck("generation", &generations).Error; err != nil {
			return err
		}
		if len(generations) > presetGenerations {
			if err := tx.Where("generation NOT IN ?", generations[:presetGenerations]).
				Delete(&PromptPresetRow{}).Error; err != nil {
				return err
			}
		}
		if err := prunePresetData(tx, now); err != nil {
			return err
		}
		generated = PromptPresetSet{Generation: generation, Version: PromptPresetVersion, Presets: presets}
		return nil
	})
	return generated, err
}
