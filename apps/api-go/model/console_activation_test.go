package model

import (
	"fmt"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupConsoleActivationTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	previousDB, previousLogDB := DB, LOG_DB
	previousRedisEnabled := common.RedisEnabled
	previousMainDatabaseType, previousLogDatabaseType := common.MainDatabaseType(), common.LogDatabaseType()
	common.RedisEnabled = false
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	DB, LOG_DB = db, db
	require.NoError(t, db.AutoMigrate(&User{}, &Token{}, &ReferralReward{}, &ReferralRewardEntry{}, &ReferralModerationOperation{}))

	t.Cleanup(func() {
		DB, LOG_DB = previousDB, previousLogDB
		common.RedisEnabled = previousRedisEnabled
		common.SetDatabaseTypes(previousMainDatabaseType, previousLogDatabaseType)
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func TestInsertTokenPermanentlyActivatesConsole(t *testing.T) {
	db := setupConsoleActivationTestDB(t)
	user := User{
		Username: "new-contributor",
		Password: "password",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
	}
	require.NoError(t, db.Create(&user).Error)
	token := Token{UserId: user.Id, Key: "first-credential", Name: "First credential"}

	require.NoError(t, InsertTokenAndActivateConsole(&token))
	var activated User
	require.NoError(t, db.First(&activated, user.Id).Error)
	assert.Positive(t, activated.ConsoleActivatedAt)

	require.NoError(t, db.Delete(&token).Error)
	var afterDelete User
	require.NoError(t, db.First(&afterDelete, user.Id).Error)
	assert.Equal(t, activated.ConsoleActivatedAt, afterDelete.ConsoleActivatedAt)
}

func TestInsertTokenFailureDoesNotActivateConsole(t *testing.T) {
	db := setupConsoleActivationTestDB(t)
	firstUser := User{Username: "existing-owner", Password: "password"}
	firstUser.AffCode = "existing-owner-aff"
	newUser := User{Username: "new-owner", Password: "password", AffCode: "new-owner-aff"}
	require.NoError(t, db.Create(&firstUser).Error)
	require.NoError(t, db.Create(&newUser).Error)
	require.NoError(t, db.Create(&Token{UserId: firstUser.Id, Key: "duplicate-key", Name: "Existing"}).Error)

	err := InsertTokenAndActivateConsole(&Token{UserId: newUser.Id, Key: "duplicate-key", Name: "Rejected"})
	require.Error(t, err)
	var unchanged User
	require.NoError(t, db.First(&unchanged, newUser.Id).Error)
	assert.Zero(t, unchanged.ConsoleActivatedAt)
}

func TestLegacyConsoleBackfillOnlyRunsWhenColumnWasIntroduced(t *testing.T) {
	db := setupConsoleActivationTestDB(t)
	legacy := User{Username: "legacy-user", Password: "password", AffCode: "legacy-user-aff"}
	newUser := User{Username: "post-rollout-user", Password: "password", AffCode: "post-rollout-user-aff"}
	require.NoError(t, db.Create(&legacy).Error)
	require.NoError(t, InitializeLegacyConsoleActivations(true))
	require.NoError(t, db.Create(&newUser).Error)
	require.NoError(t, InitializeLegacyConsoleActivations(false))

	require.NoError(t, db.First(&legacy, legacy.Id).Error)
	require.NoError(t, db.First(&newUser, newUser.Id).Error)
	assert.Positive(t, legacy.ConsoleActivatedAt)
	assert.Zero(t, newUser.ConsoleActivatedAt)
}

func TestExistingUsersL1BackfillRunsOnceAndLeavesLaterUsersAtL0(t *testing.T) {
	db := setupConsoleActivationTestDB(t)
	require.NoError(t, db.AutoMigrate(&Option{}))
	existing := User{Username: "existing-l1-floor", Password: "password", AffCode: "existing-l1-floor-aff"}
	require.NoError(t, db.Create(&existing).Error)

	require.NoError(t, InitializeExistingUsersL1Backfill())
	var activated User
	require.NoError(t, db.First(&activated, existing.Id).Error)
	assert.Positive(t, activated.ConsoleActivatedAt)

	later := User{Username: "later-l0-user", Password: "password", AffCode: "later-l0-user-aff"}
	require.NoError(t, db.Create(&later).Error)
	require.NoError(t, InitializeExistingUsersL1Backfill())
	var stillL0 User
	require.NoError(t, db.First(&stillL0, later.Id).Error)
	assert.Zero(t, stillL0.ConsoleActivatedAt)

	var marker Option
	require.NoError(t, db.First(&marker, "key = ?", existingUsersL1BackfillOptionKey).Error)
	assert.NotEmpty(t, marker.Value)
}
