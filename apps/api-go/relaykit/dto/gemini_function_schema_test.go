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
