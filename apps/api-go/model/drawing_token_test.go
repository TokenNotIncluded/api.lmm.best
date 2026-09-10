package model

import (
	"sync"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupDrawingTokenTest(t *testing.T) (*gorm.DB, User) {
	t.Helper()
	db := setupConsoleActivationTestDB(t)
	previousWarnings := ratio_setting.GroupWarnings2JSONString()
	previousLimit := operation_setting.GetMaxUserTokens()
	require.NoError(t, ratio_setting.UpdateGroupWarningsByJSONString(`{"image-2":{"enabled":false}}`))
	operation_setting.GetTokenSetting().MaxUserTokens = 100
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateGroupWarningsByJSONString(previousWarnings))
		operation_setting.GetTokenSetting().MaxUserTokens = previousLimit
	})
	level := TrustLevelMinUser + 1
	user := User{Username: "drawing-owner", Password: "password", Group: "default", Status: common.UserStatusEnabled, Role: common.RoleCommonUser, TrustLevelOverride: &level}
	require.NoError(t, db.Create(&user).Error)
	return db, user
}

func allowDrawingTestGroup(userGroup, group string) bool {
	return userGroup == "default" && (group == DrawingTokenGroup || group == "other-images")
}

func TestResolveDrawingTokenCreatesReusesAndActivatesConsole(t *testing.T) {
	db, user := setupDrawingTokenTest(t)
	first, created, err := ResolveDrawingToken(user.Id, DrawingTokenGroup, allowDrawingTestGroup)
	require.NoError(t, err)
	require.True(t, created)
	assert.Positive(t, first.Id)
	assert.NotEmpty(t, first.Key)
	assert.Equal(t, DrawingTokenGroup, first.Group)
	assert.True(t, first.UnlimitedQuota)
	reused, created, err := ResolveDrawingToken(user.Id, DrawingTokenGroup, allowDrawingTestGroup)
	require.NoError(t, err)
	assert.False(t, created)
	assert.Equal(t, first.Id, reused.Id)
	require.NoError(t, db.First(&user, user.Id).Error)
	assert.Positive(t, user.ConsoleActivatedAt)
	var count int64
	require.NoError(t, db.Model(&Token{}).Count(&count).Error)
	assert.EqualValues(t, 1, count)
}

func TestResolveDrawingTokenNeverReplacesRestrictedOrDisabledKey(t *testing.T) {
	db, user := setupDrawingTokenTest(t)
	key := Token{UserId: user.Id, Key: "existing-restricted-drawing-key", Group: DrawingTokenGroup, Status: common.TokenStatusDisabled,
		ExpiredTime: 1, RemainQuota: 0, ModelLimitsEnabled: true, ModelLimits: "another-model"}
	require.NoError(t, db.Create(&key).Error)
	// Even a second unrestricted key must not silently bypass the first key.
	require.NoError(t, db.Create(&Token{UserId: user.Id, Key: "newer-unrestricted-drawing-key", Group: DrawingTokenGroup,
		Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true}).Error)
	resolved, created, err := ResolveDrawingToken(user.Id, DrawingTokenGroup, allowDrawingTestGroup)
	require.NoError(t, err)
	assert.False(t, created)
	assert.Equal(t, key.Id, resolved.Id)
	assert.Equal(t, common.TokenStatusDisabled, resolved.Status)
	var count int64
	require.NoError(t, db.Model(&Token{}).Count(&count).Error)
	assert.EqualValues(t, 2, count)
}

func TestResolveDrawingTokenChecksLockedPermissionsGroupWarningAndLimit(t *testing.T) {
	for _, name := range []string{"disabled", "developer", "group", "warning", "limit", "explicit-group"} {
		t.Run(name, func(t *testing.T) {
			db, user := setupDrawingTokenTest(t)
			group := DrawingTokenGroup
			var expected error
			switch name {
			case "disabled":
				require.NoError(t, db.Model(&user).Update("status", common.UserStatusDisabled).Error)
				expected = ErrDrawingTokenAccessDenied
			case "developer":
				require.NoError(t, db.Model(&user).Update("trust_level_override", TrustLevelMinUser).Error)
				expected = ErrDrawingTokenAccessDenied
			case "group":
				require.NoError(t, db.Model(&user).Update("group", "removed-group").Error)
				expected = ErrDrawingTokenGroupUnavailable
			case "warning":
				require.NoError(t, ratio_setting.UpdateGroupWarningsByJSONString(`{"image-2":{"enabled":true,"message":"Confirm privacy risks","mode":"modal","confirmations":2}}`))
				expected = ErrDrawingTokenWarningRequired
			case "limit":
				operation_setting.GetTokenSetting().MaxUserTokens = 1
				require.NoError(t, db.Create(&Token{UserId: user.Id, Key: "counts-toward-key-limit", Group: "default"}).Error)
				expected = ErrDrawingTokenLimit
			case "explicit-group":
				group = "other-images"
				expected = ErrDrawingTokenRequired
			}
			token, created, err := ResolveDrawingToken(user.Id, group, allowDrawingTestGroup)
			require.ErrorIs(t, err, expected)
			assert.Nil(t, token)
			assert.False(t, created)
			var count int64
			require.NoError(t, db.Model(&Token{}).Where("user_id = ? AND "+commonGroupCol+" = ?", user.Id, DrawingTokenGroup).Count(&count).Error)
			assert.Zero(t, count)
			require.NoError(t, db.First(&user, user.Id).Error)
			assert.Zero(t, user.ConsoleActivatedAt)
		})
	}
}

func TestResolveDrawingTokenConcurrentFirstUseCreatesOneKey(t *testing.T) {
	db, user := setupDrawingTokenTest(t)
	// SQLite has no row-level FOR UPDATE; serialize its single writer. Production
	// PostgreSQL/MySQL use the user-row lock and recheck in the same transaction.
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	type result struct {
		id      int
		created bool
		err     error
	}
	results := make(chan result, 8)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			token, created, err := ResolveDrawingToken(user.Id, DrawingTokenGroup, allowDrawingTestGroup)
			id := 0
			if token != nil {
				id = token.Id
			}
			results <- result{id, created, err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	firstID, creates := 0, 0
	for result := range results {
		require.NoError(t, result.err)
		if firstID == 0 {
			firstID = result.id
		}
		assert.Equal(t, firstID, result.id)
		if result.created {
			creates++
		}
	}
	assert.Equal(t, 1, creates)
}
