package oauthserver_test

import (
	"context"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/oauthserver"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const (
	policyBrowser  = "trusted-production-policy-test-browser-binding"
	policyVerifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	policyRedirect = "http://127.0.0.1:49213/oauth/lmm/callback"
)

func productionPolicyFixture(t *testing.T, db *gorm.DB, paid bool) *service.OAuthIntegration {
	t.Helper()
	previousDialect := common.MainDatabaseType()
	common.SetMainDatabaseType(common.DatabaseType(db.Dialector.Name()))
	t.Cleanup(func() { common.SetMainDatabaseType(previousDialect) })
	require.False(t, model.LocalAcceptanceDeveloperAccessEnabled(), "production policy must not use a development bypass")
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.TopUp{}))
	level := 1
	user := model.User{Id: 42, Username: "oauth-policy-user", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", TrustLevelOverride: &level}
	if paid {
		user.TrustLevelOverride = nil
	}
	require.NoError(t, db.Create(&user).Error)
	if paid {
		require.NoError(t, db.Create(&model.TopUp{Id: 1, UserId: 42, TradeNo: "oauth-policy-paid", PaymentProvider: model.PaymentProviderStripe, PaymentMethod: model.PaymentMethodStripe, Status: common.TopUpStatusSuccess, SettledAmountMicros: 1000000, CreditedQuota: 1000000}).Error)
	}
	integration, err := service.NewOAuthIntegration(db, service.OAuthServerConfig{Enabled: true, Issuer: "https://auth.lmm.test", Groups: []string{"default"}})
	require.NoError(t, err)
	return integration
}

func productionPolicyQuery(integration *service.OAuthIntegration) string {
	return url.Values{
		"response_type": {"code"}, "client_id": {service.OAuthPiClientID}, "redirect_uri": {policyRedirect},
		"resource": {integration.Resource}, "state": {"native-client-state-at-least-128-bits"},
		"scope":          {strings.Join([]string{service.OAuthCatalogScope, service.OAuthBalanceScope, service.OAuthInvokeScope, service.OAuthGroupScope("default")}, " ")},
		"code_challenge": {"E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"}, "code_challenge_method": {"S256"},
	}.Encode()
}

func productionConsent(t *testing.T, ctx context.Context, s *service.OAuthIntegration) *oauthserver.Consent {
	t.Helper()
	pending, err := s.Core.BeginAuthorization(ctx, productionPolicyQuery(s), policyBrowser)
	require.NoError(t, err)
	consent, err := s.Core.TrustedPrepareConsent(ctx, pending.Transaction, policyBrowser, 42)
	require.NoError(t, err)
	return consent
}

func productionCode(t *testing.T, ctx context.Context, s *service.OAuthIntegration) string {
	t.Helper()
	consent := productionConsent(t, ctx, s)
	response, err := s.Core.TrustedApprove(ctx, consent.Transaction, policyBrowser, consent.Secret)
	require.NoError(t, err)
	redirect, err := url.Parse(response.RedirectURI)
	require.NoError(t, err)
	return url.Values{"grant_type": {"authorization_code"}, "client_id": {service.OAuthPiClientID}, "code": {redirect.Query().Get("code")}, "redirect_uri": {policyRedirect}, "code_verifier": {policyVerifier}, "resource": {s.Resource}}.Encode()
}

func productionRefresh(s *service.OAuthIntegration, token string) string {
	return url.Values{"grant_type": {"refresh_token"}, "client_id": {service.OAuthPiClientID}, "refresh_token": {token}, "resource": {s.Resource}}.Encode()
}

func TestProductionPolicySingleConnectionLifecycle(t *testing.T) {
	for _, activation := range []string{"explicit", "paid"} {
		t.Run(activation, func(t *testing.T) {
			oauthserver.ForTestDatabases(t, func(t *testing.T, db, _ *gorm.DB) {
				s := productionPolicyFixture(t, db, activation == "paid")
				pool, err := db.DB()
				require.NoError(t, err)
				pool.SetMaxOpenConns(1)
				waits := pool.Stats().WaitCount
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				code := productionCode(t, ctx, s)
				tokens, err := s.Core.Exchange(ctx, code, oauthserver.SenderBinding{})
				require.NoError(t, err)
				rotated, err := s.Core.Exchange(ctx, productionRefresh(s, tokens.RefreshToken), oauthserver.SenderBinding{})
				require.NoError(t, err)
				grant, err := s.Core.ValidateAccess(ctx, oauthserver.AccessRequest{Token: rotated.AccessToken, Resource: s.Resource, RequiredScopes: []string{service.OAuthInvokeScope}})
				require.NoError(t, err)
				require.Equal(t, int64(42), grant.UserID)
				require.NoError(t, ctx.Err())
				require.Equal(t, waits, pool.Stats().WaitCount, "real user AND paid-access queries must use the current transaction")
			})
		})
	}
}

func productionPolicyAction(t *testing.T, ctx context.Context, s *service.OAuthIntegration, stage string) func() error {
	t.Helper()
	switch stage {
	case "prepare":
		pending, err := s.Core.BeginAuthorization(ctx, productionPolicyQuery(s), policyBrowser)
		require.NoError(t, err)
		return func() error {
			_, err := s.Core.TrustedPrepareConsent(ctx, pending.Transaction, policyBrowser, 42)
			return err
		}
	case "approve":
		consent := productionConsent(t, ctx, s)
		return func() error {
			_, err := s.Core.TrustedApprove(ctx, consent.Transaction, policyBrowser, consent.Secret)
			return err
		}
	case "code":
		code := productionCode(t, ctx, s)
		return func() error {
			_, err := s.Core.Exchange(ctx, code, oauthserver.SenderBinding{})
			return err
		}
	default:
		tokens, err := s.Core.Exchange(ctx, productionCode(t, ctx, s), oauthserver.SenderBinding{})
		require.NoError(t, err)
		return func() error {
			if stage == "refresh" {
				_, err := s.Core.Exchange(ctx, productionRefresh(s, tokens.RefreshToken), oauthserver.SenderBinding{})
				return err
			}
			_, err := s.Core.ValidateAccess(ctx, oauthserver.AccessRequest{Token: tokens.AccessToken, Resource: s.Resource})
			return err
		}
	}
}

func TestProductionPolicyCommittedDisableAtEveryStage(t *testing.T) {
	for _, stage := range []string{"prepare", "approve", "code", "refresh", "access"} {
		t.Run(stage, func(t *testing.T) {
			oauthserver.ForTestDatabases(t, func(t *testing.T, db, other *gorm.DB) {
				s := productionPolicyFixture(t, db, true)
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				action := productionPolicyAction(t, ctx, s, stage)
				require.NoError(t, other.WithContext(ctx).Model(&model.User{}).Where("id = ?", 42).Update("status", common.UserStatusDisabled).Error)
				var protocol *oauthserver.ProtocolError
				require.ErrorAs(t, action(), &protocol)
				expected := "invalid_grant"
				if stage == "prepare" || stage == "approve" {
					expected = "access_denied"
				} else if stage == "access" {
					expected = "invalid_token"
				}
				require.Equal(t, expected, protocol.Code)
			})
		})
	}
}

func TestProductionPolicyUsesUncommittedPermissionFacts(t *testing.T) {
	oauthserver.ForTestDatabases(t, func(t *testing.T, db, _ *gorm.DB) {
		s := productionPolicyFixture(t, db, true)
		pool, err := db.DB()
		require.NoError(t, err)
		pool.SetMaxOpenConns(1)
		grant := oauthserver.Grant{UserID: 42, ClientID: service.OAuthPiClientID, Resource: s.Resource, Scopes: []string{service.OAuthGroupScope("default")}}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			require.NoError(t, s.Authorize(ctx, tx, grant))
			require.NoError(t, tx.Model(&model.TopUp{}).Where("id = ?", 1).Update("status", "failed").Error)
			require.ErrorIs(t, s.Authorize(ctx, tx, grant), service.ErrOAuthDenied)
			require.NoError(t, tx.Model(&model.TopUp{}).Where("id = ?", 1).Update("status", common.TopUpStatusSuccess).Error)
			require.NoError(t, tx.Model(&model.User{}).Where("id = ?", 42).Update("trust_level_override", 0).Error)
			require.ErrorIs(t, s.Authorize(ctx, tx, grant), service.ErrOAuthDenied)
			return nil
		}))
		require.ErrorIs(t, s.Authorize(ctx, nil, grant), service.ErrOAuthDenied)
	})
}

// PostgreSQL READ COMMITTED issuance must hold its permission fact locks until
// the credential insert commits. This guards against an outside-tx workaround.
func TestProductionPolicyLocksPermissionFactsUntilCredentialCommit(t *testing.T) {
	oauthserver.ForTestDatabases(t, func(t *testing.T, db, other *gorm.DB) {
		if db.Dialector.Name() != "postgres" {
			return // SQLite's first OAuth write already serializes all writers.
		}
		s := productionPolicyFixture(t, db, true)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		code := productionCode(t, ctx, s)
		var checked atomic.Bool
		require.NoError(t, db.Callback().Create().Before("gorm:create").Register("policy_fact_locks", func(tx *gorm.DB) {
			if tx.Statement.Table != "oauth_server_tokens" || checked.Swap(true) {
				return
			}
			for _, table := range []string{"users", "top_ups"} {
				err := other.WithContext(ctx).Transaction(func(writer *gorm.DB) error {
					if err := writer.Exec("SET LOCAL lock_timeout = '100ms'").Error; err != nil {
						return err
					}
					// No credential or user value comes from an HTTP request here.
					if table == "users" {
						return writer.Exec("UPDATE users SET status = ? WHERE id = ?", common.UserStatusDisabled, 42).Error
					}
					return writer.Exec("UPDATE top_ups SET status = ? WHERE id = ?", "failed", 1).Error
				})
				require.ErrorContains(t, err, "55P03", "permission changes must wait for credential commit: "+table)
			}
		}))
		t.Cleanup(func() { require.NoError(t, db.Callback().Create().Remove("policy_fact_locks")) })
		tokens, err := s.Core.Exchange(ctx, code, oauthserver.SenderBinding{})
		require.NoError(t, err)
		require.True(t, checked.Load())
		require.NoError(t, other.WithContext(ctx).Model(&model.User{}).Where("id = ?", 42).Update("status", common.UserStatusDisabled).Error)
		grant, err := s.Core.ValidateAccess(ctx, oauthserver.AccessRequest{Token: tokens.AccessToken, Resource: s.Resource})
		require.Error(t, err)
		require.Nil(t, grant)
	})
}
