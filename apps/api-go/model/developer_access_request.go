package model

import (
	"errors"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
)

const (
	DeveloperAccessRequestPending         = "pending"
	DeveloperAccessRequestApproved        = "approved"
	DeveloperAccessRequestRejected        = "rejected"
	DeveloperAccessRequestSourceAI        = "assistant_recommendation"
	DeveloperAccessRequestSourceUser      = "user_edited"
	DeveloperAccessRequestSourceAssistant = "assistant_request"
	DeveloperAccessRequestSourceOld       = "legacy"
	minDeveloperAccessRequestReason       = 5
	minDeveloperAccessReviewNote          = 2
	minDeveloperAccessRecommendation      = 20
	maxDeveloperAccessRequestNote         = 2000
)

var (
	ErrDeveloperAccessRequestNotFound         = errors.New("解锁申请不存在")
	ErrDeveloperAccessRequestReviewed         = errors.New("解锁申请已经处理")
	ErrDeveloperAccessRequestChanged          = errors.New("解锁申请内容已经变化")
	ErrDeveloperAccessRequestStatus           = errors.New("解锁申请状态无效")
	ErrDeveloperAccessRequestReasonTooShort   = errors.New("解锁申请说明至少需要 5 个字符")
	ErrDeveloperAccessRecommendationTooShort  = errors.New("AI 推荐信至少需要 20 个字符")
	ErrDeveloperAccessReviewNoteTooShort      = errors.New("管理员意见至少需要 2 个字符")
	ErrDeveloperAccessRequestNoteTooLong      = errors.New("解锁申请说明不能超过 2000 个字符")
	ErrDeveloperAccessRequestQueueUnavailable = errors.New("解锁申请队列暂时不可用")
)

// DeveloperAccessRequest records the non-payment path to L1 access. The
// request is deliberately separate from User.TrustLevelOverride: approving a
// request unlocks L1 without freezing later paid progression at that level.
type DeveloperAccessRequest struct {
	Id               int    `json:"id" gorm:"primaryKey"`
	UserId           int    `json:"user_id" gorm:"not null;index"`
	Status           string `json:"status" gorm:"type:varchar(20);not null;index"`
	Source           string `json:"source" gorm:"type:varchar(40);not null;default:legacy;index"`
	Reason           string `json:"reason" gorm:"type:text"`
	AIRecommendation string `json:"ai_recommendation" gorm:"type:text"`
	AdminUserId      int    `json:"admin_user_id" gorm:"index"`
	AdminNote        string `json:"admin_note" gorm:"type:text"`
	CreatedAt        int64  `json:"created_at" gorm:"not null;index"`
	ReviewedAt       int64  `json:"reviewed_at" gorm:"not null;default:0"`
	Revision         int64  `json:"-" gorm:"not null;default:0"`
}

func (DeveloperAccessRequest) TableName() string { return "developer_access_requests" }

type DeveloperAccessRequestView struct {
	DeveloperAccessRequest
	Username string `json:"username"`
	Email    string `json:"email"`
}

func normalizeDeveloperAccessRequestText(value string) (string, error) {
	value = strings.TrimSpace(value)
	if len([]rune(value)) > maxDeveloperAccessRequestNote {
		return "", ErrDeveloperAccessRequestNoteTooLong
	}
	return value, nil
}

func normalizeDeveloperAccessRequestReason(value string) (string, error) {
	value, err := normalizeDeveloperAccessRequestText(value)
	if err != nil {
		return "", err
	}
	if len([]rune(value)) < minDeveloperAccessRequestReason {
		return "", ErrDeveloperAccessRequestReasonTooShort
	}
	return value, nil
}

func normalizeDeveloperAccessRecommendation(value string) (string, error) {
	value, err := normalizeDeveloperAccessRequestText(value)
	if err != nil {
		return "", err
	}
	if len([]rune(value)) < minDeveloperAccessRecommendation {
		return "", ErrDeveloperAccessRecommendationTooShort
	}
	return redactAssistantHandoffMessage(value), nil
}

func normalizeDeveloperAccessReviewNote(value string) (string, error) {
	value, err := normalizeDeveloperAccessRequestText(value)
	if err != nil {
		return "", err
	}
	if len([]rune(value)) < minDeveloperAccessReviewNote {
		return "", ErrDeveloperAccessReviewNoteTooShort
	}
	return value, nil
}

func GetDeveloperAccessRequest(userID int) (*DeveloperAccessRequest, error) {
	if userID <= 0 {
		return nil, gorm.ErrInvalidData
	}
	var request DeveloperAccessRequest
	err := DB.Where("user_id = ?", userID).Order("id DESC").First(&request).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &request, nil
}

// reopenDeveloperAccessRequestForUserWithTx reopens the user's one letter when
// an administrator explicitly returns the account to L0.
func reopenDeveloperAccessRequestForUserWithTx(tx *gorm.DB, userID int) error {
	var latest DeveloperAccessRequest
	err := lockForUpdate(tx).Where("user_id = ?", userID).Order("id DESC").First(&latest).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if latest.Status != DeveloperAccessRequestApproved {
		return nil
	}

	return tx.Model(&latest).Updates(map[string]interface{}{
		"revision":      gorm.Expr("revision + 1"),
		"status":        DeveloperAccessRequestPending,
		"admin_user_id": 0,
		"admin_note":    "",
		"reviewed_at":   0,
	}).Error
}

func SubmitDeveloperAccessRequest(userID int, reason string) (*DeveloperAccessRequest, error) {
	return submitDeveloperAccessRequest(userID, reason, "", DeveloperAccessRequestSourceOld, false)
}

// SubmitAssistantDeveloperAccessRequest records a confirmed L1 request while
// preserving any existing recommendation letter for the same user.
func SubmitAssistantDeveloperAccessRequest(userID int, reason string) (*DeveloperAccessRequest, error) {
	return submitDeveloperAccessRequest(userID, reason, "", DeveloperAccessRequestSourceAssistant, false)
}

// SubmitAssistantDeveloperAccessRequestWithoutRecommendation records the
// user's explicit choice to remove the optional AI letter while retaining the
// same single pending L1 request.
func SubmitAssistantDeveloperAccessRequestWithoutRecommendation(userID int, reason string) (*DeveloperAccessRequest, error) {
	return submitDeveloperAccessRequest(userID, reason, "", DeveloperAccessRequestSourceAssistant, true)
}

func SubmitAssistantDeveloperAccessRecommendation(userID int, reason string, recommendation string) (*DeveloperAccessRequest, error) {
	return submitDeveloperAccessRequest(userID, reason, recommendation, DeveloperAccessRequestSourceAI, false)
}

// SubmitUserEditedDeveloperAccessRecommendation updates the user's one shared
// letter without falsely preserving AI provenance after a manual edit.
func SubmitUserEditedDeveloperAccessRecommendation(userID int, reason string, recommendation string) (*DeveloperAccessRequest, error) {
	return submitDeveloperAccessRequest(userID, reason, recommendation, DeveloperAccessRequestSourceUser, false)
}

// SubmitConfirmedAssistantDeveloperAccessRecommendation consumes the user's
// one-time confirmation and writes their one shared recommendation letter in
// the same transaction. A failed write leaves the confirmation reusable, and
// a consumed confirmation can never create or update the administrator queue
// a second time.
func SubmitConfirmedAssistantDeveloperAccessRecommendation(token string, match AuthFlowMatch, userID int, reason string, recommendation string) (*DeveloperAccessRequest, error) {
	if userID <= 0 {
		return nil, gorm.ErrInvalidData
	}
	if match.Purpose != AuthFlowPurposeAssistantL1 || match.UserId != userID || strings.TrimSpace(match.SessionId) == "" {
		return nil, ErrAuthFlowInvalid
	}
	normalizedReason, err := normalizeDeveloperAccessRequestReason(reason)
	if err != nil {
		return nil, err
	}
	normalizedRecommendation, err := normalizeDeveloperAccessRecommendation(recommendation)
	if err != nil {
		return nil, err
	}

	var request *DeveloperAccessRequest
	_, err = ConsumeAuthFlowWithAction(token, match, func(tx *gorm.DB, _ *AuthFlow) error {
		var submitErr error
		request, submitErr = submitNormalizedDeveloperAccessRequestWithTx(
			tx,
			userID,
			normalizedReason,
			normalizedRecommendation,
			DeveloperAccessRequestSourceAI,
			false,
		)
		return submitErr
	})
	if err != nil {
		if errors.Is(err, ErrAuthFlowInvalid) || errors.Is(err, ErrAuthFlowExpired) || errors.Is(err, ErrAuthFlowConsumed) {
			return nil, err
		}
		return nil, errors.Join(ErrDeveloperAccessRequestQueueUnavailable, err)
	}
	return request, nil
}

func submitDeveloperAccessRequest(userID int, reason string, recommendation string, source string, clearRecommendation bool) (*DeveloperAccessRequest, error) {
	if userID <= 0 {
		return nil, gorm.ErrInvalidData
	}
	normalizedReason, err := normalizeDeveloperAccessRequestReason(reason)
	if err != nil {
		return nil, err
	}
	normalizedRecommendation := ""
	if source == DeveloperAccessRequestSourceAI || source == DeveloperAccessRequestSourceUser {
		normalizedRecommendation, err = normalizeDeveloperAccessRecommendation(recommendation)
		if err != nil {
			return nil, err
		}
	}

	var request *DeveloperAccessRequest
	err = DB.Transaction(func(tx *gorm.DB) error {
		var submitErr error
		request, submitErr = submitNormalizedDeveloperAccessRequestWithTx(
			tx,
			userID,
			normalizedReason,
			normalizedRecommendation,
			source,
			clearRecommendation,
		)
		return submitErr
	})
	if err != nil {
		// Keep the database cause for diagnostics while giving HTTP callers a
		// stable classification. They must not treat a failed queue write as a
		// successful chat turn and should retry the same request instead.
		return nil, errors.Join(ErrDeveloperAccessRequestQueueUnavailable, err)
	}
	return request, nil
}

func submitNormalizedDeveloperAccessRequestWithTx(tx *gorm.DB, userID int, normalizedReason string, normalizedRecommendation string, source string, clearRecommendation bool) (*DeveloperAccessRequest, error) {
	// Lock the user row before checking for a pending request. This makes
	// duplicate submissions from two browser tabs collapse to one request on
	// databases that support row-level locks.
	if err := lockAssistantOwner(tx, userID); err != nil {
		return nil, err
	}
	var pending DeveloperAccessRequest
	findErr := tx.Where("user_id = ?", userID).Order("id DESC").First(&pending).Error
	if findErr == nil {
		if pending.Status == DeveloperAccessRequestApproved {
			return nil, ErrDeveloperAccessRequestReviewed
		}
		// A user has one active recommendation letter. AI suggestions and
		// manual edits update that same pending row instead of creating a
		// second queue item or preserving conflicting copies.
		updates := map[string]interface{}{
			"revision":      gorm.Expr("revision + 1"),
			"reason":        redactAssistantHandoffMessage(normalizedReason),
			"status":        DeveloperAccessRequestPending,
			"admin_user_id": 0,
			"admin_note":    "",
			"reviewed_at":   0,
		}
		pending.Revision++
		pending.Reason = updates["reason"].(string)
		pending.Status = DeveloperAccessRequestPending
		pending.AdminUserId = 0
		pending.AdminNote = ""
		pending.ReviewedAt = 0
		if source == DeveloperAccessRequestSourceAI || source == DeveloperAccessRequestSourceUser {
			updates["ai_recommendation"] = normalizedRecommendation
			updates["source"] = source
			pending.AIRecommendation = normalizedRecommendation
			pending.Source = source
		} else if clearRecommendation {
			updates["ai_recommendation"] = ""
			updates["source"] = DeveloperAccessRequestSourceAssistant
			pending.AIRecommendation = ""
			pending.Source = DeveloperAccessRequestSourceAssistant
		}
		if err := tx.Model(&pending).Updates(updates).Error; err != nil {
			return nil, err
		}
		return &pending, nil
	}
	if !errors.Is(findErr, gorm.ErrRecordNotFound) {
		return nil, findErr
	}
	request := &DeveloperAccessRequest{
		UserId:           userID,
		Status:           DeveloperAccessRequestPending,
		Source:           source,
		Reason:           redactAssistantHandoffMessage(normalizedReason),
		AIRecommendation: normalizedRecommendation,
		CreatedAt:        common.GetTimestamp(),
	}
	if err := tx.Create(request).Error; err != nil {
		return nil, err
	}
	return request, nil
}

func ListDeveloperAccessRequests(status string, limit int) ([]DeveloperAccessRequestView, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	query := DB.Table("developer_access_requests AS request").
		Select("request.*, users.username, users.email").
		Joins("JOIN users ON users.id = request.user_id").
		Order("request.id DESC").Limit(limit)
	if status = strings.TrimSpace(status); status != "" {
		if status != DeveloperAccessRequestPending && status != DeveloperAccessRequestApproved && status != DeveloperAccessRequestRejected {
			return nil, ErrDeveloperAccessRequestStatus
		}
		query = query.Where("request.status = ?", status)
		if status == DeveloperAccessRequestPending {
			// Legacy requests predate the single shared recommendation-letter
			// workflow. Keep them as history, but never surface or approve them as
			// actionable queue entries.
			query = query.Where("request.source <> ?", DeveloperAccessRequestSourceOld)
		}
	}
	var requests []DeveloperAccessRequestView
	if err := query.Find(&requests).Error; err != nil {
		return nil, err
	}
	return requests, nil
}

func ReviewDeveloperAccessRequest(adminUserID int, requestID int, approve bool, note string) (*DeveloperAccessRequest, error) {
	if adminUserID <= 0 || requestID <= 0 {
		return nil, gorm.ErrInvalidData
	}
	normalizedNote, err := normalizeDeveloperAccessReviewNote(note)
	if err != nil {
		return nil, err
	}
	var request DeveloperAccessRequest
	err = DB.Transaction(func(tx *gorm.DB) error {
		// Discover the owner, then use the same user -> request lock order as
		// submission, automatic review and administrator L0 resets.
		var owner DeveloperAccessRequest
		if err := tx.Select("id", "user_id").First(&owner, requestID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrDeveloperAccessRequestNotFound
			}
			return err
		}
		if err := lockAssistantOwner(tx, owner.UserId); err != nil {
			return err
		}
		if err := lockForUpdate(tx).Where("id = ?", requestID).First(&request).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrDeveloperAccessRequestNotFound
			}
			return err
		}
		if request.Status != DeveloperAccessRequestPending {
			return ErrDeveloperAccessRequestReviewed
		}
		if approve {
			// This timestamp is the durable non-payment activation fact. It
			// grants L1, while paid top-ups can still raise the automatic level.
			result := tx.Model(&User{}).
				Where("id = ?", request.UserId).
				Updates(map[string]interface{}{
					"console_activated_at": time.Now().Unix(),
					// A previous explicit L0 test reset must not survive a new
					// administrator approval.
					"trust_level_override": nil,
				})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 0 {
				var user User
				if err := tx.Select("id").First(&user, request.UserId).Error; err != nil {
					if errors.Is(err, gorm.ErrRecordNotFound) {
						return ErrDeveloperAccessRequestNotFound
					}
					return err
				}
			}
		}
		now := common.GetTimestamp()
		status := DeveloperAccessRequestRejected
		if approve {
			status = DeveloperAccessRequestApproved
		}
		if err := tx.Model(&request).Updates(map[string]interface{}{
			"revision":      gorm.Expr("revision + 1"),
			"status":        status,
			"admin_user_id": adminUserID,
			"admin_note":    normalizedNote,
			"reviewed_at":   now,
		}).Error; err != nil {
			return err
		}
		request.Revision++
		request.Status = status
		request.AdminUserId = adminUserID
		request.AdminNote = normalizedNote
		request.ReviewedAt = now
		if approve {
			if err := archiveApprovedDeveloperAccessRecommendation(tx, request); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if approve {
		_ = InvalidateUserCache(request.UserId)
	}
	return &request, nil
}
