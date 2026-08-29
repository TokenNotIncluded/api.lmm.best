package model

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
)

var walletQuotaColumns = []string{"quota", "used_quota", "aff_quota", "aff_history"}

type walletQuotaColumnMetadata struct {
	ColumnName string `gorm:"column:column_name"`
	DataType   string `gorm:"column:data_type"`
	ColumnType string `gorm:"column:column_type"`
	UDTName    string `gorm:"column:udt_name"`
}

func skip64BitQuotaSchemaCheck() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("SKIP_64BIT_QUOTA_SCHEMA_CHECK"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func walletQuotaColumnIsBigInt(databaseType common.DatabaseType, column walletQuotaColumnMetadata) bool {
	dataType := strings.ToLower(strings.TrimSpace(column.DataType))
	columnType := strings.ToLower(strings.TrimSpace(column.ColumnType))
	udtName := strings.ToLower(strings.TrimSpace(column.UDTName))
	switch databaseType {
	case common.DatabaseTypeMySQL:
		isBigInt := dataType == "bigint" || strings.HasPrefix(columnType, "bigint(")
		return isBigInt && !strings.Contains(columnType, "unsigned")
	case common.DatabaseTypePostgreSQL:
		return dataType == "bigint" || udtName == "int8"
	default:
		return true
	}
}

// check64BitQuotaSchema fails closed for an existing MySQL/PostgreSQL users
// table whose wallet/cumulative quota columns are not BIGINT. It intentionally
// never runs ALTER TABLE: operators must back up and migrate the schema under
// their own change-management process. SQLite is always skipped.
func check64BitQuotaSchema(db *gorm.DB, databaseType common.DatabaseType) error {
	if databaseType == common.DatabaseTypeSQLite || databaseType == common.DatabaseTypeClickHouse {
		return nil
	}
	if skip64BitQuotaSchemaCheck() {
		common.SysLog("warning: SKIP_64BIT_QUOTA_SCHEMA_CHECK is enabled; wallet quota schema safety was not verified")
		return nil
	}
	if databaseType != common.DatabaseTypeMySQL && databaseType != common.DatabaseTypePostgreSQL {
		return nil
	}
	var tableCount int64
	var columns []walletQuotaColumnMetadata
	var tableResult, columnResult *gorm.DB
	if databaseType == common.DatabaseTypeMySQL {
		tableResult = db.Raw(
			"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?",
			"users",
		).Scan(&tableCount)
		columnResult = db.Raw(
			"SELECT COLUMN_NAME AS column_name, DATA_TYPE AS data_type, COLUMN_TYPE AS column_type FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = ? AND column_name IN ?",
			"users", walletQuotaColumns,
		).Scan(&columns)
	} else {
		tableResult = db.Raw(
			"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = ?",
			"users",
		).Scan(&tableCount)
		columnResult = db.Raw(
			"SELECT column_name, data_type, udt_name FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = ? AND column_name IN ?",
			"users", walletQuotaColumns,
		).Scan(&columns)
	}
	if tableResult.Error != nil {
		return fmt.Errorf("inspect users table schema: %w", tableResult.Error)
	}
	if tableCount == 0 {
		return nil
	}
	if columnResult.Error != nil {
		return fmt.Errorf("inspect users quota schema: %w", columnResult.Error)
	}
	return validateWalletQuotaColumns(databaseType, columns)
}

func validateWalletQuotaColumns(databaseType common.DatabaseType, columns []walletQuotaColumnMetadata) error {
	seen := make(map[string]walletQuotaColumnMetadata, len(columns))
	for _, column := range columns {
		seen[strings.ToLower(strings.TrimSpace(column.ColumnName))] = column
	}
	var unsafe []string
	for _, name := range walletQuotaColumns {
		column, ok := seen[name]
		if !ok {
			unsafe = append(unsafe, name+" (missing)")
			continue
		}
		if !walletQuotaColumnIsBigInt(databaseType, column) {
			actual := column.DataType
			if column.ColumnType != "" {
				actual = column.ColumnType
			} else if column.UDTName != "" {
				actual = column.UDTName
			}
			unsafe = append(unsafe, fmt.Sprintf("%s (%s)", name, actual))
		}
	}
	if len(unsafe) == 0 {
		return nil
	}
	sort.Strings(unsafe)
	return fmt.Errorf(
		"users wallet quota schema requires BIGINT columns; unsafe columns: %s; back up and migrate them manually, or temporarily set SKIP_64BIT_QUOTA_SCHEMA_CHECK=true to bypass this startup check",
		strings.Join(unsafe, ", "),
	)
}
