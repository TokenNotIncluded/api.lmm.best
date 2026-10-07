/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
package controller

import (
	"net/http"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
)

func GetAdminSiteStatistics(c *gin.Context) {
	// Keep the handler protected even if another caller mounts it outside the
	// finance router. The role comes from authoritative AdminAuth middleware.
	role := c.GetInt("role")
	if role != common.RoleAdminUser && role != common.RoleRootUser {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"success": false, "message": "Administrator access required"})
		return
	}
	statistics, err := model.GetAdminSiteStatistics(c.Request.Context())
	if err != nil {
		c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": "Site statistics are currently unavailable"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": statistics})
}
