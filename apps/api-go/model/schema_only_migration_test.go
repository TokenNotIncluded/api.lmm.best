package model

import (
	"context"
	"testing"
)

func TestSchemaOnlyDatabaseRefusesFallbackAndMultipleDatabases(t *testing.T) {
	for _, dsn := range []string{"", "local", "mysql://example.invalid", "clickhouse://example.invalid"} {
		t.Run(dsn, func(t *testing.T) {
			t.Setenv("SQL_DSN", dsn)
			t.Setenv("LOG_SQL_DSN", "")
			if _, err := OpenSchemaOnlyMigrationDatabase(context.Background()); err == nil {
				t.Fatal("schema-only database fallback was accepted")
			}
		})
	}
	t.Setenv("SQL_DSN", "postgresql://must-not-connect.invalid/app")
	t.Setenv("LOG_SQL_DSN", "postgresql://must-not-connect.invalid/logs")
	if _, err := OpenSchemaOnlyMigrationDatabase(context.Background()); err == nil {
		t.Fatal("separate log database was accepted")
	}
	t.Setenv("LOG_SQL_DSN", "")
	t.Setenv("SKIP_64BIT_QUOTA_SCHEMA_CHECK", "true")
	if _, err := OpenSchemaOnlyMigrationDatabase(context.Background()); err == nil {
		t.Fatal("wallet schema safety bypass was accepted")
	}
}
