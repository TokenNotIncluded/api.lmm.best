package router

import (
	"encoding/json"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/stretchr/testify/require"
)

func TestOAuthHTTPExcludesSystemOneAliasesFromChat(t *testing.T) {
	h := setupOAuthHTTP(t)
	for i, fixture := range []struct {
		name    string
		typeID  int
		mapping string
	}{
		{"native-decision", constant.ChannelTypeTypeSafe, `{}`},
		{"customer-decision", constant.ChannelTypeNewAPI, `{"customer-decision":"jev-latest"}`},
		{"customer-chat", constant.ChannelTypeNewAPI, `{"customer-chat":"gpt-4o"}`},
		{"official-live", constant.ChannelTypeOpenAI, `{"official-live":"gpt-live-1"}`},
		{"official-asr", constant.ChannelTypeOpenAI, `{"official-asr":"gpt-realtime-whisper"}`},
		{"official-moderation", constant.ChannelTypeOpenAI, `{"official-moderation":"omni-moderation-latest"}`},
		{"official-image", constant.ChannelTypeOpenAI, `{"official-image":"gpt-image-1-mini"}`},
	} {
		mapping := fixture.mapping
		channel := model.Channel{Id: 401 + i, Type: fixture.typeID, Status: common.ChannelStatusEnabled, Name: fixture.name, Key: "offline-fixture", Models: fixture.name, Group: "default", ModelMapping: &mapping}
		require.NoError(t, h.db.Create(&channel).Error)
		require.NoError(t, h.db.Create(&model.Ability{Group: "default", Model: fixture.name, ChannelId: channel.Id, Enabled: true}).Error)
	}
	credentials, _ := h.approve(t)
	response := h.request("GET", "/api/oauth2/catalog", "", map[string]string{"Authorization": "Bearer " + credentials.AccessToken})
	require.Equal(t, 200, response.Code, response.Body.String())
	var catalog service.OAuthCatalog
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &catalog))
	names := make([]string, 0, len(catalog.Models))
	for _, entry := range catalog.Models {
		names = append(names, entry.UpstreamModel)
	}
	require.NotContains(t, names, "native-decision")
	require.NotContains(t, names, "customer-decision")
	require.Contains(t, names, "customer-chat")
	headers := map[string]string{"Authorization": "Bearer " + credentials.AccessToken, service.OAuthGroupHeader: service.OAuthGroupID("default"), "Content-Type": "application/json"}
	for _, name := range []string{"native-decision", "customer-decision", "official-live", "official-asr", "official-moderation", "official-image"} {
		require.NotContains(t, names, name)
		response = h.request("POST", "/v1/chat/completions", `{"model":"`+name+`"}`, headers)
		require.Equal(t, 403, response.Code, response.Body.String())
	}
	response = h.request("POST", "/v1/chat/completions", `{"model":"customer-chat"}`, headers)
	require.Equal(t, 200, response.Code, response.Body.String())
}
