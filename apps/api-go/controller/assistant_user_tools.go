package controller

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
)

// assistantToolTrace is intentionally smaller than the provider tool result.
// The browser needs to show what happened, but the conversation must never
// receive raw account data, passwords, OAuth subject IDs, or request content.
type assistantToolTrace struct {
	Name      string         `json:"name"`
	Status    string         `json:"status"`
	Input     map[string]any `json:"input,omitempty"`
	Result    *float64       `json:"result,omitempty"`
	ErrorCode string         `json:"error_code,omitempty"`
}

func buildAssistantToolTrace(call assistantOpenAIToolCall, result map[string]any) assistantToolTrace {
	trace := assistantToolTrace{
		Name:   strings.TrimSpace(call.Function.Name),
		Status: "output-available",
		Input:  assistantSafeToolInput(call.Function.Arguments),
	}
	if ok, exists := result["ok"].(bool); exists && !ok {
		trace.Status = "output-error"
	}
	if status, _ := result["status"].(string); status == "confirmation_required" || status == "navigation_ready" {
		trace.Status = "approval-requested"
	}
	if trace.Name == "calculate_math" {
		if trace.Status == "output-error" {
			trace.ErrorCode = "invalid_math_expression"
			if message, _ := result["error"].(string); message == "a math expression is required" {
				trace.ErrorCode = "missing_math_expression"
			}
		} else if value, ok := result["result"].(float64); ok && !math.IsNaN(value) && !math.IsInf(value, 0) {
			trace.Result = &value
		}
	}
	return trace
}

func assistantSafeToolInput(arguments string) map[string]any {
	input := make(map[string]any)
	if strings.TrimSpace(arguments) == "" || json.Unmarshal([]byte(arguments), &input) != nil {
		return nil
	}
	allowed := map[string]struct{}{
		"action": {}, "days": {}, "group": {}, "identifier": {}, "model_id": {},
		"expression": {}, "page": {}, "platform": {}, "provider": {}, "query": {}, "section": {},
		"target_user_id": {}, "title": {}, "topic": {},
	}
	result := make(map[string]any)
	for key, value := range input {
		if _, ok := allowed[key]; !ok {
			continue
		}
		switch typed := value.(type) {
		case string:
			trimmed := strings.TrimSpace(typed)
			if len([]rune(trimmed)) > 200 {
				trimmed = string([]rune(trimmed)[:200])
			}
			value = trimmed
		case float64:
			if math.IsNaN(typed) || math.IsInf(typed, 0) {
				continue
			}
		default:
			continue
		}
		result[key] = value
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

type assistantUserTarget struct {
	Actor *model.User
	User  *model.User
	Self  bool
	Admin bool
}

func resolveAssistantUserTarget(c *gin.Context, actorUserID int, input map[string]any, requireManage bool) (*assistantUserTarget, map[string]any) {
	if actorUserID <= 0 {
		return nil, map[string]any{"ok": false, "status": "context_unavailable", "error": "signed-in account is unavailable"}
	}
	actor, err := model.GetUserById(actorUserID, false)
	if err != nil {
		return nil, map[string]any{"ok": false, "status": "context_unavailable", "error": "current account could not be loaded"}
	}
	isAdmin := actor.Role >= common.RoleAdminUser
	targetID := 0
	if number, exists := inputNumber(input, "user_id"); exists {
		if number < 1 || math.Trunc(number) != number {
			return nil, map[string]any{"ok": false, "status": "target_invalid", "error": "target user ID is invalid"}
		}
		targetID = int(number)
	}
	identifier := strings.TrimSpace(inputString(input, "identifier"))
	if len([]rune(identifier)) > 200 {
		return nil, map[string]any{"ok": false, "status": "target_invalid", "error": "target identifier is too long"}
	}
	if targetID > 0 && identifier != "" {
		return nil, map[string]any{"ok": false, "status": "target_invalid", "error": "provide either user_id or identifier, not both"}
	}

	target := actor
	if targetID > 0 {
		if !isAdmin && targetID != actor.Id {
			return nil, map[string]any{"ok": false, "status": "target_forbidden", "error": "regular users may inspect or modify only their own account"}
		}
		target, err = model.GetUserById(targetID, false)
		if err != nil {
			return nil, map[string]any{"ok": false, "status": "target_not_found", "error": "target user was not found"}
		}
	} else if identifier != "" {
		if !isAdmin {
			if !assistantIdentifierMatchesUser(identifier, actor) {
				return nil, map[string]any{"ok": false, "status": "target_forbidden", "error": "regular users may inspect or modify only their own account"}
			}
		} else {
			users, total, searchErr := model.SearchUsers(identifier, "", nil, nil, false, 0, 10, model.NewUserSortOptions("id", "asc"))
			if searchErr != nil {
				return nil, map[string]any{"ok": false, "status": "target_lookup_failed", "error": "target user could not be located"}
			}
			if total == 0 || len(users) == 0 {
				return nil, map[string]any{"ok": false, "status": "target_not_found", "error": "target user was not found"}
			}
			// SearchUsers intentionally has no caller-role filter because the
			// normal administrator list is allowed to return a broad dataset.
			// Assistant target lookup must apply the role lattice before any
			// candidate identity reaches the model or browser.  In particular, a
			// lower-level administrator must not learn usernames or roles of peer
			// or higher-level administrators from an ambiguous substring search.
			manageableUsers := make([]*model.User, 0, len(users))
			for _, candidate := range users {
				if candidate != nil && (candidate.Id == actor.Id || canManageTargetRole(actor.Role, candidate.Role)) {
					manageableUsers = append(manageableUsers, candidate)
				}
			}
			if len(manageableUsers) == 0 {
				return nil, map[string]any{"ok": false, "status": "target_forbidden", "error": "the target is outside the administrator's permitted role scope"}
			}
			if len(manageableUsers) > 1 {
				candidates := make([]map[string]any, 0, len(manageableUsers))
				for _, candidate := range manageableUsers {
					candidates = append(candidates, assistantSafeUserIdentity(candidate))
				}
				return nil, map[string]any{"ok": false, "status": "target_ambiguous", "error": "more than one user matches; ask for a username or numeric ID", "candidates": candidates}
			}
			target = manageableUsers[0]
		}
	}

	if target.Id != actor.Id && !canManageTargetRole(actor.Role, target.Role) {
		return nil, map[string]any{"ok": false, "status": "target_forbidden", "error": "the target is outside the administrator's permitted role scope"}
	}
	if requireManage && target.Id != actor.Id && !canManageTargetRole(actor.Role, target.Role) {
		return nil, map[string]any{"ok": false, "status": "target_forbidden", "error": "the target is outside the administrator's permitted role scope"}
	}
	return &assistantUserTarget{Actor: actor, User: target, Self: target.Id == actor.Id, Admin: isAdmin}, nil
}

func assistantIdentifierMatchesUser(identifier string, user *model.User) bool {
	if user == nil {
		return false
	}
	if strconv.Itoa(user.Id) == identifier {
		return true
	}
	return strings.EqualFold(identifier, strings.TrimSpace(user.Username)) || strings.EqualFold(identifier, strings.TrimSpace(user.Email))
}

func assistantSafeUserIdentity(user *model.User) map[string]any {
	if user == nil {
		return nil
	}
	return map[string]any{
		"id":           user.Id,
		"username":     user.Username,
		"display_name": user.DisplayName,
		"role":         user.Role,
	}
}

func assistantSafeUserOverview(user *model.User) map[string]any {
	if user == nil {
		return nil
	}
	bindings := []map[string]any{}
	for _, provider := range []struct {
		name  string
		label string
		value string
	}{
		{"github", "GitHub", user.GitHubId},
		{"discord", "Discord", user.DiscordId},
		{"oidc", "OIDC", user.OidcId},
		{"wechat", "WeChat", user.WeChatId},
		{"telegram", "Telegram", user.TelegramId},
		{"linuxdo", "LinuxDO", user.LinuxDOId},
	} {
		if strings.TrimSpace(provider.value) != "" {
			bindings = append(bindings, map[string]any{"provider": provider.name, "label": provider.label, "kind": "built_in"})
		}
	}
	customCount := 0
	if customBindings, err := model.GetUserOAuthBindingsByUserId(user.Id); err == nil {
		customCount = len(customBindings)
	}
	return map[string]any{
		"id":                      user.Id,
		"username":                user.Username,
		"display_name":            user.DisplayName,
		"email":                   user.Email,
		"role":                    user.Role,
		"status":                  user.Status,
		"group":                   user.Group,
		"quota":                   user.Quota,
		"used_quota":              user.UsedQuota,
		"request_count":           user.RequestCount,
		"created_at":              user.CreatedAt,
		"last_login_at":           user.LastLoginAt,
		"last_api_activity_at":    user.LastAPIActivityAt,
		"oauth_bindings":          bindings,
		"custom_oauth_count":      customCount,
		"secrets_and_subject_ids": "omitted",
	}
}

func executeAssistantNavigateTool(c *gin.Context, actorUserID int, input map[string]any) map[string]any {
	page := strings.TrimSpace(inputString(input, "page"))
	paths := map[string]string{
		"home":                 "/",
		"getting-started":      "/getting-started",
		"pricing":              "/pricing",
		"wallet":               "/wallet",
		"keys":                 "/keys",
		"drawing":              "/drawing",
		"models":               "/models",
		"profile":              "/profile",
		"support":              "/support",
		"open-source-bounties": "/open-source-bounties",
	}
	path, ok := paths[page]
	if page == "users" {
		actor, err := model.GetUserById(actorUserID, false)
		if err != nil || actor.Role < common.RoleAdminUser {
			return map[string]any{"ok": false, "status": "target_forbidden", "error": "the users page is available only to administrators"}
		}
		path = "/users"
		ok = true
	}
	if page == "models" {
		actor, err := model.GetUserById(actorUserID, false)
		if err != nil || actor.Role < common.RoleAdminUser {
			return map[string]any{"ok": false, "status": "target_forbidden", "error": "the models page is available only to administrators"}
		}
		ok = true
	}
	if page == "usage-logs" {
		path = "/usage-logs/common"
		section := strings.TrimSpace(inputString(input, "section"))
		if section == "drawing" || section == "task" || section == "common" {
			path = "/usage-logs/" + section
		}
		ok = true
	}
	if page == "drawing" {
		user, err := model.GetUserById(actorUserID, false)
		if err != nil {
			return map[string]any{"ok": false, "status": "context_unavailable", "error": "account access could not be loaded"}
		}
		access, err := model.GetDeveloperAccessStateForUser(user)
		if err != nil || !access.Granted {
			return map[string]any{"ok": false, "status": "target_forbidden", "error": "L1 access is required for the drawing workbench"}
		}
		ok = true
	}
	if !ok {
		return map[string]any{"ok": false, "status": "page_invalid", "error": "this page is not available through assistant navigation"}
	}

	query := map[string]any{}
	identifier := strings.TrimSpace(inputString(input, "identifier"))
	if identifier == "" {
		identifier = strings.TrimSpace(inputString(input, "query"))
	}
	if identifier != "" && (page == "users" || page == "usage-logs") {
		targetInput := map[string]any{"identifier": identifier}
		target, targetError := resolveAssistantUserTarget(c, actorUserID, targetInput, false)
		if targetError != nil {
			return targetError
		}
		if page == "users" {
			query["filter"] = target.User.Username
			query["l0Only"] = false
		} else {
			query["username"] = target.User.Username
		}
	}
	if page == "users" && len(query) == 0 {
		query["l0Only"] = false
	}
	action := map[string]any{"type": "navigate", "path": path, "query": query}
	if c != nil {
		c.Set(assistantClientActionKey, action)
	}
	return map[string]any{"ok": true, "status": "navigation_ready", "path": path, "query": query}
}

func executeAssistantUserOverviewTool(c *gin.Context, actorUserID int, input map[string]any) map[string]any {
	target, targetError := resolveAssistantUserTarget(c, actorUserID, input, false)
	if targetError != nil {
		return targetError
	}
	return map[string]any{
		"ok":     true,
		"scope":  map[bool]string{true: "self", false: "administrator_target"}[target.Self],
		"user":   assistantSafeUserOverview(target.User),
		"notice": "Passwords, access tokens, OAuth subject IDs, session data, and raw request content are omitted.",
	}
}

func executeAssistantUserUsageTool(c *gin.Context, actorUserID int, input map[string]any) map[string]any {
	target, targetError := resolveAssistantUserTarget(c, actorUserID, input, false)
	if targetError != nil {
		return targetError
	}
	days := 30
	if value, exists := inputNumber(input, "days"); exists {
		if value < 1 || value > 90 || math.Trunc(value) != value {
			return map[string]any{"ok": false, "status": "range_invalid", "error": "days must be an integer between 1 and 90"}
		}
		days = int(value)
	}
	end := time.Now().Unix()
	start := time.Now().Add(-time.Duration(days) * 24 * time.Hour).Unix()
	summary, err := model.GetAssistantUsageSummary(target.User.Id, start, end, 20)
	if err != nil {
		return map[string]any{"ok": false, "status": "usage_unavailable", "error": "historical usage could not be loaded"}
	}
	return map[string]any{
		"ok":       true,
		"scope":    map[bool]string{true: "self", false: "administrator_target"}[target.Self],
		"target":   assistantSafeUserIdentity(target.User),
		"days":     days,
		"source":   "consume logs",
		"summary":  summary,
		"raw_logs": false,
	}
}

func executeAssistantPrepareUserActionTool(c *gin.Context, actorUserID int, input map[string]any) map[string]any {
	actionName := strings.TrimSpace(inputString(input, "action"))
	target, targetError := resolveAssistantUserTarget(c, actorUserID, input, true)
	if targetError != nil {
		return targetError
	}
	if actionName == "bind_oauth" {
		if !target.Self {
			return map[string]any{"ok": false, "status": "target_session_required", "error": "the target user must complete OAuth binding in their own signed-in session"}
		}
		action := map[string]any{"type": "navigate", "path": "/profile", "query": map[string]any{}}
		if c != nil {
			c.Set(assistantClientActionKey, action)
		}
		return map[string]any{"ok": true, "status": "navigation_ready", "action": "bind_oauth", "path": "/profile", "message": "Open the account bindings page; the user must choose and complete the OAuth provider flow interactively."}
	}

	if actionName != "change_password" && actionName != "unbind_oauth" && actionName != "disable" && actionName != "delete" {
		return map[string]any{"ok": false, "status": "action_invalid", "error": "unsupported user action"}
	}
	if actionName == "disable" && target.Self {
		return map[string]any{"ok": false, "status": "action_forbidden", "error": "a regular user cannot disable their own account through the assistant"}
	}
	if actionName == "disable" && !target.Admin {
		return map[string]any{"ok": false, "status": "action_forbidden", "error": "only an administrator can disable another account"}
	}
	if actionName == "delete" && !target.Self && !target.Admin {
		return map[string]any{"ok": false, "status": "action_forbidden", "error": "only an administrator can delete another account"}
	}

	action := map[string]any{
		"requires_confirmation": true,
		"target_user_id":        target.User.Id,
		"target_username":       target.User.Username,
		"target_display_name":   target.User.DisplayName,
		"target_role":           target.User.Role,
		"target_group":          target.User.Group,
		"target_is_self":        target.Self,
	}
	result := map[string]any{
		"ok":      true,
		"status":  "confirmation_required",
		"target":  assistantSafeUserIdentity(target.User),
		"message": "The browser must show a clear confirmation card. Secrets are entered only in the secure form and are never sent to the assistant conversation.",
	}
	switch actionName {
	case "change_password":
		action["type"] = "user_password_change"
		result["action"] = "change_password"
	case "unbind_oauth":
		provider, providerKind, providerLabel, providerOK := assistantOAuthProvider(inputString(input, "provider"))
		if !providerOK {
			return map[string]any{"ok": false, "status": "provider_invalid", "error": "provide a supported built-in provider or custom:<provider_id>"}
		}
		action["type"] = "user_oauth_unbind"
		action["provider"] = provider
		action["provider_kind"] = providerKind
		action["provider_label"] = providerLabel
		result["action"] = "unbind_oauth"
		result["provider"] = providerLabel
	case "disable", "delete":
		action["type"] = "user_account_action"
		action["action"] = actionName
		result["action"] = actionName
	}
	if c != nil {
		c.Set(assistantClientActionKey, action)
	}
	return result
}

func assistantOAuthProvider(value string) (provider string, kind string, label string, ok bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	builtIn := map[string]string{
		"github": "GitHub", "discord": "Discord", "oidc": "OIDC", "wechat": "WeChat", "telegram": "Telegram", "linuxdo": "LinuxDO",
	}
	if label, exists := builtIn[value]; exists {
		return value, "built_in", label, true
	}
	if strings.HasPrefix(value, "custom:") {
		id := strings.TrimPrefix(value, "custom:")
		parsed, err := strconv.Atoi(id)
		if err == nil && parsed > 0 {
			return strconv.Itoa(parsed), "custom", "Custom OAuth #" + strconv.Itoa(parsed), true
		}
	}
	return "", "", "", false
}
