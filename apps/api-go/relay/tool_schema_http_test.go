package relay

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/relay/helper"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/relayconvert"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/relayconvert/convmeta"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestHTTPToolSchemaNormalizationPreservesNumbersAndRetryBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previous := common.GetDiskCacheConfig()
	t.Cleanup(func() { common.SetDiskCacheConfig(previous) })

	const schema = `{"type":"object","required":null,"properties":{"id":{"type":"integer","minimum":9007199254740993}},"default":{"required":null,"id":9007199254740993}}`
	cases := []struct {
		name, path, body string
		format           types.RelayFormat
	}{
		{"chat", "/v1/chat/completions", `{"model":"test","messages":[{"role":"user","content":"hello"}],"tools":[{"type":"function","function":{"name":"lookup","parameters":` + schema + `}}]}`, types.RelayFormatOpenAI},
		{"claude", "/v1/messages", `{"model":"test","max_tokens":50,"messages":[{"role":"user","content":"hello"}],"tools":[{"name":"lookup","input_schema":` + schema + `}]}`, types.RelayFormatClaude},
		{"responses", "/v1/responses", `{"model":"test","input":"hello","tools":[{"type":"function","name":"lookup","parameters":` + schema + `}]}`, types.RelayFormatOpenAIResponses},
		{"gemini", "/v1beta/models/test:generateContent", `{"contents":[{"role":"user","parts":[{"text":"hello"}]}],"tools":[{"functionDeclarations":[{"name":"lookup","parameters":` + schema + `}]}]}`, types.RelayFormatGemini},
		{"gemini-json-schema", "/v1beta/models/test:generateContent", `{"contents":[{"role":"user","parts":[{"text":"hello"}]}],"tools":[{"functionDeclarations":[{"name":"lookup","parametersJsonSchema":` + schema + `}]}]}`, types.RelayFormatGemini},
	}
	for _, disk := range []bool{false, true} {
		storageName := map[bool]string{false: "memory", true: "disk"}[disk]
		t.Run(storageName, func(t *testing.T) {
			common.SetDiskCacheConfig(common.DiskCacheConfig{Enabled: disk, ThresholdMB: 1, MaxSizeMB: 1024, Path: t.TempDir()})
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					c, _ := gin.CreateTestContext(httptest.NewRecorder())
					c.Request = httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
					c.Request.Header.Set("Content-Type", "application/json")
					if disk {
						// Unknown-length bodies use the same disk decoder as large requests.
						c.Request.ContentLength = -1
					}
					defer common.CleanupBodyStorage(c)
					request, err := helper.GetAndValidateRequest(c, tc.format)
					require.NoError(t, err)
					storage, err := common.GetBodyStorage(c)
					require.NoError(t, err)
					require.Equal(t, disk, storage.IsDisk())
					before, err := json.Marshal(request)
					require.NoError(t, err)
					copy := copyHTTPToolSchemaRequest(t, request)
					converted, err := relayconvert.ConvertRequest(context.Background(), &convmeta.Values{}, types.RelayFormatOpenAI, copy)
					require.NoError(t, err)
					wire, err := json.Marshal(converted.Value)
					require.NoError(t, err)
					require.Equal(t, 2, strings.Count(string(wire), "9007199254740993"), string(wire))
					require.NotContains(t, string(wire), "9007199254740992")
					require.Equal(t, 1, strings.Count(string(wire), `"required":null`), "only opaque default data may retain null required: %s", wire)
					after, err := json.Marshal(request)
					require.NoError(t, err)
					require.Equal(t, before, after, "decoded request must survive retries")
					replayed, err := storage.Bytes()
					require.NoError(t, err)
					require.Equal(t, tc.body, string(replayed), "passthrough must replay the exact original bytes")
				})
			}
		})
	}
}

func copyHTTPToolSchemaRequest(t *testing.T, request dto.Request) dto.Request {
	t.Helper()
	var copy dto.Request
	var err error
	switch r := request.(type) {
	case *dto.GeneralOpenAIRequest:
		copy, err = copyMutableRequestForRelay(r, false)
	case *dto.ClaudeRequest:
		copy, err = copyMutableRequestForRelay(r, false)
	case *dto.OpenAIResponsesRequest:
		copy, err = copyMutableRequestForRelay(r, false)
	case *dto.GeminiChatRequest:
		copy, err = copyMutableRequestForRelay(r, false)
	default:
		t.Fatalf("unexpected request type %T", request)
	}
	require.NoError(t, err)
	return copy
}
