package service

import (
	"context"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/gin-gonic/gin"
)

// Internal assistant relays carry a platform billing identity and a system/tool
// transcript. They submit only the human's current turn through their own hook.
const ModerationRelaySkipContextKey = "moderation_skip_relay_input"

// EvaluateAdvancedSecurityText keeps the existing relay ingress contract while
// retiring synchronous literal-rule decisions. It persists only a background
// Moderation job; no OpenAI request or verdict is awaited by a user request.
func EvaluateAdvancedSecurityText(c *gin.Context, relayInfo *relaycommon.RelayInfo, text string) {
	if relayInfo == nil || strings.TrimSpace(text) == "" || (c != nil && c.GetBool(ModerationRelaySkipContextKey)) {
		return
	}
	requestID := strings.TrimSpace(relayInfo.RequestId)
	ctx := context.Background()
	if c != nil {
		if requestID == "" {
			requestID = strings.TrimSpace(c.GetString(common.RequestIdKey))
		}
		if c.Request != nil {
			ctx = c.Request.Context()
		}
	}
	if requestID == "" || relayInfo.UserId <= 0 || strings.TrimSpace(relayInfo.UserGroup) == "" {
		return
	}
	if err := QueueModeration(ctx, ModerationSubmission{
		UserID: relayInfo.UserId, Source: ModerationSourceRelayInput, RequestID: requestID,
		Group: relayInfo.UserGroup, RelayGroup: relayInfo.UsingGroup, Text: text,
	}); err != nil {
		common.SysError("relay_moderation_enqueue_unavailable")
	}
	return
}
