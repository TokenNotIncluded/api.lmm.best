package oauthserver

import (
	"context"
	"net/url"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func approveSnapshotValues(t *testing.T, s *Server, values url.Values) testFlow {
	t.Helper()
	pending, err := s.BeginAuthorization(context.Background(), values.Encode(), testBrowser)
	require.NoError(t, err)
	consent, err := s.TrustedPrepareConsent(context.Background(), pending.Transaction, testBrowser, 42)
	require.NoError(t, err)
	response, err := s.TrustedApprove(context.Background(), pending.Transaction, testBrowser, consent.Secret)
	require.NoError(t, err)
	target, err := url.Parse(response.RedirectURI)
	require.NoError(t, err)
	return testFlow{pending: pending, consent: consent, code: target.Query().Get("code")}
}

func TestCodeSnapshotsConsentScopeAcrossReapproval(t *testing.T) {
	forDatabases(t, func(t *testing.T, db, _ *gorm.DB) {
		s, _, _ := testServer(t, db)
		first := authorizeWithScope(t, s, "models:read")
		second := authorizeWithScope(t, s, "relay:invoke")
		third := authorizeWithScope(t, s, "models:read")
		for _, item := range []struct {
			flow  testFlow
			scope string
		}{
			{first, "models:read"},
			{second, "relay:invoke"},
			{third, "models:read"},
		} {
			pair, err := s.Exchange(context.Background(), codeValues(item.flow.code).Encode(), SenderBinding{})
			require.NoError(t, err)
			require.Equal(t, item.scope, pair.Scope, "each code must retain its own consent, including codes issued before a later approval")
			grant, err := s.ValidateAccess(context.Background(), AccessRequest{Token: pair.AccessToken, Resource: testResource, RequiredScopes: []string{item.scope}})
			require.NoError(t, err)
			require.Equal(t, []string{item.scope}, grant.Scopes)
			rotated, err := s.Exchange(context.Background(), refreshValues(pair.RefreshToken).Encode(), SenderBinding{})
			require.NoError(t, err)
			require.Equal(t, item.scope, rotated.Scope, "refresh must preserve the code's consent subset")
		}
		var families []model.OAuthServerGrant
		require.NoError(t, db.Find(&families).Error)
		require.Len(t, families, 1)
		require.Equal(t, "models:read relay:invoke", families[0].Scope, "family reuse retains historical token scope bounds")
	})
}

func TestCodeSnapshotsExactRedirectAcrossReapprovalAndReplay(t *testing.T) {
	forDatabases(t, func(t *testing.T, db, _ *gorm.DB) {
		s, _, _ := testServer(t, db)
		first := approveFlow(t, s)
		secondRedirect := "http://127.0.0.1:49214/oauth/callback"
		secondValues := authorizationValues()
		secondValues.Set("redirect_uri", secondRedirect)
		second := approveSnapshotValues(t, s, secondValues)
		firstExchange := codeValues(first.code)
		wrongFirst := codeValues(first.code)
		wrongFirst.Set("redirect_uri", secondRedirect)
		_, err := s.Exchange(context.Background(), wrongFirst.Encode(), SenderBinding{})
		expectProtocol(t, err, "invalid_grant")
		firstPair, err := s.Exchange(context.Background(), firstExchange.Encode(), SenderBinding{})
		require.NoError(t, err, "later approval must not replace an earlier code's exact callback")

		secondExchange := codeValues(second.code)
		_, err = s.Exchange(context.Background(), secondExchange.Encode(), SenderBinding{})
		expectProtocol(t, err, "invalid_grant")
		verify(t, s, firstPair.AccessToken)
		secondExchange.Set("redirect_uri", secondRedirect)
		secondPair, err := s.Exchange(context.Background(), secondExchange.Encode(), SenderBinding{})
		require.NoError(t, err)
		require.Equal(t, verify(t, s, firstPair.AccessToken).FamilyID, verify(t, s, secondPair.AccessToken).FamilyID)

		// Wrong redirect must not turn a used code into a revocation capability.
		_, err = s.Exchange(context.Background(), wrongFirst.Encode(), SenderBinding{})
		expectProtocol(t, err, "invalid_grant")
		verify(t, s, secondPair.AccessToken)
		_, err = s.Exchange(context.Background(), firstExchange.Encode(), SenderBinding{})
		expectProtocol(t, err, "invalid_grant")
		expectRevoked(t, s, firstPair)
		expectRevoked(t, s, secondPair)
	})
}

func TestCodeMissingConsentSnapshotFailsClosed(t *testing.T) {
	forDatabases(t, func(t *testing.T, db, _ *gorm.DB) {
		s, _, _ := testServer(t, db)
		existing, _ := issueTokens(t, s)
		for _, field := range []string{"client_id", "redirect_uri", "resource", "scope"} {
			flow := approveFlow(t, s)
			require.NoError(t, db.Model(&model.OAuthServerCode{}).Where("digest = ?", digest(flow.code)).UpdateColumn(field, "").Error)
			pair, err := s.Exchange(context.Background(), codeValues(flow.code).Encode(), SenderBinding{})
			require.Nil(t, pair)
			expectProtocol(t, err, "invalid_grant")
			verify(t, s, existing.AccessToken)
			var rejected model.OAuthServerCode
			require.NoError(t, db.Where("digest = ?", digest(flow.code)).Take(&rejected).Error)
			require.Zero(t, rejected.UsedAtMs)
		}
		rotated, err := s.Exchange(context.Background(), refreshValues(existing.RefreshToken).Encode(), SenderBinding{})
		require.NoError(t, err)
		verify(t, s, rotated.AccessToken)
	})
}

func TestCodeSnapshotRedirectMustRemainRegistered(t *testing.T) {
	forDatabases(t, func(t *testing.T, db, _ *gorm.DB) {
		s, clock, policy := testServer(t, db)
		first := approveFlow(t, s)
		config := testConfig()
		config.Clients[0].RedirectURIs = []string{"http://127.0.0.1/oauth/new-callback"}
		updated, err := New(db, config, policy)
		require.NoError(t, err)
		updated.now = clock.now
		values := authorizationValues()
		newRedirect := "http://127.0.0.1:49214/oauth/new-callback"
		values.Set("redirect_uri", newRedirect)
		second := approveSnapshotValues(t, updated, values)
		_, err = updated.Exchange(context.Background(), codeValues(first.code).Encode(), SenderBinding{})
		expectProtocol(t, err, "invalid_grant")
		secondExchange := codeValues(second.code)
		secondExchange.Set("redirect_uri", newRedirect)
		pair, err := updated.Exchange(context.Background(), secondExchange.Encode(), SenderBinding{})
		require.NoError(t, err)
		verify(t, updated, pair.AccessToken)
	})
}

// Recreate the pre-snapshot schema rather than just blanking new columns, so
// this checks the actual migration of populated SQLite/PostgreSQL tables.
type legacyCodeWithoutConsentSnapshot struct {
	Digest        string `gorm:"primaryKey;size:64"`
	Issuer        string `gorm:"not null;size:512"`
	FamilyID      string `gorm:"not null;size:43;index"`
	CodeChallenge string `gorm:"not null;size:43"`
	CreatedAtMs   int64  `gorm:"not null"`
	ExpiresAtMs   int64  `gorm:"not null;index"`
	UsedAtMs      int64  `gorm:"not null"`
}

func (legacyCodeWithoutConsentSnapshot) TableName() string { return "oauth_server_codes" }

func TestCodeSnapshotMigrationRejectsLegacyCodeAndPreservesTokens(t *testing.T) {
	forDatabases(t, func(t *testing.T, db, restartedDB *gorm.DB) {
		s, clock, policy := testServer(t, db)
		existing, _ := issueTokens(t, s)
		pending := approveFlow(t, s)
		var legacyRows []legacyCodeWithoutConsentSnapshot
		require.NoError(t, db.Find(&legacyRows).Error)
		require.NoError(t, db.Migrator().DropTable(&model.OAuthServerCode{}))
		require.NoError(t, db.AutoMigrate(&legacyCodeWithoutConsentSnapshot{}))
		require.NoError(t, db.Create(&legacyRows).Error)
		require.NoError(t, model.MigrateOAuthServer(db))
		require.NoError(t, model.MigrateOAuthServer(db), "migration must be idempotent")
		// Deployment restarts workers after migration. Use the unused second
		// pool to model those new connections: dropping/recreating this fixture
		// changes SELECT * column order and invalidates pgx's old cached plans.
		var err error
		s, err = New(restartedDB, testConfig(), policy)
		require.NoError(t, err)
		s.now = clock.now
		db = restartedDB
		var migrated model.OAuthServerCode
		require.NoError(t, db.Where("digest = ?", digest(pending.code)).Take(&migrated).Error)
		require.Empty(t, migrated.ClientID)
		require.Empty(t, migrated.RedirectURI)
		require.Empty(t, migrated.Resource)
		require.Empty(t, migrated.Scope)
		_, err = s.Exchange(context.Background(), codeValues(pending.code).Encode(), SenderBinding{})
		expectProtocol(t, err, "invalid_grant")
		verify(t, s, existing.AccessToken)
		rotated, err := s.Exchange(context.Background(), refreshValues(existing.RefreshToken).Encode(), SenderBinding{})
		require.NoError(t, err)
		verify(t, s, rotated.AccessToken)
		newPair, _ := issueTokens(t, s)
		verify(t, s, newPair.AccessToken)
		require.NoError(t, s.Revoke(context.Background(), url.Values{"client_id": {"pi-native"}, "token": {rotated.RefreshToken}}.Encode()))
		expectRevoked(t, s, newPair)
	})
}

func TestBrowserBindingApprovalRechecksExpiryAfterPolicy(t *testing.T) {
	forDatabases(t, func(t *testing.T, db, _ *gorm.DB) {
		s, clock, policy := testServer(t, db)
		prepareFlow(t, s)
		clock.advance(AuthorizationTTL - time.Millisecond)
		policy.hook = func(Grant) error {
			clock.advance(time.Millisecond)
			return nil
		}
		response, err := s.TrustedApproveByBrowserBinding(context.Background(), testBrowser)
		require.Nil(t, response)
		expectProtocol(t, err, "invalid_request")
		var codes int64
		require.NoError(t, db.Model(&model.OAuthServerCode{}).Count(&codes).Error)
		require.Zero(t, codes)
	})
}
