package model

import "strings"

// RedactModerationContent uses the same expressions and replacement order as
// assistant history. Necessary-character guards avoid running expensive
// expressions over a long ordinary paragraph when they cannot possibly match.
// Keep its parity corpus in step with changes to the history redactor.
func RedactModerationContent(value string) string {
	value = strings.TrimSpace(value)
	if strings.Contains(value, "-----BEGIN ") {
		value = assistantHistoryPEMPrivateKey.ReplaceAllString(value, "[REDACTED_PRIVATE_KEY]")
	}
	if strings.ContainsAny(value, "?&") {
		value = assistantHistoryURLSecret.ReplaceAllString(value, "$1[REDACTED]")
	}
	if strings.ContainsAny(value, ":=：") {
		value = assistantHistoryCookiePattern.ReplaceAllString(value, "$1: [REDACTED]")
	}
	if strings.Contains(strings.ToLower(value), "bearer") {
		value = assistantHistoryBearerPattern.ReplaceAllString(value, "Bearer [REDACTED_TOKEN]")
	}
	if strings.ContainsAny(value, ":=：") {
		value = assistantHistorySecretPattern.ReplaceAllString(value, "$1: [REDACTED]")
	}
	if strings.Contains(value, "[") && strings.ContainsAny(value, "_-") {
		value = assistantHistoryTOMLKeyPattern.ReplaceAllString(value, "[$1.REDACTED_API_KEY]")
	}
	if strings.ContainsAny(value, "_-") {
		value = assistantHistoryAPIKeyPattern.ReplaceAllString(value, "[REDACTED_API_KEY]")
	}
	if strings.Contains(value, "eyJ") && strings.Contains(value, ".") {
		value = assistantHistoryJWTPattern.ReplaceAllString(value, "[REDACTED_TOKEN]")
	}
	if strings.Contains(value, "@") {
		value = assistantHistoryEmailPattern.ReplaceAllString(value, "[REDACTED_EMAIL]")
	}
	digits := moderationHasDigits(value)
	if digits {
		value = redactAssistantPhoneNumbers(value)
	}
	if strings.Contains(value, ":") || (digits && strings.Contains(value, ".")) {
		value = redactAssistantIPAddresses(value)
	}
	if digits {
		value = redactAssistantCardNumbers(value)
	}
	return value
}

func moderationHasDigits(value string) bool {
	for index := 0; index < len(value); index++ {
		if value[index] >= '0' && value[index] <= '9' {
			return true
		}
	}
	return false
}
