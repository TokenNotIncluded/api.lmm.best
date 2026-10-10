package controller

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
)

func MerchantStoreEpayNotify(c *gin.Context) {
	if err := c.Request.ParseForm(); err != nil {
		c.String(http.StatusBadRequest, "fail")
		return
	}
	for _, values := range c.Request.Form {
		if len(values) != 1 {
			c.String(http.StatusBadRequest, "fail")
			return
		}
	}
	err := service.HandleMerchantStoreEpayCallback(c.Request.Context(), c.Param("id"), c.Request.Form)
	if err == nil || errors.Is(err, service.ErrMerchantStorePaymentIgnored) {
		c.String(http.StatusOK, "success")
		return
	}
	// Epay requires a plain acknowledgment. A browser return URL can never
	// call this handler's settlement path without a verified gateway signature.
	c.String(http.StatusBadRequest, "fail")
}

func MerchantStorePancakeWebhook(c *gin.Context) {
	sellerID, err := strconv.Atoi(c.Param("seller_id"))
	scope := c.Param("scope")
	if err != nil || (scope != "platform" && scope != "external") || (scope == "platform" && sellerID != 0) || (scope == "external" && sellerID < 1) {
		merchantStoreRespond(c, nil, model.ErrMerchantStoreInput)
		return
	}
	payload, err := io.ReadAll(c.Request.Body)
	if err != nil {
		merchantStoreRespond(c, nil, model.ErrMerchantStoreInput)
		return
	}
	err = service.HandleMerchantStorePancakeWebhook(c.Request.Context(), scope, sellerID, c.Param("env"), payload, c.GetHeader("X-Waffo-Signature"))
	if errors.Is(err, service.ErrMerchantStorePaymentIgnored) {
		err = nil
	}
	merchantStoreRespond(c, gin.H{"received": err == nil}, err)
}
