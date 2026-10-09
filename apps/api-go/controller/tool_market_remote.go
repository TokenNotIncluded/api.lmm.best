package controller

import (
	"encoding/json"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/internal/marketprovider"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
)

func InspectToolMarketRemote(c *gin.Context) {
	var input struct {
		Endpoint string `json:"endpoint"`
	}
	if c.ShouldBindJSON(&input) != nil {
		toolMarketRespond(c, nil, model.ErrToolMarketInput)
		return
	}
	tools, err := service.InspectToolMarketRemote(c.Request.Context(), input.Endpoint)
	toolMarketRespond(c, tools, err)
}

func ValidateToolMarketRemote(c *gin.Context) {
	toolMarketRespond(c, nil, service.ValidateToolMarketRemote(c.Request.Context(), c.GetInt("id"), c.Param("id"), false))
}

func GetToolMarketReview(c *gin.Context) {
	detail, err := model.GetToolMarketReview(c.GetInt("id"), c.Param("id"))
	toolMarketRespond(c, detail, err)
}
func ListToolMarketReviews(c *gin.Context) {
	items, err := model.ListToolMarketReviewQueue(c.GetInt("id"))
	toolMarketRespond(c, items, err)
}

func InvokeToolMarket(c *gin.Context) {
	var input struct {
		ToolID     string          `json:"tool_id"`
		VersionID  string          `json:"version_id"`
		GrantID    string          `json:"grant_id"`
		RequestKey string          `json:"request_id"`
		Arguments  json.RawMessage `json:"arguments"`
	}
	if c.ShouldBindJSON(&input) != nil {
		toolMarketRespond(c, nil, model.ErrToolMarketInput)
		return
	}
	response, err := service.ExecuteToolMarketRemote(c.Request.Context(), model.ToolMarketReserveInput{UserID: c.GetInt("id"), ClientID: model.ToolMarketWebClient, ToolID: input.ToolID, VersionID: input.VersionID, GrantID: input.GrantID, RequestKey: input.RequestKey, Arguments: input.Arguments})
	toolMarketRespond(c, response, err)
}

func GetToolMarketCallResult(c *gin.Context) {
	response, err := service.GetToolMarketExecutionResponse(c.GetInt("id"), "", c.Param("id"))
	toolMarketRespond(c, response, err)
}

func GetToolMarketConfig(c *gin.Context) {
	config, err := model.GetToolMarketConfig()
	if err != nil {
		toolMarketRespond(c, nil, err)
		return
	}
	basis, err := captureCreditBoundaryBasis()
	if err != nil {
		toolMarketRespond(c, nil, err)
		return
	}
	data := gin.H{"enabled": config.Enabled, "builtin_enabled": true, "fee_bps": config.FeeBPS, "recipient_id": config.RecipientID, "quota_per_unit": common.QuotaPerUnit, "credits_per_usd": basis.Metadata.LedgerQuotaPerUSDExact, "usage_policy": "tool_reported", "web_client_id": model.ToolMarketWebClient, "mcp_path": "/mcp/market", "result_retention_seconds": 3600, "confirmation_timeout_seconds": 120}
	// API capabilities follow the handlers shipped by this server. They are not
	// saved settings; older servers omit them and clients must default to false.
	data["capabilities"] = gin.H{"service_deletion": true, "client_record_cleanup": true, "meta_delegation": true, "mcp_oauth": service.CurrentOAuthIntegration() != nil}
	data["provider_presets"] = marketprovider.Presets()
	basis.addMetadata(data)
	toolMarketRespond(c, data, nil)
}

func CreateToolMarketToken(c *gin.Context) {
	var input struct {
		ClientID  string `json:"client_id"`
		CanInvoke bool   `json:"can_invoke"`
		CanManage bool   `json:"can_manage"`
		ExpiresAt int64  `json:"expires_at"`
	}
	if c.ShouldBindJSON(&input) != nil {
		toolMarketRespond(c, nil, model.ErrToolMarketInput)
		return
	}
	raw, token, err := model.CreateToolMarketToken(c.GetInt("id"), input.ClientID, input.CanInvoke, input.CanManage, input.ExpiresAt)
	toolMarketRespond(c, gin.H{"token": raw, "record": token}, err)
}

func RevokeToolMarketToken(c *gin.Context) {
	toolMarketRespond(c, nil, model.RevokeToolMarketToken(c.GetInt("id"), c.Param("id")))
}

func SetToolMarketPaused(c *gin.Context) {
	var input struct {
		Paused bool `json:"paused"`
	}
	if c.ShouldBindJSON(&input) != nil {
		toolMarketRespond(c, nil, model.ErrToolMarketInput)
		return
	}
	toolMarketRespond(c, nil, model.SetToolMarketPaused(c.GetInt("id"), c.Param("id"), input.Paused))
}
