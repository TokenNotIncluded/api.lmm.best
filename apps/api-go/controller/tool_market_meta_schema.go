package controller

import (
	"slices"

	"github.com/LIghtJUNction/api.lmm.best/common"
)

// One contract drives both the advertised schema and the raw-input decoder.
// Numeric values and authorization remain subject to server-side checks.
type toolMarketMetaAction struct {
	required []string
	optional []string
}

var toolMarketMetaActions = map[string]toolMarketMetaAction{
	"search":            {optional: []string{"query", "offset", "limit"}},
	"details":           {required: []string{"service_id"}},
	"status":            {},
	"load":              {required: []string{"tool_id", "version_id"}},
	"unload":            {required: []string{"tool_id", "version_id"}},
	"authorize":         {required: []string{"tool_id", "version_id", "max_price_quota", "max_total_quota", "max_calls", "expires_at"}},
	"set_tool_budget":   {required: []string{"tool_id", "version_id", "grant_id", "limit_quota"}},
	"set_client_budget": {required: []string{"limit_quota"}},
	"usage":             {optional: []string{"offset", "limit"}},
	"calls":             {optional: []string{"offset", "limit"}},
	"call_status":       {required: []string{"call_id"}},
	"invoke":            {required: []string{"tool_id", "version_id", "request_id", "arguments"}},
}

func toolMarketMetaDefaultLimit(action string) int {
	if action == "search" {
		return 5
	}
	return 20
}

func toolMarketMetaInputSchema() map[string]any {
	actions := make([]string, 0, len(toolMarketMetaActions))
	for action := range toolMarketMetaActions {
		actions = append(actions, action)
	}
	slices.Sort(actions)
	quota := map[string]any{"type": "integer", "minimum": 0, "maximum": common.MaxWalletQuota}
	schema := marketMCPSchema(map[string]any{
		"action":     map[string]any{"type": "string", "enum": actions},
		"query":      map[string]any{"type": "string", "maxLength": 120, "description": "Search service or tool names and descriptions; omit to browse."},
		"service_id": marketMCPString(), "tool_id": marketMCPString(), "version_id": marketMCPString(), "grant_id": marketMCPString(), "call_id": marketMCPString(),
		"request_id": marketMCPString(), "arguments": map[string]any{"type": "object", "additionalProperties": true, "description": "Required for invoke, including {} for a parameterless tool. Read details for the exact target schema."},
		"offset":          map[string]any{"type": "integer", "minimum": 0, "maximum": 10000, "default": 0},
		"limit":           map[string]any{"type": "integer", "minimum": 1, "maximum": 100, "description": "Search defaults to 5 services; usage and calls default to 20 records."},
		"max_price_quota": quota, "max_total_quota": quota, "limit_quota": quota,
		"max_calls":  map[string]any{"type": "integer", "minimum": 1, "maximum": 1000000},
		"expires_at": map[string]any{"type": "integer", "minimum": 1, "description": "Required for authorize. Unix time in seconds; must be in the future."},
	}, "action")
	branches := make([]any, 0, len(actions))
	for _, action := range actions {
		spec := toolMarketMetaActions[action]
		// Types are shared above. Each closed branch permits only that action's
		// fields, without repeating target schemas or weakening required fields.
		properties := map[string]any{"action": map[string]any{"const": action}}
		for _, field := range spec.required {
			properties[field] = map[string]any{}
		}
		for _, field := range spec.optional {
			properties[field] = map[string]any{}
			if field == "limit" {
				properties[field] = map[string]any{"default": toolMarketMetaDefaultLimit(action)}
			}
		}
		branch := marketMCPSchema(properties, append([]string{"action"}, spec.required...)...)
		branch["title"] = action
		branches = append(branches, branch)
	}
	schema["oneOf"] = branches
	return schema
}
