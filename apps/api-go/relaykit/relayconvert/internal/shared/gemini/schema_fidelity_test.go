package gemini

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSchemaSelectionBoundaries(t *testing.T) {
	for _, typ := range []interface{}{"null", []interface{}{"null"}, []interface{}{"string", "number"}} {
		schema := map[string]interface{}{"type": "object", "properties": map[string]interface{}{"value": map[string]interface{}{"type": typ}}}
		cleaned, full := PreserveFunctionParameters(schema)
		require.Nil(t, cleaned)
		require.Equal(t, schema, full)
	}
	for _, typ := range []interface{}{"string", []interface{}{"object", "null"}} {
		cleaned, full := PreserveFunctionParameters(map[string]interface{}{"type": typ, "properties": map[string]interface{}{}})
		require.NotNil(t, cleaned)
		require.Nil(t, full)
	}
	cleaned, full := PreserveFunctionParameters(map[string]interface{}{"type": "object", "properties": map[string]interface{}{}})
	require.Nil(t, cleaned)
	require.Nil(t, full)
	data := map[string]interface{}{"const": "data", "oneOf": "data"}
	cleaned, full = PreserveFunctionParameters(map[string]interface{}{"type": "string", "default": data, "enum": []interface{}{data}})
	require.Nil(t, full)
	require.Equal(t, data, cleaned.(map[string]interface{})["default"])
	cycle := map[string]interface{}{"type": "array"}
	cycle["items"] = cycle
	cleaned, full = PreserveFunctionParameters(cycle)
	require.Nil(t, cleaned)
	_, err := json.Marshal(full)
	require.Error(t, err, "bounded inspection must not make a cyclic in-memory schema encodable")
}

func TestPreserveFunctionParametersKeepsUnsupportedConstraints(t *testing.T) {
	params := map[string]interface{}{
		"type": "object", "properties": map[string]interface{}{},
		"additionalProperties": false, "const": "locked",
	}
	cleaned, full := PreserveFunctionParameters(params)
	require.Nil(t, cleaned)
	require.Equal(t, params, full)
}

func TestPreserveFunctionParametersWalksTupleItems(t *testing.T) {
	params := map[string]interface{}{"type": "array", "items": []interface{}{
		map[string]interface{}{"type": "string"},
		map[string]interface{}{"type": "string", "const": "second"},
	}}
	cleaned, full := PreserveFunctionParameters(params)
	require.Nil(t, cleaned)
	require.Equal(t, params, full)
}

func TestPreserveFunctionParametersUsesCleanedSubsetWhenSafe(t *testing.T) {
	cleaned, full := PreserveFunctionParameters(map[string]interface{}{
		"type": "object", "properties": map[string]interface{}{
			"name": map[string]interface{}{"type": "string"},
		}, "required": []interface{}{"name"},
	})
	require.Nil(t, full)
	require.Equal(t, "OBJECT", cleaned.(map[string]interface{})["type"])
}
