package oauthserver

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Unlike the in-memory policy, this policy exercises a real permission lookup
// on the same writer database used by OAuth. It cannot check out a root pool.
type databasePolicy struct {
	before func(context.Context, *gorm.DB, Grant) error
	calls  atomic.Int64
}

func (p *databasePolicy) Authorize(ctx context.Context, tx *gorm.DB, grant Grant) error {
	p.calls.Add(1)
	if tx == nil || tx.Statement == nil || tx.Statement.Context != ctx {
		return errors.New("missing policy transaction/context")
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return errors.New("policy received a root connection")
	}
	if p.before != nil {
		if err := p.before(ctx, tx, grant); err != nil {
			return err
		}
	}
	var subject struct{ Enabled bool }
	if err := tx.Table("oauth_policy_subjects").Where("id = ?", grant.UserID).Take(&subject).Error; err != nil {
		return err
	}
	if !subject.Enabled {
		return errors.New("subject disabled")
	}
	return nil
}

func createPolicySubject(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.Exec("CREATE TABLE oauth_policy_subjects (id BIGINT PRIMARY KEY, enabled BOOLEAN NOT NULL)").Error)
	require.NoError(t, db.Exec("INSERT INTO oauth_policy_subjects (id, enabled) VALUES (?, ?)", 42, true).Error)
}

// Construct the preceding steps with the ordinary test policy. Only the action
// under test gets the DB-backed policy and constrained connection pool.
func policyAction(t *testing.T, s *Server, stage string) func(context.Context) error {
	t.Helper()
	switch stage {
	case "prepare":
		pending, err := s.BeginAuthorization(context.Background(), authorizationValues().Encode(), testBrowser)
		require.NoError(t, err)
		return func(ctx context.Context) error {
			_, err := s.TrustedPrepareConsent(ctx, pending.Transaction, testBrowser, 42)
			return err
		}
	case "approve":
		flow := prepareFlow(t, s)
		return func(ctx context.Context) error {
			_, err := s.TrustedApprove(ctx, flow.pending.Transaction, testBrowser, flow.consent.Secret)
			return err
		}
	case "code":
		flow := approveFlow(t, s)
		return func(ctx context.Context) error {
			_, err := s.Exchange(ctx, codeValues(flow.code).Encode(), SenderBinding{})
			return err
		}
	case "refresh":
		tokens, _ := issueTokens(t, s)
		return func(ctx context.Context) error {
			_, err := s.Exchange(ctx, refreshValues(tokens.RefreshToken).Encode(), SenderBinding{})
			return err
		}
	case "access":
		tokens, _ := issueTokens(t, s)
		return func(ctx context.Context) error {
			_, err := s.ValidateAccess(ctx, AccessRequest{Token: tokens.AccessToken, Resource: testResource})
			return err
		}
	default:
		t.Fatalf("unknown policy stage %q", stage)
		return nil
	}
}

func TestDatabasePolicySingleConnectionAtEveryStage(t *testing.T) {
	for _, stage := range []string{"prepare", "approve", "code", "refresh", "access"} {
		t.Run(stage, func(t *testing.T) {
			forDatabases(t, func(t *testing.T, db, _ *gorm.DB) {
				createPolicySubject(t, db)
				s, _, _ := testServer(t, db)
				action := policyAction(t, s, stage)
				policy := &databasePolicy{}
				s.policy = policy
				pool, err := db.DB()
				require.NoError(t, err)
				pool.SetMaxOpenConns(1)
				waits := pool.Stats().WaitCount
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				require.NoError(t, action(ctx))
				require.NoError(t, ctx.Err())
				require.Equal(t, int64(1), policy.calls.Load())
				require.Equal(t, waits, pool.Stats().WaitCount, "policy must not acquire a second connection")
			})
		})
	}
}

// The original deterministic deadlock: one refresh owns the family lock and
// the other owns the last pool connection while waiting for that same lock.
// The winner must run Policy on its existing transaction, then the replay must
// still commit whole-family revocation (not time out or issue a second pair).
func TestDatabasePolicyConcurrentRefreshWithSaturatedPool(t *testing.T) {
	forDatabases(t, func(t *testing.T, db, _ *gorm.DB) {
		createPolicySubject(t, db)
		s, _, _ := testServer(t, db)
		original, _ := issueTokens(t, s)
		pool, err := db.DB()
		require.NoError(t, err)
		pool.SetMaxOpenConns(2)
		secondOwnsConnection := make(chan struct{})
		var updates atomic.Int64
		require.NoError(t, db.Callback().Update().Before("gorm:update").Register("policy_saturated_pool", func(tx *gorm.DB) {
			if tx.Statement.Table == "oauth_server_grants" && updates.Add(1) == 2 {
				close(secondOwnsConnection)
			}
		}))
		t.Cleanup(func() { require.NoError(t, db.Callback().Update().Remove("policy_saturated_pool")) })
		policy := &databasePolicy{before: func(ctx context.Context, _ *gorm.DB, _ Grant) error {
			select {
			case <-secondOwnsConnection:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}}
		s.policy = policy
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		type result struct {
			tokens *TokenResponse
			err    error
		}
		results, start := make(chan result, 2), make(chan struct{})
		waits := pool.Stats().WaitCount
		for range 2 {
			go func() {
				<-start
				tokens, err := s.Exchange(ctx, refreshValues(original.RefreshToken).Encode(), SenderBinding{})
				results <- result{tokens, err}
			}()
		}
		close(start)
		var issued []*TokenResponse
		for range 2 {
			select {
			case result := <-results:
				if result.err == nil {
					issued = append(issued, result.tokens)
				} else {
					expectProtocol(t, result.err, "invalid_grant")
					require.Nil(t, result.tokens)
				}
			case <-ctx.Done():
				t.Fatal("refreshes starved the saturated writer pool")
			}
		}
		require.NoError(t, ctx.Err())
		require.Len(t, issued, 1)
		require.Equal(t, int64(1), policy.calls.Load())
		require.Equal(t, waits, pool.Stats().WaitCount)
		expectRevoked(t, s, original)
		expectRevoked(t, s, issued[0])
		var count int64
		require.NoError(t, db.Model(&model.OAuthServerToken{}).Count(&count).Error)
		require.Equal(t, int64(4), count)
	})
}

func TestDatabasePolicySeesUncommittedIssuanceState(t *testing.T) {
	for _, stage := range []string{"prepare", "approve", "code", "refresh"} {
		t.Run(stage, func(t *testing.T) {
			forDatabases(t, func(t *testing.T, db, _ *gorm.DB) {
				createPolicySubject(t, db)
				s, _, _ := testServer(t, db)
				action := policyAction(t, s, stage)
				policy := &databasePolicy{}
				s.policy = policy
				var injected atomic.Bool
				require.NoError(t, db.Callback().Update().After("gorm:update").Register("policy_uncommitted_state", func(tx *gorm.DB) {
					if !injected.Swap(true) {
						tx.AddError(tx.Session(&gorm.Session{NewDB: true}).Exec("UPDATE oauth_policy_subjects SET enabled = ? WHERE id = ?", false, 42).Error)
					}
				}))
				t.Cleanup(func() { require.NoError(t, db.Callback().Update().Remove("policy_uncommitted_state")) })
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				err := action(ctx)
				if stage == "prepare" || stage == "approve" {
					expectProtocol(t, err, "access_denied")
				} else {
					expectProtocol(t, err, "invalid_grant")
				}
				require.True(t, injected.Load())
				require.Equal(t, int64(1), policy.calls.Load())
				require.NoError(t, ctx.Err())
			})
		})
	}
}

func TestAccessPolicyUsesTokenSnapshot(t *testing.T) {
	forDatabases(t, func(t *testing.T, db, other *gorm.DB) {
		createPolicySubject(t, db)
		s, _, _ := testServer(t, db)
		tokens, _ := issueTokens(t, s)
		var changed atomic.Bool
		s.policy = &databasePolicy{before: func(ctx context.Context, tx *gorm.DB, _ Grant) error {
			if tx.Dialector.Name() == "postgres" {
				var isolation string
				require.NoError(t, tx.Raw("SHOW transaction_isolation").Scan(&isolation).Error)
				require.Equal(t, "repeatable read", isolation)
			}
			if changed.Swap(true) {
				return nil
			}
			return other.WithContext(ctx).Exec("UPDATE oauth_policy_subjects SET enabled = ? WHERE id = ?", false, 42).Error
		}}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		// This already-in-flight validation observes the original snapshot. A
		// subsequent request must observe the newly committed denial.
		_, err := s.ValidateAccess(ctx, AccessRequest{Token: tokens.AccessToken, Resource: testResource})
		require.NoError(t, err)
		_, err = s.ValidateAccess(ctx, AccessRequest{Token: tokens.AccessToken, Resource: testResource})
		expectProtocol(t, err, "invalid_token")
	})
}
