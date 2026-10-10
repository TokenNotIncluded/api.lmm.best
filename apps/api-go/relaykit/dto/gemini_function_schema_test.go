package dto

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGeminiSchemaFieldDoesNotChangeOpenAIToolWire(t *testing.T) {
	raw := []byte(`{"name":"lookup","parameters":{"type":"object"},"parametersJsonSchema":{"const":1}}`)
	var function FunctionRequest
	require.NoError(t, json.Unmarshal(raw, &function))
	wire, err := json.Marshal(function)
	require.NoError(t, err)
	require.NotContains(t, string(wire), "parametersJsonSchema")
	declaration := GeminiFunctionDeclaration{FunctionRequest: FunctionRequest{Name: "lookup"}, ParametersJsonSchema: false}
	wire, err = json.Marshal(declaration)
	require.NoError(t, err)
	require.JSONEq(t, `{"name":"lookup","parametersJsonSchema":false}`, string(wire))
}

func TestGeminiFunctionDeclarationDecodesBothSchemaFields(t *testing.T) {
	raw := []byte(`{"name":"lookup","description":"Lookup an ID","strict":true,"arguments":"{}","parameters":{"minimum":9007199254740993},"parametersJsonSchema":{"const":9007199254740993}}`)
	var declaration GeminiFunctionDeclaration
	require.NoError(t, json.Unmarshal(raw, &declaration))
	encoded, err := json.Marshal(declaration)
	require.NoError(t, err)
	require.JSONEq(t, string(raw), string(encoded))
	parameters := declaration.Parameters.(map[string]any)
	require.Equal(t, json.Number("9007199254740993"), parameters["minimum"])
	full := declaration.ParametersJsonSchema.(map[string]any)
	require.Equal(t, json.Number("9007199254740993"), full["const"])
}
