package model

import (
	"reflect"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func mysqlBigIntWalletColumns() []walletQuotaColumnMetadata {
	columns := make([]walletQuotaColumnMetadata, 0, len(walletQuotaColumns))
	for _, name := range walletQuotaColumns {
		columns = append(columns, walletQuotaColumnMetadata{
			ColumnName: name,
			DataType:   "bigint",
			ColumnType: "bigint(20)",
		})
	}
	return columns
}

func TestValidateWalletQuotaColumnsRecognizesMySQLAndPostgresBigInt(t *testing.T) {
	require.NoError(t, validateWalletQuotaColumns(common.DatabaseTypeMySQL, mysqlBigIntWalletColumns()))

	postgresColumns := make([]walletQuotaColumnMetadata, 0, len(walletQuotaColumns))
	for _, name := range walletQuotaColumns {
		postgresColumns = append(postgresColumns, walletQuotaColumnMetadata{
			ColumnName: name,
			DataType:   "bigint",
			UDTName:    "int8",
		})
	}
	require.NoError(t, validateWalletQuotaColumns(common.DatabaseTypePostgreSQL, postgresColumns))
}

func TestValidateWalletQuotaColumnsFailsClosedForNarrowOrMissingColumns(t *testing.T) {
	columns := mysqlBigIntWalletColumns()
	columns[0].DataType = "int"
	columns[0].ColumnType = "int(11)"
	err := validateWalletQuotaColumns(common.DatabaseTypeMySQL, columns)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "quota (int(11))")

	columns = mysqlBigIntWalletColumns()
	columns[0].ColumnType = "bigint(20) unsigned"
	err = validateWalletQuotaColumns(common.DatabaseTypeMySQL, columns)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "quota (bigint(20) unsigned)")

	err = validateWalletQuotaColumns(common.DatabaseTypePostgreSQL, []walletQuotaColumnMetadata{
		{ColumnName: "quota", DataType: "bigint", UDTName: "int8"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "used_quota (missing)")
	assert.Contains(t, err.Error(), "aff_quota (missing)")
	assert.Contains(t, err.Error(), "aff_history (missing)")
}

func TestCheck64BitQuotaSchemaSkipsSQLiteAndHonorsEscapeHatch(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, check64BitQuotaSchema(db, common.DatabaseTypeSQLite))

	t.Setenv("SKIP_64BIT_QUOTA_SCHEMA_CHECK", "true")
	require.NoError(t, check64BitQuotaSchema(nil, common.DatabaseTypeMySQL))
}

func TestUserQuotaFieldsDeclareBigIntStorage(t *testing.T) {
	userType := reflect.TypeOf(User{})
	for _, fieldName := range []string{"Quota", "UsedQuota", "AffQuota", "AffHistoryQuota"} {
		field, ok := userType.FieldByName(fieldName)
		require.True(t, ok, fieldName)
		assert.Contains(t, field.Tag.Get("gorm"), "type:bigint", fieldName)
	}
}
