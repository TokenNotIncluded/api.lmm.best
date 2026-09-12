package oauthserver

import (
	"context"
	"errors"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func concurrentExchanges(t *testing.T, servers []*Server, raw string) []*TokenResponse {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	type result struct {
		token *TokenResponse
		err   error
	}
	results, start := make(chan result, 12), make(chan struct{})
	for i := range 12 {
		s := servers[i%len(servers)]
		go func() {
			<-start
			token, err := s.Exchange(ctx, raw, SenderBinding{})
			results <- result{token, err}
		}()
	}
	close(start)
	var issued []*TokenResponse
	for range 12 {
		select {
		case result := <-results:
			if result.err == nil {
				issued = append(issued, result.token)
			} else {
				expectProtocol(t, result.err, "invalid_grant")
				require.Nil(t, result.token)
			}
		case <-ctx.Done():
			t.Fatal("concurrent exchange timed out")
		}
	}
	return issued
}

func TestConcurrentCodeExchangeAcrossIndependentPools(t *testing.T) {
	forDatabases(t, func(t *testing.T, db, otherDB *gorm.DB) {
		first, _, _ := testServer(t, db)
		second, _, _ := testServer(t, otherDB)
		flow := approveFlow(t, first)
		issued := concurrentExchanges(t, []*Server{first, second}, codeValues(flow.code).Encode())
		require.Len(t, issued, 1)
		// A legitimate-PKCE replay revokes the winner; no second token pair.
		expectRevoked(t, second, issued[0])
		var count int64
		require.NoError(t, db.Model(&model.OAuthServerToken{}).Count(&count).Error)
		require.Equal(t, int64(2), count)
	})
}

func TestConcurrentRefreshReplayRevokesWholeFamilyAcrossPools(t *testing.T) {
	forDatabases(t, func(t *testing.T, db, otherDB *gorm.DB) {
		first, _, _ := testServer(t, db)
		second, _, _ := testServer(t, otherDB)
		original, _ := issueTokens(t, first)
		issued := concurrentExchanges(t, []*Server{first, second}, refreshValues(original.RefreshToken).Encode())
		require.Len(t, issued, 1)
		expectRevoked(t, second, original)
		expectRevoked(t, first, issued[0])
		var count int64
		require.NoError(t, db.Model(&model.OAuthServerToken{}).Count(&count).Error)
		require.Equal(t, int64(4), count)
	})
}

func TestConcurrentConsentDecisionIsSingleUse(t *testing.T) {
	forDatabases(t, func(t *testing.T, db, otherDB *gorm.DB) {
		first, _, _ := testServer(t, db)
		second, _, _ := testServer(t, otherDB)
		flow := prepareFlow(t, first)
		start := make(chan struct{})
		results := make(chan error, 8)
		for i := range 8 {
			s := []*Server{first, second}[i%2]
			go func() {
				<-start
				_, err := s.TrustedApprove(context.Background(), flow.pending.Transaction, testBrowser, flow.consent.Secret)
				results <- err
			}()
		}
		close(start)
		successes := 0
		for range 8 {
			if err := <-results; err != nil {
				expectProtocol(t, err, "invalid_request")
			} else {
				successes++
			}
		}
		require.Equal(t, 1, successes)
		var count int64
		require.NoError(t, db.Model(&model.OAuthServerCode{}).Count(&count).Error)
		require.Equal(t, int64(1), count)
	})
}

func TestConcurrentRefreshAndRevocationCannotEscapeFamilyLock(t *testing.T) {
	forDatabases(t, func(t *testing.T, db, otherDB *gorm.DB) {
		first, _, _ := testServer(t, db)
		second, _, _ := testServer(t, otherDB)
		for range 10 {
			original, _ := issueTokens(t, first)
			var rotated *TokenResponse
			var refreshErr, revokeErr error
			start := make(chan struct{})
			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				defer wg.Done()
				<-start
				rotated, refreshErr = first.Exchange(context.Background(), refreshValues(original.RefreshToken).Encode(), SenderBinding{})
			}()
			go func() {
				defer wg.Done()
				<-start
				revokeErr = second.Revoke(context.Background(), url.Values{"client_id": {"pi-native"}, "token": {original.AccessToken}}.Encode())
			}()
			close(start)
			wg.Wait()
			require.NoError(t, revokeErr)
			if refreshErr != nil {
				expectProtocol(t, refreshErr, "invalid_grant")
			}
			expectRevoked(t, first, original)
			if rotated != nil {
				expectRevoked(t, second, rotated)
			}
		}
	})
}

func TestTokenInsertFailureRollsBackConsumption(t *testing.T) {
	forDatabases(t, func(t *testing.T, db, _ *gorm.DB) {
		s, _, _ := testServer(t, db)
		flow := approveFlow(t, s)
		failing := true
		require.NoError(t, db.Callback().Create().Before("gorm:create").Register("oauth_test_fail_tokens", func(tx *gorm.DB) {
			if failing && tx.Statement.Table == "oauth_server_tokens" {
				tx.AddError(errors.New("injected token insert failure"))
			}
		}))
		t.Cleanup(func() { require.NoError(t, db.Callback().Create().Remove("oauth_test_fail_tokens")) })
		response, err := s.Exchange(context.Background(), codeValues(flow.code).Encode(), SenderBinding{})
		require.Nil(t, response)
		expectProtocol(t, err, "server_error")
		var code model.OAuthServerCode
		require.NoError(t, db.Take(&code).Error)
		require.Zero(t, code.UsedAtMs)
		failing = false
		tokens, err := s.Exchange(context.Background(), codeValues(flow.code).Encode(), SenderBinding{})
		require.NoError(t, err)
		failing = true
		response, err = s.Exchange(context.Background(), refreshValues(tokens.RefreshToken).Encode(), SenderBinding{})
		require.Nil(t, response)
		expectProtocol(t, err, "server_error")
		var token model.OAuthServerToken
		require.NoError(t, db.Where("digest = ?", digest(tokens.RefreshToken)).Take(&token).Error)
		require.Zero(t, token.UsedAtMs)
		failing = false
		rotated, err := s.Exchange(context.Background(), refreshValues(tokens.RefreshToken).Encode(), SenderBinding{})
		require.NoError(t, err)
		verify(t, s, rotated.AccessToken)
	})
}

func TestRevocationStorageFailureDoesNotReportSuccess(t *testing.T) {
	forDatabases(t, func(t *testing.T, db, _ *gorm.DB) {
		s, _, _ := testServer(t, db)
		tokens, _ := issueTokens(t, s)
		require.NoError(t, db.Callback().Update().After("gorm:update").Register("oauth_test_fail_revoke", func(tx *gorm.DB) {
			if updates, ok := tx.Statement.Dest.(map[string]any); ok && updates["revocation_reason"] != nil {
				tx.AddError(errors.New("injected failure after revocation write"))
			}
		}))
		t.Cleanup(func() { require.NoError(t, db.Callback().Update().Remove("oauth_test_fail_revoke")) })
		err := s.Revoke(context.Background(), url.Values{"client_id": {"pi-native"}, "token": {tokens.RefreshToken}}.Encode())
		expectProtocol(t, err, "server_error")
		require.NotContains(t, err.Error(), "injected")
		verify(t, s, tokens.AccessToken)
		var family model.OAuthServerGrant
		require.NoError(t, db.Take(&family).Error)
		require.Zero(t, family.RevokedAtMs)
	})
}

func TestReplayRevocationFailureReturnsServerErrorAndCanBeRetried(t *testing.T) {
	forDatabases(t, func(t *testing.T, db, _ *gorm.DB) {
		s, _, _ := testServer(t, db)
		original, _ := issueTokens(t, s)
		child, err := s.Exchange(context.Background(), refreshValues(original.RefreshToken).Encode(), SenderBinding{})
		require.NoError(t, err)
		failing := true
		require.NoError(t, db.Callback().Update().Before("gorm:update").Register("oauth_test_replay_failure", func(tx *gorm.DB) {
			if updates, ok := tx.Statement.Dest.(map[string]any); failing && ok && updates["revocation_reason"] != nil {
				tx.AddError(errors.New("injected revocation failure"))
			}
		}))
		t.Cleanup(func() { require.NoError(t, db.Callback().Update().Remove("oauth_test_replay_failure")) })
		_, err = s.Exchange(context.Background(), refreshValues(original.RefreshToken).Encode(), SenderBinding{})
		expectProtocol(t, err, "server_error")
		failing = false
		_, err = s.Exchange(context.Background(), refreshValues(original.RefreshToken).Encode(), SenderBinding{})
		expectProtocol(t, err, "invalid_grant")
		expectRevoked(t, s, child)
	})
}
