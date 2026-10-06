package controller

import (
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
)

func RemoveToolMarketGrantRecord(c *gin.Context) {
	toolMarketRespond(c, nil, model.RemoveToolMarketGrantRecord(c.GetInt("id"), c.Param("id")))
}

func RemoveToolMarketTokenRecord(c *gin.Context) {
	toolMarketRespond(c, nil, model.RemoveToolMarketTokenRecord(c.GetInt("id"), c.Param("id")))
}

func RemoveToolMarketClient(c *gin.Context) {
	var input struct {
		ClientID string `json:"client_id"`
	}
	if c.ShouldBindJSON(&input) != nil {
		toolMarketRespond(c, nil, model.ErrToolMarketInput)
		return
	}
	result, err := model.RemoveToolMarketClient(c.GetInt("id"), input.ClientID)
	toolMarketRespond(c, result, err)
}
