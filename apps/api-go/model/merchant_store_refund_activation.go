package model

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"gorm.io/gorm"
	"gorm.io/gorm/migrator"
	"gorm.io/gorm/schema"
)

// ActivateMerchantStoreRefunds is a private operator action, never a runtime
// initializer. Every serving writer must already support capability 4. Schema
// inspection does not create or repair any table, index, or historical row.
func ActivateMerchantStoreRefunds(db *gorm.DB, expected int) error {
	if db == nil || (expected != 3 && expected != 4) || MerchantStoreWriterCapability < 4 {
		return ErrMerchantStoreWriterFrozen
	}
	return withMerchantStoreActivationDB(db, func(bound *gorm.DB) error {
		return bound.Transaction(func(tx *gorm.DB) error {
			required, err := storeWriterGateRow(tx, "UPDATE")
			if err != nil || (required != 4 && (required != expected || required != 3)) {
				return ErrMerchantStoreWriterFrozen
			}
			// An activation retry still checks the full schema; an already-raised
			// floor is not evidence that subsequently damaged schema is safe.
			if err := storeCheckRefundActivationSchema(tx); err != nil {
				return err
			}
			if required == 4 {
				return nil
			}
			result := tx.Model(&Option{}).Where("key = ? AND value = ?", MerchantStoreWriterCapabilityOption, "3").Update("value", "4")
			if result.Error != nil || result.RowsAffected != 1 {
				return ErrMerchantStoreWriterFrozen
			}
			return nil
		})
	})
}

func storeSameColumnNames(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// The retained SQLite driver predates GORM's GetIndexes implementation. Read
// SQLite's own catalog here rather than skipping uniqueness qualification.
func storeRefundActivationIndexes(tx *gorm.DB, model any, table string) ([]gorm.Index, error) {
	if tx.Dialector.Name() == "postgres" {
		// The retained driver does not scope GetIndexes to the active schema or
		// preserve key order. Qualify the exact bound schema and ordinal keys.
		var rows []struct {
			Name    string
			Unique  bool
			Primary bool
			Columns string
		}
		if err := tx.Raw(`SELECT idx.relname AS name, i.indisunique AS "unique", i.indisprimary AS "primary",
			pg_catalog.json_agg(a.attname ORDER BY k.ordinality)::text AS columns
			FROM pg_catalog.pg_index AS i
			JOIN pg_catalog.pg_class AS idx ON idx.oid = i.indexrelid
			JOIN pg_catalog.pg_class AS tab ON tab.oid = i.indrelid
			JOIN pg_catalog.pg_namespace AS ns ON ns.oid = tab.relnamespace
			CROSS JOIN LATERAL pg_catalog.unnest(i.indkey) WITH ORDINALITY AS k(attnum, ordinality)
			JOIN pg_catalog.pg_attribute AS a ON a.attrelid = tab.oid AND a.attnum = k.attnum
			WHERE ns.nspname = pg_catalog.current_schema() AND tab.relname = ?
			AND i.indisvalid AND i.indisready AND i.indexprs IS NULL AND i.indpred IS NULL
			AND k.ordinality <= i.indnkeyatts
			GROUP BY idx.relname, i.indisunique, i.indisprimary`, table).Scan(&rows).Error; err != nil {
			return nil, err
		}
		indexes := make([]gorm.Index, 0, len(rows))
		for _, row := range rows {
			var columns []string
			if err := json.Unmarshal([]byte(row.Columns), &columns); err != nil {
				return nil, err
			}
			indexes = append(indexes, &migrator.Index{TableName: table, NameValue: row.Name, ColumnList: columns, UniqueValue: sql.NullBool{Bool: row.Unique, Valid: true}, PrimaryKeyValue: sql.NullBool{Bool: row.Primary, Valid: true}})
		}
		return indexes, nil
	}
	if tx.Dialector.Name() != "sqlite" {
		return tx.Migrator().GetIndexes(model)
	}
	var rows []struct {
		Name    string
		Unique  bool
		Partial bool
	}
	if err := tx.Raw(`SELECT name, "unique", partial FROM pragma_index_list(?)`, table).Scan(&rows).Error; err != nil {
		return nil, err
	}
	indexes := make([]gorm.Index, 0, len(rows))
	for _, row := range rows {
		if row.Partial {
			continue
		}
		var columns []string
		if err := tx.Raw("SELECT name FROM pragma_index_info(?) ORDER BY seqno", row.Name).Scan(&columns).Error; err != nil {
			return nil, err
		}
		indexes = append(indexes, &migrator.Index{TableName: table, NameValue: row.Name, ColumnList: columns, UniqueValue: sql.NullBool{Bool: row.Unique, Valid: true}})
	}
	return indexes, nil
}

func storeRefundActivationNullable(tx *gorm.DB, table, name string, column gorm.ColumnType) (bool, bool) {
	if tx.Dialector.Name() != "sqlite" {
		return column.Nullable()
	}
	// The retained SQLite parser reports implicit nullable columns as NOT NULL.
	// Inspect SQLite itself, preserving the required legacy NULL semantics.
	var notNull bool
	if err := tx.Raw(`SELECT "notnull" FROM pragma_table_info(?) WHERE name = ?`, table, name).Row().Scan(&notNull); err != nil {
		return false, false
	}
	return !notNull, true
}

// Qualify the complete source registry, not just the three refund-core tables.
// The provider dispatch fence, coupon snapshots and purchase-limit columns are
// part of the same reviewed capability. Missing uniqueness must fail closed.
func storeCheckRefundActivationSchema(tx *gorm.DB) error {
	return storeCheckMerchantStoreSchema(tx, 4)
}

func storeCheckMerchantStoreSchema(tx *gorm.DB, capability int) error {
	cache := &sync.Map{}
	for _, model := range MerchantStoreModels() {
		parsed, err := schema.Parse(model, cache, schema.NamingStrategy{})
		if err != nil {
			return fmt.Errorf("%w: invalid model", ErrMerchantStoreWriterFrozen)
		}
		if (capability < 5 && storeAccessTable(parsed.Table)) || (capability < 6 && storePhaseSixTable(parsed.Table)) || (capability < 7 && storePhaseSevenTable(parsed.Table)) {
			continue
		}
		if !tx.Migrator().HasTable(model) {
			return fmt.Errorf("%w: missing table", ErrMerchantStoreWriterFrozen)
		}
		types, err := tx.Migrator().ColumnTypes(model)
		if err != nil {
			return fmt.Errorf("%w: column inspection %s", ErrMerchantStoreWriterFrozen, parsed.Table)
		}
		columns := make(map[string]gorm.ColumnType, len(types))
		for _, column := range types {
			columns[column.Name()] = column
		}
		for _, field := range parsed.Fields {
			if field.DBName == "" || (capability < 5 && storeAccessColumn(parsed.Table, field.DBName)) || (capability < 6 && storePhaseSixColumn(parsed.Table, field.DBName)) {
				continue
			}
			column, found := columns[field.DBName]
			if !found {
				return fmt.Errorf("%w: missing %s.%s", ErrMerchantStoreWriterFrozen, parsed.Table, field.DBName)
			}
			if field.NotNull {
				nullable, known := storeRefundActivationNullable(tx, parsed.Table, field.DBName, column)
				if !known || nullable {
					return fmt.Errorf("%w: nullable %s.%s", ErrMerchantStoreWriterFrozen, parsed.Table, field.DBName)
				}
			}
			if strings.EqualFold(field.TagSettings["TYPE"], "bigint") {
				kind := strings.ToLower(column.DatabaseTypeName())
				if kind != "bigint" && kind != "int8" {
					return fmt.Errorf("%w: non-bigint %s.%s", ErrMerchantStoreWriterFrozen, parsed.Table, field.DBName)
				}
			}
			if tx.Dialector.Name() == "postgres" || tx.Dialector.Name() == "mysql" {
				if field.DataType == schema.String && field.Size > 0 {
					if storePhaseSevenTable(parsed.Table) {
						kind := strings.ToLower(column.DatabaseTypeName())
						if kind != "varchar" && kind != "character varying" {
							return fmt.Errorf("%w: non-varchar %s.%s", ErrMerchantStoreWriterFrozen, parsed.Table, field.DBName)
						}
					}
					length, known := column.Length()
					if !known || length < int64(field.Size) {
						return fmt.Errorf("%w: insufficient width %s.%s", ErrMerchantStoreWriterFrozen, parsed.Table, field.DBName)
					}
				}
			}
		}
		indexes, err := storeRefundActivationIndexes(tx, model, parsed.Table)
		if err != nil {
			return fmt.Errorf("%w: index inspection %s", ErrMerchantStoreWriterFrozen, parsed.Table)
		}
		actual := make(map[string]gorm.Index, len(indexes))
		for _, index := range indexes {
			actual[index.Name()] = index
		}
		if tx.Dialector.Name() != "sqlite" {
			primaryColumns := make([]string, len(parsed.PrimaryFields))
			for i, field := range parsed.PrimaryFields {
				primaryColumns[i] = field.DBName
			}
			primaryFound := len(primaryColumns) == 0
			for _, index := range indexes {
				primary, known := index.PrimaryKey()
				if known && primary && storeSameColumnNames(index.Columns(), primaryColumns) {
					primaryFound = true
				}
			}
			if !primaryFound {
				return ErrMerchantStoreWriterFrozen
			}
		}
		for name, expected := range parsed.ParseIndexes() {
			phase5Index := false
			phase6Index := false
			for _, field := range expected.Fields {
				phase5Index = phase5Index || storeAccessColumn(parsed.Table, field.DBName)
				phase6Index = phase6Index || storePhaseSixColumn(parsed.Table, field.DBName)
			}
			if (capability < 5 && phase5Index) || (capability < 6 && phase6Index) {
				continue
			}
			index, found := actual[name]
			unique, known := false, false
			if found {
				unique, known = index.Unique()
			}
			wanted := make([]string, len(expected.Fields))
			for i, field := range expected.Fields {
				wanted[i] = field.DBName
			}
			if !found || !known || unique != strings.EqualFold(expected.Class, "UNIQUE") || !storeSameColumnNames(index.Columns(), wanted) {
				return fmt.Errorf("%w: index mismatch %s.%s", ErrMerchantStoreWriterFrozen, parsed.Table, name)
			}
		}
	}
	for _, check := range []struct {
		model any
		table string
		name  string
	}{
		{&MerchantStoreProduct{}, "merchant_store_products", "max_quantity_per_order"},
		{&MerchantStoreProduct{}, "merchant_store_products", "max_quantity_per_buyer"},
		{&MerchantStoreRefund{}, "merchant_store_refunds", "provider_refund_reference"},
	} {
		types, err := tx.Migrator().ColumnTypes(check.model)
		if err != nil {
			return ErrMerchantStoreWriterFrozen
		}
		valid := false
		for _, column := range types {
			if column.Name() == check.name {
				nullable, known := storeRefundActivationNullable(tx, check.table, check.name, column)
				valid = known && nullable
			}
		}
		if !valid {
			return fmt.Errorf("%w: legacy nullable requirement %s", ErrMerchantStoreWriterFrozen, check.name)
		}
	}
	orderTypes, err := tx.Migrator().ColumnTypes(&MerchantStoreOrder{})
	if err != nil {
		return ErrMerchantStoreWriterFrozen
	}
	for name, expected := range map[string]string{"original_price_quota": "0", "discount_quota": "0", "discount_bps": "0", "promotion_id": "", "promotion_code": ""} {
		valid := false
		for _, column := range orderTypes {
			if column.Name() != name {
				continue
			}
			value, known := column.DefaultValue()
			// PostgreSQL emits casts for string defaults; SQLite/MySQL do not.
			value = strings.SplitN(value, "::", 2)[0]
			value = strings.Trim(strings.TrimSpace(value), "()'")
			valid = known && value == expected
		}
		if !valid {
			return fmt.Errorf("%w: invalid historical default merchant_store_orders.%s", ErrMerchantStoreWriterFrozen, name)
		}
	}
	return nil
}
