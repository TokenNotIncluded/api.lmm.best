package model

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/internal/deploymentfence"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// OpenSchemaOnlyMigrationDatabase opens a private pool, without initializing
// global DB, Redis, options, user caches, workers, authorization or application
// data. A separate log database cannot participate in its one-database commit.
func OpenSchemaOnlyMigrationDatabase(ctx context.Context) (*gorm.DB, error) {
	dsn := os.Getenv("SQL_DSN")
	if os.Getenv("LOG_SQL_DSN") != "" || skip64BitQuotaSchemaCheck() || (!strings.HasPrefix(dsn, "postgres://") && !strings.HasPrefix(dsn, "postgresql://")) {
		return nil, errors.New("schema-only maintenance requires one explicit PostgreSQL primary database")
	}
	db, err := gorm.Open(postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true}), &gorm.Config{
		Logger: logger.Discard, DisableAutomaticPing: true,
	})
	if err != nil {
		return nil, errors.New("schema-only maintenance PostgreSQL configuration unavailable")
	}
	pool, err := db.DB()
	if err != nil {
		return nil, errors.New("schema-only maintenance PostgreSQL pool unavailable")
	}
	pool.SetMaxOpenConns(1)
	pool.SetMaxIdleConns(0)
	if pool.PingContext(ctx) != nil {
		_ = pool.Close()
		return nil, errors.New("schema-only maintenance PostgreSQL connection unavailable")
	}
	return db.WithContext(ctx), nil
}

// ApplySchemaOnlyMigration is the dedicated native maintenance worker. The
// controller must first qualify the retained provider's complete baseline and
// quiesce every source writer. This worker independently requires that exact
// active native owner and rejects any other PostgreSQL client or durable owner.
// All DDL, checks and transaction-level locks use the same physical connection.
// It never calls migrateDB, InitResources, authz seeding or data backfills.
func ApplySchemaOnlyMigration(ctx context.Context, db *gorm.DB, owner deploymentfence.OwnerBinding, ownerBytes []byte) error {
	parsed, err := deploymentfence.ParseSchemaMaintenanceOwner(ownerBytes)
	if err != nil || parsed != owner || db == nil || db.Dialector.Name() != "postgres" {
		return errors.New("schema-only maintenance owner or database binding is invalid")
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Bounded waits prevent an unexpected reader or lock from turning this
		// explicit maintenance window into an unbounded production outage.
		for _, statement := range []string{"SET LOCAL lock_timeout = '5s'", "SET LOCAL statement_timeout = '120s'"} {
			if err := tx.Exec(statement).Error; err != nil {
				return errors.New("schema-only transaction limits unavailable")
			}
		}
		for _, lock := range []struct {
			key    int64
			shared bool
		}{{deploymentfence.AdvisoryKey, true}, {deploymentfence.OwnerClaimKey, false}, {MigrationAdvisoryLockKey, false}} {
			function := "pg_catalog.pg_try_advisory_xact_lock"
			if lock.shared {
				function += "_shared"
			}
			var acquired bool
			if err := tx.Raw("SELECT "+function+"(?)", lock.key).Scan(&acquired).Error; err != nil || !acquired {
				return errors.New("schema-only maintenance lock is held by another owner")
			}
		}
		if err := verifySchemaOnlyMaintenanceOwner(tx, owner, ownerBytes); err != nil {
			return err
		}
		if err := check64BitQuotaSchema(tx, "postgres"); err != nil {
			return errors.New("schema-only maintenance requires the existing 64-bit quota schema")
		}
		models, err := runtimeVerificationModels(tx)
		if err != nil {
			return errors.New("schema-only maintenance model registry unavailable")
		}
		// The one reviewed change to an existing column is nullable referral
		// order linkage. Do not replay the ordinary migrator's table/index repair.
		if err := tx.Exec("ALTER TABLE referral_rewards ALTER COLUMN top_up_id DROP NOT NULL").Error; err != nil {
			return fmt.Errorf("schema-only referral nullability: %w", sanitizeDBError(err))
		}
		if err := applyMissingPostgresSchema(tx, models); err != nil {
			return err
		}
		if err := ensureCompanyBillingProfilePostgresContract(tx); err != nil {
			return fmt.Errorf("schema-only company billing constraints: %w", sanitizeDBError(err))
		}
		identity, err := loadPostgresRuntimeIdentity(tx)
		if err != nil {
			return errors.New("schema-only final PostgreSQL identity unavailable")
		}
		inventory, err := buildPostgresSchemaInventory(tx, identity.SchemaName, models)
		if err != nil {
			return errors.New("schema-only final schema inventory unavailable")
		}
		if err := verifyPostgresSchemaInventory(tx, inventory); err != nil {
			return fmt.Errorf("schema-only existing schema semantics differ: %w", sanitizeDBError(err))
		}
		// A lost holder or changed owner rolls back the DDL rather than relying
		// on an earlier preflight. No owner is created, updated or removed here.
		return verifySchemaOnlyMaintenanceOwner(tx, owner, ownerBytes)
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted, ReadOnly: false})
}

// AutoMigrate on an existing PostgreSQL table can drop/recreate valid unique
// constraints. Only genuinely new tables use it; existing tables get missing
// columns/indexes, followed by strict semantic verification in the same TX.
func applyMissingPostgresSchema(tx *gorm.DB, models []interface{}) error {
	for _, item := range models {
		statement := &gorm.Statement{DB: tx}
		if err := statement.Parse(item); err != nil {
			return errors.New("schema-only model parse failed")
		}
		if !tx.Migrator().HasTable(item) {
			if err := tx.AutoMigrate(item); err != nil {
				return fmt.Errorf("schema-only create table %s: %w", statement.Schema.Table, sanitizeDBError(err))
			}
			continue
		}
		for _, field := range statement.Schema.Fields {
			if field.DBName == "" || field.IgnoreMigration || tx.Migrator().HasColumn(item, field.DBName) {
				continue
			}
			if err := tx.Migrator().AddColumn(item, field.Name); err != nil {
				return fmt.Errorf("schema-only add column %s.%s: %w", statement.Schema.Table, field.DBName, sanitizeDBError(err))
			}
		}
		for _, index := range statement.Schema.ParseIndexes() {
			if tx.Migrator().HasIndex(item, index.Name) {
				continue
			}
			if err := tx.Migrator().CreateIndex(item, index.Name); err != nil {
				return fmt.Errorf("schema-only create index %s.%s: %w", statement.Schema.Table, index.Name, sanitizeDBError(err))
			}
		}
	}
	return nil
}

func verifySchemaOnlyMaintenanceOwner(tx *gorm.DB, owner deploymentfence.OwnerBinding, ownerBytes []byte) error {
	identity, err := loadPostgresRuntimeIdentity(tx)
	if err != nil || verifyPostgresRuntimeIdentity(identity) != nil || identity.DatabaseName != owner.Database || identity.SchemaName != owner.Schema || identity.DatabaseUser != owner.Role {
		return errors.New("schema-only PostgreSQL runtime identity differs from native owner")
	}
	var physical struct {
		SystemIdentifier string
		DatabaseOID      int64
		SchemaOID        int64
	}
	if err := tx.Raw(`SELECT control.system_identifier::pg_catalog.text AS system_identifier,
		databases.oid::pg_catalog.int8 AS database_oid, namespaces.oid::pg_catalog.int8 AS schema_oid
		FROM pg_catalog.pg_control_system() AS control, pg_catalog.pg_database AS databases, pg_catalog.pg_namespace AS namespaces
		WHERE databases.datname OPERATOR(pg_catalog.=) pg_catalog.current_database()
		AND namespaces.nspname OPERATOR(pg_catalog.=) pg_catalog.current_schema()`).Scan(&physical).Error; err != nil ||
		physical.SystemIdentifier != owner.SystemIdentifier || physical.DatabaseOID != owner.DatabaseOID || physical.SchemaOID != owner.SchemaOID {
		return errors.New("schema-only physical PostgreSQL identity differs from native owner")
	}
	var owners []Option
	// SHARE prevents the holder's exact CAS release during this DDL transaction;
	// the owner-claim lock prevents another deployment adding a second owner.
	if err := tx.Raw("SELECT key, value FROM options WHERE " + deploymentfence.PostgreSQLPresencePredicate + " FOR SHARE").Scan(&owners).Error; err != nil ||
		len(owners) != 1 || owners[0].Key != deploymentfence.OptionPrefix+owner.DeploymentID+":"+owner.Host || !bytes.Equal([]byte(owners[0].Value), ownerBytes) {
		return errors.New("schema-only exact sole durable maintenance owner is unavailable")
	}
	var holderAlive bool
	if err := tx.Raw(`SELECT EXISTS (
		SELECT 1 FROM pg_catalog.pg_stat_activity AS activity JOIN pg_catalog.pg_locks AS locks ON locks.pid = activity.pid
		WHERE activity.pid OPERATOR(pg_catalog.=) ? AND activity.pid OPERATOR(pg_catalog.<>) pg_catalog.pg_backend_pid()
		AND activity.datid OPERATOR(pg_catalog.=) ?
		AND activity.usename OPERATOR(pg_catalog.=) ? AND activity.backend_type OPERATOR(pg_catalog.=) 'client backend'
		AND locks.locktype OPERATOR(pg_catalog.=) 'advisory' AND locks.mode OPERATOR(pg_catalog.=) 'ShareLock' AND locks.granted
		AND locks.database OPERATOR(pg_catalog.=) ? AND locks.classid::pg_catalog.int8 OPERATOR(pg_catalog.=) ?
		AND locks.objid::pg_catalog.int8 OPERATOR(pg_catalog.=) ? AND locks.objsubid OPERATOR(pg_catalog.=) 1)`,
		owner.BackendPID, owner.DatabaseOID, owner.Role, owner.DatabaseOID,
		int64(uint64(deploymentfence.AdvisoryKey)>>32), int64(uint64(deploymentfence.AdvisoryKey)&0xffffffff)).Scan(&holderAlive).Error; err != nil || !holderAlive {
		return errors.New("schema-only native owner lost its real PostgreSQL shared fence session")
	}
	var otherClients int64
	if err := tx.Raw(`SELECT pg_catalog.count(*) FROM pg_catalog.pg_stat_activity
		WHERE datid OPERATOR(pg_catalog.=) ? AND (backend_type OPERATOR(pg_catalog.=) 'client backend' OR backend_type IS NULL)
		AND pid OPERATOR(pg_catalog.<>) pg_catalog.pg_backend_pid() AND pid OPERATOR(pg_catalog.<>) ?`, owner.DatabaseOID, owner.BackendPID).
		Scan(&otherClients).Error; err != nil || otherClients != 0 {
		return errors.New("schema-only maintenance found another PostgreSQL client; all writers must remain quiesced")
	}
	return nil
}
