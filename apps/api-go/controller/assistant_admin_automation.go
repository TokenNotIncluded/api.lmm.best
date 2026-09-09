package controller

import (
	"errors"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/middleware"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service/authz"
	"github.com/gin-gonic/gin"
)

// Only the authenticated chat controller may set this request-local capability.
// Tool arguments, conversation messages and summaries cannot enable it.
const assistantAdminAutomationContextKey = "assistant_admin_automation_authorized"

// Fresh database reads prevent an in-flight agent from retaining authority
// after logout, account suspension, a password/security reset, or demotion.
func validateAssistantAdminAutomationSession(c *gin.Context, userID int) (*model.UserBase, error) {
	if c == nil || c.GetBool("use_access_token") || assistantActorUserID(c) != userID {
		return nil, errors.New("an authenticated administrator browser session is required")
	}
	identity, ok := middleware.GetSessionAuthIdentity(c)
	if !ok || identity.UserID != userID {
		return nil, errors.New("an authenticated administrator browser session is required")
	}
	user, err := assistantAdminUser(userID)
	if err != nil {
		return nil, err
	}
	session, err := model.GetUserSessionBySID(identity.SessionID)
	now := time.Now().Unix()
	if err != nil || session == nil || session.UserID != userID || session.Status != model.UserSessionStatusActive || session.RevokedAt != 0 || session.ExpiresAt <= now || session.Version != identity.SessionVersion || session.UserAuthVersion != identity.UserAuthVersion || user.AuthVersion != identity.UserAuthVersion {
		return nil, errors.New("administrator session is no longer active")
	}
	if user.GetSetting().IsSessionAutoLogoutEnabled() && session.CreatedAt < now-int64(model.UserSessionAutoLogoutAge/time.Second) {
		return nil, errors.New("administrator session requires a fresh login")
	}
	return user, nil
}

// Administrator mutations must pass through the signed, single-use confirmation
// flow. Model tool calls are not proof of administrator intent because their
// context can contain tenant-controlled data.
func maybeApplyAssistantAdminAutomatically(c *gin.Context, userID int, payload assistantAdminChangePayload) (map[string]any, bool) {
	return nil, false
}

func auditAssistantAdminChange(c *gin.Context, payload assistantAdminChangePayload, suffix string, result map[string]any) {
	detail := map[string]interface{}{"kind": payload.Kind, "status": result["status"], "applied": result["applied"], "warnings": result["warnings"], "locked_models": result["locked_models"]}
	switch payload.Kind {
	case assistantAdminPricingChangeKind:
		if payload.Pricing != nil {
			detail["model_id"] = payload.Pricing.ModelID
			detail["pricing"] = result["pricing"]
		}
	case assistantAdminChannelChangeKind:
		if payload.Channel != nil {
			detail["channel_id"], detail["fields"] = payload.Channel.ChannelID, sortedAssistantAdminChangeKeys(payload.Channel.Changes)
		}
	case assistantAdminUserSkillChangeKind:
		if payload.UserSkill != nil {
			detail["target_user_id"], detail["operation"] = payload.UserSkill.TargetUserID, payload.UserSkill.Operation
		}
	case assistantAdminModelSyncChangeKind:
		if payload.ModelSync != nil {
			detail["locale"], detail["model_count"], detail["source_digest"] = payload.ModelSync.Locale, len(payload.ModelSync.Models), payload.ModelSync.SourceDigest
		}
	default:
		detail["keys"] = result["updated_keys"]
	}
	// The relay bills a root account in c.id. Attribute mutations to the signed
	// in actor without changing the relay's billing context.
	actorID := assistantActorUserID(c)
	auditContext := c.Copy()
	auditContext.Set("id", actorID)
	auditContext.Set("role", 0)
	auditContext.Set("username", "")
	if actor, err := model.GetUserById(actorID, false); err == nil {
		auditContext.Set("role", actor.Role)
		auditContext.Set("username", actor.Username)
	}
	recordManageAudit(auditContext, "assistant.admin_"+payload.Kind+"_apply"+suffix, detail)
}

func assistantAdminChannelPermission(userID int, changes map[string]string) error {
	user, err := assistantAdminUser(userID)
	if err != nil {
		return err
	}
	for field := range changes {
		permission := authz.ChannelWrite
		if field == "status" {
			permission = authz.ChannelOperate
		}
		if !authz.Can(userID, user.Role, permission) {
			return errors.New("channel operation is outside the administrator's assigned permissions")
		}
	}
	return nil
}
