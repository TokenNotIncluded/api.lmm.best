// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
// Redaction adapted from api.lmm.best main 6efcb6fb model/assistant_history.go.
// Package redaction masks credentials in assistant history and untrusted tool results.
package redaction

import (
	"net"
	"regexp"
	"strings"
)

// These names cover pasted credential fields, not arbitrary private text.
const assistantHistoryCredentialField = `cookie|set-cookie|session(?:[_ -]?id)?|csrf(?:[_ -]?token)?|password|passwd|pwd|api[ _-]?key|access[ _-]?token|refresh[ _-]?token|id[ _-]?token|client[ _-]?secret|secret|token|bearer|authorization|proxy-authorization|密碼|密码|密钥|令牌`

var coreCredentialPattern = regexp.MustCompile(`(?i)\blmm[ksp]_[a-z0-9_-]{12,}\b`)
var (
	assistantHistoryAPIKeyPattern = regexp.MustCompile(`(?i)\b(?:sk|rk|pk|ak|tok|token|key|secret)[_-][a-z0-9._~+/-]{8,}\b`)
	// A pasted key can become part of a malformed TOML provider identifier,
	// hiding its prefix from the normal word-boundary matcher. Restrict this
	// additional match to provider sections with a long credential-like suffix.
	assistantHistoryTOMLKeyPattern = regexp.MustCompile(`(?i)\[(model_providers|providers)\.[^\]\r\n]*(?:sk|rk|pk|ak)[_-][a-z0-9._~+/-]{20,}[^\]\r\n]*\]`)
	assistantHistoryJWTPattern     = regexp.MustCompile(`\beyJ[a-zA-Z0-9_-]{10,}\.[a-zA-Z0-9_-]{10,}\.[a-zA-Z0-9_-]{10,}\b`)
	assistantHistoryEmailPattern   = regexp.MustCompile(`(?i)\b[a-z0-9.!#$%&'*+/=?^_` + "`" + `{|}~-]+@[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)+\b`)
	assistantHistoryCookiePattern  = regexp.MustCompile(`(?i)\b(cookie|set-cookie|session(?:[_ -]?id)?|csrf(?:[_ -]?token)?)\s*[:=：]\s*[^\s;,]+`)
	assistantHistoryBearerPattern  = regexp.MustCompile(`(?i)\bbearer\s+[a-z0-9._~+/-]{6,}=*`)
	assistantHistorySecretPattern  = regexp.MustCompile(`(?i)\b(password|passwd|pwd|api[ _-]?key|access[ _-]?token|refresh[ _-]?token|bearer|authorization|密碼|密码|密钥|令牌)\s*[:=：]\s*[^\s,;]+`)
	assistantHistoryURLSecret      = regexp.MustCompile(`(?i)([?&](?:api[_-]?key|access[_-]?token|refresh[_-]?token|id[_-]?token|client[_-]?secret|token|password|passwd|secret)=)[^&#\s]+`)
	assistantHistoryPEMPrivateKey  = regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?(?:-----END [A-Z0-9 ]*PRIVATE KEY-----|$)`)
	// Consume complete quoted values, including spaces and escaped quotes.
	// This also works for JSON fragments pasted inside prose or a code block.
	assistantHistoryQuotedCredential = regexp.MustCompile(`(?i)(["'](?:` + assistantHistoryCredentialField + `)["'][\t \r\n]*[:=：][\t \r\n]*)(?:"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*')`)
	// A Cookie header has multiple values, and Authorization schemes can have
	// multiple words. Redact the entire value, but never the next header line.
	assistantHistoryCredentialHeader = regexp.MustCompile(`(?i)\b(cookie|set-cookie|authorization|proxy-authorization)[\t ]*[:=：][\t ]*[^\r\n]*`)
	assistantHistoryURLUserInfo      = regexp.MustCompile(`(?i)(\b[a-z][a-z0-9+.-]*://)[^/\s?#@]+@`)

	assistantHistoryIPv4Pattern  = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)
	assistantHistoryIPv6Pattern  = regexp.MustCompile(`[0-9A-Fa-f:]{2,39}`)
	assistantHistoryPhonePattern = regexp.MustCompile(`(^|[^\w])((?:\+?86[\s-]?)?1[3-9]\d{9}|\+\d{1,3}(?:[\s.-]?\d{2,4}){2,4})([^\w]|$)`)
	assistantHistoryCardPattern  = regexp.MustCompile(`(^|[^0-9])([0-9][0-9 -]{11,22}[0-9])([^0-9]|$)`)
)

func Content(value string) string { return Text(strings.TrimSpace(value)) }

// Text preserves whitespace around structured message fields.
func Text(value string) string {
	value = assistantHistoryQuotedCredential.ReplaceAllString(value, `${1}"[REDACTED]"`)
	value = assistantHistoryCredentialHeader.ReplaceAllString(value, "$1: [REDACTED]")
	value = assistantHistoryURLUserInfo.ReplaceAllString(value, "${1}[REDACTED]@")
	value = coreCredentialPattern.ReplaceAllString(value, "[REDACTED_CORE_CREDENTIAL]")
	value = assistantHistoryPEMPrivateKey.ReplaceAllString(value, "[REDACTED_PRIVATE_KEY]")
	value = assistantHistoryURLSecret.ReplaceAllString(value, "$1[REDACTED]")
	value = assistantHistoryCookiePattern.ReplaceAllString(value, "$1: [REDACTED]")
	value = assistantHistoryBearerPattern.ReplaceAllString(value, "Bearer [REDACTED_TOKEN]")
	value = assistantHistorySecretPattern.ReplaceAllString(value, "$1: [REDACTED]")
	value = assistantHistoryTOMLKeyPattern.ReplaceAllString(value, "[$1.REDACTED_API_KEY]")
	value = assistantHistoryAPIKeyPattern.ReplaceAllString(value, "[REDACTED_API_KEY]")
	value = assistantHistoryJWTPattern.ReplaceAllString(value, "[REDACTED_TOKEN]")
	value = assistantHistoryEmailPattern.ReplaceAllString(value, "[REDACTED_EMAIL]")
	value = redactAssistantPhoneNumbers(value)
	value = redactAssistantIPAddresses(value)
	value = redactAssistantCardNumbers(value)
	return value
}

func redactAssistantPhoneNumbers(value string) string {
	return assistantHistoryPhonePattern.ReplaceAllString(value, "$1[REDACTED_PHONE]$3")
}

func redactAssistantIPAddresses(value string) string {
	value = assistantHistoryIPv4Pattern.ReplaceAllStringFunc(value, func(candidate string) string {
		if net.ParseIP(candidate) != nil {
			return "[REDACTED_IP]"
		}
		return candidate
	})
	return assistantHistoryIPv6Pattern.ReplaceAllStringFunc(value, func(candidate string) string {
		if !strings.Contains(candidate, ":") || net.ParseIP(candidate) == nil {
			return candidate
		}
		return "[REDACTED_IP]"
	})
}

func redactAssistantCardNumbers(value string) string {
	return assistantHistoryCardPattern.ReplaceAllStringFunc(value, func(candidate string) string {
		matches := assistantHistoryCardPattern.FindStringSubmatch(candidate)
		if len(matches) != 4 {
			return candidate
		}
		digits := strings.Map(func(r rune) rune {
			if r >= '0' && r <= '9' {
				return r
			}
			return -1
		}, matches[2])
		if len(digits) < 13 || len(digits) > 19 || !assistantLuhnValid(digits) {
			return candidate
		}
		return matches[1] + "[REDACTED_CARD]" + matches[3]
	})
}

func assistantLuhnValid(digits string) bool {
	sum := 0
	double := false
	for index := len(digits) - 1; index >= 0; index-- {
		digit := int(digits[index] - '0')
		if double {
			digit *= 2
			if digit > 9 {
				digit -= 9
			}
		}
		sum += digit
		double = !double
	}
	return sum%10 == 0
}
