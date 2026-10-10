package middleware

import (
	"context"
	"io"
	"mime"
	"net/http"
	"strconv"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/oauthserver"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/sjson"
)

// Authenticate before body admission. This endpoint never accepts API keys or
// caller-supplied groups, and does not read unauthenticated request bodies.
func OAuthOpenAIAuth(c *gin.Context) {
	service.OAuthNoStore(c.Writer)
	integration := service.CurrentOAuthIntegration()
	if integration == nil {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	if !integration.OAuthRequestTransport(c.Request) {
		oauthResourceFailure(c, http.StatusUnauthorized, "invalid_token")
		return
	}
	if c.Request.Method != http.MethodPost || c.Request.URL.Path != service.OAuthOpenAIBasePath+"/chat/completions" ||
		c.Request.URL.RawQuery != "" || c.Request.URL.ForceQuery || service.OAuthAlternateCredentials(c.Request) ||
		len(c.Request.Header.Values(service.OAuthGroupHeader)) != 0 || len(c.Request.Header.Values("Content-Type")) != 1 {
		oauthResourceFailure(c, http.StatusBadRequest, "invalid_request")
		return
	}
	contentType, _, err := mime.ParseMediaType(c.Request.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		oauthResourceFailure(c, http.StatusBadRequest, "invalid_request")
		return
	}
	access, err := oauthserver.BearerFromRequest(c.Request)
	if err != nil {
		oauthResourceFailure(c, http.StatusUnauthorized, "invalid_token")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	_, user, err := integration.ValidateResource(ctx, access, service.OAuthInvokeScope)
	if err != nil {
		oauthResourceFailure(c, http.StatusForbidden, "insufficient_scope")
		return
	}
	c.Set("id", user.Id)
	c.Set("username", user.Username)
	c.Set("role", user.Role)
	user.ToBaseUser().WriteContext(c)
	c.Next()
}

// Run after shared large-request admission. Only the envelope changes; normal
// OAuth group/model checks, channel selection, streaming and billing still run.
func OAuthOpenAIRequest(c *gin.Context) {
	body := oauthModelBody{maxModelBytes: 2048}
	if err := common.UnmarshalBodyReusable(c, &body); err != nil {
		status := http.StatusBadRequest
		if common.IsRequestBodyTooLargeError(err) {
			status = http.StatusRequestEntityTooLarge
		}
		oauthResourceFailure(c, status, "invalid_request")
		return
	}
	group, wireModel, err := service.OAuthModelFromID(body.Model)
	if err != nil {
		oauthResourceFailure(c, http.StatusBadRequest, "invalid_request")
		return
	}
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		oauthResourceFailure(c, http.StatusBadRequest, "invalid_request")
		return
	}
	original, err := storage.Bytes()
	if err != nil {
		oauthResourceFailure(c, http.StatusBadRequest, "invalid_request")
		return
	}
	rewritten, err := sjson.SetBytes(original, "model", wireModel)
	if err != nil {
		oauthResourceFailure(c, http.StatusBadRequest, "invalid_request")
		return
	}
	replacement, err := common.CreateBodyStorage(rewritten)
	if err != nil {
		oauthResourceFailure(c, http.StatusServiceUnavailable, "temporarily_unavailable")
		return
	}
	common.CleanupBodyStorage(c)
	c.Set(common.KeyBodyStorage, replacement)
	c.Request.Body = io.NopCloser(replacement)
	c.Request.ContentLength = int64(len(rewritten))
	c.Request.Header.Set("Content-Length", strconv.Itoa(len(rewritten)))
	c.Request.TransferEncoding = nil
	c.Request.URL.Path = "/v1/chat/completions"
	c.Request.URL.RawPath = ""
	c.Request.Header.Set(service.OAuthGroupHeader, group)
	authenticateOAuthResource(c)
}
