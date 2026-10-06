package model

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/internal/credittransition"
	"gorm.io/gorm"
)

type creditPreparationColumn struct {
	Table    string
	Name     string
	Type     string
	Nullable string
	Default  string
	DDL      string
}

var creditPreparationColumns = []creditPreparationColumn{
	{"top_ups", "pending_credit_rebase_key", "character varying", "NO", "''::character varying", "character varying(128) NOT NULL DEFAULT ''"},
	{"top_ups", "pending_credit_rebase_original_quota", "bigint", "NO", "0", "bigint NOT NULL DEFAULT 0"},
	{"top_ups", "pending_credit_rebase_effective_quota", "bigint", "NO", "0", "bigint NOT NULL DEFAULT 0"},
	{"user_subscriptions", "reset_amount", "bigint", "YES", "", "bigint"},
	{"user_subscriptions", "renewal_amount", "bigint", "YES", "", "bigint"},
}

// OpenCreditPreparationDB intentionally avoids DB/LOG_DB globals and the normal
// startup migration session. There is no setup, catalog, option, auth, cache or
// callback initialization on this connection.
func OpenCreditPreparationDB(ctx context.Context, config credittransition.Config) (*gorm.DB, error) {
	common.ClearCreditsPerUSD()
	if err := config.Validate(); err != nil {
		return nil, err
	}
	dsn := os.Getenv("SQL_DSN")
	if !strings.HasPrefix(dsn, "postgres://") && !strings.HasPrefix(dsn, "postgresql://") {
		return nil, errors.New("sealed credit preparation requires PostgreSQL SQL_DSN")
	}
	db, kind, err := chooseDB("SQL_DSN", false)
	if err != nil {
		return nil, fmt.Errorf("open credit preparation database: %w", err)
	}
	if kind != common.DatabaseTypePostgreSQL {
		return nil, errors.New("credit preparation is PostgreSQL-only")
	}
	pool, err := db.DB()
	if err != nil {
		return nil, err
	}
	pool.SetMaxOpenConns(1)
	pool.SetMaxIdleConns(1)
	pool.SetConnMaxLifetime(time.Minute)
	if err := pool.PingContext(ctx); err != nil {
		return nil, errors.Join(err, pool.Close())
	}
	return db, nil
}

// ApplyCreditPreparationSchema adds only the five explicitly listed dormant
// columns, in one transaction. It cannot update options or any financial row.
func ApplyCreditPreparationSchema(ctx context.Context, db *gorm.DB, config credittransition.Config) error {
	if err := config.Validate(); err != nil {
		return err
	}
	if db == nil || db.Dialector.Name() != "postgres" {
		return errors.New("credit preparation is PostgreSQL-only")
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SET LOCAL lock_timeout = '10s'").Error; err != nil {
			return err
		}
		if err := tx.Exec("SET LOCAL statement_timeout = '30s'").Error; err != nil {
			return err
		}
		if err := verifyCreditPreparationState(tx, config, false); err != nil {
			return err
		}
		// The schema is validated as a plain identifier by Config.Validate. All
		// table/column names and type expressions below are compile-time constants.
		for _, column := range creditPreparationColumns {
			statement := "ALTER TABLE " + quotedPreparationTable(config.Database.Schema, column.Table) +
				" ADD COLUMN IF NOT EXISTS \"" + column.Name + "\" " + column.DDL
			if err := tx.Exec(statement).Error; err != nil {
				return fmt.Errorf("prepare %s.%s: %w", column.Table, column.Name, err)
			}
		}
		return verifyCreditPreparationState(tx, config, true)
	}, &sql.TxOptions{Isolation: sql.LevelSerializable})
}

// VerifyCreditPreparation is a read-only health/migration gate. Changed options,
// database identity, non-dormant new fields or financial audits close readiness.
func VerifyCreditPreparation(ctx context.Context, db *gorm.DB, config credittransition.Config) error {
	if err := config.Validate(); err != nil {
		return err
	}
	if db == nil || db.Dialector.Name() != "postgres" {
		return errors.New("credit preparation is PostgreSQL-only")
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return verifyCreditPreparationState(tx, config, true)
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
}

func quotedPreparationTable(schema, table string) string { return `"` + schema + `"."` + table + `"` }

func verifyCreditPreparationState(tx *gorm.DB, config credittransition.Config, requireColumns bool) error {
	var identity credittransition.DatabaseIdentity
	if err := tx.Raw(`SELECT c.system_identifier::text AS system_identifier,
		pg_catalog.current_database() AS database,
		(SELECT oid::bigint FROM pg_catalog.pg_database WHERE datname = pg_catalog.current_database()) AS database_oid,
		pg_catalog.current_schema() AS schema,
		pg_catalog.current_setting('server_version_num')::integer AS server_version_num,
		CURRENT_USER AS database_user FROM pg_catalog.pg_control_system() c`).Scan(&identity).Error; err != nil {
		return fmt.Errorf("read sealed PostgreSQL identity: %w", err)
	}
	if identity != config.Database {
		return errors.New("sealed credit preparation PostgreSQL identity mismatch")
	}
	var options []Option
	if err := tx.Table(quotedPreparationTable(config.Database.Schema, "options")).Select("key, value").Where("key IN ?", credittransition.OptionKeys).Find(&options).Error; err != nil {
		return err
	}
	if len(options) != len(credittransition.OptionKeys) {
		return errors.New("sealed credit preparation option coverage mismatch")
	}
	for _, option := range options {
		if expected, exists := config.Options[option.Key]; !exists || option.Value != expected {
			return fmt.Errorf("sealed credit preparation option %s changed", option.Key)
		}
	}
	for _, table := range []string{"wallet_credit_rebases", "wallet_topup_credit_rebases", "wallet_referral_credit_rebases"} {
		var exists bool
		if err := tx.Raw("SELECT pg_catalog.to_regclass(?) IS NOT NULL", quotedPreparationTable(config.Database.Schema, table)).Scan(&exists).Error; err != nil {
			return err
		}
		if exists {
			var count int64
			if err := tx.Table(quotedPreparationTable(config.Database.Schema, table)).Count(&count).Error; err != nil {
				return err
			}
			if count != 0 {
				return errors.New("financial audit exists; preparation cannot serve this database")
			}
		}
	}
	for _, column := range creditPreparationColumns {
		var actual []struct {
			DataType               string
			IsNullable             string
			ColumnDefault          *string
			CharacterMaximumLength *int64
		}
		if err := tx.Raw(`SELECT data_type, is_nullable, column_default, character_maximum_length
			FROM information_schema.columns WHERE table_schema = ? AND table_name = ? AND column_name = ?`,
			config.Database.Schema, column.Table, column.Name).Scan(&actual).Error; err != nil {
			return err
		}
		if len(actual) == 0 && !requireColumns {
			continue
		}
		if len(actual) != 1 {
			return fmt.Errorf("missing preparation column %s.%s", column.Table, column.Name)
		}
		got := actual[0]
		value := ""
		if got.ColumnDefault != nil {
			value = *got.ColumnDefault
		}
		if got.DataType != column.Type || got.IsNullable != column.Nullable || value != column.Default ||
			(column.Name == "pending_credit_rebase_key" && (got.CharacterMaximumLength == nil || *got.CharacterMaximumLength != 128)) {
			return fmt.Errorf("incompatible preparation column %s.%s", column.Table, column.Name)
		}
		condition := `"` + column.Name + `" IS NOT NULL`
		if column.Nullable == "NO" {
			condition = `"` + column.Name + `" <> ` + strings.TrimSuffix(column.Default, "::character varying")
		}
		var active int64
		if err := tx.Table(quotedPreparationTable(config.Database.Schema, column.Table)).Where(condition).Count(&active).Error; err != nil {
			return err
		}
		if active != 0 {
			return errors.New("preparation fields are no longer dormant; use canonical runtime")
		}
	}
	return nil
}
