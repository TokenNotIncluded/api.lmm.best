package model

import (
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

const (
	companyBillingProfilePrimaryKeyName  = "company_billing_profiles_pkey"
	companyBillingProfileCountryCheck    = "company_billing_profiles_country_format"
	companyBillingProfileOwnerForeignKey = "company_billing_profiles_user_id_fkey"
)

func ensureCompanyBillingProfilePostgresContract(db *gorm.DB) error {
	if db == nil {
		return errors.New("company billing profile migration database is missing")
	}
	schema, err := currentPostgresSchema(db)
	if err != nil {
		return err
	}
	for _, constraint := range []struct {
		name string
		ddl  string
	}{
		{
			name: companyBillingProfileCountryCheck,
			ddl: `ALTER TABLE company_billing_profiles
				ADD CONSTRAINT company_billing_profiles_country_format
				CHECK (char_length(country) = 2 AND country = upper(country))`,
		},
		{
			name: companyBillingProfileOwnerForeignKey,
			ddl: `ALTER TABLE company_billing_profiles
				ADD CONSTRAINT company_billing_profiles_user_id_fkey
				FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
				DEFERRABLE INITIALLY DEFERRED`,
		},
	} {
		exists, err := postgresTableConstraintExists(db, schema, "company_billing_profiles", constraint.name)
		if err != nil {
			return err
		}
		if !exists {
			if err := db.Exec(constraint.ddl).Error; err != nil {
				return fmt.Errorf("add PostgreSQL company billing constraint %s: %w", constraint.name, err)
			}
		}
	}
	return verifyCompanyBillingProfilePostgresContract(db, schema)
}

func currentPostgresSchema(db *gorm.DB) (string, error) {
	var schema string
	if err := db.Raw(`SELECT pg_catalog.current_schema()`).Scan(&schema).Error; err != nil {
		return "", fmt.Errorf("load PostgreSQL application schema: %w", err)
	}
	if !isSafePostgresApplicationSchema(schema) {
		return "", fmt.Errorf("unsafe PostgreSQL application schema %q", schema)
	}
	return schema, nil
}

func postgresTableConstraintExists(db *gorm.DB, schema, table, constraint string) (bool, error) {
	var exists bool
	err := db.Raw(`SELECT EXISTS (
		SELECT 1
		FROM pg_catalog.pg_constraint AS constraints
		JOIN pg_catalog.pg_class AS tables ON tables.oid = constraints.conrelid
		JOIN pg_catalog.pg_namespace AS namespaces ON namespaces.oid = tables.relnamespace
		WHERE namespaces.nspname OPERATOR(pg_catalog.=) ?
		  AND tables.relname OPERATOR(pg_catalog.=) ?
		  AND constraints.conname OPERATOR(pg_catalog.=) ?
	)`, schema, table, constraint).Scan(&exists).Error
	if err != nil {
		return false, fmt.Errorf("inspect PostgreSQL constraint %s: %w", constraint, err)
	}
	return exists, nil
}

func verifyCompanyBillingProfilePostgresContract(db *gorm.DB, schema string) error {
	if db == nil {
		return errors.New("company billing profile verification database is missing")
	}
	if !isSafePostgresApplicationSchema(schema) {
		return fmt.Errorf("unsafe PostgreSQL application schema %q", schema)
	}

	var primaryValid bool
	if err := db.Raw(`SELECT COALESCE(bool_and(
		constraints.contype OPERATOR(pg_catalog.=) 'p'
		AND constraints.convalidated
		AND indexes.indisunique
		AND indexes.indisvalid
		AND indexes.indisready
		AND indexes.indnkeyatts OPERATOR(pg_catalog.=) 1
		AND pg_catalog.array_length(indexes.indkey, 1) OPERATOR(pg_catalog.=) 1
		AND indexes.indexprs IS NULL
		AND indexes.indpred IS NULL
		AND pg_catalog.pg_get_indexdef(indexes.indexrelid, 1, true) OPERATOR(pg_catalog.=) 'user_id'
	), false)
	FROM pg_catalog.pg_constraint AS constraints
	JOIN pg_catalog.pg_class AS tables ON tables.oid = constraints.conrelid
	JOIN pg_catalog.pg_namespace AS namespaces ON namespaces.oid = tables.relnamespace
	JOIN pg_catalog.pg_index AS indexes ON indexes.indexrelid = constraints.conindid
	WHERE namespaces.nspname OPERATOR(pg_catalog.=) ?
	  AND tables.relname OPERATOR(pg_catalog.=) 'company_billing_profiles'
	  AND constraints.conname OPERATOR(pg_catalog.=) ?`, schema, companyBillingProfilePrimaryKeyName).
		Scan(&primaryValid).Error; err != nil {
		return fmt.Errorf("verify company billing profile primary key: %w", err)
	}
	if !primaryValid {
		return errors.New("company billing profile primary key mismatch")
	}

	var checkDefinition string
	var checkValidated bool
	if err := db.Raw(`SELECT pg_catalog.pg_get_constraintdef(constraints.oid, false), constraints.convalidated
	FROM pg_catalog.pg_constraint AS constraints
	JOIN pg_catalog.pg_class AS tables ON tables.oid = constraints.conrelid
	JOIN pg_catalog.pg_namespace AS namespaces ON namespaces.oid = tables.relnamespace
	WHERE namespaces.nspname OPERATOR(pg_catalog.=) ?
	  AND tables.relname OPERATOR(pg_catalog.=) 'company_billing_profiles'
	  AND constraints.conname OPERATOR(pg_catalog.=) ?
	  AND constraints.contype OPERATOR(pg_catalog.=) 'c'`, schema, companyBillingProfileCountryCheck).
		Row().Scan(&checkDefinition, &checkValidated); err != nil {
		return fmt.Errorf("verify company billing profile country constraint: %w", err)
	}
	normalizedCheck := strings.Join(strings.Fields(strings.ToLower(checkDefinition)), " ")
	if !checkValidated || normalizedCheck != "check (((char_length(country) = 2) and ((country)::text = upper((country)::text))))" {
		return errors.New("company billing profile country constraint mismatch")
	}

	var foreignKeyValid bool
	if err := db.Raw(`SELECT COALESCE(bool_and(
		constraints.contype OPERATOR(pg_catalog.=) 'f'
		AND constraints.convalidated
		AND constraints.condeferrable
		AND constraints.condeferred
		AND constraints.confdeltype OPERATOR(pg_catalog.=) 'c'
		AND source_namespaces.nspname OPERATOR(pg_catalog.=) target_namespaces.nspname
		AND target_tables.relname OPERATOR(pg_catalog.=) 'users'
		AND constraints.conkey OPERATOR(pg_catalog.=) ARRAY[source_columns.attnum]::smallint[]
		AND constraints.confkey OPERATOR(pg_catalog.=) ARRAY[target_columns.attnum]::smallint[]
	), false)
	FROM pg_catalog.pg_constraint AS constraints
	JOIN pg_catalog.pg_class AS source_tables ON source_tables.oid = constraints.conrelid
	JOIN pg_catalog.pg_namespace AS source_namespaces ON source_namespaces.oid = source_tables.relnamespace
	JOIN pg_catalog.pg_class AS target_tables ON target_tables.oid = constraints.confrelid
	JOIN pg_catalog.pg_namespace AS target_namespaces ON target_namespaces.oid = target_tables.relnamespace
	JOIN pg_catalog.pg_attribute AS source_columns
	  ON source_columns.attrelid = source_tables.oid AND source_columns.attname OPERATOR(pg_catalog.=) 'user_id'
	JOIN pg_catalog.pg_attribute AS target_columns
	  ON target_columns.attrelid = target_tables.oid AND target_columns.attname OPERATOR(pg_catalog.=) 'id'
	WHERE source_namespaces.nspname OPERATOR(pg_catalog.=) ?
	  AND source_tables.relname OPERATOR(pg_catalog.=) 'company_billing_profiles'
	  AND constraints.conname OPERATOR(pg_catalog.=) ?`, schema, companyBillingProfileOwnerForeignKey).
		Scan(&foreignKeyValid).Error; err != nil {
		return fmt.Errorf("verify company billing profile owner foreign key: %w", err)
	}
	if !foreignKeyValid {
		return errors.New("company billing profile owner foreign key mismatch")
	}
	return nil
}
