package controller

import (
	"net/http"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
)

type drawingMCPTokenRequest struct {
	ApiKeyId     int    `json:"api_key_id"`
	DefaultModel string `json:"default_model,omitempty"`
}

func GetDrawingMCPAPIKeys(c *gin.Context) {
	keys, err := model.ListDrawingMCPAPIKeys(c.GetInt("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"keys": keys})
}

func GetDrawingMCPToken(c *gin.Context) {
	status, err := model.GetDrawingMCPTokenStatus(c.GetInt("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"status": status, "endpoint": "/mcp/drawing"})
}

func RotateDrawingMCPToken(c *gin.Context) {
	var req drawingMCPTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.ApiKeyId <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "api_key_id is required"})
		return
	}
	user, err := drawingMCPAuthorizedUser(c.GetInt("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	group, err := drawingMCPBoundGroup(user, req.ApiKeyId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	defaultModel := strings.TrimSpace(req.DefaultModel)
	if defaultModel != "" {
		resolved, resolveErr := drawingMCPResolveInput(user, drawingMCPGenerateInput{Prompt: "default model validation", Group: group, Model: defaultModel, N: 1})
		if resolveErr != nil {
			common.ApiError(c, resolveErr)
			return
		}
		if err := drawingMCPValidateBoundModel(user.Id, req.ApiKeyId, group, resolved.Model); err != nil {
			common.ApiError(c, err)
			return
		}
	}
	token, status, err := model.RotateDrawingMCPToken(c.GetInt("id"), req.ApiKeyId, defaultModel)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"token": token, "status": status, "endpoint": "/mcp/drawing"})
}

func RevokeDrawingMCPToken(c *gin.Context) {
	if err := model.RevokeDrawingMCPToken(c.GetInt("id")); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}
