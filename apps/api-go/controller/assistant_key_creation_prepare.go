package controller

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	"github.com/gin-gonic/gin"
)

func getAssistantKeyGroupOptions(userGroup string) []assistantKeyGroupOption {
	usableGroups := service.GetUserUsableGroups(userGroup)
	groupIDs := make([]string, 0, len(usableGroups))
	for groupID := range usableGroups {
		if _, err := currentRealSelectableGroup(userGroup, groupID); err == nil {
			groupIDs = append(groupIDs, groupID)
		}
	}
	sort.Strings(groupIDs)

	options := make([]assistantKeyGroupOption, 0, len(groupIDs))
	for _, groupID := range groupIDs {
		warning, hasWarning := ratio_setting.GetGroupWarning(groupID)
		var warningPtr *ratio_setting.GroupWarning
		if hasWarning {
			warningCopy := warning
			warningPtr = &warningCopy
		}
		options = append(options, assistantKeyGroupOption{
			ID:          groupID,
			Description: usableGroups[groupID],
			Warning:     warningPtr,
		})
	}
	return options
}

func prepareAssistantKeyDraft(userID int, sessionID, userGroup string, input assistantPrepareKeyInput) (*assistantPreparedKeyAction, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" {
		name = "AI assistant key"
	}
	if utf8.RuneCountInString(name) > 50 {
		return nil, errors.New("API key name must be at most 50 characters")
	}
	group, err := currentRealSelectableGroup(userGroup, input.Group)
	if err != nil {
		return nil, err
	}
	var warningSnapshot *ratio_setting.GroupWarning
	if warning, hasWarning := ratio_setting.GetGroupWarning(string(group)); hasWarning {
		if input.GroupWarningConfirmations != warning.Confirmations {
			return nil, fmt.Errorf("%w: %s", errAssistantKeyWarningChanged, warning.Message)
		}
		warningCopy := warning
		warningSnapshot = &warningCopy
	}

	if strings.TrimSpace(sessionID) == "" {
		return nil, errors.New("browser session is required")
	}
	draft := assistantPreparedKeyDraft{
		Version:        assistantKeyDraftVersion,
		Name:           name,
		Group:          group,
		ConversationID: input.ConversationID,
		Warning:        warningSnapshot,
	}
	payload, err := json.Marshal(draft)
	if err != nil {
		return nil, err
	}
	confirmationToken, _, err := model.CreateAuthFlow(model.AuthFlowCreate{
		Purpose:   model.AuthFlowPurposeAssistantKey,
		UserId:    userID,
		SessionId: sessionID,
		Payload:   string(payload),
		ExpiresAt: time.Now().Add(assistantKeyConfirmationTTL),
	})
	if err != nil {
		return nil, err
	}
	return &assistantPreparedKeyAction{
		Type:                 "create_key",
		ConfirmationToken:    confirmationToken,
		RequiresConfirmation: true,
		ExpiresInSeconds:     int(assistantKeyConfirmationTTL / time.Second),
		Name:                 name,
		Group:                string(group),
		ConversationID:       input.ConversationID,
		UIPath:               "/keys",
	}, nil
}

func executeAssistantCreateKeyRequestTool(c *gin.Context, userID int, input map[string]any) map[string]any {
	if c == nil || c.GetBool("use_access_token") {
		return map[string]any{"ok": false, "error": "a browser login session is required to prepare API key creation"}
	}
	user, granted, err := getAssistantDeveloperAccess(userID)
	if err != nil {
		return map[string]any{"ok": false, "error": "account access could not be loaded"}
	}
	if !granted {
		return map[string]any{"ok": false, "error": "L1 access is required to create an API key"}
	}

	name := strings.TrimSpace(inputString(input, "name"))
	if name == "" {
		name = "AI assistant key"
	}
	if utf8.RuneCountInString(name) > 50 {
		return map[string]any{"ok": false, "status": "name_invalid", "error": "API key name must be at most 50 characters"}
	}
	options := getAssistantKeyGroupOptions(user.Group)
	group := strings.TrimSpace(inputString(input, "group"))
	if assistantUserContextFromGin(c).CreateKeyAction == assistantCreateKeyActionRequest {
		group = ""
	}
	if group == "" {
		return map[string]any{
			"ok": true, "status": "group_required", "action": "create_key",
			"available_groups": options,
			"message":          "Ask the user to choose one exact routing group before requesting confirmation.",
			"requested_name":   name,
		}
	}
	if _, err := currentRealSelectableGroup(user.Group, group); err != nil {
		return map[string]any{
			"ok": false, "status": "invalid_group",
			"error": "the selected group is not available for this account", "available_groups": options,
		}
	}
	warningConfirmations := 0
	if rawConfirmations, ok := inputNumber(input, "group_warning_confirmations"); ok {
		warningConfirmations = int(rawConfirmations)
	}
	if warning, hasWarning := ratio_setting.GetGroupWarning(group); hasWarning && warningConfirmations != warning.Confirmations {
		return map[string]any{
			"ok": true, "status": "group_warning_required", "action": "create_key",
			"requested_name": name, "requested_group": group, "warning": warning,
			"required_confirmations": warning.Confirmations,
			"message":                "Show this group warning and collect the exact required confirmations before preparing key creation.",
		}
	}
	action, err := prepareAssistantKeyDraft(userID, c.GetString("session_id"), user.Group, assistantPrepareKeyInput{
		Name: name, Group: group, ConversationID: assistantHistoryConversationID(c),
		GroupWarningConfirmations: warningConfirmations,
	})
	if err != nil {
		return map[string]any{"ok": false, "error": "API key confirmation could not be prepared"}
	}
	c.Set(assistantClientActionKey, action)
	return map[string]any{
		"ok": true, "status": "confirmation_required", "action": "create_key", "ui_path": "/keys",
		"message":         "Ask the user to explicitly confirm this server-prepared key draft; do not claim that a key exists yet.",
		"requested_name":  action.Name,
		"requested_group": action.Group,
	}
}

// PrepareAssistantDefaultKey binds mutable form fields to an opaque,
// session-scoped draft. The confirmation endpoint never accepts those fields.
func PrepareAssistantDefaultKey(c *gin.Context) {
	if !requireAssistantBrowserSession(c) {
		return
	}
	var input assistantPrepareKeyInput
	if err := decodeStrictAssistantJSON(c, &input); err != nil {
		writeAssistantError(c, http.StatusBadRequest, "ASSISTANT_INVALID_REQUEST", errors.New("invalid key preparation request"))
		return
	}
	userID := c.GetInt("id")
	user, granted, err := getAssistantDeveloperAccess(userID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if !granted {
		writeAssistantError(c, http.StatusForbidden, "ASSISTANT_L1_REQUIRED", errors.New("L1 access is required to create an API key"))
		return
	}
	action, err := prepareAssistantKeyDraft(userID, c.GetString("session_id"), user.Group, input)
	if err != nil {
		switch {
		case errors.Is(err, errAssistantKeyGroupUnavailable):
			writeAssistantError(c, http.StatusUnprocessableEntity, "ASSISTANT_INVALID_GROUP", errors.New("the selected group is not available for this account"))
		case errors.Is(err, errAssistantKeyWarningChanged):
			writeAssistantError(c, http.StatusConflict, "ASSISTANT_GROUP_WARNING_CHANGED", err)
		default:
			writeAssistantError(c, http.StatusUnprocessableEntity, "ASSISTANT_KEY_PREPARE_FAILED", err)
		}
		return
	}
	common.ApiSuccess(c, action)
}
