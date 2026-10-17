// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package redaction

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"
)

var ErrInvalidJSON = errors.New("invalid_redaction_json")
var credentialField = regexp.MustCompile(`(?i)^(?:` + assistantHistoryCredentialField + `)$`)

// JSON masks a bounded untrusted result without editing JSON syntax or rounding
// numbers. Known connection/session secrets are removed even without a prefix.
// Duplicate keys, excessive depth and invalid UTF-8 fail closed.
func JSON(data []byte, secrets ...string) (json.RawMessage, error) {
	if len(data) == 0 || len(data) > 1<<20 || !utf8.Valid(data) || len(secrets) > 16 {
		return nil, ErrInvalidJSON
	}
	mask := func(value string) string {
		for _, secret := range secrets {
			if secret != "" {
				value = strings.ReplaceAll(value, secret, "[REDACTED]")
			}
		}
		return Text(value)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var read func(int) (any, error)
	read = func(depth int) (any, error) {
		if depth > 32 {
			return nil, ErrInvalidJSON
		}
		token, err := dec.Token()
		if err != nil {
			return nil, ErrInvalidJSON
		}
		switch token {
		case json.Delim('{'):
			object := map[string]any{}
			seen := map[string]bool{}
			for dec.More() {
				key, err := dec.Token()
				name, ok := key.(string)
				if err != nil || !ok || seen[name] {
					return nil, ErrInvalidJSON
				}
				seen[name] = true
				value, err := read(depth + 1)
				if err != nil {
					return nil, err
				}
				if credentialField.MatchString(name) {
					value = "[REDACTED]"
				}
				name = mask(name)
				if _, duplicate := object[name]; duplicate {
					return nil, ErrInvalidJSON
				}
				object[name] = value
			}
			if end, err := dec.Token(); err != nil || end != json.Delim('}') {
				return nil, ErrInvalidJSON
			}
			return object, nil
		case json.Delim('['):
			array := []any{}
			for dec.More() {
				value, err := read(depth + 1)
				if err != nil {
					return nil, err
				}
				array = append(array, value)
			}
			if end, err := dec.Token(); err != nil || end != json.Delim(']') {
				return nil, ErrInvalidJSON
			}
			return array, nil
		}
		switch value := token.(type) {
		case string:
			return mask(value), nil
		case json.Delim:
			return nil, ErrInvalidJSON
		default:
			return value, nil
		}
	}
	value, err := read(0)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, ErrInvalidJSON
	}
	out, err := json.Marshal(value)
	if err != nil || len(out) > 1<<20 {
		return nil, ErrInvalidJSON
	}
	return out, nil
}
