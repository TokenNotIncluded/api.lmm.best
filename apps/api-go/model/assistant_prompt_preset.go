package model

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
)

const (
	PromptPresetVersion   = "aggregate-topic-v2"
	fallbackPresetVersion = "backend-seed-v2"
	maxPromptPresets      = 4
	presetGenerations     = 12
	presetRetentionDays   = 90
	presetSignalDays      = 30
)

var ErrPromptPresetNotFound = errors.New("assistant pre-conversation preset not found")

// PromptPresetRow is a bounded, server-generated snapshot.
// It contains only reviewed backend copy and aggregate-derived ordering.
type PromptPresetRow struct {
	Id         int64  `json:"-" gorm:"primaryKey"`
	PresetId   string `json:"id" gorm:"type:varchar(64);not null;uniqueIndex:idx_assistant_pre_conversation_generation_preset,priority:2"`
	Prompt     string `json:"prompt" gorm:"type:varchar(1000);not null"`
	Label      string `json:"label,omitempty" gorm:"type:varchar(120);not null;default:''"`
	Intent     string `json:"-" gorm:"type:varchar(40);not null"`
	Generation int64  `json:"-" gorm:"not null;uniqueIndex:idx_assistant_pre_conversation_generation_preset,priority:1;index"`
	Version    string `json:"-" gorm:"type:varchar(40);not null"`
	Position   int    `json:"-" gorm:"not null"`
	CreatedAt  int64  `json:"-" gorm:"not null"`
}

func (PromptPresetRow) TableName() string {
	return "assistant_pre_conversation_preset_cache"
}

// PromptPresetStat stores aggregate-only counters. There is
// intentionally no user, conversation, session, IP, or raw-message column.
type PromptPresetStat struct {
	Id                  int64  `json:"-" gorm:"primaryKey"`
	PresetId            string `json:"preset_id" gorm:"type:varchar(64);not null;uniqueIndex:idx_assistant_pre_conversation_stat,priority:1"`
	BucketStart         int64  `json:"bucket_start" gorm:"not null;uniqueIndex:idx_assistant_pre_conversation_stat,priority:2;index"`
	Generation          int64  `json:"generation" gorm:"not null;uniqueIndex:idx_assistant_pre_conversation_stat,priority:3"`
	Version             string `json:"version" gorm:"type:varchar(40);not null;uniqueIndex:idx_assistant_pre_conversation_stat,priority:4"`
	ClickCount          int64  `json:"click_count" gorm:"not null;default:0"`
	ConversationCount   int64  `json:"conversation_count" gorm:"not null;default:0"`
	RecommendationCount int64  `json:"recommendation_count" gorm:"not null;default:0"`
	ApprovalCount       int64  `json:"approval_count" gorm:"not null;default:0"`
	UpdatedAt           int64  `json:"updated_at" gorm:"not null"`
}

func (PromptPresetStat) TableName() string {
	return "assistant_pre_conversation_preset_stats"
}

// PromptConversionRef links an existing access
// review request to an aggregate cohort. It deliberately stores neither a
// user ID nor recommendation/request text, and is removed after review or the
// bounded retention window.
type PromptConversionRef struct {
	RequestId  int    `json:"-" gorm:"primaryKey"`
	PresetId   string `json:"-" gorm:"type:varchar(64);not null;index"`
	Generation int64  `json:"-" gorm:"not null"`
	Version    string `json:"-" gorm:"type:varchar(40);not null"`
	UpdatedAt  int64  `json:"-" gorm:"not null;index"`
}

func (PromptConversionRef) TableName() string {
	return "assistant_pre_conversation_conversion_attributions"
}

// PromptConversationRef keeps the aggregate cohort
// available on later turns without copying a user ID or any conversation text.
type PromptConversationRef struct {
	ConversationId int64  `json:"-" gorm:"primaryKey"`
	PresetId       string `json:"-" gorm:"type:varchar(64);not null;index"`
	Generation     int64  `json:"-" gorm:"not null"`
	Version        string `json:"-" gorm:"type:varchar(40);not null"`
	UpdatedAt      int64  `json:"-" gorm:"not null;index"`
}

func (PromptConversationRef) TableName() string {
	return "assistant_pre_conversation_conversation_attributions"
}

type PromptPreset struct {
	Id     string `json:"id"`
	Prompt string `json:"prompt"`
	Label  string `json:"label,omitempty"`
	Source string `json:"source,omitempty"`
	// Translations holds administrator-provided per-locale copy for a custom
	// preset. It is never serialized: the endpoint returns the already-selected
	// locale, so the client cannot drift from the server's choice.
	Translations map[string]PromptPresetCopy `json:"-"`
}

// PromptPresetCopy is one localized label/prompt pair for a custom preset.
type PromptPresetCopy struct {
	Label  string `json:"label"`
	Prompt string `json:"prompt"`
}

// localizedPromptPresetLabel is the locale used when a custom preset omits the
// requested language. Administrators edit a default that every locale falls
// back to, so an unconfigured language never renders an empty starter.
const localizedPromptPresetLabel = "default"

type promptPresetCopyValue struct {
	value string
}

// UnmarshalJSON accepts either the legacy plain string or a per-locale object,
// so presets saved before multilingual support keep working unchanged.
func (value *promptPresetCopyValue) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		value.value = text
		return nil
	}
	var byLocale map[string]string
	if err := json.Unmarshal(data, &byLocale); err != nil {
		return errors.New("preset copy must be a string or a locale map of strings")
	}
	encoded, err := json.Marshal(byLocale)
	if err != nil {
		return err
	}
	value.value = string(encoded)
	return nil
}

// customPromptPresetEntry is the administrator-authored shape. Both fields may
// be a single string (legacy) or a map of locale to string.
type customPromptPresetEntry struct {
	ID     string                `json:"id"`
	Label  promptPresetCopyValue `json:"label"`
	Prompt promptPresetCopyValue `json:"prompt"`
}

func (entry customPromptPresetEntry) copies() (map[string]PromptPresetCopy, string, string, bool) {
	labels, labelDefault, labelLocalized := splitPromptPresetCopy(entry.Label.value)
	prompts, promptDefault, promptLocalized := splitPromptPresetCopy(entry.Prompt.value)
	locales := map[string]struct{}{}
	for locale := range labels {
		locales[locale] = struct{}{}
	}
	for locale := range prompts {
		locales[locale] = struct{}{}
	}
	translations := make(map[string]PromptPresetCopy, len(locales))
	for locale := range locales {
		pair := PromptPresetCopy{Label: labels[locale], Prompt: prompts[locale]}
		if pair.Label == "" {
			pair.Label = labelDefault
		}
		if pair.Prompt == "" {
			pair.Prompt = promptDefault
		}
		if pair.Label == "" || pair.Prompt == "" {
			return nil, "", "", false
		}
		translations[normalizePromptPresetLanguage(locale)] = pair
	}
	defaultCopy, ok := translations[localizedPromptPresetLabel]
	if !ok {
		if !labelLocalized && !promptLocalized {
			defaultCopy = PromptPresetCopy{Label: labelDefault, Prompt: promptDefault}
		} else {
			return nil, "", "", false
		}
	}
	return translations, defaultCopy.Label, defaultCopy.Prompt, true
}

// splitPromptPresetCopy distinguishes a legacy single string from a locale map.
func splitPromptPresetCopy(raw string) (map[string]string, string, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, "", false
	}
	if !strings.HasPrefix(trimmed, "{") {
		return nil, trimmed, false
	}
	var byLocale map[string]string
	if err := json.Unmarshal([]byte(trimmed), &byLocale); err != nil {
		return nil, trimmed, false
	}
	normalized := make(map[string]string, len(byLocale))
	for locale, text := range byLocale {
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		normalized[normalizePromptPresetLanguage(locale)] = text
	}
	return normalized, "", true
}

type PromptPresetSet struct {
	Generation int64          `json:"generation"`
	Version    string         `json:"version"`
	Presets    []PromptPreset `json:"presets"`
}

type PromptPresetRef struct {
	PresetId   string
	Generation int64
	Version    string
}

type promptCandidate struct {
	PromptPreset
	Intent   string
	Order    int
	Required bool
}

type topicRule struct {
	Topic string
	Terms []string
}

// Only fixed, reviewed topic names can cross from aggregate questions into a
// generated preset. Source substrings are never copied to cache rows.
var topicRules = []topicRule{
	{Topic: "推荐信", Terms: []string{"推荐信", "推荐正文", "recommendation", "reference letter"}},
	{Topic: "开发者访问", Terms: []string{"l0", "l1", "开发者权限", "开发者访问", "developer access", "api access"}},
	{Topic: "API Key", Terms: []string{"api key", "apikey", "密钥"}},
	{Topic: "Base URL", Terms: []string{"base url", "接口地址"}},
	{Topic: "Claude Code", Terms: []string{"claude code"}},
	{Topic: "客户端配置", Terms: []string{"客户端", "client", "配置", "setup"}},
	{Topic: "模型选择", Terms: []string{"模型", "model", "质量", "速度"}},
	{Topic: "Token 用量", Terms: []string{"token", "用量", "usage"}},
	{Topic: "模型价格", Terms: []string{"价格", "单价", "price", "pricing"}},
	{Topic: "费用估算", Terms: []string{"费用", "成本", "计费", "cost", "billing", "estimate"}},
	{Topic: "套餐折扣", Terms: []string{"套餐", "折扣", "优惠", "discount", "plan"}},
	{Topic: "本周充值优惠", Terms: []string{"本周优惠", "本周折扣", "充值优惠", "优惠码", "weekly discount", "weekly coupon"}},
	{Topic: "新用户礼包", Terms: []string{"新用户礼包", "新用户福利", "新手礼包", "新手奖励", "新用户奖励", "新人礼包", "新人福利", "新手福利", "welcome gift", "welcome bonus", "new-user gift", "new user gift", "new user bonus"}},
	{Topic: "开源悬赏", Terms: []string{"开源", "悬赏", "bounty", "challenge"}},
	{Topic: "提交证据", Terms: []string{"pull request", "提交", "证据", "evidence"}},
	{Topic: "人工支持", Terms: []string{"人工", "客服", "管理员", "support", "administrator"}},
}

var promptCandidates = []promptCandidate{
	{PromptPreset: requiredPromptPreset("ai_recommendation"), Intent: AssistantIntentRecommendation, Order: 0, Required: true},
	// Keep one orientation entry in every generated starter set. Aggregate
	// ranking may reorder the remaining slots, but removing the only
	// “what can I do here?” entry leaves new users without a safe first step.
	{PromptPreset: requiredPromptPreset("getting_started"), Intent: AssistantIntentOnboarding, Order: 1, Required: true},
	{PromptPreset: requiredPromptPreset("new_user_gift"), Intent: AssistantIntentInvitation, Order: 2, Required: true},
	{PromptPreset: requiredPromptPreset("weekly_discount"), Intent: AssistantIntentPlanPurchase, Order: 3, Required: true},
	{PromptPreset: PromptPreset{Id: "developer_access", Label: "开发者访问", Prompt: "我想使用 API，请说明当前账户可以做什么，以及如何申请开发者访问。"}, Intent: AssistantIntentOnboarding, Order: 3},
	{PromptPreset: PromptPreset{Id: "client_setup", Label: "客户端配置", Prompt: "请帮我选择并配置兼容的客户端，我会补充操作系统和使用场景。"}, Intent: AssistantIntentClientSetup, Order: 4},
	{PromptPreset: PromptPreset{Id: "pricing_cost", Label: "费用估算", Prompt: "请先解释计费方式，再根据我的模型和用量估算成本。"}, Intent: AssistantIntentCost, Order: 4},
	{PromptPreset: PromptPreset{Id: "api_key", Label: "连接 API", Prompt: "请说明创建 API Key、Base URL 和模型 ID 的安全配置步骤。"}, Intent: AssistantIntentAPIKey, Order: 5},
	{PromptPreset: PromptPreset{Id: "model_choice", Label: "选择模型", Prompt: "请根据质量、速度和成本要求帮助我选择可用模型。"}, Intent: AssistantIntentModels, Order: 6},
	{PromptPreset: PromptPreset{Id: "bounty", Label: "开源悬赏", Prompt: "请介绍如何浏览、接受或发布开源悬赏，以及需要准备哪些真实证据。"}, Intent: AssistantIntentBounty, Order: 7},
	{PromptPreset: PromptPreset{Id: "human_support", Label: "人工支持", Prompt: "我遇到了需要人工处理的问题，请先帮我整理必要信息并说明联系途径。"}, Intent: AssistantIntentHumanSupport, Order: 8},
}

func fallbackPromptPresets() PromptPresetSet {
	presets := make([]PromptPreset, 0, maxPromptPresets)
	for _, candidate := range promptCandidates[:maxPromptPresets] {
		presets = append(presets, candidate.PromptPreset)
	}
	return PromptPresetSet{
		Generation: 0,
		Version:    fallbackPresetVersion,
		Presets:    presets,
	}
}

func GetPromptPresets() (PromptPresetSet, error) {
	if custom, configured := configuredPromptPresets(); configured {
		return PromptPresetSet{Generation: 0, Version: "custom-v1", Presets: custom}, nil
	}
	var generation int64
	if err := DB.Model(&PromptPresetRow{}).Select("COALESCE(MAX(generation), 0)").Scan(&generation).Error; err != nil {
		return PromptPresetSet{}, err
	}
	if generation <= 0 {
		return fallbackPromptPresets(), nil
	}
	rows := make([]PromptPresetRow, 0, maxPromptPresets)
	if err := DB.Where("generation = ?", generation).
		Order("position ASC, preset_id ASC").Limit(maxPromptPresets).Find(&rows).Error; err != nil {
		return PromptPresetSet{}, err
	}
	if len(rows) == 0 {
		return fallbackPromptPresets(), nil
	}
	presets := make([]PromptPreset, 0, len(rows))
	required := make(map[string]struct{})
	for _, candidate := range promptCandidates {
		if candidate.Required {
			required[candidate.Id] = struct{}{}
		}
	}
	for _, row := range rows {
		if row.Version != PromptPresetVersion {
			// Old aggregate templates remain stale even when all required IDs
			// exist. Serve the new seed until the scheduled refresh replaces them.
			return fallbackPromptPresets(), nil
		}
		presets = append(presets, PromptPreset{Id: row.PresetId, Prompt: row.Prompt, Label: row.Label, Source: "default"})
		delete(required, row.PresetId)
	}
	if len(required) > 0 {
		// A cache generated before a required starter was introduced is stale.
		// Use the bounded backend seed until the scheduled refresh materializes a
		// new aggregate snapshot; never expose a partial starter set.
		return fallbackPromptPresets(), nil
	}
	return PromptPresetSet{Generation: generation, Version: rows[0].Version, Presets: presets}, nil
}

func configuredPromptPresets() ([]PromptPreset, bool) {
	common.OptionMapRWMutex.RLock()
	raw := common.OptionMap["AssistantPreConversationPresets"]
	common.OptionMapRWMutex.RUnlock()
	if strings.TrimSpace(raw) == "" {
		return nil, false
	}
	var configured []customPromptPresetEntry
	if err := json.Unmarshal([]byte(raw), &configured); err != nil || len(configured) > 20 {
		return nil, false
	}
	entries := make([]PromptPreset, 0, len(configured))
	for _, entry := range configured {
		translations, label, prompt, ok := entry.copies()
		if !ok {
			return nil, false
		}
		entries = append(entries, PromptPreset{
			Id: strings.TrimSpace(entry.ID), Label: label, Prompt: prompt,
			Source: "custom", Translations: translations,
		})
	}
	return entries, true
}

// customPromptPresetVariants returns every accepted copy for a custom preset so
// a first turn is attributed regardless of the locale the client displayed.
func customPromptPresetVariants(preset PromptPreset) []string {
	if preset.Source != "custom" || len(preset.Translations) == 0 {
		return nil
	}
	variants := make([]string, 0, len(preset.Translations))
	for _, copy := range preset.Translations {
		variants = append(variants, copy.Prompt)
	}
	return variants
}

func findPromptPreset(presetId string) (*PromptPresetRef, string, error) {
	presetId = strings.TrimSpace(presetId)
	if presetId == "" {
		return nil, "", ErrPromptPresetNotFound
	}
	current, err := GetPromptPresets()
	if err != nil {
		return nil, "", err
	}
	for _, preset := range current.Presets {
		if preset.Id == presetId {
			return &PromptPresetRef{
				PresetId: preset.Id, Generation: current.Generation, Version: current.Version,
			}, preset.Prompt, nil
		}
	}
	return nil, "", ErrPromptPresetNotFound
}

// ResolvePromptPreset validates that a first turn
// came from the current server-owned preset rather than trusting an arbitrary
// client-supplied analytics label.
func ResolvePromptPreset(presetId string, prompt string) (*PromptPresetRef, error) {
	attribution, expectedPrompt, err := findPromptPreset(presetId)
	if err != nil {
		return nil, err
	}
	normalize := func(value string) string { return strings.Join(strings.Fields(strings.TrimSpace(value)), " ") }
	normalized := normalize(prompt)
	if normalized == normalize(expectedPrompt) {
		return attribution, nil
	}
	if current, err := GetPromptPresets(); err == nil {
		for _, preset := range current.Presets {
			if preset.Id != attribution.PresetId {
				continue
			}
			for _, variant := range customPromptPresetVariants(preset) {
				if normalized == normalize(variant) {
					return attribution, nil
				}
			}
		}
	}
	// Only exact, reviewed translations for this current preset qualify.
	// Arbitrary edits, another preset's copy and unknown IDs remain unattributed.
	for _, variant := range requiredPromptPresetCopy[attribution.PresetId] {
		if normalized == normalize(variant) {
			return attribution, nil
		}
	}
	return nil, ErrPromptPresetNotFound
}
