package model

import (
	"context"
	"errors"
	"unicode"
	"unicode/utf8"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"gorm.io/gorm"
)

// NormalizeDeveloperAccessAutoReviewNote rejects visually empty replies and
// unsafe control characters in addition to redacting secrets and bounding size.
func NormalizeDeveloperAccessAutoReviewNote(note string) (string, error) {
	if !utf8.ValidString(note) {
		return "", errors.New("automatic review reply is not valid UTF-8")
	}
	note, err := normalizeDeveloperAccessReviewNote(RedactAssistantHistoryContent(note))
	if err != nil {
		return "", err
	}
	visible := 0
	for _, char := range note {
		if unicode.IsControl(char) && char != '\n' && char != '\r' && char != '\t' {
			return "", errors.New("automatic review reply contains invalid control characters")
		}
		if unicode.IsGraphic(char) && !unicode.IsSpace(char) {
			visible++
		}
	}
	if visible < minDeveloperAccessReviewNote {
		return "", ErrDeveloperAccessReviewNoteTooShort
	}
	return note, nil
}

// ApplyDeveloperAccessAutoReview fences both the submitted letter and reviewer
// configuration through commit. A human decision only adds the agent's reply;
// it never rejects a request or changes account permissions.
func ApplyDeveloperAccessAutoReview(ctx context.Context, adminUserID int, expected DeveloperAccessRequest, config setting.AssistantL1AutoReviewSettings, approve bool, note string) (*DeveloperAccessRequest, error) {
	if adminUserID <= 0 || expected.Id <= 0 || !config.UserAllowed(expected.UserId) {
		return nil, gorm.ErrInvalidData
	}
	switch expected.Source {
	case DeveloperAccessRequestSourceAI, DeveloperAccessRequestSourceUser, DeveloperAccessRequestSourceAssistant:
	default:
		return nil, gorm.ErrInvalidData
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	note, err := NormalizeDeveloperAccessAutoReviewNote(note)
	if err != nil {
		return nil, err
	}
	optionUpdateMutex.Lock()
	defer optionUpdateMutex.Unlock()
	if err := validateAssistantL1AutoReviewRoute(DB, config); err != nil {
		return nil, err
	}
	var request DeveloperAccessRequest
	err = setting.WithAssistantL1AutoReviewSettings(config, func() error {
		return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := checkAssistantL1AutoReviewOptions(tx, config); err != nil {
				return err
			}
			var reviewer User
			if err := lockForUpdate(tx).First(&reviewer, adminUserID).Error; err != nil {
				return err
			}
			if reviewer.Status != common.UserStatusEnabled || reviewer.Role != common.RoleRootUser {
				return errors.New("automatic L1 reviewer account is no longer an active root account")
			}
			// Match the submission lock order: user first, then request.
			var user User
			if err := lockForUpdate(tx).First(&user, expected.UserId).Error; err != nil {
				return err
			}
			if err := lockForUpdate(tx).First(&request, expected.Id).Error; err != nil {
				return err
			}
			if request.Status != DeveloperAccessRequestPending {
				return ErrDeveloperAccessRequestReviewed
			}
			if request != expected {
				return ErrDeveloperAccessRequestChanged
			}
			if user.Status != common.UserStatusEnabled || user.Role != common.RoleCommonUser {
				return errors.New("automatic L1 review requires an active ordinary user")
			}
			// An explicit administrator restriction is not an ordinary L0 signup.
			// Only a human may clear it, including a reset during an AI review.
			if user.TrustLevelOverride != nil {
				return errors.New("automatic L1 review cannot override an administrator's trust decision")
			}
			access, err := GetDeveloperAccessStateForUserBaseWithTx(tx, user.ToBaseUser(), CurrentDeveloperAccessPolicy())
			if err != nil {
				return err
			}
			if access.Granted {
				return errors.New("automatic L1 review only handles L0 requests")
			}
			if approve {
				if err := tx.Model(&user).Update("console_activated_at", common.GetTimestamp()).Error; err != nil {
					return err
				}
				request.Status = DeveloperAccessRequestApproved
				request.AdminUserId = adminUserID
				request.ReviewedAt = common.GetTimestamp()
			}
			request.AdminNote = note
			request.Revision++
			if err := tx.Model(&request).Updates(map[string]interface{}{
				"status": request.Status, "admin_user_id": request.AdminUserId,
				"admin_note": note, "reviewed_at": request.ReviewedAt, "revision": request.Revision,
			}).Error; err != nil {
				return err
			}
			if approve {
				return archiveApprovedDeveloperAccessRecommendation(tx, request)
			}
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	if approve {
		_ = InvalidateUserCache(request.UserId)
	}
	return &request, nil
}
