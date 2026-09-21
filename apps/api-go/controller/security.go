package controller

import (
	"bytes"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/dto"
	"github.com/LIghtJUNction/api.lmm.best/middleware"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"

	"github.com/gin-gonic/gin"
)

const publicSecurityStatsWindow = 30 * 24 * time.Hour

func GetPublicSecurityPolicy(c *gin.Context) {
	common.ApiSuccess(c, buildPublicSecurityPolicy())
}

func GetAdminSecurityPolicy(c *gin.Context) {
	settings := setting.GetAdvancedSecuritySettings()
	adminRules := make([]dto.SecurityAdminRule, 0, len(settings.RuleSet.Rules))
	for _, rule := range settings.RuleSet.Rules {
		adminRules = append(adminRules, dto.SecurityAdminRule{
			SecurityRuleSummary: dto.SecurityRuleSummary{
				ID:          rule.ID,
				Name:        rule.Name,
				Category:    rule.Category,
				Layer:       rule.Layer,
				Severity:    rule.Severity,
				Source:      rule.Source,
				Version:     rule.Version,
				Description: rule.Description,
			},
			Enabled:  rule.Enabled,
			Groups:   append([]string(nil), rule.Groups...),
			Patterns: append([]string(nil), rule.Patterns...),
		})
	}
	common.ApiSuccess(c, dto.AdminSecurityPolicy{
		Public: buildPublicSecurityPolicy(),
		Settings: dto.SecuritySettings{
			Enabled:  settings.Enabled,
			OnPrompt: settings.OnPrompt,
			Action:   settings.Action,
		},
		Rules:        adminRules,
		ViolationFee: violationFeeSettingsDTO(),
	})
}

type advancedSecuritySettingsUpdateRequest struct {
	Enabled  *bool             `json:"enabled"`
	OnPrompt *bool             `json:"on_prompt"`
	Action   string            `json:"action"`
	Rules    common.RawMessage `json:"rules"`
}

// UpdateAdvancedSecuritySettings commits the complete guardrail policy in one
// operation so a failed field cannot leave the other fields partially saved.
func UpdateAdvancedSecuritySettings(c *gin.Context) {
	var request advancedSecuritySettingsUpdateRequest
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		common.ApiErrorMsg(c, "invalid advanced security settings payload")
		return
	}
	rules := bytes.TrimSpace(request.Rules)
	if request.Enabled == nil || request.OnPrompt == nil || strings.TrimSpace(request.Action) == "" || len(rules) == 0 || (rules[0] != '{' && rules[0] != '[') {
		common.ApiErrorMsg(c, "enabled, on_prompt, action, and rules are required")
		return
	}
	action := strings.ToLower(strings.TrimSpace(request.Action))
	if err := model.UpdateAdvancedSecurityOptions(*request.Enabled, *request.OnPrompt, action, string(rules)); err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "advanced_security.settings_update", map[string]interface{}{
		"keys": []string{
			setting.AdvancedSecurityEnabledOptionKey,
			setting.AdvancedSecurityOnPromptOptionKey,
			setting.AdvancedSecurityActionOptionKey,
			setting.AdvancedSecurityRulesOptionKey,
		},
	})
	common.ApiSuccess(c, nil)
}

func GetPublicSecurityStats(c *gin.Context) {
	start, end := publicSecurityStatsWindowBounds(c)
	stats, err := model.GetAdvancedSecurityStats(model.AdvancedSecurityEventFilter{
		StartTimestamp: start,
		EndTimestamp:   end,
	})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, buildSecurityStatsDTO(stats, start, end, false))
}

func GetAdminSecurityStats(c *gin.Context) {
	filter, err := parseAdvancedSecurityEventFilter(c, false)
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	stats, err := model.GetAdvancedSecurityStats(filter)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, buildSecurityStatsDTO(stats, filter.StartTimestamp, filter.EndTimestamp, true))
}

func ListAdminSecurityEvents(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	filter, err := parseAdvancedSecurityEventFilter(c, true)
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	filter.Limit = pageInfo.GetPageSize()
	filter.Offset = pageInfo.GetStartIdx()
	events, total, err := model.ListAdvancedSecurityEvents(filter)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	actorRole := c.GetInt("role")
	actorID := c.GetInt("id")
	roles := securityEventTargetRoles(events, actorRole, actorID)
	items := make([]dto.AdvancedSecurityEvent, 0, len(events))
	for _, event := range events {
		item := dto.AdvancedSecurityEvent{
			ID:            event.ID,
			CreatedAt:     event.CreatedAt,
			RequestID:     event.RequestID,
			UserID:        event.UserID,
			Username:      event.Username,
			TokenID:       event.TokenID,
			ChannelID:     event.ChannelID,
			ModelName:     event.ModelName,
			Group:         event.Group,
			Endpoint:      event.Endpoint,
			Decision:      event.Decision,
			RuleID:        event.RuleID,
			RuleName:      event.RuleName,
			Category:      event.Category,
			Layer:         event.Layer,
			Severity:      event.Severity,
			Source:        event.Source,
			RuleVersion:   event.RuleVersion,
			PatternDigest: event.PatternDigest,
			InputDigest:   event.InputDigest,
			MatchCount:    event.MatchCount,
		}
		if !canRevealSecurityEvent(event.UserID, actorRole, actorID, roles) {
			// Keep aggregate security facts usable while removing identity,
			// routing, and digest fields belonging to a peer or higher-level
			// administrator. This mirrors assistant-history hierarchy rules.
			item.RequestID = ""
			item.UserID = 0
			item.Username = ""
			item.TokenID = 0
			item.ChannelID = 0
			item.ModelName = ""
			item.Group = ""
			item.Endpoint = ""
			item.RuleID = ""
			item.RuleName = ""
			item.PatternDigest = ""
			item.InputDigest = ""
		}
		items = append(items, item)
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(items)
	common.ApiSuccess(c, pageInfo)
}

func securityEventTargetRoles(events []model.AdvancedSecurityEvent, actorRole, actorID int) map[int]int {
	roles := make(map[int]int)
	if actorRole >= common.RoleRootUser || model.DB == nil {
		return roles
	}
	ids := make([]int, 0, len(events))
	seen := make(map[int]struct{}, len(events))
	for _, event := range events {
		if event.UserID <= 0 || event.UserID == actorID {
			continue
		}
		if _, ok := seen[event.UserID]; ok {
			continue
		}
		seen[event.UserID] = struct{}{}
		ids = append(ids, event.UserID)
	}
	if len(ids) == 0 {
		return roles
	}
	var rows []struct {
		ID   int `gorm:"column:id"`
		Role int `gorm:"column:role"`
	}
	if err := model.DB.Model(&model.User{}).
		Select("id, role").Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return roles
	}
	for _, row := range rows {
		roles[row.ID] = row.Role
	}
	return roles
}

func canRevealSecurityEvent(userID, actorRole, actorID int, targetRoles map[int]int) bool {
	if userID <= 0 || userID == actorID || actorRole >= common.RoleRootUser {
		return true
	}
	targetRole, ok := targetRoles[userID]
	return ok && canManageTargetRole(actorRole, targetRole)
}

// ListAdminAssistantSecurityReviews exposes the asynchronous AI-review lane
// through Advanced Security. It returns bounded security metadata only; raw
// request/response previews remain behind the assistant-history ACL.
func ListAdminAssistantSecurityReviews(c *gin.Context) {
	filter, err := parseAdvancedSecurityEventFilter(c, false)
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	pageInfo := common.GetPageQuery(c)
	violationsOnly := strings.EqualFold(strings.TrimSpace(c.Query("violations_only")), "true") || filter.Decision == "violation"
	clearOnly := filter.Decision == "clear"
	rows, total, err := model.ListAssistantRequestReviewsForSecurity(model.AssistantRequestReviewFilter{
		StartTimestamp: filter.StartTimestamp,
		EndTimestamp:   filter.EndTimestamp,
		UserID:         filter.UserID,
		Category:       filter.Category,
		Group:          filter.Group,
		Decision:       filter.Decision,
		ViolationsOnly: violationsOnly,
		ClearOnly:      clearOnly,
		Limit:          pageInfo.GetPageSize(),
		Offset:         pageInfo.GetStartIdx(),
	})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	items := make([]dto.AdvancedSecurityAIReview, 0, len(rows))
	actorRole := c.GetInt("role")
	actorID := c.GetInt("id")
	for _, row := range rows {
		item := dto.AdvancedSecurityAIReview{
			ID: row.ID, CreatedAt: row.CreatedAt, RequestID: row.RequestID,
			UserID: row.UserID, Group: row.Group, ReviewModel: row.ReviewModel,
			Intensity: row.Intensity, Status: row.Status, Violation: row.Violation,
			Abuse: row.Abuse, Rules: row.Rules,
		}
		canSeeExplanation := actorRole >= common.RoleRootUser || row.UserID == actorID
		if !canSeeExplanation && row.UserID > 0 {
			if target, targetErr := model.GetUserById(row.UserID, false); targetErr == nil {
				canSeeExplanation = canManageTargetRole(actorRole, target.Role)
			}
		}
		if canSeeExplanation {
			item.Explanation = row.Explanation
		} else {
			item.RequestID = ""
			item.UserID = 0
			item.Rules = nil
		}
		items = append(items, item)
	}
	common.ApiSuccess(c, gin.H{
		"items":     items,
		"total":     total,
		"page":      pageInfo.GetPage(),
		"page_size": pageInfo.GetPageSize(),
		"available": model.AssistantRequestReviewTablesAvailable(),
	})
}

// ListAdminAssistantReviewTasks is the narrow, read-only history endpoint for
// Advanced Security. It is intentionally separate from /system-task, which
// remains RootAuth because it contains unrelated operational task data.
func ListAdminAssistantReviewTasks(c *gin.Context) {
	limit, _ := strconv.Atoi(c.Query("limit"))
	tasks, err := model.ListAssistantReviewTaskSummaries(limit)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	responses := make([]model.SystemTaskSummaryResponse, 0, len(tasks))
	for _, task := range tasks {
		responses = append(responses, task.ToSummaryResponse())
	}
	common.ApiSuccess(c, responses)
}

// GetAdminAssistantReviewTask returns one assistant-review run, and never a
// different system task even if a caller guesses its task ID.
const (
	assistantReviewCleanupDefaultKeep      = 30
	assistantReviewCleanupMaxKeep          = 100
	assistantReviewCleanupMaxExpectedCount = 100_000
)

type assistantReviewCleanupResponse struct {
	TaskType      string `json:"task_type"`
	Keep          int    `json:"keep"`
	EligibleCount int64  `json:"eligible_count"`
	DeletedCount  int64  `json:"deleted_count"`
}

// PreviewAdminAssistantReviewTaskCleanup reports only terminal assistant-review
// runs that are older than the requested retained history.
func PreviewAdminAssistantReviewTaskCleanup(c *gin.Context) {
	keep, ok := parseAssistantReviewCleanupKeep(c)
	if !ok {
		return
	}
	eligible, err := model.PreviewTaskHistoryCleanup(model.SystemTaskTypeAssistantReview, keep)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, assistantReviewCleanupResponse{
		TaskType: model.SystemTaskTypeAssistantReview, Keep: keep, EligibleCount: eligible,
	})
}

// DeleteAdminAssistantReviewTasks removes only terminal assistant-review task
// history after a proof scoped specifically to this destructive operation.
func DeleteAdminAssistantReviewTasks(c *gin.Context) {
	keep, ok := parseAssistantReviewCleanupKeep(c)
	if !ok {
		return
	}
	if !middleware.RequireSecurityProof(c, securityProofScopeReviewRunsDelete, nil) {
		return
	}
	expectedCount, ok := parseAssistantReviewCleanupExpectedCount(c)
	if !ok {
		return
	}
	deleted, err := model.CleanupTaskHistoryWithAudit(
		model.SystemTaskTypeAssistantReview, keep, expectedCount, c.GetInt("id"),
	)
	if errors.Is(err, model.ErrTaskHistoryCleanupStale) {
		c.JSON(http.StatusConflict, gin.H{
			"success": false,
			"code":    "STALE_PREVIEW",
			"message": "cleanup preview is stale; refresh and confirm again",
		})
		return
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, assistantReviewCleanupResponse{
		TaskType: model.SystemTaskTypeAssistantReview, Keep: keep,
		EligibleCount: deleted, DeletedCount: deleted,
	})
}

func parseAssistantReviewCleanupKeep(c *gin.Context) (int, bool) {
	raw := strings.TrimSpace(c.Query("keep"))
	if raw == "" {
		return assistantReviewCleanupDefaultKeep, true
	}
	keep, err := strconv.Atoi(raw)
	if err != nil || keep < 1 || keep > assistantReviewCleanupMaxKeep {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "keep must be an integer between 1 and 100",
		})
		return 0, false
	}
	return keep, true
}

func parseAssistantReviewCleanupExpectedCount(c *gin.Context) (int64, bool) {
	raw := strings.TrimSpace(c.Query("expected_count"))
	expectedCount, err := strconv.ParseInt(raw, 10, 64)
	if raw == "" || err != nil || expectedCount < 0 || expectedCount > assistantReviewCleanupMaxExpectedCount {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "expected_count must be an integer between 0 and 100000",
		})
		return 0, false
	}
	return expectedCount, true
}

func GetAdminAssistantReviewTask(c *gin.Context) {
	taskID := c.Param("task_id")
	if taskID == "" {
		common.ApiErrorMsg(c, "task id is required")
		return
	}
	task, err := model.GetAssistantReviewTaskByTaskID(taskID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if task == nil {
		c.JSON(404, gin.H{"success": false, "message": "task not found"})
		return
	}
	common.ApiSuccess(c, task.ToResponse())
}

func buildPublicSecurityPolicy() dto.PublicSecurityPolicy {
	settings := setting.GetAdvancedSecuritySettings()
	categories := setting.GetAdvancedSecurityRiskCategories()
	categoryDTOs := make([]dto.SecurityRiskCategory, 0, len(categories))
	for _, category := range categories {
		categoryDTOs = append(categoryDTOs, dto.SecurityRiskCategory{
			ID:          category.ID,
			Name:        category.Name,
			Layer:       category.Layer,
			Severity:    category.Severity,
			Description: category.Description,
			Source:      category.Source,
		})
	}

	publicRules := make([]dto.SecurityRuleSummary, 0, len(settings.RuleSet.Rules))
	for _, rule := range settings.RuleSet.Rules {
		if !rule.Enabled {
			continue
		}
		publicRules = append(publicRules, dto.SecurityRuleSummary{
			ID:          rule.ID,
			Name:        rule.Name,
			Category:    rule.Category,
			Layer:       rule.Layer,
			Severity:    rule.Severity,
			Source:      rule.Source,
			Version:     rule.Version,
			Description: rule.Description,
		})
	}

	violationFees := make([]dto.SecurityViolationFeeRule, 0)
	violationSettings := operation_setting.GetViolationFeeSettings()
	if violationSettings != nil {
		for _, policy := range violationSettings.Policies {
			amount := policy.InitialAmountUSD
			if len(policy.AmountsUSD) > 0 {
				amount = policy.AmountsUSD[0]
			}
			violationFees = append(violationFees, dto.SecurityViolationFeeRule{
				Code:              string(types.ErrorCodeViolationFeeUsagePolicy),
				Provider:          "",
				Groups:            append([]string(nil), policy.Groups...),
				Trigger:           "Any upstream usage-policy violation marker, regardless of model or provider.",
				Enabled:           violationSettings.Enabled && policy.Enabled,
				AmountUSD:         amount,
				AmountsUSD:        append([]float64(nil), policy.AmountsUSD...),
				Multiplier:        policy.Multiplier,
				MaxAmountUSD:      policy.MaxAmountUSD,
				PeriodSeconds:     policy.PeriodSeconds,
				ChargeUnit:        "per violating request",
				Retryable:         false,
				Description:       "The configured group policy charges an escalating penalty after a usage-policy violation.",
				ChargingNotes:     "The penalty is deducted from wallet quota only, never below zero. The counter resets after the configured period. Users may appeal and administrators may reverse an approved penalty.",
				LocalGuardrailFee: false,
			})
		}
	}

	return dto.PublicSecurityPolicy{
		PolicyVersion:          setting.AdvancedSecurityPolicyVersion,
		ReferenceEffectiveDate: setting.AdvancedSecurityPolicyReferenceDate,
		ReferenceURL:           setting.AdvancedSecurityPolicyReferenceURL,
		Alignment:              "Anthropic public Usage Policy risk areas, adapted for this relay; not an official equivalent",
		Enforcement: dto.SecuritySettings{
			Enabled:  settings.Enabled,
			OnPrompt: settings.OnPrompt,
			Action:   settings.Action,
		},
		ProtectedGroups: advancedSecurityProtectedGroups(settings),
		RiskCategories:  categoryDTOs,
		Rules:           publicRules,
		ViolationFees:   violationFees,
	}
}

func advancedSecurityProtectedGroups(settings setting.AdvancedSecuritySettings) []string {
	if !settings.Enabled {
		return []string{}
	}
	seen := make(map[string]struct{})
	for _, rule := range settings.RuleSet.Rules {
		if !rule.Enabled {
			continue
		}
		for _, group := range rule.Groups {
			group = strings.TrimSpace(group)
			if group != "" {
				seen[group] = struct{}{}
			}
		}
	}
	groups := make([]string, 0, len(seen))
	for group := range seen {
		groups = append(groups, group)
	}
	sort.Strings(groups)
	return groups
}

func violationFeeSettingsDTO() dto.SecurityViolationFeeSettings {
	settings := operation_setting.GetViolationFeeSettings()
	result := dto.SecurityViolationFeeSettings{}
	if settings == nil {
		return result
	}
	result.Enabled = settings.Enabled
	result.Policies = make([]dto.SecurityViolationFeePolicy, 0, len(settings.Policies))
	for _, policy := range settings.Policies {
		result.Policies = append(result.Policies, dto.SecurityViolationFeePolicy{
			Name: policy.Name, Groups: append([]string(nil), policy.Groups...), Enabled: policy.Enabled,
			AmountsUSD: append([]float64(nil), policy.AmountsUSD...), InitialAmountUSD: policy.InitialAmountUSD,
			Multiplier: policy.Multiplier, MaxAmountUSD: policy.MaxAmountUSD, PeriodSeconds: policy.PeriodSeconds,
			DrainBalanceWhenShort: policy.DrainBalanceWhenShort,
		})
	}
	return result
}

func buildSecurityStatsDTO(stats model.AdvancedSecurityStats, start, end int64, includeRules bool) dto.SecurityStats {
	result := dto.SecurityStats{
		StartTimestamp:   start,
		EndTimestamp:     end,
		TotalMatches:     stats.TotalMatches,
		BlockedMatches:   stats.BlockedMatches,
		AuditedMatches:   stats.AuditedMatches,
		AffectedRequests: stats.AffectedRequests,
		AffectedUsers:    stats.AffectedUsers,
		ByCategory:       make([]dto.SecurityStatBucket, 0, len(stats.ByCategory)),
	}
	for _, bucket := range stats.ByCategory {
		result.ByCategory = append(result.ByCategory, dto.SecurityStatBucket{Key: bucket.Key, Count: bucket.Count})
	}
	if includeRules {
		result.ByRule = make([]dto.SecurityStatBucket, 0, len(stats.ByRule))
		for _, bucket := range stats.ByRule {
			result.ByRule = append(result.ByRule, dto.SecurityStatBucket{Key: bucket.Key, Count: bucket.Count})
		}
		result.AIReview = &dto.AISecurityReviewStats{
			Total: stats.AIReview.Total, Completed: stats.AIReview.Completed,
			Violations: stats.AIReview.Violations, Abuses: stats.AIReview.Abuses,
			Failed:  stats.AIReview.Failed,
			ByGroup: make([]dto.SecurityStatBucket, 0, len(stats.AIReview.ByGroup)),
		}
		for _, bucket := range stats.AIReview.ByGroup {
			result.AIReview.ByGroup = append(result.AIReview.ByGroup, dto.SecurityStatBucket{Key: bucket.Key, Count: bucket.Count})
		}
	}
	return result
}

func publicSecurityStatsWindowBounds(c *gin.Context) (int64, int64) {
	now := time.Now().Unix()
	end := now
	start := now - int64(publicSecurityStatsWindow/time.Second)
	if parsed, err := strconv.ParseInt(c.Query("start_timestamp"), 10, 64); err == nil && parsed > 0 {
		start = parsed
	}
	if parsed, err := strconv.ParseInt(c.Query("end_timestamp"), 10, 64); err == nil && parsed > 0 {
		end = parsed
	}
	if end < start {
		return now - int64(publicSecurityStatsWindow/time.Second), now
	}
	maxStart := end - int64((90*24*time.Hour)/time.Second)
	if start < maxStart {
		start = maxStart
	}
	return start, end
}

func parseAdvancedSecurityEventFilter(c *gin.Context, includePaging bool) (model.AdvancedSecurityEventFilter, error) {
	filter := model.AdvancedSecurityEventFilter{}
	var err error
	if value := strings.TrimSpace(c.Query("start_timestamp")); value != "" {
		filter.StartTimestamp, err = strconv.ParseInt(value, 10, 64)
		if err != nil || filter.StartTimestamp < 0 {
			return filter, errors.New("invalid start_timestamp")
		}
	}
	if value := strings.TrimSpace(c.Query("end_timestamp")); value != "" {
		filter.EndTimestamp, err = strconv.ParseInt(value, 10, 64)
		if err != nil || filter.EndTimestamp < 0 {
			return filter, errors.New("invalid end_timestamp")
		}
	}
	if filter.StartTimestamp > 0 && filter.EndTimestamp > 0 && filter.EndTimestamp < filter.StartTimestamp {
		return filter, errors.New("end_timestamp must be greater than or equal to start_timestamp")
	}
	if value := strings.TrimSpace(c.Query("user_id")); value != "" {
		filter.UserID, err = strconv.Atoi(value)
		if err != nil || filter.UserID <= 0 {
			return filter, errors.New("invalid user_id")
		}
	}
	filter.RuleID = strings.TrimSpace(c.Query("rule_id"))
	filter.Category = strings.ToLower(strings.TrimSpace(c.Query("category")))
	filter.Group = strings.TrimSpace(c.Query("group"))
	filter.Source = strings.ToLower(strings.TrimSpace(c.Query("source")))
	filter.ModelName = strings.TrimSpace(c.Query("model_name"))
	filter.Decision = strings.ToLower(strings.TrimSpace(c.Query("decision")))
	if filter.Decision != "" && filter.Decision != model.AdvancedSecurityDecisionBlocked && filter.Decision != model.AdvancedSecurityDecisionAudited && filter.Decision != "violation" && filter.Decision != "clear" {
		return filter, errors.New("decision must be blocked, audited, violation, or clear")
	}
	if includePaging {
		pageInfo := common.GetPageQuery(c)
		filter.Limit = pageInfo.GetPageSize()
		filter.Offset = pageInfo.GetStartIdx()
	}
	return filter, nil
}
