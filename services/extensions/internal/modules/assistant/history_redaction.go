// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package assistant

import "github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/assistant/redaction"

const assistantHistoryMessageMaxRunes = 4000
const assistantConversationTitleMaxRunes = 120

func RedactAssistantHistoryContent(value string) string { return redaction.Content(value) }

func redactAssistantHistoryBounded(value string) string {
	value = RedactAssistantHistoryContent(value)
	runes := []rune(value)
	if len(runes) > assistantHistoryMessageMaxRunes {
		return string(runes[:assistantHistoryMessageMaxRunes])
	}
	return value
}

func assistantConversationTitle(value string) string {
	value = redactAssistantHistoryBounded(value)
	runes := []rune(value)
	if len(runes) > assistantConversationTitleMaxRunes {
		return string(runes[:assistantConversationTitleMaxRunes])
	}
	return value
}
