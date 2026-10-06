package model

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/internal/deploymentfence"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

const MerchantStoreWriterCapability = 5
const MerchantStoreWriterCapabilityOption = "MerchantStoreMinimumWriterCapability"

var ErrMerchantStoreWriterFrozen = errors.New("merchant store writer is unavailable during an upgrade")
var ErrMerchantStoreWriterGateReserved = errors.New("merchant store writer capability is reserved for the reviewed operator command")

type MerchantStoreWriterGateStatus struct {
	RequiredCapability int  `json:"required_capability"`
	WriterCapability   int  `json:"writer_capability"`
	NewWritesAllowed   bool `json:"new_writes_allowed"`
	SupportsWriterGate bool `json:"supports_writer_gate"`
	SupportsVariants   bool `json:"supports_variants"`
}

func storeWriterGateRow(db *gorm.DB, lock string) (int, error) {
	if db == nil {
		return 0, ErrMerchantStoreWriterFrozen
	}
	var option Option
	// Callers include GORM save hooks and scoped product queries. Build a fresh
	// statement on the same transaction/physical connection, so product WHERE
	// clauses and pending INSERT/UPDATE values cannot contaminate this read.
	q := db.Session(&gorm.Session{NewDB: true}).Model(&Option{})
	if lock != "" && db.Dialector.Name() != "sqlite" {
		q = q.Clauses(clause.Locking{Strength: lock})
	}
	if err := q.Where("key = ?", MerchantStoreWriterCapabilityOption).First(&option).Error; err != nil {
		return 0, ErrMerchantStoreWriterFrozen
	}
	if option.Key != MerchantStoreWriterCapabilityOption {
		return 0, ErrMerchantStoreWriterFrozen
	}
	switch option.Value {
	case "1":
		return 1, nil
	case "2":
		return 2, nil
	case "3":
		return 3, nil
	case "4":
		return 4, nil
	case "5":
		return 5, nil
	default:
		return 0, ErrMerchantStoreWriterFrozen
	}
}

// This database read deliberately ignores OptionMap/Redis. The SHARE lock is
// retained by the caller's business transaction until commit or rollback.
func storeRequireWriter(tx *gorm.DB) error {
	required, err := storeWriterGateRow(tx, "SHARE")
	if err != nil || required > MerchantStoreWriterCapability {
		return ErrMerchantStoreWriterFrozen
	}
	return nil
}

// Retirement must stay unavailable while older writers can still recreate a
// deleted listing. Operators enable it only after every serving writer is ready.
func storeRequireLifecycleWriter(tx *gorm.DB) error {
	required, err := storeWriterGateRow(tx, "SHARE")
	if err != nil || required < 3 || required > MerchantStoreWriterCapability {
		return ErrMerchantStoreWriterFrozen
	}
	return nil
}

func storeReservedWriterOptionKey(key string) bool {
	return strings.EqualFold(strings.TrimSpace(key), MerchantStoreWriterCapabilityOption) || deploymentfence.ReservedOptionKey(key)
}

// MySQL may use an accent/case-insensitive key collation. Check the actual
// matched stored key as well as spelling before a generic upsert can alias it.
func storeRejectReservedOptionAlias(tx *gorm.DB, key string) error {
	if storeReservedWriterOptionKey(key) {
		return ErrMerchantStoreWriterGateReserved
	}
	if tx.Dialector.Name() != "mysql" {
		return nil
	}
	var existing Option
	err := tx.Select("key").Where("key = ?", key).First(&existing).Error
	if err == nil && storeReservedWriterOptionKey(existing.Key) {
		return ErrMerchantStoreWriterGateReserved
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	return nil
}

func GetMerchantStoreWriterGateStatus(db *gorm.DB) (MerchantStoreWriterGateStatus, error) {
	required, err := storeWriterGateRow(db, "")
	return MerchantStoreWriterGateStatus{RequiredCapability: required, WriterCapability: MerchantStoreWriterCapability, NewWritesAllowed: err == nil && required <= MerchantStoreWriterCapability, SupportsWriterGate: true, SupportsVariants: MerchantStoreWriterCapability >= 2}, err
}

// Only the explicitly invoked private operator bootstrap may create this row.
// Runtime and AutoMigrate never repair a missing row. Once variant schema is
// present, a missing gate cannot be recreated at capability 1.
func BootstrapMerchantStoreWriterGate(db *gorm.DB) error {
	if db == nil {
		return ErrMerchantStoreWriterFrozen
	}
	return withMerchantStoreActivationDB(db, func(bound *gorm.DB) error {
		return bound.Transaction(func(tx *gorm.DB) error {
			var count int64
			if err := tx.Model(&Option{}).Where("key = ?", MerchantStoreWriterCapabilityOption).Count(&count).Error; err != nil {
				return ErrMerchantStoreWriterFrozen
			}
			if count != 0 {
				_, err := storeWriterGateRow(tx, "SHARE")
				return err // Existing 1 or 2 is preserved, never reset or upgraded.
			}
			if tx.Migrator().HasTable("merchant_store_variants") || tx.Migrator().HasColumn("merchant_store_stocks", "variant_id") || tx.Migrator().HasColumn("merchant_store_orders", "variant_id") {
				return ErrMerchantStoreWriterFrozen
			}
			row := Option{Key: MerchantStoreWriterCapabilityOption, Value: "1"}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
				return ErrMerchantStoreWriterFrozen
			}
			_, err := storeWriterGateRow(tx, "SHARE")
			return err
		})
	})
}

func checkMerchantStoreWriterMigration(db *gorm.DB) error {
	if !db.Migrator().HasTable(&Option{}) {
		return nil // A fresh explicit apply creates Options, but never the gate.
	}
	var count int64
	if err := db.Model(&Option{}).Where("key = ?", MerchantStoreWriterCapabilityOption).Count(&count).Error; err != nil {
		return ErrMerchantStoreWriterFrozen
	}
	if count == 0 {
		return nil // Initial shim apply only; ordinary writes still fail closed.
	}
	required, err := storeWriterGateRow(db, "")
	if err != nil || required > MerchantStoreWriterCapability {
		return ErrMerchantStoreWriterFrozen
	}
	return nil
}

// Deployment's fence is acquired before startup migration's existing advisory
// lock. Durable owner records survive a lost deployment session and block every
// activation; no state or expiry value can silently release an unknown owner.
func withMerchantStoreActivationDB(db *gorm.DB, run func(*gorm.DB) error) (err error) {
	if db.Dialector.Name() != "postgres" {
		var keys []string
		if err := db.Model(&Option{}).Pluck("key", &keys).Error; err != nil {
			return ErrMerchantStoreWriterFrozen
		}
		for _, key := range keys {
			if deploymentfence.ReservedOptionKey(key) {
				return ErrMerchantStoreWriterFrozen
			}
		}
		return run(db)
	}
	return storeWithPostgresActivationSession(db, run)
}

// All gate operations use one physical session. Unlike startup DDL, no work is
// handed to a second pool connection, including Unix identity verification.
func storeWithPostgresActivationSession(db *gorm.DB, run func(*gorm.DB) error) (err error) {
	ctx := db.Statement.Context
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	pool, err := db.DB()
	if err != nil {
		return ErrMerchantStoreWriterFrozen
	}
	conn, err := pool.Conn(ctx)
	if err != nil {
		return ErrMerchantStoreWriterFrozen
	}
	fenceAcquired, migrationAcquired := false, false
	defer func() {
		// Reverse lock order; a failed release discards the physical connection,
		// releasing both locks instead of returning a locked session to the pool.
		for _, lock := range []struct {
			acquired bool
			key      int64
		}{{migrationAcquired, MigrationAdvisoryLockKey}, {fenceAcquired, deploymentfence.AdvisoryKey}} {
			if !lock.acquired {
				continue
			}
			// Caller cancellation must not prevent releasing either lock.
			releaseCtx, stop := context.WithTimeout(context.Background(), 2*time.Second)
			var unlocked bool
			unlockErr := conn.QueryRowContext(releaseCtx, "SELECT pg_catalog.pg_advisory_unlock($1)", lock.key).Scan(&unlocked)
			stop()
			if unlockErr != nil || !unlocked {
				// Returning an unsuccessfully unlocked session to the pool would
				// leave its advisory lock alive; discard that physical connection.
				_ = conn.Raw(func(any) error { return driver.ErrBadConn })
				err = errors.Join(err, ErrMerchantStoreWriterFrozen)
				break
			}
		}
		if closeErr := conn.Close(); closeErr != nil && !errors.Is(closeErr, sql.ErrConnDone) {
			err = errors.Join(err, ErrMerchantStoreWriterFrozen)
		}
	}()
	if _, err = storePostgresGateIdentity(ctx, conn); err != nil {
		return ErrMerchantStoreWriterFrozen
	}
	if err = conn.QueryRowContext(ctx, "SELECT pg_catalog.pg_try_advisory_lock($1)", deploymentfence.AdvisoryKey).Scan(&fenceAcquired); err != nil || !fenceAcquired {
		return ErrMerchantStoreWriterFrozen
	}
	var hasOwner bool
	if err = conn.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM options WHERE "+deploymentfence.PostgreSQLPresencePredicate+")").Scan(&hasOwner); err != nil || hasOwner {
		return ErrMerchantStoreWriterFrozen
	}
	if err = conn.QueryRowContext(ctx, "SELECT pg_catalog.pg_try_advisory_lock($1)", MigrationAdvisoryLockKey).Scan(&migrationAcquired); err != nil || !migrationAcquired {
		return ErrMerchantStoreWriterFrozen
	}
	bound := db.Session(&gorm.Session{NewDB: true, Initialized: true}).WithContext(ctx)
	bound.Statement.ConnPool = conn
	return run(bound)
}

func storePostgresGateIdentity(ctx context.Context, reader postgresMigrationIdentityReader) (postgresDatabaseIdentity, error) {
	var address sql.NullString
	var port sql.NullInt64
	var identity postgresDatabaseIdentity
	if err := reader.QueryRowContext(ctx, `
		SELECT pg_catalog.inet_server_addr()::pg_catalog.text,
		       pg_catalog.inet_server_port()::pg_catalog.int8,
		       pg_catalog.current_database(), database_meta.oid::pg_catalog.int8
		FROM pg_catalog.pg_database AS database_meta
		WHERE database_meta.datname OPERATOR(pg_catalog.=) pg_catalog.current_database()`).Scan(&address, &port, &identity.DatabaseName, &identity.DatabaseOID); err != nil {
		return identity, err
	}
	if address.Valid != port.Valid {
		return identity, ErrMerchantStoreWriterFrozen
	}
	if address.Valid {
		identity.ServerAddress, identity.ServerPort = address.String, port.Int64
	} else {
		identity.UnixSocket = true
		if err := reader.QueryRowContext(ctx, "SELECT system_identifier::pg_catalog.text FROM pg_catalog.pg_control_system()").Scan(&identity.SystemIdentifier); err != nil {
			return identity, err
		}
	}
	return identity, validatePostgresDatabaseIdentity(identity)
}

// This has no HTTP route. The caller is the explicitly invoked local operator
// command using its private primary-database credentials. It never auto-migrates.
func ActivateMerchantStoreVariants(db *gorm.DB, expected int) error {
	if db == nil || (expected != 1 && expected != 2) {
		return ErrMerchantStoreWriterFrozen
	}
	return withMerchantStoreActivationDB(db, func(bound *gorm.DB) error {
		return bound.Transaction(func(tx *gorm.DB) error {
			required, err := storeWriterGateRow(tx, "UPDATE")
			if err != nil {
				return err
			}
			if required == 2 {
				return nil // Exact activation retry is idempotent, never a downgrade.
			}
			if required != expected {
				return ErrMerchantStoreWriterFrozen
			}
			for _, entry := range []struct{ Table, Column string }{
				{"merchant_store_variants", "id"}, {"merchant_store_variants", "product_id"},
				{"merchant_store_variants", "name"}, {"merchant_store_variants", "price_quota"},
				{"merchant_store_variants", "template"}, {"merchant_store_variants", "enabled"},
				{"merchant_store_stocks", "variant_id"}, {"merchant_store_orders", "variant_id"}, {"merchant_store_orders", "variant_name"},
			} {
				if !tx.Migrator().HasColumn(entry.Table, entry.Column) {
					return ErrMerchantStoreWriterFrozen
				}
			}
			result := tx.Model(&Option{}).Where("key = ? AND value = ?", MerchantStoreWriterCapabilityOption, "1").Update("value", "2")
			if result.Error != nil || result.RowsAffected != 1 {
				return ErrMerchantStoreWriterFrozen
			}
			return nil
		})
	})
}

// ActivateMerchantStoreProductLifecycle raises the reviewed writer floor only;
// it performs no DDL and never skips the variant readiness stage or downgrades.
func ActivateMerchantStoreProductLifecycle(db *gorm.DB, expected int) error {
	if db == nil || (expected != 2 && expected != 3) || MerchantStoreWriterCapability < 3 {
		return ErrMerchantStoreWriterFrozen
	}
	return withMerchantStoreActivationDB(db, func(bound *gorm.DB) error {
		return bound.Transaction(func(tx *gorm.DB) error {
			required, err := storeWriterGateRow(tx, "UPDATE")
			if err != nil {
				return err
			}
			if required == 3 {
				return nil
			}
			if required != expected || required != 2 || !tx.Migrator().HasColumn("merchant_store_products", "status") {
				return ErrMerchantStoreWriterFrozen
			}
			result := tx.Model(&Option{}).Where("key = ? AND value = ?", MerchantStoreWriterCapabilityOption, "2").Update("value", "3")
			if result.Error != nil || result.RowsAffected != 1 {
				return ErrMerchantStoreWriterFrozen
			}
			return nil
		})
	})
}

// Gate operations require an explicit target and do not initialize Options,
// Redis, server resources, jobs, log databases, or migrations.
func OpenMerchantStoreWriterGateDatabase(ctx context.Context) (*gorm.DB, error) {
	dsn := strings.TrimSpace(os.Getenv("SQL_DSN"))
	if dsn == "" || strings.HasPrefix(dsn, "local") || isClickHouseDSN(dsn) {
		return nil, ErrMerchantStoreWriterFrozen
	}
	config := newGormConfig(true)
	config.Logger = logger.Discard // Connection errors must never print credentials.
	config.DisableAutomaticPing = true
	var db *gorm.DB
	var err error
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		db, err = gorm.Open(postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true}), config)
	} else {
		db, err = gorm.Open(mysql.New(mysql.Config{DSN: dsn, SkipInitializeWithVersion: true}), config)
	}
	if err != nil {
		if db != nil {
			_ = closeDB(db)
		}
		return nil, ErrMerchantStoreWriterFrozen
	}
	pool, err := db.DB()
	if err != nil || pool.PingContext(ctx) != nil {
		_ = closeDB(db)
		return nil, ErrMerchantStoreWriterFrozen
	}
	return db.WithContext(ctx), nil
}
