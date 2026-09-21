package oauthserver

import (
	"context"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSlowPolicyCannotExtendExpiredCapabilities(t *testing.T) {
	forDatabases(t, func(t *testing.T, db, _ *gorm.DB) {
		t.Run("identity", func(t *testing.T) {
			s, clock, policy := testServer(t, db)
			pending, err := s.BeginAuthorization(context.Background(), authorizationValues().Encode(), testBrowser)
			require.NoError(t, err)
			policy.hook = func(Grant) error { clock.advance(AuthorizationTTL); return nil }
			_, err = s.TrustedPrepareConsent(context.Background(), pending.Transaction, testBrowser, 42)
			expectProtocol(t, err, "invalid_request")
		})
		t.Run("consent", func(t *testing.T) {
			s, clock, policy := testServer(t, db)
			flow := prepareFlow(t, s)
			policy.hook = func(Grant) error { clock.advance(AuthorizationTTL); return nil }
			_, err := s.TrustedApprove(context.Background(), flow.pending.Transaction, testBrowser, flow.consent.Secret)
			expectProtocol(t, err, "invalid_request")
		})
		t.Run("code", func(t *testing.T) {
			s, clock, policy := testServer(t, db)
			flow := approveFlow(t, s)
			policy.hook = func(Grant) error { clock.advance(CodeTTL); return nil }
			_, err := s.Exchange(context.Background(), codeValues(flow.code).Encode(), SenderBinding{})
			expectProtocol(t, err, "invalid_grant")
		})
		t.Run("refresh", func(t *testing.T) {
			s, clock, policy := testServer(t, db)
			tokens, _ := issueTokens(t, s)
			policy.hook = func(Grant) error { clock.advance(DefaultRefreshIdleTTL); return nil }
			_, err := s.Exchange(context.Background(), refreshValues(tokens.RefreshToken).Encode(), SenderBinding{})
			expectProtocol(t, err, "invalid_grant")
		})
		t.Run("access", func(t *testing.T) {
			s, clock, policy := testServer(t, db)
			tokens, _ := issueTokens(t, s)
			policy.hook = func(Grant) error { clock.advance(AccessTTL); return nil }
			_, err := s.ValidateAccess(context.Background(), AccessRequest{Token: tokens.AccessToken, Resource: testResource})
			expectProtocol(t, err, "invalid_token")
		})
	})
}

func TestReplayCannotBeHiddenByScopeEscalation(t *testing.T) {
	forDatabases(t, func(t *testing.T, db, _ *gorm.DB) {
		s, _, _ := testServer(t, db)
		original, _ := issueTokens(t, s)
		child, err := s.Exchange(context.Background(), refreshValues(original.RefreshToken).Encode(), SenderBinding{})
		require.NoError(t, err)
		replay := refreshValues(original.RefreshToken)
		replay.Set("scope", "admin")
		_, err = s.Exchange(context.Background(), replay.Encode(), SenderBinding{})
		expectProtocol(t, err, "invalid_grant")
		expectRevoked(t, s, child)
	})
}

func TestDatabaseFailuresFailClosed(t *testing.T) {
	forDatabases(t, func(t *testing.T, db, _ *gorm.DB) {
		s, _, _ := testServer(t, db)
		tokens, _ := issueTokens(t, s)
		require.NoError(t, db.Migrator().DropTable(&model.OAuthServerToken{}))
		_, err := s.ValidateAccess(context.Background(), AccessRequest{Token: tokens.AccessToken, Resource: testResource})
		expectProtocol(t, err, "server_error")
		_, err = s.Exchange(context.Background(), refreshValues(tokens.RefreshToken).Encode(), SenderBinding{})
		expectProtocol(t, err, "server_error")
		err = s.Revoke(context.Background(), "client_id=pi-native&token="+tokens.RefreshToken)
		expectProtocol(t, err, "server_error")
		require.NoError(t, db.Migrator().DropTable(&model.OAuthServerAuthorization{}))
		_, err = s.BeginAuthorization(context.Background(), authorizationValues().Encode(), testBrowser)
		expectProtocol(t, err, "server_error")
	})
}

func TestCancelledContextCannotConsumeGrant(t *testing.T) {
	forDatabases(t, func(t *testing.T, db, _ *gorm.DB) {
		s, _, _ := testServer(t, db)
		flow := approveFlow(t, s)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := s.Exchange(ctx, codeValues(flow.code).Encode(), SenderBinding{})
		expectProtocol(t, err, "server_error")
		tokens, err := s.Exchange(context.Background(), codeValues(flow.code).Encode(), SenderBinding{})
		require.NoError(t, err)
		verify(t, s, tokens.AccessToken)
	})
}

func TestReservedStoredSenderBindingFailsClosed(t *testing.T) {
	forDatabases(t, func(t *testing.T, db, _ *gorm.DB) {
		s, _, _ := testServer(t, db)
		tokens, _ := issueTokens(t, s)
		grant := verify(t, s, tokens.AccessToken)
		require.NoError(t, db.Model(&model.OAuthServerGrant{}).Where("id = ?", grant.FamilyID).Updates(map[string]any{"binding_method": "dpop", "binding_thumbprint": "future-key"}).Error)
		_, err := s.ValidateAccess(context.Background(), AccessRequest{Token: tokens.AccessToken, Resource: testResource})
		expectProtocol(t, err, "invalid_token")
		_, err = s.Exchange(context.Background(), refreshValues(tokens.RefreshToken).Encode(), SenderBinding{})
		expectProtocol(t, err, "invalid_grant")
	})
}
