package model

import (
	"errors"
	"fmt"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func identityModelTables(t *testing.T, db *gorm.DB, models []interface{}) map[string]bool {
	t.Helper()
	tables := map[string]bool{}
	for _, item := range models {
		stmt := &gorm.Statement{DB: db}
		require.NoError(t, stmt.Parse(item))
		tables[stmt.Schema.Table] = true
	}
	return tables
}

func TestIdentityStartupRegistryPreservesOptInAcrossWriterFloors(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Option{}))
	for _, flags := range []struct{ oauth, oidc string }{{"", ""}, {"true", ""}, {"false", "true"}, {"true", "true"}, {"false", "TRUE"}} {
		t.Run(fmt.Sprintf("oauth=%s,oidc=%s", flags.oauth, flags.oidc), func(t *testing.T) {
			t.Setenv("OAUTH_SERVER_ENABLED", flags.oauth)
			t.Setenv("LMM_OIDC_ENABLED", flags.oidc)
			applyModels, err := startupMigrationModels(db)
			require.NoError(t, err)
			apply := identityModelTables(t, db, applyModels)
			for _, floor := range []string{"", "1", "5", "6", "7"} {
				require.NoError(t, db.Where("key = ?", MerchantStoreWriterCapabilityOption).Delete(&Option{}).Error)
				if floor != "" {
					require.NoError(t, db.Create(&Option{Key: MerchantStoreWriterCapabilityOption, Value: floor}).Error)
				}
				verifyModels, err := runtimeVerificationModels(db)
				require.NoError(t, err)
				verify := identityModelTables(t, db, verifyModels)
				for _, item := range append(oauthBillingMigrationModels(), &OIDCRecord{}) {
					stmt := &gorm.Statement{DB: db}
					require.NoError(t, stmt.Parse(item))
					expected := flags.oauth == "true"
					if stmt.Schema.Table == "lmm_oidc_records" {
						expected = flags.oidc == "true"
					}
					require.Equal(t, expected, apply[stmt.Schema.Table])
					require.Equal(t, expected, verify[stmt.Schema.Table], "floor %s", floor)
				}
			}
		})
	}
	t.Setenv("OAUTH_SERVER_ENABLED", "TRUE")
	_, err = startupMigrationModels(db)
	require.ErrorContains(t, err, "OAUTH_SERVER_ENABLED must be true or false")
}

func TestIdentityStoragePostgresApplyAndReadOnlyVerification(t *testing.T) {
	t.Setenv("OAUTH_SERVER_ENABLED", "true")
	t.Setenv("LMM_OIDC_ENABLED", "true")
	db := openIsolatedPostgresCacheTestDB(t, &Option{})
	models, err := startupMigrationModels(db)
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(append(models, &SubscriptionPlan{})...))
	require.NoError(t, ensureCompanyBillingProfilePostgresContract(db))
	require.NoError(t, db.Create(&Option{Key: "theme.frontend", Value: "default"}).Error)
	before := runtimeVerificationPostgresFacts(t, db)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SET TRANSACTION READ ONLY").Error; err != nil {
			return err
		}
		require.NoError(t, verifyPostgresRuntimeAndSchema(tx))
		require.NoError(t, VerifyOAuthBillingSchema(tx))
		require.NoError(t, VerifyOIDCStorageSchema(tx))
		return nil
	}))
	require.Equal(t, before, runtimeVerificationPostgresFacts(t, db))
	for name, damage := range map[string]string{
		"oauth-table":   "DROP TABLE oauth_server_tokens",
		"oauth-column":  "ALTER TABLE oauth_server_authorizations DROP COLUMN browser_auth_version",
		"oauth-unique":  "DROP INDEX idx_o_auth_billing_bindings_token_id; CREATE INDEX idx_o_auth_billing_bindings_token_id ON o_auth_billing_bindings(token_id)",
		"oauth-primary": `ALTER TABLE o_auth_billing_bindings DROP CONSTRAINT o_auth_billing_bindings_pkey; ALTER TABLE o_auth_billing_bindings ADD CONSTRAINT o_auth_billing_bindings_pkey PRIMARY KEY (grant_id)`,
		"oidc-primary":  "ALTER TABLE lmm_oidc_records DROP CONSTRAINT lmm_oidc_records_pkey",
		"oidc-index":    "DROP INDEX idx_oidc_owner_expiry; CREATE INDEX idx_oidc_owner_expiry ON lmm_oidc_records(expires_at, owner)",
		"oidc-deferred": "ALTER TABLE lmm_oidc_records DROP CONSTRAINT lmm_oidc_records_pkey; ALTER TABLE lmm_oidc_records ADD CONSTRAINT lmm_oidc_records_pkey PRIMARY KEY (key) DEFERRABLE",
	} {
		t.Run(name, func(t *testing.T) {
			rollback := errors.New("rollback intentional schema damage")
			err := db.Transaction(func(tx *gorm.DB) error {
				require.NoError(t, tx.Exec(damage).Error)
				damaged := runtimeVerificationPostgresFacts(t, tx)
				require.Error(t, verifyPostgresRuntimeAndSchema(tx))
				if name[:4] == "oidc" {
					require.Error(t, VerifyOIDCStorageSchema(tx))
				} else {
					require.Error(t, VerifyOAuthBillingSchema(tx))
				}
				require.Equal(t, damaged, runtimeVerificationPostgresFacts(t, tx), "failed verification cannot repair schema or records")
				return rollback
			})
			require.ErrorIs(t, err, rollback)
			require.Equal(t, before, runtimeVerificationPostgresFacts(t, db))
		})
	}
}
