package relayconvert

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/relayconvert/convmeta"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/stretchr/testify/require"
)

func TestToolSchemaKeywordsAndOpaqueData(t *testing.T) {
	schema := map[string]any{"required": nil, "minimum": json.Number("9007199254740993")}
	for _, key := range []string{"default", "const", "x-vendor"} {
		schema[key] = map[string]any{"required": nil}
	}
	for _, key := range []string{"examples", "enum"} {
		schema[key] = []any{map[string]any{"required": nil}}
	}
	singles := []string{"additionalItems", "additionalProperties", "contains", "contentSchema", "else", "if", "not", "propertyNames", "then", "unevaluatedItems", "unevaluatedProperties"}
	arrays := []string{"allOf", "anyOf", "items", "oneOf", "prefixItems"}
	maps := []string{"$defs", "definitions", "dependentSchemas", "dependencies", "patternProperties", "properties"}
	for _, key := range singles {
		schema[key] = map[string]any{"required": nil}
	}
	for _, key := range arrays {
		schema[key] = []any{map[string]any{"required": nil}}
	}
	for _, key := range maps {
		schema[key] = map[string]any{"x": map[string]any{"required": nil}, "valid": map[string]any{"required": []string{"path"}}, "empty": map[string]any{"required": []string{}}}
	}
	cleaned, err := sanitizeSchemaValue(schema)
	require.NoError(t, err)
	wire, err := json.Marshal(cleaned)
	require.NoError(t, err)
	var result map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(wire, &result))
	require.NotContains(t, result, "required")
	require.Equal(t, "9007199254740993", string(result["minimum"]))
	for _, key := range []string{"default", "const", "x-vendor"} {
		require.JSONEq(t, `{"required":null}`, string(result[key]))
	}
	for _, key := range []string{"examples", "enum"} {
		require.JSONEq(t, `[{"required":null}]`, string(result[key]))
	}
	for _, key := range singles {
		require.JSONEq(t, `{}`, string(result[key]))
	}
	for _, key := range arrays {
		require.JSONEq(t, `[{}]`, string(result[key]))
	}
	for _, key := range maps {
		require.JSONEq(t, `{"x":{},"valid":{"required":["path"]},"empty":{"required":[]}}`, string(result[key]))
	}
	require.Contains(t, schema, "required", "must not mutate caller")
	value, err := sanitizeSchemaValue(json.RawMessage(`{"items":{"required":null},"dependencies":{"a":["b","c"],"b":{"required":null}}}`))
	require.NoError(t, err)
	wire, err = json.Marshal(value)
	require.NoError(t, err)
	require.JSONEq(t, `{"items":{},"dependencies":{"a":["b","c"],"b":{}}}`, string(wire))
}

func TestToolSchemaDepthAndNonSchemas(t *testing.T) {
	for _, value := range []any{nil, true, "schema", 42, json.RawMessage(`[]`), json.RawMessage(`{"required":[],"default":{"required":null}}`)} {
		cleaned, err := sanitizeSchemaValue(value)
		require.NoError(t, err)
		require.Equal(t, value, cleaned)
	}
	atLimit := strings.Repeat(`{"items":`, 63) + `{"required":null}` + strings.Repeat(`}`, 63)
	_, err := sanitizeSchemaValue(json.RawMessage(atLimit))
	require.NoError(t, err)
	_, err = sanitizeSchemaValue(json.RawMessage(`{"items":` + atLimit + `}`))
	require.ErrorContains(t, err, "maximum depth")
	_, err = sanitizeSchemaValue(json.RawMessage(`{"required":`))
	require.Error(t, err)
}

func TestToolSchemaProtocolsAndConversion(t *testing.T) {
	for _, protocol := range []string{"chat", "claude", "responses", "gemini", "gemini-single"} {
		t.Run(protocol, func(t *testing.T) {
			schema := `{"type":"object","required":null,"properties":{"path":{"type":"string","required":null}}}`
			var req any
			switch protocol {
			case "chat":
				req = &dto.GeneralOpenAIRequest{Model: "test", Tools: []dto.ToolCallRequest{{Type: "function", Function: dto.FunctionRequest{Name: "lookup", Parameters: json.RawMessage(schema)}}}, Functions: json.RawMessage(`[{"name":"legacy","parameters":` + schema + `}]`), Messages: []dto.Message{{Role: "user", Content: map[string]any{"required": nil}}}}
			case "claude":
				req = &dto.ClaudeRequest{Model: "test", Tools: []any{map[string]any{"name": "lookup", "input_schema": json.RawMessage(schema)}}}
			case "responses":
				req = &dto.OpenAIResponsesRequest{Model: "test", Input: json.RawMessage(`"hi"`), Tools: json.RawMessage(`[{"type":"function","name":"lookup","parameters":` + schema + `},{"type":"custom","parameters":{"required":null}}]`)}
			default:
				tools := `{"functionDeclarations":[{"name":"lookup","parametersJsonSchema":` + schema + `,"parameters":` + schema + `}]}`
				if protocol == "gemini" {
					tools = "[" + tools + "]"
				}
				req = &dto.GeminiChatRequest{Tools: json.RawMessage(tools)}
			}
			require.NoError(t, SanitizeToolSchemas(req))
			wire, err := json.Marshal(req)
			require.NoError(t, err)
			remaining := 0
			if protocol == "chat" || protocol == "responses" {
				remaining = 1
			}
			require.Equal(t, remaining, strings.Count(string(wire), `"required":null`))
			if protocol == "chat" {
				require.Contains(t, string(wire), `"required":null`)
			}
			if protocol == "responses" {
				require.Contains(t, string(wire), `"parameters":{"required":null}`)
			}
			if protocol == "claude" {
				converted, err := ConvertRequest(context.Background(), &convmeta.Values{}, types.RelayFormatOpenAI, req)
				require.NoError(t, err)
				chat := converted.Value.(*dto.GeneralOpenAIRequest)
				require.Len(t, chat.Tools, 1)
				data, err := json.Marshal(chat.Tools[0].Function.Parameters)
				require.NoError(t, err)
				require.NotContains(t, string(data), `"required"`)
			}
		})
	}
}

func TestClaudeTypedToolSchema(t *testing.T) {
	for _, pointer := range []bool{false, true} {
		tool := dto.Tool{Name: "lookup", InputSchema: map[string]any{"required": nil}}
		var value any = tool
		if pointer {
			value = &tool
		}
		req := &dto.ClaudeRequest{Tools: []any{value}}
		require.NoError(t, SanitizeToolSchemas(req))
		wire, err := json.Marshal(req)
		require.NoError(t, err)
		require.NotContains(t, string(wire), `"required"`)
	}
}

func TestResponsesNamespaceSchemasAndUnchangedBytes(t *testing.T) {
	raw := json.RawMessage(`[{"type":"namespace","name":"inventory","tools":[{"type":"function","name":"lookup","parameters":{"required":null}}]},{"type":"custom","parameters":{"required":null}}]`)
	cleaned, err := sanitizeToolList(raw, "responses")
	require.NoError(t, err)
	require.JSONEq(t, `[{"type":"namespace","name":"inventory","tools":[{"type":"function","name":"lookup","parameters":{}}]},{"type":"custom","parameters":{"required":null}}]`, string(cleaned))
	unchanged := json.RawMessage("[ {\"type\":\"function\",\"parameters\":{\"required\":[]} } ]")
	cleaned, err = sanitizeToolList(unchanged, "responses")
	require.NoError(t, err)
	require.Equal(t, unchanged, cleaned)
	tooDeep := strings.Repeat(`[{"type":"namespace","tools":`, 64) + `[]` + strings.Repeat(`}]`, 64)
	_, err = sanitizeToolList(json.RawMessage(tooDeep), "responses")
	require.ErrorContains(t, err, "maximum depth")
}

func TestChatMessageToolSchemas(t *testing.T) {
	req := &dto.GeneralOpenAIRequest{Messages: []dto.Message{{
		Role: "tool", Content: map[string]any{"required": nil},
		Tools: json.RawMessage(`[{"type":"function","function":{"name":"lookup","parameters":{"required":null,"minimum":9007199254740993,"default":{"required":null}}}},{"type":"custom","custom":{"required":null}}]`),
	}}}
	require.NoError(t, SanitizeToolSchemas(req))
	require.Equal(t, 2, strings.Count(string(req.Messages[0].Tools), `"required":null`))
	require.Contains(t, string(req.Messages[0].Tools), "9007199254740993")
	require.Contains(t, string(req.Messages[0].Tools), `"default":{"required":null}`)
	require.Contains(t, string(req.Messages[0].Tools), `"custom":{"required":null}`)
	require.Contains(t, req.Messages[0].Content, "required")
}
