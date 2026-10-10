package model_setting

import (
	"net/http"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/setting/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClaudeRefusalNoChargePolicyDefaultsOffAndPersistsExplicitOptIn(t *testing.T) {
	settings := defaultClaudeSettings
	require.False(t, settings.RefusalNoOutputNoChargeEnabled)
	manager := config.NewConfigManager()
	manager.Register("claude", &settings)
	saved := map[string]string{}
	require.NoError(t, manager.SaveToDB(func(key, value string) error { saved[key] = value; return nil }))
	require.Equal(t, "false", saved["claude.refusal_no_output_no_charge_enabled"])
	require.NoError(t, manager.LoadFromDB(map[string]string{"claude.refusal_no_output_no_charge_enabled": "true"}))
	require.True(t, settings.RefusalNoOutputNoChargeEnabled)
	require.NoError(t, manager.LoadFromDB(map[string]string{"claude.refusal_no_output_no_charge_enabled": "false"}))
	require.False(t, settings.RefusalNoOutputNoChargeEnabled)
}

func TestClaudeSettingsWriteHeadersMergesConfiguredValuesIntoSingleHeader(t *testing.T) {
	settings := &ClaudeSettings{
		HeadersSettings: map[string]map[string][]string{
			"claude-3-7-sonnet-20250219-thinking": {
				"anthropic-beta": {
					"token-efficient-tools-2025-02-19",
				},
			},
		},
	}

	headers := http.Header{}
	headers.Set("anthropic-beta", "output-128k-2025-02-19")

	settings.WriteHeaders("claude-3-7-sonnet-20250219-thinking", &headers)

	got := headers.Values("anthropic-beta")
	if len(got) != 1 {
		t.Fatalf("expected a single merged header value, got %v", got)
	}
	expected := "output-128k-2025-02-19,token-efficient-tools-2025-02-19"
	if got[0] != expected {
		t.Fatalf("expected merged header %q, got %q", expected, got[0])
	}
}

func TestClaudeSettingsWriteHeadersDeduplicatesAcrossCommaSeparatedAndRepeatedValues(t *testing.T) {
	settings := &ClaudeSettings{
		HeadersSettings: map[string]map[string][]string{
			"claude-3-7-sonnet-20250219-thinking": {
				"anthropic-beta": {
					"token-efficient-tools-2025-02-19",
					"computer-use-2025-01-24",
				},
			},
		},
	}

	headers := http.Header{}
	headers.Add("anthropic-beta", "output-128k-2025-02-19, token-efficient-tools-2025-02-19")
	headers.Add("anthropic-beta", "token-efficient-tools-2025-02-19")

	settings.WriteHeaders("claude-3-7-sonnet-20250219-thinking", &headers)

	got := headers.Values("anthropic-beta")
	if len(got) != 1 {
		t.Fatalf("expected duplicate values to collapse into one header, got %v", got)
	}
	expected := "output-128k-2025-02-19,token-efficient-tools-2025-02-19,computer-use-2025-01-24"
	if got[0] != expected {
		t.Fatalf("expected deduplicated merged header %q, got %q", expected, got[0])
	}
}

func TestValidateClaudeDefaultMaxTokens(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr string
	}{
		{name: "positive default", value: `{"default": 8192}`},
		{name: "zero allowed", value: `{"default": 0}`},
		{name: "zero model override allowed", value: `{"default": 8192, "claude-test": 0}`},
		{name: "empty map allowed", value: `{}`},
		{name: "negative default rejected", value: `{"default": -1}`, wantErr: `negative Claude default max_tokens -1 for "default"`},
		{name: "negative model override rejected", value: `{"default": 8192, "claude-test": -5}`, wantErr: `negative Claude default max_tokens -5 for "claude-test"`},
		{name: "non-integer rejected", value: `{"default": "high"}`, wantErr: "JSON map of model to integer"},
		{name: "null rejected", value: `null`, wantErr: "JSON map of model to integer"},
		{name: "malformed rejected", value: `{`, wantErr: "JSON map of model to integer"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateClaudeDefaultMaxTokens(tt.value)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}
