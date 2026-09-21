package controller

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
)

const (
	promptPresetRefKey   = "assistant_pre_conversation_preset_attribution"
	promptPresetCountKey = "assistant_pre_conversation_preset_count_conversation"
)

func GetPromptPresets(c *gin.Context) {
	presets, err := model.GetPromptPresets()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.Header("Cache-Control", "public, max-age=300")
	// The explicit query parameter keeps public caches separated by language;
	// no user/session-specific data is included in this response.
	common.ApiSuccess(c, model.LocalizePromptPresets(presets, c.DefaultQuery("language", "zh")))
}

func CountPromptPresetClick(c *gin.Context) {
	if err := model.CountPresetClick(c.Param("id")); err != nil {
		if errors.Is(err, model.ErrPromptPresetNotFound) {
			c.AbortWithStatusJSON(http.StatusNotFound, gin.H{
				"success": false,
				"code":    "ASSISTANT_PRESET_NOT_FOUND",
				"message": "assistant preset was not found",
			})
			return
		}
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}

func capturePromptPresetRef(c *gin.Context, presetId string, prompt string) {
	if c == nil || strings.TrimSpace(presetId) == "" {
		return
	}
	attribution, err := model.ResolvePromptPreset(presetId, prompt)
	if err != nil {
		// The optional analytics hint never blocks chat and its prompt is never
		// logged. Unknown/stale IDs simply receive no attribution.
		return
	}
	c.Set(promptPresetRefKey, *attribution)
	c.Set(promptPresetCountKey, true)
}

func loadPromptPresetRef(c *gin.Context, conversationId int64) {
	if c == nil || conversationId <= 0 {
		return
	}
	attribution, err := model.ConversationPreset(conversationId)
	if err == nil && attribution != nil {
		c.Set(promptPresetRefKey, *attribution)
		// A transport retry can be the first successful response after the
		// initial upstream attempt failed. Count it, while the model-level
		// conversation attribution keeps ordinary replays idempotent.
		if c.GetBool("assistant_history_replay") {
			c.Set(promptPresetCountKey, true)
		}
	}
}

func promptPresetRef(c *gin.Context) (model.PromptPresetRef, bool) {
	if c == nil {
		return model.PromptPresetRef{}, false
	}
	value, exists := c.Get(promptPresetRefKey)
	if !exists {
		return model.PromptPresetRef{}, false
	}
	attribution, ok := value.(model.PromptPresetRef)
	return attribution, ok && attribution.PresetId != "" && attribution.Version != ""
}

func countPromptPresetConversation(c *gin.Context, conversationId int64) {
	if c == nil || !c.GetBool(promptPresetCountKey) {
		return
	}
	attribution, ok := promptPresetRef(c)
	if !ok {
		return
	}
	if err := model.CountPresetConversation(attribution, conversationId); err != nil {
		common.SysError(fmt.Sprintf("failed to record assistant preset conversation for %s: %v", attribution.PresetId, err))
	}
}
