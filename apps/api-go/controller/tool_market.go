package controller

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func toolMarketRespond(c *gin.Context, value any, err error) {
	if err == nil {
		common.ApiSuccess(c, value)
		return
	}
	status, code, message := http.StatusInternalServerError, "TOOL_MARKET_UNAVAILABLE", "tool market operation could not be completed"
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		status, code, message = http.StatusNotFound, "TOOL_MARKET_NOT_FOUND", "tool market resource not found"
	case errors.Is(err, model.ErrToolMarketInput):
		status, code, message = http.StatusUnprocessableEntity, "TOOL_MARKET_INVALID_INPUT", err.Error()
	case errors.Is(err, model.ErrToolMarketDenied):
		status, code, message = http.StatusForbidden, "TOOL_MARKET_DENIED", err.Error()
	case errors.Is(err, model.ErrToolMarketConflict):
		status, code, message = http.StatusConflict, "TOOL_MARKET_CONFLICT", err.Error()
	case errors.Is(err, model.ErrToolMarketBudget):
		status, code, message = http.StatusConflict, "TOOL_MARKET_BUDGET", err.Error()
	case errors.Is(err, model.ErrToolMarketBalance):
		status, code, message = http.StatusConflict, "TOOL_MARKET_BALANCE", err.Error()
	case errors.Is(err, service.ErrMarketRemoteConnection), errors.Is(err, service.ErrMarketRemoteAuth), errors.Is(err, service.ErrMarketRemoteNetwork):
		status, code, message = http.StatusUnprocessableEntity, "TOOL_MARKET_REMOTE_CONNECTION", err.Error()
	case errors.Is(err, service.ErrMarketRemoteSchema), errors.Is(err, service.ErrMarketRemoteChanged):
		status, code, message = http.StatusConflict, "TOOL_MARKET_REMOTE_CHANGED", err.Error()
	case errors.Is(err, service.ErrMarketRemoteInput):
		status, code, message = http.StatusUnprocessableEntity, "TOOL_MARKET_ARGUMENTS", err.Error()
	case errors.Is(err, service.ErrMarketRemoteBusy):
		status, code, message = http.StatusTooManyRequests, "TOOL_MARKET_BUSY", err.Error()
	}
	// Never serialize a DB/remote error: it may contain SQL, private IDs or URLs.
	c.AbortWithStatusJSON(status, gin.H{"success": false, "code": code, "message": message})
}

func toolMarketPage(c *gin.Context) (int, int, bool) {
	offset, err := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if err != nil {
		toolMarketRespond(c, nil, model.ErrToolMarketInput)
		return 0, 0, false
	}
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if err != nil || offset < 0 || offset > 10000 || limit < 1 || limit > 100 {
		toolMarketRespond(c, nil, model.ErrToolMarketInput)
		return 0, 0, false
	}
	return offset, limit, true
}

func ListToolMarket(c *gin.Context) {
	offset, limit, ok := toolMarketPage(c)
	if !ok {
		return
	}
	rows, err := model.ListToolMarket(c.GetInt("id"), c.Query("q"), c.Query("execution_type"), offset, limit)
	toolMarketRespond(c, rows, err)
}

func GetToolMarket(c *gin.Context) {
	detail, err := model.GetToolMarketDetail(c.GetInt("id"), c.Param("id"), false)
	toolMarketRespond(c, detail, err)
}

func GetToolMarketDraft(c *gin.Context) {
	detail, err := model.GetToolMarketDetail(c.GetInt("id"), c.Param("id"), true)
	toolMarketRespond(c, detail, err)
}

func SaveToolMarketDraft(c *gin.Context) {
	var input model.ToolMarketDraftInput
	if c.ShouldBindJSON(&input) != nil {
		toolMarketRespond(c, nil, model.ErrToolMarketInput)
		return
	}
	service, err := model.SaveToolMarketDraft(c.GetInt("id"), c.Param("id"), input)
	toolMarketRespond(c, service, err)
}

func SubmitToolMarketDraft(c *gin.Context) {
	var input struct {
		VersionID string `json:"version_id"`
	}
	if c.ShouldBindJSON(&input) != nil {
		toolMarketRespond(c, nil, model.ErrToolMarketInput)
		return
	}
	toolMarketRespond(c, nil, model.SubmitToolMarketDraft(c.GetInt("id"), c.Param("id"), input.VersionID))
}

func ReviewToolMarketDraft(c *gin.Context) {
	var input struct {
		VersionID string `json:"version_id"`
		Approve   bool   `json:"approve"`
		Note      string `json:"note"`
	}
	if c.ShouldBindJSON(&input) != nil {
		toolMarketRespond(c, nil, model.ErrToolMarketInput)
		return
	}
	if input.Approve {
		if err := service.ValidateToolMarketRemote(c.Request.Context(), c.GetInt("id"), c.Param("id"), true); err != nil {
			toolMarketRespond(c, nil, err)
			return
		}
	}
	toolMarketRespond(c, nil, model.ReviewToolMarketVersion(c.GetInt("id"), c.Param("id"), input.VersionID, input.Approve, input.Note))
}

func SetToolMarketFavorite(c *gin.Context) {
	var input struct {
		Favorite bool `json:"favorite"`
	}
	if c.ShouldBindJSON(&input) != nil {
		toolMarketRespond(c, nil, model.ErrToolMarketInput)
		return
	}
	toolMarketRespond(c, nil, model.SetToolMarketFavorite(c.GetInt("id"), c.Param("id"), input.Favorite))
}

func SetToolMarketInstallation(c *gin.Context) {
	var input struct {
		ClientID  string `json:"client_id"`
		ToolID    string `json:"tool_id"`
		VersionID string `json:"version_id"`
		Loaded    bool   `json:"loaded"`
	}
	if c.ShouldBindJSON(&input) != nil {
		toolMarketRespond(c, nil, model.ErrToolMarketInput)
		return
	}
	toolMarketRespond(c, nil, model.SetToolMarketInstallation(c.GetInt("id"), input.ClientID, input.ToolID, input.VersionID, input.Loaded))
}

func CreateToolMarketGrant(c *gin.Context) {
	var input model.ToolMarketGrant
	if c.ShouldBindJSON(&input) != nil {
		toolMarketRespond(c, nil, model.ErrToolMarketInput)
		return
	}
	grant, err := model.CreateToolMarketGrant(c.GetInt("id"), input)
	toolMarketRespond(c, grant, err)
}

func RevokeToolMarketGrant(c *gin.Context) {
	toolMarketRespond(c, nil, model.RevokeToolMarketGrant(c.GetInt("id"), c.Param("id")))
}

func SetToolMarketBudget(c *gin.Context) {
	var input struct {
		Scope      string `json:"scope"`
		ScopeID    string `json:"scope_id"`
		LimitQuota int    `json:"limit_quota"`
	}
	if c.ShouldBindJSON(&input) != nil {
		toolMarketRespond(c, nil, model.ErrToolMarketInput)
		return
	}
	toolMarketRespond(c, nil, model.SetToolMarketBudget(c.GetInt("id"), input.Scope, input.ScopeID, input.LimitQuota))
}

func ListToolMarketCalls(c *gin.Context) {
	offset, limit, ok := toolMarketPage(c)
	if !ok {
		return
	}
	rows, err := model.ListToolMarketCalls(c.GetInt("id"), offset, limit)
	toolMarketRespond(c, rows, err)
}

func ListToolMarketIncome(c *gin.Context) {
	offset, limit, ok := toolMarketPage(c)
	if !ok {
		return
	}
	rows, err := model.ListToolMarketIncome(c.GetInt("id"), offset, limit)
	toolMarketRespond(c, rows, err)
}

func SetToolMarketConfig(c *gin.Context) {
	var input model.ToolMarketConfig
	if c.ShouldBindJSON(&input) != nil {
		toolMarketRespond(c, nil, model.ErrToolMarketInput)
		return
	}
	toolMarketRespond(c, nil, model.SetToolMarketConfig(c.GetInt("id"), input))
}

func ListToolMarketAccountResources(c *gin.Context) {
	offset, limit, ok := toolMarketPage(c)
	if !ok {
		return
	}
	rows, err := model.ListToolMarketAccountResources(c.GetInt("id"), c.Param("kind"), offset, limit)
	toolMarketRespond(c, rows, err)
}
