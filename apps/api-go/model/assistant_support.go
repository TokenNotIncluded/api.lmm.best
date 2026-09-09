package model

import (
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	AssistantSupportKindHandoff     = "handoff"
	AssistantSupportKindAppointment = "appointment"
	AssistantSupportStatusPending   = "pending"
	AssistantSupportStatusAccepted  = "accepted"
	AssistantSupportStatusCompleted = "completed"
	AssistantSupportStatusCancelled = "cancelled"
)

var (
	ErrAssistantSupportForbidden  = errors.New("this account cannot access this support request")
	ErrAssistantSupportNotFound   = errors.New("human support request not found")
	ErrAssistantSupportConflict   = errors.New("human support request has already changed")
	ErrAssistantSupportIneligible = errors.New("a completed paid recharge is required to book technical support")
	ErrAssistantSupportInvalid    = errors.New("invalid human support request")
	ErrAssistantSupportAIBlocked  = errors.New("human support is handling this conversation")
)

// ActiveUserId is nullable so closed requests can coexist while the unique
// index prevents two active requests, including on different server instances.
// ClosedHistoryMessageId freezes a closed request's transcript boundary.
type AssistantSupportRequest struct {
	Id                     int    `json:"id" gorm:"primaryKey"`
	UserId                 int    `json:"user_id" gorm:"not null;index"`
	ConversationId         int64  `json:"conversation_id" gorm:"not null;index"`
	Kind                   string `json:"kind" gorm:"type:varchar(24);not null"`
	Status                 string `json:"status" gorm:"type:varchar(24);not null;index"`
	Topic                  string `json:"topic" gorm:"type:text;not null"`
	PreferredTime          string `json:"preferred_time" gorm:"type:varchar(255);not null"`
	ScheduledAt            int64  `json:"scheduled_at" gorm:"not null;default:0"`
	AssignedAdminId        int    `json:"assigned_admin_id" gorm:"not null;default:0;index"`
	AssignedAdminName      string `json:"assigned_admin_name" gorm:"type:varchar(512);not null;default:''"`
	ActiveUserId           *int   `json:"-" gorm:"uniqueIndex"`
	CreatedAt              int64  `json:"created_at" gorm:"not null;index"`
	UpdatedAt              int64  `json:"updated_at" gorm:"not null;index"`
	AcceptedAt             int64  `json:"accepted_at" gorm:"not null;default:0"`
	ClosedAt               int64  `json:"closed_at" gorm:"not null;default:0"`
	ClosedHistoryMessageId int64  `json:"-" gorm:"not null;default:0"`
}

func (AssistantSupportRequest) TableName() string { return "assistant_support_requests" }

func assistantSupportActor(tx *gorm.DB, actorID int) (*User, error) {
	if actorID <= 0 {
		return nil, ErrAssistantSupportForbidden
	}
	var actor User
	if err := tx.Select("id", "role", "status", "username", "display_name").First(&actor, actorID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrAssistantSupportForbidden
		}
		return nil, err
	}
	if actor.Status != common.UserStatusEnabled {
		return nil, ErrAssistantSupportForbidden
	}
	return &actor, nil
}

func assistantSupportIsAdmin(actor *User) bool {
	return actor != nil && (actor.Role == common.RoleAdminUser || actor.Role == common.RoleRootUser)
}

func assistantSupportEligibleTx(tx *gorm.DB, userID int) (bool, error) {
	expression, args := positiveNormalizedCreditedQuotaSQL()
	var count int64
	err := successfulExternalPaidTopUpQuery(tx.Model(&TopUp{})).
		Where("user_id = ? AND complete_time > 0 AND refunded_amount_micros = 0 AND refunded_quota = 0", userID).
		Where("("+expression+") > 0", args...).Count(&count).Error
	return count > 0, err
}

func IsAssistantSupportEligible(userID int) (bool, error) {
	if _, err := assistantSupportActor(DB, userID); err != nil {
		return false, err
	}
	return assistantSupportEligibleTx(DB, userID)
}

func assistantSupportText(value string, limit int) string {
	value = RedactAssistantHistoryContent(value)
	runes := []rune(value)
	if len(runes) > limit {
		return string(runes[:limit])
	}
	return value
}

func assistantSupportName(actor *User) string {
	name := actor.DisplayName
	if strings.TrimSpace(name) == "" {
		name = actor.Username
	}
	return assistantSupportText(name, 120)
}

// CreateAssistantSupportRequest never trusts client role, balance or model
// claims of payment. Appointments record a requested time, not staff acceptance.
func CreateAssistantSupportRequest(userID int, conversationID int64, kind, topic, preferredTime string, scheduledAt int64) (*AssistantSupportRequest, bool, error) {
	topic = strings.TrimSpace(topic)
	preferredTime = strings.TrimSpace(preferredTime)
	if conversationID < 0 || (kind != AssistantSupportKindHandoff && kind != AssistantSupportKindAppointment) || utf8.RuneCountInString(topic) > 2000 || utf8.RuneCountInString(preferredTime) > 200 || len(topic) > 8000 || len(preferredTime) > 800 {
		return nil, false, ErrAssistantSupportInvalid
	}
	if kind == AssistantSupportKindHandoff {
		scheduledAt = 0
		preferredTime = ""
	}
	var request AssistantSupportRequest
	created := false
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := lockAssistantOwner(tx, userID); err != nil {
			return ErrAssistantSupportForbidden
		}
		if _, err := assistantSupportActor(tx, userID); err != nil {
			return err
		}
		// Validate supplied ownership even if an earlier request is already active.
		var conversation AssistantConversation
		if conversationID > 0 {
			if err := lockForUpdate(tx).Where("id = ? AND user_id = ?", conversationID, userID).First(&conversation).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return ErrAssistantConversationNotFound
				}
				return err
			}
		}
		existing := tx.Where("active_user_id = ?", userID).First(&request).Error
		if existing == nil {
			if kind == AssistantSupportKindHandoff && request.Kind == AssistantSupportKindAppointment && request.Status == AssistantSupportStatusPending {
				if err := tx.Model(&request).Updates(map[string]any{"kind": kind, "scheduled_at": 0, "preferred_time": "", "updated_at": common.GetTimestamp()}).Error; err != nil {
					return err
				}
				if err := resetAssistantSupportTodosTx(tx, request.Id); err != nil {
					return err
				}
			}
			return nil
		}
		if !errors.Is(existing, gorm.ErrRecordNotFound) {
			return existing
		}
		if kind == AssistantSupportKindAppointment {
			now := time.Now().Unix()
			if topic == "" || preferredTime == "" || scheduledAt <= now || scheduledAt > now+int64(90*24*time.Hour/time.Second) {
				return ErrAssistantSupportInvalid
			}
			eligible, err := assistantSupportEligibleTx(tx, userID)
			if err != nil {
				return err
			}
			if !eligible {
				return ErrAssistantSupportIneligible
			}
		}
		if conversationID == 0 {
			now := common.GetTimestamp()
			conversation = AssistantConversation{UserId: userID, Title: assistantConversationTitle(topic), LastMessagePreview: assistantConversationTitle(topic), CreatedAt: now, UpdatedAt: now}
			if conversation.Title == "" {
				conversation.Title = "人工技术支持"
			}
			if err := tx.Create(&conversation).Error; err != nil {
				return err
			}
		}
		now := common.GetTimestamp()
		request = AssistantSupportRequest{UserId: userID, ConversationId: conversation.Id, Kind: kind, Status: AssistantSupportStatusPending, Topic: assistantSupportText(topic, 2000), PreferredTime: assistantSupportText(preferredTime, 200), ScheduledAt: scheduledAt, ActiveUserId: &userID, CreatedAt: now, UpdatedAt: now}
		// The owner lock serializes normal writers; ON CONFLICT also protects
		// against legacy writers which do not acquire that lock.
		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&request)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return tx.Where("active_user_id = ?", userID).First(&request).Error
		}
		created = true
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return &request, created, nil
}

// GetActiveAssistantSupportRequest also releases assignments whose operator
// lost access. The owner's normal status poll makes such requests claimable
// again without preserving authority from an old browser session.
func GetActiveAssistantSupportRequest(userID int) (*AssistantSupportRequest, error) {
	var request AssistantSupportRequest
	found := false
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := lockAssistantOwner(tx, userID); err != nil {
			return ErrAssistantSupportForbidden
		}
		if _, err := assistantSupportActor(tx, userID); err != nil {
			return err
		}
		err := lockForUpdate(tx).Where("active_user_id = ?", userID).First(&request).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		found = true
		if request.Status == AssistantSupportStatusAccepted {
			actor, err := assistantSupportActor(tx, request.AssignedAdminId)
			if err != nil && !errors.Is(err, ErrAssistantSupportForbidden) {
				return err
			}
			if err != nil || !assistantSupportIsAdmin(actor) {
				if err := tx.Model(&request).Where("status = ? AND assigned_admin_id = ?", AssistantSupportStatusAccepted, request.AssignedAdminId).Updates(map[string]any{"status": AssistantSupportStatusPending, "assigned_admin_id": 0, "assigned_admin_name": "", "accepted_at": 0, "updated_at": common.GetTimestamp()}).Error; err != nil {
					return err
				}
				return resetAssistantSupportTodosTx(tx, request.Id)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}
	return &request, nil
}

func GetLatestAssistantSupportRequest(userID int, conversationID int64) (*AssistantSupportRequest, error) {
	active, err := GetActiveAssistantSupportRequest(userID)
	if err != nil {
		return nil, err
	}
	if conversationID == 0 {
		return active, nil
	}
	if _, err := assistantSupportActor(DB, userID); err != nil {
		return nil, err
	}
	var conversation AssistantConversation
	if err := DB.Where("id = ? AND user_id = ?", conversationID, userID).First(&conversation).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrAssistantConversationNotFound
		}
		return nil, err
	}
	var request AssistantSupportRequest
	err = DB.Where("user_id = ? AND conversation_id = ?", userID, conversationID).Order("id DESC").First(&request).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &request, nil
}

func assistantSupportRequestTx(tx *gorm.DB, id int) (*AssistantSupportRequest, error) {
	if id <= 0 {
		return nil, ErrAssistantSupportNotFound
	}
	var request AssistantSupportRequest
	if err := tx.First(&request, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrAssistantSupportNotFound
		}
		return nil, err
	}
	return &request, nil
}

func GetAssistantSupportRequest(actorID, id int) (*AssistantSupportRequest, error) {
	actor, err := assistantSupportActor(DB, actorID)
	if err != nil {
		return nil, err
	}
	request, err := assistantSupportRequestTx(DB, id)
	if err != nil {
		return nil, err
	}
	if actor.Id != request.UserId && !(assistantSupportIsAdmin(actor) && (request.Status == AssistantSupportStatusPending || request.AssignedAdminId == actor.Id)) {
		return nil, ErrAssistantSupportForbidden
	}
	return request, nil
}

func GetAssistantSupportMessages(actorID, id int) ([]AssistantHistoryMessageView, error) {
	views := make([]AssistantHistoryMessageView, 0)
	_, err := withAssistantSupportWrite(actorID, id, func(tx *gorm.DB, actor *User, request *AssistantSupportRequest) error {
		if actor.Id != request.UserId && !(assistantSupportIsAdmin(actor) && request.AssignedAdminId == actor.Id) {
			return ErrAssistantSupportForbidden
		}
		var messages []AssistantHistoryMessage
		query := tx.Where("conversation_id = ? AND role IN ?", request.ConversationId, []string{AssistantHistoryRoleUser, AssistantHistoryRoleAssistant, AssistantHistoryRoleHuman})
		if request.ClosedAt > 0 {
			query = query.Where("id <= ?", request.ClosedHistoryMessageId)
		}
		if err := query.Order("sequence DESC").Limit(assistantHistoryPageMax).Find(&messages).Error; err != nil {
			return err
		}
		for i := len(messages) - 1; i >= 0; i-- {
			views = append(views, assistantSupportMessageView(messages[i]))
		}
		return nil
	})
	return views, err
}

func assistantSupportMessageView(message AssistantHistoryMessage) AssistantHistoryMessageView {
	return AssistantHistoryMessageView{Id: message.Id, Role: message.Role, Content: message.Content, ActorName: message.ActorName, CreatedAt: message.CreatedAt, PrivacyNotice: AssistantHistoryPrivacyNotice}
}

// Lock the owner first, as all AI history writes and account deletion do.
// Locking only the request would leave a race with an already-running AI turn.
func withAssistantSupportWrite(actorID, id int, update func(*gorm.DB, *User, *AssistantSupportRequest) error) (*AssistantSupportRequest, error) {
	initial, err := assistantSupportRequestTx(DB, id)
	if err != nil {
		return nil, err
	}
	var request *AssistantSupportRequest
	err = DB.Transaction(func(tx *gorm.DB) error {
		var lockedUsers []User
		if err := lockForUpdate(tx).Select("id").Where("id IN ?", []int{initial.UserId, actorID}).Order("id ASC").Find(&lockedUsers).Error; err != nil {
			return err
		}
		ownerPresent := false
		for _, user := range lockedUsers {
			if user.Id == initial.UserId {
				ownerPresent = true
			}
		}
		if !ownerPresent {
			return ErrAssistantSupportNotFound
		}
		actor, err := assistantSupportActor(tx, actorID)
		if err != nil {
			return err
		}
		var conversation AssistantConversation
		if err := lockForUpdate(tx).Where("id = ? AND user_id = ?", initial.ConversationId, initial.UserId).First(&conversation).Error; err != nil {
			return ErrAssistantSupportNotFound
		}
		request, err = assistantSupportRequestTx(lockForUpdate(tx), id)
		if err != nil {
			return err
		}
		return update(tx, actor, request)
	})
	if err != nil {
		return nil, err
	}
	return request, nil
}

func AcceptAssistantSupportRequest(adminID, id int) (*AssistantSupportRequest, error) {
	return withAssistantSupportWrite(adminID, id, func(tx *gorm.DB, actor *User, request *AssistantSupportRequest) error {
		if !assistantSupportIsAdmin(actor) || actor.Id == request.UserId {
			return ErrAssistantSupportForbidden
		}
		if request.Status == AssistantSupportStatusAccepted && request.AssignedAdminId == actor.Id {
			return nil
		}
		if request.Status != AssistantSupportStatusPending {
			return ErrAssistantSupportConflict
		}
		now := common.GetTimestamp()
		result := tx.Model(request).Where("status = ? AND assigned_admin_id = 0", AssistantSupportStatusPending).Updates(map[string]any{"status": AssistantSupportStatusAccepted, "assigned_admin_id": actor.Id, "assigned_admin_name": assistantSupportName(actor), "accepted_at": now, "updated_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrAssistantSupportConflict
		}
		return resetAssistantSupportTodosTx(tx, id)
	})
}

func AddAssistantSupportMessage(actorID, id int, content string) (*AssistantHistoryMessageView, error) {
	content = strings.TrimSpace(content)
	if content == "" || utf8.RuneCountInString(content) > assistantHistoryMessageMaxRunes || len(content) > 4*assistantHistoryMessageMaxRunes {
		return nil, ErrAssistantSupportInvalid
	}
	var view AssistantHistoryMessageView
	_, err := withAssistantSupportWrite(actorID, id, func(tx *gorm.DB, actor *User, request *AssistantSupportRequest) error {
		if request.Status != AssistantSupportStatusPending && request.Status != AssistantSupportStatusAccepted {
			return ErrAssistantSupportConflict
		}
		role := AssistantHistoryRoleUser
		if actor.Id != request.UserId {
			if !assistantSupportIsAdmin(actor) || request.Status != AssistantSupportStatusAccepted || request.AssignedAdminId != actor.Id {
				return ErrAssistantSupportForbidden
			}
			role = AssistantHistoryRoleHuman
		}
		message, err := appendAssistantHistoryMessageTx(tx, request.ConversationId, role, content)
		if err != nil {
			return err
		}
		if role == AssistantHistoryRoleHuman {
			message.ActorUserId = actor.Id
			message.ActorName = assistantSupportName(actor)
			if err := tx.Model(message).Updates(map[string]any{"actor_user_id": message.ActorUserId, "actor_name": message.ActorName}).Error; err != nil {
				return err
			}
		}
		now := common.GetTimestamp()
		if err := tx.Model(&AssistantConversation{}).Where("id = ?", request.ConversationId).Updates(map[string]any{"updated_at": now, "last_message_preview": assistantConversationTitle(message.Content)}).Error; err != nil {
			return err
		}
		if err := tx.Model(request).Update("updated_at", now).Error; err != nil {
			return err
		}
		if role == AssistantHistoryRoleUser {
			if err := resetAssistantSupportTodosTx(tx, id); err != nil {
				return err
			}
		}
		view = assistantSupportMessageView(*message)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &view, nil
}

func CloseAssistantSupportRequest(actorID, id int, cancel bool) (*AssistantSupportRequest, error) {
	return withAssistantSupportWrite(actorID, id, func(tx *gorm.DB, actor *User, request *AssistantSupportRequest) error {
		status := AssistantSupportStatusCompleted
		if cancel {
			if actor.Id != request.UserId {
				return ErrAssistantSupportForbidden
			}
			status = AssistantSupportStatusCancelled
		} else if !assistantSupportIsAdmin(actor) || request.AssignedAdminId != actor.Id || actor.Id == request.UserId {
			return ErrAssistantSupportForbidden
		}
		if request.Status == status {
			return nil
		}
		if request.Status != AssistantSupportStatusPending && request.Status != AssistantSupportStatusAccepted {
			return ErrAssistantSupportConflict
		}
		if !cancel && request.Status != AssistantSupportStatusAccepted {
			return ErrAssistantSupportConflict
		}
		var lastID int64
		if err := tx.Model(&AssistantHistoryMessage{}).Where("conversation_id = ?", request.ConversationId).Select("COALESCE(MAX(id),0)").Scan(&lastID).Error; err != nil {
			return err
		}
		now := common.GetTimestamp()
		return tx.Model(request).Updates(map[string]any{"status": status, "active_user_id": nil, "closed_at": now, "updated_at": now, "closed_history_message_id": lastID}).Error
	})
}

func resetAssistantSupportTodosTx(tx *gorm.DB, id int) error {
	return tx.Where("category = ? AND item_id = ?", UnifiedTodoCategoryHumanSupport, id).Delete(&UnifiedTodoRead{}).Error
}

func blockAssistantAIForSupportTx(tx *gorm.DB, userID int, conversationID int64) (bool, error) {
	query := tx.Model(&AssistantSupportRequest{}).Where("active_user_id = ? AND (status = ? OR (status = ? AND kind = ?))", userID, AssistantSupportStatusAccepted, AssistantSupportStatusPending, AssistantSupportKindHandoff)
	if conversationID > 0 {
		query = query.Where("conversation_id = ?", conversationID)
	}
	var count int64
	err := query.Count(&count).Error
	return count > 0, err
}

func BlockAssistantAIForSupport(userID int, conversationID int64) (bool, error) {
	return blockAssistantAIForSupportTx(DB, userID, conversationID)
}
