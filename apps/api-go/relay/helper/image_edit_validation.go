package helper

import (
	"errors"
	"mime"
	"net/http"
	"strings"

	basecommon "github.com/LIghtJUNction/api.lmm.best/common"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/gin-gonic/gin"
)

const imageEditFilesValidatedKey = "lmm_image_edit_files_validated"

// ValidateOpenAIImageEditMultipart validates files independently of channel
// selection and pass-through settings. A successful validation is reusable on
// retries of the same replayable request body.
func ValidateOpenAIImageEditMultipart(c *gin.Context) *types.NewAPIError {
	if c == nil || c.Request == nil || c.GetBool(imageEditFilesValidatedKey) {
		return nil
	}
	rawContentType := c.Request.Header.Get("Content-Type")
	mediaType, _, err := mime.ParseMediaType(rawContentType)
	if err != nil {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(rawContentType)), "multipart/form-data") {
			return types.NewErrorWithStatusCode(err, types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
		}
		return nil
	}
	if mediaType != "multipart/form-data" {
		return nil
	}

	form := c.Request.MultipartForm
	if form == nil {
		form, err = basecommon.ParseMultipartFormReusable(c)
		if err != nil {
			if basecommon.IsRequestBodyTooLargeError(err) || errors.Is(err, basecommon.ErrRequestBodyTooLarge) {
				return types.NewErrorWithStatusCode(err, types.ErrorCodeReadRequestBodyFailed, http.StatusRequestEntityTooLarge, types.ErrOptionWithSkipRetry())
			}
			return types.NewErrorWithStatusCode(err, types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
		}
		c.Request.MultipartForm = form
	}
	if err := relaycommon.ValidateImageEditMultipartFiles(form); err != nil {
		return types.NewErrorWithStatusCode(err, types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	c.Set(imageEditFilesValidatedKey, true)
	return nil
}
