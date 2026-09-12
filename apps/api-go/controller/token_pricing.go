package controller

import (
	"math"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting/billing_setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/dynamic_pricing_setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	"github.com/gin-gonic/gin"
)

type tokenPricingEntry struct {
	Model            string   `json:"model"`
	Group            string   `json:"group"`
	Currency         string   `json:"currency"`
	BillingMode      string   `json:"billing_mode"`
	Unit             string   `json:"unit"`
	GroupRatio       float64  `json:"group_ratio"`
	TrustDiscount    float64  `json:"trust_discount_ratio"`
	ModelRatio       float64  `json:"model_ratio,omitempty"`
	CompletionRatio  float64  `json:"completion_ratio,omitempty"`
	CacheRatio       float64  `json:"cache_ratio,omitempty"`
	CreateCacheRatio float64  `json:"create_cache_ratio,omitempty"`
	InputPrice       *float64 `json:"input_price,omitempty"`
	OutputPrice      *float64 `json:"output_price,omitempty"`
	RequestPrice     *float64 `json:"request_price,omitempty"`
	Expression       string   `json:"billing_expression,omitempty"`
	DynamicPricing   bool     `json:"dynamic_pricing"`
}

// Only disclose prices for the same token group/model boundary as /v1/models.
// Rate values are read from the live billing settings, not a stale price cache.
func tokenPricingEntries(catalog []model.Pricing, groups modelListGroups, limited bool, limits map[string]bool, discount float64, requested string) []tokenPricingEntry {
	result := make([]tokenPricingEntry, 0)
	seen := make(map[string]bool)
	for _, item := range catalog {
		name := item.ModelName
		if requested != "" && requested != name {
			continue
		}
		if limited && !limits[name] && !limits[ratio_setting.FormatMatchingModelName(name)] {
			continue
		}
		for _, group := range groups.ownerGroups {
			if !slices.Contains(item.EnableGroup, group) && !slices.Contains(item.EnableGroup, "all") {
				continue
			}
			key := name + "\x00" + group
			if seen[key] {
				continue
			}
			seen[key] = true
			ratio, ok := ratio_setting.GetGroupGroupRatio(groups.userGroup, group)
			if !ok {
				ratio = ratio_setting.GetGroupRatio(group)
			}
			ratio *= discount
			entry := tokenPricingEntry{Model: name, Group: group, Currency: "USD", GroupRatio: ratio, TrustDiscount: discount, DynamicPricing: dynamic_pricing_setting.IsEnabled()}
			if billing_setting.GetBillingMode(name) == billing_setting.BillingModeTieredExpr {
				entry.BillingMode, entry.Unit = "tiered_expr", "expression"
				entry.Expression, ok = billing_setting.GetBillingExpr(name)
				if !ok {
					continue
				}
			} else if price, configured := ratio_setting.GetModelPrice(name, false); configured {
				entry.BillingMode, entry.Unit = "per_request", "request"
				cost := price * ratio
				entry.RequestPrice = &cost
			} else {
				entry.ModelRatio, ok, _ = ratio_setting.GetModelRatio(name)
				if !ok {
					continue
				}
				entry.BillingMode, entry.Unit = "per_token", "million_tokens"
				entry.CompletionRatio = ratio_setting.GetCompletionRatio(name)
				entry.CacheRatio, _ = ratio_setting.GetCacheRatio(name)
				entry.CreateCacheRatio, _ = ratio_setting.GetCreateCacheRatio(name)
				input := entry.ModelRatio * ratio * 1_000_000 / common.QuotaPerUnit
				output := input * entry.CompletionRatio
				entry.InputPrice, entry.OutputPrice = &input, &output
			}
			result = append(result, entry)
		}
	}
	return result
}

func GetTokenPricing(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	requested := strings.TrimSpace(c.Query("model"))
	if len(requested) > 512 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "model query is too long"})
		return
	}
	groups, err := getModelListGroups(c)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": "pricing context unavailable"})
		return
	}
	trust, err := model.GetTrustLevelInfoByUserID(c.GetInt("id"))
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": "pricing context unavailable"})
		return
	}
	catalog := getPricingCache()
	if catalog == nil || common.QuotaPerUnit <= 0 || math.IsNaN(common.QuotaPerUnit) || math.IsInf(common.QuotaPerUnit, 0) {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": "pricing unavailable"})
		return
	}
	var limits map[string]bool
	if raw, ok := common.GetContextKey(c, constant.ContextKeyTokenModelLimit); ok {
		limits, _ = raw.(map[string]bool)
	}
	entries := tokenPricingEntries(catalog, groups, common.GetContextKeyBool(c, constant.ContextKeyTokenModelLimitEnabled), limits, trust.DiscountRatio, requested)
	if requested != "" && len(entries) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "model not available"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": entries, "updated_at": time.Now().Unix(), "scope": "token", "price_basis": "configured_base_rates", "final_cost_depends_on_usage": true})
}
