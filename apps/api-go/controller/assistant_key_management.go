package controller

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/middleware"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
)

const assistantListedKeyMetadataContextKey = "assistant_listed_key_metadata"

var assistantKeyManagementActionRule = newAssistantActionRule("删除", "删掉", "移除", "撤销", "停用", "禁用", "delete", "remove", "revoke", "disable")

// This workflow reads metadata for existing credentials, including for an
// account that lost developer access. Creation keeps its separate L1 gate.
func assistantKeyManagementWorkflowRequired(context assistantUserContext) bool {
	if context.KeyManagementRequested {
		return true
	}
	text := strings.ToLower(strings.TrimSpace(context.LatestUserRequest))
	if context.CreateKeyAction != assistantCreateKeyActionNone && !assistantTextContainsAny(text, "删除", "删掉", "撤销", "停用", "禁用", "列表", "列出", "现有", "已有", "delete", "remove", "revoke", "disable", "list", "existing") {
		return false
	}
	return assistantTextContainsAny(text, "密钥", "令牌", "api key", "api_key", "token") &&
		assistantTextContainsAny(text, "删除", "删掉", "撤销", "停用", "禁用", "列表", "列出", "我的", "现有", "已有", "delete", "remove", "revoke", "disable", "list", "my keys", "existing")
}

func assistantKeyManagementWorkflowMinSteps(context assistantUserContext) int {
	if !assistantKeyManagementWorkflowRequired(context) {
		return 0
	}
	steps := 4 // read metadata, look up a missing target, prepare, then answer
	if context.ConversationTitleNeeded {
		steps++
	}
	return steps
}

func assistantPendingKeyManagementRequest(message string, conversations ...[]assistantOpenAIMessage) bool {
	if len(conversations) == 0 || len([]rune(message)) > 128 || assistantActionDeclined(message, assistantKeyManagementActionRule) {
		return false
	}
	messages := conversations[0]
	if len(messages) < 3 || messages[len(messages)-1].Role != "user" || strings.TrimSpace(messages[len(messages)-1].Content) != strings.TrimSpace(message) || messages[len(messages)-2].Role != "assistant" {
		return false
	}
	prompt := strings.ToLower(messages[len(messages)-2].Content)
	if !assistantTextContainsAny(prompt, "密钥", "令牌", "api key", "key id") || !assistantTextContainsAny(prompt, "选择", "编号", "id", "which", "choose", "name") {
		return false
	}
	for index := len(messages) - 3; index >= 0; index-- {
		if messages[index].Role == "user" {
			return assistantKeyManagementWorkflowRequired(assistantUserContext{LatestUserRequest: messages[index].Content, CreateKeyAction: classifyAssistantCreateKeyAction(messages[index].Content)})
		}
	}
	return false
}

func assistantKeyManagementToolDefinitions() []assistantOpenAIToolDefinition {
	return []assistantOpenAIToolDefinition{
		{Type: "function", Function: assistantOpenAIToolFunction{
			Name:        "list_my_api_keys",
			Description: "List only the signed-in user's existing API key metadata: exact ID, name, status, group and timestamps. No credential, masked key, quota, IP rule, or secret is returned. Read this before preparing deletion or disabling; never guess IDs. Paginate when needed. token_id can read one exact ID specified by the user; exact_name is an exact name filter. Duplicate names require the user to choose an exact ID. Revocation remains available after loss of L1 access; it never grants key creation or developer access.",
			Parameters: objectSchema(map[string]any{
				"page":       map[string]any{"type": "integer", "minimum": 1, "maximum": 10000},
				"page_size":  map[string]any{"type": "integer", "minimum": 1, "maximum": 50},
				"exact_name": map[string]any{"type": "string", "maxLength": 50},
				"token_id":   map[string]any{"type": "integer", "minimum": 1},
			}, nil),
		}},
		{Type: "function", Function: assistantOpenAIToolFunction{
			Name:        "prepare_api_key_action",
			Description: "Prepare a browser confirmation card to delete or disable ONE exact API key belonging to the signed-in user. First call list_my_api_keys and use an ID it actually returned. The user must select that exact ID or its complete name. Names with Chinese or other non-ASCII characters must be quoted in full, or supplied as the entire selection reply. Never guess an ID, select an arbitrary key, or silently act on all keys. For duplicate names ask the user to choose the exact ID before preparing a card. This tool does not mutate a key; deletion/disabling requires the user's click and current two-factor verification in the secure form. Never ask for a key or a two-factor code in chat. No enable, creation, cross-user action or quota change is supported.",
			Parameters: objectSchema(map[string]any{
				"action":   map[string]any{"type": "string", "enum": []string{"delete", "disable"}},
				"token_id": map[string]any{"type": "integer", "minimum": 1},
			}, []string{"action", "token_id"}),
		}},
	}
}

func validateAssistantKeyManagementSession(c *gin.Context, userID int) error {
	if c == nil || c.GetBool("use_access_token") || assistantActorUserID(c) != userID {
		return errors.New("a current browser login session is required")
	}
	identity, ok := middleware.GetSessionAuthIdentity(c)
	if !ok || identity.UserID != userID {
		return errors.New("a current browser login session is required")
	}
	user, err := model.GetUserById(userID, false)
	if err != nil || user == nil || user.Status != common.UserStatusEnabled || user.AuthVersion != identity.UserAuthVersion {
		return errors.New("account session is no longer active")
	}
	session, err := model.GetUserSessionBySID(identity.SessionID)
	now := time.Now().Unix()
	if err != nil || session == nil || session.UserID != userID || session.Status != model.UserSessionStatusActive || session.RevokedAt != 0 || session.ExpiresAt <= now || session.Version != identity.SessionVersion || session.UserAuthVersion != identity.UserAuthVersion {
		return errors.New("account session is no longer active")
	}
	if user.GetSetting().IsSessionAutoLogoutEnabled() && session.CreatedAt < now-int64(model.UserSessionAutoLogoutAge/time.Second) {
		return errors.New("account session requires a fresh login")
	}
	return nil
}

func executeAssistantListMyAPIKeysTool(c *gin.Context, userID int, input map[string]any) map[string]any {
	if err := validateAssistantKeyManagementSession(c, userID); err != nil {
		return map[string]any{"ok": false, "status": "browser_session_required", "error": err.Error()}
	}
	// The tool has no actor override, even for administrators.
	for key := range input {
		if key != "page" && key != "page_size" && key != "exact_name" && key != "token_id" {
			return map[string]any{"ok": false, "status": "input_invalid", "error": "only page, page_size, exact_name and token_id are accepted"}
		}
	}
	page, size := 1, 20
	for key, destination := range map[string]*int{"page": &page, "page_size": &size} {
		if raw, exists := input[key]; exists {
			number, valid := raw.(float64)
			max := float64(50)
			if key == "page" {
				max = 10000
			}
			if !valid || math.IsNaN(number) || math.IsInf(number, 0) || math.Trunc(number) != number || number < 1 || number > max {
				return map[string]any{"ok": false, "status": "input_invalid", "error": "invalid pagination"}
			}
			*destination = int(number)
		}
	}
	name, _ := input["exact_name"].(string)
	if _, exists := input["exact_name"]; exists {
		if _, valid := input["exact_name"].(string); !valid || len([]rune(name)) > 50 {
			return map[string]any{"ok": false, "status": "input_invalid", "error": "exact_name must be at most 50 characters"}
		}
	}
	var keys []model.AssistantKeyMetadata
	var total int64
	var err error
	if _, exists := input["token_id"]; exists {
		id, valid := inputNumber(input, "token_id")
		if !valid || math.Trunc(id) != id || id < 1 || id > float64(1<<53-1) || name != "" || page != 1 {
			return map[string]any{"ok": false, "status": "input_invalid", "error": "token_id must be one exact ID without name or page filters"}
		}
		key, keyErr := model.GetAssistantKeyMetadataByID(userID, int(id))
		if keyErr != nil {
			return map[string]any{"ok": false, "status": "target_unavailable", "error": "the requested own API key is unavailable"}
		}
		keys, total, err = []model.AssistantKeyMetadata{*key}, 1, nil
	} else {
		keys, total, err = model.ListAssistantKeyMetadata(userID, (page-1)*size, size, name)
	}
	if err != nil {
		return map[string]any{"ok": false, "status": "keys_unavailable", "error": "API key metadata could not be loaded"}
	}
	listed := map[int]model.AssistantKeyMetadata{}
	if stored, exists := c.Get(assistantListedKeyMetadataContextKey); exists {
		listed, _ = stored.(map[int]model.AssistantKeyMetadata)
		if listed == nil {
			listed = map[int]model.AssistantKeyMetadata{}
		}
	}
	for _, key := range keys {
		listed[key.ID] = key
	}
	c.Set(assistantListedKeyMetadataContextKey, listed)
	return map[string]any{"ok": true, "scope": "self", "keys": keys, "total": total, "page": page, "page_size": size, "has_more": int64(page*size) < total, "credentials_returned": false}
}

func assistantKeyIDSelectedByUser(c *gin.Context, id int) bool {
	text := assistantKeySelectionMessage(c)
	if text == strconv.Itoa(id) {
		return true
	}
	pattern := `(?:\bid\s*[:：#]?\s*|#\s*|编号\s*[:：]?\s*|(?:删除|删掉|停用|禁用)\s*)` + strconv.Itoa(id) + `(?:[^0-9]|$)`
	return regexp.MustCompile(pattern).MatchString(text)
}

func assistantKeySelectionMessage(c *gin.Context) string {
	return strings.ToLower(assistantKeySelectionRawMessage(c))
}

func assistantKeySelectionRawMessage(c *gin.Context) string {
	text := strings.TrimSpace(c.GetString("assistant_history_latest_message"))
	if text == "" {
		text = strings.TrimSpace(assistantUserContextFromGin(c).LatestUserRequest)
	}
	return text
}

func assistantKeyNameSelectedByUser(c *gin.Context, name string) bool {
	if strings.TrimSpace(name) == "" {
		return false
	}
	text := assistantKeySelectionRawMessage(c)
	if text == name {
		return true
	}
	// Quoting selects the complete, case-sensitive metadata name. Neither a
	// filtered list nor pagination can turn a longer name into this target.
	for _, quotes := range [][2]string{{`"`, `"`}, {"'", "'"}, {"`", "`"}, {"“", "”"}, {"‘", "’"}, {"「", "」"}, {"『", "』"}, {"《", "》"}} {
		if strings.Contains(text, quotes[0]+name+quotes[1]) {
			return true
		}
	}
	// Unquoted non-ASCII names cannot be split from surrounding prose reliably.
	// Require a full quoted name or ID instead of treating a CJK prefix as a
	// selected key. ASCII names must occupy a whole whitespace-delimited token,
	// so "abc" cannot match "abc甲", "abc-long", or "abc.long" either.
	if regexp.MustCompile(`[^\x00-\x7f]`).MatchString(name) {
		return false
	}
	return regexp.MustCompile(`(?:^|\s)` + regexp.QuoteMeta(name) + `(?:\s|$)`).MatchString(text)
}

func executeAssistantPrepareAPIKeyActionTool(c *gin.Context, userID int, input map[string]any) map[string]any {
	if err := validateAssistantKeyManagementSession(c, userID); err != nil {
		return map[string]any{"ok": false, "status": "browser_session_required", "error": err.Error()}
	}
	for key := range input {
		if key != "action" && key != "token_id" {
			return map[string]any{"ok": false, "status": "input_invalid", "error": "only action and token_id are accepted"}
		}
	}
	operation := inputString(input, "action")
	if operation != "delete" && operation != "disable" {
		return map[string]any{"ok": false, "status": "action_invalid", "error": "choose delete or disable"}
	}
	if assistantActionDeclined(assistantUserContextFromGin(c).LatestUserRequest, assistantKeyManagementActionRule) {
		return map[string]any{"ok": false, "status": "action_declined", "error": "the user declined key mutation"}
	}
	id, valid := inputNumber(input, "token_id")
	if !valid || math.IsNaN(id) || math.IsInf(id, 0) || math.Trunc(id) != id || id < 1 || id > float64(1<<53-1) {
		return map[string]any{"ok": false, "status": "target_invalid", "error": "one exact API key ID is required"}
	}
	stored, _ := c.Get(assistantListedKeyMetadataContextKey)
	listed, _ := stored.(map[int]model.AssistantKeyMetadata)
	previous, exists := listed[int(id)]
	if !exists {
		return map[string]any{"ok": false, "status": "list_required", "error": "first list the user's API keys and choose an ID actually returned by that tool"}
	}
	key, err := model.GetAssistantKeyMetadataByID(userID, int(id))
	if err != nil || key.Name != previous.Name || key.Group != previous.Group || key.Status != previous.Status {
		return map[string]any{"ok": false, "status": "target_changed", "error": "the selected key changed or is unavailable; list keys again"}
	}
	_, nameMatches, err := model.ListAssistantKeyMetadata(userID, 0, 1, key.Name)
	if err != nil {
		return map[string]any{"ok": false, "status": "keys_unavailable", "error": "API key metadata could not be loaded"}
	}
	if nameMatches > 1 && !assistantKeyIDSelectedByUser(c, key.ID) {
		return map[string]any{"ok": false, "status": "target_choice_required", "error": "multiple keys share this name; ask the user to choose an exact key ID before preparing confirmation"}
	}
	if !assistantKeyIDSelectedByUser(c, key.ID) && !assistantKeyNameSelectedByUser(c, key.Name) {
		return map[string]any{"ok": false, "status": "target_choice_required", "error": "ask the user to select the exact ID, quote the complete key name, or send the complete name as their selection reply"}
	}
	twoFactorRequired, err := model.IsTwoFAEnabled(userID)
	if err != nil {
		return map[string]any{"ok": false, "status": "security_unavailable", "error": "account security could not be loaded"}
	}
	draft := model.AssistantKeyManagementDraft{Version: model.AssistantKeyManagementDraftVersion, Operation: operation, Key: *key}
	payload, err := json.Marshal(draft)
	if err != nil {
		return map[string]any{"ok": false, "error": "key action could not be prepared"}
	}
	token, _, err := model.CreateAuthFlow(model.AuthFlowCreate{
		Purpose: model.AuthFlowPurposeAssistantKeyManagement, UserId: userID, SessionId: c.GetString("session_id"),
		Payload: string(payload), ExpiresAt: time.Now().Add(assistantKeyConfirmationTTL),
	})
	if err != nil {
		return map[string]any{"ok": false, "error": "key confirmation could not be prepared"}
	}
	c.Set(assistantClientActionKey, map[string]any{
		"type": "api_key_action", "action": operation, "token": key, "confirmation_token": token,
		"requires_confirmation": true, "expires_in_seconds": int(assistantKeyConfirmationTTL / time.Second),
		"two_factor_required": twoFactorRequired, "ui_path": "/keys",
	})
	// The provider receives the preview only. The opaque confirmation token is
	// transported privately to the browser and never enters model context.
	return map[string]any{"ok": true, "status": "confirmation_required", "action": operation, "token": key, "message": "Review the exact key in the browser card and explicitly confirm. The key has not been changed yet; any two-factor code belongs only in that secure form."}
}

func ConfirmAssistantAPIKeyAction(c *gin.Context) {
	if !requireAssistantBrowserSession(c) {
		return
	}
	var input assistantConfirmKeyInput
	if err := decodeStrictJSONRequest(c, &input); err != nil {
		writeAssistantError(c, http.StatusBadRequest, "ASSISTANT_INVALID_REQUEST", errors.New("invalid key confirmation request"))
		return
	}
	if strings.TrimSpace(input.ConfirmationToken) == "" {
		writeAssistantError(c, http.StatusUnprocessableEntity, "ASSISTANT_CONFIRMATION_REQUIRED", errors.New("an opaque confirmation token is required"))
		return
	}
	identity, ok := middleware.GetSessionAuthIdentity(c)
	if !ok || validateAssistantKeyManagementSession(c, identity.UserID) != nil {
		writeAssistantError(c, http.StatusForbidden, "ASSISTANT_SESSION_REQUIRED", errors.New("a current browser login session is required"))
		return
	}
	fence, err := model.NewAssistantKeyAuthorizationFence(identity.UserID, identity.SessionID, identity.SessionVersion, identity.UserAuthVersion, model.CurrentDeveloperAccessPolicy())
	if err != nil {
		writeAssistantError(c, http.StatusForbidden, "ASSISTANT_SESSION_REQUIRED", errors.New("a current browser login session is required"))
		return
	}
	key, operation, err := model.ConsumeAssistantKeyManagementFlow(strings.TrimSpace(input.ConfirmationToken), fence, input.TwoFactorCode)
	if err != nil {
		switch {
		case errors.Is(err, model.ErrAssistantKeyTwoFactorInvalid):
			writeAssistantError(c, http.StatusUnprocessableEntity, "ASSISTANT_TWO_FACTOR_INVALID", errors.New("a valid current two-factor code is required"))
		case errors.Is(err, model.ErrAuthFlowInvalid), errors.Is(err, model.ErrAuthFlowExpired), errors.Is(err, model.ErrAuthFlowConsumed), errors.Is(err, model.ErrAssistantKeyAuthorizationChanged), errors.Is(err, model.ErrAssistantKeyTargetChanged):
			writeAssistantError(c, http.StatusUnprocessableEntity, "ASSISTANT_KEY_CONFIRMATION_INVALID", errors.New("key confirmation is invalid, expired, already used, or its target changed; list keys and prepare it again"))
		default:
			writeAssistantError(c, http.StatusInternalServerError, "ASSISTANT_KEY_ACTION_FAILED", errors.New("key action could not be completed; please try again"))
		}
		return
	}
	model.RecordLog(identity.UserID, model.LogTypeSystem, fmt.Sprintf("%s API key %d via assistant", operation, key.ID))
	common.ApiSuccess(c, gin.H{"id": key.ID, "name": key.Name, "group": key.Group, "action": operation, "status": key.Status})
}
