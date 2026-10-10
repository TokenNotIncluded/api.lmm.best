package service

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func identityStartupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "identity.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	pool.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, pool.Close()) })
	return db
}

func TestConfigureOAuthIntegrationOnlyVerifiesExistingStorage(t *testing.T) {
	t.Setenv("LMM_DB_MIGRATION_MODE", string(model.DBMigrationModeVerify))
	// Programmatic configuration is authoritative even when the environment
	// flag is disabled. Schema validation must not silently disable the service.
	t.Setenv("OAUTH_SERVER_ENABLED", "false")
	cfg := OAuthServerConfig{Enabled: true, Issuer: "https://oauth.example.com", Groups: []string{"default"}}
	db := identityStartupTestDB(t)
	t.Cleanup(func() { _, _ = ConfigureOAuthIntegration(nil, OAuthServerConfig{}) })
	require.NoError(t, db.Exec("PRAGMA query_only = ON").Error)
	_, err := ConfigureOAuthIntegration(db, cfg)
	require.ErrorContains(t, err, "oauth_server_authorizations is missing")
	require.Nil(t, CurrentOAuthIntegration())
	require.False(t, db.Migrator().HasTable(&model.OAuthServerAuthorization{}))
	require.NoError(t, db.Exec("PRAGMA query_only = OFF").Error)
	require.NoError(t, model.MigrateOAuthBilling(db))
	require.NoError(t, db.Exec("PRAGMA query_only = ON").Error)
	integration, err := ConfigureOAuthIntegration(db, cfg)
	require.NoError(t, err, "configure succeeds with read-only storage after explicit apply")
	require.Same(t, integration, CurrentOAuthIntegration())
	require.NoError(t, db.Exec("PRAGMA query_only = OFF").Error)
	require.NoError(t, db.Migrator().DropIndex(&model.OAuthBillingBinding{}, "idx_o_auth_billing_bindings_token_id"))
	require.NoError(t, db.Exec("PRAGMA query_only = ON").Error)
	_, err = ConfigureOAuthIntegration(db, cfg)
	require.ErrorContains(t, err, "idx_o_auth_billing_bindings_token_id is missing")
	require.Nil(t, CurrentOAuthIntegration(), "failed reconfiguration cannot publish an unchecked integration")
	require.False(t, db.Migrator().HasIndex(&model.OAuthBillingBinding{}, "idx_o_auth_billing_bindings_token_id"))
}

func setOIDCStartupTestEnvironment(t *testing.T) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "oidc-key.pem")
	require.NoError(t, os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0600))
	t.Setenv("LMM_OIDC_ENABLED", "true")
	t.Setenv("LMM_OIDC_SIGNING_KEY_FILE", path)
	t.Setenv("LMM_OIDC_ISSUER", "https://oidc.example.com/oidc")
	t.Setenv("LMM_OIDC_CLIENTS", `[{"client_id":"test-client","name":"Test","redirect_uris":["https://client.example.com/callback"],"resources":["https://resource.example.com"],"scopes":["openid"],"controller":"human"}]`)
	t.Setenv("LMM_OIDC_RESOURCES", `[{"id":"test-resource","uri":"https://resource.example.com","secret_env":"OIDC_STARTUP_TEST_SECRET"}]`)
	t.Setenv("OIDC_STARTUP_TEST_SECRET", "isolated-provider-resource-secret-32-bytes")
	t.Setenv("LMM_OIDC_TRUSTED_PROXY_CIDRS", "")
}

func TestConfigureOIDCOnlyVerifiesExistingStorage(t *testing.T) {
	t.Setenv("LMM_DB_MIGRATION_MODE", string(model.DBMigrationModeVerify))
	setOIDCStartupTestEnvironment(t)
	db := identityStartupTestDB(t)
	require.NoError(t, db.Exec("PRAGMA query_only = ON").Error)
	_, err := ConfigureSubprojectOIDC(db)
	require.ErrorContains(t, err, "lmm_oidc_records is missing")
	require.False(t, db.Migrator().HasTable(&model.OIDCRecord{}))
	require.NoError(t, db.Exec("PRAGMA query_only = OFF").Error)
	require.NoError(t, model.MigrateOIDCStorage(db))
	expired := model.OIDCRecord{Key: "expired", Value: "old-value", ExpiresAt: time.Now().Unix() - 1}
	require.NoError(t, db.Create(&expired).Error)
	require.NoError(t, db.Exec("PRAGMA query_only = ON").Error)
	provider, err := ConfigureSubprojectOIDC(db)
	require.NoError(t, err, "configure must perform neither DDL nor expiry DELETE")
	require.NotNil(t, provider)
	var preserved model.OIDCRecord
	require.NoError(t, db.First(&preserved, "key = ?", expired.Key).Error)
	require.Equal(t, expired, preserved)
	require.NoError(t, db.Exec("PRAGMA query_only = OFF").Error)
	require.NoError(t, db.Migrator().DropIndex(&model.OIDCRecord{}, "idx_oidc_owner_expiry"))
	require.NoError(t, db.Exec("PRAGMA query_only = ON").Error)
	_, err = ConfigureSubprojectOIDC(db)
	require.ErrorContains(t, err, "idx_oidc_owner_expiry is missing")
	require.False(t, db.Migrator().HasIndex(&model.OIDCRecord{}, "idx_oidc_owner_expiry"))
}

func TestOIDCStoreWritesRetireExpiredRecords(t *testing.T) {
	db := identityStartupTestDB(t)
	require.NoError(t, model.MigrateOIDCStorage(db))
	now := time.Now().Unix()
	require.NoError(t, db.Create(&[]model.OIDCRecord{
		{Key: "expired", Value: "old", ExpiresAt: now - 1},
		{Key: "active", Value: "keep", ExpiresAt: now + 3600},
	}).Error)
	store := oidcStore{db: db}
	require.NoError(t, store.Set(context.Background(), "new", []byte("fresh"), now+3600, "test-owner"))
	var records []model.OIDCRecord
	require.NoError(t, db.Order("key").Find(&records).Error)
	require.Equal(t, []model.OIDCRecord{
		{Key: "active", Value: "keep", ExpiresAt: now + 3600},
		{Key: "new", Value: "fresh", Owner: "test-owner", ExpiresAt: now + 3600},
	}, records)
	value, err := store.Get(context.Background(), "new")
	require.NoError(t, err)
	require.Equal(t, []byte("fresh"), value)
}
