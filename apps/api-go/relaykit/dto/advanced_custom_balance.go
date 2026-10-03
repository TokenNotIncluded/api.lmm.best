package dto

import (
	"encoding/json"
	"errors"
	"math"
	"strings"
	"unicode/utf8"
)

const (
	MaxAdvancedCustomBalanceBodyBytes    = 16 << 10
	MaxAdvancedCustomBalancePointerBytes = 1024
	MaxAdvancedCustomBalancePointerDepth = 32
)

// AdvancedCustomBalanceConfig applies only to the channel's balance route.
// Templates contain JSON data, never executable expressions or network targets.
type AdvancedCustomBalanceConfig struct {
	Method       string   `json:"method,omitempty"`
	BodyTemplate *string  `json:"body_template,omitempty"`
	JSONPointer  string   `json:"json_pointer,omitempty"`
	Scale        *float64 `json:"scale,omitempty"`
}

func (c *AdvancedCustomBalanceConfig) RequestMethod() string {
	if c == nil || strings.TrimSpace(c.Method) == "" {
		return "GET"
	}
	return strings.ToUpper(strings.TrimSpace(c.Method))
}

func (c *AdvancedCustomBalanceConfig) Validate() error {
	if c == nil {
		return nil
	}
	method := c.RequestMethod()
	if method != "GET" && method != "POST" {
		return errors.New("balance method must be GET or POST")
	}
	if c.BodyTemplate != nil {
		if method != "POST" {
			return errors.New("balance body template requires POST")
		}
		if len(*c.BodyTemplate) > MaxAdvancedCustomBalanceBodyBytes || !utf8.ValidString(*c.BodyTemplate) {
			return errors.New("balance body template is too large or is not UTF-8")
		}
		if !json.Valid([]byte(*c.BodyTemplate)) {
			return errors.New("balance body template must be valid JSON")
		}
	}
	if _, err := AdvancedCustomBalancePointerTokens(c.JSONPointer); err != nil {
		return err
	}
	if c.Scale != nil {
		if c.JSONPointer == "" {
			return errors.New("balance scale requires a JSON pointer")
		}
		if *c.Scale <= 0 || math.IsNaN(*c.Scale) || math.IsInf(*c.Scale, 0) {
			return errors.New("balance scale must be finite and positive")
		}
	}
	return nil
}

func (c *AdvancedCustomBalanceConfig) RenderBody(apiKey string) ([]byte, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	if c == nil || c.BodyTemplate == nil {
		return nil, nil
	}
	const placeholder = "{api_key}"
	count := strings.Count(*c.BodyTemplate, placeholder)
	if count == 0 {
		return []byte(*c.BodyTemplate), nil
	}
	if len(apiKey) > MaxAdvancedCustomBalanceBodyBytes {
		return nil, errors.New("rendered balance body is too large")
	}
	keyJSON, err := json.Marshal(apiKey)
	if err != nil {
		return nil, errors.New("balance credential cannot be encoded")
	}
	// Substitute only the escaped contents of a JSON string. A placeholder
	// outside a string is already rejected by the template JSON validation.
	escapedKey := string(keyJSON[1 : len(keyJSON)-1])
	staticBytes := len(*c.BodyTemplate) - count*len(placeholder)
	if len(escapedKey) > (MaxAdvancedCustomBalanceBodyBytes-staticBytes)/count {
		return nil, errors.New("rendered balance body is too large")
	}
	body := []byte(strings.ReplaceAll(*c.BodyTemplate, placeholder, escapedKey))
	if len(body) > MaxAdvancedCustomBalanceBodyBytes {
		return nil, errors.New("rendered balance body is too large")
	}
	if !json.Valid(body) {
		return nil, errors.New("rendered balance body must be valid JSON")
	}
	return body, nil
}

// AdvancedCustomBalancePointerTokens accepts the JSON string representation
// from RFC 6901, not a URI fragment or an executable JSONPath expression.
// Empty means that the existing automatic extractor remains in use.
func AdvancedCustomBalancePointerTokens(pointer string) ([]string, error) {
	if pointer == "" {
		return nil, nil
	}
	if len(pointer) > MaxAdvancedCustomBalancePointerBytes || !utf8.ValidString(pointer) || !strings.HasPrefix(pointer, "/") {
		return nil, errors.New("balance JSON pointer must start with / and fit within 1024 bytes")
	}
	tokens := strings.Split(pointer[1:], "/")
	if len(tokens) > MaxAdvancedCustomBalancePointerDepth {
		return nil, errors.New("balance JSON pointer has too many levels")
	}
	for i, token := range tokens {
		for j := 0; j < len(token); j++ {
			if token[j] != '~' {
				continue
			}
			if j+1 >= len(token) || (token[j+1] != '0' && token[j+1] != '1') {
				return nil, errors.New("balance JSON pointer has an invalid escape")
			}
			j++
		}
		tokens[i] = strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
	}
	return tokens, nil
}
