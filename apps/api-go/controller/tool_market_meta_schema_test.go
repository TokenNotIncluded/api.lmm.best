package controller

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/stretchr/testify/require"
)

func TestToolMarketMetaSchemaActionContracts(t *testing.T) {
	encoded, err := json.Marshal(toolMarketMetaInputSchema())
	require.NoError(t, err)
	var schema jsonschema.Schema
	require.NoError(t, json.Unmarshal(encoded, &schema))
	resolved, err := schema.Resolve(nil)
	require.NoError(t, err)
	// This table is independent of the implementation's action registry.
	cases := []struct {
		input    map[string]any
		optional []string
	}{
		{map[string]any{"action": "search"}, []string{"query", "offset", "limit"}},
		{map[string]any{"action": "details", "service_id": "service"}, nil},
		{map[string]any{"action": "status"}, nil},
		{map[string]any{"action": "load", "tool_id": "tool", "version_id": "version"}, nil},
		{map[string]any{"action": "unload", "tool_id": "tool", "version_id": "version"}, nil},
		{map[string]any{"action": "authorize", "tool_id": "tool", "version_id": "version", "max_price_quota": 0, "max_total_quota": 0, "max_calls": 1, "expires_at": common.GetTimestamp() + 3600}, nil},
		{map[string]any{"action": "set_tool_budget", "tool_id": "tool", "version_id": "version", "grant_id": "grant", "limit_quota": 0}, nil},
		{map[string]any{"action": "set_client_budget", "limit_quota": 0}, nil},
		{map[string]any{"action": "usage"}, []string{"offset", "limit"}},
		{map[string]any{"action": "calls"}, []string{"offset", "limit"}},
		{map[string]any{"action": "call_status", "call_id": "call"}, nil},
		{map[string]any{"action": "invoke", "tool_id": "tool", "version_id": "version", "request_id": "request", "arguments": map[string]any{}}, nil},
	}
	require.Len(t, schema.OneOf, len(cases))
	validFields := map[string]any{"query": "lookup", "offset": 0, "limit": 1}
	for _, tc := range cases {
		for key, value := range tc.input {
			validFields[key] = value
		}
	}
	check := func(t *testing.T, value map[string]any, valid bool) {
		t.Helper()
		raw, err := json.Marshal(value)
		require.NoError(t, err)
		var wire any
		require.NoError(t, json.Unmarshal(raw, &wire))
		_, decodeErr := decodeToolMarketMetaInput(raw)
		if valid {
			require.NoError(t, resolved.Validate(wire))
			require.NoError(t, decodeErr)
		} else {
			require.Error(t, resolved.Validate(wire), string(raw))
			require.ErrorIs(t, decodeErr, model.ErrToolMarketInput, string(raw))
		}
	}
	for _, tc := range cases {
		t.Run(tc.input["action"].(string), func(t *testing.T) {
			check(t, tc.input, true)
			for required := range tc.input {
				missing := maps.Clone(tc.input)
				delete(missing, required)
				check(t, missing, false)
			}
			for field := range schema.Properties {
				if _, required := tc.input[field]; required || slices.Contains(tc.optional, field) {
					continue
				}
				crossAction := maps.Clone(tc.input)
				value, exists := validFields[field]
				require.True(t, exists, "add a valid fixture for schema property %s", field)
				crossAction[field] = value
				check(t, crossAction, false)
			}
			unknown := maps.Clone(tc.input)
			unknown["client_id"] = "another-client"
			check(t, unknown, false)
		})
	}
	check(t, map[string]any{"action": "invoke", "tool_id": "tool", "version_id": "version", "request_id": "request", "arguments": nil}, false)
	check(t, map[string]any{"action": "search", "query": strings.Repeat("用", 120), "limit": 100, "offset": 10000}, true)
	check(t, map[string]any{"action": "search", "query": strings.Repeat("用", 121)}, false)
	check(t, map[string]any{"action": "search", "limit": 0}, false)
	check(t, map[string]any{"action": "calls", "limit": 101}, false)
	check(t, map[string]any{"action": "unknown"}, false)
	second, err := json.Marshal(toolMarketMetaInputSchema())
	require.NoError(t, err)
	require.Equal(t, string(encoded), string(second), "tools/list schema must be deterministic")
}

func TestToolMarketMetaPaginationDefaultsMatchSchema(t *testing.T) {
	for action, expected := range map[string]int{"search": 5, "usage": 20, "calls": 20} {
		input, err := decodeToolMarketMetaInput(json.RawMessage(`{"action":"` + action + `"}`))
		require.NoError(t, err)
		require.Equal(t, expected, input.Limit)
		found := false
		for _, raw := range toolMarketMetaInputSchema()["oneOf"].([]any) {
			branch := raw.(map[string]any)
			if branch["title"] == action {
				properties := branch["properties"].(map[string]any)
				require.Equal(t, expected, properties["limit"].(map[string]any)["default"])
				found = true
			}
		}
		require.True(t, found)
	}
}
