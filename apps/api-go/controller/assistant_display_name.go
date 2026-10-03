package controller

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/middleware"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
)

const assistantDisplayNameConfirmationTTL = 10 * time.Minute

type assistantDisplayNameInput struct {
	DisplayName       string `json:"display_name"`
	ConfirmationToken string `json:"confirmation_token"`
	Confirmed         bool   `json:"confirmed"`
}

func executeAssistantPrepareDisplayNameTool(c *gin.Context, actorUserID int, input map[string]any) map[string]any {
	if c == nil || c.GetBool("use_access_token") {
		return map[string]any{"ok": false, "status": "session_required", "error": "a current browser login session is required"}
	}
	identity, ok := middleware.GetSessionAuthIdentity(c)
	if !ok || identity.UserID != actorUserID {
		return map[string]any{"ok": false, "status": "session_required", "error": "a current browser login session is required"}
	}
	if _, _, err := service.ValidateLoginSession(identity); err != nil {
		return map[string]any{"ok": false, "status": "session_required", "error": "a current browser login session is required"}
	}
	// Resolve the current user even for administrators. This tool cannot be
	// used to modify a lower-role account through administrator permissions.
	if id, exists := inputNumber(input, "user_id"); exists && id != float64(actorUserID) {
		return map[string]any{"ok": false, "status": "target_forbidden", "error": "nickname changes through this tool are limited to your own account"}
	}
	user, err := model.GetUserById(actorUserID, false)
	if err != nil || user == nil || user.Status != common.UserStatusEnabled {
		return map[string]any{"ok": false, "status": "context_unavailable", "error": "current account could not be loaded"}
	}
	if identifier := strings.TrimSpace(inputString(input, "identifier")); identifier != "" && !assistantIdentifierMatchesUser(identifier, user) {
		return map[string]any{"ok": false, "status": "target_forbidden", "error": "nickname changes through this tool are limited to your own account"}
	}
	name := user.DisplayName
	if normalized, err := model.NormalizeAssistantDisplayName(name); err == nil {
		name = normalized
	} else {
		// Legacy profile edits allowed an empty or multi-line nickname. Keep
		// the form usable so the owner can replace such a value.
		name = ""
	}
	if value, supplied := input["display_name"]; supplied {
		providedName, valid := value.(string)
		if !valid {
			return map[string]any{"ok": false, "status": "display_name_invalid", "error": model.ErrAssistantDisplayNameInvalid.Error()}
		}
		name, err = model.NormalizeAssistantDisplayName(providedName)
		if err != nil {
			return map[string]any{"ok": false, "status": "display_name_invalid", "error": err.Error()}
		}
	}
	token, _, err := model.CreateAuthFlow(model.AuthFlowCreate{
		Purpose: model.AuthFlowPurposeAssistantDisplayName, UserId: actorUserID,
		SessionId: identity.SessionID, ExpiresAt: time.Now().Add(assistantDisplayNameConfirmationTTL),
	})
	if err != nil {
		return map[string]any{"ok": false, "status": "confirmation_unavailable", "error": "the nickname confirmation form could not be prepared; please try again"}
	}
	c.Set(assistantClientActionKey, map[string]any{
		"type": "user_display_name_change", "requires_confirmation": true,
		"target_user_id": user.Id, "target_username": user.Username,
		"target_display_name": user.DisplayName, "target_role": user.Role,
		"target_group": user.Group, "target_is_self": true,
		"proposed_display_name": name, "confirmation_token": token,
	})
	return map[string]any{"ok": true, "status": "confirmation_required", "action": "change_display_name", "message": "The nickname form is ready. The user can edit the nickname and confirm in the browser. No account change has occurred yet."}
}

func ConfirmAssistantDisplayName(c *gin.Context) {
	if !requireAssistantBrowserSession(c) {
		return
	}
	var input assistantDisplayNameInput
	if err := decodeStrictJSONRequest(c, &input); err != nil {
		writeAssistantError(c, http.StatusBadRequest, "ASSISTANT_INVALID_REQUEST", errors.New("invalid nickname confirmation request"))
		return
	}
	if !requireAssistantConfirmation(c, input.Confirmed) {
		return
	}
	identity, ok := middleware.GetSessionAuthIdentity(c)
	if !ok || identity.UserID != c.GetInt("id") {
		writeAssistantError(c, http.StatusForbidden, "ASSISTANT_SESSION_REQUIRED", model.ErrAssistantProfileSessionInvalid)
		return
	}
	user, err := model.ConfirmAssistantDisplayName(strings.TrimSpace(input.ConfirmationToken), model.AuthFlowMatch{
		Purpose: model.AuthFlowPurposeAssistantDisplayName, UserId: identity.UserID, SessionId: identity.SessionID,
	}, identity.SessionVersion, identity.UserAuthVersion, input.DisplayName)
	if err != nil {
		switch {
		case errors.Is(err, model.ErrAssistantDisplayNameInvalid):
			writeAssistantError(c, http.StatusUnprocessableEntity, "ASSISTANT_DISPLAY_NAME_INVALID", err)
		case errors.Is(err, model.ErrAssistantProfileSessionInvalid):
			writeAssistantError(c, http.StatusForbidden, "ASSISTANT_SESSION_REQUIRED", err)
		case errors.Is(err, model.ErrAuthFlowInvalid), errors.Is(err, model.ErrAuthFlowExpired), errors.Is(err, model.ErrAuthFlowConsumed):
			writeAssistantError(c, http.StatusUnprocessableEntity, "ASSISTANT_PROFILE_CONFIRMATION_INVALID", errors.New("nickname confirmation is invalid, expired, or already used; prepare it again"))
		default:
			writeAssistantError(c, http.StatusInternalServerError, "ASSISTANT_PROFILE_UPDATE_FAILED", errors.New("nickname could not be changed; please try again"))
		}
		return
	}
	model.RecordLog(user.Id, model.LogTypeSystem, "updated own display name via assistant")
	common.ApiSuccess(c, gin.H{"display_name": user.DisplayName})
}
