package controller

import (
	"encoding/json"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
)

func SetMerchantStoreProductSaleLimit(c *gin.Context) {
	var input struct {
		SaleLimit *int64 `json:"sale_limit"`
	}
	if c.ShouldBindBodyWith(&input, binding.JSON) != nil {
		merchantStoreRespond(c, nil, model.ErrMerchantStoreInput)
		return
	}
	// A null value explicitly clears the limit. Missing fields never do so.
	var fields map[string]json.RawMessage
	body, _ := c.Get(gin.BodyBytesKey)
	data, ok := body.([]byte)
	if !ok || json.Unmarshal(data, &fields) != nil {
		merchantStoreRespond(c, nil, model.ErrMerchantStoreInput)
		return
	}
	if _, present := fields["sale_limit"]; !present {
		merchantStoreRespond(c, nil, model.ErrMerchantStoreInput)
		return
	}
	merchantStoreRespond(c, nil, model.SetMerchantStoreProductSaleLimit(c.GetInt("id"), c.Param("id"), input.SaleLimit))
}

func SetMerchantStoreProductListed(c *gin.Context) {
	var input struct {
		Listed *bool `json:"listed"`
	}
	if c.ShouldBindJSON(&input) != nil || input.Listed == nil {
		merchantStoreRespond(c, nil, model.ErrMerchantStoreInput)
		return
	}
	merchantStoreRespond(c, nil, model.SetMerchantStoreProductListed(c.GetInt("id"), c.Param("id"), *input.Listed))
}
