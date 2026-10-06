package controller

import (
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
)

func SaveMerchantStoreVariant(c *gin.Context) {
	var input model.MerchantStoreVariantInput
	if c.ShouldBindJSON(&input) != nil {
		merchantStoreRespond(c, nil, model.ErrMerchantStoreInput)
		return
	}
	v, err := model.SaveMerchantStoreVariant(c.GetInt("id"), c.Param("id"), c.Param("variant_id"), input)
	if err == nil {
		v, err = model.GetMerchantStoreVariant(c.GetInt("id"), c.Param("id"), v.ID)
	}
	merchantStoreRespond(c, v, err)
}

func SetMerchantStoreVariantEnabled(c *gin.Context) {
	var input struct {
		Enabled *bool `json:"enabled"`
	}
	if c.ShouldBindJSON(&input) != nil || input.Enabled == nil {
		merchantStoreRespond(c, nil, model.ErrMerchantStoreInput)
		return
	}
	err := model.SetMerchantStoreVariantEnabled(c.GetInt("id"), c.Param("id"), c.Param("variant_id"), *input.Enabled)
	var variant *model.MerchantStoreVariant
	if err == nil {
		variant, err = model.GetMerchantStoreVariant(c.GetInt("id"), c.Param("id"), c.Param("variant_id"))
	}
	merchantStoreRespond(c, variant, err)
}

func AddMerchantStoreVariantInventory(c *gin.Context) {
	var input struct {
		Items []string `json:"items"`
	}
	if c.ShouldBindJSON(&input) != nil {
		merchantStoreRespond(c, nil, model.ErrMerchantStoreInput)
		return
	}
	count, err := model.AddMerchantStoreVariantStock(c.GetInt("id"), c.Param("id"), c.Param("variant_id"), input.Items)
	merchantStoreRespond(c, gin.H{"added": count}, err)
}

func ListMerchantStoreVariantInventory(c *gin.Context) {
	offset, limit, ok := merchantStorePage(c)
	if !ok {
		return
	}
	items, err := model.ListMerchantStoreVariantStock(c.GetInt("id"), c.Param("id"), c.Param("variant_id"), offset, limit)
	merchantStoreList(c, items, offset, limit, err)
}

func DeleteMerchantStoreVariantInventory(c *gin.Context) {
	merchantStoreRespond(c, nil, model.RemoveMerchantStoreVariantStock(c.GetInt("id"), c.Param("id"), c.Param("variant_id"), c.Param("stock_id")))
}
