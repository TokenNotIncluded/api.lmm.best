package controller

import (
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
)

func ListToolMarketAccountResources(c *gin.Context) {
	offset, limit, ok := toolMarketPage(c)
	if !ok {
		return
	}
	if c.Param("kind") == "oauth-clients" {
		integration := service.CurrentOAuthIntegration()
		if integration == nil {
			toolMarketRespond(c, []model.ToolMarketOAuthClient{}, nil)
			return
		}
		rows, err := model.ListToolMarketOAuthClients(integration.DB.WithContext(c.Request.Context()), c.GetInt("id"), integration.Issuer, integration.Resource,
			[]string{service.OAuthPiClientID, service.OAuthDshClientID}, []string{service.OAuthMarketDiscoverScope, service.OAuthMarketInvokeScope}, offset, limit, integration.MarketResource())
		toolMarketRespond(c, rows, err)
		return
	}
	rows, err := model.ListToolMarketAccountResources(c.GetInt("id"), c.Param("kind"), offset, limit)
	toolMarketRespond(c, rows, err)
}

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
