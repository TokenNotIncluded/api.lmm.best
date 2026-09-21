package controller

import (
	"unicode/utf8"

	"github.com/LIghtJUNction/api.lmm.best/model"
)

const assistantHistoryContextMaxItems = 100

const assistantHistoryExcerptMarker = "[Earlier history excerpt; some content or turns omitted]\n"

// compactAssistantHistoryToRuneBudget keeps recent complete turns verbatim,
// then uses bounded excerpts to retain the initial task and earlier context.
// These are explicitly incomplete quotations, never generated instructions or
// claims about completed operations. Roles stay unchanged and no protocol or
// authorization fields are reconstructed from history. Callers must evaluate
// server policy against the original owned history, before this lossy step.
func compactAssistantHistoryToRuneBudget(history []model.AssistantHistoryMessage, budget int) []model.AssistantHistoryMessage {
	if budget <= 0 || len(history) < 2 {
		return []model.AssistantHistoryMessage{}
	}
	countRunes := func(messages []model.AssistantHistoryMessage) int {
		total := 0
		for _, message := range messages {
			total += utf8.RuneCountInString(message.Content)
		}
		return total
	}
	if countRunes(history) <= budget {
		return history
	}

	// Reserve one third for excerpts, unless that would lose the newest full
	// turn. A long most-recent answer always takes precedence over old snippets.
	recent := trimAssistantHistoryToRuneBudget(history, budget-budget/3)
	if len(recent) == 0 {
		recent = trimAssistantHistoryToRuneBudget(history, budget)
	}
	remaining := budget - countRunes(recent)
	older := history[:len(history)-len(recent)]
	const minimumExcerptRunes = 128
	pairs := min(len(older)/2, remaining/(2*minimumExcerptRunes))
	if pairs == 0 {
		return recent
	}
	perMessage := min(256, remaining/(2*pairs))
	compacted := make([]model.AssistantHistoryMessage, 0, pairs*2+len(recent))
	for pair := 0; pair < pairs; pair++ {
		// Preserve the opening goal and the nearest omitted turns. This avoids
		// spending all the excerpt budget on the oldest, least relevant replies.
		start := 0
		if pair > 0 {
			start = len(older) - 2*(pairs-pair)
		}
		for _, original := range older[start : start+2] {
			message := original
			message.Content = assistantHistoryExcerpt(original.Content, perMessage)
			compacted = append(compacted, message)
		}
	}
	return append(compacted, recent...)
}

func assistantHistoryExcerpt(content string, budget int) string {
	markerRunes := utf8.RuneCountInString(assistantHistoryExcerptMarker)
	runes := []rune(content)
	if len(runes)+markerRunes <= budget {
		return assistantHistoryExcerptMarker + content
	}
	available := budget - markerRunes - 1 // one explicit ellipsis
	if available <= 0 {
		return string([]rune(assistantHistoryExcerptMarker)[:max(0, min(budget, markerRunes))])
	}
	head := (available + 1) / 2
	tail := available - head
	return assistantHistoryExcerptMarker + string(runes[:head]) + "…" + string(runes[len(runes)-tail:])
}
