package model

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSumUsedQuotaByUserIDSurvivesUsernameChange(t *testing.T) {
	previous := LOG_DB
	db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	LOG_DB = db
	t.Cleanup(func() { LOG_DB = previous })
	require.NoError(t, db.AutoMigrate(&Log{}))
	require.NoError(t, db.Create(&Log{
		UserId: 42, Username: "old-name", Type: LogTypeConsume,
		Quota: 123, PromptTokens: 10, CompletionTokens: 5,
	}).Error)

	stat, err := SumUsedQuotaByUserID(LogTypeUnknown, 0, 0, "", 42, "", 0, "")
	require.NoError(t, err)
	require.Equal(t, 123, stat.Quota)
	stat, err = SumUsedQuotaByUserID(LogTypeUnknown, 0, 0, "", 43, "", 0, "")
	require.NoError(t, err)
	require.Zero(t, stat.Quota)
	_, err = SumUsedQuotaByUserID(LogTypeUnknown, 0, 0, "", 0, "", 0, "")
	require.Error(t, err)

	// A username-based query no longer finds the row after an account rename;
	// the authenticated self-stat path must not depend on this mutable field.
	stat, err = SumUsedQuota(LogTypeUnknown, 0, 0, "", "new-name", "", 0, "")
	require.NoError(t, err)
	require.Zero(t, stat.Quota)
}
