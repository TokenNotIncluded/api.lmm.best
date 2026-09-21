package model

import "strings"

// Keep these reviewed variants in sync with the four English keys used by the
// web client. The parity test checks every locale; client text is never trusted
// merely because it carries a known preset ID.
var requiredPromptPresetCopy = map[string]map[string]string{
	"ai_recommendation": {
		"en":    "Help me write an L1 recommendation.",
		"zh":    "帮我写封 L1 推荐信吧。",
		"zh-TW": "幫我寫封 L1 推薦信吧。",
		"fr":    "Aidez-moi à rédiger une recommandation L1.",
		"ja":    "L1 の推薦文を書くのを手伝ってください。",
		"ru":    "Помогите мне написать рекомендацию для L1.",
		"vi":    "Giúp tôi viết thư giới thiệu để lên L1.",
	},
	"getting_started": {
		"en":    "Where should I start?",
		"zh":    "第一次来，我该从哪开始？",
		"zh-TW": "第一次來，我該從哪開始？",
		"fr":    "Par où commencer ?",
		"ja":    "何から始めればいいですか？",
		"ru":    "С чего мне начать?",
		"vi":    "Tôi nên bắt đầu từ đâu?",
	},
	"new_user_gift": {
		"en":    "How do I get the new-user gift?",
		"zh":    "新用户礼包怎么领取？",
		"zh-TW": "新使用者禮包怎麼領取？",
		"fr":    "Comment obtenir le cadeau de bienvenue ?",
		"ja":    "新規ユーザー特典はどうすればもらえますか？",
		"ru":    "Как получить подарок новому пользователю?",
		"vi":    "Làm sao để nhận quà cho người dùng mới?",
	},
	"weekly_discount": {
		"en":    "Any top-up discounts this week?",
		"zh":    "这周充值有什么优惠？",
		"zh-TW": "這週儲值有什麼優惠？",
		"fr":    "Y a-t-il une réduction sur les recharges cette semaine ?",
		"ja":    "今週のチャージ割引はありますか？",
		"ru":    "Есть скидки на пополнение на этой неделе?",
		"vi":    "Tuần này nạp tiền có ưu đãi gì?",
	},
}

func requiredPromptPreset(id string) PromptPreset {
	prompt := requiredPromptPresetCopy[id]["zh"]
	return PromptPreset{Id: id, Prompt: prompt, Label: prompt, Source: "default"}
}

func normalizePromptPresetLanguage(value string) string {
	value = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), "_", "-"))
	if value == localizedPromptPresetLabel {
		// The administrator default is a locale key too, not a language.
		return localizedPromptPresetLabel
	}
	if value == "zhtw" || value == "zh-tw" || value == "zh-hk" || value == "zh-mo" || strings.HasPrefix(value, "zh-hant") {
		return "zh-TW"
	}
	if value == "zhcn" || value == "zh" || strings.HasPrefix(value, "zh-") {
		return "zh"
	}
	switch base := strings.SplitN(value, "-", 2)[0]; base {
	case "fr", "ja", "ru", "vi":
		return base
	default:
		return "en"
	}
}

// LocalizePromptPresets changes copy, not IDs, ordering or attribution. Use a
// separate slice so callers cannot mutate a shared snapshot through this view.
func LocalizePromptPresets(set PromptPresetSet, language string) PromptPresetSet {
	locale := normalizePromptPresetLanguage(language)
	presets := make([]PromptPreset, len(set.Presets))
	copy(presets, set.Presets)
	for index, preset := range presets {
		if preset.Source == "custom" {
			presets[index].Prompt, presets[index].Label = customPromptPresetCopy(preset, locale)
			continue
		}
		if prompt, ok := requiredPromptPresetCopy[preset.Id][locale]; ok {
			presets[index].Prompt = prompt
			presets[index].Label = prompt
		}
	}
	set.Presets = presets
	return set
}

// customPromptPresetCopy selects the requested locale, then the administrator
// default, then the stored single-language copy. A custom preset therefore
// follows the interface language whenever a translation exists.
func customPromptPresetCopy(preset PromptPreset, locale string) (string, string) {
	if len(preset.Translations) > 0 {
		for _, candidate := range []string{locale, localizedPromptPresetLabel} {
			if copy, ok := preset.Translations[candidate]; ok {
				return copy.Prompt, copy.Label
			}
		}
	}
	return preset.Prompt, preset.Label
}
