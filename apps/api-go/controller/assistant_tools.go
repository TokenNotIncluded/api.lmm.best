package controller

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/i18n"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
)

type assistantHandoffInput struct {
	Confirmed         bool   `json:"confirmed"`
	Message           string `json:"message"`
	ConfirmationToken string `json:"confirmation_token"`
}

const assistantHandoffConfirmationTTL = 10 * time.Minute

type assistantResolveHandoffInput struct {
	Note string `json:"note"`
}

func requireAssistantBrowserSession(c *gin.Context) bool {
	if c.GetBool("use_access_token") {
		writeAssistantError(c, http.StatusForbidden, "ASSISTANT_SESSION_REQUIRED", errors.New("assistant tools require a browser login session"))
		return false
	}
	return true
}

func requireAssistantConfirmation(c *gin.Context, confirmed bool) bool {
	if !confirmed {
		writeAssistantError(c, http.StatusUnprocessableEntity, "ASSISTANT_CONFIRMATION_REQUIRED", errors.New("explicit confirmation is required"))
		return false
	}
	return true
}

func GetAssistantPricing(c *gin.Context) {
	if !requireAssistantBrowserSession(c) {
		return
	}
	if result, blocked := assistantDeveloperCapabilityRequired(c.GetInt("id"), "live model pricing"); blocked {
		message := inputString(result, "error")
		if message == "" {
			message = "L1 access is required for live model pricing"
		}
		writeAssistantError(c, http.StatusForbidden, "ASSISTANT_L1_REQUIRED", errors.New(message))
		return
	}
	status, response := buildPricingResponse(c, true)
	c.JSON(status, response)
}

func GetAssistantPlanOffers(c *gin.Context) {
	if !requireAssistantBrowserSession(c) {
		return
	}
	userID := c.GetInt("id")
	result := executeAssistantPlanOffersTool(userID)
	if ok, _ := result["ok"].(bool); ok {
		// The chat tool has a request-local payment-intent gate. A standalone
		// browser GET has no such proof, so an L0 session may inspect current
		// plans but must never turn this endpoint into a checkout bypass.
		if _, granted, err := getAssistantDeveloperAccess(userID); err != nil {
			writeAssistantError(c, http.StatusServiceUnavailable, "ASSISTANT_PERMISSION_STATE_UNAVAILABLE", errors.New("assistant permission state unavailable"))
			return
		} else if !granted {
			result["read_only"] = true
			result["checkout_available"] = false
			if _, restricted := result["status"]; !restricted {
				result["status"] = "l1_required"
			}
			if _, hidden := result["payment_hidden"]; !hidden {
				result["message"] = "Plan offers are view-only until L1 access is approved."
			}
		}
	}
	common.ApiSuccess(c, result)
}

func getAssistantDeveloperAccess(userID int) (*model.UserBase, bool, error) {
	user, err := model.GetUserCache(userID)
	if err != nil {
		return nil, false, err
	}
	access, err := model.GetDeveloperAccessStateForUserBase(user)
	if err != nil {
		return nil, false, err
	}
	return user, access.Granted, nil
}

func SubmitAssistantHandoff(c *gin.Context) {
	if !requireAssistantBrowserSession(c) {
		return
	}
	var input assistantHandoffInput
	if err := c.ShouldBindJSON(&input); err != nil {
		writeAssistantError(c, http.StatusBadRequest, "ASSISTANT_INVALID_REQUEST", errors.New("invalid support request"))
		return
	}
	if !requireAssistantConfirmation(c, input.Confirmed) {
		return
	}
	userID := c.GetInt("id")
	confirmationToken := strings.TrimSpace(input.ConfirmationToken)
	var (
		lead *model.AssistantLead
		err  error
	)
	if confirmationToken != "" {
		sessionID := strings.TrimSpace(c.GetString("session_id"))
		if sessionID == "" {
			writeAssistantError(c, http.StatusForbidden, "ASSISTANT_SESSION_REQUIRED", errors.New("a browser login session is required for assistant support confirmation"))
			return
		}
		lead, err = model.SubmitAssistantHandoffWithAuthFlow(
			confirmationToken,
			model.AuthFlowMatch{
				Purpose:   model.AuthFlowPurposeAssistantHandoff,
				UserId:    userID,
				SessionId: sessionID,
			},
		)
	} else {
		lead, err = model.SubmitAssistantHandoff(userID, input.Message)
	}
	if err != nil {
		switch {
		case errors.Is(err, model.ErrAuthFlowInvalid),
			errors.Is(err, model.ErrAuthFlowExpired),
			errors.Is(err, model.ErrAuthFlowConsumed):
			writeAssistantError(c, http.StatusUnprocessableEntity, "ASSISTANT_HANDOFF_CONFIRMATION_INVALID", errors.New("support confirmation is invalid, expired, or already used"))
		case errors.Is(err, model.ErrAssistantHandoffMessageRequired),
			errors.Is(err, model.ErrAssistantHandoffMessageTooShort),
			errors.Is(err, model.ErrAssistantHandoffMessageTooLong):
			writeAssistantError(c, http.StatusUnprocessableEntity, "ASSISTANT_HANDOFF_INVALID_MESSAGE", err)
		default:
			common.ApiError(c, err)
		}
		return
	}
	model.RecordLog(c.GetInt("id"), model.LogTypeSystem, fmt.Sprintf("submitted assistant support request %d", lead.Id))
	common.ApiSuccess(c, lead)
}

func GetAssistantHandoff(c *gin.Context) {
	lead, err := model.GetLatestAssistantHandoff(c.GetInt("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, lead)
}

func AdminListAssistantHandoffs(c *gin.Context) {
	leads, err := model.ListAssistantHandoffs(c.Query("status"), 100)
	if err != nil {
		if errors.Is(err, model.ErrAssistantLeadStatus) {
			writeAssistantError(c, http.StatusBadRequest, "ASSISTANT_HANDOFF_INVALID_STATUS", err)
			return
		}
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, leads)
}

func assistantSummarySince(c *gin.Context, errorCode string) (int64, bool) {
	days := 30
	if raw := strings.TrimSpace(c.Query("days")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 365 {
			writeAssistantError(c, http.StatusBadRequest, errorCode, errors.New("days must be between 1 and 365"))
			return 0, false
		}
		days = parsed
	}
	return time.Now().Add(-time.Duration(days) * 24 * time.Hour).Unix(), true
}

func AdminGetAssistantIntentSummary(c *gin.Context) {
	since, ok := assistantSummarySince(c, "ASSISTANT_INTENT_DAYS_INVALID")
	if !ok {
		return
	}
	summary, err := model.ListAssistantIntentSummary(since)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, summary)
}

func AdminGetAssistantFirstQuestionSummary(c *gin.Context) {
	since, ok := assistantSummarySince(c, "ASSISTANT_FIRST_QUESTION_DAYS_INVALID")
	if !ok {
		return
	}
	summary, err := model.ListAssistantFirstQuestionSummary(since)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, summary)
}

func AdminGetAssistantProfileSummary(c *gin.Context) {
	since, ok := assistantSummarySince(c, "ASSISTANT_PROFILE_DAYS_INVALID")
	if !ok {
		return
	}
	summary, err := model.ListAssistantProfileSummary(since)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, summary)
}

func AdminGetAssistantReview(c *gin.Context) {
	task, err := model.GetLatestSystemTask(model.SystemTaskTypeAssistantReview)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if task == nil {
		common.ApiSuccess(c, nil)
		return
	}
	common.ApiSuccess(c, task.ToResponse())
}

func AdminRunAssistantReview(c *gin.Context) {
	task, err := service.StartAssistantReview()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, task.ToResponse())
}

func AdminListAssistantRequestReviews(c *gin.Context) {
	userID, err := strconv.Atoi(strings.TrimSpace(c.Query("user_id")))
	if err != nil || userID <= 0 {
		writeAssistantError(c, http.StatusBadRequest, "ASSISTANT_REVIEW_USER_INVALID", errors.New("user_id must be a positive integer"))
		return
	}
	target, err := model.GetUserById(userID, false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if target.Id != c.GetInt("id") && !canManageTargetRole(c.GetInt("role"), target.Role) {
		common.ApiErrorI18n(c, i18n.MsgUserNoPermissionSameLevel)
		return
	}
	page := 1
	if raw := strings.TrimSpace(c.Query("page")); raw != "" {
		page, err = strconv.Atoi(raw)
		if err != nil || page < 1 {
			writeAssistantError(c, http.StatusBadRequest, "ASSISTANT_REVIEW_PAGE_INVALID", errors.New("page must be a positive integer"))
			return
		}
	}
	limit := model.AssistantRequestReviewPageMax
	if raw := strings.TrimSpace(c.Query("page_size")); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > model.AssistantRequestReviewPageMax {
			writeAssistantError(c, http.StatusBadRequest, "ASSISTANT_REVIEW_PAGE_SIZE_INVALID", errors.New("page_size must be between 1 and 100"))
			return
		}
	}
	violationsOnly := strings.EqualFold(strings.TrimSpace(c.Query("violations_only")), "true")
	rows, total, err := model.ListAssistantRequestReviews(userID, violationsOnly, (page-1)*limit, limit)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	count, err := model.AssistantReviewViolationCount(userID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	resetAt, err := model.AssistantReviewResetAt(userID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{
		"items": rows, "total": total, "page": page, "page_size": limit,
		"violation_count": count, "reset_at": resetAt,
		// Queue saturation is process-wide and intentionally read-only. Expose
		// the bounded review coverage counters to administrators so dropped
		// samples are visible without delaying user requests.
		"queue_stats": assistantReviewQueueStatsSnapshot(),
	})
}

func AdminResetAssistantRequestReviewViolations(c *gin.Context) {
	userID, err := strconv.Atoi(strings.TrimSpace(c.Param("user_id")))
	if err != nil || userID <= 0 {
		writeAssistantError(c, http.StatusBadRequest, "ASSISTANT_REVIEW_USER_INVALID", errors.New("user_id must be a positive integer"))
		return
	}
	target, err := model.GetUserById(userID, false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if target.Id != c.GetInt("id") && !canManageTargetRole(c.GetInt("role"), target.Role) {
		common.ApiErrorI18n(c, i18n.MsgUserNoPermissionSameLevel)
		return
	}
	now := common.GetTimestamp()
	if err := model.ResetAssistantReviewViolations(userID, now); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"user_id": userID, "violation_count": 0, "reset_at": now})
}

func AdminGetAssistantFundingSummary(c *gin.Context) {
	since, ok := assistantSummarySince(c, "ASSISTANT_FUNDING_DAYS_INVALID")
	if !ok {
		return
	}
	billingUser, err := loadAssistantBillingUser()
	if err != nil || billingUser == nil {
		writeAssistantError(c, http.StatusServiceUnavailable, "ASSISTANT_BILLING_ACCOUNT_UNAVAILABLE", errors.New("AI assistant billing account is unavailable"))
		return
	}
	end := time.Now().Unix()
	summary, err := model.GetAssistantFundingSummary(billingUser.Id, since, end)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	remainingQuota, err := model.GetUserQuota(billingUser.Id, true)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	remainingUSD := float64(remainingQuota)
	if common.QuotaPerUnit > 0 {
		remainingUSD /= common.QuotaPerUnit
	}
	common.ApiSuccess(c, gin.H{
		"start_timestamp":   summary.StartTimestamp,
		"end_timestamp":     summary.EndTimestamp,
		"requests":          summary.Requests,
		"prompt_tokens":     summary.PromptTokens,
		"completion_tokens": summary.CompletionTokens,
		"total_tokens":      summary.TotalTokens,
		"quota":             summary.Quota,
		"cost_usd":          summary.CostUSD,
		"remaining_quota":   remainingQuota,
		"remaining_usd":     remainingUSD,
	})
}

func AdminResolveAssistantHandoff(c *gin.Context) {
	leadID, err := strconv.Atoi(c.Param("id"))
	if err != nil || leadID <= 0 {
		writeAssistantError(c, http.StatusBadRequest, "ASSISTANT_HANDOFF_INVALID_ID", errors.New("invalid support request id"))
		return
	}
	var input assistantResolveHandoffInput
	if c.Request.ContentLength != 0 {
		if err := c.ShouldBindJSON(&input); err != nil {
			writeAssistantError(c, http.StatusBadRequest, "ASSISTANT_INVALID_REQUEST", errors.New("invalid support resolution"))
			return
		}
	}
	lead, err := model.ResolveAssistantHandoff(c.GetInt("id"), leadID, input.Note)
	if err != nil {
		switch {
		case errors.Is(err, model.ErrAssistantLeadNotFound):
			writeAssistantError(c, http.StatusNotFound, "ASSISTANT_HANDOFF_NOT_FOUND", err)
		case errors.Is(err, model.ErrAssistantLeadAlreadyResolved):
			writeAssistantError(c, http.StatusConflict, "ASSISTANT_HANDOFF_ALREADY_RESOLVED", err)
		case errors.Is(err, model.ErrAssistantAdminNoteTooLong):
			writeAssistantError(c, http.StatusUnprocessableEntity, "ASSISTANT_HANDOFF_NOTE_TOO_LONG", err)
		default:
			common.ApiError(c, err)
		}
		return
	}
	model.RecordLog(c.GetInt("id"), model.LogTypeSystem, fmt.Sprintf("resolved assistant support request %d", lead.Id))
	common.ApiSuccess(c, lead)
}
