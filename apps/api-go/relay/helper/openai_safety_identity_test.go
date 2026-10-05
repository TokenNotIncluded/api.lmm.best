package helper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	appconstant "github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/model"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupPrivateSafetyIdentity(t *testing.T) *relaycommon.RelayInfo {
	t.Helper()
	oldDB, oldSecret, oldSettings := model.DB, common.CryptoSecret, setting.GetModerationSettings()
	t.Cleanup(func() {
		model.DB, common.CryptoSecret = oldDB, oldSecret
		require.NoError(t, setting.UpdateModerationSettings(oldSettings.OptionValues()))
	})
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "identity.sqlite")), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.AssistantGiftRiskKey{}))
	model.DB, common.CryptoSecret = db, "synthetic-relay-private-key"
	settings := setting.DefaultModerationSettings()
	settings.Enabled, settings.AssistantEnabled, settings.SafetyIdentifierEnabled = true, true, true
	settings.GroupPolicies = map[string]setting.ModerationGroupPolicy{
		"account": {Mode: setting.ModerationModeTolerant},
		"request": {Mode: setting.ModerationModeStrict},
		"off":     {Mode: setting.ModerationModeOff},
	}
	require.NoError(t, setting.UpdateModerationSettings(settings.OptionValues()))
	return &relaycommon.RelayInfo{UserId: 42, UserGroup: "account", UsingGroup: "request", ChannelMeta: &relaycommon.ChannelMeta{ChannelType: appconstant.ChannelTypeOpenAI}}
}

func TestPrivateSafetyIdentityFinalBodyOverridesClientAndPreservesPayload(t *testing.T) {
	info := setupPrivateSafetyIdentity(t)
	want, err := model.OpenAIPrivateSafetyIdentifier(context.Background(), info.UserId)
	require.NoError(t, err)
	for _, path := range []string{"/v1/chat/completions", "/v1/responses"} {
		for _, name := range []string{"model-a", "model-b"} {
			body := []byte(`{"model":"` + name + `","safety_identifier":"client","safety_identifier":"override","user":"forged-email@example.test","metadata":{"integer":9007199254740993},"input":"hi"}`)
			out := ApplyOpenAIPrivateSafetyIdentifier(context.Background(), info, "https://api.openai.com"+path, "", body)
			var fields map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(out, &fields))
			var identifier string
			require.NoError(t, json.Unmarshal(fields["safety_identifier"], &identifier))
			require.Equal(t, want, identifier)
			require.NotContains(t, string(out), `"override"`)
			require.JSONEq(t, `{"integer":9007199254740993}`, string(fields["metadata"]))
			require.JSONEq(t, `"forged-email@example.test"`, string(fields["user"]), "legacy user field is not used as our identity")
		}
	}
}

func TestPrivateSafetyIdentityFinalOriginAndSupportedEndpointsOnly(t *testing.T) {
	info := setupPrivateSafetyIdentity(t)
	body := []byte(`{"input":"hi"}`)
	for _, target := range []string{
		"http://api.openai.com/v1/responses", "https://api.openai.com.evil.test/v1/responses",
		"https://api.openai.com@relay.example/v1/responses", "https://api.openai.com:8443/v1/responses",
		"https://api.openai.com/v1/moderations", "https://api.openai.com/v1/realtime",
		"https://api.openai.com/v1/responses/compact", "https://api.openai.com/v1/embeddings",
		"https://api.openai.com/v1/chat/completions#fragment",
	} {
		require.Equal(t, body, ApplyOpenAIPrivateSafetyIdentifier(context.Background(), info, target, "", body), target)
	}
	require.Equal(t, body, ApplyOpenAIPrivateSafetyIdentifier(context.Background(), info, "https://api.openai.com/v1/responses", "relay.example", body))
	for _, channelType := range []int{appconstant.ChannelTypeAzure, appconstant.ChannelTypeOpenRouter, appconstant.ChannelTypeCustom} {
		info.ChannelType = channelType
		require.Equal(t, body, ApplyOpenAIPrivateSafetyIdentifier(context.Background(), info, "https://api.openai.com/v1/responses", "", body))
	}
	info.ChannelType = appconstant.ChannelTypeOpenAI
	require.Contains(t, string(ApplyOpenAIPrivateSafetyIdentifier(context.Background(), info, "wss://api.openai.com:443/v1/responses", "api.openai.com", body)), "safety_identifier")
}

func TestPrivateSafetyIdentityUsesConfiguredTrustedPolicyScope(t *testing.T) {
	info := setupPrivateSafetyIdentity(t)
	body := []byte(`{"input":"hi"}`)
	apply := func() []byte {
		return ApplyOpenAIPrivateSafetyIdentifier(context.Background(), info, "https://api.openai.com/v1/responses", "", body)
	}
	for _, update := range []map[string]string{
		{setting.ModerationSafetyIdentifierEnabledOptionKey: "false"},
		{setting.ModerationSafetyIdentifierEnabledOptionKey: "true", setting.ModerationEnabledOptionKey: "false"},
	} {
		require.NoError(t, setting.UpdateModerationSettings(update))
		require.Equal(t, body, apply())
	}
	require.NoError(t, setting.UpdateModerationSettings(map[string]string{setting.ModerationEnabledOptionKey: "true", setting.ModerationPolicyScopeOptionKey: setting.ModerationPolicyScopeRequestGroup}))
	info.UsingGroup = "off"
	require.Equal(t, body, apply())
	info.UsingGroup = ""
	require.Equal(t, body, apply(), "missing trusted request group must not fall back to enabled account")
	info.UsingGroup = "request"
	require.NotEqual(t, body, apply())
	info.IsAssistant, info.UsingGroup = true, "off"
	require.NotEqual(t, body, apply(), "assistant always uses account policy")
	require.NoError(t, setting.UpdateModerationSettings(map[string]string{setting.AssistantModerationEnabledOptionKey: "false"}))
	require.Equal(t, body, apply())
}

func TestPrivateSafetyIdentityUnavailableRemovesForgeryWithoutBlocking(t *testing.T) {
	info := setupPrivateSafetyIdentity(t)
	model.DB = nil
	body := []byte(`{"model":"x","safety_identifier":"forged","input":"hi"}`)
	out := ApplyOpenAIPrivateSafetyIdentifier(context.Background(), info, "https://api.openai.com/v1/responses", "", body)
	require.JSONEq(t, `{"model":"x","input":"hi"}`, string(out))
	for _, malformed := range []string{`{"input":`, `null`, `[1]`} {
		require.Equal(t, malformed, string(ApplyOpenAIPrivateSafetyIdentifier(context.Background(), info, "https://api.openai.com/v1/responses", "", []byte(malformed))))
	}
}

func TestPrivateSafetyIdentityRawHTTPBodyIsReplayableAtFinalBoundary(t *testing.T) {
	info := setupPrivateSafetyIdentity(t)
	info.ChannelSetting.PassThroughBodyEnabled = true
	body := []byte(`{"model":"x","unknown_field":{"keep":true},"safety_identifier":"client-override"}`)
	req, err := http.NewRequest(http.MethodPost, "https://api.openai.com/v1/chat/completions", bytes.NewReader(body))
	require.NoError(t, err)
	ApplyOpenAIPrivateSafetyIdentifierToRequest(req, info)
	got, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	require.EqualValues(t, len(got), req.ContentLength)
	require.NotContains(t, string(got), "client-override")
	require.Contains(t, string(got), `"unknown_field":{"keep":true}`)
	replay, err := req.GetBody()
	require.NoError(t, err)
	defer replay.Close()
	gotReplay, err := io.ReadAll(replay)
	require.NoError(t, err)
	require.Equal(t, got, gotReplay)
}

type identityFailingBody struct {
	data   []byte
	read   bool
	closed bool
}

func (body *identityFailingBody) Read(buffer []byte) (int, error) {
	body.read = true
	count := copy(buffer, body.data)
	body.data = nil
	return count, io.ErrUnexpectedEOF
}

func (body *identityFailingBody) Close() error { body.closed = true; return nil }

func TestPrivateSafetyIdentityBodyFailurePreservesOriginalTransportError(t *testing.T) {
	info := setupPrivateSafetyIdentity(t)
	failed := &identityFailingBody{data: []byte(`{"safety_identifier":"forged"}`)}
	req, err := http.NewRequest(http.MethodPost, "https://api.openai.com/v1/responses", failed)
	require.NoError(t, err)
	ApplyOpenAIPrivateSafetyIdentifierToRequest(req, info)
	got, err := io.ReadAll(req.Body)
	require.True(t, errors.Is(err, io.ErrUnexpectedEOF), "identity processing cannot turn an already-failed upload into an accepted forged request")
	require.JSONEq(t, `{"safety_identifier":"forged"}`, string(got))
	require.NoError(t, req.Body.Close())
	require.True(t, failed.closed)
}

func TestPrivateSafetyIdentityDisabledDoesNotReadOrRewriteRequest(t *testing.T) {
	info := setupPrivateSafetyIdentity(t)
	require.NoError(t, setting.UpdateModerationSettings(map[string]string{setting.ModerationSafetyIdentifierEnabledOptionKey: "false"}))
	body := &identityFailingBody{data: []byte(`{"safety_identifier":"client"}`)}
	req, err := http.NewRequest(http.MethodPost, "https://api.openai.com/v1/responses", body)
	require.NoError(t, err)
	ApplyOpenAIPrivateSafetyIdentifierToRequest(req, info)
	require.Same(t, body, req.Body)
	require.False(t, body.read)
	require.False(t, body.closed)
}
