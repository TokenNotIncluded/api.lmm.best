package service

import (
	"strings"
	"unicode/utf8"
)

// truncateAuthMetadata keeps the existing byte budget without splitting UTF-8.
// These are display-only IP/User-Agent fields, not credentials or identifiers.
// Replace malformed bytes and NUL so optional metadata remains valid SQL text.
func truncateAuthMetadata(value string, max int) string {
	if max <= 0 {
		return ""
	}
	value = strings.TrimSpace(value)
	var result strings.Builder
	result.Grow(min(len(value), max))
	for _, r := range value {
		if r == 0 {
			r = utf8.RuneError
		}
		if utf8.RuneLen(r) > max-result.Len() {
			break
		}
		result.WriteRune(r)
	}
	return result.String()
}
