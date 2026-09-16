package dto

import (
	"encoding/json"
	"unicode/utf16"
	"unicode/utf8"
)

// normalizeRawJSONForTokenCount decodes JSON Unicode escapes only while
// scanning JSON string literals. It is intentionally used only at raw-JSON
// token-count boundaries: semantic strings have already been decoded by the
// JSON parser and must not be decoded a second time.
func normalizeRawJSONForTokenCount(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}

	out := make([]byte, 0, len(raw))
	inString := false
	for i := 0; i < len(raw); {
		b := raw[i]
		if !inString {
			out = append(out, b)
			if b == '"' {
				inString = true
			}
			i++
			continue
		}

		if b == '"' {
			out = append(out, b)
			inString = false
			i++
			continue
		}
		if b != '\\' {
			out = append(out, b)
			i++
			continue
		}
		if i+1 >= len(raw) {
			out = append(out, b)
			i++
			continue
		}
		if raw[i+1] != 'u' {
			// Consume the escaped byte as a pair. This keeps escaped quotes from
			// toggling string state and keeps escaped backslashes from exposing a
			// following literal "uXXXX" sequence to this scanner.
			out = append(out, raw[i], raw[i+1])
			i += 2
			continue
		}

		codeUnit, ok := decodeJSONHex4(raw[i+2:])
		if !ok {
			out = append(out, raw[i], raw[i+1])
			i += 2
			continue
		}

		r := rune(codeUnit)
		if codeUnit >= 0xD800 && codeUnit <= 0xDBFF {
			if i+12 <= len(raw) && raw[i+6] == '\\' && raw[i+7] == 'u' {
				low, lowOK := decodeJSONHex4(raw[i+8:])
				if lowOK && low >= 0xDC00 && low <= 0xDFFF {
					out = utf8.AppendRune(out, utf16.DecodeRune(r, rune(low)))
					i += 12
					continue
				}
			}
			// Preserve malformed/lone surrogates instead of manufacturing the
			// replacement rune. Billing normalization must be deterministic.
			out = append(out, raw[i:i+6]...)
			i += 6
			continue
		}
		if codeUnit >= 0xDC00 && codeUnit <= 0xDFFF {
			out = append(out, raw[i:i+6]...)
			i += 6
			continue
		}

		out = utf8.AppendRune(out, r)
		i += 6
	}
	return string(out)
}

func decodeJSONHex4(raw []byte) (uint16, bool) {
	if len(raw) < 4 {
		return 0, false
	}
	var value uint16
	for _, b := range raw[:4] {
		value <<= 4
		switch {
		case b >= '0' && b <= '9':
			value |= uint16(b - '0')
		case b >= 'a' && b <= 'f':
			value |= uint16(b-'a') + 10
		case b >= 'A' && b <= 'F':
			value |= uint16(b-'A') + 10
		default:
			return 0, false
		}
	}
	return value, true
}
