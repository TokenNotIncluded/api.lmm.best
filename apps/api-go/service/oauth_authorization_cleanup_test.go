package service

import (
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/stretchr/testify/require"
)

func TestCleanupAuthArtifactsIncludesExpiredOAuthBrowserState(t *testing.T) {
	setupAuthSessionTestDB(t)
	require.NoError(t, model.MigrateOAuthServer(model.DB))
	now := time.Now()
	require.NoError(t, model.DB.Create(&[]model.OAuthServerAuthorization{
		{Digest: "expired-oauth-browser", Issuer: "https://local.example.test", ExpiresAtMs: now.Add(-time.Minute).UnixMilli()},
		{Digest: "live-oauth-browser", Issuer: "https://local.example.test", ExpiresAtMs: now.Add(time.Minute).UnixMilli(), BrowserCSRFHash: "live-one-time-csrf"},
	}).Error)
	cleanupAuthArtifacts()
	var rows []model.OAuthServerAuthorization
	require.NoError(t, model.DB.Find(&rows).Error)
	require.Len(t, rows, 1)
	require.Equal(t, "live-oauth-browser", rows[0].Digest)
	require.Equal(t, "live-one-time-csrf", rows[0].BrowserCSRFHash)
}
