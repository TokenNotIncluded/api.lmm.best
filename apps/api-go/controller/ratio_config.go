package controller

import (
	"net/http"

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

	c.JSON(http.StatusOK, gin.H{
		"success":               true,
		"pricing_storage_basis": "legacy_pricing_unit",
		"message":               "",
		"data":                  ratio_setting.GetExposedData(),
	})
}
