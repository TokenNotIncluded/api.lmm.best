package controller

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"

	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
)

func (h *OAuthHTTP) MarketResourceMetadata(c *gin.Context) {
	c.JSON(200, gin.H{"resource": h.Integration.MarketResource(), "authorization_servers": []string{h.Integration.Issuer}, "scopes_supported": service.OAuthMarketScopes(), "bearer_methods_supported": []string{"header"}})
}

func (h *OAuthHTTP) RegisterMCPClient(c *gin.Context) {
	kind, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if err != nil || kind != "application/json" || c.Request.URL.RawQuery != "" || c.GetHeader("Authorization") != "" {
		c.JSON(400, gin.H{"error": "invalid_client_metadata"})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	decoder := json.NewDecoder(c.Request.Body)
	var input service.MCPClientRegistration
	var trailing any
	if decoder.Decode(&input) != nil || decoder.Decode(&trailing) != io.EOF {
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
