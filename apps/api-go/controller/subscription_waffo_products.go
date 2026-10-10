package controller

import (
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/gin-gonic/gin"
)

type WaffoPancakePurchaseOption struct {
	ProductType string                       `json:"product_type"`
	AutoRenew   bool                         `json:"auto_renew"`
	Settlement  *WaffoPancakeSettlementQuote `json:"settlement"`
}

func subscriptionWaffoPancakePurchaseOptions(plan *model.SubscriptionPlan, currency string) []WaffoPancakePurchaseOption {
	options := make([]WaffoPancakePurchaseOption, 0, 2)
	for _, product := range model.EnabledWaffoPancakeProducts(plan.WaffoPancakeBindings()) {
		selected, err := plan.WaffoPancakePurchasePlan(product.ProductType)
		if err != nil {
			continue
		}
		options = append(options, WaffoPancakePurchaseOption{
			ProductType: product.ProductType,
			AutoRenew:   product.ProductType == model.WaffoPancakeProductTypeSubscription,
			Settlement:  subscriptionWaffoPancakeSettlementQuote(selected, currency),
		})
	}
	return options
}

// Keep the old card preview usable. Checkout still requires an explicit mode
// when both products are enabled, and the new dialog uses the per-mode quotes.
func firstWaffoPancakeSettlement(options []WaffoPancakePurchaseOption) *WaffoPancakeSettlementQuote {
	for _, option := range options {
		if option.Settlement != nil && option.Settlement.Available {
			return option.Settlement
		}
	}
	if len(options) > 0 {
		return options[0].Settlement
	}
	return nil
}

func waffoPancakeProductMustBeRecreated(existing, next *model.SubscriptionPlan) bool {
	if existing == nil || next == nil {
		return false
	}
	for _, before := range existing.WaffoPancakeBindings() {
		for _, after := range next.WaffoPancakeBindings() {
			if before.ProductID == "" || before.ProductID != after.ProductID {
				continue
			}
			oldPlan, newPlan := *existing, *next
			oldPlan.WaffoPancakeProductId, oldPlan.WaffoPancakeProductType = before.ProductID, before.ProductType
			newPlan.WaffoPancakeProductId, newPlan.WaffoPancakeProductType = after.ProductID, after.ProductType
			if waffoPancakeSingleProductMustBeRecreated(&oldPlan, &newPlan) {
				return true
			}
		}
	}
	return false
}

// This is a provider-specific plan setting, not a rule for Stripe/Creem/ePay.
func prepareWaffoPancakePlanProducts(c *gin.Context, next, existing *model.SubscriptionPlan) bool {
	if next.WaffoPancakeProducts == nil && existing != nil && existing.WaffoPancakeProducts != nil {
		next.WaffoPancakeProducts = existing.WaffoPancakeBindings()
	}
	if next.WaffoPancakeProducts == nil {
		return true // Older single-product admin requests keep their contract.
	}
	products, err := model.ValidateWaffoPancakeProducts(next.WaffoPancakeProducts)
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return false
	}
	next.WaffoPancakeProducts = products
	available := model.EnabledWaffoPancakeProducts(products)
	// The two legacy columns are only a read projection for older clients.
	// New configuration, authorization, and checkout use the product list.
	next.WaffoPancakeProductId = ""
	next.WaffoPancakeProductType = model.WaffoPancakeProductTypeSubscription
	if len(available) == 0 {
		return true
	}
	merchantID, privateKey := service.WaffoPancakeCredentials()
	storeID := strings.TrimSpace(setting.WaffoPancakeStoreID)
	if merchantID == "" || privateKey == "" || storeID == "" {
		common.ApiErrorMsg(c, "Waffo Pancake 未完成配置")
		return false
	}
	catalog, err := service.ListWaffoPancakeCatalog(c.Request.Context(), merchantID, privateKey)
	if err != nil {
		common.ApiErrorMsg(c, "无法核验 Waffo Pancake 套餐商品")
		return false
	}
	for _, product := range available {
		active := false
		switch product.ProductType {
		case model.WaffoPancakeProductTypeOneTime:
			active = service.WaffoPancakeCatalogHasActiveOneTimeProduct(catalog, storeID, product.ProductID)
		case model.WaffoPancakeProductTypeSubscription:
			active = service.WaffoPancakeCatalogHasActiveSubscriptionProduct(catalog, storeID, product.ProductID)
		}
		if !active {
			common.ApiErrorMsg(c, "套餐绑定的 Waffo Pancake 商品类型不匹配、无效或未启用")
			return false
		}
	}
	next.WaffoPancakeProductId = available[0].ProductID
	next.WaffoPancakeProductType = available[0].ProductType
	return true
}
