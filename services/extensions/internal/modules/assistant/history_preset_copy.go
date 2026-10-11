// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
// Reviewed locale copy reused from main 6efcb6fb model/assistant_prompt_preset_copy.go.
package assistant

// Keep these reviewed variants in sync with the four English keys used by the
// web client. The parity test checks every locale; client text is never trusted
// merely because it carries a known preset ID.
var requiredPromptPresetCopy = map[string]map[string]string{
	"ai_recommendation": {
		"en":    "How do I apply for L1 access?",
		"zh":    "如何申请 L1 访问权限？",
		"zh-TW": "如何申請 L1 存取權限？",
		"fr":    "Comment demander l’accès L1 ?",
		"ja":    "L1 の利用を申請するには？",
		"ru":    "Как запросить доступ L1?",
		"vi":    "Làm cách nào để xin quyền L1?",
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
