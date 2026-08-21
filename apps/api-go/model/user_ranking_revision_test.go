package model

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestUserRankingRevisionTracksOnlyVisibilityWrites(t *testing.T) {
	db := openUserRankingRevisionTestDB(t, "file:user-ranking-revision?mode=memory&cache=shared")
	ctx := context.Background()
	initial, err := currentUserRankingRevision(db, ctx)
	require.NoError(t, err)

	user := User{
		Username: "ranking-revision-user",
		AffCode:  "ranking-revision-aff",
		Status:   common.UserStatusEnabled,
	}
	require.NoError(t, db.Create(&user).Error)
	created := requireUserRankingRevision(t, db)
	assert.Equal(t, initial+1, created)

	require.NoError(t, db.Model(&User{}).Where("id = ?", user.Id).Update("quota", 10).Error)
	assert.Equal(t, created, requireUserRankingRevision(t, db), "unrelated quota updates must not invalidate rankings")

	for index, update := range []map[string]interface{}{
		{"setting": `{"usage_leaderboard_visibility":"public"}`},
		{"setting": `{"usage_leaderboard_visibility":"hidden"}`},
		{"setting": `{"usage_leaderboard_visibility":"public"}`},
		{"setting": `{"usage_leaderboard_visibility":"hidden"}`},
		{"display_name": "Visible name"},
		{"username": "ranking-revision-renamed"},
		{"status": common.UserStatusDisabled},
	} {
		before := requireUserRankingRevision(t, db)
		require.NoError(t, db.Model(&User{}).Where("id = ?", user.Id).Updates(update).Error)
		assert.Equal(t, before+1, requireUserRankingRevision(t, db), "visibility update %d must advance monotonically", index)
	}

	var current User
	require.NoError(t, db.First(&current, user.Id).Error)
	beforeStructUpdate := requireUserRankingRevision(t, db)
	require.NoError(t, db.Model(&current).Updates(User{DisplayName: "Struct update name"}).Error)
	assert.Equal(t, beforeStructUpdate+1, requireUserRankingRevision(t, db), "struct Updates must be captured before GORM mutates Statement.Model")

	var edited User
	require.NoError(t, db.First(&edited, user.Id).Error)
	edited.DisplayName = "UpdateWithTx name"
	beforeUpdateWithTx := requireUserRankingRevision(t, db)
	require.NoError(t, edited.UpdateWithTx(db, false))
	assert.Equal(t, beforeUpdateWithTx+1, requireUserRankingRevision(t, db), "User.UpdateWithTx must invalidate cached public identity")

	beforeUpdateColumn := requireUserRankingRevision(t, db)
	require.NoError(t, db.Model(&User{}).Where("id = ?", user.Id).UpdateColumn("display_name", "").Error)
	assert.Equal(t, beforeUpdateColumn+1, requireUserRankingRevision(t, db), "UpdateColumn must not bypass privacy invalidation")

	beforeUnrelatedColumn := requireUserRankingRevision(t, db)
	require.NoError(t, db.Model(&User{}).Where("id = ?", user.Id).UpdateColumn("quota", 20).Error)
	assert.Equal(t, beforeUnrelatedColumn, requireUserRankingRevision(t, db), "unrelated UpdateColumn must not invalidate rankings")

	beforeDelete := requireUserRankingRevision(t, db)
	require.NoError(t, db.Delete(&user).Error)
	assert.Equal(t, beforeDelete+1, requireUserRankingRevision(t, db))
}

func TestUserRankingRevisionRollsBackWithUserMutation(t *testing.T) {
	db := openUserRankingRevisionTestDB(t, "file:user-ranking-revision-rollback?mode=memory&cache=shared")
	user := User{
		Username: "ranking-revision-rollback-user",
		AffCode:  "ranking-revision-rollback-aff",
		Status:   common.UserStatusEnabled,
	}
	require.NoError(t, db.Create(&user).Error)
	before := requireUserRankingRevision(t, db)

	expected := errors.New("rollback sentinel")
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&User{}).Where("id = ?", user.Id).Update("setting", `{"usage_leaderboard_visibility":"hidden"}`).Error; err != nil {
			return err
		}
		assert.Equal(t, before+1, requireUserRankingRevision(t, tx))
		return expected
	})
	require.ErrorIs(t, err, expected)
	assert.Equal(t, before, requireUserRankingRevision(t, db))
}

func TestUserRankingRevisionCallbacksFailClosedWithoutSingleton(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:user-ranking-revision-missing?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&User{}))
	require.NoError(t, RegisterUserRankingRevisionCallbacks(db))

	user := User{
		Username: "ranking-revision-missing-user",
		AffCode:  "ranking-revision-missing-aff",
		Status:   common.UserStatusEnabled,
	}
	err = db.Create(&user).Error
	require.Error(t, err)
	var count int64
	require.NoError(t, db.Model(&User{}).Count(&count).Error)
	assert.Zero(t, count, "the user write must roll back when revision state is unavailable")
}

func TestUserRankingRevisionIsSharedAcrossDatabaseHandles(t *testing.T) {
	dsn := "file:user-ranking-revision-shared?mode=memory&cache=shared"
	first := openUserRankingRevisionTestDB(t, dsn)
	second, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	secondSQLDB, err := second.DB()
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, secondSQLDB.Close()) })
	require.NoError(t, RegisterUserRankingRevisionCallbacks(second))

	user := User{
		Username: "ranking-revision-shared-user",
		AffCode:  "ranking-revision-shared-aff",
		Status:   common.UserStatusEnabled,
	}
	require.NoError(t, first.Create(&user).Error)
	before := requireUserRankingRevision(t, first)
	require.Equal(t, before, requireUserRankingRevision(t, second))

	require.NoError(t, second.Model(&User{}).Where("id = ?", user.Id).Update("setting", `{"usage_leaderboard_visibility":"hidden"}`).Error)
	assert.Equal(t, before+1, requireUserRankingRevision(t, first))
}

func openUserRankingRevisionTestDB(t *testing.T, dsn string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&UserRankingRevision{}, &User{}))
	require.NoError(t, EnsureUserRankingRevisionState(db))
	require.NoError(t, RegisterUserRankingRevisionCallbacks(db))
	t.Cleanup(func() {
		sqlDB, err := db.DB()
		if err == nil {
			assert.NoError(t, sqlDB.Close())
		}
	})
	return db
}

func requireUserRankingRevision(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	revision, err := currentUserRankingRevision(db, context.Background())
	require.NoError(t, err, fmt.Sprintf("read revision from %p", db))
	return revision
}
