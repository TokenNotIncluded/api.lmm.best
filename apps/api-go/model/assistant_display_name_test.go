package model

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAssistantDisplayNameNormalization(t *testing.T) {
	for _, value := range []string{"", "   ", strings.Repeat("字", 21), "a\nb", "a\x00b", string([]byte{0xff})} {
		_, err := NormalizeAssistantDisplayName(value)
		assert.ErrorIs(t, err, ErrAssistantDisplayNameInvalid)
	}
	name, err := NormalizeAssistantDisplayName(" " + strings.Repeat("字", 20) + " ")
	require.NoError(t, err)
	assert.Equal(t, strings.Repeat("字", 20), name)
}

func TestAssistantDisplayNameAtomicRollbackCacheAndExpiry(t *testing.T) {
	db := setupAssistantGiftTestDB(t)
	require.NoError(t, db.AutoMigrate(&AuthFlow{}, &UserSession{}))
	user := &User{Username: "nickname-atomic", AffCode: "nickname-atomic-aff", DisplayName: "原昵称", Status: common.UserStatusEnabled, Role: common.RoleCommonUser, Group: "default", AuthVersion: 1}
	require.NoError(t, db.Create(user).Error)
	session := UserSession{SID: "nickname-atomic-session", UserID: user.Id, Version: 1, UserAuthVersion: 1, Status: UserSessionStatusActive, CreatedAt: time.Now().Unix(), ExpiresAt: time.Now().Add(time.Hour).Unix()}
	require.NoError(t, db.Create(&session).Error)
	match := AuthFlowMatch{Purpose: AuthFlowPurposeAssistantDisplayName, UserId: user.Id, SessionId: session.SID}
	token, flow, err := CreateAuthFlow(AuthFlowCreate{Purpose: match.Purpose, UserId: user.Id, SessionId: session.SID, ExpiresAt: time.Now().Add(time.Minute)})
	require.NoError(t, err)

	// A write failure must roll the nickname and one-time consumption back.
	writeErr := errors.New("simulated profile write failure")
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register("test:nickname-write-fail", func(tx *gorm.DB) {
		if tx.Statement.Table == "users" {
			tx.AddError(writeErr)
		}
	}))
	_, err = ConfirmAssistantDisplayName(token, match, 1, 1, "新昵称")
	assert.ErrorIs(t, err, writeErr)
	require.NoError(t, db.Callback().Update().Remove("test:nickname-write-fail"))
	flow, err = GetAuthFlow(token, match)
	require.NoError(t, err)
	assert.Nil(t, flow.ConsumedAt)
	loaded, err := GetUserById(user.Id, false)
	require.NoError(t, err)
	assert.Equal(t, "原昵称", loaded.DisplayName)

	server := useUserCacheMiniRedis(t)
	require.NoError(t, populateUserCache(*user))
	assert.True(t, server.Exists(getUserCacheKey(user.Id)))
	updated, err := ConfirmAssistantDisplayName(token, match, 1, 1, "新昵称")
	require.NoError(t, err)
	assert.Equal(t, "新昵称", updated.DisplayName)
	assert.False(t, server.Exists(getUserCacheKey(user.Id)))
	_, err = ConfirmAssistantDisplayName(token, match, 1, 1, "重放昵称")
	assert.ErrorIs(t, err, ErrAuthFlowConsumed)

	token, flow, err = CreateAuthFlow(AuthFlowCreate{Purpose: match.Purpose, UserId: user.Id, SessionId: session.SID, ExpiresAt: time.Now().Add(time.Minute)})
	require.NoError(t, err)
	require.NoError(t, db.Model(flow).Update("expires_at", time.Now().Add(-time.Second)).Error)
	_, err = ConfirmAssistantDisplayName(token, match, 1, 1, "过期昵称")
	assert.ErrorIs(t, err, ErrAuthFlowExpired)
	loaded, err = GetUserById(user.Id, false)
	require.NoError(t, err)
	assert.Equal(t, "新昵称", loaded.DisplayName)
}
