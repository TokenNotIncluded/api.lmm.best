package model

import (
	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
)

// A support request is shared by the whole staff queue. Joining the current
// viewer record keeps a stale session role from granting access after demotion.
func unifiedHumanSupportQuery(db *gorm.DB, userID int) *gorm.DB {
	return db.Table("assistant_support_requests AS support").
		Joins("JOIN users AS requester ON requester.id = support.user_id AND requester.deleted_at IS NULL").
		Joins("JOIN users AS viewer ON viewer.id = ? AND viewer.deleted_at IS NULL", userID).
		Where("viewer.role >= ? AND viewer.status = ?", common.RoleAdminUser, common.UserStatusEnabled).
		Where("support.status = ? OR (support.status = ? AND support.assigned_admin_id = ?)",
			AssistantSupportStatusPending, AssistantSupportStatusAccepted, userID)
}

func unifiedHumanSupportCount(db *gorm.DB, userID int, unreadOnly bool) (int64, error) {
	query := unifiedHumanSupportQuery(db, userID)
	if unreadOnly {
		query = query.Where(`NOT EXISTS (
			SELECT 1 FROM unified_todo_reads AS read_marker
			WHERE read_marker.user_id = ? AND read_marker.category = ? AND read_marker.item_id = support.id
		)`, userID, UnifiedTodoCategoryHumanSupport)
	}
	return unifiedTodoCount(query)
}

func unifiedHumanSupportCandidates(db *gorm.DB, userID int, ids []int) ([]unifiedTodoCandidate, error) {
	if len(ids) == 0 {
		return []unifiedTodoCandidate{}, nil
	}
	var rows []struct {
		AssistantSupportRequest
		Username string
	}
	if err := unifiedHumanSupportQuery(db, userID).
		Select("support.*, requester.username").Where("support.id IN ?", ids).Scan(&rows).Error; err != nil {
		return nil, err
	}
	items := make([]unifiedTodoCandidate, 0, len(rows))
	for _, row := range rows {
		items = append(items, unifiedTodoCandidate{Item: UnifiedTodoItem{
			Id:       unifiedTodoItemID(UnifiedTodoCategoryHumanSupport, row.Id),
			SourceId: row.Id, Category: UnifiedTodoCategoryHumanSupport,
			Type: row.Kind, Title: "assistant.human_support", Summary: RedactAssistantHistoryContent(row.Topic),
			CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
			Details: map[string]any{
				"request_id": row.Id, "conversation_id": row.ConversationId,
				"kind": row.Kind, "status": row.Status,
				"topic":          RedactAssistantHistoryContent(row.Topic),
				"preferred_time": RedactAssistantHistoryContent(row.PreferredTime),
				"scheduled_at":   row.ScheduledAt,
				"user_id":        row.UserId, "username": RedactAssistantHistoryContent(row.Username),
				"assigned_admin_id":   row.AssignedAdminId,
				"assigned_admin_name": RedactAssistantHistoryContent(row.AssignedAdminName),
			},
		}})
	}
	return items, nil
}
