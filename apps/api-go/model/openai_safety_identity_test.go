package model

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"path/filepath"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func safetyIdentityDatabase(t *testing.T, path string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&AssistantGiftRiskKey{}))
	return db
}

func TestOpenAIPrivateSafetyIdentifierDurableAcrossNodesRestartsAndDatabaseSwitch(t *testing.T) {
	oldDB, oldSecret := DB, common.CryptoSecret
	t.Cleanup(func() { DB, common.CryptoSecret = oldDB, oldSecret })
	path := filepath.Join(t.TempDir(), "identity.sqlite")
	DB = safetyIdentityDatabase(t, path)
	common.CryptoSecret = "first-instance-bootstrap-key"
	first, err := OpenAIPrivateSafetyIdentifier(context.Background(), 42)
	require.NoError(t, err)
	require.Len(t, first, 64) // Both OpenAI schemas allow a maximum of 64 bytes.
	_, err = hex.DecodeString(first)
	require.NoError(t, err)
	other, err := OpenAIPrivateSafetyIdentifier(context.Background(), 43)
	require.NoError(t, err)
	require.NotEqual(t, first, other)
	unkeyed := sha256.Sum256([]byte("42"))
	require.NotEqual(t, hex.EncodeToString(unkeyed[:]), first)
	require.NotEqual(t, common.GenerateHMACWithKey([]byte(common.CryptoSecret), "42"), first)
	// A fresh DB handle simulates another process/restart, with a different
	// process-local secret. Identity must come from the shared durable row.
	DB = safetyIdentityDatabase(t, path)
	common.CryptoSecret = "second-instance-local-key"
	again, err := OpenAIPrivateSafetyIdentifier(context.Background(), 42)
	require.NoError(t, err)
	require.Equal(t, first, again)
	var count int64
	require.NoError(t, DB.Model(&AssistantGiftRiskKey{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
	// Another installation cannot accidentally inherit an in-memory cache.
	DB = safetyIdentityDatabase(t, filepath.Join(t.TempDir(), "other.sqlite"))
	otherInstallation, err := OpenAIPrivateSafetyIdentifier(context.Background(), 42)
	require.NoError(t, err)
	require.NotEqual(t, first, otherInstallation)
}

func TestOpenAIPrivateSafetyIdentifierRejectsInvalidOrUnavailableIdentity(t *testing.T) {
	oldDB := DB
	t.Cleanup(func() { DB = oldDB })
	DB = nil
	for _, userID := range []int{-1, 0, 42} {
		id, err := OpenAIPrivateSafetyIdentifier(context.Background(), userID)
		require.Empty(t, id)
		require.ErrorIs(t, err, ErrOpenAISafetyIdentityUnavailable)
	}
	DB = safetyIdentityDatabase(t, filepath.Join(t.TempDir(), "cancelled.sqlite"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	id, err := OpenAIPrivateSafetyIdentifier(ctx, 42)
	require.Empty(t, id)
	require.ErrorIs(t, err, ErrOpenAISafetyIdentityUnavailable)
}

func TestAssistantGiftRiskSecretNeverTracesKeyMaterial(t *testing.T) {
	oldSecret := common.CryptoSecret
	t.Cleanup(func() { common.CryptoSecret = oldSecret })
	common.CryptoSecret = "synthetic-sensitive-bootstrap-material"
	db := safetyIdentityDatabase(t, filepath.Join(t.TempDir(), "private.sqlite"))
	var trace bytes.Buffer
	db = db.Session(&gorm.Session{Logger: gormlogger.New(log.New(&trace, "", 0), gormlogger.Config{LogLevel: gormlogger.Info})}).Debug()
	first, err := getAssistantGiftRiskSecret(db)
	require.NoError(t, err)
	again, err := getAssistantGiftRiskSecret(db)
	require.NoError(t, err)
	require.Equal(t, first, again)
	require.Empty(t, trace.String(), "private initializer must bypass all SQL tracing")
	// A failing write uses the same logger boundary, rather than rendering the
	// proposed key in an INSERT error log. This is a real DB callback failure.
	require.NoError(t, db.Exec("DELETE FROM assistant_gift_risk_keys").Error)
	trace.Reset()
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register("test:reject_private_insert", func(tx *gorm.DB) { tx.AddError(fmt.Errorf("synthetic write rejection")) }))
	_, err = getAssistantGiftRiskSecret(db)
	require.Error(t, err)
	require.Empty(t, trace.String())
}
