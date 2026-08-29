package model

import (
	"bytes"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	commonRelay "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"gorm.io/gorm"
)

type TaskStatus string

func (t TaskStatus) ToVideoStatus() string {
	var status string
	switch t {
	case TaskStatusQueued, TaskStatusSubmitted:
		status = dto.VideoStatusQueued
	case TaskStatusInProgress:
		status = dto.VideoStatusInProgress
	case TaskStatusSuccess:
		status = dto.VideoStatusCompleted
	case TaskStatusFailure:
		status = dto.VideoStatusFailed
	default:
		status = dto.VideoStatusUnknown // Default fallback
	}
	return status
}

const (
	TaskStatusNotStart   TaskStatus = "NOT_START"
	TaskStatusSubmitted             = "SUBMITTED"
	TaskStatusQueued                = "QUEUED"
	TaskStatusInProgress            = "IN_PROGRESS"
	TaskStatusFailure               = "FAILURE"
	TaskStatusSuccess               = "SUCCESS"
	TaskStatusUnknown               = "UNKNOWN"
)

// TaskRefundStatus is persisted on the task so a process restart can resume a
// refund without relying on an in-memory claim or on quota being non-zero.
// Financial side effects are applied in one database transaction; there is
// intentionally no long-lived "processing" state that could be ambiguous
// after a crash.
type TaskRefundStatus string

const (
	TaskRefundStatusPending   TaskRefundStatus = "PENDING"
	TaskRefundStatusCompleted TaskRefundStatus = "COMPLETED"
)

// TaskRefundLegacyCutoff separates tasks created before timeout refunds were
// introduced. Those legacy tasks are failed without an automatic refund.
const TaskRefundLegacyCutoff int64 = 1771718400 // 2026-02-22 00:00:00 UTC

type Task struct {
	ID         int64                 `json:"id" gorm:"primary_key;AUTO_INCREMENT"`
	CreatedAt  int64                 `json:"created_at" gorm:"index"`
	UpdatedAt  int64                 `json:"updated_at"`
	TaskID     string                `json:"task_id" gorm:"type:varchar(191);index"` // 第三方id，不一定有/ song id\ Task id
	Platform   constant.TaskPlatform `json:"platform" gorm:"type:varchar(30);index"` // 平台
	UserId     int                   `json:"user_id" gorm:"index"`
	Group      string                `json:"group" gorm:"type:varchar(50)"` // 修正计费用
	ChannelId  int                   `json:"channel_id" gorm:"index"`
	Quota      int                   `json:"quota"`
	Action     string                `json:"action" gorm:"type:varchar(40);index"` // 任务类型, song, lyrics, description-mode
	Status     TaskStatus            `json:"status" gorm:"type:varchar(20);index"` // 任务状态
	FailReason string                `json:"fail_reason"`
	SubmitTime int64                 `json:"submit_time" gorm:"index"`
	StartTime  int64                 `json:"start_time" gorm:"index"`
	FinishTime int64                 `json:"finish_time" gorm:"index"`
	Progress   string                `json:"progress" gorm:"type:varchar(20);index"`
	// RefundQuota keeps the original pre-consumed amount after Quota is cleared.
	// RefundStatus is an explicit durable intent/state marker; both are hidden
	// from task API responses because they are internal billing state.
	RefundStatus TaskRefundStatus `json:"-" gorm:"type:varchar(16);default:'';index"`
	RefundQuota  int              `json:"-" gorm:"default:0"`
	RefundedAt   int64            `json:"-" gorm:"default:0"`
	Properties   Properties       `json:"properties" gorm:"type:json"`
	Username     string           `json:"username,omitempty" gorm:"-"`
	// 禁止返回给用户，内部可能包含key等隐私信息
	PrivateData TaskPrivateData `json:"-" gorm:"column:private_data;type:json"`
	Data        json.RawMessage `json:"data" gorm:"type:json"`
}

func (t *Task) SetData(data any) {
	b, _ := common.Marshal(data)
	t.Data = json.RawMessage(b)
}

func (t *Task) GetData(v any) error {
	return common.Unmarshal(t.Data, &v)
}

type Properties struct {
	Input             string `json:"input"`
	UpstreamModelName string `json:"upstream_model_name,omitempty"`
	OriginModelName   string `json:"origin_model_name,omitempty"`
}

func (m *Properties) Scan(val interface{}) error {
	bytesValue, _ := val.([]byte)
	if len(bytesValue) == 0 {
		*m = Properties{}
		return nil
	}
	return common.Unmarshal(bytesValue, m)
}

func (m Properties) Value() (driver.Value, error) {
	if m == (Properties{}) {
		return nil, nil
	}
	return common.Marshal(m)
}

type TaskPrivateData struct {
	Key            string `json:"key,omitempty"`
	UpstreamTaskID string `json:"upstream_task_id,omitempty"` // 上游真实 task ID
	ResultURL      string `json:"result_url,omitempty"`       // 任务成功后的结果 URL（视频地址等）
	// 计费上下文：用于异步退款/差额结算（轮询阶段读取）
	BillingSource  string              `json:"billing_source,omitempty"`  // "wallet" 或 "subscription"
	SubscriptionId int                 `json:"subscription_id,omitempty"` // 订阅 ID，用于订阅退款
	TokenId        int                 `json:"token_id,omitempty"`        // 令牌 ID，用于令牌额度退款
	NodeName       string              `json:"node_name,omitempty"`       // 发起任务的节点名，轮询结算阶段据此归属日志而非最后查询节点
	BillingContext *TaskBillingContext `json:"billing_context,omitempty"` // 计费参数快照（用于轮询阶段重新计算）
}

// TaskBillingContext 记录任务提交时的计费参数，以便轮询阶段可以重新计算额度。
type TaskBillingContext struct {
	ModelPrice      float64            `json:"model_price,omitempty"`       // 模型单价
	GroupRatio      float64            `json:"group_ratio,omitempty"`       // 分组倍率
	ModelRatio      float64            `json:"model_ratio,omitempty"`       // 模型倍率
	OtherRatios     map[string]float64 `json:"other_ratios,omitempty"`      // 附加倍率（时长、分辨率等）
	OriginModelName string             `json:"origin_model_name,omitempty"` // 模型名称，必须为OriginModelName
	PerCallBilling  bool               `json:"per_call_billing,omitempty"`  // 按次计费：跳过轮询阶段的差额结算
}

// GetUpstreamTaskID 获取上游真实 task ID（用于与 provider 通信）
// 旧数据没有 UpstreamTaskID 时，TaskID 本身就是上游 ID
func (t *Task) GetUpstreamTaskID() string {
	if t.PrivateData.UpstreamTaskID != "" {
		return t.PrivateData.UpstreamTaskID
	}
	return t.TaskID
}

// GetResultURL 获取任务结果 URL（视频地址等）
// 新数据存在 PrivateData.ResultURL 中；旧数据回退到 FailReason（历史兼容）
func (t *Task) GetResultURL() string {
	if t.PrivateData.ResultURL != "" {
		return t.PrivateData.ResultURL
	}
	return t.FailReason
}

// GenerateTaskID 生成对外暴露的 task_xxxx 格式 ID
func GenerateTaskID() string {
	key, _ := common.GenerateRandomCharsKey(32)
	return "task_" + key
}

func (p *TaskPrivateData) Scan(val interface{}) error {
	bytesValue, _ := val.([]byte)
	if len(bytesValue) == 0 {
		return nil
	}
	return common.Unmarshal(bytesValue, p)
}

func (p TaskPrivateData) Value() (driver.Value, error) {
	if (p == TaskPrivateData{}) {
		return nil, nil
	}
	return common.Marshal(p)
}

// SyncTaskQueryParams 用于包含所有搜索条件的结构体，可以根据需求添加更多字段
type SyncTaskQueryParams struct {
	Platform       constant.TaskPlatform
	ChannelID      string
	TaskID         string
	UserID         string
	Action         string
	Status         string
	StartTimestamp int64
	EndTimestamp   int64
	UserIDs        []int
}

func InitTask(platform constant.TaskPlatform, relayInfo *commonRelay.RelayInfo) *Task {
	properties := Properties{}
	privateData := TaskPrivateData{}
	if relayInfo != nil && relayInfo.ChannelMeta != nil {
		if relayInfo.ChannelMeta.ChannelType == constant.ChannelTypeGemini ||
			relayInfo.ChannelMeta.ChannelType == constant.ChannelTypeVertexAi {
			privateData.Key = relayInfo.ChannelMeta.ApiKey
		}
		if relayInfo.UpstreamModelName != "" {
			properties.UpstreamModelName = relayInfo.UpstreamModelName
		}
		if relayInfo.OriginModelName != "" {
			properties.OriginModelName = relayInfo.OriginModelName
		}
	}

	// 使用预生成的公开 ID（如果有），否则新生成
	taskID := ""
	if relayInfo.TaskRelayInfo != nil && relayInfo.TaskRelayInfo.PublicTaskID != "" {
		taskID = relayInfo.TaskRelayInfo.PublicTaskID
	} else {
		taskID = GenerateTaskID()
	}

	t := &Task{
		TaskID:      taskID,
		UserId:      relayInfo.UserId,
		Group:       relayInfo.UsingGroup,
		SubmitTime:  time.Now().Unix(),
		Status:      TaskStatusNotStart,
		Progress:    "0%",
		ChannelId:   relayInfo.ChannelId,
		Platform:    platform,
		Properties:  properties,
		PrivateData: privateData,
	}
	return t
}

func TaskGetAllUserTask(userId int, startIdx int, num int, queryParams SyncTaskQueryParams) []*Task {
	var tasks []*Task
	var err error

	// 初始化查询构建器
	query := DB.Where("user_id = ?", userId)

	if queryParams.TaskID != "" {
		query = query.Where("task_id = ?", queryParams.TaskID)
	}
	if queryParams.Action != "" {
		query = query.Where("action = ?", queryParams.Action)
	}
	if queryParams.Status != "" {
		query = query.Where("status = ?", queryParams.Status)
	}
	if queryParams.Platform != "" {
		query = query.Where("platform = ?", queryParams.Platform)
	}
	if queryParams.StartTimestamp != 0 {
		// 假设您已将前端传来的时间戳转换为数据库所需的时间格式，并处理了时间戳的验证和解析
		query = query.Where("submit_time >= ?", queryParams.StartTimestamp)
	}
	if queryParams.EndTimestamp != 0 {
		query = query.Where("submit_time <= ?", queryParams.EndTimestamp)
	}

	// 获取数据
	err = query.Omit("channel_id").Order("id desc").Limit(num).Offset(startIdx).Find(&tasks).Error
	if err != nil {
		return nil
	}

	return tasks
}

func TaskGetAllTasks(startIdx int, num int, queryParams SyncTaskQueryParams) []*Task {
	var tasks []*Task
	var err error

	// 初始化查询构建器
	query := DB

	// 添加过滤条件
	if queryParams.ChannelID != "" {
		query = query.Where("channel_id = ?", queryParams.ChannelID)
	}
	if queryParams.Platform != "" {
		query = query.Where("platform = ?", queryParams.Platform)
	}
	if queryParams.UserID != "" {
		query = query.Where("user_id = ?", queryParams.UserID)
	}
	if len(queryParams.UserIDs) != 0 {
		query = query.Where("user_id in (?)", queryParams.UserIDs)
	}
	if queryParams.TaskID != "" {
		query = query.Where("task_id = ?", queryParams.TaskID)
	}
	if queryParams.Action != "" {
		query = query.Where("action = ?", queryParams.Action)
	}
	if queryParams.Status != "" {
		query = query.Where("status = ?", queryParams.Status)
	}
	if queryParams.StartTimestamp != 0 {
		query = query.Where("submit_time >= ?", queryParams.StartTimestamp)
	}
	if queryParams.EndTimestamp != 0 {
		query = query.Where("submit_time <= ?", queryParams.EndTimestamp)
	}

	// 获取数据
	err = query.Order("id desc").Limit(num).Offset(startIdx).Find(&tasks).Error
	if err != nil {
		return nil
	}

	return tasks
}

func GetTimedOutUnfinishedTasks(cutoffUnix int64, limit int) []*Task {
	var tasks []*Task
	err := DB.Where("progress != ?", "100%").
		Where("status NOT IN ?", []string{TaskStatusFailure, TaskStatusSuccess}).
		Where("submit_time < ?", cutoffUnix).
		Order("submit_time").
		Limit(limit).
		Find(&tasks).Error
	if err != nil {
		return nil
	}
	return tasks
}

// GetUnrefundedFailedTasks returns failed tasks whose non-zero quota marks a
// pending refund. Legacy tasks are excluded before LIMIT so old rows cannot
// starve current refund reconciliation.
func GetUnrefundedFailedTasks(updatedBefore int64, limit int) []*Task {
	if limit <= 0 {
		return nil
	}
	var tasks []*Task
	err := DB.Where("status = ?", TaskStatusFailure).
		Where("(refund_status = ? OR (COALESCE(refund_status, '') = '' AND (quota != ? OR refund_quota != ?)))", TaskRefundStatusPending, 0, 0).
		Where("updated_at <= ?", updatedBefore).
		Where("(submit_time <= ? OR submit_time >= ?)", 0, TaskRefundLegacyCutoff).
		Order("id").Limit(limit).Find(&tasks).Error
	if err != nil {
		return nil
	}
	return tasks
}

func GetAllUnFinishSyncTasks(limit int) []*Task {
	var tasks []*Task
	var err error
	limit = normalizeTaskQueryLimit(limit)
	// get all tasks progress is not 100%
	err = DB.Where("progress != ?", "100%").Where("status != ?", TaskStatusFailure).Where("status != ?", TaskStatusSuccess).Limit(limit).Order("id").Find(&tasks).Error
	if err != nil {
		return nil
	}
	return tasks
}

func normalizeTaskQueryLimit(limit int) int {
	if limit <= 0 {
		return constant.DefaultTaskQueryLimit
	}
	if limit > constant.MaxTaskQueryLimit {
		return constant.MaxTaskQueryLimit
	}
	return limit
}

// HasUnfinishedSyncTasks reports whether at least one async (Suno/video) task is
// still in progress. It is a cheap existence check (LIMIT 1) used to decide
// whether the async_task_poll system task needs to run; when no task is pending
// the scheduler skips creating a row entirely.
func HasUnfinishedSyncTasks() bool {
	var id int64
	err := DB.Model(&Task{}).
		Where("progress != ?", "100%").
		Where("status != ?", TaskStatusFailure).
		Where("status != ?", TaskStatusSuccess).
		Limit(1).
		Pluck("id", &id).Error
	return err == nil && id != 0
}

// HasTaskPollingWork keeps the scheduler alive for both active polling and
// failed tasks whose refunds still need reconciliation.
func HasTaskPollingWork() bool {
	if HasUnfinishedSyncTasks() {
		return true
	}
	var id int64
	err := DB.Model(&Task{}).
		Where("status = ?", TaskStatusFailure).
		Where("(refund_status = ? OR (COALESCE(refund_status, '') = '' AND (quota != ? OR refund_quota != ?)))", TaskRefundStatusPending, 0, 0).
		Where("(submit_time <= ? OR submit_time >= ?)", 0, TaskRefundLegacyCutoff).
		Limit(1).Pluck("id", &id).Error
	return err == nil && id != 0
}

func GetByTaskId(userId int, taskId string) (*Task, bool, error) {
	if taskId == "" {
		return nil, false, nil
	}
	var task *Task
	var err error
	err = DB.Where("user_id = ? and task_id = ?", userId, taskId).
		First(&task).Error
	exist, err := RecordExist(err)
	if err != nil {
		return nil, false, err
	}
	return task, exist, err
}

func GetByTaskIds(userId int, taskIds []any) ([]*Task, error) {
	if len(taskIds) == 0 {
		return nil, nil
	}
	var task []*Task
	var err error
	err = DB.Where("user_id = ? and task_id in (?)", userId, taskIds).
		Find(&task).Error
	if err != nil {
		return nil, err
	}
	return task, nil
}

func (Task *Task) Insert() error {
	var err error
	err = DB.Create(Task).Error
	return err
}

type taskSnapshot struct {
	Status     TaskStatus
	Progress   string
	StartTime  int64
	FinishTime int64
	FailReason string
	ResultURL  string
	Data       json.RawMessage
}

func (s taskSnapshot) Equal(other taskSnapshot) bool {
	return s.Status == other.Status &&
		s.Progress == other.Progress &&
		s.StartTime == other.StartTime &&
		s.FinishTime == other.FinishTime &&
		s.FailReason == other.FailReason &&
		s.ResultURL == other.ResultURL &&
		bytes.Equal(s.Data, other.Data)
}

func (t *Task) Snapshot() taskSnapshot {
	return taskSnapshot{
		Status:     t.Status,
		Progress:   t.Progress,
		StartTime:  t.StartTime,
		FinishTime: t.FinishTime,
		FailReason: t.FailReason,
		ResultURL:  t.PrivateData.ResultURL,
		Data:       t.Data,
	}
}

func (Task *Task) Update() error {
	var err error
	err = DB.Save(Task).Error
	return err
}

func (t *Task) UpdateQuota() error {
	return DB.Model(t).Update("quota", t.Quota).Error
}

// ClaimQuotaForRefund atomically clears an expected marker. Only the caller
// that wins this compare-and-swap may perform the external refund.
func ClaimQuotaForRefund(id int64, expectedQuota int) (bool, error) {
	if id <= 0 || expectedQuota == 0 {
		return false, nil
	}
	result := DB.Model(&Task{}).
		Where("id = ? AND quota = ?", id, expectedQuota).
		Update("quota", 0)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

// RestoreQuotaAfterFailedRefund returns a marker only if no other process has
// claimed it since the failed attempt.
func RestoreQuotaAfterFailedRefund(id int64, quota int) (bool, error) {
	if id <= 0 || quota == 0 {
		return false, nil
	}
	result := DB.Model(&Task{}).
		Where("id = ? AND quota = ?", id, 0).
		Update("quota", quota)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

var ErrTaskRefundInvalid = errors.New("invalid task refund intent")

// PrepareTaskRefundIntent durably records the amount that must be refunded.
// It deliberately leaves Task.Quota untouched: a crash between this step and
// ApplyPreparedTaskRefund therefore leaves an observable pending intent, not a
// task that looks already refunded.
func PrepareTaskRefundIntent(id int64, expectedQuota int) (int, bool, error) {
	if id <= 0 || expectedQuota < 0 {
		return 0, false, ErrTaskRefundInvalid
	}

	var quota int
	var pending bool
	err := DB.Transaction(func(tx *gorm.DB) error {
		var task Task
		if err := lockForUpdate(tx).Where("id = ?", id).First(&task).Error; err != nil {
			return err
		}
		switch task.RefundStatus {
		case TaskRefundStatusCompleted:
			quota = task.RefundQuota
			return nil
		case TaskRefundStatusPending:
			if task.RefundQuota <= 0 {
				return ErrTaskRefundInvalid
			}
			quota = task.RefundQuota
			pending = true
			return nil
		case "":
			quota = task.RefundQuota
			if quota == 0 {
				quota = task.Quota
			}
			if quota == 0 {
				return nil
			}
			if quota < 0 {
				return ErrTaskRefundInvalid
			}
			// expectedQuota is a caller-side sanity check only. The locked row is
			// authoritative so a stale poller cannot overwrite a newer intent.
			if expectedQuota > 0 && task.Quota != expectedQuota && task.RefundQuota == 0 {
				return ErrTaskRefundInvalid
			}
			result := tx.Model(&Task{}).
				Where("id = ? AND (refund_status = '' OR refund_status IS NULL)", id).
				Updates(map[string]interface{}{
					"refund_status": TaskRefundStatusPending,
					"refund_quota":  quota,
					"updated_at":    common.GetTimestamp(),
				})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return gorm.ErrRecordNotFound
			}
			pending = true
			return nil
		default:
			return ErrTaskRefundInvalid
		}
	})
	return quota, pending, err
}

// ApplyPreparedTaskRefund applies a pending intent atomically with all
// database-backed quota changes and the final task state. If the process dies,
// the transaction either commits every change or rolls back, so retrying the
// pending intent cannot double-refund the wallet.
func ApplyPreparedTaskRefund(id int64) (int, bool, error) {
	if id <= 0 {
		return 0, false, ErrTaskRefundInvalid
	}

	var quota int
	var applied bool
	var userID int
	var tokenKey string
	err := DB.Transaction(func(tx *gorm.DB) error {
		var task Task
		if err := lockForUpdate(tx).Where("id = ?", id).First(&task).Error; err != nil {
			return err
		}
		if task.RefundStatus == TaskRefundStatusCompleted {
			quota = task.RefundQuota
			return nil
		}
		if task.RefundStatus != TaskRefundStatusPending || task.RefundQuota <= 0 {
			return ErrTaskRefundInvalid
		}
		quota = task.RefundQuota
		userID = task.UserId

		if task.PrivateData.BillingSource == "subscription" && task.PrivateData.SubscriptionId > 0 {
			var subscription UserSubscription
			if err := lockForUpdate(tx).
				Where("id = ?", task.PrivateData.SubscriptionId).
				First(&subscription).Error; err != nil {
				return err
			}
			used := subscription.AmountUsed - int64(quota)
			if used < 0 {
				used = 0
			}
			if err := tx.Model(&UserSubscription{}).
				Where("id = ?", subscription.Id).
				Update("amount_used", used).Error; err != nil {
				return err
			}
		} else if userID > 0 {
			if err := ApplyWalletQuotaDelta(tx, userID, quota); err != nil {
				return err
			}
		}

		if task.PrivateData.TokenId > 0 {
			var token Token
			err := tx.Select("id, key").Where("id = ?", task.PrivateData.TokenId).First(&token).Error
			if err == nil {
				tokenKey = token.Key
				if err := tx.Model(&Token{}).Where("id = ?", token.Id).Updates(map[string]interface{}{
					"remain_quota":  gorm.Expr("remain_quota + ?", quota),
					"used_quota":    gorm.Expr("used_quota - ?", quota),
					"accessed_time": common.GetTimestamp(),
				}).Error; err != nil {
					return err
				}
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}

		if userID > 0 {
			if err := tx.Model(&User{}).Where("id = ?", userID).
				Update("used_quota", gorm.Expr("used_quota - ?", quota)).Error; err != nil {
				return err
			}
		}
		if task.ChannelId > 0 {
			if err := tx.Model(&Channel{}).Where("id = ?", task.ChannelId).
				Update("used_quota", gorm.Expr("used_quota - ?", quota)).Error; err != nil {
				return err
			}
		}

		result := tx.Model(&Task{}).
			Where("id = ? AND refund_status = ?", id, TaskRefundStatusPending).
			Updates(map[string]interface{}{
				"quota":         0,
				"refund_status": TaskRefundStatusCompleted,
				"refunded_at":   common.GetTimestamp(),
				"updated_at":    common.GetTimestamp(),
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		applied = true
		return nil
	})
	if err != nil || !applied {
		return quota, false, err
	}

	// The database is authoritative. Invalidate cache snapshots only after the
	// transaction commits; a cache failure cannot make the durable refund retry
	// and double-apply.
	if userID > 0 {
		if cacheErr := invalidateUserCache(userID); cacheErr != nil {
			common.SysLog("failed to invalidate refunded user cache: " + cacheErr.Error())
		}
	}
	if tokenKey != "" {
		if cacheErr := invalidateTokenCacheForMutation(tokenKey); cacheErr != nil {
			common.SysLog("failed to invalidate refunded token cache: " + cacheErr.Error())
		}
	}
	return quota, true, nil
}

// ApplyTaskRefund is the restart-safe refund entry point used by async task
// reconciliation. The intent transaction is intentionally separate from the
// effect transaction so a crash between them leaves a visible pending row.
func ApplyTaskRefund(id int64, expectedQuota int) (int, bool, error) {
	quota, pending, err := PrepareTaskRefundIntent(id, expectedQuota)
	if err != nil || !pending {
		return quota, false, err
	}
	return ApplyPreparedTaskRefund(id)
}

// UpdateWithStatus performs a conditional UPDATE guarded by fromStatus (CAS).
// Returns (true, nil) if this caller won the update, (false, nil) if
// another process already moved the task out of fromStatus. MySQL commonly
// reports changed rows rather than matched rows, so a same-value no-op update
// can also return false even when the status predicate still matched.
//
// Uses Model().Select("*").Updates() instead of Save() because GORM's Save
// falls back to INSERT ON CONFLICT when the WHERE-guarded UPDATE matches
// zero rows, which silently bypasses the CAS guard.
func (t *Task) UpdateWithStatus(fromStatus TaskStatus) (bool, error) {
	result := DB.Model(t).Where("status = ?", fromStatus).Select("*").Updates(t)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

// TaskBulkUpdateByID performs an unconditional bulk UPDATE by primary key IDs.
// WARNING: This function has NO CAS (Compare-And-Swap) guard — it will overwrite
// any concurrent status changes. DO NOT use in billing/quota lifecycle flows
// (e.g., timeout, success, failure transitions that trigger refunds or settlements).
// For status transitions that involve billing, use Task.UpdateWithStatus() instead.
func TaskBulkUpdateByID(ids []int64, params map[string]any) error {
	if len(ids) == 0 {
		return nil
	}
	return DB.Model(&Task{}).
		Where("id in (?)", ids).
		Updates(params).Error
}

type TaskQuotaUsage struct {
	Mode  string  `json:"mode"`
	Count float64 `json:"count"`
}

// TaskCountAllTasks returns total tasks that match the given query params (admin usage)
func TaskCountAllTasks(queryParams SyncTaskQueryParams) int64 {
	var total int64
	query := DB.Model(&Task{})
	if queryParams.ChannelID != "" {
		query = query.Where("channel_id = ?", queryParams.ChannelID)
	}
	if queryParams.Platform != "" {
		query = query.Where("platform = ?", queryParams.Platform)
	}
	if queryParams.UserID != "" {
		query = query.Where("user_id = ?", queryParams.UserID)
	}
	if len(queryParams.UserIDs) != 0 {
		query = query.Where("user_id in (?)", queryParams.UserIDs)
	}
	if queryParams.TaskID != "" {
		query = query.Where("task_id = ?", queryParams.TaskID)
	}
	if queryParams.Action != "" {
		query = query.Where("action = ?", queryParams.Action)
	}
	if queryParams.Status != "" {
		query = query.Where("status = ?", queryParams.Status)
	}
	if queryParams.StartTimestamp != 0 {
		query = query.Where("submit_time >= ?", queryParams.StartTimestamp)
	}
	if queryParams.EndTimestamp != 0 {
		query = query.Where("submit_time <= ?", queryParams.EndTimestamp)
	}
	_ = query.Count(&total).Error
	return total
}

// TaskCountAllUserTask returns total tasks for given user
func TaskCountAllUserTask(userId int, queryParams SyncTaskQueryParams) int64 {
	var total int64
	query := DB.Model(&Task{}).Where("user_id = ?", userId)
	if queryParams.TaskID != "" {
		query = query.Where("task_id = ?", queryParams.TaskID)
	}
	if queryParams.Action != "" {
		query = query.Where("action = ?", queryParams.Action)
	}
	if queryParams.Status != "" {
		query = query.Where("status = ?", queryParams.Status)
	}
	if queryParams.Platform != "" {
		query = query.Where("platform = ?", queryParams.Platform)
	}
	if queryParams.StartTimestamp != 0 {
		query = query.Where("submit_time >= ?", queryParams.StartTimestamp)
	}
	if queryParams.EndTimestamp != 0 {
		query = query.Where("submit_time <= ?", queryParams.EndTimestamp)
	}
	_ = query.Count(&total).Error
	return total
}
func (t *Task) ToOpenAIVideo() *dto.OpenAIVideo {
	openAIVideo := dto.NewOpenAIVideo()
	openAIVideo.ID = t.TaskID
	openAIVideo.Status = t.Status.ToVideoStatus()
	openAIVideo.Model = t.Properties.OriginModelName
	openAIVideo.SetProgressStr(t.Progress)
	openAIVideo.CreatedAt = t.CreatedAt
	openAIVideo.CompletedAt = t.UpdatedAt
	openAIVideo.SetMetadata("url", t.GetResultURL())
	return openAIVideo
}
