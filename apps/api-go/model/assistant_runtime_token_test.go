package model

import (
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAssistantRuntimeTokenIsDurableRootOwnedAndInternalOnly(t *testing.T) {
	db := setupConsoleActivationTestDB(t)
	root := User{Username: "assistant-runtime-root", AffCode: "assistant-runtime-root", Password: "password", Role: common.RoleRootUser, Status: common.UserStatusEnabled}
	ordinary := User{Username: "assistant-runtime-user", AffCode: "assistant-runtime-user", Password: "password", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&root).Error)
	require.NoError(t, db.Create(&ordinary).Error)

	_, _, err := EnsureAssistantRuntimeToken(ordinary.Id, "default")
	require.ErrorIs(t, err, ErrAssistantRuntimeTokenUnavailable)
	first, created, err := EnsureAssistantRuntimeToken(root.Id, "default")
	require.NoError(t, err)
	require.True(t, created)
	assert.Positive(t, first.Id)
	assert.NotEmpty(t, first.Key)
	assert.True(t, first.OneTimeReveal)
	assert.Equal(t, TokenCreationSourceAssistantRuntime, first.CreationSource)
	_, err = ResolveDrawingTokenByID(root.Id, first.Id, "")
	assert.ErrorIs(t, err, ErrDrawingTokenRequired)
	_, err = validateDrawingMCPKey(db, root.Id, first.Id)
	assert.ErrorIs(t, err, ErrDrawingMCPKeyInvalid)
	_, _, err = ResolveDrawingToken(root.Id, "default", func(string, string) bool { return true })
	assert.ErrorIs(t, err, ErrDrawingTokenRequired)
	listed, err := ListDrawingMCPAPIKeys(root.Id)
	require.NoError(t, err)
	assert.Empty(t, listed)

	reused, created, err := EnsureAssistantRuntimeToken(root.Id, "image-2")
	require.NoError(t, err)
	assert.False(t, created)
	assert.Equal(t, first.Id, reused.Id)
	assert.Equal(t, first.Key, reused.Key)
	assert.Equal(t, "image-2", reused.Group)
	assert.Zero(t, reused.AccessedTime, "opening key management must not count as assistant use")
	require.NoError(t, TouchAssistantRuntimeToken(root.Id, reused.Id))
	var touched Token
	require.NoError(t, db.First(&touched, reused.Id).Error)
	assert.Positive(t, touched.AccessedTime)

	automatic, err := GetUserTokensByCreationMode(root.Id, 0, 10, TokenCreationModeAutomatic)
	require.NoError(t, err)
	require.Len(t, automatic, 1)
	assert.Equal(t, first.Id, automatic[0].Id)
	otherAutomatic, err := GetUserTokensByCreationMode(ordinary.Id, 0, 10, TokenCreationModeAutomatic)
	require.NoError(t, err)
	assert.Empty(t, otherAutomatic)

	_, err = GetTokenByKey(first.Key, true)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	_, err = ValidateUserToken(first.Key)
	require.ErrorIs(t, err, ErrTokenInvalid)
	keys, err := GetTokenKeysByIds([]int{first.Id}, root.Id)
	require.NoError(t, err)
	assert.Empty(t, keys)

	stored, err := GetTokenByIds(first.Id, root.Id)
	require.NoError(t, err)
	assert.Empty(t, stored.GetFullKey())
	assert.ErrorIs(t, stored.Update(), ErrAssistantRuntimeTokenManaged)
	assert.ErrorIs(t, stored.SelectUpdate(), ErrAssistantRuntimeTokenManaged)
	assert.ErrorIs(t, DeleteTokenById(first.Id, root.Id), ErrAssistantRuntimeTokenManaged)
	manual := Token{UserId: root.Id, Key: "manual-key-beside-assistant-runtime", Name: "manual", CreationSource: TokenCreationSourceManual}
	require.NoError(t, db.Create(&manual).Error)
	_, err = BatchDeleteTokens([]int{first.Id, manual.Id}, root.Id)
	assert.ErrorIs(t, err, ErrAssistantRuntimeTokenManaged)
	var count int64
	require.NoError(t, db.Model(&Token{}).Where("user_id = ?", root.Id).Count(&count).Error)
	assert.EqualValues(t, 2, count)
	usableCount, err := CountUserTokens(root.Id)
	require.NoError(t, err)
	assert.EqualValues(t, 1, usableCount, "the internal key must not consume a user-managed key slot")
}
