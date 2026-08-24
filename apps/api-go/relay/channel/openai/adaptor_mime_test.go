package openai

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	relayconstant "github.com/LIghtJUNction/api.lmm.best/relay/constant"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

var testPNGBytes = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 0}
var testHEICBytes = []byte{0, 0, 0, 0x18, 'f', 't', 'y', 'p', 'h', 'e', 'i', 'c'}

func TestConvertImageRequestRejectsUnsupportedMaskContent(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("model", "gpt-image-1"))
	imagePart, err := writer.CreateFormFile("image", "input.png")
	require.NoError(t, err)
	_, err = imagePart.Write(testPNGBytes)
	require.NoError(t, err)
	maskPart, err := writer.CreateFormFile("mask", "mask.png")
	require.NoError(t, err)
	_, err = maskPart.Write(testHEICBytes)
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", &body)
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())
	require.NoError(t, c.Request.ParseMultipartForm(32<<20))

	info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeImagesEdits}
	converted, err := (&Adaptor{}).ConvertImageRequest(c, info, dto.ImageRequest{Model: "gpt-image-1"})

	require.Nil(t, converted)
	require.EqualError(t, err, `unsupported image content type "application/octet-stream"; supported formats: JPEG, PNG, WebP`)
}

func TestConvertImageRequestRejectsUnsupportedImageContent(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("model", "gpt-image-1"))
	part, err := writer.CreateFormFile("image", "input.png")
	require.NoError(t, err)
	_, err = part.Write(testHEICBytes)
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", &body)
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())
	require.NoError(t, c.Request.ParseMultipartForm(32<<20))

	info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeImagesEdits}
	converted, err := (&Adaptor{}).ConvertImageRequest(c, info, dto.ImageRequest{Model: "gpt-image-1"})

	require.Nil(t, converted)
	require.EqualError(t, err, `unsupported image content type "application/octet-stream"; supported formats: JPEG, PNG, WebP`)
}
