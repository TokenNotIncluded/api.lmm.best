package controller

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
)

func GetPublicCreditUnitOptions(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	units, err := model.GetPublicCreditUnitConfig()
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": "credit denomination is unavailable"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": units})
}

func PutPublicCreditUnitOptions(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, 4097))
	if err != nil || len(body) > 4096 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid credit denomination request"})
		return
	}
	fields, err := canonicalModelsDevObject(body)
	allowed := map[string]bool{"credit_unit_schema_version": true, "public_credits_per_usd_exact": true, "expected_public_credits_per_usd_exact": true, "expected_ledger_quota_per_usd_exact": true}
	if err == nil {
		for key := range fields {
			if !allowed[key] {
				err = errors.New("unexpected credit denomination field")
				break
			}
		}
	}
	var request model.PublicCreditUnitUpdate
	if err != nil || json.Unmarshal(body, &request) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid credit denomination request"})
		return
	}
	units, err := model.UpdatePublicCreditUnitConfig(request)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, model.ErrPublicCreditRevisionConflict) {
			status = http.StatusConflict
		}
		if errors.Is(err, common.ErrCreditUnitsUnavailable) || errors.Is(err, model.ErrPricingUnitsStale) {
			status = http.StatusServiceUnavailable
		}
		c.JSON(status, gin.H{"success": false, "message": err.Error()})
		return
	}
	recordManageAudit(c, "option.public_credit_unit_update", map[string]any{"credit_unit_schema_version": units.CreditUnitSchemaVersion, "public_credits_per_usd_exact": units.PublicCreditsPerUSDExact, "ledger_quota_per_usd_exact": units.LedgerQuotaPerUSDExact})
	c.JSON(http.StatusOK, gin.H{"success": true, "data": units})
}
