package service

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Each qualification owns a temporary schema, but uses a real multi-connection
// PostgreSQL pool and the production session, challenge and identity helpers.
func githubMigrationPostgresDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("TEST_POSTGRES_DSN"))
	if dsn == "" || os.Getenv("TEST_POSTGRES_ISOLATED_SCHEMA") != "1" {
		t.Skip("set TEST_POSTGRES_DSN and TEST_POSTGRES_ISOLATED_SCHEMA=1 for isolated PostgreSQL qualification")
	}
	base, err := gorm.Open(postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	basePool, err := base.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, basePool.Close()) })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, basePool.PingContext(ctx))
	schema := fmt.Sprintf("lmm_github_migration_%d_%d", os.Getpid(), time.Now().UnixNano())
	quoted := pgx.Identifier{schema}.Sanitize()
	require.NoError(t, base.WithContext(ctx).Exec("CREATE SCHEMA "+quoted).Error)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, base.WithContext(ctx).Exec("DROP SCHEMA IF EXISTS "+quoted+" CASCADE").Error)
	})
	parsed, err := url.Parse(dsn)
	if err == nil && (parsed.Scheme == "postgres" || parsed.Scheme == "postgresql") {
		query := parsed.Query()
		query.Set("search_path", schema)
		parsed.RawQuery = query.Encode()
		dsn = parsed.String()
	} else {
		dsn += " search_path=" + schema
	}
	db, err := gorm.Open(postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	pool.SetMaxOpenConns(16)
	t.Cleanup(func() { require.NoError(t, pool.Close()) })
	testContext, cancelTest := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancelTest)
	db = db.WithContext(testContext)

	previousDB, previousRedis := model.DB, common.RedisEnabled
	previousMain := common.MainDatabaseType()
	previousActiveLimit, previousIssuanceLimit := common.UserSessionActiveLimit, common.UserSessionIssuanceLimit
	previousIssuanceWindow := common.UserSessionIssuanceWindowSeconds
	t.Cleanup(func() {
		model.DB, common.RedisEnabled = previousDB, previousRedis
		common.SetMainDatabaseType(previousMain)
		common.UserSessionActiveLimit, common.UserSessionIssuanceLimit = previousActiveLimit, previousIssuanceLimit
		common.UserSessionIssuanceWindowSeconds = previousIssuanceWindow
	})
	model.DB, common.RedisEnabled = db, false
	common.SetMainDatabaseType(common.DatabaseTypePostgreSQL)
	common.UserSessionActiveLimit, common.UserSessionIssuanceLimit = 50, 100
	common.UserSessionIssuanceWindowSeconds = 3600
	useTestSessionSecret(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.UserSession{}, &model.AuthFlow{}, &model.ExternalIdentityClaim{}))
	return db
}

func githubMigrationPostgresCandidate(t *testing.T, db *gorm.DB, name string) (*model.User, string) {
	t.Helper()
	user := &model.User{
		Username: "github-pg-" + name, AffCode: "github-pg-" + name,
		GitHubId: "legacy-" + name, Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1,
	}
	require.NoError(t, db.Create(user).Error)
	token, _, err := model.CreateAuthFlow(model.AuthFlowCreate{
		Purpose: model.AuthFlowPurposeTwoFALogin, Provider: "github", Intent: model.AuthFlowIntentLogin,
		UserId: user.Id, ExpiresAt: time.Now().Add(5 * time.Minute),
	})
	require.NoError(t, err)
	return user, token
}

func githubMigrationPostgresAction(user *model.User, token, numericID string, beforeMigration, proof func(*gorm.DB) error) func(*gorm.DB) error {
	return func(tx *gorm.DB) error {
		if _, err := model.ConsumeAuthFlowWithTx(tx, token, model.AuthFlowMatch{
			Purpose: model.AuthFlowPurposeTwoFALogin, Provider: "github", Intent: model.AuthFlowIntentLogin, UserId: user.Id,
		}); err != nil {
			return err
		}
		if beforeMigration != nil {
			if err := beforeMigration(tx); err != nil {
				return err
			}
		}
		if err := model.MigrateGitHubIdentityWithTx(tx, user.Id, user.AuthVersion, user.GitHubId, numericID, ""); err != nil {
			return err
		}
		if proof != nil {
			return proof(tx)
		}
		return nil
	}
}

type githubMigrationPostgresResult struct {
	user   *model.User
	bundle *AuthBundle
	err    error
}

func githubMigrationPostgresLogin(user *model.User, action func(*gorm.DB) error) githubMigrationPostgresResult {
	bundle, err := CreateLoginSessionWithAction(user.Id, user.AuthVersion, "oauth:github:2fa", "192.0.2.1", "github-postgres-test", action)
	return githubMigrationPostgresResult{user: user, bundle: bundle, err: err}
}

func githubMigrationPostgresCounts(t *testing.T, db *gorm.DB, sessions, claims int64) {
	t.Helper()
	var sessionCount, claimCount int64
	require.NoError(t, db.Model(&model.UserSession{}).Count(&sessionCount).Error)
	require.NoError(t, db.Model(&model.ExternalIdentityClaim{}).Count(&claimCount).Error)
	require.Equal(t, sessions, sessionCount)
	require.Equal(t, claims, claimCount)
}

func githubMigrationPostgresFreshState(t *testing.T, db *gorm.DB, user *model.User, token string) {
	t.Helper()
	var stored model.User
	require.NoError(t, db.First(&stored, user.Id).Error)
	require.Equal(t, user.GitHubId, stored.GitHubId)
	require.Equal(t, user.AuthVersion, stored.AuthVersion)
	require.Equal(t, user.Status, stored.Status)
	flow, err := model.GetAuthFlow(token, model.AuthFlowMatch{
		Purpose: model.AuthFlowPurposeTwoFALogin, Provider: "github", Intent: model.AuthFlowIntentLogin, UserId: user.Id,
	})
	require.NoError(t, err, "a rejected transaction must preserve the original challenge")
	require.Nil(t, flow.ConsumedAt)
}

func githubMigrationPostgresCommitted(t *testing.T, db *gorm.DB, user *model.User, token, numericID string, bundle *AuthBundle) {
	t.Helper()
	require.NotNil(t, bundle)
	require.NotEmpty(t, bundle.AccessToken)
	require.NotEmpty(t, bundle.RefreshToken)
	identity, err := ParseAccessToken(bundle.AccessToken)
	require.NoError(t, err)
	require.Equal(t, user.Id, identity.UserID)
	require.Equal(t, bundle.Session.SID, identity.SessionID)
	require.Equal(t, user.AuthVersion, identity.UserAuthVersion)
	_, _, err = ValidateLoginSession(identity)
	require.NoError(t, err)
	var stored model.User
	require.NoError(t, db.First(&stored, user.Id).Error)
	require.Equal(t, numericID, stored.GitHubId)
	var claim model.ExternalIdentityClaim
	require.NoError(t, db.Where("provider = ? AND subject = ?", model.ExternalIdentityProviderGitHub, numericID).First(&claim).Error)
	require.Equal(t, user.Id, claim.UserId)
	_, err = model.GetAuthFlow(token, model.AuthFlowMatch{Purpose: model.AuthFlowPurposeTwoFALogin})
	require.ErrorIs(t, err, model.ErrAuthFlowConsumed)
}

func TestGitHubMigrationPostgresConcurrentCompletionIssuesOneSession(t *testing.T) {
	db := githubMigrationPostgresDB(t)
	user, token := githubMigrationPostgresCandidate(t, db, "same-flow")
	const numericID = "10001"
	const attempts = 8
	start := make(chan struct{})
	results := make(chan githubMigrationPostgresResult, attempts)
	var group sync.WaitGroup
	for range attempts {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			results <- githubMigrationPostgresLogin(user, githubMigrationPostgresAction(user, token, numericID, nil, nil))
		}()
	}
	close(start)
	group.Wait()
	close(results)
	var winner *AuthBundle
	for result := range results {
		if result.err == nil {
			require.NotNil(t, result.bundle, "every successful completion must issue a session")
			require.Nil(t, winner, "the same challenge cannot issue a second session")
			winner = result.bundle
		} else {
			require.ErrorIs(t, result.err, model.ErrAuthFlowConsumed)
			require.Nil(t, result.bundle, "a rejected completion must not return tokens")
		}
	}
	githubMigrationPostgresCounts(t, db, 1, 1)
	githubMigrationPostgresCommitted(t, db, user, token, numericID, winner)
	replayed := githubMigrationPostgresLogin(user, githubMigrationPostgresAction(user, token, numericID, nil, nil))
	require.ErrorIs(t, replayed.err, model.ErrAuthFlowConsumed)
	require.Nil(t, replayed.bundle)
	githubMigrationPostgresCounts(t, db, 1, 1)
}

func TestGitHubMigrationPostgresConcurrentCandidatesHaveOneOwner(t *testing.T) {
	db := githubMigrationPostgresDB(t)
	first, firstToken := githubMigrationPostgresCandidate(t, db, "first")
	second, secondToken := githubMigrationPostgresCandidate(t, db, "second")
	const numericID = "10002"
	arrived := make(chan struct{}, 2)
	release := make(chan struct{})
	// Both transactions have locked their own user and consumed their own flow
	// before either claims the common identity. A single connection cannot pass.
	barrier := func(*gorm.DB) error {
		arrived <- struct{}{}
		select {
		case <-release:
			return nil
		case <-time.After(10 * time.Second):
			return fmt.Errorf("concurrent identity qualification barrier timed out")
		}
	}
	results := make(chan githubMigrationPostgresResult, 2)
	for _, candidate := range []struct {
		user  *model.User
		token string
	}{{first, firstToken}, {second, secondToken}} {
		go func() {
			results <- githubMigrationPostgresLogin(candidate.user, githubMigrationPostgresAction(candidate.user, candidate.token, numericID, barrier, nil))
		}()
	}
	for range 2 {
		select {
		case <-arrived:
		case <-time.After(10 * time.Second):
			close(release)
			for range 2 {
				<-results
			}
			t.Fatal("both candidates must enter independent PostgreSQL transactions")
		}
	}
	close(release)
	// Drain both workers before an assertion can restore the fixture globals.
	outcomes := []githubMigrationPostgresResult{<-results, <-results}
	var winner, loser githubMigrationPostgresResult
	for _, result := range outcomes {
		if result.err == nil {
			require.Nil(t, winner.user, "a numeric identity must have one owner")
			winner = result
		} else {
			require.ErrorIs(t, result.err, model.ErrExternalIdentityAlreadyClaimed)
			require.Nil(t, result.bundle)
			loser = result
		}
	}
	require.NotNil(t, winner.user)
	require.NotNil(t, loser.user)
	tokens := map[int]string{first.Id: firstToken, second.Id: secondToken}
	githubMigrationPostgresCounts(t, db, 1, 1)
	githubMigrationPostgresCommitted(t, db, winner.user, tokens[winner.user.Id], numericID, winner.bundle)
	githubMigrationPostgresFreshState(t, db, loser.user, tokens[loser.user.Id])
	var owners int64
	require.NoError(t, db.Model(&model.User{}).Where("github_id = ?", numericID).Count(&owners).Error)
	require.EqualValues(t, 1, owners)
}

func TestGitHubMigrationPostgresBackupCodeRollsBackWhenSessionInsertFails(t *testing.T) {
	db := githubMigrationPostgresDB(t)
	require.NoError(t, db.AutoMigrate(&model.TwoFA{}, &model.TwoFABackupCode{}))
	user, token := githubMigrationPostgresCandidate(t, db, "backup")
	const backupCode, numericID = "ABCD-EFGH", "10003"
	hash, err := common.HashBackupCode(backupCode)
	require.NoError(t, err)
	factor := &model.TwoFA{UserId: user.Id, Secret: "JBSWY3DPEHPK3PXP", IsEnabled: true, FailedAttempts: 2}
	backup := &model.TwoFABackupCode{UserId: user.Id, CodeHash: hash}
	require.NoError(t, db.Create(factor).Error)
	require.NoError(t, db.Create(backup).Error)
	valid, err := model.CheckLoginTwoFactorCode(user.Id, backupCode)
	require.NoError(t, err)
	require.True(t, valid)
	action := githubMigrationPostgresAction(user, token, numericID, nil, func(tx *gorm.DB) error {
		return model.ConsumeLoginTwoFactorCodeWithTx(tx, user.Id, backupCode)
	})
	require.NoError(t, db.Exec(`CREATE FUNCTION reject_github_session() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'session insert unavailable'; END $$`).Error)
	require.NoError(t, db.Exec(`CREATE TRIGGER reject_github_session BEFORE INSERT ON user_sessions FOR EACH ROW EXECUTE FUNCTION reject_github_session()`).Error)
	failed := githubMigrationPostgresLogin(user, action)
	require.ErrorContains(t, failed.err, "session insert unavailable")
	require.Nil(t, failed.bundle, "a failed insert must not expose signed access or refresh tokens")
	githubMigrationPostgresCounts(t, db, 0, 0)
	githubMigrationPostgresFreshState(t, db, user, token)
	var storedBackup model.TwoFABackupCode
	require.NoError(t, db.First(&storedBackup, backup.Id).Error)
	require.False(t, storedBackup.IsUsed)
	require.Nil(t, storedBackup.UsedAt)
	var storedFactor model.TwoFA
	require.NoError(t, db.First(&storedFactor, factor.Id).Error)
	require.Equal(t, factor.FailedAttempts, storedFactor.FailedAttempts)
	require.Nil(t, storedFactor.LastUsedAt)
	require.NoError(t, db.Exec(`DROP TRIGGER reject_github_session ON user_sessions`).Error)
	valid, err = model.CheckLoginTwoFactorCode(user.Id, backupCode)
	require.NoError(t, err)
	require.True(t, valid, "the same backup code must remain usable after rollback")
	retried := githubMigrationPostgresLogin(user, action)
	require.NoError(t, retried.err)
	githubMigrationPostgresCounts(t, db, 1, 1)
	githubMigrationPostgresCommitted(t, db, user, token, numericID, retried.bundle)
	require.NoError(t, db.First(&storedBackup, backup.Id).Error)
	require.True(t, storedBackup.IsUsed)
	require.NotNil(t, storedBackup.UsedAt)
	require.NoError(t, db.First(&storedFactor, factor.Id).Error)
	require.Zero(t, storedFactor.FailedAttempts)
	require.NotNil(t, storedFactor.LastUsedAt)
	valid, err = model.CheckLoginTwoFactorCode(user.Id, backupCode)
	require.NoError(t, err)
	require.False(t, valid, "a committed backup code must not authorize another login")
}

func TestGitHubMigrationPostgresSessionLimitsPreserveBindingAndChallenge(t *testing.T) {
	for _, limit := range []string{"active", "issuance"} {
		t.Run(limit, func(t *testing.T) {
			db := githubMigrationPostgresDB(t)
			user, token := githubMigrationPostgresCandidate(t, db, limit)
			now := time.Now().Unix()
			existing := &model.UserSession{
				SID: "existing-" + limit, UserID: user.Id, Version: 1, UserAuthVersion: user.AuthVersion,
				Status: model.UserSessionStatusActive, RefreshHash: strings.Repeat("a", 64), LoginMethod: "password",
				CreatedAt: now, LastActiveAt: now, ExpiresAt: now + 3600,
			}
			expectedError := model.ErrUserSessionLimit
			if limit == "active" {
				common.UserSessionActiveLimit = 1
			} else {
				common.UserSessionIssuanceLimit = 1
				existing.Status, existing.RevokedAt = model.UserSessionStatusRevoked, now
				expectedError = model.ErrUserSessionIssuanceLimit
			}
			require.NoError(t, db.Create(existing).Error)
			result := githubMigrationPostgresLogin(user, githubMigrationPostgresAction(user, token, "10004", nil, nil))
			require.ErrorIs(t, result.err, expectedError)
			require.Nil(t, result.bundle)
			githubMigrationPostgresCounts(t, db, 1, 0)
			githubMigrationPostgresFreshState(t, db, user, token)
			var stored model.UserSession
			require.NoError(t, db.First(&stored, "sid = ?", existing.SID).Error)
			require.Equal(t, *existing, stored, "a limit rejection must preserve existing sessions")
		})
	}
}
