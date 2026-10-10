/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
package controller

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// assistantAccountDisableAuthFlowPurpose is intentionally local to the
// controller package. The opaque token is persisted as a hash by AuthFlow and
// is bound to both the actor's browser session and user ID.
const assistantAccountDisableAuthFlowPurpose = "assistant_account_disable"

type assistantAccountDisableDraft struct {
	TargetUserID int    `json:"target_user_id"`
	Reason       string `json:"reason"`
}

type accountActionRequestInput struct {
	Confirmed         bool   `json:"confirmed"`
	ConfirmationToken string `json:"confirmation_token"`
	Reason            string `json:"reason"`
	TargetUserID      int    `json:"target_user_id"`
}

type accountAppealInput struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Reason   string `json:"reason"`
}

type accountActionReviewInput struct {
	Note string `json:"note"`
}

func accountActionError(c *gin.Context, status int, code string, err error) {
	c.AbortWithStatusJSON(status, gin.H{
		"success": false,
		"code":    code,
		"message": err.Error(),
	})
}

func notifyAccountActionRequest(request *model.AccountActionRequest) {
	if request == nil || request.Id <= 0 {
		return
	}
	subject := "账号操作申请待管理员审核"
	content := fmt.Sprintf(
		"有一条账号操作申请待审核。申请 #%d，类型=%s，目标用户=%d，请在用户管理页面审核。\n申请说明：%s",
		request.Id,
		request.Kind,
		request.TargetUserId,
		request.Reason,
	)
	// NotifyRootUser logs delivery failures internally. It is deliberately
	// called after the request transaction has committed, so a missing email or
	// notification channel can never roll back the durable queue row.
	service.NotifyRootUser("account_action_request", subject, content)
}

func submitAssistantAccountDisableRequest(c *gin.Context, input accountActionRequestInput) {
	if !requireAssistantToolEnabled(c, "request_human_support") {
		return
	}
	if c.GetBool("use_access_token") {
		accountActionError(c, http.StatusForbidden, "ACCOUNT_ACTION_SESSION_REQUIRED", errors.New("账号操作申请必须使用浏览器登录会话"))
		return
	}
	if !input.Confirmed {
		accountActionError(c, http.StatusUnprocessableEntity, "ACCOUNT_ACTION_CONFIRMATION_REQUIRED", errors.New("请先在页面中明确确认此账号操作申请"))
		return
	}
	sessionID := strings.TrimSpace(c.GetString("session_id"))
	actorID := c.GetInt("id")
	if actorID <= 0 || sessionID == "" {
		accountActionError(c, http.StatusForbidden, "ACCOUNT_ACTION_SESSION_REQUIRED", errors.New("账号操作申请需要有效的浏览器登录会话"))
		return
	}
	flow, err := model.ConsumeAuthFlow(input.ConfirmationToken, model.AuthFlowMatch{
		Purpose:   assistantAccountDisableAuthFlowPurpose,
		UserId:    actorID,
		SessionId: sessionID,
	})
	if err != nil {
		if errors.Is(err, model.ErrAuthFlowInvalid) || errors.Is(err, model.ErrAuthFlowExpired) || errors.Is(err, model.ErrAuthFlowConsumed) {
			accountActionError(c, http.StatusUnprocessableEntity, "ACCOUNT_ACTION_CONFIRMATION_INVALID", errors.New("账号操作确认已失效，请重新与助手确认"))
			return
		}
		common.ApiError(c, err)
		return
	}
	var draft assistantAccountDisableDraft
	if json.Unmarshal([]byte(flow.Payload), &draft) != nil ||
		strings.TrimSpace(input.Reason) != draft.Reason ||
		(input.TargetUserID > 0 && input.TargetUserID != draft.TargetUserID) {
		accountActionError(c, http.StatusUnprocessableEntity, "ACCOUNT_ACTION_CONFIRMATION_MISMATCH", errors.New("确认内容与助手生成的申请不一致"))
		return
	}
	request, err := model.SubmitAccountDisableRequest(actorID, draft.TargetUserID, draft.Reason)
	if err != nil {
		writeAccountActionModelError(c, err)
		return
	}
	if request.Created {
		notifyAccountActionRequest(request)
		model.RecordLog(actorID, model.LogTypeSystem, fmt.Sprintf("submitted assistant account disable request %d", request.Id))
	}
	common.ApiSuccess(c, request)
}

// SubmitAccountActionRequest is the confirmation endpoint for the existing
// assistant action pattern. It creates a pending proposal only; it never
// changes User.Status.
func SubmitAccountActionRequest(c *gin.Context) {
	var input accountActionRequestInput
	if err := c.ShouldBindJSON(&input); err != nil {
		accountActionError(c, http.StatusBadRequest, "ACCOUNT_ACTION_INVALID_REQUEST", errors.New("账号操作申请格式无效"))
		return
	}
	submitAssistantAccountDisableRequest(c, input)
}

func currentAccountAppealUser(c *gin.Context, input accountAppealInput) (int, error) {
	if userID := c.GetInt("id"); userID > 0 {
		if strings.TrimSpace(c.GetString("session_id")) == "" {
			return 0, model.ErrAccountActionInvalidIdentity
		}
		user, err := model.GetUserById(userID, false)
		if err != nil {
			return 0, err
		}
		if user.Status != common.UserStatusDisabled {
			return 0, model.ErrAccountActionUserState
		}
		return user.Id, nil
	}

	// A disabled account has no usable dashboard session after approval of a
	// disable action. The fallback therefore proves possession of the account's
	// password over the TLS-protected API and is additionally protected by the
	// route's critical limiter and optional Turnstile check. No user-specific
	// error is returned, preventing account enumeration.
	username := strings.TrimSpace(input.Username)
	password := input.Password
	if username == "" || password == "" {
		return 0, model.ErrAccountActionInvalidIdentity
	}
	var user model.User
	if err := model.DB.Where("username = ? OR email = ?", username, username).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, model.ErrAccountActionInvalidIdentity
		}
		return 0, err
	}
	if user.Status != common.UserStatusDisabled || !common.ValidatePasswordAndHash(password, user.Password) {
		return 0, model.ErrAccountActionInvalidIdentity
	}
	return user.Id, nil
}

// SubmitAccountAppeal supports both an authenticated session and the
// disabled-account password fallback described in currentAccountAppealUser.
func SubmitAccountAppeal(c *gin.Context) {
	var input accountAppealInput
	if err := c.ShouldBindJSON(&input); err != nil {
		accountActionError(c, http.StatusBadRequest, "ACCOUNT_APPEAL_INVALID_REQUEST", errors.New("解封申请格式无效"))
		return
	}
	userID, err := currentAccountAppealUser(c, input)
	if err != nil {
		if errors.Is(err, model.ErrAccountActionInvalidIdentity) {
			accountActionError(c, http.StatusUnauthorized, "ACCOUNT_APPEAL_IDENTITY_INVALID", errors.New("账号身份校验失败或暂不允许提交解封申请"))
			return
		}
		if errors.Is(err, model.ErrAccountActionUserState) {
			accountActionError(c, http.StatusConflict, "ACCOUNT_APPEAL_NOT_NEEDED", err)
			return
		}
		common.ApiError(c, err)
		return
	}
	request, err := model.SubmitAccountAppeal(userID, input.Reason)
	if err != nil {
		writeAccountActionModelError(c, err)
		return
	}
	if request.Created {
		notifyAccountActionRequest(request)
		model.RecordLog(userID, model.LogTypeSystem, fmt.Sprintf("submitted account appeal %d", request.Id))
	}
	common.ApiSuccess(c, request)
}

func GetAccountAppeal(c *gin.Context) {
	request, err := model.GetLatestAccountActionRequest(c.GetInt("id"), model.AccountActionKindAppeal)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, request)
}

func ListAccountActionRequests(c *gin.Context) {
	requests, err := model.ListAccountActionRequests(c.Query("status"), c.Query("kind"), 100)
	if err != nil {
		switch {
		case errors.Is(err, model.ErrAccountActionRequestStatus):
			accountActionError(c, http.StatusBadRequest, "ACCOUNT_ACTION_INVALID_STATUS", err)
		case errors.Is(err, model.ErrAccountActionRequestKind):
			accountActionError(c, http.StatusBadRequest, "ACCOUNT_ACTION_INVALID_KIND", err)
		default:
			common.ApiError(c, err)
		}
		return
	}
	common.ApiSuccess(c, requests)
}

func reviewAccountActionRequest(c *gin.Context, approve bool) {
	requestID, err := strconv.Atoi(strings.TrimSpace(c.Param("id")))
	if err != nil || requestID <= 0 {
		accountActionError(c, http.StatusBadRequest, "ACCOUNT_ACTION_INVALID_ID", errors.New("账号操作申请编号无效"))
		return
	}
	var input accountActionReviewInput
	if c.Request.ContentLength != 0 {
		if err := c.ShouldBindJSON(&input); err != nil {
			accountActionError(c, http.StatusBadRequest, "ACCOUNT_ACTION_INVALID_REVIEW", errors.New("管理员审核意见格式无效"))
			return
		}
	}
	request, err := model.ReviewAccountActionRequest(c.GetInt("id"), c.GetInt("role"), requestID, approve, input.Note)
	if err != nil {
		writeAccountActionModelError(c, err)
		return
	}
	if approve {
		model.RecordLog(c.GetInt("id"), model.LogTypeSystem, fmt.Sprintf("approved account action request %d", requestID))
		recordManageAuditFor(c, request.TargetUserId, "user.account_action.approve", map[string]interface{}{
			"request_id": request.Id,
			"kind":       request.Kind,
		})
	} else {
		model.RecordLog(c.GetInt("id"), model.LogTypeSystem, fmt.Sprintf("rejected account action request %d", requestID))
		recordManageAuditFor(c, request.TargetUserId, "user.account_action.reject", map[string]interface{}{
			"request_id": request.Id,
			"kind":       request.Kind,
		})
	}
	common.ApiSuccess(c, request)
}

func ApproveAccountActionRequest(c *gin.Context) {
	reviewAccountActionRequest(c, true)
}

func RejectAccountActionRequest(c *gin.Context) {
	reviewAccountActionRequest(c, false)
}

func writeAccountActionModelError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, model.ErrAccountActionReasonTooShort):
		accountActionError(c, http.StatusUnprocessableEntity, "ACCOUNT_ACTION_REASON_TOO_SHORT", err)
	case errors.Is(err, model.ErrAccountActionReasonTooLong):
		accountActionError(c, http.StatusUnprocessableEntity, "ACCOUNT_ACTION_REASON_TOO_LONG", err)
	case errors.Is(err, model.ErrAccountActionReviewNoteTooShort):
		accountActionError(c, http.StatusUnprocessableEntity, "ACCOUNT_ACTION_REVIEW_NOTE_REQUIRED", err)
	case errors.Is(err, model.ErrAccountActionReviewNoteTooLong):
		accountActionError(c, http.StatusUnprocessableEntity, "ACCOUNT_ACTION_REVIEW_NOTE_TOO_LONG", err)
	case errors.Is(err, model.ErrAccountActionRequestNotFound):
		accountActionError(c, http.StatusNotFound, "ACCOUNT_ACTION_REQUEST_NOT_FOUND", err)
	case errors.Is(err, model.ErrAccountActionRequestReviewed):
		accountActionError(c, http.StatusConflict, "ACCOUNT_ACTION_REQUEST_ALREADY_REVIEWED", err)
	case errors.Is(err, model.ErrAccountActionTargetForbidden):
		accountActionError(c, http.StatusForbidden, "ACCOUNT_ACTION_TARGET_FORBIDDEN", err)
	case errors.Is(err, model.ErrAccountActionRootProtected):
		accountActionError(c, http.StatusForbidden, "ACCOUNT_ACTION_ROOT_PROTECTED", err)
	case errors.Is(err, model.ErrAccountActionUserState), errors.Is(err, model.ErrAccountActionApprovalState):
		accountActionError(c, http.StatusConflict, "ACCOUNT_ACTION_STATE_CONFLICT", err)
	case errors.Is(err, model.ErrAccountActionRequestKind):
		accountActionError(c, http.StatusBadRequest, "ACCOUNT_ACTION_INVALID_KIND", err)
	default:
		common.ApiError(c, err)
	}
}
