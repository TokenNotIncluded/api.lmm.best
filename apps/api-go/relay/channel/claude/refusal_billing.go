package claude

import (
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/setting/model_setting"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const claudeRefusalStateKey = "claude_refusal_billing_state"

// Keep measured upstream evidence separate from usage estimated or converted
// for a client protocol. In particular, a missing output_tokens field must not
// become evidence of a free refusal just because the Go zero value is zero.
type claudeRefusalState struct {
	stopReason            string
	finalStopReason       string
	messageStopped        bool
	hasOutput             bool
	hasZeroOutput         bool
	hasNonzeroOutput      bool
	hasBillableCategory   bool
	hasAdditionalAttempts bool
}

func resetClaudeRefusalBilling(c *gin.Context) {
	c.Set(claudeRefusalStateKey, &claudeRefusalState{})
	common.SetContextKey(c, constant.ContextKeyBillingExemptReason, "")
	maybeMarkClaudeRefusal(c, "")
}

func claudeRefusalBillingState(c *gin.Context) *claudeRefusalState {
	if state, ok := c.Get(claudeRefusalStateKey); ok {
		if state, ok := state.(*claudeRefusalState); ok {
			return state
		}
	}
	state := &claudeRefusalState{}
	c.Set(claudeRefusalStateKey, state)
	return state
}

func (s *claudeRefusalState) observeUsage(data, path string) {
	if len(gjson.Get(data, path+".iterations").Array()) > 0 {
		s.hasAdditionalAttempts = true
	}
	output := gjson.Get(data, path+".output_tokens")
	if output.Type != gjson.Number {
		return
	}
	if output.Float() == 0 {
		s.hasZeroOutput = true
	} else {
		s.hasNonzeroOutput = true
	}
}

func observeClaudeRefusal(c *gin.Context, response *dto.ClaudeResponse, data string) {
	state := claudeRefusalBillingState(c)
	if response.Type == "message_stop" {
		state.messageStopped = true
	}
	if response.Type == "content_block_start" || response.Type == "content_block_delta" || len(response.Content) > 0 {
		state.hasOutput = true
	}
	if response.Message != nil {
		content := gjson.Get(data, "message.content")
		state.hasOutput = state.hasOutput || len(content.Array()) > 0 || (content.Type == gjson.String && content.String() != "")
		state.observeUsage(data, "message.usage")
	}
	state.observeUsage(data, "usage")
	// Anthropic charges these categories even before output (September 2026).
	// Keep this upstream fact independent of client identity and local prices.
	for _, path := range []string{"stop_details.category", "delta.stop_details.category", "message.stop_details.category"} {
		category := gjson.Get(data, path)
		switch strings.ToLower(strings.TrimSpace(category.String())) {
		case "bio", "frontier_llm", "reasoning_extraction":
			state.hasBillableCategory = true
		}
		if category.Exists() && category.Type != gjson.String && category.Type != gjson.Null {
			state.hasBillableCategory = true
		}
	}
	stopReason := response.StopReason
	if response.Delta != nil && response.Delta.StopReason != nil {
		stopReason = *response.Delta.StopReason
	}
	if stopReason != "" {
		state.stopReason = stopReason
		if response.Type == "message_delta" {
			state.finalStopReason = stopReason
		}
		maybeMarkClaudeRefusal(c, stopReason)
	}
}

func markClaudeRefusalBillingExemption(c *gin.Context) {
	state := claudeRefusalBillingState(c)
	if model_setting.GetClaudeSettings().RefusalNoOutputNoChargeEnabled &&
		strings.EqualFold(state.stopReason, "refusal") &&
		!state.hasOutput && state.hasZeroOutput && !state.hasNonzeroOutput &&
		!state.hasBillableCategory && !state.hasAdditionalAttempts {
		common.SetContextKey(c, constant.ContextKeyBillingExemptReason, constant.BillingExemptReasonClaudeRefusalNoOutput)
	}
}
