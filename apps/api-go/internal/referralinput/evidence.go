// Package referralinput validates moderation input before database work.
package referralinput

import (
	"errors"
	"strings"
	"unicode/utf8"
)

const MaxEvidenceBytes = 1000

var ErrInvalidEvidence = errors.New("moderation evidence must contain 1-1000 UTF-8 bytes without NUL")

// NormalizeEvidence keeps the existing byte limit and Unicode TrimSpace rules.
// Never truncate evidence: a partial explanation must not become an audit record.
func NormalizeEvidence(value string) (string, error) {
	value = strings.TrimSpace(value)
	if len(value) == 0 || len(value) > MaxEvidenceBytes || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return "", ErrInvalidEvidence
	}
	return value, nil
}
