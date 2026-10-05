package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/model"
	relaychannel "github.com/LIghtJUNction/api.lmm.best/relay/channel"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type assistantPrivateIdentityTransport func(*http.Request) (*http.Response, error)

func (transport assistantPrivateIdentityTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return transport(req)
}

func TestAssistantPrivateIdentityFromControllerThroughFinalTransport(t *testing.T) {
	oldDB, oldLogDB, oldRedis, oldSecret := model.DB, model.LOG_DB, common.RedisEnabled, common.CryptoSecret
	oldSettings := setting.GetModerationSettings()
	t.Cleanup(func() {
		model.DB, model.LOG_DB, common.RedisEnabled, common.CryptoSecret = oldDB, oldLogDB, oldRedis, oldSecret
		require.NoError(t, setting.UpdateModerationSettings(oldSettings.OptionValues()))
	})
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.AssistantGiftRiskKey{}, &model.AssistantLead{}, &model.AssistantProfileBucket{}, &model.AssistantFirstQuestionStat{}))
	common.CryptoSecret = "synthetic-assistant-identity-key"
	withAssistantSettings(t, true, "fixture-assistant-model")
	settings := setting.DefaultModerationSettings()
	settings.AssistantEnabled, settings.SafetyIdentifierEnabled = true, true
	settings.PolicyScope = setting.ModerationPolicyScopeRequestGroup
	settings.GroupPolicies = map[string]setting.ModerationGroupPolicy{
		"actor-review": {Mode: setting.ModerationModeStrict},
		"actor-off":    {Mode: setting.ModerationModeOff},
		"default":      {Mode: setting.ModerationModeOff},
	}
	service.InitHttpClient()
	client := service.GetHttpClient()
	oldTransport := client.Transport
	t.Cleanup(func() { client.Transport = oldTransport })
	var sent map[string]json.RawMessage
	client.Transport = assistantPrivateIdentityTransport(func(req *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(req.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(body, &sent))
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{}`)), Request: req}, nil
	})
	identifiers := make(map[int]string)
	for _, test := range []struct {
		name, actorGroup, payerMode, missingKey string
		actorID                                 int
		inject                                  bool
	}{
		{name: "first actor with payer policy off", actorID: 42, actorGroup: "actor-review", payerMode: setting.ModerationModeOff, inject: true},
		{name: "second actor with the same payer", actorID: 84, actorGroup: "actor-review", payerMode: setting.ModerationModeOff, inject: true},
		{name: "actor opt-out with payer policy on", actorID: 42, actorGroup: "actor-off", payerMode: setting.ModerationModeStrict},
		{name: "missing actor ID never falls back to payer", actorID: 42, actorGroup: "actor-review", payerMode: setting.ModerationModeStrict, missingKey: assistantActorUserIDKey},
		{name: "missing actor group never falls back to payer", actorID: 42, actorGroup: "actor-review", payerMode: setting.ModerationModeStrict, missingKey: assistantActorGroupKey},
	} {
		t.Run(test.name, func(t *testing.T) {
			settings.GroupPolicies["default"] = setting.ModerationGroupPolicy{Mode: test.payerMode}
			require.NoError(t, setting.UpdateModerationSettings(settings.OptionValues()))
			engine := gin.New()
			engine.POST("/api/assistant/chat", func(c *gin.Context) {
				// These are authenticated middleware values, not request fields.
				common.SetContextKey(c, constant.ContextKeyUserId, test.actorID)
				common.SetContextKey(c, constant.ContextKeyUserGroup, test.actorGroup)
			}, PrepareAssistantRequest, func(c *gin.Context) {
				require.Equal(t, 987, c.GetInt("id"), "the actual controller selected the shared root payer")
				require.Equal(t, "default", common.GetContextKeyString(c, constant.ContextKeyUserGroup))
				require.Equal(t, test.actorID, c.GetInt(assistantActorUserIDKey))
				require.Equal(t, test.actorGroup, c.GetString(assistantActorGroupKey))
				if test.missingKey != "" {
					delete(c.Keys, test.missingKey)
				}
				var request dto.GeneralOpenAIRequest
				require.NoError(t, common.UnmarshalBodyReusable(c, &request))
				info, err := relaycommon.GenRelayInfo(c, types.RelayFormatOpenAI, &request, nil)
				require.NoError(t, err)
				require.True(t, info.IsAssistant)
				require.Equal(t, 987, info.UserId, "billing identity must remain the payer")
				common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
				common.SetContextKey(c, constant.ContextKeyChannelParamOverride, map[string]any{"safety_identifier": "channel-forgery"})
				info.InitChannelMeta(c)
				body, err := json.Marshal(request)
				require.NoError(t, err)
				body, err = relaycommon.ApplyParamOverrideWithRelayInfo(body, info)
				require.NoError(t, err)
				upstream, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, "https://api.openai.com/v1/chat/completions", bytes.NewReader(body))
				require.NoError(t, err)
				response, err := relaychannel.DoRequest(c, upstream, info)
				require.NoError(t, err)
				require.NoError(t, response.Body.Close())
				c.Status(http.StatusNoContent)
			})
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/api/assistant/chat", strings.NewReader(`{"message":"How do I create an API key?","assistant_actor_user_id":987,"assistant_actor_group":"default"}`))
			request.Header.Set("Content-Type", "application/json")
			engine.ServeHTTP(response, request)
			require.Equal(t, http.StatusNoContent, response.Code)
			var identifier string
			require.NoError(t, json.Unmarshal(sent["safety_identifier"], &identifier))
			payerID, err := model.OpenAIPrivateSafetyIdentifier(context.Background(), 987)
			require.NoError(t, err)
			require.NotEqual(t, payerID, identifier, "optional identity must never misattribute an actor to the root payer")
			if test.inject {
				want, err := model.OpenAIPrivateSafetyIdentifier(context.Background(), test.actorID)
				require.NoError(t, err)
				require.Equal(t, want, identifier)
				identifiers[test.actorID] = identifier
			} else {
				require.Equal(t, "channel-forgery", identifier, "disabled or unavailable actor policy leaves the optional request unchanged")
			}
		})
	}
	require.NotEmpty(t, identifiers[42])
	require.NotEmpty(t, identifiers[84])
	require.NotEqual(t, identifiers[42], identifiers[84], "two actors sharing a payer need different stable identifiers")
}
