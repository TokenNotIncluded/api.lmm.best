package controller

import (
	"context"
	"errors"
	"net/http"

	"github.com/LIghtJUNction/api.lmm.best/middleware"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
)

// RequestWaffoPancakeCustomerPortal issues a link only for a live dashboard
// session. A scoped personal API token must not become a customer portal login.
func RequestWaffoPancakeCustomerPortal(c *gin.Context) {
	requestWaffoPancakeCustomerPortal(c, service.CreateWaffoPancakeCustomerPortalLink)
}

func requestWaffoPancakeCustomerPortal(c *gin.Context, issue func(context.Context, int) (*service.WaffoPancakeCustomerPortalLink, error)) {
	c.Header("Cache-Control", "no-store, private")
	c.Header("Pragma", "no-cache")
	c.Header("Referrer-Policy", "no-referrer")
	identity, ok := middleware.GetSessionAuthIdentity(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "code": "AUTH_SESSION_REQUIRED", "message": "Sign in to open the customer portal."})
		return
	}
	// Deliberately do not bind a request body or query: customer, store,
	// environment and redirect targets cannot be selected by the caller.
	link, err := issue(c.Request.Context(), identity.UserID)
	if err != nil || link == nil {
		status, code := http.StatusBadGateway, "WAFFO_PORTAL_UNAVAILABLE"
		if errors.Is(err, service.ErrWaffoPancakePortalUnavailable) {
			status = http.StatusServiceUnavailable
		}
		// Provider errors can carry request/response data. Do not echo or log
		// them; in particular, a failed response may still contain a token.
		c.JSON(status, gin.H{"success": false, "code": code, "message": "The customer portal is unavailable. Please try again later."})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": link})
}
