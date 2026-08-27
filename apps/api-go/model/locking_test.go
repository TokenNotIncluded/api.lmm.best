package model

import (
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/utils/tests"
)

// lockForUpdate must emit FOR UPDATE on databases that support it and skip
// it on SQLite, where the syntax does not exist.
//
// The dummy dialector is used because SQLite drivers strip locking clauses
// from the generated SQL, which would mask what the helper itself does.
func TestRowLockHelpersEmitSupportedClauses(t *testing.T) {
	dummyDB, err := gorm.Open(tests.DummyDialector{}, &gorm.Config{DryRun: true})
	require.NoError(t, err)
	buildSQL := func(lock func(*gorm.DB) *gorm.DB) string {
		var rows []Redemption
		return lock(dummyDB).Where("id = ?", 1).Find(&rows).Statement.SQL.String()
	}

	t.Cleanup(func() {
		common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	})

	for _, databaseType := range []common.DatabaseType{
		common.DatabaseTypeMySQL,
		common.DatabaseTypePostgreSQL,
	} {
		common.SetDatabaseTypes(databaseType, common.DatabaseTypeSQLite)
		assert.Contains(t, buildSQL(lockForUpdate), "FOR UPDATE")
		assert.Contains(t, buildSQL(lockForShare), "FOR SHARE")
	}

	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	assert.NotContains(t, buildSQL(lockForUpdate), "FOR UPDATE")
	assert.NotContains(t, buildSQL(lockForShare), "FOR SHARE")
}
