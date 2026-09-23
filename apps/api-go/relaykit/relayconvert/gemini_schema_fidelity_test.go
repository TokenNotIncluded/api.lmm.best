package relayconvert

import (
	"encoding/json"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/relayconvert/convmeta"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/stretchr/testify/require"
)

// Assert serialized provider fields through the registered request converters,
// so these regressions also compile against DTOs without parametersJsonSchema.
func TestGeminiRegisteredToolSchemaFidelity(t *testing.T) {
	deep := any(map[string]any{"type": "string"})
	for i := 0; i < 70; i++ {
		deep = map[string]any{"type": "array", "items": deep}
	}
	cases := []struct {
		name   string
		schema any
		full   bool
		want   any
	}{
		{"const", map[string]any{"type": "object", "properties": map[string]any{"mode": map[string]any{"const": "fixed"}}}, true, nil},
		{"tuple_safe_entries", map[string]any{"type": "object", "properties": map[string]any{"pair": map[string]any{"type": "array", "items": []any{map[string]any{"type": "string"}, map[string]any{"type": "integer"}}}}}, true, nil},
		{"constrained_empty_object", map[string]any{"type": "object", "properties": map[string]any{}, "minProperties": 1}, false, map[string]any{"type": "OBJECT", "properties": map[string]any{}, "minProperties": 1}},
		{"closed_empty_object", map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}, true, nil},
		{"deep_safe_subtree", map[string]any{"type": "object", "properties": map[string]any{"nested": deep}}, true, nil},
		{"safe_nullable", map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": []any{"string", "null"}, "description": "Name"}}, "required": []any{"name"}}, false, map[string]any{"type": "OBJECT", "properties": map[string]any{"name": map[string]any{"type": "STRING", "nullable": true, "description": "Name"}}, "required": []any{"name"}}},
	}
	for _, source := range []string{"chat", "responses", "claude"} {
		for _, tc := range cases {
			t.Run(source+"/"+tc.name, func(t *testing.T) {
				var request any
				var wire map[string]any
				switch source {
				case "chat":
					request = &dto.GeneralOpenAIRequest{}
					wire = map[string]any{"model": "gemini-test", "messages": []any{map[string]any{"role": "user", "content": "test"}}, "tools": []any{map[string]any{"type": "function", "function": map[string]any{"name": "lookup", "parameters": tc.schema}}}}
				case "responses":
					request = &dto.OpenAIResponsesRequest{}
					wire = map[string]any{"model": "gemini-test", "input": "test", "tools": []any{map[string]any{"type": "function", "name": "lookup", "parameters": tc.schema}}}
				case "claude":
					request = &dto.ClaudeRequest{}
					wire = map[string]any{"model": "gemini-test", "max_tokens": 100, "messages": []any{map[string]any{"role": "user", "content": "test"}}, "tools": []any{map[string]any{"name": "lookup", "input_schema": tc.schema}}}
				}
				encoded, err := json.Marshal(wire)
				require.NoError(t, err)
				require.NoError(t, json.Unmarshal(encoded, request))
				result, err := ConvertRequest(nil, &convmeta.Values{}, types.RelayFormatGemini, request)
				require.NoError(t, err)
				encoded, err = json.Marshal(result.Value)
				require.NoError(t, err)
				var provider struct {
					Tools []struct {
						Functions []map[string]json.RawMessage `json:"functionDeclarations"`
					} `json:"tools"`
				}
				require.NoError(t, json.Unmarshal(encoded, &provider))
				require.Len(t, provider.Tools, 1)
				require.Len(t, provider.Tools[0].Functions, 1)
				declaration := provider.Tools[0].Functions[0]
				_, hasSubset := declaration["parameters"]
				_, hasFull := declaration["parametersJsonSchema"]
				require.NotEqual(t, hasSubset, hasFull, "exactly one schema dialect must be sent: %s", encoded)
				field, expected := "parameters", tc.want
				if tc.full {
					field, expected = "parametersJsonSchema", tc.schema
				}
				require.Contains(t, declaration, field)
				wantJSON, err := json.Marshal(expected)
				require.NoError(t, err)
				require.JSONEq(t, string(wantJSON), string(declaration[field]))
			})
		}
	}
}
