package controller

import (
	"context"
	"net/http"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/gin-gonic/gin"
)

var assistantToolPolicyReader = model.ReadAssistantToolPolicy

// Direct helper calls without an authenticated HTTP actor use the supplied
// local settings. Authenticated production requests always read the database.
func refreshAssistantToolPolicy(c *gin.Context) (string, setting.AssistantToolPolicy, error) {
	if c == nil || c.Request == nil || assistantActorUserID(c) <= 0 {
		return setting.NormalizeAssistantToolPolicy(setting.GetAssistantSettings().ToolPolicy)
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	canonical, policy, err := assistantToolPolicyReader(ctx)
	if err != nil {
		return "", setting.AssistantToolPolicy{}, err
	}
	// Publish the validated snapshot for catalogue/choice filtering. Execution
	// uses this captured policy, independent of concurrent background refreshes.
	if err := setting.UpdateAssistantToolPolicy(canonical); err != nil {
		return "", setting.AssistantToolPolicy{}, err
	}
	return canonical, policy, nil
}

// RootAuth protects this route; access tokens cannot manage assistant policy.
func AdminGetAssistantToolCatalogue(c *gin.Context) {
	if !requireAssistantBrowserSession(c) {
		return
	}
	common.ApiSuccess(c, gin.H{"groups": setting.AssistantToolCatalogue()})
}

// Check before consuming a preview or starting work. Disabling a tool also
// blocks pending assistant confirmations, without disabling normal console APIs.
func requireAssistantToolEnabled(c *gin.Context, name string) bool {
	_, policy, err := refreshAssistantToolPolicy(c)
	if err != nil {
		writeAssistantError(c, http.StatusServiceUnavailable, "ASSISTANT_TOOL_POLICY_UNAVAILABLE", err)
		c.Abort()
		return false
	}
	if policy.Enabled(name) {
		return true
	}
	writeAssistantError(c, http.StatusForbidden, "ASSISTANT_TOOL_DISABLED", setting.AssistantToolDisabledError(name))
	c.Abort()
	return false
}

func assistantAdminChangeTool(kind string) string {
	switch kind {
	case assistantAdminConfigChangeKind:
		return "prepare_admin_config_change"
	case assistantAdminPricingChangeKind:
		return "prepare_admin_pricing_change"
	case assistantAdminChannelChangeKind:
		return "prepare_admin_channel_change"
	case assistantAdminUserSkillChangeKind:
		return "prepare_admin_user_skill_change"
	case assistantAdminModelSyncChangeKind:
		return "prepare_admin_model_sync"
	default:
		return ""
	}
}

// Generic administrator reads cannot be an alias for a disabled specialised
// tool. Route permissions still run independently on the original HTTP route.
func assistantAdminOperationPolicyTool(handler string) string {
	switch handler {
	case "GetOptions":
		return "get_admin_server_config"
	case "AdminGetAssistantUserProfile", "AdminListMemories":
		return "get_admin_user_skills"
	case "GetAllChannels", "SearchChannels", "GetChannel", "GetChannelOps", "GetSyncableChannels", "ChannelListModels", "EnabledListModels", "GetTagModels":
		return "get_admin_channels"
	case "GetAllModelsMeta", "GetModelMeta", "SearchModelsMeta", "GetMissingModels", "GetGroups":
		return "get_admin_model_inventory"
	default:
		return ""
	}
}

func assistantAdminOperationPolicyEnabled(handler string) bool {
	name := assistantAdminOperationPolicyTool(handler)
	return name == "" || setting.AssistantToolEnabled(name)
}
