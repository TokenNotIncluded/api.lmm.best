package relay

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	basecommon "github.com/LIghtJUNction/api.lmm.best/common"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	relayconstant "github.com/LIghtJUNction/api.lmm.best/relay/constant"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/LIghtJUNction/api.lmm.best/setting/model_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func invalidImageEditContext(t *testing.T) *gin.Context {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("model", "gpt-image-1"))
	part, err := writer.CreateFormFile("image", "image.png")
	require.NoError(t, err)
	_, err = part.Write([]byte{0, 0, 0, 0x18, 'f', 't', 'y', 'p', 'h', 'e', 'i', 'c'})
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", &body)
	context.Request.Header.Set("Content-Type", writer.FormDataContentType())
	t.Cleanup(func() { basecommon.CleanupBodyStorage(context) })
	return context
}

func TestImageHelperCannotBypassValidationWithPassThrough(t *testing.T) {
	gin.SetMode(gin.TestMode)
	originalPassThrough := model_setting.GetGlobalSettings().PassThroughRequestEnabled
	t.Cleanup(func() { model_setting.GetGlobalSettings().PassThroughRequestEnabled = originalPassThrough })

	for _, test := range []struct {
		name           string
		globalSetting  bool
		channelSetting bool
	}{
		{name: "pass-through off"},
		{name: "global pass-through", globalSetting: true},
		{name: "channel pass-through", channelSetting: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			model_setting.GetGlobalSettings().PassThroughRequestEnabled = test.globalSetting
			context := invalidImageEditContext(t)
			info := &relaycommon.RelayInfo{
				RelayMode: relayconstant.RelayModeImagesEdits,
				Request:   &dto.ImageRequest{Model: "gpt-image-1"},
				ChannelMeta: &relaycommon.ChannelMeta{
					ChannelSetting: dto.ChannelSettings{PassThroughBodyEnabled: test.channelSetting},
				},
			}

			apiErr := ImageHelper(context, info)
			require.NotNil(t, apiErr)
			require.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
			require.Equal(t, types.ErrorCodeInvalidRequest, apiErr.GetErrorCode())
			require.True(t, types.IsSkipRetryError(apiErr))
			require.Nil(t, info.Billing, "local validation must not create a billing session")
			require.Empty(t, context.GetStringSlice("use_channel"), "local validation must not select or contact an upstream")
		})
	}
}
