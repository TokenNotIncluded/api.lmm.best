package common

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/setting/model_setting"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestRemoveDisabledFieldsKeepsToolAndStreamOptionNumbers(t *testing.T) {
	original := model_setting.GetGlobalSettings().PassThroughRequestEnabled
	model_setting.GetGlobalSettings().PassThroughRequestEnabled = false
	t.Cleanup(func() { model_setting.GetGlobalSettings().PassThroughRequestEnabled = original })

	tools := `[ {"type":"function","parameters":{"minimum":9007199254740993,"default":{"required":null,"n":9007199254740993}}} ]`
	input := []byte(`{"tools":` + tools + `,"service_tier":"flex","stream_options":{"include_obfuscation":false,"n":9007199254740993}}`)
	out, err := RemoveDisabledFields(input, dto.ChannelOtherSettings{}, false)
	require.NoError(t, err)
	var compactTools bytes.Buffer
	require.NoError(t, json.Compact(&compactTools, []byte(tools)))
	require.Equal(t, compactTools.String(), gjson.GetBytes(out, "tools").Raw)
	require.Equal(t, "9007199254740993", gjson.GetBytes(out, "stream_options.n").Raw)
	require.False(t, gjson.GetBytes(out, "service_tier").Exists())
	require.False(t, gjson.GetBytes(out, "stream_options.include_obfuscation").Exists())

	for _, streamOptions := range []string{`null`, `[]`, `"opaque"`} {
		input = []byte(`{"service_tier":"flex","stream_options":` + streamOptions + `}`)
		out, err = RemoveDisabledFields(input, dto.ChannelOtherSettings{}, false)
		require.NoError(t, err)
		require.Equal(t, streamOptions, gjson.GetBytes(out, "stream_options").Raw)
	}
}

func TestParamOverrideKeepsSchemaNumbers(t *testing.T) {
	input := []byte(`{"tools":[{"type":"function","parameters":{"minimum":9007199254740993,"default":{"required":null,"n":9007199254740993}}}],"prunable":{"type":"drop"}}`)
	for _, tt := range []struct {
		name, parametersPath string
		operation            map[string]any
	}{
		{"copy", "copied", map[string]any{"mode": "copy", "from": "tools.0.parameters", "to": "copied"}},
		{"move", "moved", map[string]any{"mode": "move", "from": "tools.0.parameters", "to": "moved"}},
		{"append", "tools.0.parameters", map[string]any{"mode": "append", "path": "tools", "value": map[string]any{"type": "custom"}}},
		{"prepend", "tools.1.parameters", map[string]any{"mode": "prepend", "path": "tools", "value": map[string]any{"type": "custom"}}},
		{"merge", "tools.0.parameters", map[string]any{"mode": "append", "path": "tools.0.parameters", "value": map[string]any{"description": "retained"}}},
		{"prune root", "tools.0.parameters", map[string]any{"mode": "prune_objects", "value": "drop"}},
		{"prune target", "tools.0.parameters", map[string]any{"mode": "prune_objects", "path": "tools", "value": "drop"}},
		{"sync", "copied", map[string]any{"mode": "sync_fields", "from": "json:tools.0.parameters", "to": "json:copied"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			out, err := ApplyParamOverride(input, map[string]any{"operations": []any{tt.operation}}, nil)
			require.NoError(t, err)
			require.Equal(t, "9007199254740993", gjson.GetBytes(out, tt.parametersPath+".minimum").Raw)
			require.Equal(t, "9007199254740993", gjson.GetBytes(out, tt.parametersPath+".default.n").Raw)
			require.Equal(t, "null", gjson.GetBytes(out, tt.parametersPath+".default.required").Raw)
			require.NotContains(t, string(out), "9007199254740992")
		})
	}
}
