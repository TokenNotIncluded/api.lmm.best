package service

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func oldLoginSessionFixture(user *model.User, sid string, createdAt, now int64) model.UserSession {
	return model.UserSession{
		SID: sid, UserID: user.Id, Version: 1, UserAuthVersion: user.AuthVersion,
		Status: model.UserSessionStatusActive, RefreshHash: "hash-" + sid, LoginMethod: "password",
		CreatedAt: createdAt, LastActiveAt: now, ExpiresAt: now + int64((24*time.Hour)/time.Second),
	}
}

func TestWeekOldSessionCleanupBoundaryAndUserScope(t *testing.T) {
	user := setupAuthSessionTestDB(t)
	other := &model.User{Username: "other-session-user", AffCode: "other-session", AuthVersion: 1, Status: common.UserStatusEnabled}
	optedOut := &model.User{Username: "opted-out-user", AffCode: "opted-out", AuthVersion: 1, Status: common.UserStatusEnabled, Setting: `{"session_auto_logout":false}`}
	require.NoError(t, model.DB.Create(other).Error)
	require.NoError(t, model.DB.Create(optedOut).Error)
	now := time.Now().Unix()
	cutoff := now - int64(model.UserSessionAutoLogoutAge/time.Second)
	rows := []model.UserSession{
		oldLoginSessionFixture(user, "over-one-week", cutoff-1, now),
		oldLoginSessionFixture(user, "exactly-one-week", cutoff, now),
		oldLoginSessionFixture(user, "under-one-week", cutoff+1, now),
		oldLoginSessionFixture(other, "other-user-old", cutoff-1, now),
		oldLoginSessionFixture(optedOut, "opted-out-old", cutoff-1, now),
		oldLoginSessionFixture(user, "already-revoked", cutoff-1, now),
	}
	rows[5].Status = model.UserSessionStatusRevoked
	rows[5].RevokedAt = now - 120
	rows[5].RevokedReason = "manual_logout"
	require.NoError(t, model.DB.Create(&rows).Error)

	require.NoError(t, model.RevokeWeekOldUserSessions(user.Id, now))
	for _, row := range rows {
		stored, err := model.GetUserSessionBySID(row.SID)
		require.NoError(t, err)
		if row.SID == "over-one-week" {
			assert.Equal(t, model.UserSessionStatusRevoked, stored.Status)
			assert.Positive(t, stored.RevokedAt)
			assert.NotEmpty(t, stored.RevokedReason)
		} else {
			assert.Equal(t, row.Status, stored.Status, row.SID)
			assert.Equal(t, row.RevokedAt, stored.RevokedAt, row.SID)
			assert.Equal(t, row.RevokedReason, stored.RevokedReason, row.SID)
		}
		assert.Equal(t, row.CreatedAt, stored.CreatedAt)
		assert.Equal(t, now, stored.LastActiveAt, "recent activity must not extend the login age")
	}

	require.NoError(t, model.RevokeWeekOldUserSessions(0, now))
	otherSession, err := model.GetUserSessionBySID("other-user-old")
	require.NoError(t, err)
	assert.Equal(t, model.UserSessionStatusRevoked, otherSession.Status)
	optedOutSession, err := model.GetUserSessionBySID("opted-out-old")
	require.NoError(t, err)
	assert.Equal(t, model.UserSessionStatusActive, optedOutSession.Status)
	var count int64
	require.NoError(t, model.DB.Model(&model.UserSession{}).Count(&count).Error)
	assert.Equal(t, int64(len(rows)), count, "automatic logout retains the session audit records")
}

func TestSessionAutoLogoutSettingPreservesPreferencesAndCache(t *testing.T) {
	user := setupAuthSessionTestDB(t)
	settings := `{"language":"en","quota_warning_threshold":42,"future_setting":{"enabled":true}}`
	require.NoError(t, model.DB.Model(user).Update("setting", settings).Error)
	useIndependentAuthSessionRedis(t)
	cached, err := model.GetUserCache(user.Id)
	require.NoError(t, err)
	assert.True(t, cached.GetSetting().IsSessionAutoLogoutEnabled())

	for _, enabled := range []bool{false, true} {
		require.NoError(t, model.UpdateUserSessionAutoLogout(user.Id, enabled))
		stored, err := model.GetUserById(user.Id, true)
		require.NoError(t, err)
		var actual map[string]json.RawMessage
		require.NoError(t, json.Unmarshal([]byte(stored.Setting), &actual))
		assert.JSONEq(t, `"en"`, string(actual["language"]))
		assert.JSONEq(t, `42`, string(actual["quota_warning_threshold"]))
		assert.JSONEq(t, `{"enabled":true}`, string(actual["future_setting"]))
		assert.JSONEq(t, fmt.Sprint(enabled), string(actual["session_auto_logout"]))
		cached, err = model.GetUserCache(user.Id)
		require.NoError(t, err)
		assert.Equal(t, enabled, cached.GetSetting().IsSessionAutoLogoutEnabled(), "cached authentication must see the updated preference")
	}
}

func TestSessionAutoLogoutEnforcedForCachedAccessAndRefresh(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		for _, operation := range []string{"access", "refresh"} {
			t.Run(fmt.Sprintf("enabled=%t/%s", enabled, operation), func(t *testing.T) {
				useTestSessionSecret(t)
				user := setupAuthSessionTestDB(t)
				if !enabled {
					require.NoError(t, model.UpdateUserSessionAutoLogout(user.Id, false))
				}
				bundle, err := CreateLoginSession(user.Id, "password", "127.0.0.1", "test-agent")
				require.NoError(t, err)
				createdAt := time.Now().Add(-8 * 24 * time.Hour).Unix()
				require.NoError(t, model.DB.Model(&model.UserSession{}).Where("sid = ?", bundle.Session.SID).Updates(map[string]interface{}{
					"created_at": createdAt, "last_active_at": time.Now().Unix(),
				}).Error)
				_, _, cachedServer, cachedClient := useIndependentAuthSessionRedis(t)
				common.RDB = cachedClient
				_, err = model.GetUserSessionCached(bundle.Session.SID)
				require.NoError(t, err)
				assert.NotEmpty(t, cachedLoginSessionKey(t, cachedServer))
				identity, err := ParseAccessToken(bundle.AccessToken)
				require.NoError(t, err)

				if operation == "access" {
					_, _, err = ValidateLoginSession(identity)
				} else {
					_, _, err = RefreshLoginSession(bundle.RefreshToken, bundle.Session.SID, "127.0.0.2", "test-agent")
				}
				stored, readErr := model.GetUserSessionBySID(bundle.Session.SID)
				require.NoError(t, readErr)
				assert.Equal(t, createdAt, stored.CreatedAt)
				if enabled {
					assert.ErrorIs(t, err, ErrLoginSessionRevoked)
					assert.Equal(t, model.UserSessionStatusRevoked, stored.Status)
					_, _, err = ValidateLoginSession(identity)
					assert.ErrorIs(t, err, ErrLoginSessionRevoked)
					_, _, err = RefreshLoginSession(bundle.RefreshToken, bundle.Session.SID, "127.0.0.2", "test-agent")
					assert.ErrorIs(t, err, ErrLoginSessionRevoked, "refresh must not resurrect a logged-out session")
				} else {
					assert.NoError(t, err)
					assert.Equal(t, model.UserSessionStatusActive, stored.Status)
				}
			})
		}
	}
}

func TestSessionAutoLogoutRespectsDatabasePreferenceAcrossIndependentCaches(t *testing.T) {
	useTestSessionSecret(t)
	user := setupAuthSessionTestDB(t)
	bundle, err := CreateLoginSession(user.Id, "password", "127.0.0.1", "test-agent")
	require.NoError(t, err)
	identity, err := ParseAccessToken(bundle.AccessToken)
	require.NoError(t, err)
	require.NoError(t, model.DB.Model(&model.UserSession{}).Where("sid = ?", bundle.Session.SID).
		Update("created_at", time.Now().Add(-8*24*time.Hour).Unix()).Error)
	_, clientA, serverB, clientB := useIndependentAuthSessionRedis(t)

	common.RDB = clientB
	cached, err := model.GetUserCache(user.Id)
	require.NoError(t, err)
	require.True(t, cached.GetSetting().IsSessionAutoLogoutEnabled())
	common.RDB = clientA
	require.NoError(t, model.UpdateUserSessionAutoLogout(user.Id, false))
	common.RDB = clientB
	cached, err = model.GetUserCache(user.Id)
	require.NoError(t, err)
	require.True(t, cached.GetSetting().IsSessionAutoLogoutEnabled(), "node B still holds its original enabled preference")
	_, _, err = ValidateLoginSession(identity)
	require.NoError(t, err, "an old cache must not revoke a session after the user has opted out")
	revoked, err := model.RevokeWeekOldUserSession(user.Id, bundle.Session.SID, time.Now().Unix())
	require.NoError(t, err)
	assert.False(t, revoked)
	require.NoError(t, model.RevokeWeekOldUserSessions(0, time.Now().Unix()))
	stored, err := model.GetUserSessionBySID(bundle.Session.SID)
	require.NoError(t, err)
	require.Equal(t, model.UserSessionStatusActive, stored.Status)

	serverB.FastForward(3 * time.Second)
	cached, err = model.GetUserCache(user.Id)
	require.NoError(t, err)
	require.False(t, cached.GetSetting().IsSessionAutoLogoutEnabled())
	common.RDB = clientA
	require.NoError(t, model.UpdateUserSessionAutoLogout(user.Id, true))
	common.RDB = clientB
	cached, err = model.GetUserCache(user.Id)
	require.NoError(t, err)
	require.False(t, cached.GetSetting().IsSessionAutoLogoutEnabled(), "node B still holds the disabled preference")
	_, _, err = RefreshLoginSession(bundle.RefreshToken, bundle.Session.SID, "127.0.0.2", "test-agent")
	assert.ErrorIs(t, err, ErrLoginSessionRevoked, "refresh must honor the newly enabled database preference")
	stored, err = model.GetUserSessionBySID(bundle.Session.SID)
	require.NoError(t, err)
	assert.Equal(t, model.UserSessionStatusRevoked, stored.Status)
}

func TestSessionAutoLogoutCleanupEntryPoints(t *testing.T) {
	for _, operation := range []string{"list", "login", "hourly"} {
		t.Run(operation, func(t *testing.T) {
			useTestSessionSecret(t)
			user := setupAuthSessionTestDB(t)
			common.UserSessionActiveLimit = 1
			now := time.Now().Unix()
			old := oldLoginSessionFixture(user, "old-session", now-int64((8*24*time.Hour)/time.Second), now)
			require.NoError(t, model.DB.Create(&old).Error)

			switch operation {
			case "list":
				sessions, err := ListLoginSessions(user.Id, old.SID)
				require.NoError(t, err)
				assert.Empty(t, sessions, "an old session is omitted even when it is the current session")
			case "login":
				bundle, err := CreateLoginSession(user.Id, "password", "127.0.0.1", "new-browser")
				require.NoError(t, err, "old sessions must be revoked before checking the active session limit")
				assert.NotEqual(t, old.SID, bundle.Session.SID)
			case "hourly":
				cleanupAuthArtifacts()
			}
			stored, err := model.GetUserSessionBySID(old.SID)
			require.NoError(t, err, "newly revoked audit records must survive the cleanup pass")
			assert.Equal(t, model.UserSessionStatusRevoked, stored.Status)
			assert.Positive(t, stored.RevokedAt)
		})
	}
}
