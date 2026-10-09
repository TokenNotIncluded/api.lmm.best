package controller

import (
	"context"
	"net/http"
	"strconv"
	"strings"
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
	if c == nil || c.Request == nil || assistantActorUserID(c) <= 0 || model.DB == nil {
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
	if policy.Enabled(name) && assistantConfiguredLevelAllowed(c, policy, name) {
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

func assistantAdminOperationPolicyEnabled(handler string, role int) bool {
	name := assistantAdminOperationPolicyTool(handler)
	level := 5
	if role >= common.RoleRootUser {
		level = 6
	}
	return name == "" || setting.AssistantToolAllowedAtLevel(name, level)
}

func assistantContextLevel(state assistantUserContext) int {
	if state.AdministratorMode {
		if state.AccessLevel == "ROOT" || state.AccessLevel == "L6" {
			return 6
		}
		return 5
	}
	if len(state.AccessLevel) == 2 && strings.HasPrefix(state.AccessLevel, "L") {
		if level, err := strconv.Atoi(state.AccessLevel[1:]); err == nil && level >= 0 && level <= 4 {
			return level
		}
	}
	if state.DeveloperAccessGranted {
		return 1
	}
	return 0
}

func assistantConfiguredLevelAllowed(c *gin.Context, policy setting.AssistantToolPolicy, name string) bool {
	// The existing business checks already implement the default floor. Only
	// explicit, additional restrictions need this extra authoritative read.
	if _, configured := policy.Rules[name]; !configured {
		return true
	}
	if c == nil || c.Request == nil || assistantActorUserID(c) <= 0 || model.DB == nil {
		return false
	}
	level, err := model.AssistantToolLevelDB(model.DB.WithContext(c.Request.Context()), assistantActorUserID(c))
	return err == nil && policy.AllowedAtLevel(name, level)
}
