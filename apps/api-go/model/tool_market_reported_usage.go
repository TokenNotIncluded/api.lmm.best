package model

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"

	"gorm.io/gorm"
)

const ToolMarketUsageReported = "tool_reported"
const ToolMarketUsageVerified = "platform_verified"

// Tool reports are a marketplace trust policy, not independent measurements.
// Pricing and ceilings come only from the approved, immutable call snapshot.
// A signed receipt, when supplied, must validate; a bad signature cannot fall
// back to unsigned usage. Historical unsigned outcomes have no accepted source.
func ReadToolMarketUsage(db *gorm.DB, c ToolMarketCall, data json.RawMessage) (map[string]int64, string, string, error) {
	var result map[string]json.RawMessage
	if decodeMarketUsageJSON(data, &result) != nil {
		return nil, "", "", ErrToolMarketMetering
	}
	var meta map[string]json.RawMessage
	if raw, ok := result["_meta"]; ok && decodeMarketUsageJSON(raw, &meta) != nil {
		return nil, "", "", ErrToolMarketMetering
	}
	if _, supplied := meta["lmm_metering"]; supplied {
		q, err := VerifyToolMarketMeteringResult(db, c, data)
		return q, ToolMarketUsageVerified, string(meta["lmm_metering"]), err
	}
	candidates := []json.RawMessage{}
	add := func(object json.RawMessage) error {
		if len(object) == 0 {
			return nil
		}
		var fields map[string]json.RawMessage
		if decodeMarketUsageJSON(object, &fields) != nil || fields == nil {
			return ErrToolMarketMetering
		}
		if usage, ok := fields["usage"]; ok {
			candidates = append(candidates, usage)
		}
		return nil
	}
	if raw, ok := meta["lmm_usage"]; ok {
		candidates = append(candidates, raw)
	}
	if raw, ok := result["usage"]; ok {
		candidates = append(candidates, raw)
	}
	if raw, ok := result["structuredContent"]; ok {
		// Structured output can be any JSON value; only object envelopes have usage.
		if len(bytes.TrimSpace(raw)) > 0 && bytes.TrimSpace(raw)[0] == '{' && add(raw) != nil {
			return nil, "", "", ErrToolMarketMetering
		}
	}
	var content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if raw, ok := result["content"]; ok && json.Unmarshal(raw, &content) != nil {
		return nil, "", "", ErrToolMarketMetering
	}
	for _, item := range content {
		if item.Type != "text" {
			continue
		}
		text := bytes.TrimSpace([]byte(item.Text))
		if len(text) > 0 && text[0] == '{' && json.Valid(text) {
			if add(text) != nil {
				return nil, "", "", ErrToolMarketMetering
			}
		}
	}
	if len(candidates) == 0 {
		return nil, "", "", ErrToolMarketMetering
	}
	rules := toolMarketCallRules(c)
	var quantities map[string]int64
	for _, raw := range candidates {
		if len(raw) > 65536 {
			return nil, "", "", ErrToolMarketMetering
		}
		var usage map[string]json.RawMessage
		if decodeMarketUsageJSON(raw, &usage) != nil || usage == nil {
			return nil, "", "", ErrToolMarketMetering
		}
		next := map[string]int64{}
		for _, rule := range rules {
			value, ok := usage[rule.Metric]
			var n int64
			if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) || json.Unmarshal(value, &n) != nil {
				return nil, "", "", ErrToolMarketMetering
			}
			next[rule.Metric] = n
		}
		if _, err := toolMarketRulesQuota(rules, next); err != nil {
			return nil, "", "", ErrToolMarketMetering
		}
		if quantities != nil && !reflect.DeepEqual(quantities, next) {
			return nil, "", "", ErrToolMarketMetering
		}
		quantities = next
	}
	return quantities, ToolMarketUsageReported, string(candidates[0]), nil
}

// Reject duplicate keys as well as trailing JSON: different parsers must not
// see different quantities from the same evidence.
func decodeMarketUsageJSON(raw []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	depth := 0
	var walk func() error
	walk = func() error {
		depth++
		defer func() { depth-- }()
		if depth > 128 {
			return ErrToolMarketMetering
		}
		t, err := d.Token()
		if err != nil {
			return err
		}
		if delim, ok := t.(json.Delim); ok {
			switch delim {
			case '{':
				seen := map[string]bool{}
				for d.More() {
					key, err := d.Token()
					if err != nil {
						return err
					}
					name, ok := key.(string)
					if !ok || seen[name] {
						return ErrToolMarketMetering
					}
					seen[name] = true
					if err := walk(); err != nil {
						return err
					}
				}
			case '[':
				for d.More() {
					if err := walk(); err != nil {
						return err
					}
				}
			default:
				return ErrToolMarketMetering
			}
			_, err = d.Token()
			return err
		}
		return nil
	}
	if walk() != nil {
		return ErrToolMarketMetering
	}
	if _, err := d.Token(); err != io.EOF {
		return ErrToolMarketMetering
	}
	return json.Unmarshal(raw, out)
}
