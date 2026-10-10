package model

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const marketCredentialTestKey = "0123456789abcdef0123456789abcdef"
const marketCredentialTestSecret = "publisher-service-fixture-secret"

type marketCredentialFixture struct {
	db      *gorm.DB
	owner   User
	other   User
	service *ToolMarketService
}

func newMarketCredentialFixture(t *testing.T) marketCredentialFixture {
	t.Helper()
	t.Setenv("TOOL_MARKET_ENCRYPTION_KEY", marketCredentialTestKey)
	t.Setenv("CRYPTO_SECRET", "")
	db := marketTestDB(t)
	require.NoError(t, db.AutoMigrate(&ToolMarketCredential{}))
	fixture := marketCredentialFixture{db: db, owner: marketTestUser(t, db, "credential-owner", 0, common.RoleCommonUser), other: marketTestUser(t, db, "credential-other", 0, common.RoleRootUser)}
	var err error
	fixture.service, err = SaveToolMarketDraft(fixture.owner.Id, "", marketTestDraft(1))
	require.NoError(t, err)
	return fixture
}

func (f marketCredentialFixture) configure(t *testing.T, mode, secret string) {
	t.Helper()
	require.NoError(t, ConfigureToolMarketCredential(f.owner.Id, f.service.ID, f.service.DraftVersionID, mode, secret, ""))
}

func (f marketCredentialFixture) validated(t *testing.T) {
	t.Helper()
	require.NoError(t, f.db.Model(&ToolMarketVersion{}).Where("id = ?", f.service.DraftVersionID).Update("validation_digest", gorm.Expr("digest")).Error)
	require.NoError(t, f.db.Model(&ToolMarketToolVersion{}).Where("version_id = ?", f.service.DraftVersionID).Update("remote_digest", strings.Repeat("a", 64)).Error)
}

func (f marketCredentialFixture) requireUnvalidated(t *testing.T) {
	t.Helper()
	var version ToolMarketVersion
	require.NoError(t, f.db.First(&version, "id = ?", f.service.DraftVersionID).Error)
	require.Empty(t, version.ValidationDigest)
	var tools []ToolMarketToolVersion
	require.NoError(t, f.db.Where("version_id = ?", version.ID).Find(&tools).Error)
	require.NotEmpty(t, tools)
	for _, tool := range tools {
		require.Empty(t, tool.RemoteDigest)
	}
}

func TestToolMarketCredentialEncryptedOwnerOnlyAndValidationInvalidated(t *testing.T) {
	f := newMarketCredentialFixture(t)
	f.validated(t)
	var before ToolMarketVersion
	require.NoError(t, f.db.First(&before, "id = ?", f.service.DraftVersionID).Error)
	f.configure(t, "bearer", marketCredentialTestSecret)
	f.requireUnvalidated(t)
	credential, err := ResolveToolMarketCredential(f.owner.Id, f.service.ID, before.ID, before.Endpoint)
	require.NoError(t, err)
	require.Equal(t, "bearer", credential.Mode)
	require.Equal(t, marketCredentialTestSecret, credential.Secret)
	metadata, err := GetToolMarketCredentialMetadata(f.owner.Id, f.service.ID, before.ID)
	require.NoError(t, err)
	require.True(t, metadata.Configured)
	require.Equal(t, "bearer", metadata.Mode)
	require.Positive(t, metadata.UpdatedAt)
	_, err = GetToolMarketCredentialMetadata(f.other.Id, f.service.ID, before.ID)
	require.ErrorIs(t, err, ErrToolMarketDenied)
	require.ErrorIs(t, ConfigureToolMarketCredential(f.other.Id, f.service.ID, before.ID, "bearer", "other-secret", ""), ErrToolMarketDenied)
	var stored ToolMarketCredential
	require.NoError(t, f.db.First(&stored, "version_id = ?", before.ID).Error)
	require.True(t, strings.HasPrefix(stored.Ciphertext, "v1:"))
	require.NotContains(t, stored.Ciphertext, marketCredentialTestSecret)
	for _, value := range []any{stored, credential} {
		data, err := json.Marshal(value)
		require.NoError(t, err)
		require.JSONEq(t, `{}`, string(data))
	}
	detail, err := GetToolMarketDetail(f.owner.Id, f.service.ID, true)
	require.NoError(t, err)
	require.Equal(t, before.Digest, detail.Version.Digest)
	data, err := json.Marshal(detail)
	require.NoError(t, err)
	require.NotContains(t, string(data), marketCredentialTestSecret)
	require.NotContains(t, string(data), stored.Ciphertext)
	var events []ToolMarketEvent
	require.NoError(t, f.db.Where("action = ?", "credential.configure").Find(&events).Error)
	require.Len(t, events, 1)
	hash := sha256.Sum256([]byte(marketCredentialTestSecret))
	for _, sensitive := range []string{marketCredentialTestSecret, stored.Ciphertext, marketDigest(marketCredentialTestSecret), hex.EncodeToString(hash[:])} {
		require.NotContains(t, events[0].Details, sensitive)
	}
	require.JSONEq(t, `{"version_id":"`+before.ID+`","mode":"bearer","configured":true,"updated_at":`+jsonNumber(metadata.UpdatedAt)+`}`, events[0].Details)
}

func jsonNumber(value int64) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func TestToolMarketCredentialInputValidation(t *testing.T) {
	for _, valid := range []struct{ mode, secret string }{{"none", ""}, {"bearer", "provider_token-123+/="}, {"api_key", "provider api key"}} {
		require.NoError(t, ValidateToolMarketCredential(valid.mode, valid.secret))
	}
	invalid := []struct{ mode, secret string }{{"", ""}, {"cookie", "value"}, {"none", "value"}, {"bearer", ""}, {"api_key", "  "}, {"bearer", "Bearer token"}, {"bearer", "token\r\nInjected: value"}, {"api_key", "token\x00"}, {"api_key", "token\t"}, {"bearer", "token\u0085"}, {"bearer", strings.Repeat("a", 4097)}, {"api_key", "invalid-\xff"}}
	for _, prefix := range []string{"lmm_at_", "lmm_rt_", "lmm_market_", "lmm_mcp_", "lmm_drawing_mcp_", "lmm_bounty_mcp_"} {
		invalid = append(invalid, struct{ mode, secret string }{"bearer", prefix + "private-client-token"})
		invalid = append(invalid, struct{ mode, secret string }{"api_key", " " + prefix + "private-client-token "})
	}
	for _, input := range invalid {
		require.ErrorIs(t, ValidateToolMarketCredential(input.mode, input.secret), ErrToolMarketInput)
	}
}

func TestToolMarketCredentialDraftStateAndExecutionBinding(t *testing.T) {
	for _, state := range []string{"pending", "published", "rejected"} {
		t.Run(state, func(t *testing.T) {
			f := newMarketCredentialFixture(t)
			require.NoError(t, f.db.Model(&ToolMarketVersion{}).Where("id = ?", f.service.DraftVersionID).Update("status", state).Error)
			require.ErrorIs(t, ConfigureToolMarketCredential(f.owner.Id, f.service.ID, f.service.DraftVersionID, "bearer", marketCredentialTestSecret, ""), ErrToolMarketConflict)
		})
	}
	t.Run("wrong-version-type", func(t *testing.T) {
		f := newMarketCredentialFixture(t)
		require.NoError(t, f.db.Model(&ToolMarketVersion{}).Where("id = ?", f.service.DraftVersionID).Update("execution_type", "builtin").Error)
		require.ErrorIs(t, ConfigureToolMarketCredential(f.owner.Id, f.service.ID, f.service.DraftVersionID, "bearer", marketCredentialTestSecret, ""), ErrToolMarketConflict)
	})
	t.Run("identity-and-endpoint", func(t *testing.T) {
		f := newMarketCredentialFixture(t)
		f.configure(t, "api_key", marketCredentialTestSecret)
		_, err := ResolveToolMarketCredential(f.other.Id, f.service.ID, f.service.DraftVersionID, "https://example.com/mcp")
		require.ErrorIs(t, err, ErrToolMarketDenied)
		for _, endpoint := range []string{"https://example.com/other", "https://other.example.com/mcp", "https://example.com/mcp?key=value", ""} {
			_, err := ResolveToolMarketCredential(f.owner.Id, f.service.ID, f.service.DraftVersionID, endpoint)
			require.Error(t, err)
		}
		var original ToolMarketCredential
		require.NoError(t, f.db.First(&original, "version_id = ?", f.service.DraftVersionID).Error)
		for _, mutation := range []map[string]any{{"owner_id": f.other.Id}, {"service_id": uuid.NewString()}, {"endpoint_digest": strings.Repeat("0", 64)}, {"mode": "cookie"}} {
			require.NoError(t, f.db.Model(&ToolMarketCredential{}).Where("version_id = ?", original.VersionID).Updates(mutation).Error)
			_, err := ResolveToolMarketCredential(f.owner.Id, f.service.ID, original.VersionID, "https://example.com/mcp")
			require.ErrorIs(t, err, ErrToolMarketConflict)
			require.NoError(t, f.db.Model(&ToolMarketCredential{}).Where("version_id = ?", original.VersionID).Select("owner_id", "service_id", "endpoint_digest", "mode").Updates(original).Error)
		}
		for _, mutation := range []map[string]any{{"id": uuid.NewString()}, {"mode": "bearer"}} {
			require.NoError(t, f.db.Model(&ToolMarketCredential{}).Where("version_id = ?", original.VersionID).Updates(mutation).Error)
			_, err := ResolveToolMarketCredential(f.owner.Id, f.service.ID, original.VersionID, "https://example.com/mcp")
			require.ErrorIs(t, err, ErrToolMarketCredentialUnavailable)
			require.NoError(t, f.db.Model(&ToolMarketCredential{}).Where("version_id = ?", original.VersionID).Updates(map[string]any{"id": original.ID, "mode": original.Mode}).Error)
		}
	})
	t.Run("disabled-owner", func(t *testing.T) {
		f := newMarketCredentialFixture(t)
		f.configure(t, "bearer", marketCredentialTestSecret)
		require.NoError(t, f.db.Model(&User{}).Where("id = ?", f.owner.Id).Update("status", common.UserStatusDisabled).Error)
		_, err := ResolveToolMarketCredential(f.owner.Id, f.service.ID, f.service.DraftVersionID, "https://example.com/mcp")
		require.ErrorIs(t, err, ErrToolMarketDenied)
	})
}

func TestToolMarketCredentialCopyReencryptsAndRejectsCrossBinding(t *testing.T) {
	f := newMarketCredentialFixture(t)
	f.configure(t, "bearer", marketCredentialTestSecret)
	priorVersionID := f.service.DraftVersionID
	var prior ToolMarketCredential
	require.NoError(t, f.db.First(&prior, "version_id = ?", priorVersionID).Error)
	var err error
	f.service, err = SaveToolMarketDraft(f.owner.Id, f.service.ID, marketTestDraft(2))
	require.NoError(t, err)
	require.ErrorIs(t, ConfigureToolMarketCredential(f.owner.Id, f.service.ID, priorVersionID, "bearer", "updated-old-secret", ""), ErrToolMarketConflict)
	f.validated(t)
	require.NoError(t, ConfigureToolMarketCredential(f.owner.Id, f.service.ID, f.service.DraftVersionID, "", "", priorVersionID))
	f.requireUnvalidated(t)
	var copied ToolMarketCredential
	require.NoError(t, f.db.First(&copied, "version_id = ?", f.service.DraftVersionID).Error)
	require.NotEqual(t, prior.ID, copied.ID)
	require.NotEqual(t, prior.Ciphertext, copied.Ciphertext)
	resolved, err := ResolveToolMarketCredential(f.owner.Id, f.service.ID, copied.VersionID, "https://example.com/mcp")
	require.NoError(t, err)
	require.Equal(t, marketCredentialTestSecret, resolved.Secret)
	require.NoError(t, f.db.Model(&ToolMarketCredential{}).Where("version_id = ?", copied.VersionID).Update("ciphertext", prior.Ciphertext).Error)
	_, err = ResolveToolMarketCredential(f.owner.Id, f.service.ID, copied.VersionID, "https://example.com/mcp")
	require.ErrorIs(t, err, ErrToolMarketCredentialUnavailable)
	require.ErrorIs(t, ConfigureToolMarketCredential(f.owner.Id, f.service.ID, copied.VersionID, "api_key", "", priorVersionID), ErrToolMarketInput)
	otherService, err := SaveToolMarketDraft(f.owner.Id, "", marketTestDraft(1))
	require.NoError(t, err)
	require.Error(t, ConfigureToolMarketCredential(f.owner.Id, otherService.ID, otherService.DraftVersionID, "", "", priorVersionID))
	changedEndpoint := marketTestDraft(1)
	changedEndpoint.Endpoint = "https://example.com/other"
	f.service, err = SaveToolMarketDraft(f.owner.Id, f.service.ID, changedEndpoint)
	require.NoError(t, err)
	require.ErrorIs(t, ConfigureToolMarketCredential(f.owner.Id, f.service.ID, f.service.DraftVersionID, "", "", priorVersionID), ErrToolMarketConflict)
}

func TestToolMarketCredentialUnavailableKeysFailClosedAndClearDoesNotNeedKey(t *testing.T) {
	f := newMarketCredentialFixture(t)
	for _, key := range []string{"", "short", "REPLACE_WITH_AT_LEAST_32_RANDOM_BYTES", strings.Repeat("a", 40)} {
		t.Setenv("TOOL_MARKET_ENCRYPTION_KEY", key)
		require.ErrorIs(t, ConfigureToolMarketCredential(f.owner.Id, f.service.ID, f.service.DraftVersionID, "bearer", marketCredentialTestSecret, ""), ErrToolMarketCredentialUnavailable)
		var count int64
		require.NoError(t, f.db.Model(&ToolMarketCredential{}).Count(&count).Error)
		require.Zero(t, count)
	}
	t.Setenv("TOOL_MARKET_ENCRYPTION_KEY", "")
	t.Setenv("CRYPTO_SECRET", marketCredentialTestKey)
	f.configure(t, "bearer", marketCredentialTestSecret)
	resolved, err := ResolveToolMarketCredential(f.owner.Id, f.service.ID, f.service.DraftVersionID, "https://example.com/mcp")
	require.NoError(t, err)
	require.Equal(t, marketCredentialTestSecret, resolved.Secret)
	t.Setenv("TOOL_MARKET_ENCRYPTION_KEY", "fedcba9876543210fedcba9876543210")
	_, err = ResolveToolMarketCredential(f.owner.Id, f.service.ID, f.service.DraftVersionID, "https://example.com/mcp")
	require.ErrorIs(t, err, ErrToolMarketCredentialUnavailable)
	t.Setenv("TOOL_MARKET_ENCRYPTION_KEY", "")
	t.Setenv("CRYPTO_SECRET", "")
	_, err = ResolveToolMarketCredential(f.owner.Id, f.service.ID, f.service.DraftVersionID, "https://example.com/mcp")
	require.ErrorIs(t, err, ErrToolMarketCredentialUnavailable)
	f.validated(t)
	f.configure(t, "none", "")
	f.requireUnvalidated(t)
	resolved, err = ResolveToolMarketCredential(f.owner.Id, f.service.ID, f.service.DraftVersionID, "https://example.com/mcp")
	require.NoError(t, err)
	require.Equal(t, "none", resolved.Mode)
	require.Empty(t, resolved.Secret)
	metadata, err := GetToolMarketCredentialMetadata(f.owner.Id, f.service.ID, f.service.DraftVersionID)
	require.NoError(t, err)
	require.False(t, metadata.Configured)
	require.Equal(t, "none", metadata.Mode)
}
