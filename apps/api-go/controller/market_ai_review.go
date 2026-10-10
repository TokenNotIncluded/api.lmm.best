package controller

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/gin-gonic/gin"
)

func marketAIReviewSettingsDTO(s model.MarketAIReviewSettings) gin.H {
	return gin.H{"tool_mode": s.ToolMode, "store_mode": s.StoreMode, "review_group": s.ReviewGroup, "review_model": s.ReviewModel, "engine": "openai_moderation", "supported_inputs": []string{"text"}, "categories": setting.ModerationCategories()}
}

func GetMarketAIReviewSettings(c *gin.Context) {
	s, err := model.ReadMarketAIReviewSettings(c.Request.Context())
	if err != nil {
		common.ApiErrorMsg(c, "market AI review settings are unavailable")
		return
	}
	common.ApiSuccess(c, marketAIReviewSettingsDTO(s))
}

func UpdateMarketAIReviewSettings(c *gin.Context) {
	var in struct {
		ToolMode  *string `json:"tool_mode"`
		StoreMode *string `json:"store_mode"`
	}
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&in) != nil || decoder.Decode(new(any)) != io.EOF || in.ToolMode == nil || in.StoreMode == nil || setting.ValidateMarketAIReviewMode(*in.ToolMode) != nil || setting.ValidateMarketAIReviewMode(*in.StoreMode) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "tool_mode and store_mode must be off, assist or auto"})
		return
	}
	if err := model.UpdateOptionsBulk(map[string]string{setting.ToolMarketAIReviewModeOptionKey: *in.ToolMode, setting.StoreAIReviewModeOptionKey: *in.StoreMode}); err != nil {
		common.ApiErrorMsg(c, "market AI review settings could not be saved; check the official moderation route")
		return
	}
	GetMarketAIReviewSettings(c)
}

func ListToolMarketAIReviews(c *gin.Context) {
	rows, err := model.ListMarketAIReviews(c.Request.Context(), c.GetInt("id"), model.ModerationSourceMarketTool, c.Param("id"), c.Query("version_id"))
	toolMarketRespond(c, gin.H{"rows": rows}, err)
}
func ListStoreAIReviews(c *gin.Context) {
	rows, err := model.ListMarketAIReviews(c.Request.Context(), c.GetInt("id"), model.ModerationSourceMarketProduct, c.Param("id"), "")
	if errors.Is(err, model.ErrToolMarketDenied) {
		err = model.ErrMerchantStoreDenied
	}
	merchantStoreRespond(c, gin.H{"rows": rows}, err)
}
