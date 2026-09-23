package model

import (
	"encoding/base64"
	"path/filepath"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/glebarez/sqlite"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestPasskeysAreIndependentAndScopedToTheirOwner(t *testing.T) {
	truncateTables(t)
	user := User{Username: "passkey-multiple-owner", Password: "password", AffCode: "passkey-multiple-owner", AuthVersion: 1}
	other := User{Username: "passkey-multiple-other", Password: "password", AffCode: "passkey-multiple-other", AuthVersion: 1}
	require.NoError(t, DB.Create(&user).Error)
	require.NoError(t, DB.Create(&other).Error)

	firstID := []byte("first-passkey")
	secondID := []byte("second-passkey")
	first := &PasskeyCredential{UserID: user.Id, Name: "Laptop", CredentialID: base64.StdEncoding.EncodeToString(firstID), PublicKey: "first-public-key"}
	second := &PasskeyCredential{UserID: user.Id, Name: "Phone", CredentialID: base64.StdEncoding.EncodeToString(secondID), PublicKey: "second-public-key"}
	require.NoError(t, CreatePasskeyCredentialWithAuthVersion(first))
	require.NoError(t, CreatePasskeyCredentialWithAuthVersion(second))
	assertUserAuthVersion(t, user.Id, 3)

	credentials, err := GetPasskeysByUserID(user.Id)
	require.NoError(t, err)
	require.Len(t, credentials, 2)
	assert.Equal(t, []string{"Laptop", "Phone"}, []string{credentials[0].Name, credentials[1].Name})
	storedFirst, err := GetPasskeyByCredentialID(firstID)
	require.NoError(t, err)
	assert.Equal(t, first.ID, storedFirst.ID)
	storedSecond, err := GetPasskeyByCredentialID(secondID)
	require.NoError(t, err)
	assert.Equal(t, second.ID, storedSecond.ID)

	usedAt := time.Now().UTC().Truncate(time.Second)
	require.NoError(t, UpdatePasskeyAssertionState(user.Id, &webauthn.Credential{
		ID:            secondID,
		Authenticator: webauthn.Authenticator{SignCount: 4},
	}, usedAt))
	credentials, err = GetPasskeysByUserID(user.Id)
	require.NoError(t, err)
	assert.Nil(t, credentials[0].LastUsedAt)
	assert.EqualValues(t, 4, credentials[1].SignCount)
	assert.Equal(t, usedAt.Unix(), credentials[1].LastUsedAt.Unix())

	assert.ErrorIs(t, DeletePasskeyByIDWithAuthVersion(other.Id, first.ID), ErrPasskeyNotFound)
	assertUserAuthVersion(t, other.Id, 1)
	require.NoError(t, DeletePasskeyByIDWithAuthVersion(user.Id, first.ID))
	assertUserAuthVersion(t, user.Id, 4)
	credentials, err = GetPasskeysByUserID(user.Id)
	require.NoError(t, err)
	require.Len(t, credentials, 1)
	assert.Equal(t, second.ID, credentials[0].ID)
	require.NoError(t, DeletePasskeyByUserIDWithAuthVersion(user.Id))
	assertUserAuthVersion(t, user.Id, 5)
	credentials, err = GetPasskeysByUserID(user.Id)
	require.NoError(t, err)
	assert.Empty(t, credentials)
}

type legacySinglePasskeyCredential struct {
	ID           int    `gorm:"primaryKey"`
	UserID       int    `gorm:"uniqueIndex;not null"`
	CredentialID string `gorm:"type:varchar(512);uniqueIndex;not null"`
	PublicKey    string `gorm:"type:text;not null"`
}

func (legacySinglePasskeyCredential) TableName() string { return "passkey_credentials" }

func TestPasskeyMigrationPreservesExistingCredentialAndAllowsAnother(t *testing.T) {
	previousDB := DB
	previousType := common.MainDatabaseType()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "legacy-passkeys.db")), &gorm.Config{})
	require.NoError(t, err)
	DB = db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	t.Cleanup(func() {
		DB = previousDB
		common.SetMainDatabaseType(previousType)
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})

	require.NoError(t, db.AutoMigrate(&legacySinglePasskeyCredential{}))
	require.True(t, db.Migrator().HasIndex(&legacySinglePasskeyCredential{}, legacyPasskeyUserUniqueIndex))
	require.NoError(t, db.Create(&legacySinglePasskeyCredential{UserID: 7, CredentialID: "old-id", PublicKey: "old-key"}).Error)
	require.NoError(t, migratePasskeyCredentialUserIndex())
	require.NoError(t, db.AutoMigrate(&PasskeyCredential{}))
	require.NoError(t, migratePasskeyCredentialUserIndex())
	assert.False(t, db.Migrator().HasIndex(&PasskeyCredential{}, legacyPasskeyUserUniqueIndex))
	assert.True(t, db.Migrator().HasIndex(&PasskeyCredential{}, "idx_passkey_credentials_owner"))
	require.NoError(t, db.Create(&PasskeyCredential{UserID: 7, CredentialID: "new-id", PublicKey: "new-key"}).Error)
	credentials, err := GetPasskeysByUserID(7)
	require.NoError(t, err)
	require.Len(t, credentials, 2)
	assert.Equal(t, "old-id", credentials[0].CredentialID)
	assert.Equal(t, "new-id", credentials[1].CredentialID)
	assert.Error(t, db.Create(&PasskeyCredential{UserID: 8, CredentialID: "old-id", PublicKey: "duplicate-key"}).Error)
}
