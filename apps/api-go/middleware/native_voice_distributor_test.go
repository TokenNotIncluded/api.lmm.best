package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestLiveDistributorAuthorizesTheNestedModel(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(method, "/v1/live/sessions?model=misleading-query", strings.NewReader(`{"model":"misleading-top-level","session":{"model":"public-live"}}`))
		c.Request.Header.Set("Content-Type", "application/json")
		request, selectChannel, err := getModelRequest(c)
		require.NoError(t, err)
		require.True(t, selectChannel)
		require.Equal(t, "public-live", request.Model)
	}
}

func TestNativeVoiceChannelsRequireExplicitSupport(t *testing.T) {
	for _, path := range []string{"/v1/live/sessions", "/v1/realtime/translations", "/v1/realtime/transcription_sessions"} {
		for _, channelType := range []int{constant.ChannelTypeOpenAI, constant.ChannelTypeNewAPI} {
			require.True(t, channelSupportsRequestPath(&model.Channel{Type: channelType}, path, "voice-alias"))
		}
		for _, channelType := range []int{constant.ChannelTypeTypeSafe, constant.ChannelTypeGemini, constant.ChannelTypeAnthropic, constant.ChannelTypeAdvancedCustom} {
			require.False(t, channelSupportsRequestPath(&model.Channel{Type: channelType}, path, "voice-alias"))
		}
	}
}
