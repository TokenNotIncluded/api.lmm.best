package model

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	userRankingRevisionSingletonID       = 1
	userRankingCreateCallback            = "lmm:user-ranking-revision:create"
	userRankingCaptureUpdateCallback     = "lmm:user-ranking-revision:capture-update"
	userRankingBumpUpdateCallback        = "lmm:user-ranking-revision:bump-update"
	userRankingDeleteCallback            = "lmm:user-ranking-revision:delete"
	userRankingUpdateRelevantInstanceKey = "lmm:user-ranking-revision:update-relevant"
)

var userRankingVisibilityFields = []string{
	"Status",
	"Setting",
	"Username",
	"DisplayName",
	"DeletedAt",
}

// UserRankingRevision is a transactionally monotonic invalidation source for
// public user-ranking snapshots. All API instances read the same singleton.
type UserRankingRevision struct {
	ID       int   `gorm:"primaryKey;autoIncrement:false"`
	Revision int64 `gorm:"not null"`
}

// EnsureUserRankingRevisionState creates and seeds the singleton during the
// migration apply phase. Runtime verify mode must only call the verifier.
func EnsureUserRankingRevisionState(db *gorm.DB) error {
	if db == nil {
		return gorm.ErrInvalidDB
	}
	if err := db.AutoMigrate(&UserRankingRevision{}); err != nil {
		return fmt.Errorf("migrate user ranking revision: %w", err)
	}
	seed := UserRankingRevision{ID: userRankingRevisionSingletonID, Revision: 1}
	if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&seed).Error; err != nil {
		return fmt.Errorf("seed user ranking revision: %w", err)
	}
	return VerifyUserRankingRevisionState(db)
}

// VerifyUserRankingRevisionState proves the singleton exists without writing.
func VerifyUserRankingRevisionState(db *gorm.DB) error {
	if db == nil {
		return gorm.ErrInvalidDB
	}
	var state UserRankingRevision
	if err := db.Where("id = ?", userRankingRevisionSingletonID).Take(&state).Error; err != nil {
		return fmt.Errorf("read user ranking revision: %w", err)
	}
	if state.Revision <= 0 {
		return errors.New("user ranking revision must be positive")
	}
	return nil
}

// RegisterUserRankingRevisionCallbacks installs fail-closed callbacks on the
// active GORM connection. Relevant user writes and the revision increment run
// in the same database transaction.
func RegisterUserRankingRevisionCallbacks(db *gorm.DB) error {
	if db == nil {
		return gorm.ErrInvalidDB
	}
	if db.Callback().Create().Get(userRankingCreateCallback) == nil {
		if err := db.Callback().Create().After("gorm:after_create").Before("gorm:commit_or_rollback_transaction").Register(userRankingCreateCallback, bumpUserRankingRevisionAfterCreate); err != nil {
			return fmt.Errorf("register user ranking create callback: %w", err)
		}
	}
	if db.Callback().Update().Get(userRankingCaptureUpdateCallback) == nil {
		if err := db.Callback().Update().After("gorm:before_update").Before("gorm:update").Register(userRankingCaptureUpdateCallback, captureUserRankingRevisionUpdate); err != nil {
			return fmt.Errorf("register user ranking update capture callback: %w", err)
		}
	}
	if db.Callback().Update().Get(userRankingBumpUpdateCallback) == nil {
		if err := db.Callback().Update().After("gorm:after_update").Before("gorm:commit_or_rollback_transaction").Register(userRankingBumpUpdateCallback, bumpUserRankingRevisionAfterUpdate); err != nil {
			return fmt.Errorf("register user ranking update callback: %w", err)
		}
	}
	if db.Callback().Delete().Get(userRankingDeleteCallback) == nil {
		if err := db.Callback().Delete().After("gorm:after_delete").Before("gorm:commit_or_rollback_transaction").Register(userRankingDeleteCallback, bumpUserRankingRevisionAfterDelete); err != nil {
			return fmt.Errorf("register user ranking delete callback: %w", err)
		}
	}
	return nil
}

// CurrentUserRankingRevision performs one indexed singleton read. It is safe
// on the anonymous hot path and replaces the previous O(number of users) scan.
func CurrentUserRankingRevision(ctx context.Context) (int64, error) {
	return currentUserRankingRevision(DB, ctx)
}

func currentUserRankingRevision(db *gorm.DB, ctx context.Context) (int64, error) {
	if db == nil {
		return 0, gorm.ErrInvalidDB
	}
	if ctx == nil {
		return 0, errors.New("read user ranking revision: nil context")
	}
	var revision int64
	if err := db.WithContext(ctx).
		Model(&UserRankingRevision{}).
		Where("id = ?", userRankingRevisionSingletonID).
		Select("revision").
		Scan(&revision).Error; err != nil {
		return 0, fmt.Errorf("read user ranking revision: %w", err)
	}
	if revision <= 0 {
		return 0, errors.New("user ranking revision is missing")
	}
	return revision, nil
}

func bumpUserRankingRevisionAfterCreate(tx *gorm.DB) {
	if userRankingStatement(tx) && tx.RowsAffected > 0 {
		bumpUserRankingRevision(tx)
	}
}

func captureUserRankingRevisionUpdate(tx *gorm.DB) {
	if userRankingStatement(tx) && userRankingUpdateTouchesVisibility(tx) {
		tx.InstanceSet(userRankingUpdateRelevantInstanceKey, true)
	}
}

func bumpUserRankingRevisionAfterUpdate(tx *gorm.DB) {
	if !userRankingStatement(tx) || tx.RowsAffected <= 0 {
		return
	}
	relevant, ok := tx.InstanceGet(userRankingUpdateRelevantInstanceKey)
	if ok && relevant == true {
		bumpUserRankingRevision(tx)
	}
}

func bumpUserRankingRevisionAfterDelete(tx *gorm.DB) {
	if userRankingStatement(tx) && tx.RowsAffected > 0 {
		bumpUserRankingRevision(tx)
	}
}

func userRankingStatement(tx *gorm.DB) bool {
	return tx != nil && tx.Error == nil && tx.Statement != nil && tx.Statement.Schema != nil &&
		tx.Statement.Schema.ModelType == reflect.TypeOf(User{})
}

func userRankingUpdateTouchesVisibility(tx *gorm.DB) bool {
	if values, ok := tx.Statement.Dest.(map[string]interface{}); ok {
		for key := range values {
			if userRankingVisibilityField(key) {
				return true
			}
		}
		return false
	}
	for _, selected := range tx.Statement.Selects {
		if selected == "*" || userRankingVisibilityField(selected) {
			return true
		}
	}
	return tx.Statement.Changed(userRankingVisibilityFields...)
}

func userRankingVisibilityField(name string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(name, "_", ""))
	switch normalized {
	case "status", "setting", "username", "displayname", "deletedat":
		return true
	default:
		return false
	}
}

func bumpUserRankingRevision(tx *gorm.DB) {
	result := tx.Session(&gorm.Session{NewDB: true, SkipHooks: true}).
		Model(&UserRankingRevision{}).
		Where("id = ?", userRankingRevisionSingletonID).
		UpdateColumn("revision", gorm.Expr("revision + 1"))
	if result.Error != nil {
		tx.AddError(fmt.Errorf("increment user ranking revision: %w", result.Error))
		return
	}
	if result.RowsAffected != 1 {
		tx.AddError(errors.New("increment user ranking revision: singleton missing"))
	}
}
