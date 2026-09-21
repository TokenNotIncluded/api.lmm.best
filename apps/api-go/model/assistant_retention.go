package model

import (
	"context"
	"time"

	"gorm.io/gorm"
)

const assistantRetentionBatchMax = 500

type AssistantRetentionCutoffs struct {
	ActiveBefore     int64 `json:"active_before"`
	ArchivedBefore   int64 `json:"archived_before"`
	RestrictedBefore int64 `json:"restricted_before"`
}

type AssistantRetentionDeleteResult struct {
	Conversations  int64 `json:"conversations"`
	Messages       int64 `json:"messages"`
	SecureCards    int64 `json:"secure_cards"`
	Incidents      int64 `json:"incidents"`
	IntentLeads    int64 `json:"intent_leads"`
	ProfileAudits  int64 `json:"profile_audits"`
	ProfileBuckets int64 `json:"profile_buckets"`
	FirstQuestions int64 `json:"first_questions"`
	SecurityEvents int64 `json:"security_events"`
	GiftRiskMemory int64 `json:"gift_risk_memory"`
	RequestReviews int64 `json:"request_reviews"`
}

func NormalizeAssistantRetentionBatchSize(batchSize int) int {
	if batchSize <= 0 {
		return 200
	}
	if batchSize > assistantRetentionBatchMax {
		return assistantRetentionBatchMax
	}
	return batchSize
}

func AssistantRetentionCutoffsFromNow(now time.Time, activeDays, archivedDays, restrictedDays int) AssistantRetentionCutoffs {
	return AssistantRetentionCutoffs{
		ActiveBefore:     now.Add(-time.Duration(activeDays) * 24 * time.Hour).Unix(),
		ArchivedBefore:   now.Add(-time.Duration(archivedDays) * 24 * time.Hour).Unix(),
		RestrictedBefore: now.Add(-time.Duration(restrictedDays) * 24 * time.Hour).Unix(),
	}
}

func assistantRetentionEligible(query *gorm.DB, cutoffs AssistantRetentionCutoffs) *gorm.DB {
	query = query.Where("id NOT IN (?)", query.Session(&gorm.Session{NewDB: true}).Model(&AssistantSupportRequest{}).Select("conversation_id").Where("active_user_id IS NOT NULL"))
	return query.Where(
		"(restricted_at > 0 AND restricted_at < ?) OR "+
			"(restricted_at = 0 AND archived_at > 0 AND archived_at < ?) OR "+
			"(restricted_at = 0 AND archived_at = 0 AND updated_at < ?)",
		cutoffs.RestrictedBefore,
		cutoffs.ArchivedBefore,
		cutoffs.ActiveBefore,
	)
}

// PurgeAssistantConversationsBefore deletes one bounded batch. Eligibility is
// rechecked while the rows are locked, so a conversation updated after a
// scheduler payload was created is never deleted by a stale candidate list.
func PurgeAssistantConversationsBefore(ctx context.Context, cutoffs AssistantRetentionCutoffs, batchSize int) (AssistantRetentionDeleteResult, error) {
	result := AssistantRetentionDeleteResult{}
	if cutoffs.ActiveBefore <= 0 || cutoffs.ArchivedBefore <= 0 || cutoffs.RestrictedBefore <= 0 {
		return result, gorm.ErrInvalidData
	}
	batchSize = NormalizeAssistantRetentionBatchSize(batchSize)

	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var conversations []AssistantConversation
		query := lockForUpdate(tx.WithContext(ctx)).Model(&AssistantConversation{}).
			Select("id").Order("id ASC").Limit(batchSize)
		if err := assistantRetentionEligible(query, cutoffs).Find(&conversations).Error; err != nil {
			return err
		}
		if len(conversations) == 0 {
			return nil
		}

		conversationIDs := make([]int64, 0, len(conversations))
		for _, conversation := range conversations {
			conversationIDs = append(conversationIDs, conversation.Id)
		}

		// SELECT FOR UPDATE may have waited on a creator whose insert was not
		// visible in that statement's snapshot. Recheck after the locks are
		// acquired, before deleting any transcript or request rows. A locking
		// read also uses current committed data under MySQL REPEATABLE READ.
		var activeIDs []int64
		if err := lockForUpdate(tx).Model(&AssistantSupportRequest{}).Where("conversation_id IN ? AND active_user_id IS NOT NULL", conversationIDs).Pluck("conversation_id", &activeIDs).Error; err != nil {
			return err
		}
		active := make(map[int64]bool, len(activeIDs))
		for _, id := range activeIDs {
			active[id] = true
		}
		retained := conversationIDs[:0]
		for _, id := range conversationIDs {
			if !active[id] {
				retained = append(retained, id)
			}
		}
		conversationIDs = retained
		if len(conversationIDs) == 0 {
			return nil
		}

		var incidentIDs []int
		if err := tx.Model(&AssistantSecurityIncident{}).
			Where("conversation_id IN ?", conversationIDs).
			Pluck("id", &incidentIDs).Error; err != nil {
			return err
		}
		if len(incidentIDs) > 0 {
			if err := tx.Where("category = ? AND item_id IN ?", UnifiedTodoCategorySecurityIncident, incidentIDs).
				Delete(&UnifiedTodoRead{}).Error; err != nil {
				return err
			}
		}

		requests := tx.Model(&AssistantSupportRequest{}).Select("id").Where("conversation_id IN ?", conversationIDs)
		if err := tx.Where("category = ? AND item_id IN (?)", UnifiedTodoCategoryHumanSupport, requests).Delete(&UnifiedTodoRead{}).Error; err != nil {
			return err
		}
		if err := tx.Where("conversation_id IN ?", conversationIDs).Delete(&AssistantSupportRequest{}).Error; err != nil {
			return err
		}
		cards := tx.Where("conversation_id IN ?", conversationIDs).Delete(&AssistantSecureCard{})
		if cards.Error != nil {
			return cards.Error
		}
		messages := tx.Where("conversation_id IN ?", conversationIDs).Delete(&AssistantHistoryMessage{})
		if messages.Error != nil {
			return messages.Error
		}
		incidents := tx.Where("conversation_id IN ?", conversationIDs).Delete(&AssistantSecurityIncident{})
		if incidents.Error != nil {
			return incidents.Error
		}
		deleted := assistantRetentionEligible(
			tx.Where("id IN ?", conversationIDs),
			cutoffs,
		).Delete(&AssistantConversation{})
		if deleted.Error != nil {
			return deleted.Error
		}

		result.Conversations = deleted.RowsAffected
		result.Messages = messages.RowsAffected
		result.SecureCards = cards.RowsAffected
		result.Incidents = incidents.RowsAffected
		return nil
	})
	return result, err
}

// ScrubExpiredAssistantSecureCards erases ciphertext while retaining harmless
// card metadata for conversation transcripts. Standalone cards (the direct
// key-creation path has no conversation to display them in) are removed once
// they can no longer be revealed. The selection is bounded and safe to call
// repeatedly.
func ScrubExpiredAssistantSecureCards(ctx context.Context, now int64, batchSize int) (int64, error) {
	if now <= 0 {
		return 0, gorm.ErrInvalidData
	}
	batchSize = NormalizeAssistantRetentionBatchSize(batchSize)
	var ids []string
	if err := DB.WithContext(ctx).Model(&AssistantSecureCard{}).
		Where("(ciphertext <> '' OR conversation_id = 0) AND (expires_at <= ? OR revealed_at > 0)", now).
		Order("created_at ASC").Limit(batchSize).Pluck("id", &ids).Error; err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	updated := DB.WithContext(ctx).Model(&AssistantSecureCard{}).
		Where("id IN ? AND ciphertext <> '' AND (expires_at <= ? OR revealed_at > 0)", ids, now).
		Update("ciphertext", "")
	if updated.Error != nil {
		return 0, updated.Error
	}
	deleted := DB.WithContext(ctx).
		Where("id IN ? AND conversation_id = 0 AND (expires_at <= ? OR revealed_at > 0)", ids, now).
		Delete(&AssistantSecureCard{})
	if deleted.Error != nil {
		return 0, deleted.Error
	}
	// Count rows selected rather than SQL statements affected: a standalone
	// card is both scrubbed and deleted in this pass.
	return int64(len(ids)), nil
}

// PurgeAdvancedSecurityEventsBefore removes old rule-match rows in bounded
// batches. Security events contain only digests and metadata, but they are
// produced per matched rule; without a retention boundary this audit table can
// grow with traffic forever.
func PurgeAdvancedSecurityEventsBefore(ctx context.Context, cutoff int64, batchSize int) (int64, error) {
	if cutoff <= 0 {
		return 0, gorm.ErrInvalidData
	}
	batchSize = NormalizeAssistantRetentionBatchSize(batchSize)
	var ids []uint
	if err := DB.WithContext(ctx).Model(&AdvancedSecurityEvent{}).
		Where("created_at < ?", cutoff).
		Order("created_at ASC, id ASC").Limit(batchSize).Pluck("id", &ids).Error; err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	deleted := DB.WithContext(ctx).Where("id IN ? AND created_at < ?", ids, cutoff).
		Delete(&AdvancedSecurityEvent{})
	return deleted.RowsAffected, deleted.Error
}

// PurgeAssistantGiftNetworkRiskBefore removes stale network risk counters in
// bounded batches. Network counters roll over after assistantGiftRiskAge, so a
// row older than the retention cutoff cannot contribute to a future decision.
// Identity counters are intentionally retained forever: they enforce the
// one-opportunity rule across accounts and must not be removed by retention.
// The timestamp predicate is repeated on delete so a concurrent gift decision
// that refreshed a selected row is never lost.
func PurgeAssistantGiftNetworkRiskBefore(ctx context.Context, cutoff int64, batchSize int) (int64, error) {
	if cutoff <= 0 {
		return 0, gorm.ErrInvalidData
	}
	batchSize = NormalizeAssistantRetentionBatchSize(batchSize)
	var keyHashes []string
	if err := DB.WithContext(ctx).Model(&AssistantGiftRiskMemory{}).
		Where("kind = ? AND updated_at < ?", assistantGiftRiskNetwork, cutoff).
		Order("updated_at ASC, key_hash ASC").Limit(batchSize).Pluck("key_hash", &keyHashes).Error; err != nil {
		return 0, err
	}
	if len(keyHashes) == 0 {
		return 0, nil
	}
	deleted := DB.WithContext(ctx).Where(
		"key_hash IN ? AND kind = ? AND updated_at < ?",
		keyHashes, assistantGiftRiskNetwork, cutoff,
	).Delete(&AssistantGiftRiskMemory{})
	return deleted.RowsAffected, deleted.Error
}

// PurgeAssistantRequestReviewsBefore removes old sampled review rows in
// bounded batches. Review rows contain redacted previews and verdicts, but
// they still grow with sampled traffic and must follow the security retention
// boundary rather than remain indefinitely.
func PurgeAssistantRequestReviewsBefore(ctx context.Context, cutoff int64, batchSize int) (int64, error) {
	if cutoff <= 0 {
		return 0, gorm.ErrInvalidData
	}
	if !assistantReviewTablesAvailable(DB) {
		return 0, nil
	}
	batchSize = NormalizeAssistantRetentionBatchSize(batchSize)
	var ids []int64
	if err := DB.WithContext(ctx).Model(&AssistantRequestReview{}).
		Where("created_at < ?", cutoff).
		Order("created_at ASC, id ASC").Limit(batchSize).Pluck("id", &ids).Error; err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	deleted := DB.WithContext(ctx).
		Where("id IN ? AND created_at < ?", ids, cutoff).
		Delete(&AssistantRequestReview{})
	return deleted.RowsAffected, deleted.Error
}

// PurgeAssistantIntentLeadsBefore removes old aggregate chat-intent rows in a
// bounded batch. Chat rows contain no transcript, but they still retain a user
// id and one row per uncached turn; retaining them forever would let routine
// assistant traffic grow the table without limit. Explicit support handoffs
// are excluded because they are an operator queue/history, not analytics.
func PurgeAssistantIntentLeadsBefore(ctx context.Context, cutoff int64, batchSize int) (int64, error) {
	if cutoff <= 0 {
		return 0, gorm.ErrInvalidData
	}
	batchSize = NormalizeAssistantRetentionBatchSize(batchSize)
	var ids []int
	if err := DB.WithContext(ctx).Model(&AssistantLead{}).
		Where("source = ? AND created_at < ?", AssistantLeadSourceChat, cutoff).
		Order("created_at ASC, id ASC").Limit(batchSize).Pluck("id", &ids).Error; err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	deleted := DB.WithContext(ctx).
		Where("id IN ? AND source = ? AND created_at < ?", ids, AssistantLeadSourceChat, cutoff).
		Delete(&AssistantLead{})
	return deleted.RowsAffected, deleted.Error
}

// PurgeAssistantUserProfileAuditsBefore removes old automatic profile-change
// audit rows in bounded batches. The audit stores only hashes and counts, but
// it is still one row per profile transition; source=administrator is kept as
// a durable operator audit and is not part of this assistant retention pass.
func PurgeAssistantUserProfileAuditsBefore(ctx context.Context, cutoff int64, batchSize int) (int64, error) {
	if cutoff <= 0 {
		return 0, gorm.ErrInvalidData
	}
	batchSize = NormalizeAssistantRetentionBatchSize(batchSize)
	var ids []int64
	if err := DB.WithContext(ctx).Model(&AssistantUserProfileAudit{}).
		Where("source = ? AND created_at < ?", AssistantProfileSourceAI, cutoff).
		Order("created_at ASC, id ASC").Limit(batchSize).Pluck("id", &ids).Error; err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	deleted := DB.WithContext(ctx).
		Where("id IN ? AND source = ? AND created_at < ?", ids, AssistantProfileSourceAI, cutoff).
		Delete(&AssistantUserProfileAudit{})
	return deleted.RowsAffected, deleted.Error
}

// PurgeAssistantProfileBucketsBefore removes old aggregate profile buckets in
// bounded batches. The bucket key is hourly, but time alone does not cap the
// number of rows; without this boundary even the fixed profile vocabulary
// grows forever.
func PurgeAssistantProfileBucketsBefore(ctx context.Context, cutoff int64, batchSize int) (int64, error) {
	if cutoff <= 0 {
		return 0, gorm.ErrInvalidData
	}
	batchSize = NormalizeAssistantRetentionBatchSize(batchSize)
	var ids []int
	if err := DB.WithContext(ctx).Model(&AssistantProfileBucket{}).
		Where("bucket_start < ?", cutoff).
		Order("bucket_start ASC, id ASC").Limit(batchSize).Pluck("id", &ids).Error; err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	deleted := DB.WithContext(ctx).
		Where("id IN ? AND bucket_start < ?", ids, cutoff).
		Delete(&AssistantProfileBucket{})
	return deleted.RowsAffected, deleted.Error
}

// PurgeAssistantFirstQuestionsBefore removes old first-question aggregate
// buckets in bounded batches. Questions are redacted and aggregate-only, but
// a new question hash can still create one row per hour indefinitely.
func PurgeAssistantFirstQuestionsBefore(ctx context.Context, cutoff int64, batchSize int) (int64, error) {
	if cutoff <= 0 {
		return 0, gorm.ErrInvalidData
	}
	batchSize = NormalizeAssistantRetentionBatchSize(batchSize)
	var ids []int
	if err := DB.WithContext(ctx).Model(&AssistantFirstQuestionStat{}).
		Where("bucket_start < ?", cutoff).
		Order("bucket_start ASC, id ASC").Limit(batchSize).Pluck("id", &ids).Error; err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	deleted := DB.WithContext(ctx).
		Where("id IN ? AND bucket_start < ?", ids, cutoff).
		Delete(&AssistantFirstQuestionStat{})
	return deleted.RowsAffected, deleted.Error
}
