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
	case errors.Is(err, model.ErrMerchantStoreExtoreReauthorize):
		code, message, status = "STORE_EXTORE_REAUTHORIZE", "Extore authorization expired or could not be refreshed. Connect again.", http.StatusConflict
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
	if input.ClientID == "" {
		input.ClientID = "lmm-best-store"
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
	merchantStoreRespond(c, gin.H{"authorization_url": flow.SyncAuthorizationURL(state), "base_url": flow.Origin, "redirect_uri": flow.RedirectURI}, nil)
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
	credentials, err := service.MerchantStoreExtoreClient.Connect(c.Request.Context(), flow, code)
	if err != nil {
		storeExtoreError(c, err)
		return
	}
	catalog, err := service.MerchantStoreExtoreClient.ConnectionCatalog(c.Request.Context(), flow.Origin, *credentials)
	if err != nil {
		storeExtoreError(c, err)
		return
	}
	secret, err := json.Marshal(credentials)
	if err != nil {
		storeExtoreError(c, err)
		return
	}
	connection, err := model.SaveMerchantStoreExtoreConnection(identity.UserID, flow.Origin, catalog.Shop.ID, catalog.Shop.Name, flow.ClientID, string(secret), int64(credentials.GrantExpires))
	if err != nil {
		storeExtoreError(c, err)
		return
	}
	results := make([]service.MerchantStoreExtoreSyncResult, 0, len(catalog.Products))
	for _, raw := range catalog.Products {
		var listing struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(raw, &listing)
		source, syncErr := service.MerchantStoreExtoreSyncListing(identity.UserID, connection.ID, raw)
		result := service.MerchantStoreExtoreSyncResult{SourceID: listing.ID, Success: syncErr == nil}
		if syncErr != nil {
			result.Message = service.MerchantStoreExtoreSyncMessage(syncErr)
		} else {
			result.ProductID = source.ProductID
			result.LastSyncedAt = source.LastSyncedAt
			result.Message = "Product synchronized from Extore"
		}
		results = append(results, result)
	}
	merchantStoreRespond(c, gin.H{"connection": connection, "results": results}, nil)
}

func ListMerchantStoreExtoreConnections(c *gin.Context) {
	identity, ok := middleware.GetSessionAuthIdentity(c)
	if !ok {
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}
	rows, err := model.ListMerchantStoreExtoreConnections(identity.UserID)
	if err != nil {
		storeExtoreError(c, err)
		return
	}
	sources, err := model.MerchantStoreExtoreProductSources(identity.UserID)
	if err != nil {
		storeExtoreError(c, err)
		return
	}
	merchantStoreRespond(c, gin.H{"connections": rows, "sources": sources}, nil)
}
func GetMerchantStoreExtoreConnectionCatalog(c *gin.Context) {
	identity, ok := middleware.GetSessionAuthIdentity(c)
	if !ok {
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}
	catalog, err := service.MerchantStoreExtoreConnectionCatalog(c.Request.Context(), identity.UserID, c.Param("id"))
	if err != nil {
		storeExtoreError(c, err)
		return
	}
	merchantStoreRespond(c, catalog, nil)
}
func SyncMerchantStoreExtoreProduct(c *gin.Context) {
	identity, ok := middleware.GetSessionAuthIdentity(c)
	if !ok {
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}
	var input struct {
		SourceID string `json:"source_id"`
	}
	if common.DecodeJson(c.Request.Body, &input) != nil || input.SourceID == "" || len(input.SourceID) > 100 {
		storeExtoreError(c, extore.ErrInput)
		return
	}
	catalog, err := service.MerchantStoreExtoreConnectionCatalog(c.Request.Context(), identity.UserID, c.Param("id"))
	if err != nil {
		storeExtoreError(c, err)
		return
	}
	for _, raw := range catalog.Products {
		var listing struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(raw, &listing) != nil {
			storeExtoreError(c, extore.ErrProtocol)
			return
		}
		if listing.ID != input.SourceID {
			continue
		}
		source, err := service.MerchantStoreExtoreSyncListing(identity.UserID, c.Param("id"), raw)
		if err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"success": false, "code": "STORE_EXTORE_SYNC_FAILED", "message": service.MerchantStoreExtoreSyncMessage(err)})
			return
		}
		merchantStoreRespond(c, source, nil)
		return
	}
	c.JSON(http.StatusForbidden, gin.H{"success": false, "code": "STORE_EXTORE_PRODUCT_NOT_APPROVED", "message": "This Extore product is not available in the current authorization"})
}
