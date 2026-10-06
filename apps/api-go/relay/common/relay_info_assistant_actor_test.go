package common

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGenRelayInfoSeparatesAssistantActorFromPayer(t *testing.T) {
	for _, assistant := range []bool{false, true} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		common.SetContextKey(c, constant.ContextKeyUserId, 987)
		common.SetContextKey(c, constant.ContextKeyUserGroup, "payer")
		common.SetContextKey(c, constant.ContextKeyUsingGroup, "route")
		common.SetContextKey(c, constant.ContextKeyAssistantActorUserID, 42)
		common.SetContextKey(c, constant.ContextKeyAssistantActorGroup, "actor")
		c.Set("assistant_request", assistant)
		info, err := GenRelayInfo(c, types.RelayFormatOpenAI, &dto.GeneralOpenAIRequest{Model: "fixture"}, nil)
		require.NoError(t, err)
		require.Equal(t, 987, info.UserId)
		require.Equal(t, "payer", info.UserGroup)
		require.Equal(t, "route", info.UsingGroup)
		if assistant {
			require.Equal(t, 42, info.AssistantActorUserID)
			require.Equal(t, "actor", info.AssistantActorGroup)
			common.SetContextKey(c, constant.ContextKeyAssistantActorUserID, 84)
			common.SetContextKey(c, constant.ContextKeyAssistantActorGroup, "other")
			require.Equal(t, 42, info.AssistantActorUserID, "the actor is a request snapshot, not mutable payer context")
			require.Equal(t, "actor", info.AssistantActorGroup)
		} else {
			require.Zero(t, info.AssistantActorUserID)
			require.Empty(t, info.AssistantActorGroup, "ordinary API requests retain their authenticated token owner")
		}
		encoded, err := json.Marshal(info)
		require.NoError(t, err)
		require.NotContains(t, string(encoded), "AssistantActorUserID")
		require.NotContains(t, string(encoded), "AssistantActorGroup")
	}
}
