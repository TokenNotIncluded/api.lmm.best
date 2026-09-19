package controller

import (
	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"strconv"
)

func AcquisitionFunnel(c *gin.Context) {
	from, _ := strconv.ParseInt(c.Query("from"), 10, 64)
	to, _ := strconv.ParseInt(c.Query("to"), 10, 64)
	days, err := strconv.Atoi(c.DefaultQuery("observation_days", "30"))
	if err != nil {
		common.ApiError(c, model.ErrAcquisitionInvalid)
		return
	}
	result, err := model.GetAcquisitionFunnel(c.Request.Context(), from, to, days, model.AcquisitionFunnelFilter{Source: c.Query("source"), Campaign: c.Query("campaign"), Content: c.Query("content"), ConnectionMethod: c.Query("connection_method")})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}
