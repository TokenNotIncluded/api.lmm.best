package model

import (
	"database/sql"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestModerationProviderCallsDefaultDialectDDL(t *testing.T) {
	for _, dialect := range []string{"mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			db := topUpSortDryRunDB(t, dialect)
			pool, err := db.DB()
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, pool.Close()) })
			statement := &gorm.Statement{DB: db}
			require.NoError(t, statement.Parse(&ModerationJob{}))
			field := statement.Schema.LookUpField("ProviderCallsJSON")
			require.NotNil(t, field)
			// MySQL 8 requires a TEXT default to be an expression, even
			// for a literal. Exercise the pinned dialect's emitted DDL.
			require.Equal(t, "text NOT NULL DEFAULT ('[]')", db.Migrator().FullDataTypeOf(field).SQL)
		})
	}
}

func moderationProviderCallsDefaultRoundTrip(t *testing.T, db *gorm.DB, userID int) {
	t.Helper()
	job := moderationTestJob(userID, "omitted-provider-default", ModerationSourceRelayInput, "tolerant")
	job.EventKey, job.Status, job.Payload = "omitted-provider-default", ModerationJobCompleted, ""
	require.NoError(t, db.Omit("provider_calls_json").Create(job).Error)
	var stored ModerationJob
	require.NoError(t, db.First(&stored, job.ID).Error)
	require.Equal(t, "[]", stored.ProviderCallsJSON, "the database must supply an empty journal for omitted columns")
	require.Empty(t, stored.ProviderCalls())
	require.Error(t, db.Model(&ModerationJob{}).Where("id = ?", job.ID).Update("provider_calls_json", nil).Error, "the journal must remain NOT NULL")
	journal := `[{"attempt":1,"batch_index":1,"response_id":"modr-retained","request_id":"req_retained"}]`
	require.NoError(t, db.Model(&ModerationJob{}).Where("id = ?", job.ID).Update("provider_calls_json", journal).Error)
	require.NoError(t, db.AutoMigrate(&ModerationJob{}))
	require.NoError(t, db.First(&stored, job.ID).Error)
	require.Equal(t, journal, stored.ProviderCallsJSON, "reapplying the canonical default must not rewrite existing provider receipts")
	require.Equal(t, ModerationJobCompleted, stored.Status)
	require.Empty(t, stored.Payload)
	require.Len(t, stored.ProviderCalls(), 1)
}

func TestModerationProviderCallsOmittedDefaultSQLite(t *testing.T) {
	db, user := setupModerationEffectsTestDB(t)
	moderationProviderCallsDefaultRoundTrip(t, db, user.Id)
}

func TestModerationProviderCallsOmittedDefaultMySQL(t *testing.T) {
	db := marketDrawingMySQLDB(t)
	user := marketTestUser(t, db, "mysql-provider-default", 123456, common.RoleCommonUser)
	var column struct {
		DataType      string
		IsNullable    string
		ColumnDefault sql.NullString
		Extra         string
	}
	require.NoError(t, db.Raw(`SELECT DATA_TYPE, IS_NULLABLE, COLUMN_DEFAULT, EXTRA FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'moderation_jobs' AND COLUMN_NAME = 'provider_calls_json'`).Row().Scan(&column.DataType, &column.IsNullable, &column.ColumnDefault, &column.Extra))
	require.Equal(t, "text", column.DataType)
	require.Equal(t, "NO", column.IsNullable)
	require.True(t, column.ColumnDefault.Valid)
	require.Contains(t, column.ColumnDefault.String, "[]")
	require.Contains(t, column.Extra, "DEFAULT_GENERATED", "MySQL must recognize the TEXT default as an expression")
	moderationProviderCallsDefaultRoundTrip(t, db, user.Id)
}
