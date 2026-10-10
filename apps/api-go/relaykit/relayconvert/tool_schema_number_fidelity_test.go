package relayconvert

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/relayconvert/convmeta"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestResponsesConvertedToolSchemaNumberFidelity(t *testing.T) {
	for _, fullSchema := range []bool{false, true} {
		for _, target := range []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatClaude, types.RelayFormatOpenAIResponses, types.RelayFormatGemini} {
			name := string(target) + "/subset"
			if fullSchema {
				name = string(target) + "/full"
			}
			t.Run(name, func(t *testing.T) {
				extra := ""
				largeNumber := "1e40"
				if fullSchema {
					extra = `,"additionalProperties":false,"$defs":{"nested":{"required":null,"minimum":9007199254740993}}`
					largeNumber = "1e400"
				}
				schema := `{"type":"object","required":null,"properties":{"path":{"type":"integer","minimum":9007199254740993}},"default":{"required":null,"integer":-9007199254740993,"fraction":0.12345678901234567890123456789,"large":` + largeNumber + `}` + extra + `}`
				// Decode the actual wire shape before normalization. Hand-built
				// json.Number values would bypass the tool decoder regressions.
				wire := `{"model":"test-model","input":"hello","max_output_tokens":100,"tools":[{"type":"function","name":"lookup","parameters":` + schema + `},{"type":"custom","name":"custom","format":{"type":"text"}}]}`
				var request dto.OpenAIResponsesRequest
				require.NoError(t, json.Unmarshal([]byte(wire), &request))
				require.NoError(t, SanitizeToolSchemas(&request))
				result, err := ConvertRequest(context.Background(), &convmeta.Values{UpstreamModelName: "test-model"}, target, &request)
				require.NoError(t, err)
				encoded, err := json.Marshal(result.Value)
				require.NoError(t, err)

				path := "tools.0.parameters"
				switch target {
				case types.RelayFormatOpenAI:
					path = "tools.0.function.parameters"
				case types.RelayFormatClaude:
					path = "tools.0.input_schema"
				case types.RelayFormatGemini:
					path = "tools.0.functionDeclarations.0.parameters"
					if fullSchema {
						path = "tools.0.functionDeclarations.0.parametersJsonSchema"
					}
				}
				providerSchema := gjson.GetBytes(encoded, path)
				require.True(t, providerSchema.IsObject(), "%s", encoded)
				for _, number := range []struct{ key, want string }{
					{"properties.path.minimum", "9007199254740993"},
					{"default.integer", "-9007199254740993"},
					{"default.fraction", "0.12345678901234567890123456789"},
					{"default.large", largeNumber},
					{"default.required", "null"},
				} {
					require.Equal(t, number.want, providerSchema.Get(number.key).Raw, "%s: %s", number.key, encoded)
				}
				require.False(t, providerSchema.Get("required").Exists(), "%s", encoded)
				if fullSchema {
					require.Equal(t, "9007199254740993", providerSchema.Get("$defs.nested.minimum").Raw)
					require.False(t, providerSchema.Get("$defs.nested.required").Exists())
				}
			})
		}
	}
}
