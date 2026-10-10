package service

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func githubMigrationSessionFixture(t *testing.T) (*model.User, string) {
	t.Helper()
	useTestSessionSecret(t)
	user := setupAuthSessionTestDB(t)
	require.NoError(t, model.DB.AutoMigrate(&model.ExternalIdentityClaim{}))
	user.GitHubId = "legacy-name"
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", user.Id).Update("github_id", user.GitHubId).Error)
	token, _, err := model.CreateAuthFlow(model.AuthFlowCreate{Purpose: model.AuthFlowPurposeTwoFALogin, Provider: "github", Intent: model.AuthFlowIntentLogin, UserId: user.Id, ExpiresAt: time.Now().Add(time.Minute)})
	require.NoError(t, err)
	return user, token
}

func migrationSessionAction(user *model.User, token string) func(*gorm.DB) error {
	return func(tx *gorm.DB) error {
		if _, err := model.ConsumeAuthFlowWithTx(tx, token, model.AuthFlowMatch{Purpose: model.AuthFlowPurposeTwoFALogin, Provider: "github", Intent: model.AuthFlowIntentLogin, UserId: user.Id}); err != nil {
			return err
		}
		return model.MigrateGitHubIdentityWithTx(tx, user.Id, user.AuthVersion, user.GitHubId, "10001", "")
	}
}

func TestGitHubMigrationSessionInsertFailureRollsBackAllState(t *testing.T) {
	user, token := githubMigrationSessionFixture(t)
	require.NoError(t, model.DB.Exec(`CREATE TRIGGER reject_github_session BEFORE INSERT ON user_sessions BEGIN SELECT RAISE(ABORT, 'session insert unavailable'); END`).Error)
	bundle, err := CreateLoginSessionWithAction(user.Id, user.AuthVersion, "oauth:github:2fa", "127.0.0.1", "test", migrationSessionAction(user, token))
	require.Error(t, err)
	assert.Nil(t, bundle)
	stored, err := model.GetUserById(user.Id, false)
	require.NoError(t, err)
	assert.Equal(t, "legacy-name", stored.GitHubId)
	_, err = model.GetAuthFlow(token, model.AuthFlowMatch{Purpose: model.AuthFlowPurposeTwoFALogin})
	require.NoError(t, err, "failed session insert must preserve challenge")
	var sessions, claims int64
	require.NoError(t, model.DB.Model(&model.UserSession{}).Count(&sessions).Error)
	require.NoError(t, model.DB.Model(&model.ExternalIdentityClaim{}).Count(&claims).Error)
	assert.Zero(t, sessions)
	assert.Zero(t, claims)
}

func TestGitHubMigrationConcurrentCompletionIssuesOneSession(t *testing.T) {
	user, token := githubMigrationSessionFixture(t)
	var successes atomic.Int32
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			bundle, err := CreateLoginSessionWithAction(user.Id, user.AuthVersion, "oauth:github:2fa", "127.0.0.1", "test", migrationSessionAction(user, token))
			if err == nil && bundle != nil {
				successes.Add(1)
			} else {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	assert.EqualValues(t, 1, successes.Load())
	for err := range errs {
		assert.ErrorIs(t, err, model.ErrAuthFlowConsumed)
	}
	var sessions, claims int64
	require.NoError(t, model.DB.Model(&model.UserSession{}).Count(&sessions).Error)
	require.NoError(t, model.DB.Model(&model.ExternalIdentityClaim{}).Count(&claims).Error)
	assert.EqualValues(t, 1, sessions)
	assert.EqualValues(t, 1, claims)
	bundle, err := CreateLoginSessionWithAction(user.Id, user.AuthVersion, "oauth:github:2fa", "127.0.0.1", "test", migrationSessionAction(user, token))
	assert.ErrorIs(t, err, model.ErrAuthFlowConsumed)
	assert.Nil(t, bundle)
}

func TestGitHubMigrationRejectsChangedAccountAndActionFailure(t *testing.T) {
	for _, tc := range []string{"relink", "email", "auth_version", "disabled", "deleted", "action"} {
		t.Run(tc, func(t *testing.T) {
			user, token := githubMigrationSessionFixture(t)
			action := migrationSessionAction(user, token)
			switch tc {
			case "relink":
				require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", user.Id).Update("github_id", "another-name").Error)
			case "email":
				require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", user.Id).Update("email", "changed@example.test").Error)
				action = func(tx *gorm.DB) error {
					return model.MigrateGitHubIdentityWithTx(tx, user.Id, user.AuthVersion, user.GitHubId, "10001", "old@example.test")
				}
			case "auth_version":
				require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", user.Id).Update("auth_version", user.AuthVersion+1).Error)
			case "disabled":
				require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", user.Id).Update("status", 2).Error)
			case "deleted":
				require.NoError(t, model.DB.Delete(user).Error)
			case "action":
				// An action failure after migration must roll all state back.
				base := action
				action = func(tx *gorm.DB) error {
					if err := base(tx); err != nil {
						return err
					}
					return errors.New("account action unavailable")
				}
			}
			bundle, err := CreateLoginSessionWithAction(user.Id, user.AuthVersion, "oauth:github:2fa", "127.0.0.1", "test", action)
			require.Error(t, err)
			assert.Nil(t, bundle)
			var sessions, claims int64
			require.NoError(t, model.DB.Model(&model.UserSession{}).Count(&sessions).Error)
			require.NoError(t, model.DB.Model(&model.ExternalIdentityClaim{}).Count(&claims).Error)
			assert.Zero(t, sessions)
			assert.Zero(t, claims)
		})
	}
}
