package controller

import (
	"net/http"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

func GetRatioConfig(c *gin.Context) {
	if !ratio_setting.IsExposeRatioEnabled() {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "倍率配置接口未启用",
		})
		return
	}

	config, err := model.GetUSDPriceConfig()
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": "pricing currency units are unavailable"})
		return
	}
	data, err := pricingSyncDataFromConfig(config)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.Header("Cache-Control", "no-store")
	response := gin.H{
		"success":                       true,
		"pricing_schema_version":        model.PricingSchemaUSD,
		"pricing_currency":              model.PricingCurrencyUSD,
		"pricing_storage_basis":         config.StorageBasis,
		"credits_per_usd":               config.CreditsPerUSD,
		"legacy_pricing_units_per_usd":  config.LegacyPricingUnitsPerUSD,
		"legacy_pricing_quota_per_unit": config.CreditsPerUSD / config.LegacyPricingUnitsPerUSD,
		"model_ratio_usd_per_million":   config.ModelRatioUSDPerMillion,
		"message":                       "",
		"data":                          data,
		"model_ratio_unit":              "LEDGER_QUOTA_PER_TOKEN",
	}
	if err := addCreditUnitMetadata(response); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": "pricing currency units are unavailable"})
		return
	}
	c.JSON(http.StatusOK, response)
}
