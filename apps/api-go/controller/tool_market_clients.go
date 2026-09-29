package controller

import (
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
)

func DisconnectToolMarketClient(c *gin.Context) {
	var input struct {
		ClientID string `json:"client_id"`
	}
	if c.ShouldBindJSON(&input) != nil {
		toolMarketRespond(c, nil, model.ErrToolMarketInput)
		return
	}
	result, err := model.DisconnectToolMarketClient(c.GetInt("id"), input.ClientID)
	toolMarketRespond(c, result, err)
}
