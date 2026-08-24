package helper

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	basecommon "github.com/LIghtJUNction/api.lmm.best/common"
	relayconstant "github.com/LIghtJUNction/api.lmm.best/relay/constant"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/LIghtJUNction/api.lmm.best/setting/model_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func imageValidationContext(t *testing.T, field, filename string, content []byte, validImage bool) *gin.Context {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("model", "gpt-image-1"))
	if validImage && field != "image" && field != "image[]" {
		image, err := writer.CreateFormFile("image", "blob")
		require.NoError(t, err)
		_, err = image.Write([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 0})
		require.NoError(t, err)
	}
	part, err := writer.CreateFormFile(field, filename)
	require.NoError(t, err)
	_, err = part.Write(content)
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", &body)
	context.Request.Header.Set("Content-Type", writer.FormDataContentType())
	t.Cleanup(func() { basecommon.CleanupBodyStorage(context) })
	return context
}

func requireInvalidImageAPIError(t *testing.T, err error) {
	t.Helper()
	var apiErr *types.NewAPIError
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
	require.Equal(t, types.ErrorCodeInvalidRequest, apiErr.GetErrorCode())
	require.True(t, types.IsSkipRetryError(apiErr))
}

func TestImageEditValidationRejectsEveryForwardedFileFieldBeforeRelay(t *testing.T) {
	gin.SetMode(gin.TestMode)
	heic := []byte{0, 0, 0, 0x18, 'f', 't', 'y', 'p', 'h', 'e', 'i', 'c'}
	originalPassThrough := model_setting.GetGlobalSettings().PassThroughRequestEnabled
	t.Cleanup(func() { model_setting.GetGlobalSettings().PassThroughRequestEnabled = originalPassThrough })

	for _, passThrough := range []bool{false, true} {
		model_setting.GetGlobalSettings().PassThroughRequestEnabled = passThrough
		for _, test := range []struct {
			field      string
			validImage bool
		}{
			{field: "image"},
			{field: "image[]"},
			{field: "mask", validImage: true},
		} {
			t.Run(test.field+"/pass-through="+map[bool]string{false: "off", true: "on"}[passThrough], func(t *testing.T) {
				context := imageValidationContext(t, test.field, "image.png", heic, test.validImage)
				_, err := GetAndValidOpenAIImageRequest(context, relayconstant.RelayModeImagesEdits)
				requireInvalidImageAPIError(t, err)
				require.False(t, context.GetBool(imageEditFilesValidatedKey))
			})
		}
	}
}

func TestImageEditValidationAcceptsExtensionlessPNGAndCachesSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	context := imageValidationContext(t, "image", "blob", []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 0}, false)

	_, err := GetAndValidOpenAIImageRequest(context, relayconstant.RelayModeImagesEdits)
	require.NoError(t, err)
	require.True(t, context.GetBool(imageEditFilesValidatedKey))
	require.Nil(t, ValidateOpenAIImageEditMultipart(context))
}
