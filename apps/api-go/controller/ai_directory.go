/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
package controller

import (
	"net/http"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
)

func GetAIDirectory(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    gin.H{"links": model.PublicAIDirectoryLinks()},
	})
}
