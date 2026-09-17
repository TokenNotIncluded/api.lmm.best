package model

import (
	"strings"
	"unicode/utf8"
)

const referralEvidenceMaxCharacters = 1000

// normalizeReferralEvidence matches varchar(1000): the limit is Unicode code
// points, not UTF-8 bytes. Preserve the submitted evidence except for the
// existing edge-whitespace normalization; never silently truncate audit text.
func normalizeReferralEvidence(value string) (string, bool) {
	value = strings.TrimSpace(value)
	valid := value != "" && utf8.ValidString(value) && !strings.ContainsRune(value, 0) &&
		utf8.RuneCountInString(value) <= referralEvidenceMaxCharacters
	return value, valid
}
