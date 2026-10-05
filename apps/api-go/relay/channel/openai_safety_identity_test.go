package channel_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	appconstant "github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/model"
	relaychannel "github.com/LIghtJUNction/api.lmm.best/relay/channel"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type privateIdentityTransport func(*http.Request) (*http.Response, error)

func (transport privateIdentityTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return transport(req)
}

func TestOpenAIPrivateIdentityActualHTTPTransportAfterRawBodyAndOverrides(t *testing.T) {
	oldDB, oldSecret, oldSettings := model.DB, common.CryptoSecret, setting.GetModerationSettings()
	t.Cleanup(func() {
		model.DB, common.CryptoSecret = oldDB, oldSecret
		require.NoError(t, setting.UpdateModerationSettings(oldSettings.OptionValues()))
	})
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "transport.sqlite")), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.AssistantGiftRiskKey{}))
	model.DB, common.CryptoSecret = db, "synthetic-transport-key"
	settings := setting.DefaultModerationSettings()
	settings.Enabled, settings.SafetyIdentifierEnabled = true, true
	settings.GroupPolicies = map[string]setting.ModerationGroupPolicy{"account": {Mode: setting.ModerationModeTolerant}}
	require.NoError(t, setting.UpdateModerationSettings(settings.OptionValues()))
	want, err := model.OpenAIPrivateSafetyIdentifier(context.Background(), 42)
	require.NoError(t, err)
	service.InitHttpClient()
	client := service.GetHttpClient()
	oldTransport := client.Transport
	t.Cleanup(func() { client.Transport = oldTransport })
	var actual map[string]json.RawMessage
	client.Transport = privateIdentityTransport(func(req *http.Request) (*http.Response, error) {
		body, readErr := io.ReadAll(req.Body)
		require.NoError(t, readErr)
		require.EqualValues(t, len(body), req.ContentLength)
		require.NoError(t, json.Unmarshal(body, &actual))
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader([]byte(`{}`))), Request: req}, nil
	})
	for _, path := range []string{"/v1/chat/completions", "/v1/responses"} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, path, nil)
		info := &relaycommon.RelayInfo{UserId: 42, UserGroup: "account", UsingGroup: "request", ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:   appconstant.ChannelTypeOpenAI,
			ParamOverride: map[string]any{"safety_identifier": "channel-forgery"},
		}}
		info.ChannelSetting.PassThroughBodyEnabled = true
		body, err := relaycommon.ApplyParamOverrideWithRelayInfo([]byte(`{"model":"mapped-model","input":"hi","unknown_raw":{"keep":true},"safety_identifier":"client-forgery"}`), info)
		require.NoError(t, err)
		require.Contains(t, string(body), "channel-forgery")
		req, err := http.NewRequest(http.MethodPost, "https://api.openai.com"+path, bytes.NewReader(body))
		require.NoError(t, err)
		resp, err := relaychannel.DoRequest(c, req, info)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		var got string
		require.NoError(t, json.Unmarshal(actual["safety_identifier"], &got))
		require.Equal(t, want, got)
		require.JSONEq(t, `{"keep":true}`, string(actual["unknown_raw"]))
		// The same final transport must keep third-party requests uninjected.
		req, err = http.NewRequest(http.MethodPost, "https://third-party.example"+path, bytes.NewReader(body))
		require.NoError(t, err)
		resp, err = relaychannel.DoRequest(c, req, info)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		require.JSONEq(t, `"channel-forgery"`, string(actual["safety_identifier"]))
	}
}
