package controller

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAssistantOperationRedactsArbitraryProviderAuthenticationContainers(t *testing.T) {
	value := map[string]any{
		"header_override": `{"X-Provider-Cred":"opaque-provider-value"}`,
		"headers":         map[string]any{"X-Custom-Access": "opaque-provider-value"},
		"settings":        `{"advanced_custom":{"advanced_routes":[{"auth":{"type":"header","name":"X-Vendor-Cred","value":"opaque-provider-value"}}]}}`,
		"proxy":           "socks5://proxy-user:opaque-provider-value@example.test:1080",
		"enabled":         true,
	}
	encoded, err := json.Marshal(assistantRedactOperationResponse(value, "GetOptions"))
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "opaque-provider-value")
	assert.Contains(t, string(encoded), `"enabled":true`)
	option := map[string]any{"key": "client.default_headers", "value": `{"X-Opaque":"opaque-provider-value"}`}
	encoded, err = json.Marshal(assistantRedactOperationResponse(option, "GetOptions"))
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "opaque-provider-value")
}

func TestAssistantOperationChannelReadsNeverDiscloseRootOnlyProviderFields(t *testing.T) {
	for _, handler := range []string{"GetChannel", "GetAllChannels", "SearchChannels"} {
		t.Run(handler, func(t *testing.T) {
			channel := map[string]any{"id": 1, "name": "visible-channel", "models": "visible-model", "key": "opaque-provider-value", "base_url": "https://provider.test/opaque-provider-value", "other": "opaque-provider-value", "setting": `{"custom":"opaque-provider-value"}`, "settings": `{"custom":"opaque-provider-value"}`, "openai_organization": "opaque-provider-value"}
			encoded, err := json.Marshal(assistantRedactOperationResponse(map[string]any{"data": []any{channel}}, handler))
			require.NoError(t, err)
			assert.NotContains(t, string(encoded), "opaque-provider-value")
			assert.Contains(t, string(encoded), "visible-channel")
			assert.Contains(t, string(encoded), "visible-model")
			assert.Equal(t, "opaque-provider-value", channel["key"], "redaction must not mutate shared response data")
		})
	}
}

func TestAssistantOperationRedactsCredentialsInErrorsAndURLs(t *testing.T) {
	for _, input := range []string{
		"upstream rejected api_key=opaque-provider-value",
		"connection failed https://alice:opaque-provider-value@provider.test/v1?mode=fast",
		"request failed https://provider.test/v1?auth=opaque-provider-value&mode=fast",
		"https://provider.test/object?X-Amz-Signature=opaque-provider-value&mode=fast",
		"https://provider.test/callback#auth=opaque-provider-value",
		"https://provider.test/callback?auth=%ZZopaque-provider-value",
	} {
		t.Run(input, func(t *testing.T) {
			value := assistantRedactOperationResponse(map[string]any{"message": input}, "FetchUpstreamModels")
			encoded, err := json.Marshal(value)
			require.NoError(t, err)
			assert.NotContains(t, string(encoded), "opaque-provider-value")
		})
	}
	value := assistantRedactOperationResponse(map[string]any{"url": "https://docs.example.test/guide?mode=fast"}, "GetOptions").(map[string]any)
	assert.Equal(t, "https://docs.example.test/guide?mode=fast", value["url"])
	basicCredential := base64.StdEncoding.EncodeToString([]byte("user:opaque-provider-value"))
	assert.NotContains(t, assistantRedactOperationValue("upstream rejected Basic "+basicCredential, 0), basicCredential)
	assert.Equal(t, "Basic plan", assistantRedactOperationValue("Basic plan", 0))
}

func TestAssistantOperationRetainsSecurityProofFailureMetadata(t *testing.T) {
	value := assistantRedactOperationResponse(map[string]any{"success": false, "code": "SECURITY_PROOF_REQUIRED", "message": "Complete security verification"}, "GetChannelKey").(map[string]any)
	assert.Equal(t, "SECURITY_PROOF_REQUIRED", value["code"])
	assert.Equal(t, false, value["success"])
	value = assistantRedactOperationResponse(map[string]any{"success": true, "data": "opaque-provider-value"}, "GetChannelKey").(map[string]any)
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "opaque-provider-value")
	assert.Equal(t, true, value["redacted"])
}
