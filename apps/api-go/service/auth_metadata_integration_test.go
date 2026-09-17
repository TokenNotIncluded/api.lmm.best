package service

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/stretchr/testify/require"
)

func TestLoginSessionMetadataSurvivesCreateAndRead(t *testing.T) {
	useTestSessionSecret(t)
	user := setupAuthSessionTestDB(t)
	for _, userAgent := range []string{strings.Repeat("中", 171), "browser\x00\xff", strings.Repeat("a", 510) + "😀"} {
		bundle, err := CreateLoginSession(user.Id, "password", "127.0.0.1", userAgent)
		require.NoError(t, err)
		var persisted model.UserSession
		require.NoError(t, model.DB.Where("sid = ?", bundle.Session.SID).First(&persisted).Error)
		require.True(t, utf8.ValidString(persisted.UserAgent))
		require.NotContains(t, persisted.UserAgent, "\x00")
		require.LessOrEqual(t, len(persisted.UserAgent), 512)
		require.Equal(t, truncateAuthMetadata(userAgent, 512), persisted.UserAgent)
		require.Equal(t, persisted.UserAgent, bundle.Session.UserAgent)
		require.Equal(t, "password", persisted.LoginMethod)
		require.Equal(t, user.AuthVersion, persisted.UserAuthVersion)

		views, err := ListLoginSessions(user.Id, bundle.Session.SID)
		require.NoError(t, err)
		require.NotEmpty(t, views)
		require.Equal(t, bundle.Session.SID, views[0].SID)
		require.Equal(t, persisted.UserAgent, views[0].UserAgent)
	}
}
