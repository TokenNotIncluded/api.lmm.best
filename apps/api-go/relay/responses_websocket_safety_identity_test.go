package relay

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	appconstant "github.com/LIghtJUNction/api.lmm.best/constant"
	appmodel "github.com/LIghtJUNction/api.lmm.best/model"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	relayconstant "github.com/LIghtJUNction/api.lmm.best/relay/constant"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestResponsesWebSocketPrivateIdentityAfterFinalOverrides(t *testing.T) {
	oldDB, oldSecret, oldSettings := appmodel.DB, common.CryptoSecret, setting.GetModerationSettings()
	t.Cleanup(func() {
		appmodel.DB, common.CryptoSecret = oldDB, oldSecret
		require.NoError(t, setting.UpdateModerationSettings(oldSettings.OptionValues()))
	})
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "websocket.sqlite")), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&appmodel.AssistantGiftRiskKey{}))
	appmodel.DB, common.CryptoSecret = db, "synthetic-websocket-key"
	// Bootstrap is covered by model tests; prepare the durable key outside the
	// request timeout so this transport test only exercises identity routing.
	require.NoError(t, db.Create(&appmodel.AssistantGiftRiskKey{
		Id: "assistant-gift-risk-v1", Secret: common.CryptoSecret, CreatedAt: common.GetTimestamp(),
	}).Error)
	settings := setting.DefaultModerationSettings()
	settings.Enabled, settings.SafetyIdentifierEnabled = true, true
	settings.GroupPolicies = map[string]setting.ModerationGroupPolicy{"account": {Mode: setting.ModerationModeTolerant}}
	require.NoError(t, setting.UpdateModerationSettings(settings.OptionValues()))
	want, err := appmodel.OpenAIPrivateSafetyIdentifier(context.Background(), 42)
	require.NoError(t, err)
	for _, baseURL := range []string{"https://api.openai.com", "https://third-party.example"} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
		common.SetContextKey(c, appconstant.ContextKeyChannelType, appconstant.ChannelTypeOpenAI)
		common.SetContextKey(c, appconstant.ContextKeyChannelBaseUrl, baseURL)
		common.SetContextKey(c, appconstant.ContextKeyChannelParamOverride, map[string]any{"safety_identifier": "channel-forgery"})
		common.SetContextKey(c, appconstant.ContextKeyOriginalModel, "mapped-model")
		info := &relaycommon.RelayInfo{UserId: 42, UserGroup: "account", UsingGroup: "request", RequestURLPath: "/v1/responses", RelayMode: relayconstant.RelayModeResponses, RelayFormat: types.RelayFormatOpenAIResponses}
		req := dto.OpenAIResponsesRequest{Model: "client-model", Input: common.RawMessage(`"hello"`), SafetyIdentifier: common.RawMessage(`"client-forgery"`)}
		for _, streamID := range []string{"stream-1", "stream-2"} {
			out, apiErr := buildResponsesWSCreatePayload(c, info, req, common.RawMessage(`false`), streamID)
			require.Nil(t, apiErr)
			var event map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(out, &event))
			var got string
			require.NoError(t, json.Unmarshal(event["safety_identifier"], &got))
			if baseURL == "https://api.openai.com" {
				require.Equal(t, want, got)
			} else {
				require.Equal(t, "channel-forgery", got)
			}
			require.JSONEq(t, `"response.create"`, string(event["type"]))
			require.JSONEq(t, `false`, string(event["generate"]))
		}
	}
}

func TestResponsesWebSocketPrivateIdentityUsesCapturedAssistantActor(t *testing.T) {
	oldDB, oldSecret, oldSettings := appmodel.DB, common.CryptoSecret, setting.GetModerationSettings()
	t.Cleanup(func() {
		appmodel.DB, common.CryptoSecret = oldDB, oldSecret
		require.NoError(t, setting.UpdateModerationSettings(oldSettings.OptionValues()))
	})
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "assistant-websocket.sqlite")), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&appmodel.AssistantGiftRiskKey{}))
	appmodel.DB, common.CryptoSecret = db, "synthetic-assistant-websocket-key"
	// Prepare the shared key before the actor/override assertions enter the
	// bounded request path, as in the transport fixture above.
	require.NoError(t, db.Create(&appmodel.AssistantGiftRiskKey{
		Id: "assistant-gift-risk-v1", Secret: common.CryptoSecret, CreatedAt: common.GetTimestamp(),
	}).Error)
	settings := setting.DefaultModerationSettings()
	settings.AssistantEnabled, settings.SafetyIdentifierEnabled = true, true
	settings.PolicyScope = setting.ModerationPolicyScopeRequestGroup
	settings.GroupPolicies = map[string]setting.ModerationGroupPolicy{
		"actor": {Mode: setting.ModerationModeTolerant},
		"payer": {Mode: setting.ModerationModeOff},
	}
	require.NoError(t, setting.UpdateModerationSettings(settings.OptionValues()))
	identifiers := map[int]string{}
	for _, actorID := range []int{42, 84, 0} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
		c.Set("assistant_request", true)
		common.SetContextKey(c, appconstant.ContextKeyUserId, 987)
		common.SetContextKey(c, appconstant.ContextKeyUserGroup, "payer")
		common.SetContextKey(c, appconstant.ContextKeyUsingGroup, "payer")
		common.SetContextKey(c, appconstant.ContextKeyAssistantActorUserID, actorID)
		common.SetContextKey(c, appconstant.ContextKeyAssistantActorGroup, "actor")
		common.SetContextKey(c, appconstant.ContextKeyChannelType, appconstant.ChannelTypeOpenAI)
		common.SetContextKey(c, appconstant.ContextKeyChannelBaseUrl, "https://api.openai.com")
		common.SetContextKey(c, appconstant.ContextKeyChannelParamOverride, map[string]any{"safety_identifier": "channel-forgery"})
		request := dto.OpenAIResponsesRequest{Model: "fixture", Input: common.RawMessage(`"hello"`)}
		info, err := relaycommon.GenRelayInfo(c, types.RelayFormatOpenAIResponses, &request, nil)
		require.NoError(t, err)
		require.Equal(t, 987, info.UserId)
		out, apiErr := buildResponsesWSCreatePayload(c, info, request, nil, "stream-fixture")
		require.Nil(t, apiErr)
		var fields map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(out, &fields))
		var identifier string
		require.NoError(t, json.Unmarshal(fields["safety_identifier"], &identifier))
		if actorID == 0 {
			require.Equal(t, "channel-forgery", identifier, "missing actor must not bind the websocket to its payer")
			continue
		}
		want, err := appmodel.OpenAIPrivateSafetyIdentifier(context.Background(), actorID)
		require.NoError(t, err)
		require.Equal(t, want, identifier)
		identifiers[actorID] = identifier
	}
	require.NotEqual(t, identifiers[42], identifiers[84])
}
