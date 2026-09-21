package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/LIghtJUNction/api.lmm.best/common"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type SystemTaskStatus string

const (
	SystemTaskStatusPending   SystemTaskStatus = "pending"
	SystemTaskStatusRunning   SystemTaskStatus = "running"
	SystemTaskStatusSucceeded SystemTaskStatus = "succeeded"
	SystemTaskStatusFailed    SystemTaskStatus = "failed"

	SystemTaskTypeLogCleanup         = "log_cleanup"
	SystemTaskTypeChannelTest        = "channel_test"
	SystemTaskTypeModelUpdate        = "model_update"
	SystemTaskTypeMidjourneyPoll     = "midjourney_poll"
	SystemTaskTypeAsyncTaskPoll      = "async_task_poll"
	SystemTaskTypeAssistantRetention = "assistant_retention"
	SystemTaskTypeAssistantPresets   = "assistant_pre_conversation_presets"
	SystemTaskTypeAssistantReview    = "assistant_review"
)

var (
	ErrSystemTaskLockLost      = errors.New("system task lock lost")
	ErrTaskHistoryCleanupStale = errors.New("task history cleanup preview is stale")
)

const (
	systemTaskJSONMaxBytes  = 1 << 20
	systemTaskErrorMaxBytes = 4 << 10
)

type SystemTask struct {
	ID        int64            `json:"id" gorm:"primary_key"`
	TaskID    string           `json:"task_id" gorm:"type:varchar(64);uniqueIndex"`
	Type      string           `json:"type" gorm:"type:varchar(64);index"`
	Status    SystemTaskStatus `json:"status" gorm:"type:varchar(32);index"`
	ActiveKey *string          `json:"active_key,omitempty" gorm:"type:varchar(64);uniqueIndex"`
	Payload   string           `json:"payload" gorm:"type:text"`
	State     string           `json:"state" gorm:"type:text"`
	Result    string           `json:"result" gorm:"type:text"`
	Error     string           `json:"error" gorm:"type:text"`
	LockedBy  string           `json:"locked_by" gorm:"type:varchar(128);index"`
	CreatedAt int64            `json:"created_at" gorm:"bigint;index"`
	UpdatedAt int64            `json:"updated_at" gorm:"bigint;index"`
}

type SystemTaskLock struct {
	Type        string `json:"type" gorm:"type:varchar(64);primaryKey"`
	TaskID      string `json:"task_id" gorm:"type:varchar(64);index"`
	LockedBy    string `json:"locked_by" gorm:"type:varchar(128);index"`
	LockedUntil int64  `json:"locked_until" gorm:"bigint;index"`
	UpdatedAt   int64  `json:"updated_at" gorm:"bigint;index"`
}

type SystemTaskResponse struct {
	ID        int64            `json:"id"`
	TaskID    string           `json:"task_id"`
	Type      string           `json:"type"`
	Status    SystemTaskStatus `json:"status"`
	ActiveKey *string          `json:"active_key,omitempty"`
	Payload   any              `json:"payload"`
	State     any              `json:"state"`
	Result    any              `json:"result"`
	Error     string           `json:"error"`
	LockedBy  string           `json:"locked_by"`
	CreatedAt int64            `json:"created_at"`
	UpdatedAt int64            `json:"updated_at"`
}

// SystemTaskSummaryResponse is the bounded representation used by the task
// history list. Payloads and results are implementation details of individual
// handlers and may each be as large as systemTaskJSONMaxBytes. Returning them
// for every row lets an otherwise harmless list request retain tens of
// megabytes of JSON in the Go heap. The list only needs progress, so expose a
// small state projection and leave the full representation to GetSystemTask.
type SystemTaskSummaryResponse struct {
	ID        int64            `json:"id"`
	TaskID    string           `json:"task_id"`
	Type      string           `json:"type"`
	Status    SystemTaskStatus `json:"status"`
	ActiveKey *string          `json:"active_key,omitempty"`
	State     any              `json:"state,omitempty"`
	Error     string           `json:"error"`
	LockedBy  string           `json:"locked_by"`
	CreatedAt int64            `json:"created_at"`
	UpdatedAt int64            `json:"updated_at"`
}

func (task *SystemTask) BeforeCreate(_ *gorm.DB) error {
	now := common.GetTimestamp()
	if task.CreatedAt == 0 {
		task.CreatedAt = now
	}
	if task.UpdatedAt == 0 {
		task.UpdatedAt = now
	}
	return nil
}

func (lock *SystemTaskLock) BeforeCreate(_ *gorm.DB) error {
	if lock.UpdatedAt == 0 {
		lock.UpdatedAt = common.GetTimestamp()
	}
	return nil
}

func GenerateSystemTaskID() (string, error) {
	key, err := common.GenerateRandomCharsKey(32)
	if err != nil {
		return "", err
	}
	return "systask_" + key, nil
}

func CreateSystemTask(taskType string, payload any, state any) (*SystemTask, error) {
	taskID, err := GenerateSystemTaskID()
	if err != nil {
		return nil, err
	}
	payloadText, err := marshalSystemTaskJSON(payload)
	if err != nil {
		return nil, err
	}
	stateText, err := marshalSystemTaskJSON(state)
	if err != nil {
		return nil, err
	}

	task := &SystemTask{
		TaskID:    taskID,
		Type:      taskType,
		Status:    SystemTaskStatusPending,
		ActiveKey: &taskType,
		Payload:   payloadText,
		State:     stateText,
	}

	if err := DB.Create(task).Error; err != nil {
		return nil, err
	}
	return task, nil
}

func GetSystemTaskByTaskID(taskID string) (*SystemTask, error) {
	var task SystemTask
	if err := DB.Where("task_id = ?", taskID).First(&task).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &task, nil
}

// GetAssistantReviewTaskByTaskID keeps the AdminAuth review detail endpoint
// scoped to assistant review runs, rather than turning it into a generic task
// lookup.
func GetAssistantReviewTaskByTaskID(taskID string) (*SystemTask, error) {
	var task SystemTask
	if err := DB.Where("task_id = ? AND type = ?", taskID, SystemTaskTypeAssistantReview).First(&task).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &task, nil
}

func GetActiveSystemTask(taskType string) (*SystemTask, error) {
	var task SystemTask
	err := DB.Where("type = ? AND status IN ?", taskType, activeSystemTaskStatuses()).
		Order("id desc").
		First(&task).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &task, nil
}

func FindPendingSystemTasks(taskType string, limit int) ([]*SystemTask, error) {
	var tasks []*SystemTask
	if limit <= 0 {
		limit = 1
	}
	err := DB.Where("type = ? AND status = ?", taskType, SystemTaskStatusPending).
		Order("id asc").
		Limit(limit).
		Find(&tasks).Error
	return tasks, err
}

func FindEarliestPendingSystemTasks(taskTypes []string) (map[string]*SystemTask, error) {
	tasksByType := map[string]*SystemTask{}
	if len(taskTypes) == 0 {
		return tasksByType, nil
	}

	subQuery := DB.Model(&SystemTask{}).
		Select("MIN(id)").
		Where("type IN ? AND status = ?", taskTypes, SystemTaskStatusPending).
		Group("type")
	var tasks []*SystemTask
	if err := DB.Where("id IN (?)", subQuery).Find(&tasks).Error; err != nil {
		return nil, err
	}
	for _, task := range tasks {
		tasksByType[task.Type] = task
	}
	return tasksByType, nil
}

func ListSystemTasks(limit int) ([]*SystemTask, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	var tasks []*SystemTask
	err := DB.Order("id desc").Limit(limit).Find(&tasks).Error
	return tasks, err
}

// ListSystemTaskSummaries loads only fields needed by the root task history
// panel. Keeping payload/result out of the SELECT is important because both
// are bounded per row, not per list response.
func ListSystemTaskSummaries(limit int) ([]*SystemTask, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	var tasks []*SystemTask
	err := DB.Select("id, task_id, type, status, active_key, CASE WHEN LENGTH(state) <= ? THEN state ELSE '' END AS state, error, locked_by, created_at, updated_at", systemTaskSummaryStateMaxBytes).
		Order("id desc").Limit(limit).Find(&tasks).Error
	return tasks, err
}

// ListAssistantReviewTaskSummaries exposes only the bounded history needed by
// the Advanced Security page. Keeping this separate from the root task
// history prevents an AdminAuth caller from enumerating unrelated system
// tasks or their progress payloads.
func ListAssistantReviewTaskSummaries(limit int) ([]*SystemTask, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	var tasks []*SystemTask
	err := DB.Select("id, task_id, type, status, active_key, CASE WHEN LENGTH(state) <= ? THEN state ELSE '' END AS state, error, locked_by, created_at, updated_at", systemTaskSummaryStateMaxBytes).
		Where("type = ?", SystemTaskTypeAssistantReview).
		Order("id desc").Limit(limit).Find(&tasks).Error
	return tasks, err
}

var terminalSystemTaskStatuses = []SystemTaskStatus{SystemTaskStatusSucceeded, SystemTaskStatusFailed}

// PreviewTaskHistoryCleanup returns the number of terminal rows that a cleanup
// would remove. Active work and other task types are never eligible.
func PreviewTaskHistoryCleanup(taskType string, keep int) (int64, error) {
	if taskType == "" || keep < 0 {
		return 0, gorm.ErrInvalidData
	}

	var eligible int64
	err := DB.Transaction(func(tx *gorm.DB) error {
		var terminalCount int64
		if err := tx.Model(&SystemTask{}).
			Where("type = ? AND status IN ?", taskType, terminalSystemTaskStatuses).
			Count(&terminalCount).Error; err != nil {
			return err
		}
		if terminalCount > int64(keep) {
			eligible = terminalCount - int64(keep)
		}
		return nil
	})
	return eligible, err
}

// CleanupTaskHistory reselects eligible IDs in a transaction, locks those rows
// where supported, and deletes exactly that selection. Keeping the selection
// separate from the delete avoids dialect-specific DELETE LIMIT behavior.
func CleanupTaskHistory(taskType string, keep int) (int64, error) {
	if taskType == "" || keep < 0 {
		return 0, gorm.ErrInvalidData
	}

	var deleted int64
	err := DB.Transaction(func(tx *gorm.DB) error {
		ids, err := selectTaskHistoryCleanupIDs(tx, taskType, keep)
		if err != nil {
			return err
		}
		deleted, err = deleteTaskHistoryCleanupIDs(tx, ids)
		return err
	})
	return deleted, err
}

// CleanupTaskHistoryWithAudit performs an administrator-confirmed cleanup and
// its audit insert atomically. A changed candidate count aborts the transaction
// before either the selected rows or the audit record can be written.
func CleanupTaskHistoryWithAudit(taskType string, keep int, expectedCount int64, adminUserID int) (int64, error) {
	if taskType == "" || keep < 0 || expectedCount < 0 || adminUserID <= 0 {
		return 0, gorm.ErrInvalidData
	}

	var deleted int64
	err := DB.Transaction(func(tx *gorm.DB) error {
		ids, err := selectTaskHistoryCleanupIDs(tx, taskType, keep)
		if err != nil {
			return err
		}
		if int64(len(ids)) != expectedCount {
			return ErrTaskHistoryCleanupStale
		}
		deleted, err = deleteTaskHistoryCleanupIDs(tx, ids)
		if err != nil {
			return err
		}

		var admin User
		if err := tx.Select("username").First(&admin, adminUserID).Error; err != nil {
			return err
		}
		log := &Log{
			UserId: adminUserID, Username: admin.Username,
			CreatedAt: common.GetTimestamp(), Type: LogTypeSystem,
			Content: fmt.Sprintf("deleted %d assistant review run history records (keep=%d)", deleted, keep),
		}
		ensureLogRequestId(log)
		return tx.Create(log).Error
	})
	return deleted, err
}

func selectTaskHistoryCleanupIDs(tx *gorm.DB, taskType string, keep int) ([]int64, error) {
	var ids []int64
	err := tx.Model(&SystemTask{}).
		Select("id").
		Where("type = ? AND status IN ?", taskType, terminalSystemTaskStatuses).
		Order("id DESC").
		Offset(keep).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Find(&ids).Error
	return ids, err
}

func deleteTaskHistoryCleanupIDs(tx *gorm.DB, ids []int64) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	result := tx.Where("id IN ?", ids).Delete(&SystemTask{})
	return result.RowsAffected, result.Error
}

// PruneTaskHistory bounds terminal task history without touching work that can
// still run. It preserves the historical error-only API for scheduled callers.
func PruneTaskHistory(taskType string, keep int) error {
	_, err := CleanupTaskHistory(taskType, keep)
	return err
}

// GetLatestSystemTask returns the most recent task row of the given type
// (any status) so the scheduler can decide whether enough time has elapsed
// since the last run. Returns (nil, nil) when no row exists.
func GetLatestSystemTask(taskType string) (*SystemTask, error) {
	var task SystemTask
	err := DB.Where("type = ?", taskType).Order("id desc").First(&task).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &task, nil
}

func GetLatestSystemTasks(taskTypes []string) (map[string]*SystemTask, error) {
	tasksByType := map[string]*SystemTask{}
	if len(taskTypes) == 0 {
		return tasksByType, nil
	}

	subQuery := DB.Model(&SystemTask{}).
		Select("MAX(id)").
		Where("type IN ?", taskTypes).
		Group("type")
	var tasks []*SystemTask
	if err := DB.Where("id IN (?)", subQuery).Find(&tasks).Error; err != nil {
		return nil, err
	}
	for _, task := range tasks {
		tasksByType[task.Type] = task
	}
	return tasksByType, nil
}

func ClaimSystemTask(id int64, taskType string, runnerID string, lockUntil int64) (*SystemTask, bool, error) {
	now := common.GetTimestamp()
	var task SystemTask
	if err := DB.Where("id = ? AND type = ? AND status = ?", id, taskType, SystemTaskStatusPending).First(&task).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}

	acquired, expiredTaskID, err := acquireSystemTaskLock(taskType, task.TaskID, runnerID, now, lockUntil)
	if err != nil || !acquired {
		return nil, acquired, err
	}
	if expiredTaskID != "" && expiredTaskID != task.TaskID {
		if err := MarkSystemTaskLeaseExpired(expiredTaskID); err != nil {
			_ = ReleaseSystemTaskLock(task.TaskID, runnerID)
			return nil, false, err
		}
	}

	result := DB.Model(&SystemTask{}).
		Where("id = ? AND type = ? AND status = ?", id, taskType, SystemTaskStatusPending).
		Updates(map[string]any{
			"status":     SystemTaskStatusRunning,
			"locked_by":  runnerID,
			"updated_at": now,
		})
	if result.Error != nil {
		_ = ReleaseSystemTaskLock(task.TaskID, runnerID)
		return nil, false, result.Error
	}
	if result.RowsAffected == 0 {
		_ = ReleaseSystemTaskLock(task.TaskID, runnerID)
		return nil, false, nil
	}

	if err := DB.Where("id = ?", id).First(&task).Error; err != nil {
		return nil, false, err
	}
	return &task, true, nil
}

func acquireSystemTaskLock(taskType string, taskID string, lockedBy string, now int64, lockUntil int64) (bool, string, error) {
	lock := &SystemTaskLock{
		Type:        taskType,
		TaskID:      taskID,
		LockedBy:    lockedBy,
		LockedUntil: lockUntil,
		UpdatedAt:   now,
	}
	if err := DB.Create(lock).Error; err == nil {
		return true, "", nil
	}

	var existing SystemTaskLock
	err := DB.Where("type = ?", taskType).First(&existing).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, "", nil
		}
		return false, "", err
	}
	if existing.LockedUntil >= now {
		return false, "", nil
	}

	result := DB.Model(&SystemTaskLock{}).
		Where("type = ? AND locked_until < ?", taskType, now).
		Updates(map[string]any{
			"task_id":      taskID,
			"locked_by":    lockedBy,
			"locked_until": lockUntil,
			"updated_at":   now,
		})
	if result.Error != nil {
		return false, "", result.Error
	}
	if result.RowsAffected == 0 {
		return false, "", nil
	}
	return true, existing.TaskID, nil
}

func UpdateSystemTaskState(taskID string, lockedBy string, state any) error {
	stateText, err := marshalSystemTaskJSON(state)
	if err != nil {
		return err
	}
	now := common.GetTimestamp()
	result := DB.Model(&SystemTask{}).
		Where("task_id = ? AND status = ? AND locked_by = ?", taskID, SystemTaskStatusRunning, lockedBy).
		Where("EXISTS (SELECT 1 FROM system_task_locks WHERE system_task_locks.task_id = system_tasks.task_id AND system_task_locks.locked_by = ? AND system_task_locks.locked_until >= ?)", lockedBy, now).
		Updates(map[string]any{
			"state":      stateText,
			"updated_at": now,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrSystemTaskLockLost
	}
	return nil
}

func RenewSystemTaskLock(taskID string, lockedBy string, lockUntil int64) error {
	now := common.GetTimestamp()
	result := DB.Model(&SystemTaskLock{}).
		Where("task_id = ? AND locked_by = ? AND locked_until >= ?", taskID, lockedBy, now).
		Updates(map[string]any{
			"locked_until": lockUntil,
			"updated_at":   now,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrSystemTaskLockLost
	}
	return nil
}

func MarkSystemTaskLeaseExpired(taskID string) error {
	result := DB.Model(&SystemTask{}).
		Where("task_id = ? AND status = ?", taskID, SystemTaskStatusRunning).
		Updates(map[string]any{
			"status":     SystemTaskStatusFailed,
			"active_key": nil,
			"error":      "task lease expired",
			"updated_at": common.GetTimestamp(),
		})
	return result.Error
}

func ExpireStaleSystemTaskLocks(now int64) error {
	var locks []*SystemTaskLock
	if err := DB.Where("locked_until < ?", now).Find(&locks).Error; err != nil {
		return err
	}
	for _, lock := range locks {
		if err := MarkSystemTaskLeaseExpired(lock.TaskID); err != nil {
			return err
		}
		result := DB.Where("type = ? AND task_id = ? AND locked_by = ? AND locked_until < ?", lock.Type, lock.TaskID, lock.LockedBy, now).
			Delete(&SystemTaskLock{})
		if result.Error != nil {
			return result.Error
		}
	}
	return nil
}

func ReleaseSystemTaskLock(taskID string, lockedBy string) error {
	result := DB.Where("task_id = ? AND locked_by = ?", taskID, lockedBy).Delete(&SystemTaskLock{})
	return result.Error
}

func FinishSystemTask(taskID string, lockedBy string, status SystemTaskStatus, resultPayload any, errorMessage string) error {
	resultText, err := marshalSystemTaskJSON(resultPayload)
	if err != nil {
		if !errors.Is(err, common.ErrLimitExceeded) {
			return err
		}
		status = SystemTaskStatusFailed
		resultText = ""
		errorMessage = "system task result exceeded byte limit"
	}
	errorMessage = limitSystemTaskError(errorMessage)
	now := common.GetTimestamp()
	result := DB.Model(&SystemTask{}).
		Where("task_id = ? AND status = ? AND locked_by = ?", taskID, SystemTaskStatusRunning, lockedBy).
		Where("EXISTS (SELECT 1 FROM system_task_locks WHERE system_task_locks.task_id = system_tasks.task_id AND system_task_locks.locked_by = ? AND system_task_locks.locked_until >= ?)", lockedBy, now).
		Updates(map[string]any{
			"status":     status,
			"active_key": nil,
			"result":     resultText,
			"error":      errorMessage,
			"updated_at": now,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrSystemTaskLockLost
	}
	return ReleaseSystemTaskLock(taskID, lockedBy)
}

func (task *SystemTask) DecodePayload(v any) error {
	return decodeSystemTaskJSONString(task.Payload, v)
}

func (task *SystemTask) DecodeState(v any) error {
	return decodeSystemTaskJSONString(task.State, v)
}

func (task *SystemTask) ToResponse() SystemTaskResponse {
	return SystemTaskResponse{
		ID:        task.ID,
		TaskID:    task.TaskID,
		Type:      task.Type,
		Status:    task.Status,
		ActiveKey: task.ActiveKey,
		Payload:   decodeSystemTaskJSONValue(task.Payload),
		State:     decodeSystemTaskJSONValue(task.State),
		Result:    decodeSystemTaskJSONValue(task.Result),
		Error:     task.Error,
		LockedBy:  task.LockedBy,
		CreatedAt: task.CreatedAt,
		UpdatedAt: task.UpdatedAt,
	}
}

// ToSummaryResponse intentionally projects state to progress only. A handler
// may write arbitrary bounded JSON state, but the list endpoint must not
// deserialize that entire value for every historical row.
func (task *SystemTask) ToSummaryResponse() SystemTaskSummaryResponse {
	return SystemTaskSummaryResponse{
		ID: task.ID, TaskID: task.TaskID, Type: task.Type, Status: task.Status,
		ActiveKey: task.ActiveKey, State: decodeSystemTaskProgress(task.State),
		Error: task.Error, LockedBy: task.LockedBy,
		CreatedAt: task.CreatedAt, UpdatedAt: task.UpdatedAt,
	}
}

const systemTaskSummaryStateMaxBytes = 16 << 10

func decodeSystemTaskProgress(data string) any {
	if len(data) == 0 || len(data) > systemTaskSummaryStateMaxBytes {
		return nil
	}
	var state struct {
		Progress *int `json:"progress"`
	}
	if err := json.Unmarshal([]byte(data), &state); err != nil || state.Progress == nil {
		return nil
	}
	return map[string]int{"progress": *state.Progress}
}

func activeSystemTaskStatuses() []string {
	return []string{string(SystemTaskStatusPending), string(SystemTaskStatusRunning)}
}

func marshalSystemTaskJSON(v any) (string, error) {
	if v == nil {
		return "", nil
	}
	data, err := common.MarshalLimit(v, systemTaskJSONMaxBytes)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func limitSystemTaskError(message string) string {
	if len(message) <= systemTaskErrorMaxBytes {
		return message
	}
	message = message[:systemTaskErrorMaxBytes]
	for len(message) > 0 && !utf8.ValidString(message) {
		message = message[:len(message)-1]
	}
	return message
}

func decodeSystemTaskJSONString(data string, v any) error {
	if data == "" {
		return nil
	}
	return common.UnmarshalJsonStr(data, v)
}

func decodeSystemTaskJSONValue(data string) any {
	if data == "" {
		return nil
	}
	var value any
	if err := common.UnmarshalJsonStr(data, &value); err != nil {
		return data
	}
	return value
}
