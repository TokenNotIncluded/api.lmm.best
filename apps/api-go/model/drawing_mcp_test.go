package model

import (
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
)

func TestDrawingMCPTokenBindsAndRotatesWithoutTouchingBountyToken(t *testing.T) {
	db := setupOpenSourceBountyTestDB(t)
	require.NoError(t, db.AutoMigrate(&Token{}, &DrawingMCPToken{}, &OpenSourceBountyMCPToken{}))
	user := createOpenSourceBountyUser(t, db, "drawing-mcp-user", 100, common.RoleCommonUser)
	key := &Token{UserId: user.Id, Key: "drawing-api-key", Group: DrawingTokenGroup, Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true}
	require.NoError(t, db.Create(key).Error)

	first, status, err := RotateDrawingMCPToken(user.Id, key.Id)
	require.NoError(t, err)
	require.NotEmpty(t, first)
	require.Equal(t, key.Id, status.ApiKeyId)
	identity, err := VerifyDrawingMCPToken(first)
	require.NoError(t, err)
	require.Equal(t, DrawingMCPTokenIdentity{UserId: user.Id, ApiKeyId: key.Id}, identity)

	bountyToken, _, err := RotateOpenSourceBountyMCPToken(user.Id)
	require.NoError(t, err)
	_, err = VerifyDrawingMCPToken(bountyToken)
	require.ErrorIs(t, err, ErrDrawingMCPTokenInvalid)

	second, secondStatus, err := RotateDrawingMCPToken(user.Id, key.Id, "image-2")
	require.NoError(t, err)
	require.Equal(t, "image-2", secondStatus.DefaultModel)
	_, err = VerifyDrawingMCPToken(first)
	require.ErrorIs(t, err, ErrDrawingMCPTokenInvalid)
	identity, err = VerifyDrawingMCPToken(second)
	require.NoError(t, err)
	require.Equal(t, key.Id, identity.ApiKeyId)
	require.Equal(t, "image-2", identity.DefaultModel)

	require.NoError(t, RevokeDrawingMCPToken(user.Id))
	_, err = VerifyDrawingMCPToken(second)
	require.ErrorIs(t, err, ErrDrawingMCPTokenInvalid)
	_, err = VerifyOpenSourceBountyMCPToken(bountyToken)
	require.NoError(t, err)
}

func TestDrawingMCPTokenRejectsOtherUsersAndOAuthKeys(t *testing.T) {
	db := setupOpenSourceBountyTestDB(t)
	require.NoError(t, db.AutoMigrate(&Token{}, &DrawingMCPToken{}))
	user := createOpenSourceBountyUser(t, db, "drawing-mcp-owner", 100, common.RoleCommonUser)
	other := createOpenSourceBountyUser(t, db, "drawing-mcp-other", 100, common.RoleCommonUser)
	otherKey := &Token{UserId: other.Id, Key: "other-drawing-key", Group: DrawingTokenGroup, Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true}
	oauthKey := &Token{UserId: user.Id, Key: "oauth-managed-key", Group: DrawingTokenGroup, Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true, OAuthManaged: true}
	require.NoError(t, db.Create(otherKey).Error)
	require.NoError(t, db.Create(oauthKey).Error)

	_, _, err := RotateDrawingMCPToken(user.Id, otherKey.Id)
	require.ErrorIs(t, err, ErrDrawingMCPKeyInvalid)
	_, _, err = RotateDrawingMCPToken(user.Id, oauthKey.Id)
	require.ErrorIs(t, err, ErrDrawingMCPKeyInvalid)
}

func TestDrawingMCPTokenRejectsExpiredAndExhaustedKeys(t *testing.T) {
	db := setupOpenSourceBountyTestDB(t)
	require.NoError(t, db.AutoMigrate(&Token{}, &DrawingMCPToken{}))
	user := createOpenSourceBountyUser(t, db, "drawing-mcp-invalid", 100, common.RoleCommonUser)
	keys := []*Token{
		{UserId: user.Id, Key: "expired-drawing-key", Group: DrawingTokenGroup, Status: common.TokenStatusEnabled, ExpiredTime: common.GetTimestamp() - 1},
		{UserId: user.Id, Key: "exhausted-drawing-key", Group: DrawingTokenGroup, Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: 0},
	}
	for _, key := range keys {
		require.NoError(t, db.Create(key).Error)
		_, _, err := RotateDrawingMCPToken(user.Id, key.Id)
		require.ErrorIs(t, err, ErrDrawingMCPKeyInvalid)
	}
}

func TestDrawingMCPTokenAllowsEmptyGroupKeyToInheritAccountGroup(t *testing.T) {
	db := setupOpenSourceBountyTestDB(t)
	require.NoError(t, db.AutoMigrate(&Token{}, &DrawingMCPToken{}))
	user := createOpenSourceBountyUser(t, db, "drawing-mcp-inherited", 100, common.RoleCommonUser)
	user.Group = DrawingTokenGroup
	require.NoError(t, db.Save(user).Error)
	key := &Token{UserId: user.Id, Key: "inherited-drawing-key", Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true}
	require.NoError(t, db.Create(key).Error)
	secret, _, err := RotateDrawingMCPToken(user.Id, key.Id)
	require.NoError(t, err)
	require.NotEmpty(t, secret)
	resolved, err := ResolveDrawingTokenByID(user.Id, key.Id, DrawingTokenGroup)
	require.NoError(t, err)
	require.Equal(t, key.Id, resolved.Id)
}

func TestDrawingMCPRechecksBoundKeyLimitsAfterIssuance(t *testing.T) {
	db := setupOpenSourceBountyTestDB(t)
	require.NoError(t, db.AutoMigrate(&Token{}, &DrawingMCPToken{}))
	user := createOpenSourceBountyUser(t, db, "drawing-mcp-budget", 100, common.RoleCommonUser)
	key := &Token{UserId: user.Id, Key: "budget-drawing-key", Group: DrawingTokenGroup, Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: 100}
	require.NoError(t, db.Create(key).Error)
	secret, _, err := RotateDrawingMCPToken(user.Id, key.Id)
	require.NoError(t, err)
	for _, update := range []map[string]any{
		{"remain_quota": 0},
		{"remain_quota": 100, "status": common.TokenStatusDisabled},
		{"status": common.TokenStatusEnabled, "expired_time": common.GetTimestamp() - 1},
	} {
		require.NoError(t, db.Model(key).Updates(update).Error)
		_, err := VerifyDrawingMCPToken(secret)
		require.ErrorIs(t, err, ErrDrawingMCPTokenInvalid)
	}
	var unchanged Token
	require.NoError(t, db.First(&unchanged, key.Id).Error)
	require.Equal(t, "budget-drawing-key", unchanged.Key)
}
