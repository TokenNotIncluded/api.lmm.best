package model

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestCloseDB_NilSafety(t *testing.T) {
	origDB := DB
	origLogDB := LOG_DB
	defer func() {
		DB = origDB
		LOG_DB = origLogDB
	}()

	// Case 1: Both DB and LOG_DB are nil
	DB = nil
	LOG_DB = nil
	require.NoError(t, CloseDB())
	require.Nil(t, DB)
	require.Nil(t, LOG_DB)

	// Case 2: DB is open, but LOG_DB is nil (e.g. failure before InitLogDB)
	db1, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	DB = db1
	LOG_DB = nil
	require.NoError(t, CloseDB())
	require.Nil(t, DB)
	require.Nil(t, LOG_DB)

	// Case 3: Both DB and LOG_DB are distinct non-nil databases
	db2, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	db3, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	DB = db2
	LOG_DB = db3
	require.NoError(t, CloseDB())
	require.Nil(t, DB)
	require.Nil(t, LOG_DB)

	// Case 4: Idempotent call after already closed
	require.NoError(t, CloseDB())

	// Case 5: Direct call to closeDB with nil
	require.NoError(t, closeDB(nil))
}
