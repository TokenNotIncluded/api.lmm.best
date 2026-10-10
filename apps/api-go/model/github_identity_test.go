package model

import (
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestGitHubIdentityBackfillClaimsOnlyNumericAndRejectsDuplicates(t *testing.T) {
	truncateTables(t)
	numeric := User{Username: "github-numeric", GitHubId: "123456", AffCode: "github-numeric"}
	legacy := User{Username: "github-legacy", GitHubId: "legacy-name", AffCode: "github-legacy"}
	require.NoError(t, DB.Create(&numeric).Error)
	require.NoError(t, DB.Create(&legacy).Error)
	require.NoError(t, InitializeExternalIdentityClaims())
	require.NoError(t, InitializeExternalIdentityClaims())
	var claims []ExternalIdentityClaim
	require.NoError(t, DB.Where("provider = ?", ExternalIdentityProviderGitHub).Find(&claims).Error)
	require.Len(t, claims, 1)
	assert.Equal(t, numeric.Id, claims[0].UserId)
	duplicate := User{Username: "github-duplicate", GitHubId: numeric.GitHubId, AffCode: "github-duplicate"}
	require.NoError(t, DB.Create(&duplicate).Error)
	assert.ErrorIs(t, InitializeExternalIdentityClaims(), ErrExternalIdentityAlreadyClaimed)
}

func TestGitHubAuthenticatedBindingRevalidatesSessionAndReleasesClaims(t *testing.T) {
	truncateTables(t)
	user := User{Username: "github-binder", GitHubId: "legacy-other", AffCode: "github-binder", AuthVersion: 1, Status: common.UserStatusEnabled}
	require.NoError(t, DB.Create(&user).Error)
	session := UserSession{SID: "github-binding", UserID: user.Id, UserAuthVersion: 1, Version: 1, RefreshHash: "hash", Status: UserSessionStatusActive, ExpiresAt: time.Now().Add(time.Hour).Unix()}
	require.NoError(t, CreateUserSession(&session))
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { return BindGitHubIdentityForSessionWithTx(tx, user.Id, session.SID, "98765") }))
	require.NoError(t, user.ClearBinding("github"))
	var count int64
	require.NoError(t, DB.Model(&ExternalIdentityClaim{}).Where("provider = ? AND user_id = ?", ExternalIdentityProviderGitHub, user.Id).Count(&count).Error)
	assert.Zero(t, count)
	for _, tc := range []string{"revoked", "version", "expired"} {
		t.Run(tc, func(t *testing.T) {
			updates := map[string]interface{}{"status": UserSessionStatusActive, "revoked_at": 0, "user_auth_version": user.AuthVersion, "expires_at": time.Now().Add(time.Hour).Unix()}
			switch tc {
			case "revoked":
				updates["status"] = UserSessionStatusRevoked
			case "version":
				updates["user_auth_version"] = user.AuthVersion + 1
			case "expired":
				updates["expires_at"] = time.Now().Add(-time.Hour).Unix()
			}
			require.NoError(t, DB.Model(&UserSession{}).Where("sid = ?", session.SID).Updates(updates).Error)
			assert.ErrorIs(t, DB.Transaction(func(tx *gorm.DB) error { return BindGitHubIdentityForSessionWithTx(tx, user.Id, session.SID, "98765") }), ErrUserSessionInactive)
		})
	}
}
