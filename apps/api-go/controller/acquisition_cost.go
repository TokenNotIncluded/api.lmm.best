package controller

import (
	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"strconv"
)

func GetAcquisitionCost(c *gin.Context) {
	from, _ := strconv.ParseInt(c.Query("from"), 10, 64)
	to, _ := strconv.ParseInt(c.Query("to"), 10, 64)
	days, _ := strconv.Atoi(c.Query("observation_days"))
	report, err := model.GetAcquisitionCostReport(c.Request.Context(), model.AcquisitionCostScope{LinkID: c.Query("link_id"), FromAt: from, ToAt: to, ObservationDays: days, Currency: c.Query("currency")})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, report)
}
func SaveAcquisitionCost(c *gin.Context) {
	var input struct {
		model.AcquisitionCostScope
		AmountMicros *int64 `json:"amount_micros"`
	}
	if c.ShouldBindJSON(&input) != nil || input.AmountMicros == nil {
		common.ApiError(c, model.ErrAcquisitionInvalid)
		return
	}
	saved, err := model.SaveAcquisitionCost(c.Request.Context(), input.AcquisitionCostScope, *input.AmountMicros, c.GetInt("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAuditFor(c, 0, "acquisition.cost", map[string]interface{}{"link_id": saved.LinkID, "from": saved.FromAt, "to": saved.ToAt, "observation_days": saved.ObservationDays, "currency": saved.Currency, "amount_micros": saved.AmountMicros})
	common.ApiSuccess(c, saved)
}
