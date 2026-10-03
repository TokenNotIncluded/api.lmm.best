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

func TestDecodedToolSchemaNumberFidelityAcrossProtocols(t *testing.T) {
	const schema = `{"type":"object","required":null,"properties":{"id":{"const":9007199254740993}},"default":{"required":null,"fraction":0.12345678901234567890123456789,"large":1e400}}`
	for _, source := range []string{"chat", "claude", "gemini", "gemini-json-schema"} {
		for _, target := range []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatClaude, types.RelayFormatOpenAIResponses, types.RelayFormatGemini} {
			t.Run(source+"/"+string(target), func(t *testing.T) {
				var request any
				var wire string
				switch source {
				case "chat":
					request = &dto.GeneralOpenAIRequest{}
					wire = `{"model":"test","messages":[{"role":"user","content":"hello"}],"tools":[{"type":"function","function":{"name":"lookup","parameters":` + schema + `}}]}`
				case "claude":
					request = &dto.ClaudeRequest{}
					wire = `{"model":"test","max_tokens":50,"messages":[{"role":"user","content":"hello"}],"tools":[{"name":"lookup","input_schema":` + schema + `}]}`
				default:
					request = &dto.GeminiChatRequest{}
					field := "parameters"
					if source == "gemini-json-schema" {
						field = "parametersJsonSchema"
					}
					wire = `{"contents":[{"role":"user","parts":[{"text":"hello"}]}],"tools":[{"functionDeclarations":[{"name":"lookup","` + field + `":` + schema + `}]}]}`
				}
				require.NoError(t, json.Unmarshal([]byte(wire), request))
				require.NoError(t, SanitizeToolSchemas(request))
				meta := &convmeta.Values{
					UpstreamModelName: "test", ChannelMetaAttached: true,
					Options: &convmeta.Options{Claude: convmeta.ClaudeOptions{
						DefaultMaxTokens: func(string) int { return 50 },
					}},
				}
				result, err := ConvertRequest(context.Background(), meta, target, request)
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
					path = "tools.0.functionDeclarations.0.parametersJsonSchema"
					if source == "gemini" {
						path = "tools.0.functionDeclarations.0.parameters"
					}
				}
				providerSchema := gjson.GetBytes(encoded, path)
				require.True(t, providerSchema.IsObject(), "%s", encoded)
				require.Equal(t, "9007199254740993", providerSchema.Get("properties.id.const").Raw)
				require.Equal(t, "0.12345678901234567890123456789", providerSchema.Get("default.fraction").Raw)
				require.Equal(t, "1e400", providerSchema.Get("default.large").Raw)
				require.Equal(t, "null", providerSchema.Get("default.required").Raw)
				require.False(t, providerSchema.Get("required").Exists())
			})
		}
	}
}
