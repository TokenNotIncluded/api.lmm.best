package model

import (
	"context"
	"encoding/base64"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/go-redis/redis/v8"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func migrateUserAssistantData(t *testing.T) {
	t.Helper()
	require.NoError(t, DB.AutoMigrate(
		&UnifiedTodoRead{},
		&AssistantLead{},
		&AssistantUserProfile{},
		&AssistantUserProfileAudit{},
		&AssistantMemory{},
		&AdvancedSecurityEvent{},
		&DeveloperAccessRequest{},
		&AccountActionRequest{},
		&L1OnboardingTodo{},
		&PromptConversionRef{},
		&PromptConversationRef{},
		&AssistantConversation{}, &AssistantSupportRequest{},
		&AssistantHistoryMessage{},
		&AssistantSecureCard{},
		&AssistantSecurityIncident{},
		&AssistantNewUserGift{},
	))
	t.Cleanup(func() {
		for _, table := range []string{
			"unified_todo_reads", "assistant_leads", "assistant_user_profiles", "assistant_user_profile_audits", "assistant_memories",
			"advanced_security_events", "developer_access_requests", "account_action_requests", "l1_onboarding_todos",
			"assistant_pre_conversation_conversion_attributions",
			"assistant_pre_conversation_conversation_attributions", "assistant_history_messages",
			"assistant_secure_cards", "assistant_security_incidents", "assistant_conversations", "assistant_new_user_gifts",
		} {
			DB.Exec("DELETE FROM " + table)
		}
	})
}

func TestHardDeleteUserFailsClosedWhenAuthFenceCannotPublish(t *testing.T) {
	truncateTables(t)
	migrateUserAssistantData(t)

	user := User{Username: "hard-delete-user", Password: "password", TelegramId: "hard-delete-telegram"}
	require.NoError(t, DB.Create(&user).Error)
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		return ClaimExternalIdentityWithTx(tx, ExternalIdentityProviderTelegram, user.TelegramId, user.Id)
	}))
	require.NoError(t, DB.Create(&Token{UserId: user.Id, Key: "hard-delete-token"}).Error)
	require.NoError(t, DB.Create(&TwoFA{UserId: user.Id, Secret: "secret", IsEnabled: true}).Error)
	require.NoError(t, DB.Create(&TwoFABackupCode{UserId: user.Id, CodeHash: "hash"}).Error)
	require.NoError(t, DB.Create(&PasskeyCredential{UserID: user.Id, CredentialID: "credential", PublicKey: "public-key"}).Error)
	require.NoError(t, DB.Create(&UserOAuthBinding{UserId: user.Id, ProviderId: 1, ProviderUserId: "provider-user"}).Error)
	require.NoError(t, DB.Create(&UserSession{
		SID: "hard-delete-session", UserID: user.Id, Version: 1, UserAuthVersion: 1,
		Status: UserSessionStatusActive, RefreshHash: "refresh-hash", LoginMethod: "password",
		LastActiveAt: 1, ExpiresAt: 2,
	}).Error)
	require.NoError(t, DB.Create(&AuthFlow{
		TokenHash: "hard-delete-auth-flow", Purpose: AuthFlowPurposeTwoFALogin,
		UserId: user.Id, ExpiresAt: time.Now().Add(time.Minute),
	}).Error)

	oldRedisEnabled, oldRDB := common.RedisEnabled, common.RDB
	common.RedisEnabled = true
	common.RDB = redis.NewClient(&redis.Options{
		Dialer: func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("forced redis failure")
		},
		MaxRetries: -1,
	})
	t.Cleanup(func() {
		_ = common.RDB.Close()
		common.RedisEnabled, common.RDB = oldRedisEnabled, oldRDB
	})

	require.Error(t, HardDeleteUserById(user.Id))

	var count int64
	require.NoError(t, DB.Unscoped().Model(&User{}).Where("id = ?", user.Id).Count(&count).Error)
	assert.EqualValues(t, 1, count)
	for _, record := range []any{
		&Token{},
		&TwoFA{},
		&TwoFABackupCode{},
		&PasskeyCredential{},
		&UserOAuthBinding{},
		&UserSession{},
		&AuthFlow{},
		&ExternalIdentityClaim{},
	} {
		require.NoError(t, DB.Unscoped().Model(record).Where("user_id = ?", user.Id).Count(&count).Error)
		assert.EqualValues(t, 1, count)
	}
}

func TestHardDeleteUserPublishesTombstoneAndPurgesAuthenticationData(t *testing.T) {
	truncateTables(t)
	migrateUserAssistantData(t)
	server := useUserCacheMiniRedis(t)

	user := User{
		Username: "hard-delete-success", Password: "password", AuthVersion: 1,
		TelegramId: "hard-delete-success-telegram",
	}
	require.NoError(t, DB.Create(&user).Error)
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		return ClaimExternalIdentityWithTx(tx, ExternalIdentityProviderTelegram, user.TelegramId, user.Id)
	}))
	require.NoError(t, DB.Create(&Token{UserId: user.Id, Key: "hard-delete-success-token"}).Error)
	require.NoError(t, DB.Create(&TwoFA{UserId: user.Id, Secret: "secret", IsEnabled: true}).Error)
	require.NoError(t, DB.Create(&TwoFABackupCode{UserId: user.Id, CodeHash: "hash"}).Error)
	require.NoError(t, DB.Create(&PasskeyCredential{UserID: user.Id, CredentialID: "credential-success", PublicKey: "public-key"}).Error)
	require.NoError(t, DB.Create(&UserOAuthBinding{UserId: user.Id, ProviderId: 1, ProviderUserId: "provider-user-success"}).Error)
	require.NoError(t, DB.Create(&UserSession{
		SID: "hard-delete-success-session", UserID: user.Id, Version: 1, UserAuthVersion: 1,
		Status: UserSessionStatusActive, RefreshHash: "refresh-hash", LoginMethod: "password",
		LastActiveAt: 1, ExpiresAt: 2,
	}).Error)
	require.NoError(t, DB.Create(&AuthFlow{
		TokenHash: "hard-delete-success-flow", Purpose: AuthFlowPurposeTwoFALogin,
		UserId: user.Id, ExpiresAt: time.Now().Add(time.Minute),
	}).Error)
	viewer := User{Username: "hard-delete-viewer", Password: "password", Role: common.RoleAdminUser, AffCode: "hard-delete-viewer"}
	require.NoError(t, DB.Create(&viewer).Error)
	conversation := AssistantConversation{UserId: user.Id, Title: "private", LastMessagePreview: "private", CreatedAt: 1, UpdatedAt: 1}
	require.NoError(t, DB.Create(&conversation).Error)
	require.NoError(t, DB.Create(&AssistantHistoryMessage{ConversationId: conversation.Id, Sequence: 1, Role: AssistantHistoryRoleUser, Content: "private", CreatedAt: 1}).Error)
	require.NoError(t, DB.Create(&AssistantSecureCard{Id: "hard-delete-card", OwnerUserId: user.Id, ConversationId: conversation.Id, Type: AssistantSecureCardTypeAPIKey, Summary: "private", Ciphertext: "encrypted", CreatedAt: 1, ExpiresAt: 2}).Error)
	incident := AssistantSecurityIncident{UserId: user.Id, ConversationId: conversation.Id, Category: AssistantSecurityIncidentCategory, Status: AssistantSecurityIncidentStatusOpen, InputDigest: "digest", CreatedAt: 1, UpdatedAt: 1}
	require.NoError(t, DB.Create(&incident).Error)
	require.NoError(t, DB.Create(&UnifiedTodoRead{UserId: viewer.Id, Category: UnifiedTodoCategorySecurityIncident, ItemId: incident.Id, ReadAt: 1}).Error)
	require.NoError(t, DB.Create(&PromptConversationRef{ConversationId: conversation.Id, PresetId: "preset", Generation: 1, Version: "v1", UpdatedAt: 1}).Error)
	require.NoError(t, DB.Create(&AssistantLead{UserId: user.Id, Source: AssistantLeadSourceHandoff, Intent: AssistantIntentHumanSupport, Message: "private", Status: AssistantLeadStatusPending, CreatedAt: 1}).Error)
	resolvedLead := AssistantLead{UserId: viewer.Id, Source: AssistantLeadSourceHandoff, Intent: AssistantIntentHumanSupport, Message: "other", Status: AssistantLeadStatusResolved, AdminUserId: user.Id, CreatedAt: 1, ResolvedAt: 1}
	require.NoError(t, DB.Create(&resolvedLead).Error)
	require.NoError(t, DB.Create(&AssistantMemory{UserId: user.Id, Title: "private", Content: "private", TagsJSON: "[]", Source: AssistantMemorySourceAssistant, Enabled: true, UpdatedBy: user.Id, CreatedAt: 1, UpdatedAt: 1}).Error)
	require.NoError(t, DB.Create(&AssistantUserProfile{UserId: user.Id, ProfileKey: AssistantProfileNormal, TagsJSON: "[]", Strategy: "private", Source: AssistantProfileSourceAI, Enabled: true, UpdatedBy: user.Id, CreatedAt: 1, UpdatedAt: 1}).Error)
	require.NoError(t, DB.Create(&AdvancedSecurityEvent{UserID: user.Id, Username: user.Username, CreatedAt: 1, RuleID: "private", Category: "private"}).Error)
	require.NoError(t, DB.Create(&DeveloperAccessRequest{UserId: user.Id, Status: DeveloperAccessRequestPending, Source: DeveloperAccessRequestSourceAI, Reason: "private", CreatedAt: 1}).Error)
	require.NoError(t, DB.Create(&AccountActionRequest{TargetUserId: user.Id, RequestedByUserId: user.Id, Kind: AccountActionKindDisable, Status: AccountActionStatusPending, Reason: "private", CreatedAt: 1}).Error)
	require.NoError(t, DB.Create(&L1OnboardingTodo{UserId: user.Id, CreatedAt: 1, UpdatedAt: 1}).Error)
	require.NoError(t, populateUserCache(user))
	// Administrative hard deletion commonly targets an already soft-deleted
	// user; the shared version increment must therefore query unscoped.
	require.NoError(t, DB.Delete(&user).Error)

	require.NoError(t, HardDeleteUserById(user.Id))

	var count int64
	require.NoError(t, DB.Unscoped().Model(&User{}).Where("id = ?", user.Id).Count(&count).Error)
	assert.Zero(t, count)
	for _, record := range []any{
		&Token{},
		&TwoFA{},
		&TwoFABackupCode{},
		&PasskeyCredential{},
		&UserOAuthBinding{},
		&UserSession{},
		&AuthFlow{},
		&ExternalIdentityClaim{},
		&AssistantLead{},
		&AssistantUserProfile{},
		&AssistantMemory{},
		&PromptConversationRef{},
		&AssistantConversation{}, &AssistantSupportRequest{},
		&AssistantHistoryMessage{},
		&AssistantSecureCard{},
		&AssistantSecurityIncident{},
		&AdvancedSecurityEvent{},
		&DeveloperAccessRequest{},
		&L1OnboardingTodo{},
	} {
		column := "user_id"
		if _, ok := record.(*AssistantSecureCard); ok {
			column = "owner_user_id"
		}
		if _, ok := record.(*PromptConversationRef); ok {
			column = "conversation_id"
		}
		if _, ok := record.(*AssistantHistoryMessage); ok {
			column = "conversation_id"
		}
		reference := any(user.Id)
		if column == "conversation_id" {
			reference = conversation.Id
		}
		require.NoError(t, DB.Unscoped().Model(record).Where(column+" = ?", reference).Count(&count).Error)
		assert.Zero(t, count)
	}
	require.NoError(t, DB.Model(&AccountActionRequest{}).Where("target_user_id = ? OR requested_by_user_id = ?", user.Id, user.Id).Count(&count).Error)
	assert.Zero(t, count)
	require.NoError(t, DB.Model(&UnifiedTodoRead{}).Where("category = ? AND item_id = ?", UnifiedTodoCategorySecurityIncident, incident.Id).Count(&count).Error)
	assert.Zero(t, count)
	require.NoError(t, DB.First(&resolvedLead, resolvedLead.Id).Error)
	assert.Zero(t, resolvedLead.AdminUserId)
	assert.False(t, server.Exists(getUserAuthFenceKey(user.Id)))
	committed, err := common.RDB.Get(t.Context(), getUserAuthVersionKey(user.Id)).Result()
	require.NoError(t, err)
	assert.Equal(t, "2", committed)
	assert.False(t, server.Exists(getUserCacheKey(user.Id)))
}

func TestSoftDeleteUserPurgesPrivateAssistantState(t *testing.T) {
	truncateTables(t)
	migrateUserAssistantData(t)
	user := User{Username: "soft-delete-assistant", Password: "password", AffCode: "soft-delete-assistant", AuthVersion: 1}
	require.NoError(t, DB.Create(&user).Error)
	require.NoError(t, DB.Create(&AssistantMemory{
		UserId: user.Id, Title: "private", Content: "private", TagsJSON: "[]",
		Source: AssistantMemorySourceAssistant, Enabled: true, UpdatedBy: user.Id, CreatedAt: 1, UpdatedAt: 1,
	}).Error)
	require.NoError(t, user.Delete())

	var count int64
	require.NoError(t, DB.Unscoped().Model(&User{}).Where("id = ?", user.Id).Count(&count).Error)
	assert.EqualValues(t, 1, count)
	require.NoError(t, DB.Model(&AssistantMemory{}).Where("user_id = ?", user.Id).Count(&count).Error)
	assert.Zero(t, count)
}

func TestIncrementFailedAttemptsCountsConcurrentFailures(t *testing.T) {
	truncateTables(t)

	user := User{Username: "twofa-cas-user", Password: "password"}
	require.NoError(t, DB.Create(&user).Error)
	twoFA := TwoFA{UserId: user.Id, Secret: "secret", IsEnabled: true}
	require.NoError(t, DB.Create(&twoFA).Error)

	const attempts = 4
	errs := make(chan error, attempts)
	var wg sync.WaitGroup
	for range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- (&TwoFA{Id: twoFA.Id}).IncrementFailedAttempts()
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	var reloaded TwoFA
	require.NoError(t, DB.First(&reloaded, twoFA.Id).Error)
	assert.Equal(t, attempts, reloaded.FailedAttempts)
}

func TestValidateBackupCodeCanOnlySucceedOnce(t *testing.T) {
	truncateTables(t)

	const code = "ABCD-1234"
	user := User{Id: 123, Username: "backup-code-user", Password: "password", AuthVersion: 1}
	require.NoError(t, DB.Create(&user).Error)
	require.NoError(t, DB.Create(&TwoFA{UserId: user.Id, Secret: "secret", IsEnabled: false}).Error)
	require.NoError(t, CreatePendingTwoFASetupBackupCodes(user.Id, []string{code}))

	const attempts = 2
	results := make(chan bool, attempts)
	errs := make(chan error, attempts)
	var wg sync.WaitGroup
	for range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			valid, err := ValidateBackupCode(123, code)
			results <- valid
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)

	for err := range errs {
		require.NoError(t, err)
	}
	wins := 0
	for valid := range results {
		if valid {
			wins++
		}
	}
	assert.Equal(t, 1, wins)

	remaining, err := GetUnusedBackupCodeCount(123)
	require.NoError(t, err)
	assert.Zero(t, remaining)
}

func TestPendingTwoFASetupAPIsRejectEnabledFactor(t *testing.T) {
	truncateTables(t)

	user := User{Username: "enabled-twofa-guard", Password: "password", AuthVersion: 1}
	require.NoError(t, DB.Create(&user).Error)
	twoFA := TwoFA{UserId: user.Id, Secret: "secret", IsEnabled: true}
	require.NoError(t, DB.Create(&twoFA).Error)

	require.Error(t, CreatePendingTwoFASetupBackupCodes(user.Id, []string{"ABCD-1234"}))
	require.Error(t, twoFA.DeletePendingTwoFASetup())

	var stored TwoFA
	require.NoError(t, DB.First(&stored, twoFA.Id).Error)
	assert.True(t, stored.IsEnabled)
	var backupCodeCount int64
	require.NoError(t, DB.Model(&TwoFABackupCode{}).Where("user_id = ?", user.Id).Count(&backupCodeCount).Error)
	assert.Zero(t, backupCodeCount)
}

func TestSecurityFactorMutationsAdvanceUserAuthVersion(t *testing.T) {
	truncateTables(t)

	user := User{
		Username:    "security-factor-version-user",
		Password:    "password",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Group:       "default",
		AuthVersion: 1,
	}
	require.NoError(t, DB.Create(&user).Error)
	twoFA := TwoFA{UserId: user.Id, Secret: "secret", IsEnabled: false}
	require.NoError(t, DB.Create(&twoFA).Error)

	require.NoError(t, twoFA.EnableWithAuthVersion())
	assertUserAuthVersion(t, user.Id, 2)
	assert.ErrorIs(t, twoFA.EnableWithAuthVersion(), ErrTwoFAAlreadyEnabled)
	assertUserAuthVersion(t, user.Id, 2)
	require.NoError(t, ReplaceBackupCodesWithAuthVersion(user.Id, []string{"ABCD-1234"}))
	assertUserAuthVersion(t, user.Id, 3)
	require.NoError(t, DisableTwoFAWithAuthVersion(user.Id))
	assertUserAuthVersion(t, user.Id, 4)

	credential := &PasskeyCredential{UserID: user.Id, CredentialID: "credential-id", PublicKey: "public-key"}
	require.NoError(t, UpsertPasskeyCredentialWithAuthVersion(credential))
	assertUserAuthVersion(t, user.Id, 5)
	require.NoError(t, DeletePasskeyByUserIDWithAuthVersion(user.Id))
	assertUserAuthVersion(t, user.Id, 6)
}

func TestUpdatePasskeyAssertionStateCannotRewriteRegistrationIdentity(t *testing.T) {
	truncateTables(t)

	user := User{Username: "passkey-assertion-state", Password: "password", AuthVersion: 1}
	require.NoError(t, DB.Create(&user).Error)
	credentialID := []byte("stable-credential-id")
	stored := PasskeyCredential{
		UserID:          user.Id,
		CredentialID:    base64.StdEncoding.EncodeToString(credentialID),
		PublicKey:       "original-public-key",
		AttestationType: "packed",
		AAGUID:          "original-aaguid",
		SignCount:       1,
		Transports:      `["usb"]`,
		Attachment:      "platform",
	}
	require.NoError(t, DB.Create(&stored).Error)
	usedAt := time.Now().UTC().Truncate(time.Second)
	validated := &webauthn.Credential{
		ID:              credentialID,
		PublicKey:       []byte("replacement-public-key"),
		AttestationType: "none",
		Flags: webauthn.CredentialFlags{
			UserPresent:    true,
			UserVerified:   true,
			BackupEligible: true,
			BackupState:    true,
		},
		Authenticator: webauthn.Authenticator{
			AAGUID:       []byte("replacement-aaguid"),
			SignCount:    8,
			CloneWarning: true,
		},
	}
	require.NoError(t, UpdatePasskeyAssertionState(user.Id, validated, usedAt))

	var updated PasskeyCredential
	require.NoError(t, DB.First(&updated, stored.ID).Error)
	assert.Equal(t, stored.CredentialID, updated.CredentialID)
	assert.Equal(t, stored.PublicKey, updated.PublicKey)
	assert.Equal(t, stored.AttestationType, updated.AttestationType)
	assert.Equal(t, stored.AAGUID, updated.AAGUID)
	assert.Equal(t, stored.Transports, updated.Transports)
	assert.Equal(t, stored.Attachment, updated.Attachment)
	assert.EqualValues(t, 8, updated.SignCount)
	assert.True(t, updated.CloneWarning)
	assert.True(t, updated.UserPresent)
	assert.True(t, updated.UserVerified)
	assert.True(t, updated.BackupEligible)
	assert.True(t, updated.BackupState)
	require.NotNil(t, updated.LastUsedAt)
	assert.Equal(t, usedAt.Unix(), updated.LastUsedAt.Unix())

	validated.ID = []byte("another-credential")
	assert.ErrorIs(t, UpdatePasskeyAssertionState(user.Id, validated, usedAt), ErrPasskeyNotFound)
}

func assertUserAuthVersion(t *testing.T, userID int, expected int64) {
	t.Helper()
	var version int64
	require.NoError(t, DB.Model(&User{}).Where("id = ?", userID).Select("auth_version").Scan(&version).Error)
	assert.Equal(t, expected, version)
}
