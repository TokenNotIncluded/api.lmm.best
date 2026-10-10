package controller

import (
	"errors"
	"net/http"
	"sort"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
)

func GetUSDPriceOptions(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	config, err := model.GetUSDPriceConfig()
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": "pricing currency units are unavailable"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": config})
}

func decodeUSDPriceUpdate(c *gin.Context) (model.USDPriceUpdate, bool) {
	var request model.USDPriceUpdate
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid USD pricing request"})
		return request, false
	}
	if request.SchemaVersion != 2 || request.Currency != "USD" || len(request.ExpectedRevision) != 64 || len(request.Values) == 0 || len(request.Values) > 12 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "schema_version:2, currency:USD, expected_revision and pricing values are required"})
		return request, false
	}
	return request, true
}

func USDPriceOptionsValidate(c *gin.Context) { usdPriceOptionsWrite(c, false) }
func USDPriceOptionsBulk(c *gin.Context)     { usdPriceOptionsWrite(c, true) }
func usdPriceOptionsWrite(c *gin.Context, save bool) {
	c.Header("Cache-Control", "no-store")
	request, ok := decodeUSDPriceUpdate(c)
	if !ok {
		return
	}
	var config model.USDPriceConfig
	var result model.OptionUpdateResult
	var err error
	if save {
		config, result, err = model.UpdateUSDPriceConfig(request)
	} else {
		config, result, err = model.ValidateUSDPriceConfig(request)
	}
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, model.ErrPricingRevisionConflict) {
			status = http.StatusConflict
		}
		if errors.Is(err, common.ErrCreditUnitsUnavailable) || errors.Is(err, model.ErrPricingUnitsStale) {
			status = http.StatusServiceUnavailable
		}
		c.JSON(status, gin.H{"success": false, "message": err.Error()})
		return
	}
	if save {
		recordManageAudit(c, "option.pricing_usd_bulk_update", map[string]any{"keys": pricingOptionKeys(request.Values), "locked_models": result.LockedModels, "schema_version": 2, "currency": "USD"})
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": config, "warnings": result.Warnings, "locked_models": result.LockedModels})
}

func pricingOptionKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
