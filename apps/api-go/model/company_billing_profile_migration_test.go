package model

import (
	"fmt"
	"net/url"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestCompanyBillingProfilePostgresMigrationContract(t *testing.T) {
	databaseURL := os.Getenv("LMM_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("LMM_TEST_DATABASE_URL is required")
	}
	admin, err := gorm.Open(postgres.Open(databaseURL), &gorm.Config{})
	require.NoError(t, err)
	schema := fmt.Sprintf("lmm_company_billing_%d", os.Getpid())
	require.True(t, isSafePostgresApplicationSchema(schema))
	require.NoError(t, admin.Exec(`CREATE SCHEMA `+schema).Error)
	t.Cleanup(func() {
		require.NoError(t, admin.Exec(`DROP SCHEMA `+schema+` CASCADE`).Error)
	})

	parsed, err := url.Parse(databaseURL)
	require.NoError(t, err)
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	db, err := gorm.Open(postgres.Open(parsed.String()), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Exec(`CREATE TABLE users (id BIGINT PRIMARY KEY)`).Error)
	require.NoError(t, db.AutoMigrate(&CompanyBillingProfile{}))
	require.NoError(t, ensureCompanyBillingProfilePostgresContract(db))
	require.NoError(t, verifyCompanyBillingProfilePostgresContract(db, schema))

	transaction := db.Begin()
	require.NoError(t, transaction.Error)
	require.NoError(t, transaction.Exec(`INSERT INTO company_billing_profiles
		(user_id,country,is_business,use_for_invoices,created_at,updated_at)
		VALUES (7,'US',false,false,1,1)`).Error)
	require.NoError(t, transaction.Exec(`INSERT INTO users (id) VALUES (7)`).Error)
	require.NoError(t, transaction.Commit().Error)
	require.NoError(t, db.Exec(`DELETE FROM users WHERE id=7`).Error)
	var remaining int64
	require.NoError(t, db.Table("company_billing_profiles").Count(&remaining).Error)
	require.Zero(t, remaining, "owner deletion must physically remove billing PII")

	require.NoError(t, db.Exec(`ALTER TABLE company_billing_profiles
		DROP CONSTRAINT company_billing_profiles_user_id_fkey`).Error)
	require.NoError(t, db.Exec(`ALTER TABLE company_billing_profiles
		ADD CONSTRAINT company_billing_profiles_user_id_fkey
		FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE`).Error)
	require.ErrorContains(
		t,
		verifyCompanyBillingProfilePostgresContract(db, schema),
		"owner foreign key mismatch",
	)
}
