package service

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/LIghtJUNction/api.lmm.best/model"
)

const marketSecretGuardMaxBytes = 2 << 20
const marketSecretGuardMaxDepth = 32

// marketRemoteSecretSafe prevents publisher credentials reflected by a remote
// from becoming public tool metadata or durable caller delivery payloads.
// Any unsupported or over-budget value fails closed without exposing a value.
func marketRemoteSecretSafe(value any, credential *model.ToolMarketResolvedCredential) bool {
	if credential == nil || credential.Secret == "" {
		return true
	}
	data, err := json.Marshal(value)
	if err != nil || len(data) > marketSecretGuardMaxBytes {
		return false
	}
	guard := marketSecretGuard{remaining: marketSecretGuardMaxBytes}
	for _, variant := range []string{credential.Secret, base64.StdEncoding.EncodeToString([]byte(credential.Secret)), base64.RawStdEncoding.EncodeToString([]byte(credential.Secret)), base64.URLEncoding.EncodeToString([]byte(credential.Secret)), base64.RawURLEncoding.EncodeToString([]byte(credential.Secret))} {
		duplicate := false
		for _, existing := range guard.variants {
			if existing == variant {
				duplicate = true
				break
			}
		}
		if !duplicate {
			guard.variants = append(guard.variants, variant)
		}
	}
	if !guard.take(len(data)) {
		return false
	}
	decoded, err := marketSecretGuardDecode(data)
	return err == nil && guard.safe(decoded, 0)
}

type marketSecretGuard struct {
	variants  []string
	remaining int
}

func (g *marketSecretGuard) take(size int) bool {
	if size < 0 || size > g.remaining {
		return false
	}
	g.remaining -= size
	return true
}

func marketSecretGuardDecode(data []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, io.ErrUnexpectedEOF
		}
		return nil, err
	}
	return decoded, nil
}

func (g *marketSecretGuard) matches(value string) bool {
	for _, variant := range g.variants {
		if strings.Contains(value, variant) {
			return true
		}
	}
	return false
}

func (g *marketSecretGuard) safe(value any, depth int) bool {
	if depth > marketSecretGuardMaxDepth || !g.take(1) {
		return false
	}
	switch value := value.(type) {
	case map[string]any:
		for key, child := range value {
			if !g.safe(key, depth+1) || !g.safe(child, depth+1) {
				return false
			}
		}
	case []any:
		for _, child := range value {
			if !g.safe(child, depth+1) {
				return false
			}
		}
	case string:
		return g.safeString(value, depth)
	case json.Number:
		return g.take(len(value.String())) && !g.matches(value.String())
	case nil, bool:
		return true
	default:
		return false
	}
	return true
}

func (g *marketSecretGuard) safeString(value string, depth int) bool {
	if !g.take(len(value)) || g.matches(value) {
		return false
	}
	trimmed := strings.TrimSpace(value)
	if len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[' || trimmed[0] == '"') {
		if !g.take(len(trimmed)) {
			return false
		}
		if decoded, err := marketSecretGuardDecode([]byte(trimmed)); err == nil && !g.safe(decoded, depth+1) {
			return false
		}
	}
	if strings.Contains(value, `\`) {
		unescaped, ok := g.unescapeText(value)
		if !ok || (unescaped != value && !g.safe(unescaped, depth+1)) {
			return false
		}
	}
	// Media content is serialized as base64. Decode a whole candidate as well:
	// a reflected secret inside binary/text prefixes may not be 3-byte aligned.
	if encoding := marketSecretGuardBase64Encoding(value); encoding != nil {
		if !g.take(len(value)) {
			return false
		}
		if decoded, err := encoding.DecodeString(value); err == nil {
			text := string(decoded)
			if g.matches(text) || (text != value && utf8.Valid(decoded) && !g.safe(text, depth+1)) {
				return false
			}
		}
	}
	return true
}

func marketSecretGuardBase64Encoding(value string) *base64.Encoding {
	if len(value) < 4 {
		return nil
	}
	for _, character := range value {
		if !((character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || strings.ContainsRune("+/-_=", character)) {
			return nil
		}
	}
	url := strings.ContainsAny(value, "-_")
	padded := strings.HasSuffix(value, "=")
	if url && padded {
		return base64.URLEncoding
	}
	if url {
		return base64.RawURLEncoding
	}
	if padded {
		return base64.StdEncoding
	}
	return base64.RawStdEncoding
}

// Preserve valid JSON escapes while safely quoting ordinary text, including
// literal quotes, controls and non-JSON backslashes. Decode each layer once.
func (g *marketSecretGuard) unescapeText(value string) (string, bool) {
	var quoted strings.Builder
	appendText := func(text string) bool {
		if !g.take(len(text)) {
			return false
		}
		quoted.WriteString(text)
		return true
	}
	if !appendText(`"`) {
		return "", false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		switch {
		case character == '\\':
			length := 1
			if index+1 < len(value) && strings.ContainsRune(`"\/bfnrt`, rune(value[index+1])) {
				length = 2
			} else if index+5 < len(value) && value[index+1] == 'u' {
				length = 6
				for _, digit := range value[index+2 : index+6] {
					if !((digit >= '0' && digit <= '9') || (digit >= 'a' && digit <= 'f') || (digit >= 'A' && digit <= 'F')) {
						length = 1
						break
					}
				}
			}
			if length == 1 {
				if !appendText(`\\`) {
					return "", false
				}
			} else {
				if !appendText(value[index : index+length]) {
					return "", false
				}
				index += length - 1
			}
		case character == '"':
			if !appendText(`\"`) {
				return "", false
			}
		case character < 0x20:
			const hexadecimal = "0123456789abcdef"
			if !appendText(`\u00` + string([]byte{hexadecimal[character>>4], hexadecimal[character&15]})) {
				return "", false
			}
		default:
			if !appendText(value[index : index+1]) {
				return "", false
			}
		}
	}
	if !appendText(`"`) || !g.take(quoted.Len()) {
		return "", false
	}
	var decoded string
	if json.Unmarshal([]byte(quoted.String()), &decoded) != nil {
		return "", false
	}
	return decoded, true
}
