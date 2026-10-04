package model

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

const (
	ModerationSourceRelayInput      = "relay_input"
	ModerationSourceAssistantInput  = "assistant_input"
	ModerationSourceAssistantOutput = "assistant_output"
	ModerationJobPending            = "pending"
	ModerationJobRunning            = "running"
	ModerationJobCompleted          = "completed"
	ModerationJobFailed             = "failed"
	ModerationJobCancelled          = "cancelled"
	ModerationMaxPayloadBytes       = 256 << 10
	ModerationPayloadMax            = ModerationMaxPayloadBytes
	ModerationJobMaxAttempts        = 3
	ModerationPendingMax            = 4096
)

var (
	ErrModerationJobInvalid = errors.New("moderation job is invalid")
	ErrModerationLeaseLost  = errors.New("moderation job lease lost")
	ErrModerationQueueFull  = errors.New("moderation queue is full")
)

// ModerationJob is a leased, durable background task. Only the bounded,
// redacted current turn is retained while work is pending. Every terminal
// transition erases Payload; API projections must never expose it.
type ModerationJob struct {
	ID                        int64  `json:"id" gorm:"primaryKey"`
	EventKey                  string `json:"-" gorm:"type:char(64);not null;uniqueIndex"`
	UserID                    int    `json:"user_id" gorm:"not null;index:idx_moderation_user_created,priority:1"`
	Source                    string `json:"source" gorm:"type:varchar(24);not null;index"`
	RequestID                 string `json:"request_id" gorm:"type:varchar(128);not null;index"`
	Group                     string `json:"group" gorm:"type:varchar(64);not null;index"`
	ReviewGroup               string `json:"review_group" gorm:"type:varchar(64);not null"`
	ReviewModel               string `json:"review_model" gorm:"type:varchar(128);not null"`
	InputDigest               string `json:"input_digest" gorm:"type:char(64);not null"`
	InputTruncated            bool   `json:"input_truncated" gorm:"not null;default:false"`
	Payload                   string `json:"-" gorm:"size:262144;not null"`
	CapturedMode              string `json:"mode" gorm:"type:varchar(16);not null"`
	CapturedCategoryFinesJSON string `json:"-" gorm:"type:text;not null"`
	Status                    string `json:"status" gorm:"type:varchar(16);not null;index:idx_moderation_queue,priority:1"`
	Attempts                  int    `json:"attempts" gorm:"not null;default:0"`
	NextAttemptAt             int64  `json:"-" gorm:"not null;index:idx_moderation_queue,priority:2"`
	LeaseOwner                string `json:"-" gorm:"type:varchar(128);not null"`
	LeaseUntil                int64  `json:"-" gorm:"not null;index"`
	Flagged                   bool   `json:"flagged" gorm:"not null;default:false;index"`
	CategoriesJSON            string `json:"-" gorm:"type:text;not null"`
	CategoryScoresJSON        string `json:"-" gorm:"type:text;not null"`
	ResponseModel             string `json:"response_model" gorm:"type:varchar(128);not null"`
	ReviewID                  int64  `json:"review_id" gorm:"not null;default:0"`
	FeeRecordID               uint   `json:"fee_record_id" gorm:"not null;default:0"`
	FeeCategory               string `json:"fee_category" gorm:"type:varchar(64);not null"`
	RequestedQuota            int    `json:"requested_quota" gorm:"type:bigint;not null;default:0"`
	ChargedQuota              int    `json:"charged_quota" gorm:"type:bigint;not null;default:0"`
	FeeStatus                 string `json:"fee_status" gorm:"type:varchar(24);not null"`
	ErrorMessage              string `json:"error,omitempty" gorm:"type:varchar(256);not null"`
	CreatedAt                 int64  `json:"created_at" gorm:"not null;index:idx_moderation_user_created,priority:2"`
	UpdatedAt                 int64  `json:"updated_at" gorm:"not null"`
	CompletedAt               int64  `json:"completed_at" gorm:"not null;default:0"`
}

func (ModerationJob) TableName() string { return "moderation_jobs" }

func boundedModerationPayload(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > ModerationPayloadMax {
		value = value[:ModerationPayloadMax]
		for len(value) > 0 && !utf8.ValidString(value) {
			value = value[:len(value)-1]
		}
	}
	return value
}

func moderationSourceValid(source string) bool {
	return source == ModerationSourceRelayInput || source == ModerationSourceAssistantInput || source == ModerationSourceAssistantOutput
}

// Even a bounded, redacted message remains private user content. Gorm's
// default error/slow-query trace interpolates INSERT values, so every queue
// operation uses a separate silent session. Workers report stable error codes
// outside this layer instead of exposing SQL parameters.
func moderationDB(ctx context.Context) *gorm.DB {
	return DB.WithContext(ctx).Session(&gorm.Session{Logger: logger.Discard})
}

func EnqueueModerationJob(ctx context.Context, job *ModerationJob) (bool, error) {
	if DB == nil || job == nil || job.UserID <= 0 || !moderationSourceValid(job.Source) {
		return false, ErrModerationJobInvalid
	}
	if job.CapturedMode == setting.ModerationModeOff {
		return false, nil
	}
	if job.CapturedMode != setting.ModerationModeTolerant && job.CapturedMode != setting.ModerationModeStrict {
		return false, ErrModerationJobInvalid
	}
	job.RequestID = strings.TrimSpace(job.RequestID)
	job.Group = strings.TrimSpace(job.Group)
	job.ReviewGroup = strings.TrimSpace(job.ReviewGroup)
	job.ReviewModel = strings.TrimSpace(job.ReviewModel)
	if job.RequestID == "" || len(job.RequestID) > 128 || job.Group == "" || len(job.Group) > 64 || len(job.ReviewGroup) > 64 || job.ReviewModel == "" || len(job.ReviewModel) > 128 {
		return false, ErrModerationJobInvalid
	}
	fullPayload := RedactModerationContent(job.Payload)
	job.InputTruncated = job.InputTruncated || len(fullPayload) > ModerationPayloadMax
	job.Payload = boundedModerationPayload(fullPayload)
	if job.Payload == "" {
		return false, ErrModerationJobInvalid
	}
	var fines map[string]float64
	if job.CapturedCategoryFinesJSON == "" {
		job.CapturedCategoryFinesJSON = "{}"
	}
	if json.Unmarshal([]byte(job.CapturedCategoryFinesJSON), &fines) != nil {
		return false, ErrModerationJobInvalid
	}
	for category, amount := range fines {
		if !setting.IsModerationCategory(category) || setting.ValidateModerationFineUSD(amount) != nil {
			return false, ErrModerationJobInvalid
		}
	}
	digest := sha256.Sum256([]byte(fullPayload))
	if job.InputDigest == "" {
		job.InputDigest = hex.EncodeToString(digest[:])
	} else if decoded, err := hex.DecodeString(job.InputDigest); err != nil || len(decoded) != sha256.Size {
		return false, ErrModerationJobInvalid
	}
	key, _ := json.Marshal([]any{job.UserID, job.RequestID, job.Source, job.InputDigest})
	digest = sha256.Sum256(key)
	job.EventKey = hex.EncodeToString(digest[:])
	if job.CreatedAt <= 0 {
		job.CreatedAt = common.GetTimestamp()
	}
	job.ID, job.Attempts, job.LeaseUntil, job.CompletedAt = 0, 0, 0, 0
	job.Status, job.LeaseOwner, job.ErrorMessage = ModerationJobPending, "", ""
	job.NextAttemptAt, job.UpdatedAt = job.CreatedAt, job.CreatedAt
	job.Flagged, job.ReviewID, job.FeeRecordID = false, 0, 0
	job.CategoriesJSON, job.CategoryScoresJSON = "[]", "{}"
	job.RequestedQuota, job.ChargedQuota = 0, 0
	job.ResponseModel, job.FeeCategory, job.FeeStatus = "", "", "none"
	if ctx == nil {
		ctx = context.Background()
	}
	var created bool
	err := moderationDB(ctx).Transaction(func(tx *gorm.DB) error {
		var owner User
		if err := lockForUpdate(tx).Select([]string{"id", "group", "status"}).Where("id = ?", job.UserID).First(&owner).Error; err != nil {
			return err
		}
		if owner.Group != job.Group || owner.Status != common.UserStatusEnabled {
			return ErrModerationJobInvalid
		}
		var existing ModerationJob
		if err := tx.Where("event_key = ?", job.EventKey).First(&existing).Error; err == nil {
			*job = existing
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var pending int64
		if err := tx.Model(&ModerationJob{}).Where("status IN ?", []string{ModerationJobPending, ModerationJobRunning}).Count(&pending).Error; err != nil {
			return err
		}
		if pending >= ModerationPendingMax {
			return ErrModerationQueueFull
		}
		result := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "event_key"}}, DoNothing: true}).Create(job)
		created = result.RowsAffected == 1
		return result.Error
	})
	return created, err
}

func ClaimModerationJob(ctx context.Context, owner string, now, leaseSecs int64) (*ModerationJob, error) {
	if DB == nil || strings.TrimSpace(owner) == "" || len(owner) > 128 || leaseSecs <= 0 || leaseSecs > 300 {
		return nil, ErrModerationJobInvalid
	}
	if now <= 0 {
		now = common.GetTimestamp()
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var claimed *ModerationJob
	err := moderationDB(ctx).Transaction(func(tx *gorm.DB) error {
		// The final crashed attempt must not leave its payload or a running row
		// forever. A new owner can retry only while the bounded budget remains.
		if err := tx.Model(&ModerationJob{}).Where("status = ? AND lease_until <= ? AND attempts >= ?", ModerationJobRunning, now, ModerationJobMaxAttempts).Updates(map[string]any{"status": ModerationJobFailed, "payload": "", "lease_owner": "", "lease_until": 0, "error_message": "review worker lease expired", "updated_at": now, "completed_at": now}).Error; err != nil {
			return err
		}
		eligible := "attempts < ? AND ((status = ? AND next_attempt_at <= ?) OR (status = ? AND lease_until <= ?))"
		var jobs []ModerationJob
		if err := tx.Where(eligible, ModerationJobMaxAttempts, ModerationJobPending, now, ModerationJobRunning, now).Order("next_attempt_at ASC, id ASC").Limit(1).Find(&jobs).Error; err != nil {
			return err
		}
		if len(jobs) == 0 {
			return nil
		}
		job := jobs[0]
		result := tx.Model(&ModerationJob{}).Where("id = ?", job.ID).Where(eligible, ModerationJobMaxAttempts, ModerationJobPending, now, ModerationJobRunning, now).Updates(map[string]any{"status": ModerationJobRunning, "lease_owner": owner, "lease_until": now + leaseSecs, "attempts": gorm.Expr("attempts + 1"), "updated_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}
		job.Status, job.LeaseOwner, job.LeaseUntil, job.UpdatedAt = ModerationJobRunning, owner, now+leaseSecs, now
		job.Attempts++
		claimed = &job
		return nil
	})
	return claimed, err
}

func RetryModerationJob(ctx context.Context, id int64, owner string, now, next int64, statusError string) error {
	if DB == nil || id <= 0 || owner == "" {
		return ErrModerationJobInvalid
	}
	if now <= 0 {
		now = common.GetTimestamp()
	}
	if next < now {
		next = now
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return moderationDB(ctx).Transaction(func(tx *gorm.DB) error {
		var job ModerationJob
		if err := lockForUpdate(tx).Where("id = ? AND status = ? AND lease_owner = ? AND lease_until > ?", id, ModerationJobRunning, owner, now).First(&job).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrModerationLeaseLost
			}
			return err
		}
		values := map[string]any{"status": ModerationJobPending, "next_attempt_at": next, "lease_owner": "", "lease_until": 0, "updated_at": now, "error_message": boundedAssistantReviewText(RedactAssistantHistoryContent(statusError), 256)}
		if job.Attempts >= ModerationJobMaxAttempts {
			values["status"], values["payload"], values["completed_at"] = ModerationJobFailed, "", now
		}
		return tx.Model(&job).Updates(values).Error
	})
}

func CancelModerationJob(ctx context.Context, id int64, owner, reason string) error {
	if DB == nil || id <= 0 || owner == "" {
		return ErrModerationJobInvalid
	}
	if ctx == nil {
		ctx = context.Background()
	}
	now := common.GetTimestamp()
	result := moderationDB(ctx).Model(&ModerationJob{}).Where("id = ? AND status = ? AND lease_owner = ? AND lease_until > ?", id, ModerationJobRunning, owner, now).Updates(map[string]any{"status": ModerationJobCancelled, "payload": "", "lease_owner": "", "lease_until": 0, "updated_at": now, "completed_at": now, "error_message": boundedAssistantReviewText(RedactAssistantHistoryContent(reason), 256)})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrModerationLeaseLost
	}
	return nil
}

type ModerationCompletion struct {
	Flagged          bool
	Categories       []string
	Scores           map[string]float64
	ResponseModel    string
	CurrentMode      string
	CategoryFinesUSD map[string]float64
	Now              int64
}

func (job ModerationJob) Categories() []string {
	var categories []string
	if json.Unmarshal([]byte(job.CategoriesJSON), &categories) != nil {
		return []string{}
	}
	return categories
}

func CompleteModerationJob(ctx context.Context, id int64, owner string, completion ModerationCompletion) error {
	if DB == nil || id <= 0 || owner == "" {
		return ErrModerationJobInvalid
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if completion.Now <= 0 {
		completion.Now = common.GetTimestamp()
	}
	if completion.CurrentMode != setting.ModerationModeOff && completion.CurrentMode != setting.ModerationModeTolerant && completion.CurrentMode != setting.ModerationModeStrict {
		return ErrModerationJobInvalid
	}
	for category, amount := range completion.CategoryFinesUSD {
		if !setting.IsModerationCategory(category) || setting.ValidateModerationFineUSD(amount) != nil {
			return ErrModerationJobInvalid
		}
	}
	if len(completion.Categories) > 32 || len(completion.Scores) > 32 {
		return ErrModerationJobInvalid
	}
	categories := make([]string, 0, len(completion.Categories))
	seen := make(map[string]bool, len(completion.Categories))
	for _, category := range completion.Categories {
		category = strings.TrimSpace(category)
		if !setting.IsModerationCategory(category) {
			return ErrModerationJobInvalid
		}
		if !seen[category] {
			categories = append(categories, category)
			seen[category] = true
		}
	}
	sort.Strings(categories)
	if completion.Flagged && len(categories) == 0 {
		return ErrModerationJobInvalid
	}
	if !completion.Flagged {
		categories = []string{}
	}
	for category, score := range completion.Scores {
		if !setting.IsModerationCategory(category) || score < 0 || score > 1 || math.IsNaN(score) || math.IsInf(score, 0) {
			return ErrModerationJobInvalid
		}
	}
	encodedCategories, _ := json.Marshal(categories)
	encodedScores, err := json.Marshal(completion.Scores)
	if err != nil {
		return ErrModerationJobInvalid
	}
	var cacheUser int
	err = moderationDB(ctx).Transaction(func(tx *gorm.DB) error {
		// This row lock is shared with all seven option writers and must be
		// acquired before task/user locks. It fences policy changes across nodes.
		settings, err := LockModerationSettings(tx)
		if err != nil {
			return err
		}
		var identity ModerationJob
		if err := tx.Select("id, user_id, status").Where("id = ?", id).First(&identity).Error; err != nil {
			return err
		}
		if identity.Status == ModerationJobCompleted {
			return nil
		}
		// Account deletion owns the user lock before erasing private tasks.
		// Follow the same order so a worker cannot deadlock that transaction.
		var user User
		if err := lockForUpdate(tx).Select([]string{"id", "group", "status", "quota"}).Where("id = ?", identity.UserID).First(&user).Error; err != nil {
			return err
		}
		var job ModerationJob
		if err := lockForUpdate(tx).Where("id = ?", id).First(&job).Error; err != nil {
			return err
		}
		if job.Status == ModerationJobCompleted {
			return nil
		}
		// Policy/account locks can block past the time supplied by the worker.
		// Check real time after acquiring the task lock, so an expired lease
		// cannot commit simply because completion started earlier.
		leaseCheckTime := common.GetTimestamp()
		if completion.Now > leaseCheckTime {
			leaseCheckTime = completion.Now
		}
		if job.Status != ModerationJobRunning || job.LeaseOwner != owner || job.LeaseUntil <= leaseCheckTime {
			return ErrModerationLeaseLost
		}
		enabled := settings.Enabled
		if job.Source == ModerationSourceAssistantInput || job.Source == ModerationSourceAssistantOutput {
			enabled = settings.AssistantEnabled
		}
		policy, configured := setting.ResolveModerationPolicy(settings, job.Group)
		if !enabled || !configured || policy.Mode == setting.ModerationModeOff || user.Group != job.Group || user.Status != common.UserStatusEnabled || completion.CurrentMode == setting.ModerationModeOff || job.InputTruncated {
			return tx.Model(&job).Updates(map[string]any{"status": ModerationJobCancelled, "payload": "", "lease_owner": "", "lease_until": 0, "completed_at": completion.Now, "updated_at": completion.Now, "error_message": "review policy disabled or subject group changed"}).Error
		}
		strict := policy.Mode == setting.ModerationModeStrict && job.CapturedMode == setting.ModerationModeStrict && completion.CurrentMode == setting.ModerationModeStrict && job.Source != ModerationSourceAssistantOutput
		values := map[string]any{"status": ModerationJobCompleted, "flagged": completion.Flagged, "categories_json": string(encodedCategories), "category_scores_json": string(encodedScores), "response_model": boundedAssistantReviewText(completion.ResponseModel, 128), "payload": "", "lease_owner": "", "lease_until": 0, "updated_at": completion.Now, "completed_at": completion.Now, "error_message": "", "fee_status": "none"}
		var record *ViolationFeeRecord
		if completion.Flagged && strict {
			var captured map[string]float64
			if json.Unmarshal([]byte(job.CapturedCategoryFinesJSON), &captured) != nil {
				return ErrModerationJobInvalid
			}
			amount, category := 0.0, ""
			for _, match := range categories {
				candidate := math.Min(captured[match], math.Min(policy.CategoryFinesUSD[match], completion.CategoryFinesUSD[match]))
				if candidate > amount && candidate <= setting.ModerationMaxCategoryFineUSD && !math.IsInf(candidate, 0) && !math.IsNaN(candidate) {
					amount, category = candidate, match
				}
			}
			if amount > 0 {
				if common.QuotaPerUnit <= 0 || math.IsNaN(common.QuotaPerUnit) || math.IsInf(common.QuotaPerUnit, 0) {
					return ErrModerationJobInvalid
				}
				// A configured fine is a ceiling. Round down in decimal arithmetic
				// to wallet units; a sub-unit amount remains warning-only.
				requested, conversionErr := common.WalletQuotaFromDecimalStrict(decimal.NewFromFloat(amount).Mul(decimal.NewFromFloat(common.QuotaPerUnit)).Floor())
				if conversionErr != nil {
					return conversionErr
				}
				if requested > 0 {
					var existing ViolationFeeRecord
					lookup := tx.Where("user_id = ? AND request_id = ?", user.Id, job.RequestID).First(&existing).Error
					if lookup == nil {
						values["fee_record_id"], values["fee_status"] = existing.ID, "already_processed"
					} else if !errors.Is(lookup, gorm.ErrRecordNotFound) {
						return lookup
					} else {
						charged := requested
						if user.Quota <= 0 {
							charged = 0
						} else if charged > user.Quota {
							charged = user.Quota
						}
						if charged > 0 {
							debit := UpdateWalletQuotaByDelta(tx.Model(&User{}).Where("id = ? AND quota >= ?", user.Id, charged), -charged)
							if debit.Error != nil {
								return debit.Error
							}
							if debit.RowsAffected != 1 {
								return ErrWalletQuotaOutOfRange
							}
							cacheUser = user.Id
						}
						chargedAmount := float64(charged) / common.QuotaPerUnit
						fresh := ViolationFeeRecord{UserID: user.Id, RequestID: job.RequestID, PolicyKey: "moderation:" + job.Group, Group: job.Group, Occurrence: 1, PeriodStartedAt: completion.Now, PeriodEndsAt: completion.Now, RequestedAmountUSD: amount, ChargedAmountUSD: chargedAmount, RequestedQuota: requested, ChargedQuota: charged, ErrorCode: "moderation." + category, Status: ViolationFeeRecordStatusCharged, CreatedAt: completion.Now}
						if err := tx.Create(&fresh).Error; err != nil {
							return err
						}
						record = &fresh
						values["requested_quota"], values["charged_quota"], values["fee_category"], values["fee_record_id"] = requested, charged, category, fresh.ID
						feeStatus := "charged"
						if charged == 0 {
							feeStatus = "insufficient_balance"
						} else if charged < requested {
							feeStatus = "partial"
						}
						values["fee_status"] = feeStatus
					}
				}
			}
		}
		// Keep existing administrator/user violation-history APIs usable. One
		// committed user request produces one history row, independent of retry
		// count or overlapping relay/assistant-input captures.
		if job.Source != ModerationSourceAssistantOutput {
			var existing AssistantRequestReview
			lookup := tx.Where("user_id = ? AND request_id = ?", job.UserID, job.RequestID).First(&existing).Error
			if lookup == nil {
				values["review_id"] = existing.ID
			} else if !errors.Is(lookup, gorm.ErrRecordNotFound) {
				return lookup
			} else {
				review := AssistantRequestReview{UserID: job.UserID, RequestID: job.RequestID, Group: job.Group, ReviewModel: job.ReviewModel, Intensity: job.CapturedMode, Status: AssistantRequestReviewStatusCompleted, Violation: completion.Flagged, RulesJSON: string(encodedCategories), CreatedAt: job.CreatedAt, UpdatedAt: completion.Now}
				if completion.Flagged {
					review.Explanation = "OpenAI Moderation flagged: " + strings.Join(categories, ", ")
				}
				if err := tx.Create(&review).Error; err != nil {
					return err
				}
				values["review_id"] = review.ID
			}
		}
		if completion.Flagged {
			mode := setting.ModerationModeTolerant
			if strict {
				mode = setting.ModerationModeStrict
			}
			notice := ModerationNotice{UserID: job.UserID, JobID: job.ID, RequestID: job.RequestID, Source: job.Source, Mode: mode, CategoriesJSON: string(encodedCategories), CreatedAt: completion.Now, UpdatedAt: completion.Now}
			if record != nil {
				notice.FeeRecordID, notice.RequestedQuota, notice.ChargedQuota = record.ID, record.RequestedQuota, record.ChargedQuota
			}
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "user_id"}, {Name: "request_id"}, {Name: "source"}}, DoNothing: true}).Create(&notice).Error; err != nil {
				return err
			}
		}
		return tx.Model(&job).Updates(values).Error
	})
	if err == nil && cacheUser > 0 {
		if cacheErr := InvalidateUserCache(cacheUser); cacheErr != nil {
			common.SysLog("failed to invalidate moderation wallet cache")
		}
	}
	return err
}

type ModerationQueueStats struct {
	Pending      int64 `json:"pending"`
	Running      int64 `json:"running"`
	Completed    int64 `json:"completed"`
	Failed       int64 `json:"failed"`
	Cancelled    int64 `json:"cancelled"`
	Flagged      int64 `json:"flagged"`
	Fined        int64 `json:"fined"`
	ChargedQuota int64 `json:"charged_quota"`
}

func ModerationStats(ctx context.Context) (ModerationQueueStats, error) {
	var stats ModerationQueueStats
	if DB == nil {
		return stats, ErrModerationJobInvalid
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if !DB.Migrator().HasTable(&ModerationJob{}) {
		return stats, nil
	}
	err := moderationDB(ctx).Model(&ModerationJob{}).Select(`COALESCE(SUM(CASE WHEN status = 'pending' THEN 1 ELSE 0 END),0) AS pending, COALESCE(SUM(CASE WHEN status = 'running' THEN 1 ELSE 0 END),0) AS running, COALESCE(SUM(CASE WHEN status = 'completed' THEN 1 ELSE 0 END),0) AS completed, COALESCE(SUM(CASE WHEN status = 'failed' THEN 1 ELSE 0 END),0) AS failed, COALESCE(SUM(CASE WHEN status = 'cancelled' THEN 1 ELSE 0 END),0) AS cancelled, COALESCE(SUM(CASE WHEN status = 'completed' AND flagged THEN 1 ELSE 0 END),0) AS flagged, COALESCE(SUM(CASE WHEN charged_quota > 0 THEN 1 ELSE 0 END),0) AS fined, COALESCE(SUM(charged_quota),0) AS charged_quota`).Scan(&stats).Error
	return stats, err
}

type ModerationJobFilter struct {
	UserID         int
	Group          string
	Status         string
	Source         string
	StartTimestamp int64
	EndTimestamp   int64
	Limit          int
	Offset         int
}

// ListModerationJobs loads only bounded administration metadata. The caller
// owns its role-based API projection; submitted text and captured policy never
// leave this model query, including for a root administrator.
func ListModerationJobs(ctx context.Context, filter ModerationJobFilter) ([]ModerationJob, int64, error) {
	if DB == nil {
		return nil, 0, ErrModerationJobInvalid
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if !DB.Migrator().HasTable(&ModerationJob{}) {
		return []ModerationJob{}, 0, nil
	}
	query := moderationDB(ctx).Model(&ModerationJob{})
	if filter.UserID > 0 {
		query = query.Where("user_id = ?", filter.UserID)
	}
	if filter.Group != "" {
		query = query.Where(clause.Eq{Column: clause.Column{Name: "group"}, Value: filter.Group})
	}
	if filter.Status != "" {
		query = query.Where("status = ?", filter.Status)
	}
	if filter.Source != "" {
		query = query.Where("source = ?", filter.Source)
	}
	if filter.StartTimestamp > 0 {
		query = query.Where("created_at >= ?", filter.StartTimestamp)
	}
	if filter.EndTimestamp > 0 {
		query = query.Where("created_at <= ?", filter.EndTimestamp)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	limit := filter.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}
	rows := make([]ModerationJob, 0, limit)
	if err := query.Omit("payload", "captured_category_fines_json", "lease_owner", "category_scores_json", "error_message").Order("created_at DESC, id DESC").Offset(offset).Limit(limit).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}
