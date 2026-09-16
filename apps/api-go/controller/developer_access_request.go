package controller

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type developerAccessRequestInput struct {
	Reason            string `json:"reason"`
	AIRecommendation  string `json:"ai_recommendation"`
	ConfirmationToken string `json:"confirmation_token"`
	Confirmed         bool   `json:"confirmed"`
}

type developerAccessRequestReviewInput struct {
	Note string `json:"note"`
}

type developerAccessRequestSelfResponse struct {
	Id               int    `json:"id"`
	Status           string `json:"status"`
	Source           string `json:"source"`
	Reason           string `json:"reason"`
	AIRecommendation string `json:"ai_recommendation"`
	AdminNote        string `json:"admin_note"`
	CreatedAt        int64  `json:"created_at"`
	ReviewedAt       int64  `json:"reviewed_at"`
}

func toDeveloperAccessRequestSelfResponse(request *model.DeveloperAccessRequest) any {
	if request == nil {
		return nil
	}
	return developerAccessRequestSelfResponse{
		Id:               request.Id,
		Status:           request.Status,
		Source:           request.Source,
		Reason:           request.Reason,
		AIRecommendation: request.AIRecommendation,
		AdminNote:        request.AdminNote,
		CreatedAt:        request.CreatedAt,
		ReviewedAt:       request.ReviewedAt,
	}
}

func developerAccessRequestError(c *gin.Context, status int, code string, message string) {
	c.AbortWithStatusJSON(status, gin.H{
		"success": false,
		"code":    code,
		"message": message,
	})
}

func currentDeveloperAccessUser(c *gin.Context) (*model.UserBase, error) {
	userID := c.GetInt("id")
	if userID <= 0 {
		return nil, gorm.ErrInvalidData
	}
	return model.GetUserCache(userID)
}

func GetDeveloperAccessRequest(c *gin.Context) {
	request, err := model.GetDeveloperAccessRequest(c.GetInt("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, toDeveloperAccessRequestSelfResponse(request))
}

func SubmitDeveloperAccessRequest(c *gin.Context) {
	user, err := currentDeveloperAccessUser(c)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	access, err := model.GetDeveloperAccessStateForUserBase(user)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if access.Granted {
		developerAccessRequestError(c, http.StatusConflict, "DEVELOPER_ACCESS_ALREADY_ACTIVE", "developer access is already active")
		return
	}
	var input developerAccessRequestInput
	if err := c.ShouldBindJSON(&input); err != nil {
		developerAccessRequestError(c, http.StatusUnprocessableEntity, "DEVELOPER_ACCESS_INVALID_REQUEST", "invalid unlock request")
		return
	}
	if !input.Confirmed {
		developerAccessRequestError(c, http.StatusUnprocessableEntity, "DEVELOPER_ACCESS_CONFIRMATION_REQUIRED", "explicit confirmation is required before sending the request to an administrator")
		return
	}
	sessionID := strings.TrimSpace(c.GetString("session_id"))
	if sessionID == "" {
		developerAccessRequestError(c, http.StatusForbidden, "DEVELOPER_ACCESS_SESSION_REQUIRED", "a browser login session is required")
		return
	}
	var request *model.DeveloperAccessRequest
	var presetAttribution *model.PromptPresetRef
	if strings.TrimSpace(input.AIRecommendation) == "" && strings.TrimSpace(input.ConfirmationToken) == "" {
		// The no-AI path is deliberately first-class: the request enters the
		// same administrator queue with only the user's redacted statement.
		// This is not an approval and does not unlock L1.
		request, err = model.SubmitAssistantDeveloperAccessRequestWithoutRecommendation(user.Id, input.Reason)
	} else if strings.TrimSpace(input.ConfirmationToken) != "" {
		if strings.TrimSpace(input.AIRecommendation) == "" {
			developerAccessRequestError(c, http.StatusUnprocessableEntity, "DEVELOPER_ACCESS_AI_CONFIRMATION_INVALID", "AI recommendation confirmation is incomplete; continue the conversation to prepare a new one")
			return
		}
		// Validate the short-lived draft before atomically consuming its one-time
		// token and writing the user's one shared recommendation letter.
		flow, flowErr := model.GetAuthFlow(input.ConfirmationToken, model.AuthFlowMatch{
			Purpose:   model.AuthFlowPurposeAssistantL1,
			UserId:    user.Id,
			SessionId: sessionID,
		})
		if flowErr != nil {
			if errors.Is(flowErr, model.ErrAuthFlowInvalid) || errors.Is(flowErr, model.ErrAuthFlowExpired) || errors.Is(flowErr, model.ErrAuthFlowConsumed) {
				developerAccessRequestError(c, http.StatusUnprocessableEntity, "DEVELOPER_ACCESS_AI_CONFIRMATION_INVALID", "AI recommendation confirmation is invalid or expired; continue the conversation to prepare a new one")
				return
			}
			common.ApiError(c, flowErr)
			return
		}
		var draft assistantL1RecommendationDraft
		if json.Unmarshal([]byte(flow.Payload), &draft) != nil {
			developerAccessRequestError(c, http.StatusUnprocessableEntity, "DEVELOPER_ACCESS_AI_CONFIRMATION_MISMATCH", "AI recommendation draft is invalid")
			return
		}
		if draft.PresetId != "" && draft.PresetVersion != "" {
			presetAttribution = &model.PromptPresetRef{
				PresetId: draft.PresetId, Generation: draft.PresetGeneration, Version: draft.PresetVersion,
			}
		}
		request, err = model.SubmitConfirmedAssistantDeveloperAccessRecommendation(
			input.ConfirmationToken,
			model.AuthFlowMatch{
				Purpose:   model.AuthFlowPurposeAssistantL1,
				UserId:    user.Id,
				SessionId: sessionID,
			},
			user.Id,
			input.Reason,
			input.AIRecommendation,
		)
		if errors.Is(err, model.ErrAuthFlowInvalid) || errors.Is(err, model.ErrAuthFlowExpired) || errors.Is(err, model.ErrAuthFlowConsumed) {
			developerAccessRequestError(c, http.StatusUnprocessableEntity, "DEVELOPER_ACCESS_AI_CONFIRMATION_INVALID", "AI recommendation confirmation is invalid, expired, or already used")
			return
		}
	} else {
		// The signed-in user may edit the one shared recommendation letter
		// directly. AI drafts use a token for their first confirmation; later
		// human edits update the same pending row without creating another one.
		request, err = model.SubmitUserEditedDeveloperAccessRecommendation(user.Id, input.Reason, input.AIRecommendation)
	}
	if err != nil {
		switch {
		case errors.Is(err, model.ErrDeveloperAccessRequestQueueUnavailable):
			c.Header("Retry-After", "2")
			developerAccessRequestError(c, http.StatusServiceUnavailable, "DEVELOPER_ACCESS_QUEUE_UNAVAILABLE", "L1 申请暂时无法进入待处理队列，请使用相同内容重试")
			return
		case errors.Is(err, model.ErrDeveloperAccessRequestReasonTooShort):
			developerAccessRequestError(c, http.StatusUnprocessableEntity, "DEVELOPER_ACCESS_REASON_TOO_SHORT", err.Error())
			return
		case errors.Is(err, model.ErrDeveloperAccessRecommendationTooShort):
			developerAccessRequestError(c, http.StatusUnprocessableEntity, "DEVELOPER_ACCESS_RECOMMENDATION_TOO_SHORT", err.Error())
			return
		case errors.Is(err, model.ErrDeveloperAccessRequestNoteTooLong):
			developerAccessRequestError(c, http.StatusUnprocessableEntity, "DEVELOPER_ACCESS_REASON_TOO_LONG", err.Error())
			return
		}
		common.ApiError(c, err)
		return
	}
	if presetAttribution != nil {
		if statErr := model.CountPresetRecommendation(*presetAttribution, request.Id); statErr != nil {
			common.SysError("failed to record assistant preset recommendation for request " + strconv.Itoa(request.Id))
		}
	}
	// New applications (with or without an AI letter) enter the independently
	// configured, bounded review queue. A strict reviewer can approve clear legitimate requests; any
	// uncertainty, timeout, or malformed response leaves the request pending for
	// the existing human review path.
	common.ApiSuccess(c, toDeveloperAccessRequestSelfResponse(request))
}

func ListDeveloperAccessRequests(c *gin.Context) {
	requests, err := model.ListDeveloperAccessRequests(c.Query("status"), 100)
	if err != nil {
		if errors.Is(err, model.ErrDeveloperAccessRequestStatus) {
			developerAccessRequestError(c, http.StatusBadRequest, "DEVELOPER_ACCESS_INVALID_STATUS", "invalid request status")
			return
		}
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, requests)
}

// ListUserDeveloperAccessRecommendationArchives is an optional user-management
// view. It keeps the same strict role boundary as other cross-user admin data:
// an administrator may inspect a lower-role user, but never a peer or a
// higher-role administrator.
func ListUserDeveloperAccessRecommendationArchives(c *gin.Context) {
	userID, err := strconv.Atoi(c.Param("id"))
	if err != nil || userID <= 0 {
		developerAccessRequestError(c, http.StatusBadRequest, "DEVELOPER_ACCESS_INVALID_ID", "invalid user id")
		return
	}
	target, err := model.GetUserById(userID, false)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			developerAccessRequestError(c, http.StatusNotFound, "DEVELOPER_ACCESS_USER_NOT_FOUND", "user was not found")
			return
		}
		common.ApiError(c, err)
		return
	}
	if !canManageTargetRole(c.GetInt("role"), target.Role) {
		developerAccessRequestError(c, http.StatusForbidden, "DEVELOPER_ACCESS_ARCHIVE_FORBIDDEN", "you cannot view this user's recommendation archive")
		return
	}
	limit := 50
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		if parsed, parseErr := strconv.Atoi(raw); parseErr == nil {
			limit = parsed
		}
	}
	archives, err := model.ListDeveloperAccessRecommendationArchives(userID, limit)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, archives)
}

func reviewDeveloperAccessRequest(c *gin.Context, approve bool) {
	requestID, err := strconv.Atoi(c.Param("id"))
	if err != nil || requestID <= 0 {
		developerAccessRequestError(c, http.StatusBadRequest, "DEVELOPER_ACCESS_INVALID_ID", "invalid unlock request id")
		return
	}
	var input developerAccessRequestReviewInput
	if c.Request.ContentLength != 0 {
		if err := c.ShouldBindJSON(&input); err != nil {
			developerAccessRequestError(c, http.StatusUnprocessableEntity, "DEVELOPER_ACCESS_INVALID_REQUEST", "invalid review request")
			return
		}
	}
	request, err := model.ReviewDeveloperAccessRequest(c.GetInt("id"), requestID, approve, input.Note)
	if err != nil {
		switch {
		case errors.Is(err, model.ErrDeveloperAccessRequestNotFound):
			developerAccessRequestError(c, http.StatusNotFound, "DEVELOPER_ACCESS_REQUEST_NOT_FOUND", err.Error())
		case errors.Is(err, model.ErrDeveloperAccessRequestReviewed):
			developerAccessRequestError(c, http.StatusConflict, "DEVELOPER_ACCESS_REQUEST_ALREADY_REVIEWED", err.Error())
		case errors.Is(err, model.ErrDeveloperAccessReviewNoteTooShort):
			developerAccessRequestError(c, http.StatusUnprocessableEntity, "DEVELOPER_ACCESS_REVIEW_NOTE_TOO_SHORT", err.Error())
		case errors.Is(err, model.ErrDeveloperAccessRequestNoteTooLong):
			developerAccessRequestError(c, http.StatusUnprocessableEntity, "DEVELOPER_ACCESS_REVIEW_NOTE_TOO_LONG", err.Error())
		default:
			common.ApiError(c, err)
		}
		return
	}
	if approve {
		if statErr := model.CountPresetApproval(requestID); statErr != nil && !errors.Is(statErr, gorm.ErrRecordNotFound) {
			common.SysError("failed to record assistant preset approval for request " + strconv.Itoa(requestID))
		}
		model.RecordLog(c.GetInt("id"), model.LogTypeSystem, "approved developer access request "+strconv.Itoa(requestID))
	} else {
		model.RecordLog(c.GetInt("id"), model.LogTypeSystem, "rejected developer access request "+strconv.Itoa(requestID))
	}
	common.ApiSuccess(c, request)
}

func ApproveDeveloperAccessRequest(c *gin.Context) {
	reviewDeveloperAccessRequest(c, true)
}

func RejectDeveloperAccessRequest(c *gin.Context) {
	reviewDeveloperAccessRequest(c, false)
}

// RetiredDeveloperAccessRequest never creates a queue entry or consumes a legacy
// confirmation token. Existing requests remain readable for historical auditing.
func RetiredDeveloperAccessRequest(c *gin.Context) {
	developerAccessRequestError(c, http.StatusGone, "DEVELOPER_ACCESS_LETTER_RETIRED", "请继续与内置客服对话完成核验，无需提交推荐信。Continue with the built-in assistant; recommendation letters are no longer required.")
}
