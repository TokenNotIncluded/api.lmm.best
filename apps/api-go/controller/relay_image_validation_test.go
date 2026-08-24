package controller

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRelayRejectsUnsupportedImageBeforeBillingAndChannelSelection(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("model", "gpt-image-1"))
	part, err := writer.CreateFormFile("image[]", "image.png")
	require.NoError(t, err)
	_, err = part.Write([]byte{0, 0, 0, 0x18, 'f', 't', 'y', 'p', 'h', 'e', 'i', 'c'})
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	response := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(response)
	context.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", &body)
	context.Request.Header.Set("Content-Type", writer.FormDataContentType())
	context.Set(common.RequestIdKey, "image-validation-test")
	t.Cleanup(func() { common.CleanupBodyStorage(context) })

	Relay(context, types.RelayFormatOpenAIImage)

	require.Equal(t, http.StatusBadRequest, response.Code)
	var payload struct {
		Error types.OpenAIError `json:"error"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
	require.Equal(t, "new_api_error", payload.Error.Type)
	require.Equal(t, string(types.ErrorCodeInvalidRequest), payload.Error.Code)
	require.Contains(t, payload.Error.Message, "unsupported image content type")
	require.Empty(t, context.GetStringSlice("use_channel"), "validation must stop before upstream selection")
}
