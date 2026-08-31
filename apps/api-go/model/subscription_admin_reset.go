package model

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/schema"
)

const (
	SubscriptionResetModeHard = "hard"
	SubscriptionResetModeSoft = "soft"

	SubscriptionResetVoucherAvailable = "available"
	SubscriptionResetVoucherRedeemed  = "redeemed"
	SubscriptionResetVoucherRevoked   = "revoked"

	maxSubscriptionResetTargets       = 5000
	maxSubscriptionResetSubscriptions = 20_000

	subscriptionResetPreviewRetentionSeconds int64 = 7 * 24 * 60 * 60
)

var (
	ErrSubscriptionResetRequiresActiveSubscription = errors.New("subscription reset requires an active subscription")
	ErrSubscriptionResetVoucherUnavailable         = errors.New("subscription reset voucher is unavailable")
	ErrSubscriptionResetVoucherExpired             = errors.New("subscription reset voucher has expired")
	ErrSubscriptionResetPreviewStale               = errors.New("subscription reset preview is stale")
	ErrSubscriptionResetOperationConflict          = errors.New("subscription reset operation id is already bound to another preview")
)

type SubscriptionResetVoucher struct {
	Id          int    `json:"id"`
	UserId      int    `json:"user_id" gorm:"not null;index;uniqueIndex:idx_subscription_reset_voucher_operation"`
	PlanId      int    `json:"plan_id" gorm:"not null;index;uniqueIndex:idx_subscription_reset_voucher_operation"`
	OperationId string `json:"operation_id" gorm:"type:varchar(64);not null;uniqueIndex:idx_subscription_reset_voucher_operation"`
	Status      string `json:"status" gorm:"type:varchar(16);not null;default:'available';index"`
	ExpiresAt   int64  `json:"expires_at" gorm:"not null;index"`
	RedeemedAt  int64  `json:"redeemed_at" gorm:"not null;default:0"`
	CreatedBy   int    `json:"created_by" gorm:"not null;index"`
	CreatedAt   int64  `json:"created_at" gorm:"not null"`
	UpdatedAt   int64  `json:"updated_at" gorm:"not null"`
}

func (SubscriptionResetVoucher) TableName() string { return "subscription_reset_vouchers" }

type SubscriptionResetEvent struct {
	Id            int    `json:"id"`
	OperationId   string `json:"operation_id" gorm:"type:varchar(64);not null;uniqueIndex:idx_subscription_reset_event_operation"`
	UserId        int    `json:"user_id" gorm:"not null;index;uniqueIndex:idx_subscription_reset_event_operation"`
	PlanId        int    `json:"plan_id" gorm:"not null;index;uniqueIndex:idx_subscription_reset_event_operation"`
	Mode          string `json:"mode" gorm:"type:varchar(24);not null;uniqueIndex:idx_subscription_reset_event_operation"`
	ActorUserId   int    `json:"actor_user_id" gorm:"not null;index"`
	VoucherId     int    `json:"voucher_id" gorm:"not null;default:0"`
	ResetCount    int    `json:"reset_count" gorm:"not null;default:0"`
	RestoredQuota int64  `json:"restored_quota" gorm:"not null;default:0"`
	VoucherExpiry int64  `json:"voucher_expiry" gorm:"not null;default:0"`
	CreatedAt     int64  `json:"created_at" gorm:"not null;index"`
}

func (SubscriptionResetEvent) TableName() string { return "subscription_reset_events" }

type SubscriptionResetTargetsJSON string

func (SubscriptionResetTargetsJSON) GormDataType() string { return "text" }

func (SubscriptionResetTargetsJSON) GormDBDataType(db *gorm.DB, _ *schema.Field) string {
	if db != nil && db.Dialector != nil && db.Dialector.Name() == "mysql" {
		return "LONGTEXT"
	}
	return "TEXT"
}

type SubscriptionResetPreview struct {
	Token               string                       `json:"token" gorm:"type:varchar(64);primaryKey"`
	ActorUserId         int                          `json:"actor_user_id" gorm:"not null;index"`
	Mode                string                       `json:"mode" gorm:"type:varchar(16);not null"`
	TargetsJSON         SubscriptionResetTargetsJSON `json:"-" gorm:"not null"`
	PayloadHash         string                       `json:"payload_hash" gorm:"type:varchar(64);not null"`
	TargetCount         int                          `json:"target_count" gorm:"not null"`
	ActiveSubscriptions int                          `json:"active_subscriptions" gorm:"not null"`
	QuotaToRestore      int64                        `json:"quota_to_restore" gorm:"not null"`
	VoucherExpiresAt    int64                        `json:"voucher_expires_at" gorm:"not null;default:0"`
	ExpiresAt           int64                        `json:"expires_at" gorm:"not null;index"`
	ConsumedAt          int64                        `json:"consumed_at" gorm:"not null;default:0"`
	OperationId         string                       `json:"operation_id" gorm:"type:varchar(64);not null;default:''"`
	CreatedAt           int64                        `json:"created_at" gorm:"not null"`
}

func (SubscriptionResetPreview) TableName() string { return "subscription_reset_previews" }

type SubscriptionResetOperation struct {
	OperationId  string `json:"operation_id" gorm:"type:varchar(64);primaryKey"`
	PreviewToken string `json:"preview_token" gorm:"type:varchar(64);not null;uniqueIndex"`
	ActorUserId  int    `json:"actor_user_id" gorm:"not null;index"`
	Mode         string `json:"mode" gorm:"type:varchar(16);not null"`
	PayloadHash  string `json:"payload_hash" gorm:"type:varchar(64);not null"`
	ResultJSON   string `json:"-" gorm:"type:text;not null"`
	CreatedAt    int64  `json:"created_at" gorm:"not null"`
	CompletedAt  int64  `json:"completed_at" gorm:"not null;index"`
}

func (SubscriptionResetOperation) TableName() string { return "subscription_reset_operations" }

type SubscriptionResetPreviewSubscription struct {
	Id         int    `json:"id"`
	UserId     int    `json:"user_id"`
	PlanId     int    `json:"plan_id"`
	AmountUsed int64  `json:"amount_used"`
	Status     string `json:"status"`
	EndTime    int64  `json:"end_time"`
	UpdatedAt  int64  `json:"updated_at"`
}

type SubscriptionResetPreviewTarget struct {
	UserId        int                                    `json:"user_id"`
	PlanId        int                                    `json:"plan_id"`
	Subscriptions []SubscriptionResetPreviewSubscription `json:"subscriptions"`
}

type AdminSubscriptionRecordFilter struct {
	Query    string
	PlanId   int
	Status   string
	Page     int
	PageSize int
}

type AdminSubscriptionRecord struct {
	Id                  int    `json:"id"`
	UserId              int    `json:"user_id"`
	Username            string `json:"username"`
	Email               string `json:"email"`
	PlanId              int    `json:"plan_id"`
	PlanTitle           string `json:"plan_title"`
	PlanArchivedAt      int64  `json:"plan_archived_at"`
	AmountTotal         int64  `json:"amount_total"`
	AmountUsed          int64  `json:"amount_used"`
	StartTime           int64  `json:"start_time"`
	EndTime             int64  `json:"end_time"`
	Status              string `json:"status"`
	Source              string `json:"source"`
	LastResetTime       int64  `json:"last_reset_time"`
	NextResetTime       int64  `json:"next_reset_time"`
	AllowWalletOverflow bool   `json:"allow_wallet_overflow"`
	CreatedAt           int64  `json:"created_at"`
	UpdatedAt           int64  `json:"updated_at"`
}

type AdminSubscriptionRecordPage struct {
	Items    []AdminSubscriptionRecord `json:"items"`
	Total    int64                     `json:"total"`
	Page     int                       `json:"page"`
	PageSize int                       `json:"page_size"`
}

func normalizeSubscriptionAdminPage(page, pageSize int) (int, int) {
	if page < 1 {
		page = 1
	}
	if page > 1_000_000 {
		page = 1_000_000
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	return page, pageSize
}

func adminSubscriptionUserIDCastType(db *gorm.DB) string {
	if db != nil && db.Dialector != nil && db.Dialector.Name() == "mysql" {
		return "CHAR"
	}
	return "TEXT"
}

func applyAdminSubscriptionSearch(query *gorm.DB, value string) *gorm.DB {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return query
	}
	like := "%" + value + "%"
	predicate := fmt.Sprintf(
		"CAST(us.user_id AS %s) LIKE ? OR LOWER(users.username) LIKE ? OR LOWER(COALESCE(users.email, '')) LIKE ?",
		adminSubscriptionUserIDCastType(query),
	)
	return query.Where(predicate, like, like, like)
}

func ListAdminSubscriptionRecords(filter AdminSubscriptionRecordFilter) (*AdminSubscriptionRecordPage, error) {
	page, pageSize := normalizeSubscriptionAdminPage(filter.Page, filter.PageSize)
	query := DB.Table("user_subscriptions AS us").
		Joins("JOIN users ON users.id = us.user_id AND users.deleted_at IS NULL").
		Joins("JOIN subscription_plans AS plans ON plans.id = us.plan_id")
	query = applyAdminSubscriptionSearch(query, filter.Query)
	if filter.PlanId > 0 {
		query = query.Where("us.plan_id = ?", filter.PlanId)
	}
	status := strings.TrimSpace(filter.Status)
	if status != "" && status != "all" {
		query = query.Where("us.status = ?", status)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, err
	}
	items := make([]AdminSubscriptionRecord, 0)
	err := query.Select(`us.id, us.user_id, users.username, COALESCE(users.email, '') AS email,
		us.plan_id, plans.title AS plan_title, plans.archived_at AS plan_archived_at,
		us.amount_total, us.amount_used, us.start_time, us.end_time, us.status, us.source,
		us.last_reset_time, us.next_reset_time, us.allow_wallet_overflow, us.created_at, us.updated_at`).
		Order("us.id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Scan(&items).Error
	if err != nil {
		return nil, err
	}
	return &AdminSubscriptionRecordPage{Items: items, Total: total, Page: page, PageSize: pageSize}, nil
}

type AdminSubscriptionResetEligibleFilter struct {
	Query    string `json:"query"`
	PlanId   int    `json:"plan_id"`
	PlanIds  []int  `json:"plan_ids"`
	UserIds  []int  `json:"user_ids"`
	Page     int    `json:"-"`
	PageSize int    `json:"-"`
}

type AdminSubscriptionResetEligible struct {
	UserId                  int    `json:"user_id"`
	Username                string `json:"username"`
	Email                   string `json:"email"`
	PlanId                  int    `json:"plan_id"`
	PlanTitle               string `json:"plan_title"`
	PlanArchivedAt          int64  `json:"plan_archived_at"`
	ActiveSubscriptionCount int64  `json:"active_subscription_count"`
	AmountTotal             int64  `json:"amount_total"`
	AmountUsed              int64  `json:"amount_used"`
	NextResetTime           int64  `json:"next_reset_time"`
	BankedVoucherCount      int64  `json:"banked_voucher_count"`
}

type AdminSubscriptionResetEligiblePage struct {
	Items    []AdminSubscriptionResetEligible `json:"items"`
	Total    int64                            `json:"total"`
	Page     int                              `json:"page"`
	PageSize int                              `json:"page_size"`
}

func adminSubscriptionResetEligibleQuery(filter AdminSubscriptionResetEligibleFilter, now int64) *gorm.DB {
	query := DB.Table("user_subscriptions AS us").
		Joins("JOIN users ON users.id = us.user_id AND users.deleted_at IS NULL").
		Joins("JOIN subscription_plans AS plans ON plans.id = us.plan_id").
		Where("us.status = ? AND us.end_time > ?", "active", now)
	query = applyAdminSubscriptionSearch(query, filter.Query)
	if len(filter.PlanIds) > 0 {
		query = query.Where("us.plan_id IN ?", filter.PlanIds)
	} else if filter.PlanId > 0 {
		query = query.Where("us.plan_id = ?", filter.PlanId)
	}
	if len(filter.UserIds) > 0 {
		query = query.Where("us.user_id IN ?", filter.UserIds)
	}
	return query
}

func ListAdminSubscriptionResetEligible(filter AdminSubscriptionResetEligibleFilter) (*AdminSubscriptionResetEligiblePage, error) {
	page, pageSize := normalizeSubscriptionAdminPage(filter.Page, filter.PageSize)
	now := GetDBTimestamp()
	grouped := adminSubscriptionResetEligibleQuery(filter, now).Select("us.user_id, us.plan_id").Group("us.user_id, us.plan_id")
	var total int64
	if err := DB.Table("(?) AS eligible", grouped).Count(&total).Error; err != nil {
		return nil, err
	}
	items := make([]AdminSubscriptionResetEligible, 0)
	err := adminSubscriptionResetEligibleQuery(filter, now).
		Select(`us.user_id, users.username, COALESCE(users.email, '') AS email,
			us.plan_id, plans.title AS plan_title, plans.archived_at AS plan_archived_at,
			COUNT(*) AS active_subscription_count, SUM(us.amount_total) AS amount_total,
			SUM(us.amount_used) AS amount_used,
			MIN(CASE WHEN us.next_reset_time > 0 THEN us.next_reset_time ELSE NULL END) AS next_reset_time`).
		Group("us.user_id, users.username, users.email, us.plan_id, plans.title, plans.archived_at").
		Order("us.user_id DESC, us.plan_id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Scan(&items).Error
	if err != nil {
		return nil, err
	}
	if len(items) > 0 {
		targets := make([]SubscriptionResetTarget, 0, len(items))
		for _, item := range items {
			targets = append(targets, SubscriptionResetTarget{UserId: item.UserId, PlanId: item.PlanId})
		}
		counts, countErr := loadAvailableSubscriptionResetVoucherCounts(targets, now)
		if countErr != nil {
			return nil, countErr
		}
		for index := range items {
			items[index].BankedVoucherCount = counts[fmt.Sprintf("%d:%d", items[index].UserId, items[index].PlanId)]
		}
	}
	return &AdminSubscriptionResetEligiblePage{Items: items, Total: total, Page: page, PageSize: pageSize}, nil
}

type SubscriptionResetTarget struct {
	UserId int `json:"user_id"`
	PlanId int `json:"plan_id"`
}

type AdminSubscriptionResetPreviewResult struct {
	Token               string                           `json:"token"`
	Mode                string                           `json:"mode"`
	TargetCount         int                              `json:"target_count"`
	UserCount           int                              `json:"user_count"`
	PlanCount           int                              `json:"plan_count"`
	ActiveSubscriptions int                              `json:"active_subscriptions"`
	QuotaToRestore      int64                            `json:"quota_to_restore"`
	VoucherExpiresAt    int64                            `json:"voucher_expires_at,omitempty"`
	ExpiresAt           int64                            `json:"expires_at"`
	Targets             []AdminSubscriptionResetEligible `json:"targets"`
}

func normalizeSubscriptionResetMode(value string) (string, error) {
	mode := strings.ToLower(strings.TrimSpace(value))
	if mode != SubscriptionResetModeHard && mode != SubscriptionResetModeSoft {
		return "", errors.New("invalid subscription reset mode")
	}
	return mode, nil
}

func checkedSubscriptionResetAdd(current, value int64) (int64, error) {
	if (value > 0 && current > math.MaxInt64-value) || (value < 0 && current < math.MinInt64-value) {
		return 0, errors.New("subscription reset quota total exceeds the supported range")
	}
	return current + value, nil
}

func loadAvailableSubscriptionResetVoucherCounts(targets []SubscriptionResetTarget, now int64) (map[string]int64, error) {
	counts := make(map[string]int64, len(targets))
	type voucherCount struct {
		UserId int
		PlanId int
		Count  int64
	}
	const pairsPerQuery = 200
	for start := 0; start < len(targets); start += pairsPerQuery {
		end := start + pairsPerQuery
		if end > len(targets) {
			end = len(targets)
		}
		conditions := make([]string, 0, end-start)
		args := make([]any, 0, 2*(end-start))
		for _, target := range targets[start:end] {
			conditions = append(conditions, "(user_id = ? AND plan_id = ?)")
			args = append(args, target.UserId, target.PlanId)
		}
		rows := make([]voucherCount, 0)
		err := DB.Model(&SubscriptionResetVoucher{}).
			Select("user_id, plan_id, COUNT(*) AS count").
			Where("status = ? AND expires_at > ?", SubscriptionResetVoucherAvailable, now).
			Where("("+strings.Join(conditions, " OR ")+")", args...).
			Group("user_id, plan_id").Scan(&rows).Error
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			counts[fmt.Sprintf("%d:%d", row.UserId, row.PlanId)] = row.Count
		}
	}
	return counts, nil
}

func loadSubscriptionResetTargetSummaries(targets []SubscriptionResetTarget, now int64) ([]AdminSubscriptionResetEligible, []SubscriptionResetPreviewTarget, error) {
	if len(targets) == 0 {
		return []AdminSubscriptionResetEligible{}, []SubscriptionResetPreviewTarget{}, nil
	}
	selected := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		selected[fmt.Sprintf("%d:%d", target.UserId, target.PlanId)] = struct{}{}
	}
	type activeRow struct {
		Id             int
		UserId         int
		Username       string
		Email          string
		PlanId         int
		PlanTitle      string
		PlanArchivedAt int64
		AmountTotal    int64
		AmountUsed     int64
		NextResetTime  int64
		Status         string
		EndTime        int64
		UpdatedAt      int64
	}
	rows := make([]activeRow, 0)
	const pairsPerQuery = 200
	for start := 0; start < len(targets); start += pairsPerQuery {
		end := start + pairsPerQuery
		if end > len(targets) {
			end = len(targets)
		}
		conditions := make([]string, 0, end-start)
		args := make([]any, 0, 2*(end-start))
		for _, target := range targets[start:end] {
			conditions = append(conditions, "(us.user_id = ? AND us.plan_id = ?)")
			args = append(args, target.UserId, target.PlanId)
		}
		chunk := make([]activeRow, 0)
		err := DB.Table("user_subscriptions AS us").
			Select(`us.id, us.user_id, users.username, COALESCE(users.email, '') AS email,
				us.plan_id, plans.title AS plan_title, plans.archived_at AS plan_archived_at,
				us.amount_total, us.amount_used, us.next_reset_time, us.status, us.end_time, us.updated_at`).
			Joins("JOIN users ON users.id = us.user_id AND users.deleted_at IS NULL").
			Joins("JOIN subscription_plans AS plans ON plans.id = us.plan_id").
			Where("us.status = ? AND us.end_time > ?", "active", now).
			Where("("+strings.Join(conditions, " OR ")+")", args...).
			Order("us.user_id, us.plan_id, us.id").Scan(&chunk).Error
		if err != nil {
			return nil, nil, err
		}
		rows = append(rows, chunk...)
		if len(rows) > maxSubscriptionResetSubscriptions {
			return nil, nil, errors.New("subscription reset selection exceeds 20000 active subscriptions")
		}
	}
	aggregated := make(map[string]*AdminSubscriptionResetEligible, len(targets))
	frozen := make(map[string]*SubscriptionResetPreviewTarget, len(targets))
	for _, row := range rows {
		if row.AmountUsed < 0 {
			return nil, nil, errors.New("subscription reset encountered a negative used quota")
		}
		key := fmt.Sprintf("%d:%d", row.UserId, row.PlanId)
		if _, ok := selected[key]; !ok {
			continue
		}
		item := aggregated[key]
		if item == nil {
			item = &AdminSubscriptionResetEligible{
				UserId: row.UserId, Username: row.Username, Email: row.Email,
				PlanId: row.PlanId, PlanTitle: row.PlanTitle, PlanArchivedAt: row.PlanArchivedAt,
			}
			aggregated[key] = item
			frozen[key] = &SubscriptionResetPreviewTarget{UserId: row.UserId, PlanId: row.PlanId}
		}
		item.ActiveSubscriptionCount++
		var addErr error
		item.AmountTotal, addErr = checkedSubscriptionResetAdd(item.AmountTotal, row.AmountTotal)
		if addErr != nil {
			return nil, nil, addErr
		}
		item.AmountUsed, addErr = checkedSubscriptionResetAdd(item.AmountUsed, row.AmountUsed)
		if addErr != nil {
			return nil, nil, addErr
		}
		if row.NextResetTime > 0 && (item.NextResetTime == 0 || row.NextResetTime < item.NextResetTime) {
			item.NextResetTime = row.NextResetTime
		}
		frozen[key].Subscriptions = append(frozen[key].Subscriptions, SubscriptionResetPreviewSubscription{
			Id: row.Id, UserId: row.UserId, PlanId: row.PlanId, AmountUsed: row.AmountUsed,
			Status: row.Status, EndTime: row.EndTime, UpdatedAt: row.UpdatedAt,
		})
	}
	voucherCounts, err := loadAvailableSubscriptionResetVoucherCounts(targets, now)
	if err != nil {
		return nil, nil, err
	}
	for key, count := range voucherCounts {
		if item := aggregated[key]; item != nil {
			item.BankedVoucherCount = count
		}
	}

	summaries := make([]AdminSubscriptionResetEligible, 0, len(aggregated))
	frozenTargets := make([]SubscriptionResetPreviewTarget, 0, len(aggregated))
	for _, target := range targets {
		key := fmt.Sprintf("%d:%d", target.UserId, target.PlanId)
		if item := aggregated[key]; item != nil {
			summaries = append(summaries, *item)
			frozenTargets = append(frozenTargets, *frozen[key])
		}
	}
	return summaries, frozenTargets, nil
}

func addOneCalendarMonthUTC(timestamp int64) int64 {
	current := time.Unix(timestamp, 0).UTC()
	nextMonth := time.Date(current.Year(), current.Month()+1, 1, current.Hour(), current.Minute(), current.Second(), current.Nanosecond(), time.UTC)
	lastDay := time.Date(nextMonth.Year(), nextMonth.Month()+1, 0, current.Hour(), current.Minute(), current.Second(), current.Nanosecond(), time.UTC).Day()
	day := current.Day()
	if day > lastDay {
		day = lastDay
	}
	return time.Date(nextMonth.Year(), nextMonth.Month(), day, current.Hour(), current.Minute(), current.Second(), current.Nanosecond(), time.UTC).Unix()
}

func subscriptionResetPayloadHash(mode string, targets []SubscriptionResetPreviewTarget) (string, error) {
	payload, err := json.Marshal(struct {
		Mode    string                           `json:"mode"`
		Targets []SubscriptionResetPreviewTarget `json:"targets"`
	}{Mode: mode, Targets: targets})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return fmt.Sprintf("%x", digest[:]), nil
}

func AdminPreviewSubscriptionsReset(input AdminSubscriptionResetBatchInput) (*AdminSubscriptionResetPreviewResult, error) {
	if input.ActorUserId <= 0 {
		return nil, errors.New("invalid subscription reset actor")
	}
	mode, err := normalizeSubscriptionResetMode(input.Mode)
	if err != nil {
		return nil, err
	}
	targets, err := resolveSubscriptionResetTargets(input)
	if err != nil {
		return nil, err
	}
	now := GetDBTimestamp()
	summaries, frozenTargets, err := loadSubscriptionResetTargetSummaries(targets, now)
	if err != nil {
		return nil, err
	}
	if len(summaries) == 0 {
		return nil, errors.New("no active subscription users matched the reset request")
	}
	users := make(map[int]struct{})
	plans := make(map[int]struct{})
	result := &AdminSubscriptionResetPreviewResult{
		Token: uuid.NewString(), Mode: mode, Targets: summaries,
		TargetCount: len(summaries), ExpiresAt: now + 10*60,
	}
	for _, item := range summaries {
		users[item.UserId] = struct{}{}
		plans[item.PlanId] = struct{}{}
		result.ActiveSubscriptions += int(item.ActiveSubscriptionCount)
		result.QuotaToRestore, err = checkedSubscriptionResetAdd(result.QuotaToRestore, item.AmountUsed)
		if err != nil {
			return nil, err
		}
	}
	result.UserCount = len(users)
	result.PlanCount = len(plans)
	if mode == SubscriptionResetModeSoft {
		result.VoucherExpiresAt = addOneCalendarMonthUTC(now)
	}
	targetJSON, err := json.Marshal(frozenTargets)
	if err != nil {
		return nil, err
	}
	payloadHash, err := subscriptionResetPayloadHash(mode, frozenTargets)
	if err != nil {
		return nil, err
	}
	preview := SubscriptionResetPreview{
		Token: result.Token, ActorUserId: input.ActorUserId, Mode: mode,
		TargetsJSON: SubscriptionResetTargetsJSON(targetJSON), PayloadHash: payloadHash,
		TargetCount: result.TargetCount, ActiveSubscriptions: result.ActiveSubscriptions,
		QuotaToRestore: result.QuotaToRestore, VoucherExpiresAt: result.VoucherExpiresAt,
		ExpiresAt: result.ExpiresAt, CreatedAt: now,
	}
	if err := DB.Create(&preview).Error; err != nil {
		return nil, err
	}
	return result, nil
}

type SubscriptionResetAuditContext struct {
	Username   string
	IP         string
	Role       int
	AuthMethod string
}

type AdminSubscriptionResetBatchInput struct {
	ActorUserId  int
	OperationId  string
	PreviewToken string
	Mode         string
	Targets      []SubscriptionResetTarget
	AllMatching  bool
	Filter       AdminSubscriptionResetEligibleFilter
	Audit        SubscriptionResetAuditContext
}

type AdminSubscriptionResetBatchResult struct {
	OperationId        string `json:"operation_id"`
	Mode               string `json:"mode"`
	RequestedTargets   int    `json:"requested_targets"`
	ProcessedTargets   int    `json:"processed_targets"`
	SkippedTargets     int    `json:"skipped_targets"`
	ResetSubscriptions int    `json:"reset_subscriptions"`
	RestoredQuota      int64  `json:"restored_quota"`
	VouchersIssued     int    `json:"vouchers_issued"`
	VoucherExpiresAt   int64  `json:"voucher_expires_at,omitempty"`
}

func normalizeResetTargets(targets []SubscriptionResetTarget) ([]SubscriptionResetTarget, error) {
	if len(targets) > maxSubscriptionResetTargets {
		return nil, fmt.Errorf("too many subscription reset targets: %d", len(targets))
	}
	seen := make(map[string]struct{}, len(targets))
	result := make([]SubscriptionResetTarget, 0, len(targets))
	for _, target := range targets {
		if target.UserId <= 0 || target.PlanId <= 0 {
			return nil, errors.New("invalid subscription reset target")
		}
		key := fmt.Sprintf("%d:%d", target.UserId, target.PlanId)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, target)
	}
	return result, nil
}

func normalizeSubscriptionResetFilterIds(values []int, label string) ([]int, error) {
	if len(values) > 100 {
		return nil, fmt.Errorf("too many subscription reset %s filters", label)
	}
	if len(values) == 0 {
		return nil, nil
	}
	normalized := make([]int, 0, len(values))
	seen := make(map[int]struct{}, len(values))
	for _, value := range values {
		if value <= 0 {
			return nil, fmt.Errorf("invalid subscription reset %s filter", label)
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		normalized = append(normalized, value)
	}
	return normalized, nil
}

func resolveSubscriptionResetTargets(input AdminSubscriptionResetBatchInput) ([]SubscriptionResetTarget, error) {
	if !input.AllMatching {
		return normalizeResetTargets(input.Targets)
	}
	if len(input.Targets) > 0 {
		return nil, errors.New("explicit reset targets cannot be combined with all_matching")
	}
	filter := input.Filter
	if filter.PlanId < 0 {
		return nil, errors.New("invalid subscription reset plan filter")
	}
	if filter.PlanId > 0 && len(filter.PlanIds) > 0 {
		return nil, errors.New("plan_id cannot be combined with plan_ids")
	}
	filter.Query = strings.TrimSpace(filter.Query)
	if utf8.RuneCountInString(filter.Query) > 200 {
		return nil, errors.New("subscription reset search filter is too long")
	}
	var err error
	filter.PlanIds, err = normalizeSubscriptionResetFilterIds(filter.PlanIds, "plan")
	if err != nil {
		return nil, err
	}
	filter.UserIds, err = normalizeSubscriptionResetFilterIds(filter.UserIds, "user")
	if err != nil {
		return nil, err
	}
	filter.Page = 1
	filter.PageSize = maxSubscriptionResetTargets
	now := GetDBTimestamp()
	rows := make([]SubscriptionResetTarget, 0)
	err = adminSubscriptionResetEligibleQuery(filter, now).
		Select("us.user_id, us.plan_id").Group("us.user_id, us.plan_id").
		Order("us.user_id, us.plan_id").Limit(maxSubscriptionResetTargets + 1).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	if len(rows) > maxSubscriptionResetTargets {
		return nil, errors.New("subscription reset selection exceeds 5000 targets")
	}
	return normalizeResetTargets(rows)
}

func activeSubscriptionExistsTx(tx *gorm.DB, target SubscriptionResetTarget, now int64) (bool, error) {
	var count int64
	err := tx.Model(&UserSubscription{}).
		Where("user_id = ? AND plan_id = ? AND status = ? AND end_time > ?", target.UserId, target.PlanId, "active", now).
		Count(&count).Error
	return count > 0, err
}

func subscriptionResetOperationResult(operation *SubscriptionResetOperation) (*AdminSubscriptionResetBatchResult, error) {
	var result AdminSubscriptionResetBatchResult
	if operation == nil || json.Unmarshal([]byte(operation.ResultJSON), &result) != nil {
		return nil, errors.New("subscription reset operation result is malformed")
	}
	return &result, nil
}

func createSubscriptionResetAuditTx(tx *gorm.DB, actorUserId int, audit SubscriptionResetAuditContext, now int64, action, content string, params map[string]any) error {
	username := strings.TrimSpace(audit.Username)
	if username == "" {
		if err := tx.Model(&User{}).Select("username").Where("id = ?", actorUserId).Scan(&username).Error; err != nil {
			return err
		}
	}
	other := map[string]any{
		"op": buildOpField(action, params),
		"admin_info": map[string]any{
			"admin_id": actorUserId, "admin_username": username,
			"admin_role": audit.Role, "auth_method": audit.AuthMethod,
		},
	}
	log := &Log{
		UserId: actorUserId, Username: username, CreatedAt: now, Type: LogTypeManage,
		Content: content, Ip: strings.TrimSpace(audit.IP), Other: common.MapToJsonStr(other),
	}
	ensureLogRequestId(log)
	return tx.Create(log).Error
}

func subscriptionResetOperationMatches(operation *SubscriptionResetOperation, actorUserId int, previewToken string) bool {
	return operation != nil && operation.ActorUserId == actorUserId && operation.PreviewToken == previewToken
}

func verifySubscriptionResetPreviewTx(tx *gorm.DB, targets []SubscriptionResetPreviewTarget, now int64) error {
	type resetPair struct{ userId, planId int }
	expected := make(map[int]SubscriptionResetPreviewSubscription)
	pairs := make([]resetPair, 0, len(targets))
	seenPairs := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		pairKey := fmt.Sprintf("%d:%d", target.UserId, target.PlanId)
		if target.UserId <= 0 || target.PlanId <= 0 {
			return ErrSubscriptionResetPreviewStale
		}
		if _, duplicate := seenPairs[pairKey]; duplicate {
			return ErrSubscriptionResetPreviewStale
		}
		seenPairs[pairKey] = struct{}{}
		pairs = append(pairs, resetPair{userId: target.UserId, planId: target.PlanId})
		for _, subscription := range target.Subscriptions {
			if subscription.Id <= 0 || subscription.UserId != target.UserId || subscription.PlanId != target.PlanId {
				return ErrSubscriptionResetPreviewStale
			}
			if _, duplicate := expected[subscription.Id]; duplicate {
				return ErrSubscriptionResetPreviewStale
			}
			expected[subscription.Id] = subscription
		}
	}
	if len(expected) == 0 {
		return ErrSubscriptionResetPreviewStale
	}
	seen := make(map[int]struct{}, len(expected))
	const pairsPerQuery = 200
	for start := 0; start < len(pairs); start += pairsPerQuery {
		end := start + pairsPerQuery
		if end > len(pairs) {
			end = len(pairs)
		}
		conditions := make([]string, 0, end-start)
		args := make([]any, 0, 2*(end-start))
		for _, pair := range pairs[start:end] {
			conditions = append(conditions, "(user_id = ? AND plan_id = ?)")
			args = append(args, pair.userId, pair.planId)
		}
		rows := make([]UserSubscription, 0)
		query := lockForUpdate(tx).Where("status = ? AND end_time > ?", "active", now).
			Where("("+strings.Join(conditions, " OR ")+")", args...).Order("id")
		if err := query.Find(&rows).Error; err != nil {
			return err
		}
		for _, current := range rows {
			frozen, ok := expected[current.Id]
			if !ok || current.UserId != frozen.UserId || current.PlanId != frozen.PlanId ||
				current.Status != frozen.Status || current.EndTime != frozen.EndTime || current.AmountUsed != frozen.AmountUsed ||
				current.UpdatedAt != frozen.UpdatedAt {
				return ErrSubscriptionResetPreviewStale
			}
			seen[current.Id] = struct{}{}
		}
	}
	if len(seen) != len(expected) {
		return ErrSubscriptionResetPreviewStale
	}
	return nil
}

func resetFrozenSubscriptionTargetTx(tx *gorm.DB, target SubscriptionResetPreviewTarget, now int64) (int, int64, error) {
	resetCount := 0
	var restoredQuota int64
	for _, frozen := range target.Subscriptions {
		query := tx.Model(&UserSubscription{}).Where(
			"id = ? AND user_id = ? AND plan_id = ? AND status = ? AND end_time = ? AND end_time > ? AND amount_used = ? AND updated_at = ?",
			frozen.Id, frozen.UserId, frozen.PlanId, frozen.Status, frozen.EndTime, now, frozen.AmountUsed, frozen.UpdatedAt,
		)
		updated := query.UpdateColumn("amount_used", 0)
		if updated.Error != nil {
			return 0, 0, updated.Error
		}
		if updated.RowsAffected != 1 {
			if frozen.AmountUsed != 0 {
				return 0, 0, ErrSubscriptionResetPreviewStale
			}
			var unchanged int64
			if err := query.Count(&unchanged).Error; err != nil {
				return 0, 0, err
			}
			if unchanged != 1 {
				return 0, 0, ErrSubscriptionResetPreviewStale
			}
		}
		resetCount++
		var addErr error
		restoredQuota, addErr = checkedSubscriptionResetAdd(restoredQuota, frozen.AmountUsed)
		if addErr != nil {
			return 0, 0, addErr
		}
	}
	return resetCount, restoredQuota, nil
}

func AdminResetSubscriptionsBatch(input AdminSubscriptionResetBatchInput) (*AdminSubscriptionResetBatchResult, error) {
	if input.ActorUserId <= 0 {
		return nil, errors.New("invalid subscription reset actor")
	}
	previewToken := strings.TrimSpace(input.PreviewToken)
	if previewToken == "" {
		return nil, errors.New("subscription reset preview is required")
	}
	operationId := strings.TrimSpace(input.OperationId)
	if operationId == "" {
		return nil, errors.New("subscription reset operation id is required")
	}
	if len(operationId) > 64 {
		return nil, errors.New("subscription reset operation id is too long")
	}
	now := GetDBTimestamp()
	var result *AdminSubscriptionResetBatchResult
	err := DB.Transaction(func(tx *gorm.DB) error {
		var existingOperation SubscriptionResetOperation
		existingErr := tx.Where("operation_id = ?", operationId).First(&existingOperation).Error
		if existingErr == nil {
			if !subscriptionResetOperationMatches(&existingOperation, input.ActorUserId, previewToken) {
				return ErrSubscriptionResetOperationConflict
			}
			var err error
			result, err = subscriptionResetOperationResult(&existingOperation)
			return err
		}
		if !errors.Is(existingErr, gorm.ErrRecordNotFound) {
			return existingErr
		}

		var preview SubscriptionResetPreview
		if err := lockForUpdate(tx).Where("token = ? AND actor_user_id = ?", previewToken, input.ActorUserId).First(&preview).Error; err != nil {
			return err
		}
		if preview.ExpiresAt <= now {
			return errors.New("subscription reset preview has expired")
		}
		if preview.ConsumedAt > 0 {
			return errors.New("subscription reset preview has already been consumed")
		}
		var targets []SubscriptionResetPreviewTarget
		if err := json.Unmarshal([]byte(preview.TargetsJSON), &targets); err != nil {
			return errors.New("subscription reset preview targets are malformed")
		}
		payloadHash, err := subscriptionResetPayloadHash(preview.Mode, targets)
		if err != nil || payloadHash != preview.PayloadHash || len(targets) != preview.TargetCount {
			return errors.New("subscription reset preview payload is invalid")
		}
		if err := verifySubscriptionResetPreviewTx(tx, targets, now); err != nil {
			return err
		}
		claim := tx.Model(&SubscriptionResetPreview{}).
			Where("token = ? AND actor_user_id = ? AND consumed_at = 0 AND expires_at > ?", previewToken, input.ActorUserId, now).
			Updates(map[string]any{"consumed_at": now, "operation_id": operationId})
		if claim.Error != nil {
			return claim.Error
		}
		if claim.RowsAffected != 1 {
			return errors.New("subscription reset preview has already been consumed")
		}

		result = &AdminSubscriptionResetBatchResult{
			OperationId: operationId, Mode: preview.Mode, RequestedTargets: len(targets),
			ProcessedTargets: len(targets), VoucherExpiresAt: preview.VoucherExpiresAt,
		}
		for _, target := range targets {
			event := SubscriptionResetEvent{
				OperationId: operationId, UserId: target.UserId, PlanId: target.PlanId,
				Mode: preview.Mode, ActorUserId: input.ActorUserId, CreatedAt: now,
			}
			if preview.Mode == SubscriptionResetModeHard {
				resetCount, restoredQuota, err := resetFrozenSubscriptionTargetTx(tx, target, now)
				if err != nil {
					return err
				}
				event.ResetCount = resetCount
				event.RestoredQuota = restoredQuota
				result.ResetSubscriptions += resetCount
				nextRestoredQuota, addErr := checkedSubscriptionResetAdd(result.RestoredQuota, restoredQuota)
				if addErr != nil {
					return addErr
				}
				result.RestoredQuota = nextRestoredQuota
			} else {
				voucher := SubscriptionResetVoucher{
					UserId: target.UserId, PlanId: target.PlanId, OperationId: operationId,
					Status: SubscriptionResetVoucherAvailable, ExpiresAt: preview.VoucherExpiresAt,
					CreatedBy: input.ActorUserId, CreatedAt: now, UpdatedAt: now,
				}
				if err := tx.Create(&voucher).Error; err != nil {
					return err
				}
				event.VoucherId = voucher.Id
				event.VoucherExpiry = preview.VoucherExpiresAt
				result.VouchersIssued++
			}
			if err := tx.Create(&event).Error; err != nil {
				return err
			}
		}
		resultJSON, err := json.Marshal(result)
		if err != nil {
			return err
		}
		if err := tx.Create(&SubscriptionResetOperation{
			OperationId: operationId, PreviewToken: previewToken, ActorUserId: input.ActorUserId,
			Mode: preview.Mode, PayloadHash: preview.PayloadHash, ResultJSON: string(resultJSON),
			CreatedAt: now, CompletedAt: now,
		}).Error; err != nil {
			return err
		}
		return createSubscriptionResetAuditTx(
			tx, input.ActorUserId, input.Audit, now, "subscription.reset.execute",
			fmt.Sprintf("Executed %s subscription reset %s", result.Mode, result.OperationId),
			map[string]any{
				"operation_id": result.OperationId, "mode": result.Mode,
				"requested_targets": result.RequestedTargets, "processed_targets": result.ProcessedTargets,
				"reset_subscriptions": result.ResetSubscriptions, "restored_quota": result.RestoredQuota,
				"vouchers_issued": result.VouchersIssued,
			},
		)

	})
	if err == nil {
		return result, nil
	}
	var completed SubscriptionResetOperation
	if lookupErr := DB.Where("operation_id = ?", operationId).First(&completed).Error; lookupErr == nil && subscriptionResetOperationMatches(&completed, input.ActorUserId, previewToken) {
		return subscriptionResetOperationResult(&completed)
	}
	return nil, err
}

type UserSubscriptionResetVoucher struct {
	SubscriptionResetVoucher
	PlanTitle string `json:"plan_title"`
	Expired   bool   `json:"expired"`
}

func CleanupSubscriptionResetPreviewsContext(ctx context.Context, batchSize int) (int64, error) {
	if batchSize <= 0 {
		return 0, gorm.ErrInvalidData
	}
	cutoff := getDBTimestamp(DB.WithContext(ctx)) - subscriptionResetPreviewRetentionSeconds
	eligible := "(consumed_at = 0 AND expires_at < ?) OR (consumed_at > 0 AND consumed_at < ? AND EXISTS (SELECT 1 FROM subscription_reset_operations AS operations WHERE operations.preview_token = subscription_reset_previews.token))"
	var deleted int64
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var tokens []string
		if err := lockForUpdate(tx).Model(&SubscriptionResetPreview{}).
			Where(eligible, cutoff, cutoff).
			Order("CASE WHEN consumed_at > 0 THEN consumed_at ELSE expires_at END, token").
			Limit(batchSize).
			Pluck("token", &tokens).Error; err != nil {
			return err
		}
		if len(tokens) == 0 {
			return nil
		}
		result := tx.Where("token IN ?", tokens).
			Where(eligible, cutoff, cutoff).
			Delete(&SubscriptionResetPreview{})
		deleted = result.RowsAffected
		return result.Error
	})
	return deleted, err
}

func ListUserSubscriptionResetVouchers(userId int) ([]UserSubscriptionResetVoucher, error) {
	if userId <= 0 {
		return nil, errors.New("invalid user id")
	}
	now := GetDBTimestamp()
	rows := make([]UserSubscriptionResetVoucher, 0)
	err := DB.Table("subscription_reset_vouchers AS vouchers").
		Select("vouchers.*, plans.title AS plan_title").
		Joins("JOIN subscription_plans AS plans ON plans.id = vouchers.plan_id").
		Where("vouchers.user_id = ?", userId).
		Order(clause.Expr{
			SQL:  "CASE WHEN vouchers.status = ? AND vouchers.expires_at > ? THEN 0 ELSE 1 END, vouchers.id DESC",
			Vars: []any{SubscriptionResetVoucherAvailable, now},
		}).Limit(100).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for index := range rows {
		rows[index].Expired = rows[index].Status == SubscriptionResetVoucherAvailable && rows[index].ExpiresAt <= now
	}
	return rows, nil
}

func RedeemUserSubscriptionResetVoucher(userId, voucherId int) (*SubscriptionResetResult, error) {
	return RedeemUserSubscriptionResetVoucherWithAudit(userId, voucherId, SubscriptionResetAuditContext{})
}

func RedeemUserSubscriptionResetVoucherWithAudit(userId, voucherId int, audit SubscriptionResetAuditContext) (*SubscriptionResetResult, error) {
	if userId <= 0 || voucherId <= 0 {
		return nil, errors.New("invalid reset voucher")
	}
	now := GetDBTimestamp()
	operationId := fmt.Sprintf("voucher:%d", voucherId)
	var result *SubscriptionResetResult
	err := DB.Transaction(func(tx *gorm.DB) error {
		var voucher SubscriptionResetVoucher
		if err := lockForUpdate(tx).Where("id = ? AND user_id = ?", voucherId, userId).First(&voucher).Error; err != nil {
			return err
		}
		if voucher.Status == SubscriptionResetVoucherRedeemed {
			var event SubscriptionResetEvent
			if err := tx.Where("operation_id = ? AND voucher_id = ? AND mode = ?", operationId, voucherId, "voucher_redeem").First(&event).Error; err != nil {
				return ErrSubscriptionResetVoucherUnavailable
			}
			var plan SubscriptionPlan
			if err := tx.Select("id", "title").Where("id = ?", voucher.PlanId).First(&plan).Error; err != nil {
				return err
			}
			result = &SubscriptionResetResult{
				PlanId: voucher.PlanId, PlanTitle: plan.Title,
				MatchedCount: event.ResetCount, ResetCount: event.ResetCount, UserCount: 1,
				RestoredQuota: event.RestoredQuota, AffectedUserIds: []int{userId},
			}
			return nil
		}
		if voucher.Status != SubscriptionResetVoucherAvailable {
			return ErrSubscriptionResetVoucherUnavailable
		}
		if voucher.ExpiresAt <= now {
			return ErrSubscriptionResetVoucherExpired
		}
		active, err := activeSubscriptionExistsTx(tx, SubscriptionResetTarget{UserId: userId, PlanId: voucher.PlanId}, now)
		if err != nil {
			return err
		}
		if !active {
			return ErrSubscriptionResetRequiresActiveSubscription
		}
		var plan SubscriptionPlan
		if err := lockForUpdate(tx).Where("id = ?", voucher.PlanId).First(&plan).Error; err != nil {
			return err
		}
		claim := tx.Model(&SubscriptionResetVoucher{}).
			Where("id = ? AND user_id = ? AND status = ? AND expires_at > ?", voucherId, userId, SubscriptionResetVoucherAvailable, now).
			Updates(map[string]any{"status": SubscriptionResetVoucherRedeemed, "redeemed_at": now, "updated_at": now})
		if claim.Error != nil {
			return claim.Error
		}
		if claim.RowsAffected != 1 {
			return ErrSubscriptionResetVoucherUnavailable
		}
		result, err = adminResetUserSubscriptionsByPlanTx(tx, userId, &plan, now, false)
		if err != nil {
			return err
		}
		if err := tx.Create(&SubscriptionResetEvent{
			OperationId: operationId, UserId: userId, PlanId: voucher.PlanId,
			Mode: "voucher_redeem", ActorUserId: userId, VoucherId: voucher.Id,
			ResetCount: result.ResetCount, RestoredQuota: result.RestoredQuota, CreatedAt: now,
		}).Error; err != nil {
			return err
		}
		return createSubscriptionResetAuditTx(
			tx, userId, audit, now, "subscription.reset.voucher_redeem",
			fmt.Sprintf("Redeemed subscription reset voucher %d", voucherId),
			map[string]any{
				"voucher_id": voucherId, "reset_subscriptions": result.ResetCount,
				"restored_quota": result.RestoredQuota,
			},
		)
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
