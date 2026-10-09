package controller

import (
	"math"
	"strings"
)

// The worst case JSON escape expansion of a page remains below the shared
// tool-result budget. Exact reads have their own Unicode-character cursor.
func assistantAdminConfigReadSchema() map[string]any {
	return objectSchema(map[string]any{
		"query":        map[string]any{"type": "string", "maxLength": 200, "description": "Case-insensitive setting key or label search."},
		"key":          map[string]any{"type": "string", "maxLength": 200, "description": "One exact setting key returned by this tool."},
		"offset":       map[string]any{"type": "integer", "minimum": 0},
		"limit":        map[string]any{"type": "integer", "minimum": 1, "maximum": 20},
		"value_offset": map[string]any{"type": "integer", "minimum": 0},
		"value_limit":  map[string]any{"type": "integer", "minimum": 1, "maximum": 6000},
	}, nil)
}

func assistantReadInteger(input map[string]any, key string, fallback, min, max int) (int, bool) {
	raw, present := input[key]
	if !present {
		return fallback, true
	}
	number, ok := raw.(float64)
	if !ok || math.IsNaN(number) || math.IsInf(number, 0) || math.Trunc(number) != number || number < float64(min) || number > float64(max) {
		return 0, false
	}
	return int(number), true
}

func assistantAdminConfigPage(role int, input map[string]any) map[string]any {
	invalid := func() map[string]any {
		return map[string]any{"ok": false, "status": "invalid_arguments", "error": "Use an exact key or query, nonnegative cursors, limit 1-20 and value_limit 1-6000. Value cursors require an exact key."}
	}
	offset, a := assistantReadInteger(input, "offset", 0, 0, 1<<30)
	limit, b := assistantReadInteger(input, "limit", 10, 1, 20)
	valueOffset, d := assistantReadInteger(input, "value_offset", 0, 0, 1<<30)
	valueLimit, e := assistantReadInteger(input, "value_limit", 4000, 1, 6000)
	query := strings.ToLower(strings.TrimSpace(inputString(input, "query")))
	key := inputString(input, "key")
	if !a || !b || !d || !e || len([]rune(query)) > 200 || len(key) > 200 || (key != "" && query != "") {
		return invalid()
	}
	if key == "" {
		if _, ok := input["value_offset"]; ok {
			return invalid()
		}
		if _, ok := input["value_limit"]; ok {
			return invalid()
		}
	}
	for _, name := range []string{"query", "key"} {
		if raw, ok := input[name]; ok {
			if _, ok := raw.(string); !ok {
				return invalid()
			}
		}
	}
	labels := assistantAdminAvailableConfigLabels()
	keys := make([]string, 0)
	for _, candidate := range sortedAssistantAdminConfigKeys() {
		if (key == "" || candidate == key) && (query == "" || strings.Contains(strings.ToLower(candidate+" "+labels[candidate]), query)) {
			keys = append(keys, candidate)
		}
	}
	if key != "" && len(keys) == 0 {
		return map[string]any{"ok": false, "status": "not_found", "error": "This setting is not in the assistant's non-secret allowlist."}
	}
	total := len(keys)
	offset = min(offset, total)
	end := min(offset+limit, total)
	keys = keys[offset:end]
	current := assistantAdminCurrentOptions(keys)
	settings := make([]map[string]any, 0, len(keys))
	for _, name := range keys {
		value := []rune(current[name])
		start := 0
		size := 256
		if key != "" {
			start = min(valueOffset, len(value))
			size = valueLimit
		}
		stop := min(start+size, len(value))
		row := map[string]any{"key": name, "label": labels[name], "current_value": string(value[start:stop]), "value_offset": start, "value_length": len(value), "value_truncated": stop < len(value)}
		if stop < len(value) {
			row["next_value_offset"] = stop
		}
		settings = append(settings, row)
	}
	return map[string]any{"ok": true, "administrator_role": role, "configurable_settings": settings, "total": total, "offset": offset, "has_more": end < total, "next_offset": end, "sensitive_settings_omitted": true, "write_rule": "Read the full exact value before preparing a change. Use prepare_admin_config_change and wait for explicit browser confirmation."}
}
