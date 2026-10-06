package controller

import (
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
)

func merchantStoreCatalogueBoolean(c *gin.Context, key string) (*bool, bool) {
	values, present := c.Request.URL.Query()[key]
	if !present {
		return nil, true
	}
	if len(values) != 1 || (values[0] != "true" && values[0] != "false") {
		merchantStoreRespond(c, nil, model.ErrMerchantStoreInput)
		return nil, false
	}
	value := values[0] == "true"
	return &value, true
}

func merchantStoreCatalogueQuery(c *gin.Context) (model.MerchantStoreCatalogueQuery, bool) {
	query := model.MerchantStoreCatalogueQuery{Sort: c.Query("sort"), Tag: c.Query("tag"), Stock: c.Query("stock")}
	for _, key := range []string{"sort", "tag", "stock"} {
		if len(c.Request.URL.Query()[key]) > 1 {
			merchantStoreRespond(c, nil, model.ErrMerchantStoreInput)
			return query, false
		}
	}
	var ok bool
	if query.AutoDelivery, ok = merchantStoreCatalogueBoolean(c, "auto_delivery"); !ok {
		return query, false
	}
	if query.AIProcessing, ok = merchantStoreCatalogueBoolean(c, "ai_processing"); !ok {
		return query, false
	}
	if query.GuestPurchase, ok = merchantStoreCatalogueBoolean(c, "guest_purchase"); !ok {
		return query, false
	}
	return query, true
}

func SetMerchantStoreCatalogueMetadata(c *gin.Context) {
	var input model.MerchantStoreCatalogueMetadata
	if err := c.ShouldBindJSON(&input); err != nil {
		merchantStoreRespond(c, nil, model.ErrMerchantStoreInput)
		return
	}
	err := model.SetMerchantStoreCatalogueMetadata(c.GetInt("id"), c.Param("id"), input)
	if err == nil {
		var stored model.MerchantStoreCatalogueMetadata
		err = model.DB.Where("product_id = ?", c.Param("id")).First(&stored).Error
		merchantStoreRespond(c, stored, err)
		return
	}
	merchantStoreRespond(c, nil, err)
}
