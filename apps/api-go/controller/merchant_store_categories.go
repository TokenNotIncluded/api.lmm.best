package controller

import (
	"encoding/json"
	"io"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
)

func ListMerchantStoreCategories(c *gin.Context) {
	listMerchantStoreCategories(c, false)
}

func ListAdminMerchantStoreCategories(c *gin.Context) {
	listMerchantStoreCategories(c, true)
}

func listMerchantStoreCategories(c *gin.Context, includeInactive bool) {
	offset, limit, ok := merchantStorePage(c)
	if !ok {
		return
	}
	supported := model.MerchantStoreCategoriesSupported()
	items := []model.MerchantStoreCategory{}
	var err error
	if supported {
		items, err = model.ListMerchantStoreCategories(c.GetInt("id"), includeInactive, offset, limit)
		if items == nil {
			items = []model.MerchantStoreCategory{}
		}
	}
	merchantStoreRespond(c, gin.H{"supported": supported, "items": items, "offset": offset, "limit": limit, "has_more": len(items) == limit}, err)
}

func SaveMerchantStoreCategory(c *gin.Context) {
	var input model.MerchantStoreCategoryInput
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || decoder.Decode(new(any)) != io.EOF {
		merchantStoreRespond(c, nil, model.ErrMerchantStoreInput)
		return
	}
	category, err := model.SaveMerchantStoreCategory(c.GetInt("id"), c.Param("id"), input)
	merchantStoreRespond(c, category, err)
}

func SetMerchantStoreProductCategory(c *gin.Context) {
	var input struct {
		CategoryID *string `json:"category_id"`
	}
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || decoder.Decode(new(any)) != io.EOF || input.CategoryID == nil {
		merchantStoreRespond(c, nil, model.ErrMerchantStoreInput)
		return
	}
	product, err := model.SetMerchantStoreProductCategory(c.GetInt("id"), c.Param("id"), *input.CategoryID)
	merchantStoreRespond(c, product, err)
}
