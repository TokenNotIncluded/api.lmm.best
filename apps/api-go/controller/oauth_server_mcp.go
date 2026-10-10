package controller

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
)

func (h *OAuthHTTP) MarketResourceMetadata(c *gin.Context) {
	c.JSON(200, gin.H{"resource": h.Integration.MarketResource(), "authorization_servers": []string{h.Integration.Issuer}, "scopes_supported": service.OAuthMarketScopes(), "bearer_methods_supported": []string{"header"}})
}

func (h *OAuthHTTP) RegisterMCPClient(c *gin.Context) {
	kind, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if err != nil || kind != "application/json" || c.Request.URL.RawQuery != "" || len(c.Request.Header.Values("Authorization")) != 0 || service.OAuthAlternateCredentials(c.Request) {
		c.JSON(400, gin.H{"error": "invalid_client_metadata"})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	input, err := decodeMCPClientRegistration(c.Request.Body)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid_client_metadata"})
		return
	}
	client, err := h.Integration.RegisterMCPClient(c.Request.Context(), input)
	if errors.Is(err, service.ErrMCPRegistration) {
		c.JSON(400, gin.H{"error": "invalid_client_metadata"})
		return
	}
	if err != nil {
		c.JSON(503, gin.H{"error": "temporarily_unavailable"})
		return
	}
	c.JSON(201, client)
}

// Unknown standard metadata is ignored, not fetched or treated as identity.
// Reject duplicate/case-alias keys and null policy fields before unmarshalling.
func decodeMCPClientRegistration(body io.Reader) (service.MCPClientRegistration, error) {
	var input service.MCPClientRegistration
	raw, err := io.ReadAll(io.LimitReader(body, (16<<10)+1))
	if err != nil || len(raw) > 16<<10 || !utf8.Valid(raw) {
		return input, service.ErrMCPRegistration
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return input, service.ErrMCPRegistration
	}
	known := map[string]bool{"client_name": true, "application_type": true, "redirect_uris": true, "token_endpoint_auth_method": true, "grant_types": true, "response_types": true, "scope": true}
	seen := map[string]bool{}
	fields := map[string]json.RawMessage{}
	for decoder.More() {
		token, err := decoder.Token()
		key, ok := token.(string)
		folded := strings.ToLower(key)
		if err != nil || !ok || seen[folded] || (known[folded] && key != folded) {
			return input, service.ErrMCPRegistration
		}
		seen[folded] = true
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return input, service.ErrMCPRegistration
		}
		if known[key] {
			if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				return input, service.ErrMCPRegistration
			}
			fields[key] = value
		}
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return input, service.ErrMCPRegistration
	}
	if _, err := decoder.Token(); err != io.EOF {
		return input, service.ErrMCPRegistration
	}
	filtered, err := json.Marshal(fields)
	if err != nil || json.Unmarshal(filtered, &input) != nil {
		return input, service.ErrMCPRegistration
	}
	return input, nil
}

// Public OAuth endpoints use no cookies. Browser clients may read discovery
// and exchange their own PKCE codes; consent/login routes never use this CORS
// policy and retain same-origin authentication and CSRF checks.
func OAuthPublicClientCORS(c *gin.Context) {
	service.MCPPublicClientHeaders(c.Writer.Header())
	if c.Request.Method == http.MethodOptions {
		c.AbortWithStatus(http.StatusNoContent)
		return
	}
	c.Next()
}
