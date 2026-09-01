package model

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
)

const (
	dbMigrationModeEnv = "LMM_DB_MIGRATION_MODE"

	DBMigrationModeApply  DBMigrationMode = "apply"
	DBMigrationModeVerify DBMigrationMode = "verify"

	// MigrationAdvisoryLockKey is the shared Go/Rust PostgreSQL startup-migration lock contract.
	MigrationAdvisoryLockKey int64 = 0x4c4d4d4150490001
)

type DBMigrationMode string

func databaseMigrationModeFromEnv() (DBMigrationMode, error) {
	raw, set := os.LookupEnv(dbMigrationModeEnv)
	if !set {
		return DBMigrationModeApply, nil
	}
	switch DBMigrationMode(raw) {
	case DBMigrationModeApply, DBMigrationModeVerify:
		return DBMigrationMode(raw), nil
	default:
		return "", fmt.Errorf("%s must be exactly apply or verify", dbMigrationModeEnv)
	}
}

func validatePrimaryMigrationDSNBeforeOpen(mode DBMigrationMode, dsn string) error {
	if mode != DBMigrationModeVerify {
		return nil
	}
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		return nil
	}
	return errors.New("LMM_DB_MIGRATION_MODE=verify requires SQL_DSN to use postgres:// or postgresql://")
}

type migrationAdvisoryLock interface {
	Identity() postgresDatabaseIdentity
	Acquire() error
	Release() error
}

type postgresMigrationLock struct {
	conn     *sql.Conn
	identity postgresDatabaseIdentity
	acquired bool
}

type postgresDatabaseIdentity struct {
	ServerAddress string
	ServerPort    int64
	DatabaseName  string
	DatabaseOID   int64
}

func openPostgresMigrationLock(db *gorm.DB) (migrationAdvisoryLock, error) {
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("open PostgreSQL migration lock pool: %w", err)
	}
	conn, err := sqlDB.Conn(context.Background())
	if err != nil {
		return nil, fmt.Errorf("open PostgreSQL migration lock session: %w", err)
	}
	lock := &postgresMigrationLock{conn: conn}
	if err := conn.QueryRowContext(context.Background(), `
		SELECT pg_catalog.inet_server_addr()::pg_catalog.text,
		       pg_catalog.inet_server_port()::pg_catalog.int8,
		       pg_catalog.current_database(), database_meta.oid::pg_catalog.int8
		FROM pg_catalog.pg_database AS database_meta
		WHERE database_meta.datname OPERATOR(pg_catalog.=) pg_catalog.current_database()`).
		Scan(&lock.identity.ServerAddress, &lock.identity.ServerPort,
			&lock.identity.DatabaseName, &lock.identity.DatabaseOID); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("identify PostgreSQL migration lock database: %w", err)
	}
	if err := validatePostgresDatabaseIdentity(lock.identity); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return lock, nil
}

func validatePostgresDatabaseIdentity(identity postgresDatabaseIdentity) error {
	if identity.ServerAddress == "" || identity.ServerPort <= 0 || identity.ServerPort > 65535 ||
		identity.DatabaseName == "" || identity.DatabaseOID <= 0 {
		return errors.New("PostgreSQL migration lock database identity is incomplete or ambiguous")
	}
	return nil
}

func (lock *postgresMigrationLock) Identity() postgresDatabaseIdentity {
	if lock == nil {
		return postgresDatabaseIdentity{}
	}
	return lock.identity
}

func (lock *postgresMigrationLock) Acquire() error {
	if lock == nil || lock.conn == nil || lock.acquired {
		return errors.New("PostgreSQL migration lock session is not available")
	}
	var locked bool
	if err := lock.conn.QueryRowContext(context.Background(),
		"SELECT pg_catalog.pg_try_advisory_lock($1)", MigrationAdvisoryLockKey).Scan(&locked); err != nil {
		return fmt.Errorf("acquire PostgreSQL migration advisory lock: %w", err)
	}
	if !locked {
		return errors.New("PostgreSQL migration advisory lock is held by another startup")
	}
	lock.acquired = true
	return nil
}

func (lock *postgresMigrationLock) Release() error {
	if lock == nil || lock.conn == nil {
		return nil
	}
	conn := lock.conn
	lock.conn = nil
	if !lock.acquired {
		return conn.Close()
	}
	lock.acquired = false
	var unlocked bool
	unlockErr := conn.QueryRowContext(context.Background(),
		"SELECT pg_catalog.pg_advisory_unlock($1)", MigrationAdvisoryLockKey).Scan(&unlocked)
	if unlockErr == nil && !unlocked {
		unlockErr = errors.New("PostgreSQL migration advisory lock was not owned by this startup")
	}
	return errors.Join(unlockErr, conn.Close())
}

type StartupMigrationSession struct {
	mode        DBMigrationMode
	locks       []migrationAdvisoryLock
	lockFactory func(*gorm.DB) (migrationAdvisoryLock, error)
	mu          sync.Mutex
	closed      bool
}

func newStartupMigrationSession(mode DBMigrationMode) *StartupMigrationSession {
	return &StartupMigrationSession{mode: mode, lockFactory: openPostgresMigrationLock}
}

func (session *StartupMigrationSession) Applies() bool {
	return session != nil && session.mode == DBMigrationModeApply
}

func (session *StartupMigrationSession) acquirePrimaryLock(db *gorm.DB, databaseType common.DatabaseType) error {
	if session == nil {
		return errors.New("database migration session is missing")
	}
	if session.mode == DBMigrationModeVerify && databaseType != common.DatabaseTypePostgreSQL {
		return errors.New("LMM_DB_MIGRATION_MODE=verify requires PostgreSQL as the primary database")
	}
	if databaseType != common.DatabaseTypePostgreSQL {
		return nil
	}
	return session.acquirePostgresLock(db)
}

func (session *StartupMigrationSession) acquireLogLock(db *gorm.DB, databaseType common.DatabaseType) error {
	if session == nil {
		return errors.New("database migration session is missing")
	}
	if databaseType != common.DatabaseTypePostgreSQL {
		return nil
	}
	return session.acquirePostgresLock(db)
}

func (session *StartupMigrationSession) acquirePostgresLock(db *gorm.DB) error {
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.closed {
		return errors.New("database migration session is already closed")
	}
	lock, err := session.lockFactory(db)
	if err != nil {
		return err
	}
	if lock == nil {
		return errors.New("PostgreSQL migration lock session is missing")
	}
	identity := lock.Identity()
	if err := validatePostgresDatabaseIdentity(identity); err != nil {
		return errors.Join(err, lock.Release())
	}
	for _, held := range session.locks {
		same, err := comparePostgresDatabaseIdentity(held.Identity(), identity)
		if err != nil {
			return errors.Join(err, lock.Release())
		}
		if same {
			return lock.Release()
		}
	}
	if err := lock.Acquire(); err != nil {
		return errors.Join(err, lock.Release())
	}
	session.locks = append(session.locks, lock)
	return nil
}

func comparePostgresDatabaseIdentity(left, right postgresDatabaseIdentity) (bool, error) {
	if err := validatePostgresDatabaseIdentity(left); err != nil {
		return false, err
	}
	if err := validatePostgresDatabaseIdentity(right); err != nil {
		return false, err
	}
	sameServer := left.ServerAddress == right.ServerAddress && left.ServerPort == right.ServerPort
	if !sameServer {
		return false, nil
	}
	sameName := left.DatabaseName == right.DatabaseName
	sameOID := left.DatabaseOID == right.DatabaseOID
	if sameName && sameOID {
		return true, nil
	}
	if sameName || sameOID {
		return false, errors.New("PostgreSQL migration lock database identity changed during startup")
	}
	return false, nil
}

func (session *StartupMigrationSession) runPrimaryPhase(
	db *gorm.DB,
	databaseType common.DatabaseType,
	apply func() error,
	verify func() error,
) error {
	if err := session.acquirePrimaryLock(db, databaseType); err != nil {
		return session.closeOnFailure(err)
	}
	var err error
	if session.Applies() {
		err = apply()
	} else {
		err = verify()
	}
	if err != nil {
		return session.closeOnFailure(err)
	}
	return nil
}

func (session *StartupMigrationSession) runLogPhase(
	db *gorm.DB,
	databaseType common.DatabaseType,
	apply func() error,
	verify func() error,
) error {
	if err := session.acquireLogLock(db, databaseType); err != nil {
		return session.closeOnFailure(err)
	}
	var err error
	if session.Applies() {
		err = apply()
	} else {
		err = verify()
	}
	if err != nil {
		return session.closeOnFailure(err)
	}
	return nil
}

func (session *StartupMigrationSession) closeOnFailure(err error) error {
	return errors.Join(err, session.Close())
}

func (session *StartupMigrationSession) Close() error {
	if session == nil {
		return nil
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.closed {
		return nil
	}
	session.closed = true
	var releaseErr error
	for index := len(session.locks) - 1; index >= 0; index-- {
		releaseErr = errors.Join(releaseErr, session.locks[index].Release())
	}
	session.locks = nil
	return releaseErr
}

type postgresRuntimeIdentity struct {
	DatabaseName         string
	SchemaName           string
	DatabaseUser         string
	ServerVersion        int64
	ConfiguredSearchPath string
	EffectiveSearchPath  string
}

func verifyPostgresRuntimeAndSchema(db *gorm.DB) error {
	identity, err := loadPostgresRuntimeIdentity(db)
	if err != nil {
		return err
	}
	if err := verifyPostgresRuntimeIdentity(identity); err != nil {
		return err
	}
	requiredModels := append(mainMigrationModels(), &SubscriptionPlan{})
	inventory, err := buildPostgresSchemaInventory(db, identity.SchemaName, requiredModels)
	if err != nil {
		return err
	}
	if err := verifyPostgresSchemaInventory(db, inventory); err != nil {
		return err
	}
	return verifyPostgresMigrationPostconditions(db, identity.SchemaName)
}

func loadPostgresRuntimeIdentity(db *gorm.DB) (postgresRuntimeIdentity, error) {
	var identity postgresRuntimeIdentity
	if err := db.Raw(`SELECT pg_catalog.current_database() AS database_name,
		pg_catalog.current_schema() AS schema_name, CURRENT_USER AS database_user,
		pg_catalog.current_setting('server_version_num')::pg_catalog.int8 AS server_version,
		pg_catalog.current_setting('search_path') AS configured_search_path,
		pg_catalog.array_to_string(pg_catalog.current_schemas(true), ',') AS effective_search_path`).Scan(&identity).Error; err != nil {
		return postgresRuntimeIdentity{}, fmt.Errorf("verify PostgreSQL runtime identity: %w", err)
	}
	return identity, nil
}

func verifyPostgresRuntimeIdentity(identity postgresRuntimeIdentity) error {
	if identity.DatabaseName == "" || identity.SchemaName == "" || identity.DatabaseUser == "" || identity.ServerVersion <= 0 {
		return errors.New("PostgreSQL runtime identity is incomplete")
	}
	if !isSafePostgresApplicationSchema(identity.SchemaName) {
		return fmt.Errorf("PostgreSQL application schema %q is not a safe unquoted identifier", identity.SchemaName)
	}
	configured, err := normalizePostgresSearchPath(identity.ConfiguredSearchPath)
	if err != nil {
		return fmt.Errorf("normalize configured PostgreSQL search_path: %w", err)
	}
	effective, err := normalizePostgresSearchPath(identity.EffectiveSearchPath)
	if err != nil {
		return fmt.Errorf("normalize effective PostgreSQL search_path: %w", err)
	}
	expectedConfigured := []string{identity.SchemaName}
	if !equalPostgresSearchPath(configured, expectedConfigured) {
		return fmt.Errorf("configured PostgreSQL search_path must be exactly %q, got %q", identity.SchemaName, identity.ConfiguredSearchPath)
	}
	expectedEffective := []string{"pg_catalog", identity.SchemaName}
	if !equalPostgresSearchPath(effective, expectedEffective) {
		return fmt.Errorf("effective PostgreSQL search_path must be exactly pg_catalog, %s, got %q", identity.SchemaName, identity.EffectiveSearchPath)
	}
	return nil
}

func isSafePostgresApplicationSchema(schema string) bool {
	if schema == "" || len(schema) > 63 || strings.HasPrefix(schema, "pg_") || schema == "information_schema" {
		return false
	}
	for index := 0; index < len(schema); index++ {
		char := schema[index]
		if (char >= 'a' && char <= 'z') || char == '_' || (index > 0 && char >= '0' && char <= '9') {
			continue
		}
		return false
	}
	return true
}

func normalizePostgresSearchPath(value string) ([]string, error) {
	var paths []string
	var token strings.Builder
	quoted := false
	for index := 0; index < len(value); index++ {
		char := value[index]
		if quoted {
			if char == '"' {
				if index+1 < len(value) && value[index+1] == '"' {
					token.WriteByte('"')
					index++
					continue
				}
				quoted = false
				continue
			}
			token.WriteByte(char)
			continue
		}
		switch char {
		case '"':
			if strings.TrimSpace(token.String()) != "" {
				return nil, errors.New("quoted schema name has an invalid prefix")
			}
			quoted = true
		case ',':
			path := strings.TrimSpace(token.String())
			if path == "" {
				return nil, errors.New("search_path contains an empty schema")
			}
			paths = append(paths, path)
			token.Reset()
		default:
			token.WriteByte(char)
		}
	}
	if quoted {
		return nil, errors.New("search_path contains an unterminated quoted schema")
	}
	path := strings.TrimSpace(token.String())
	if path == "" {
		return nil, errors.New("search_path contains an empty schema")
	}
	return append(paths, path), nil
}

func equalPostgresSearchPath(actual, expected []string) bool {
	if len(actual) != len(expected) {
		return false
	}
	for index := range actual {
		if actual[index] != expected[index] {
			return false
		}
	}
	return true
}

func verifyPostgresSchemaInventory(db *gorm.DB, inventory postgresSchemaInventory) error {
	return verifyPostgresSchemaInventoryAgainstCatalog(db, inventory)
}

func verifyPostgresMigrationPostconditions(db *gorm.DB, schema string) error {
	if !isSafePostgresApplicationSchema(schema) {
		return fmt.Errorf("unsafe PostgreSQL application schema %q", schema)
	}
	var tokenType, priceType string
	if err := db.Raw(`SELECT columns.data_type FROM information_schema.columns AS columns
		WHERE columns.table_schema OPERATOR(pg_catalog.=) ?
		  AND columns.table_name OPERATOR(pg_catalog.=) 'tokens'
		  AND columns.column_name OPERATOR(pg_catalog.=) 'model_limits'`, schema).Scan(&tokenType).Error; err != nil {
		return err
	}
	if tokenType != "text" {
		return fmt.Errorf("tokens.model_limits type is %q, expected text", tokenType)
	}
	if err := db.Raw(`SELECT columns.data_type FROM information_schema.columns AS columns
		WHERE columns.table_schema OPERATOR(pg_catalog.=) ?
		  AND columns.table_name OPERATOR(pg_catalog.=) 'subscription_plans'
		  AND columns.column_name OPERATOR(pg_catalog.=) 'price_amount'`, schema).Scan(&priceType).Error; err != nil {
		return err
	}
	if priceType != "numeric" {
		return fmt.Errorf("subscription_plans.price_amount type is %q, expected numeric", priceType)
	}
	var legacyIndex bool
	if err := db.Raw(`SELECT EXISTS (SELECT 1 FROM pg_catalog.pg_indexes AS indexes
		WHERE indexes.schemaname OPERATOR(pg_catalog.=) ?
		  AND indexes.indexname OPERATOR(pg_catalog.=) ?)`, schema, legacyOpenSourceBountyParticipantIndex).
		Scan(&legacyIndex).Error; err != nil {
		return err
	}
	if legacyIndex {
		return errors.New("legacy open-source bounty participant index is still present")
	}
	checks := []struct {
		name  string
		query string
	}{
		{"user auth-version backfill", `SELECT pg_catalog.count(*) FROM users
			WHERE auth_version IS NULL OR auth_version OPERATOR(pg_catalog.<) 1`},
		{"external identity backfill", `SELECT pg_catalog.count(*) FROM users AS users
			WHERE users.telegram_id OPERATOR(pg_catalog.<>) '' AND NOT EXISTS (
				SELECT 1 FROM external_identity_claims AS claims
				WHERE claims.provider OPERATOR(pg_catalog.=) 'telegram'
				  AND claims.user_id OPERATOR(pg_catalog.=) users.id)`},
		{"retired frontend options", `SELECT pg_catalog.count(*) FROM options
			WHERE (key OPERATOR(pg_catalog.=) 'theme.frontend' AND value OPERATOR(pg_catalog.<>) 'default')
			   OR key OPERATOR(pg_catalog.=) ANY (ARRAY['ApiInfo','Announcements','FAQ','UptimeKumaUrl','UptimeKumaSlug'])`},
	}
	for _, check := range checks {
		var pending int64
		if err := db.Raw(check.query).Scan(&pending).Error; err != nil {
			return fmt.Errorf("verify %s: %w", check.name, err)
		}
		if pending != 0 {
			return fmt.Errorf("%s is incomplete (%d rows)", check.name, pending)
		}
	}
	var normalizedThemeCount int64
	if err := db.Raw(`SELECT pg_catalog.count(*) FROM options
		WHERE key OPERATOR(pg_catalog.=) 'theme.frontend'
		  AND value OPERATOR(pg_catalog.=) 'default'`).Scan(&normalizedThemeCount).Error; err != nil {
		return err
	}
	if normalizedThemeCount != 1 {
		return errors.New("retired theme option normalization is incomplete")
	}
	if err := verifyCompanyBillingProfilePostgresContract(db, schema); err != nil {
		return err
	}
	return nil
}

func verifyLogDatabaseSchema(db *gorm.DB, databaseType common.DatabaseType) error {
	if databaseType == common.DatabaseTypeClickHouse {
		var createTableSQL string
		if err := db.Raw("SHOW CREATE TABLE logs").Scan(&createTableSQL).Error; err != nil {
			return fmt.Errorf("verify ClickHouse log table: %w", err)
		}
		if createTableSQL == "" {
			return errors.New("required ClickHouse log table is missing")
		}
		return nil
	}
	if databaseType == common.DatabaseTypePostgreSQL {
		identity, err := loadPostgresRuntimeIdentity(db)
		if err != nil {
			return err
		}
		if err := verifyPostgresRuntimeIdentity(identity); err != nil {
			return err
		}
		inventory, err := buildPostgresSchemaInventory(db, identity.SchemaName, []interface{}{&Log{}})
		if err != nil {
			return err
		}
		return verifyPostgresSchemaInventory(db, inventory)
	}
	if !db.Migrator().HasTable(&Log{}) {
		return errors.New("required log table is missing")
	}
	statement := &gorm.Statement{DB: db}
	if err := statement.Parse(&Log{}); err != nil {
		return err
	}
	for _, field := range statement.Schema.Fields {
		if field.DBName != "" && !db.Migrator().HasColumn(&Log{}, field.DBName) {
			return fmt.Errorf("required log column %s is missing", field.DBName)
		}
	}
	for _, index := range statement.Schema.ParseIndexes() {
		if !db.Migrator().HasIndex(&Log{}, index.Name) {
			return fmt.Errorf("required log index %s is missing", index.Name)
		}
	}
	return nil
}
