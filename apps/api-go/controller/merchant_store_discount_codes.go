package controller

import (
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
)

func ListMerchantStoreDiscountCodes(c *gin.Context) {
	offset, limit, ok := merchantStorePage(c)
	if !ok {
		return
	}
	items, total, err := model.ListMerchantStoreDiscountCodes(c.GetInt("id"), c.Param("id"), offset, limit)
	if items == nil {
		items = []model.MerchantStoreDiscountCode{}
	}
	merchantStoreRespond(c, gin.H{"items": items, "offset": offset, "limit": limit, "has_more": int64(offset)+int64(len(items)) < total}, err)
}

func SaveMerchantStoreDiscountCode(c *gin.Context) {
	var input model.MerchantStoreDiscountCodeInput
	if c.ShouldBindJSON(&input) != nil {
		merchantStoreRespond(c, nil, model.ErrMerchantStoreInput)
		return
	}
	value, err := model.SaveMerchantStoreDiscountCode(c.GetInt("id"), c.Param("id"), c.Param("promotion_id"), input)
	merchantStoreRespond(c, value, err)
}

func DeleteMerchantStoreDiscountCode(c *gin.Context) {
	affected, err := model.BatchMerchantStoreDiscountCodes(c.GetInt("id"), c.Param("id"), []string{c.Param("promotion_id")}, "delete")
	merchantStoreRespond(c, gin.H{"affected": affected}, err)
}

func BatchMerchantStoreDiscountCodes(c *gin.Context) {
	var input struct {
		IDs    []string `json:"ids"`
		Action string   `json:"action"`
	}
	if c.ShouldBindJSON(&input) != nil {
		merchantStoreRespond(c, nil, model.ErrMerchantStoreInput)
		return
	}
	affected, err := model.BatchMerchantStoreDiscountCodes(c.GetInt("id"), c.Param("id"), input.IDs, input.Action)
	merchantStoreRespond(c, gin.H{"affected": affected}, err)
}

func CleanupMerchantStoreDiscountCodes(c *gin.Context) {
	var input struct {
		Limit int `json:"limit"`
	}
	if c.ShouldBindJSON(&input) != nil {
		merchantStoreRespond(c, nil, model.ErrMerchantStoreInput)
		return
	}
	if input.Limit == 0 {
		input.Limit = 100
	}
	if input.Limit < 1 || input.Limit > 100 {
		merchantStoreRespond(c, nil, model.ErrMerchantStoreInput)
		return
	}
	deleted, err := model.CleanupMerchantStoreDiscountCodes(c.GetInt("id"), c.Param("id"), input.Limit)
	merchantStoreRespond(c, gin.H{"deleted": deleted}, err)
}

func ResolveMerchantStoreDiscountCode(c *gin.Context) {
	promotion, err := model.ResolveMerchantStoreDiscountCode(c.GetInt("id"), c.Param("id"), c.Query("code"))
	if err != nil {
		merchantStoreRespond(c, nil, err)
		return
	}
	// Public links reveal only the offer, never ownership, usage or stock data.
	merchantStoreRespond(c, gin.H{"product_id": promotion.ProductID, "code": promotion.Code, "discount_bps": promotion.DiscountBPS, "variant_ids": promotion.VariantIDs, "expires_at": promotion.ExpiresAt, "status": promotion.Status}, nil)
}

func QuoteMerchantStoreDiscountCode(c *gin.Context) {
	var input struct {
		PromotionCode string `json:"promotion_code"`
		VariantID     string `json:"variant_id"`
		Quantity      int    `json:"quantity"`
	}
	if c.ShouldBindJSON(&input) != nil {
		merchantStoreRespond(c, nil, model.ErrMerchantStoreInput)
		return
	}
	quote, err := model.QuoteMerchantStoreDiscountCode(c.GetInt("id"), c.Param("id"), input.PromotionCode, input.VariantID, input.Quantity)
	if err == nil && !quote.Free {
		product := model.MerchantStoreProduct{SellerID: quote.SellerID, PaymentMethods: quote.PaymentMethods}
		service.FilterMerchantStorePublicPaymentMethods(&product)
		quote.PaymentMethods = product.PaymentMethods
		if len(quote.PaymentMethods) == 0 {
			quote.CheckoutAllowed = false
			quote.MaxQuantity = 0
		} else {
			balance := false
			for _, method := range quote.PaymentMethods {
				balance = balance || method == "balance"
			}
			if !balance {
				quote.MaxQuantity = min(quote.MaxQuantity, 100)
			}
			if quote.Quantity > quote.MaxQuantity {
				quote.CheckoutAllowed = false
			}
		}
	}
	merchantStoreRespond(c, quote, err)
}
