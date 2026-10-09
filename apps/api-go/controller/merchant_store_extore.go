package controller

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/internal/extore"
	"github.com/LIghtJUNction/api.lmm.best/middleware"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
)

const storeExtorePurpose = service.MerchantStoreExtorePurpose

func storeExtoreError(c *gin.Context, err error) {
	c.Header("Cache-Control", "no-store")
	code, message, status := "STORE_EXTORE_UNAVAILABLE", "Extore could not be read. Start authorization again.", http.StatusBadGateway
	switch {
	case errors.Is(err, extore.ErrInput):
		code, message, status = "STORE_EXTORE_INPUT", "Check the Extore HTTPS base URL, client ID and registered callback.", http.StatusUnprocessableEntity
	case errors.Is(err, extore.ErrDenied):
		code, message, status = "STORE_EXTORE_DENIED", "Extore authorization was denied. No products were imported.", http.StatusForbidden
	case errors.Is(err, extore.ErrProtocol):
		code, message, status = "STORE_EXTORE_PROTOCOL", "This server did not return a valid Extore commerce import v1 response.", http.StatusBadGateway
	case errors.Is(err, model.ErrAuthFlowInvalid), errors.Is(err, model.ErrAuthFlowExpired), errors.Is(err, model.ErrAuthFlowConsumed):
		code, message, status = "STORE_EXTORE_EXPIRED", "This import authorization has expired or was already used. Connect again.", http.StatusConflict
	case errors.Is(err, common.ErrPersistentKeyUnavailable):
		code, message, status = "STORE_EXTORE_SECURE_STORAGE", "Extore import needs a persistent server encryption key. Ask an administrator to configure it.", http.StatusServiceUnavailable
	}
	// Provider, database and cryptography error strings must not reach the client.
	c.AbortWithStatusJSON(status, gin.H{"success": false, "code": code, "message": message})
}

func AuthorizeMerchantStoreExtore(c *gin.Context) {
	identity, ok := middleware.GetSessionAuthIdentity(c)
	if !ok {
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}
	var input struct {
		BaseURL  string `json:"base_url"`
		ClientID string `json:"client_id"`
	}
	if common.DecodeJson(c.Request.Body, &input) != nil {
		storeExtoreError(c, extore.ErrInput)
		return
	}
	flow, err := extore.NewFlow(input.BaseURL, input.ClientID, service.MerchantStoreExtoreRedirectURI())
	if err != nil {
		storeExtoreError(c, err)
		return
	}
	raw, err := json.Marshal(flow)
	if err != nil {
		storeExtoreError(c, err)
		return
	}
	encrypted, err := service.EncryptMerchantStoreExtoreFlow(string(raw))
	if err != nil {
		storeExtoreError(c, err)
		return
	}
	if err = service.MerchantStoreExtoreClient.Discover(c.Request.Context(), flow.Origin); err != nil {
		storeExtoreError(c, err)
		return
	}
	state, _, err := model.CreateAuthFlow(model.AuthFlowCreate{Purpose: storeExtorePurpose, UserId: identity.UserID, SessionId: identity.SessionID, Payload: encrypted, ExpiresAt: time.Now().Add(10 * time.Minute)})
	if err != nil {
		storeExtoreError(c, err)
		return
	}
	merchantStoreRespond(c, gin.H{"authorization_url": flow.AuthorizationURL(state), "base_url": flow.Origin, "redirect_uri": flow.RedirectURI}, nil)
}

func ReadMerchantStoreExtoreCatalog(c *gin.Context) {
	identity, ok := middleware.GetSessionAuthIdentity(c)
	if !ok {
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}
	var input struct {
		CallbackURL string `json:"callback_url"`
	}
	if common.DecodeJson(c.Request.Body, &input) != nil {
		storeExtoreError(c, extore.ErrInput)
		return
	}
	_, query, err := extore.ParseCallback(input.CallbackURL)
	if err != nil {
		storeExtoreError(c, err)
		return
	}
	match := model.AuthFlowMatch{Purpose: storeExtorePurpose, UserId: identity.UserID, SessionId: identity.SessionID}
	pending, err := model.GetAuthFlow(query.Get("state"), match)
	if err != nil {
		storeExtoreError(c, err)
		return
	}
	raw, err := service.DecryptMerchantStoreExtoreFlow(pending.Payload)
	if err != nil {
		storeExtoreError(c, err)
		return
	}
	var flow extore.Flow
	if json.Unmarshal([]byte(raw), &flow) != nil {
		storeExtoreError(c, extore.ErrInput)
		return
	}
	code, callbackErr := flow.ValidateCallback(input.CallbackURL)
	if callbackErr != nil && !errors.Is(callbackErr, extore.ErrDenied) {
		storeExtoreError(c, callbackErr)
		return
	}
	// Consume before exchanging: simultaneous callbacks can never replay a code.
	if _, err = model.ConsumeAuthFlow(query.Get("state"), match); err != nil {
		storeExtoreError(c, err)
		return
	}
	if callbackErr != nil {
		storeExtoreError(c, callbackErr)
		return
	}
	catalog, err := service.MerchantStoreExtoreClient.ReadCatalog(c.Request.Context(), flow, code)
	if err != nil {
		storeExtoreError(c, err)
		return
	}
	merchantStoreRespond(c, catalog, nil)
}
