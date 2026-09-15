package dto

import (
	"strings"
	"unicode/utf8"
)

// normalizeTokenJSON decodes JSON Unicode escapes only on fields that are
// still represented as raw JSON when token-count metadata is assembled.
// Semantic message text must not pass through this helper: a user can
// legitimately ask the model about the literal six-character string \u4e2d.
func normalizeTokenJSON(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}

	var builder strings.Builder
	builder.Grow(len(raw))
	backslashRun := 0
	for i := 0; i < len(raw); {
		if raw[i] == '\\' {
			if backslashRun%2 == 0 {
				if decoded, consumed, ok := decodeTokenJSONUnicodeEscape(raw[i:]); ok {
					builder.WriteRune(decoded)
					i += consumed
					backslashRun = 0
					continue
				}
			}
			builder.WriteByte(raw[i])
			backslashRun++
			i++
			continue
		}

		builder.WriteByte(raw[i])
		backslashRun = 0
		i++
	}
	return builder.String()
}

func decodeTokenJSONUnicodeEscape(raw []byte) (rune, int, bool) {
	if len(raw) < 6 || raw[0] != '\\' || raw[1] != 'u' {
		return 0, 0, false
	}
	first, ok := tokenJSONHex4(raw[2:6])
	if !ok {
		return 0, 0, false
	}
	if first >= 0xD800 && first <= 0xDBFF {
		if len(raw) >= 12 && raw[6] == '\\' && raw[7] == 'u' {
			second, secondOK := tokenJSONHex4(raw[8:12])
			if secondOK && second >= 0xDC00 && second <= 0xDFFF {
				decoded := rune(0x10000 + (uint32(first-0xD800) << 10) + uint32(second-0xDC00))
				return decoded, 12, true
			}
		}
		return utf8.RuneError, 6, true
	}
	if first >= 0xDC00 && first <= 0xDFFF {
		return utf8.RuneError, 6, true
	}
	return rune(first), 6, true
}

func tokenJSONHex4(value []byte) (uint16, bool) {
	if len(value) != 4 {
		return 0, false
	}
	var result uint16
	for _, current := range value {
		var nibble byte
		switch {
		case current >= '0' && current <= '9':
			nibble = current - '0'
		case current >= 'a' && current <= 'f':
			nibble = current - 'a' + 10
		case current >= 'A' && current <= 'F':
			nibble = current - 'A' + 10
		default:
			return 0, false
		}
		result = result<<4 | uint16(nibble)
	}
	return result, true
}
