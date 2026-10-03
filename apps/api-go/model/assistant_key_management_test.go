package model

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupAssistantKeyManagement(t *testing.T) (*User, *Token, AssistantKeyAuthorizationFence) {
	t.Helper()
	db := setupConsoleActivationTestDB(t)
	require.NoError(t, db.AutoMigrate(&AuthFlow{}, &UserSession{}, &TwoFA{}, &TwoFABackupCode{}))
	user := &User{Username: "key-owner", AffCode: "key-owner-aff", Status: common.UserStatusEnabled, AuthVersion: 1}
	require.NoError(t, db.Create(user).Error)
	session := UserSession{SID: "key-owner-session", UserID: user.Id, Version: 1, UserAuthVersion: 1,
		Status: UserSessionStatusActive, ExpiresAt: time.Now().Add(time.Hour).Unix(), RefreshHash: "hash", LoginMethod: "password"}
	require.NoError(t, db.Create(&session).Error)
	allowedIPs := "private-ip-limits"
	token := &Token{UserId: user.Id, Key: "never-send-this-credential", Name: "key-to-revoke", Status: common.TokenStatusEnabled,
		Group: "default", CreatedTime: 100, AccessedTime: 200, ExpiredTime: -1, RemainQuota: 70, UsedQuota: 30,
		UnlimitedQuota: false, ModelLimits: "private-model-limits", ModelLimitsEnabled: true, AllowIps: &allowedIPs,
		CrossGroupRetry: true, AutoGroups: `["default","vip"]`, CreationSource: TokenCreationSourceManual}
	require.NoError(t, db.Create(token).Error)
	fence, err := NewAssistantKeyAuthorizationFence(user.Id, session.SID, 1, 1, DeveloperAccessPolicy{})
	require.NoError(t, err)
	return user, token, fence
}

func prepareAssistantKeyManagementTestFlow(t *testing.T, userID int, token *Token, operation string) string {
	t.Helper()
	payload, err := json.Marshal(AssistantKeyManagementDraft{Version: AssistantKeyManagementDraftVersion, Operation: operation, Key: *assistantKeyMetadata(token)})
	require.NoError(t, err)
	confirmation, _, err := CreateAuthFlow(AuthFlowCreate{Purpose: AuthFlowPurposeAssistantKeyManagement,
		UserId: userID, SessionId: "key-owner-session", Payload: string(payload), ExpiresAt: time.Now().Add(time.Minute)})
	require.NoError(t, err)
	return confirmation
}

func assertAssistantKeyManagementFlowFresh(t *testing.T, confirmation string) {
	t.Helper()
	flow, err := GetAuthFlow(confirmation, AuthFlowMatch{Purpose: AuthFlowPurposeAssistantKeyManagement})
	require.NoError(t, err)
	assert.Nil(t, flow.ConsumedAt)
}

func assertAssistantKeyMetadataAllowlist(t *testing.T, metadata any) {
	t.Helper()
	payload, err := json.Marshal(metadata)
	require.NoError(t, err)
	var fields map[string]any
	require.NoError(t, json.Unmarshal(payload, &fields))
	assert.Len(t, fields, 7)
	for _, allowed := range []string{"id", "name", "status", "group", "created_time", "accessed_time", "expired_time"} {
		assert.Contains(t, fields, allowed)
	}
	for _, secret := range []string{"never-send-this-credential", "private-ip-limits", "private-model-limits", "remain_quota", "used_quota", "key", "user_id"} {
		assert.NotContains(t, fields, secret)
		if secret != "key" { // The public name can contain the word key.
			assert.NotContains(t, string(payload), secret)
		}
	}
}

func TestAssistantKeyMetadataIsOwnAllowlistAndExactPaginated(t *testing.T) {
	user, token, _ := setupAssistantKeyManagement(t)
	items := []Token{
		{UserId: user.Id, Key: "literal-wildcard", Name: "%literal_", Status: common.TokenStatusEnabled},
		{UserId: user.Id + 1, Key: "foreign", Name: token.Name},
		{UserId: user.Id, Key: "oauth", Name: token.Name, OAuthManaged: true},
		{UserId: user.Id, Key: "runtime", Name: token.Name, CreationSource: TokenCreationSourceAssistantRuntime},
		{UserId: user.Id, Key: "deleted", Name: token.Name},
	}
	require.NoError(t, DB.Create(&items).Error)
	require.NoError(t, DB.Delete(&items[4]).Error)
	page, total, err := ListAssistantKeyMetadata(user.Id, 0, 1, "")
	require.NoError(t, err)
	assert.EqualValues(t, 2, total)
	require.Len(t, page, 1)
	assert.Equal(t, items[0].Id, page[0].ID)
	assertAssistantKeyMetadataAllowlist(t, page[0])
	page, total, err = ListAssistantKeyMetadata(user.Id, 1, 1, "")
	require.NoError(t, err)
	assert.EqualValues(t, 2, total)
	require.Len(t, page, 1)
	assert.Equal(t, token.Id, page[0].ID)
	page, total, err = ListAssistantKeyMetadata(user.Id, 0, 50, "%literal_")
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	require.Len(t, page, 1)
	assert.Equal(t, items[0].Id, page[0].ID)
	for _, excluded := range items[1:] {
		_, err := GetAssistantKeyMetadataByID(user.Id, excluded.Id)
		assert.ErrorIs(t, err, ErrAssistantKeyTargetChanged)
	}
	metadata, err := GetAssistantKeyMetadataByID(user.Id, token.Id)
	require.NoError(t, err)
	assertAssistantKeyMetadataAllowlist(t, metadata)
	for _, bounds := range [][3]int{{0, 0, 20}, {user.Id, -1, 20}, {user.Id, 0, 0}, {user.Id, 0, 51}} {
		_, _, err := ListAssistantKeyMetadata(bounds[0], bounds[1], bounds[2], "")
		assert.ErrorIs(t, err, gorm.ErrInvalidData)
	}
}

func TestAssistantKeyDisablePreservesConcurrentQuotaAndFencesCache(t *testing.T) {
	user, token, fence := setupAssistantKeyManagement(t)
	server := useUserCacheMiniRedis(t)
	confirmation := prepareAssistantKeyManagementTestFlow(t, user.Id, token, AssistantKeyOperationDisable)
	require.NoError(t, cacheSetTokenForTest(*token))
	require.NoError(t, DB.Model(token).Updates(map[string]any{"remain_quota": 37, "used_quota": 63, "accessed_time": 300}).Error)
	metadata, operation, err := ConsumeAssistantKeyManagementFlow(confirmation, fence, "")
	require.NoError(t, err)
	assert.Equal(t, AssistantKeyOperationDisable, operation)
	assert.Equal(t, common.TokenStatusDisabled, metadata.Status)
	assert.EqualValues(t, 300, metadata.AccessedTime)
	assertAssistantKeyMetadataAllowlist(t, metadata)
	var persisted Token
	require.NoError(t, DB.First(&persisted, token.Id).Error)
	assert.Equal(t, common.TokenStatusDisabled, persisted.Status)
	assert.Equal(t, 37, persisted.RemainQuota)
	assert.Equal(t, 63, persisted.UsedQuota)
	assert.Equal(t, token.Key, persisted.Key)
	assert.Equal(t, token.AllowIps, persisted.AllowIps)
	assert.Equal(t, token.ModelLimits, persisted.ModelLimits)
	assert.Equal(t, token.AutoGroups, persisted.AutoGroups)
	assert.Equal(t, token.CrossGroupRetry, persisted.CrossGroupRetry)
	assert.False(t, server.Exists(getTokenCacheKey(token.Key)))
	assert.True(t, server.Exists(getTokenCacheFenceKey(token.Key)))
	initialized, err := cacheInitToken(*token)
	require.NoError(t, err)
	assert.Zero(t, initialized, "a stale enabled snapshot must not repopulate the cache")
	_, _, err = ConsumeAssistantKeyManagementFlow(confirmation, fence, "")
	assert.ErrorIs(t, err, ErrAuthFlowConsumed)
}

func TestAssistantKeyDeleteIsSoftDeleteAndPreservesCredentialAccounting(t *testing.T) {
	user, token, fence := setupAssistantKeyManagement(t)
	confirmation := prepareAssistantKeyManagementTestFlow(t, user.Id, token, AssistantKeyOperationDelete)
	metadata, operation, err := ConsumeAssistantKeyManagementFlow(confirmation, fence, "")
	require.NoError(t, err)
	assert.Equal(t, AssistantKeyOperationDelete, operation)
	assert.Equal(t, token.Id, metadata.ID)
	assertAssistantKeyMetadataAllowlist(t, metadata)
	var persisted Token
	require.NoError(t, DB.Unscoped().First(&persisted, token.Id).Error)
	assert.True(t, persisted.DeletedAt.Valid)
	assert.Equal(t, token.RemainQuota, persisted.RemainQuota)
	assert.Equal(t, token.UsedQuota, persisted.UsedQuota)
	assert.Equal(t, token.Key, persisted.Key)
	_, err = GetAssistantKeyMetadataByID(user.Id, token.Id)
	assert.ErrorIs(t, err, ErrAssistantKeyTargetChanged)
	_, _, err = ConsumeAssistantKeyManagementFlow(confirmation, fence, "")
	assert.ErrorIs(t, err, ErrAuthFlowConsumed)
}

func TestAssistantKeyDisableAlreadyDisabledConsumesOneConfirmation(t *testing.T) {
	user, token, fence := setupAssistantKeyManagement(t)
	require.NoError(t, DB.Model(token).Update("status", common.TokenStatusDisabled).Error)
	token.Status = common.TokenStatusDisabled
	confirmation := prepareAssistantKeyManagementTestFlow(t, user.Id, token, AssistantKeyOperationDisable)
	updateCount := 0
	callbackName := "assistant-test-no-redundant-disable"
	require.NoError(t, DB.Callback().Update().Before("gorm:update").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Table == "tokens" {
			updateCount++
		}
	}))
	t.Cleanup(func() { _ = DB.Callback().Update().Remove(callbackName) })
	metadata, operation, err := ConsumeAssistantKeyManagementFlow(confirmation, fence, "")
	require.NoError(t, err)
	assert.Equal(t, AssistantKeyOperationDisable, operation)
	assert.Equal(t, common.TokenStatusDisabled, metadata.Status)
	assert.Zero(t, updateCount, "avoid no-op UPDATE affected-row differences on MySQL")
	_, _, err = ConsumeAssistantKeyManagementFlow(confirmation, fence, "")
	assert.ErrorIs(t, err, ErrAuthFlowConsumed)
}

func TestAssistantKeyManagementRejectsForeignAndManagedTargets(t *testing.T) {
	for _, target := range []string{"foreign", "oauth", "runtime", "deleted"} {
		t.Run(target, func(t *testing.T) {
			user, token, fence := setupAssistantKeyManagement(t)
			confirmation := prepareAssistantKeyManagementTestFlow(t, user.Id, token, AssistantKeyOperationDelete)
			switch target {
			case "foreign":
				require.NoError(t, DB.Model(token).Update("user_id", user.Id+1).Error)
			case "oauth":
				require.NoError(t, DB.Model(token).Update("oauth_managed", true).Error)
			case "runtime":
				require.NoError(t, DB.Model(token).Update("creation_source", TokenCreationSourceAssistantRuntime).Error)
			case "deleted":
				require.NoError(t, DB.Delete(token).Error)
			}
			_, _, err := ConsumeAssistantKeyManagementFlow(confirmation, fence, "")
			assert.ErrorIs(t, err, ErrAssistantKeyTargetChanged)
			assertAssistantKeyManagementFlowFresh(t, confirmation)
		})
	}
}

func TestAssistantKeyManagementRejectsStaleDisplayIdentity(t *testing.T) {
	for field, value := range map[string]any{"name": "renamed", "group": "vip", "status": common.TokenStatusDisabled} {
		t.Run(field, func(t *testing.T) {
			user, token, fence := setupAssistantKeyManagement(t)
			confirmation := prepareAssistantKeyManagementTestFlow(t, user.Id, token, AssistantKeyOperationDelete)
			require.NoError(t, DB.Model(token).Update(field, value).Error)
			_, _, err := ConsumeAssistantKeyManagementFlow(confirmation, fence, "")
			assert.ErrorIs(t, err, ErrAssistantKeyTargetChanged)
			assertAssistantKeyManagementFlowFresh(t, confirmation)
			var persisted Token
			require.NoError(t, DB.First(&persisted, token.Id).Error)
			assert.False(t, persisted.DeletedAt.Valid)
		})
	}
}

func TestAssistantKeyManagementRejectsChangedAuthorization(t *testing.T) {
	cases := []struct {
		name   string
		change func(*testing.T, *User)
	}{
		{"user_disabled", func(t *testing.T, user *User) {
			require.NoError(t, DB.Model(user).Update("status", common.UserStatusDisabled).Error)
		}},
		{"user_auth_version", func(t *testing.T, user *User) { require.NoError(t, DB.Model(user).Update("auth_version", 2).Error) }},
		{"user_deleted", func(t *testing.T, user *User) { require.NoError(t, DB.Delete(user).Error) }},
		{"session_revoked", func(t *testing.T, user *User) {
			require.NoError(t, DB.Model(&UserSession{}).Where("user_id = ?", user.Id).Update("status", UserSessionStatusRevoked).Error)
		}},
		{"session_version", func(t *testing.T, user *User) {
			require.NoError(t, DB.Model(&UserSession{}).Where("user_id = ?", user.Id).Update("version", 2).Error)
		}},
		{"session_auth_version", func(t *testing.T, user *User) {
			require.NoError(t, DB.Model(&UserSession{}).Where("user_id = ?", user.Id).Update("user_auth_version", 2).Error)
		}},
		{"session_expired", func(t *testing.T, user *User) {
			require.NoError(t, DB.Model(&UserSession{}).Where("user_id = ?", user.Id).Update("expires_at", time.Now().Unix()-1).Error)
		}},
		{"session_revoked_at", func(t *testing.T, user *User) {
			require.NoError(t, DB.Model(&UserSession{}).Where("user_id = ?", user.Id).Update("revoked_at", time.Now().Unix()).Error)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			user, token, fence := setupAssistantKeyManagement(t)
			confirmation := prepareAssistantKeyManagementTestFlow(t, user.Id, token, AssistantKeyOperationDisable)
			tc.change(t, user)
			_, _, err := ConsumeAssistantKeyManagementFlow(confirmation, fence, "")
			assert.ErrorIs(t, err, ErrAssistantKeyAuthorizationChanged)
			assertAssistantKeyManagementFlowFresh(t, confirmation)
			var persisted Token
			require.NoError(t, DB.First(&persisted, token.Id).Error)
			assert.Equal(t, common.TokenStatusEnabled, persisted.Status)
		})
	}
}

func TestAssistantKeyManagementFlowCannotCrossOwnerSessionOrPurpose(t *testing.T) {
	user, token, fence := setupAssistantKeyManagement(t)
	confirmation := prepareAssistantKeyManagementTestFlow(t, user.Id, token, AssistantKeyOperationDisable)
	otherOwner, err := NewAssistantKeyAuthorizationFence(user.Id+1, "key-owner-session", 1, 1, DeveloperAccessPolicy{})
	require.NoError(t, err)
	otherSession, err := NewAssistantKeyAuthorizationFence(user.Id, "another-session", 1, 1, DeveloperAccessPolicy{})
	require.NoError(t, err)
	for _, wrongFence := range []AssistantKeyAuthorizationFence{otherOwner, otherSession, {}} {
		_, _, err := ConsumeAssistantKeyManagementFlow(confirmation, wrongFence, "")
		assert.ErrorIs(t, err, ErrAuthFlowInvalid)
	}
	assertAssistantKeyManagementFlowFresh(t, confirmation)
	require.NoError(t, DB.Model(&AuthFlow{}).Where("token_hash = ?", authFlowTokenHash(confirmation)).Update("purpose", AuthFlowPurposeAssistantKey).Error)
	_, _, err = ConsumeAssistantKeyManagementFlow(confirmation, fence, "")
	assert.ErrorIs(t, err, ErrAuthFlowInvalid)
}

func TestAssistantKeyManagementChecksLockedAutoLogoutPreferenceAndSessionAge(t *testing.T) {
	for _, enableAfterPreparation := range []bool{false, true} {
		name := "old_session_opted_out"
		if enableAfterPreparation {
			name = "enabled_after_preparation"
		}
		t.Run(name, func(t *testing.T) {
			user, token, fence := setupAssistantKeyManagement(t)
			require.NoError(t, DB.Model(user).Update("setting", `{"session_auto_logout":false}`).Error)
			require.NoError(t, DB.Model(&UserSession{}).Where("user_id = ?", user.Id).
				Update("created_at", time.Now().Add(-UserSessionAutoLogoutAge-time.Hour).Unix()).Error)
			confirmation := prepareAssistantKeyManagementTestFlow(t, user.Id, token, AssistantKeyOperationDisable)
			if enableAfterPreparation {
				require.NoError(t, DB.Model(user).Update("setting", `{"session_auto_logout":true}`).Error)
			}
			metadata, _, err := ConsumeAssistantKeyManagementFlow(confirmation, fence, "")
			var persisted Token
			require.NoError(t, DB.First(&persisted, token.Id).Error)
			if enableAfterPreparation {
				assert.ErrorIs(t, err, ErrAssistantKeyAuthorizationChanged)
				assert.Nil(t, metadata)
				assert.Equal(t, common.TokenStatusEnabled, persisted.Status)
				assertAssistantKeyManagementFlowFresh(t, confirmation)
			} else {
				require.NoError(t, err)
				assert.Equal(t, common.TokenStatusDisabled, metadata.Status)
				assert.Equal(t, common.TokenStatusDisabled, persisted.Status)
			}
		})
	}
}

func TestAssistantKeyManagementChecksCurrentTwoFactorAndConsumesBackupOnce(t *testing.T) {
	user, token, fence := setupAssistantKeyManagement(t)
	confirmation := prepareAssistantKeyManagementTestFlow(t, user.Id, token, AssistantKeyOperationDisable)
	// A factor enabled after preparation must be checked at confirmation.
	factor := TwoFA{UserId: user.Id, Secret: "JBSWY3DPEHPK3PXP", IsEnabled: true}
	require.NoError(t, DB.Create(&factor).Error)
	backupHash, err := common.Password2Hash("ABCD-EFGH")
	require.NoError(t, err)
	backup := TwoFABackupCode{UserId: user.Id, CodeHash: backupHash}
	require.NoError(t, DB.Create(&backup).Error)
	_, _, err = ConsumeAssistantKeyManagementFlow(confirmation, fence, "")
	assert.ErrorIs(t, err, ErrAssistantKeyTwoFactorInvalid)
	assertAssistantKeyManagementFlowFresh(t, confirmation)
	require.NoError(t, DB.First(&factor, factor.Id).Error)
	assert.Equal(t, 1, factor.FailedAttempts)
	metadata, _, err := ConsumeAssistantKeyManagementFlow(confirmation, fence, "ABCD-EFGH")
	require.NoError(t, err)
	assert.Equal(t, common.TokenStatusDisabled, metadata.Status)
	require.NoError(t, DB.First(&backup, backup.Id).Error)
	assert.True(t, backup.IsUsed)
	require.NoError(t, DB.First(&factor, factor.Id).Error)
	assert.Zero(t, factor.FailedAttempts)
	var disabled Token
	require.NoError(t, DB.First(&disabled, token.Id).Error)
	secondConfirmation := prepareAssistantKeyManagementTestFlow(t, user.Id, &disabled, AssistantKeyOperationDelete)
	_, _, err = ConsumeAssistantKeyManagementFlow(secondConfirmation, fence, "ABCD-EFGH")
	assert.ErrorIs(t, err, ErrAssistantKeyTwoFactorInvalid)
	assertAssistantKeyManagementFlowFresh(t, secondConfirmation)
	require.NoError(t, DB.First(&disabled, token.Id).Error)
	assert.False(t, disabled.DeletedAt.Valid)
}

func TestAssistantKeyManagementCacheFailureRollsBackFlowAndRevocation(t *testing.T) {
	user, token, fence := setupAssistantKeyManagement(t)
	useUserCacheMiniRedis(t)
	confirmation := prepareAssistantKeyManagementTestFlow(t, user.Id, token, AssistantKeyOperationDelete)
	require.NoError(t, common.RDB.Close())
	_, _, err := ConsumeAssistantKeyManagementFlow(confirmation, fence, "")
	require.Error(t, err)
	assertAssistantKeyManagementFlowFresh(t, confirmation)
	var persisted Token
	require.NoError(t, DB.First(&persisted, token.Id).Error)
	assert.False(t, persisted.DeletedAt.Valid)
}

func TestAssistantKeyManagementInvalidPayloadAndExpiryCannotMutate(t *testing.T) {
	for _, invalid := range []string{"unknown_version", "create", "unknown_field", "trailing_json", "expired"} {
		t.Run(invalid, func(t *testing.T) {
			user, token, fence := setupAssistantKeyManagement(t)
			confirmation := prepareAssistantKeyManagementTestFlow(t, user.Id, token, AssistantKeyOperationDelete)
			query := DB.Model(&AuthFlow{}).Where("token_hash = ?", authFlowTokenHash(confirmation))
			if invalid == "expired" {
				require.NoError(t, query.Update("expires_at", time.Now().Add(-time.Second)).Error)
			} else {
				var flow AuthFlow
				require.NoError(t, query.First(&flow).Error)
				var draft map[string]any
				require.NoError(t, json.Unmarshal([]byte(flow.Payload), &draft))
				switch invalid {
				case "unknown_version":
					draft["version"] = 2
				case "create":
					draft["operation"] = "create"
				case "unknown_field":
					draft["key"].(map[string]any)["key"] = "injected-secret"
				}
				payload, err := json.Marshal(draft)
				require.NoError(t, err)
				if invalid == "trailing_json" {
					payload = append(payload, []byte(` {"operation":"create"}`)...)
				}
				require.NoError(t, query.Update("payload", string(payload)).Error)
			}
			_, _, err := ConsumeAssistantKeyManagementFlow(confirmation, fence, "")
			if invalid == "expired" {
				assert.ErrorIs(t, err, ErrAuthFlowExpired)
			} else {
				assert.ErrorIs(t, err, ErrAuthFlowInvalid)
			}
			var persisted Token
			require.NoError(t, DB.First(&persisted, token.Id).Error)
			assert.False(t, persisted.DeletedAt.Valid)
			var flow AuthFlow
			require.NoError(t, DB.Where("token_hash = ?", authFlowTokenHash(confirmation)).First(&flow).Error)
			assert.Nil(t, flow.ConsumedAt)
		})
	}
}

func TestAssistantKeyManagementStaleTargetRollsBackAcceptedBackupCode(t *testing.T) {
	user, token, fence := setupAssistantKeyManagement(t)
	confirmation := prepareAssistantKeyManagementTestFlow(t, user.Id, token, AssistantKeyOperationDelete)
	require.NoError(t, DB.Create(&TwoFA{UserId: user.Id, Secret: "JBSWY3DPEHPK3PXP", IsEnabled: true}).Error)
	backupHash, err := common.Password2Hash("ABCD-EFGH")
	require.NoError(t, err)
	backup := TwoFABackupCode{UserId: user.Id, CodeHash: backupHash}
	require.NoError(t, DB.Create(&backup).Error)
	require.NoError(t, DB.Model(token).Update("name", "changed-key").Error)
	_, _, err = ConsumeAssistantKeyManagementFlow(confirmation, fence, "ABCD-EFGH")
	assert.ErrorIs(t, err, ErrAssistantKeyTargetChanged)
	assertAssistantKeyManagementFlowFresh(t, confirmation)
	require.NoError(t, DB.First(&backup, backup.Id).Error)
	assert.False(t, backup.IsUsed, "rollback accepted backup when the final mutation cannot commit")
}

func TestAssistantKeyManagementRefreshesFenceAfterLongMutation(t *testing.T) {
	user, token, fence := setupAssistantKeyManagement(t)
	server := useUserCacheMiniRedis(t)
	confirmation := prepareAssistantKeyManagementTestFlow(t, user.Id, token, AssistantKeyOperationDisable)
	callbackName := "assistant-test-expired-in-flight-fence"
	require.NoError(t, DB.Callback().Update().After("gorm:update").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Table != "tokens" {
			return
		}
		server.FastForward(time.Duration(tokenCacheFenceSeconds+1) * time.Second)
		initialized, err := cacheInitToken(*token)
		require.NoError(t, err)
		assert.Equal(t, 1, initialized)
	}))
	t.Cleanup(func() { _ = DB.Callback().Update().Remove(callbackName) })
	_, _, err := ConsumeAssistantKeyManagementFlow(confirmation, fence, "")
	require.NoError(t, err)
	assert.False(t, server.Exists(getTokenCacheKey(token.Key)))
	assert.True(t, server.Exists(getTokenCacheFenceKey(token.Key)))
}
