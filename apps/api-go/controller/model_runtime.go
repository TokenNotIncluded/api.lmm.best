// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package controller

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
)

func GetModelRuntimeStates(c *gin.Context) {
	var input struct {
		Models []string `json:"models"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || len(input.Models) == 0 || len(input.Models) > 200 {
		common.ApiErrorMsg(c, "Provide between 1 and 200 model IDs")
		return
	}
	for _, name := range input.Models {
		if strings.TrimSpace(name) == "" || len(name) > 256 {
			common.ApiErrorMsg(c, "Invalid model ID")
			return
		}
	}
	authenticated, access := c.GetInt("id") > 0, false
	groups := map[string]string{}
	if authenticated {
		user, err := model.GetUserCache(c.GetInt("id"))
		if err != nil {
			common.ApiError(c, err)
			return
		}
		state, err := model.GetDeveloperAccessStateForUserBase(user)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		access = state.Granted
		groups = service.GetUserUsableGroups(user.Group)
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()
	states, err := model.ModelRuntimeStates(ctx, input.Models, groups, authenticated, access, common.GetTimestamp())
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": "Model runtime state is unavailable"})
		return
	}
	c.Header("Cache-Control", "private, no-store")
	common.ApiSuccess(c, states)
}
