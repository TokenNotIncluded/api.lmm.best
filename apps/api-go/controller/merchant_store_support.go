// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package controller

import (
	"strconv"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
)

func ListMerchantStoreSupport(c *gin.Context) {
	offset, limit, ok := merchantStorePage(c)
	if !ok { return }
	unread := c.DefaultQuery("unread", "false")
	if unread != "true" && unread != "false" {
		merchantStoreRespond(c, nil, model.ErrMerchantStoreInput)
		return
	}
	rows, err := model.ListMerchantStoreSupport(c.Request.Context(), c.GetInt("id"), c.DefaultQuery("role", "buyer"), c.Query("status"), unread == "true", offset, limit)
	merchantStoreList(c, rows, offset, limit, err)
}

func OpenMerchantStoreSupport(c *gin.Context) {
	var in struct {
		ProductID string `json:"product_id"`
		OrderID string `json:"order_id"`
	}
	if c.ShouldBindJSON(&in) != nil {
		merchantStoreRespond(c, nil, model.ErrMerchantStoreInput)
		return
	}
	row, err := model.OpenMerchantStoreSupport(c.Request.Context(), c.GetInt("id"), in.ProductID, in.OrderID)
	merchantStoreRespond(c, row, err)
}

func GetMerchantStoreSupportHistory(c *gin.Context) {
	before, err := strconv.ParseInt(c.DefaultQuery("before", "0"), 10, 64)
	limit, limitErr := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if err != nil || limitErr != nil || before < 0 || limit < 1 || limit > 100 {
		merchantStoreRespond(c, nil, model.ErrMerchantStoreInput)
		return
	}
	row, err := model.GetMerchantStoreSupportHistory(c.Request.Context(), c.GetInt("id"), c.Param("id"), before, limit)
	merchantStoreRespond(c, row, err)
}

func SendMerchantStoreSupportMessage(c *gin.Context) {
	var in struct {
		RequestKey string `json:"request_key"`
		Body string `json:"body"`
	}
	if c.ShouldBindJSON(&in) != nil {
		merchantStoreRespond(c, nil, model.ErrMerchantStoreInput)
		return
	}
	row, err := model.SendMerchantStoreSupportMessage(c.Request.Context(), c.GetInt("id"), c.Param("id"), in.RequestKey, in.Body)
	merchantStoreRespond(c, row, err)
}

func MarkMerchantStoreSupportRead(c *gin.Context) {
	var in struct { ThroughID int64 `json:"through_id"` }
	if c.ShouldBindJSON(&in) != nil {
		merchantStoreRespond(c, nil, model.ErrMerchantStoreInput)
		return
	}
	err := model.MarkMerchantStoreSupportRead(c.Request.Context(), c.GetInt("id"), c.Param("id"), in.ThroughID)
	merchantStoreRespond(c, gin.H{}, err)
}

func SetMerchantStoreSupportStatus(c *gin.Context) {
	var in struct { Status string `json:"status"` }
	if c.ShouldBindJSON(&in) != nil {
		merchantStoreRespond(c, nil, model.ErrMerchantStoreInput)
		return
	}
	err := model.SetMerchantStoreSupportStatus(c.Request.Context(), c.GetInt("id"), c.Param("id"), in.Status)
	merchantStoreRespond(c, gin.H{}, err)
}

func ListMerchantStoreCustomers(c *gin.Context) {
	offset, limit, ok := merchantStorePage(c)
	if !ok { return }
	rows, err := model.ListMerchantStoreCustomers(c.Request.Context(), c.GetInt("id"), c.Query("q"), offset, limit)
	merchantStoreList(c, rows, offset, limit, err)
}

func SaveMerchantStoreCustomer(c *gin.Context) {
	buyer, err := strconv.Atoi(c.Param("buyer_id"))
	var in model.MerchantStoreCustomerInput
	if err != nil || buyer <= 0 || c.ShouldBindJSON(&in) != nil {
		merchantStoreRespond(c, nil, model.ErrMerchantStoreInput)
		return
	}
	err = model.SaveMerchantStoreCustomer(c.Request.Context(), c.GetInt("id"), buyer, in)
	merchantStoreRespond(c, gin.H{}, err)
}

func GetMerchantStoreSupportAssistantContext(c *gin.Context) {
	row, err := model.GetMerchantStoreSupportAssistantContext(c.Request.Context(), c.GetInt("id"), c.Param("id"))
	merchantStoreRespond(c, row, err)
}
