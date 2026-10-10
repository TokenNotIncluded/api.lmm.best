package controller

import (
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
)

func ListMerchantStoreCart(c *gin.Context) {
	offset, limit, ok := merchantStorePage(c)
	if !ok {
		return
	}
	items, err := model.ListMerchantStoreCart(c.GetInt("id"), offset, limit)
	for i := range items {
		if items[i].Product != nil {
			service.FilterMerchantStorePublicPaymentMethods(items[i].Product)
		}
	}
	merchantStoreRespond(c, gin.H{"items": items, "offset": offset, "limit": limit, "has_more": len(items) == limit}, err)
}
func SetMerchantStoreCartItem(c *gin.Context) {
	var input model.MerchantStoreCartInput
	if err := c.ShouldBindJSON(&input); err != nil {
		merchantStoreRespond(c, nil, model.ErrMerchantStoreInput)
		return
	}
	item, err := model.SetMerchantStoreCartItem(c.GetInt("id"), input)
	merchantStoreRespond(c, item, err)
}
func DeleteMerchantStoreCartItem(c *gin.Context) {
	merchantStoreRespond(c, nil, model.DeleteMerchantStoreCartItem(c.GetInt("id"), c.Param("item_id")))
}
func ClearMerchantStoreCart(c *gin.Context) {
	merchantStoreRespond(c, nil, model.ClearMerchantStoreCollections(c.GetInt("id"), true))
}
func ListMerchantStoreFavorites(c *gin.Context) {
	offset, limit, ok := merchantStorePage(c)
	if !ok {
		return
	}
	items, err := model.ListMerchantStoreFavorites(c.GetInt("id"), offset, limit)
	for i := range items {
		if items[i].Product != nil {
			service.FilterMerchantStorePublicPaymentMethods(items[i].Product)
		}
	}
	merchantStoreRespond(c, gin.H{"items": items, "offset": offset, "limit": limit, "has_more": len(items) == limit}, err)
}
func SetMerchantStoreFavorite(c *gin.Context) {
	var input struct {
		ProductID string `json:"product_id"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		merchantStoreRespond(c, nil, model.ErrMerchantStoreInput)
		return
	}
	merchantStoreRespond(c, nil, model.SetMerchantStoreFavorite(c.GetInt("id"), input.ProductID))
}
func DeleteMerchantStoreFavorite(c *gin.Context) {
	merchantStoreRespond(c, nil, model.DeleteMerchantStoreFavorite(c.GetInt("id"), c.Param("product_id")))
}
func ClearMerchantStoreFavorites(c *gin.Context) {
	merchantStoreRespond(c, nil, model.ClearMerchantStoreCollections(c.GetInt("id"), false))
}
func CleanupMerchantStoreCollections(c *gin.Context) {
	carts, favorites, err := model.CleanupMerchantStoreCollections(c.GetInt("id"))
	merchantStoreRespond(c, gin.H{"cart_deleted": carts, "favorites_deleted": favorites}, err)
}
