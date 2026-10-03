package service

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func TestToolMarketSecretGuardCoversToolMetadataAndEveryContentKind(t *testing.T) {
	const secret = "publisher-fixture-secret-987"
	reflections := map[string]any{
		"tool-description":  &mcp.Tool{Name: "lookup", Description: "Authorization: Bearer " + secret, InputSchema: map[string]any{"type": "object"}},
		"tool-name":         &mcp.Tool{Name: secret, InputSchema: map[string]any{"type": "object"}},
		"tool-title":        &mcp.Tool{Name: "lookup", Title: secret, InputSchema: map[string]any{"type": "object"}},
		"tool-meta":         &mcp.Tool{Name: "lookup", Meta: mcp.Meta{"diagnostic": secret}, InputSchema: map[string]any{"type": "object"}},
		"tool-input":        &mcp.Tool{Name: "lookup", InputSchema: map[string]any{"type": "object", "properties": map[string]any{secret: map[string]any{"type": "string"}}}},
		"tool-output":       &mcp.Tool{Name: "lookup", InputSchema: map[string]any{"type": "object"}, OutputSchema: map[string]any{"type": "object", "description": secret}},
		"result-meta":       &mcp.CallToolResult{Meta: mcp.Meta{"http": map[string]any{"X-API-Key": secret}}},
		"result-structured": &mcp.CallToolResult{StructuredContent: map[string]any{"headers": []any{map[string]any{"Authorization": "Bearer " + secret}}}},
		"result-text":       &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: secret}}},
		"result-text-meta":  &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "answer", Meta: mcp.Meta{"debug": secret}}}},
		"result-image":      &mcp.CallToolResult{Content: []mcp.Content{&mcp.ImageContent{Data: []byte("x" + secret + " image bytes"), MIMEType: "image/png"}}},
		"result-audio":      &mcp.CallToolResult{Content: []mcp.Content{&mcp.AudioContent{Data: []byte("xy" + secret + " audio bytes"), MIMEType: "audio/wav"}}},
		"result-link":       &mcp.CallToolResult{Content: []mcp.Content{&mcp.ResourceLink{URI: "https://example.com/resource", Description: secret}}},
		"result-resource":   &mcp.CallToolResult{Content: []mcp.Content{&mcp.EmbeddedResource{Resource: &mcp.ResourceContents{URI: "https://example.com/resource", Text: secret}}}},
	}
	for _, mode := range []string{"bearer", "api_key"} {
		for name, value := range reflections {
			t.Run(mode+"/"+name, func(t *testing.T) {
				require.False(t, marketRemoteSecretSafe(value, &model.ToolMarketResolvedCredential{Mode: mode, Secret: secret}))
			})
		}
	}
}

func TestToolMarketSecretGuardDetectsEncodedAndMixedEscapeReflection(t *testing.T) {
	const secret = "provider-credential-☃-🧪-à"
	credential := &model.ToolMarketResolvedCredential{Mode: "bearer", Secret: secret}
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		for _, text := range []string{encoding.EncodeToString([]byte(secret)), encoding.EncodeToString([]byte("x" + secret + "suffix")), encoding.EncodeToString([]byte("xy" + secret + "suffix"))} {
			require.False(t, marketRemoteSecretSafe(map[string]any{"encoded": text}, credential))
		}
	}
	credential.Secret = "abcd"
	for _, text := range []string{`{"key":"abc\u0064"}`, `API key: abc\u0064`, `API key: ab\u0063\u0064`, `prefix "quoted" abc\u0064 \d`, `prefix \"quoted\" abc\u0064`, `{"key":"abc\\u0064"}`} {
		require.False(t, marketRemoteSecretSafe(&mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, credential), text)
	}
	require.False(t, marketRemoteSecretSafe(map[string]any{"encoded": base64.StdEncoding.EncodeToString([]byte(`{"key":"abc\u0064"}`))}, credential))
	require.False(t, marketRemoteSecretSafe(map[string]any{`ab\u0063d`: "value"}, credential))
	credential.Secret = "🔑key"
	require.False(t, marketRemoteSecretSafe(map[string]any{"text": `API key: \ud83d\udd11k\u0065y`}, credential))
	credential.Secret = "9007199254740993"
	require.False(t, marketRemoteSecretSafe(json.RawMessage(`{"example":9007199254740993}`), credential))
	credential.Secret = "abcd"
	require.False(t, marketRemoteSecretSafe(json.RawMessage(`{"abc\u0064":"value"}`), credential))
}

func TestToolMarketSecretGuardNestedEncodingDepthAndResourceLimits(t *testing.T) {
	credential := &model.ToolMarketResolvedCredential{Mode: "api_key", Secret: "publisher-fixture-secret-987"}
	var encoded any = map[string]any{"key": credential.Secret}
	for index := 0; index < 8; index++ {
		data, err := json.Marshal(encoded)
		require.NoError(t, err)
		encoded = string(data)
	}
	require.False(t, marketRemoteSecretSafe(encoded, credential))
	deep := any("ordinary")
	for index := 0; index < marketSecretGuardMaxDepth+2; index++ {
		deep = []any{deep}
	}
	require.False(t, marketRemoteSecretSafe(deep, credential))
	require.False(t, marketRemoteSecretSafe(map[string]any{"text": strings.Repeat("z", marketSecretGuardMaxBytes+1)}, credential))
	// The shared budget includes the serialized value and decoded strings, not
	// merely individual fields or each separately parsed JSON/encoding layer.
	require.False(t, marketRemoteSecretSafe(map[string]any{"text": strings.Repeat("z", marketSecretGuardMaxBytes*3/4)}, credential))
	require.False(t, marketRemoteSecretSafe(make(chan int), credential))
	cycle := map[string]any{}
	cycle["self"] = cycle
	require.False(t, marketRemoteSecretSafe(cycle, credential))
	credential.Secret = "a"
	require.False(t, marketRemoteSecretSafe(map[string]any{"value": "ordinary data"}, credential))
}

func TestToolMarketSecretGuardAllowsOrdinaryResultsAndAnonymousRemotes(t *testing.T) {
	credential := &model.ToolMarketResolvedCredential{Mode: "bearer", Secret: "publisher-fixture-secret-987"}
	for _, value := range []any{
		&mcp.Tool{Name: "lookup", Description: "Read a public record", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}}}},
		&mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Delivered a useful answer."}}, StructuredContent: map[string]any{"answer": 123, "success": true}},
		map[string]any{"text": `regular expression: \d+; JSON: {"answer": "safe"}; path C:\work\file`},
		map[string]any{"text": "first line\nsecond line", "json": `{"answer":"plain"}`},
		&mcp.CallToolResult{Content: []mcp.Content{&mcp.ImageContent{Data: []byte("ordinary image bytes"), MIMEType: "image/png"}}},
	} {
		require.True(t, marketRemoteSecretSafe(value, credential))
	}
	require.True(t, marketRemoteSecretSafe(map[string]any{"text": credential.Secret}, nil))
	require.True(t, marketRemoteSecretSafe(make(chan int), &model.ToolMarketResolvedCredential{Mode: "none"}))
}
