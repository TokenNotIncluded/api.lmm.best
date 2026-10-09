package model

import (
	"errors"
	"fmt"
	"os"

	"gorm.io/gorm"
)

// OIDCRecord retains the provider's existing key, ownership and expiry contract.
// Keeping the storage model here lets standalone migrations cover the same
// opt-in tables that the HTTP runtime requires.
type OIDCRecord struct {
	Key       string `gorm:"primaryKey;size:160"`
	Value     string `gorm:"type:text;not null"`
	Owner     string `gorm:"size:128;index:idx_oidc_owner_expiry"`
	ExpiresAt int64  `gorm:"index;index:idx_oidc_owner_expiry"`
}

func (OIDCRecord) TableName() string { return "lmm_oidc_records" }

// OAuthServerEnabledFromEnv preserves the runtime's strict opt-in parsing.
func OAuthServerEnabledFromEnv() (bool, error) {
	switch os.Getenv("OAUTH_SERVER_ENABLED") {
	case "", "false":
		return false, nil
	case "true":
		return true, nil
	default:
		return false, errors.New("OAUTH_SERVER_ENABLED must be true or false")
	}
}

// SubprojectOIDCEnabledFromEnv preserves the provider's exact-true opt-in.
func SubprojectOIDCEnabledFromEnv() bool { return os.Getenv("LMM_OIDC_ENABLED") == "true" }

func oauthServerMigrationModels() []interface{} {
	return []interface{}{&OAuthServerAuthorization{}, &OAuthServerGrant{}, &OAuthServerCode{}, &OAuthServerToken{}}
}

func oauthBillingMigrationModels() []interface{} {
	return append(oauthServerMigrationModels(), &OAuthBillingBinding{})
}

func validateOAuthStorageDialect(db *gorm.DB) error {
	if db == nil {
		return errors.New("oauth server storage: nil database")
	}
	switch db.Dialector.Name() {
	case "sqlite", "postgres":
		return nil
	default:
		return fmt.Errorf("oauth server storage: unsupported dialect %q", db.Dialector.Name())
	}
}

func identityMigrationModels(db *gorm.DB) ([]interface{}, error) {
	enabled, err := OAuthServerEnabledFromEnv()
	if err != nil {
		return nil, err
	}
	var models []interface{}
	if enabled {
		if err := validateOAuthStorageDialect(db); err != nil {
			return nil, err
		}
		models = append(models, oauthBillingMigrationModels()...)
	}
	if SubprojectOIDCEnabledFromEnv() {
		models = append(models, &OIDCRecord{})
	}
	native, err := nativeAccountMigrationModels(db)
	if err != nil {
		return nil, err
	}
	return append(models, native...), nil
}

func startupMigrationModels(db *gorm.DB) ([]interface{}, error) {
	optional, err := identityMigrationModels(db)
	if err != nil {
		return nil, err
	}
	return append(mainMigrationModels(), optional...), nil
}

func verifyEnabledIdentityStorageSchema(db *gorm.DB) error {
	models, err := identityMigrationModels(db)
	if err != nil || len(models) == 0 {
		return err
	}
	return verifyIdentityStorageSchema(db, models)
}

// MigrateOIDCStorage is an explicit opt-in for isolated storage users. Normal
// application startup applies this model through the startup migration registry.
func MigrateOIDCStorage(db *gorm.DB) error {
	if db == nil {
		return errors.New("OIDC storage migration: nil database")
	}
	return db.AutoMigrate(&OIDCRecord{})
}

func VerifyOAuthBillingSchema(db *gorm.DB) error {
	if err := validateOAuthStorageDialect(db); err != nil {
		return err
	}
	return verifyIdentityStorageSchema(db, oauthBillingMigrationModels())
}

func VerifyOIDCStorageSchema(db *gorm.DB) error {
	return verifyIdentityStorageSchema(db, []interface{}{&OIDCRecord{}})
}

// Schema checks never apply migrations. PostgreSQL uses the same canonical
// schema, index and constraint verifier as the main startup registry.
func verifyIdentityStorageSchema(db *gorm.DB, models []interface{}) error {
	if db == nil {
		return errors.New("identity storage verification: nil database")
	}
	if db.Dialector.Name() == "postgres" {
		identity, err := loadPostgresRuntimeIdentity(db)
		if err != nil {
			return err
		}
		if err := verifyPostgresRuntimeIdentity(identity); err != nil {
			return err
		}
		inventory, err := buildPostgresSchemaInventory(db, identity.SchemaName, models)
		if err != nil {
			return err
		}
		if err := verifyPostgresSchemaInventory(db, inventory); err != nil {
			return err
		}
		return verifyIdentityStoragePostconditions(db, identity.SchemaName, models)
	}
	for _, item := range models {
		stmt := &gorm.Statement{DB: db}
		if err := stmt.Parse(item); err != nil {
			return err
		}
		if !db.Migrator().HasTable(item) {
			return fmt.Errorf("required identity storage table %s is missing", stmt.Schema.Table)
		}
		for _, field := range stmt.Schema.Fields {
			if field.DBName != "" && !db.Migrator().HasColumn(item, field.DBName) {
				return fmt.Errorf("required identity storage column %s.%s is missing", stmt.Schema.Table, field.DBName)
			}
		}
		for _, index := range stmt.Schema.ParseIndexes() {
			if !db.Migrator().HasIndex(item, index.Name) {
				return fmt.Errorf("required identity storage index %s.%s is missing", stmt.Schema.Table, index.Name)
			}
		}
	}
	return nil
}

func verifyIdentityStoragePostconditions(db *gorm.DB, schema string, models []interface{}) error {
	for _, item := range models {
		stmt := &gorm.Statement{DB: db}
		if err := stmt.Parse(item); err != nil {
			return err
		}
		// OIDC uses ON CONFLICT(key), and OAuth storage uses immediate digest
		// uniqueness. A matching DEFERRABLE key cannot provide that contract.
		var deferred bool
		if err := db.Raw(`SELECT EXISTS (SELECT 1 FROM pg_catalog.pg_constraint AS constraints
			JOIN pg_catalog.pg_class AS tables ON tables.oid OPERATOR(pg_catalog.=) constraints.conrelid
			JOIN pg_catalog.pg_namespace AS namespaces ON namespaces.oid OPERATOR(pg_catalog.=) tables.relnamespace
			WHERE namespaces.nspname OPERATOR(pg_catalog.=) ?
			  AND tables.relname OPERATOR(pg_catalog.=) ?
			  AND constraints.contype IN ('p', 'u') AND constraints.condeferrable)`, schema, stmt.Schema.Table).
			Scan(&deferred).Error; err != nil {
			return err
		}
		if deferred {
			return fmt.Errorf("identity storage key constraint %s must not be deferrable", stmt.Schema.Table)
		}
	}
	return nil
}
