package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/oauthserver"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func installIdentityCurrencyFixture(t *testing.T) {
	t.Helper()
	oldK, oldErr := common.CreditsPerUSD()
	oldQ, _ := common.LegacyPricingQuotaPerUnit()
	// A caller may already have installed a precise payment-site anchor. Keep
	// that fixture; only initialize otherwise-unconfigured identity test sites.
	if oldErr != nil {
		require.NoError(t, common.SetCreditCurrencyBasis(decimal.NewFromFloat(common.QuotaPerUnit).Mul(decimal.NewFromInt(7)), decimal.NewFromFloat(common.QuotaPerUnit)))
	}
	t.Cleanup(func() {
		if oldErr != nil {
			common.ClearCreditsPerUSD()
		} else {
			require.NoError(t, common.SetCreditCurrencyBasis(oldK, oldQ))
		}
	})
}

func TestOAuthBalanceHTTPRejectsMissingBasisWithoutChangingAuthorization(t *testing.T) {
	db := setupUserOnboardingTestDB(t)
	require.NoError(t, model.MigrateOAuthServer(db))
	user := model.User{Username: "oauth-currency", AffCode: "oauth-currency", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", Quota: 3500000, ConsoleActivatedAt: 1}
	require.NoError(t, db.Create(&user).Error)
	integration, err := service.NewOAuthIntegration(db, service.OAuthServerConfig{Enabled: true, Issuer: "https://currency.example.test", Groups: []string{"default"}})
	require.NoError(t, err)
	ctx := context.Background()
	const browser = "synthetic-browser-binding-at-least-32-bytes"
	const verifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	query := url.Values{"response_type": {"code"}, "client_id": {service.OAuthCLIClientID}, "redirect_uri": {service.OAuthNativeRedirect}, "resource": {integration.Resource}, "scope": {service.OAuthBalanceScope + " " + service.OAuthGroupScope("default")}, "state": {strings.Repeat("s", 32)}, "code_challenge": {"E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"}, "code_challenge_method": {"S256"}}
	pending, err := integration.Core.BeginAuthorization(ctx, query.Encode(), browser)
	require.NoError(t, err)
	consent, err := integration.Core.TrustedPrepareConsent(ctx, pending.Transaction, browser, int64(user.Id))
	require.NoError(t, err)
	approved, err := integration.Core.TrustedApprove(ctx, pending.Transaction, browser, consent.Secret)
	require.NoError(t, err)
	redirect, err := url.Parse(approved.RedirectURI)
	require.NoError(t, err)
	tokens, err := integration.Core.Exchange(ctx, url.Values{"grant_type": {"authorization_code"}, "client_id": {service.OAuthCLIClientID}, "redirect_uri": {service.OAuthNativeRedirect}, "resource": {integration.Resource}, "code": {redirect.Query().Get("code")}, "code_verifier": {verifier}}.Encode(), oauthserver.SenderBinding{})
	require.NoError(t, err)
	require.NoError(t, common.SetCreditCurrencyBasis(decimal.NewFromInt(3500000), decimal.NewFromInt(500000)))
	handler := NewOAuthHTTP(integration)
	request := func(token string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/oauth2/balance", nil)
		c.Request.Header.Set("Authorization", "Bearer "+token)
		handler.Balance(c)
		return w
	}
	w := request(tokens.AccessToken)
	require.Equal(t, http.StatusOK, w.Code)
	var data map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &data))
	require.Equal(t, "USD", data["currency"])
	require.EqualValues(t, 1, data["balance"])
	require.EqualValues(t, 3500000, data["quota"])
	common.ClearCreditsPerUSD()
	w = request(tokens.AccessToken)
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	require.JSONEq(t, `{"error":"temporarily_unavailable"}`, w.Body.String())
	require.Equal(t, http.StatusUnauthorized, request("invalid-synthetic-token").Code)
	require.NoError(t, db.Model(&user).Update("status", common.UserStatusDisabled).Error)
	require.Equal(t, http.StatusUnauthorized, request(tokens.AccessToken).Code)
}

func TestOAuthBalanceUsesFixedUSDAnchorAndRawOneCredit(t *testing.T) {
	installIdentityCurrencyFixture(t)
	require.NoError(t, common.SetCreditCurrencyBasis(decimal.NewFromInt(3500000), decimal.NewFromInt(500000)))
	oldQ, oldFX, oldB := common.QuotaPerUnit, operation_setting.USDExchangeRate, operation_setting.TopUpPlatformUnitsPerCNY
	t.Cleanup(func() {
		common.QuotaPerUnit, operation_setting.USDExchangeRate, operation_setting.TopUpPlatformUnitsPerCNY = oldQ, oldFX, oldB
	})
	for _, liveQ := range []float64{500000, 3000000} {
		common.QuotaPerUnit, operation_setting.USDExchangeRate, operation_setting.TopUpPlatformUnitsPerCNY = liveQ, 9.9, 2.4
		for _, raw := range []int{0, 1, 3500000} {
			data, err := oauthBalancePayload(raw)
			require.NoError(t, err)
			require.Equal(t, 2, data["schema_version"])
			require.Equal(t, "USD", data["currency"])
			require.Equal(t, "CREDIT", data["quota_unit"])
			require.Equal(t, raw, data["quota"])
			require.Equal(t, 1, data["credit_unit"])
			require.Equal(t, "3500000", data["credits_per_usd"])
			require.Equal(t, 3500000.0, data["quota_per_unit"])
			require.InDelta(t, float64(raw)/3500000, data["balance"], 1e-16)
			require.Nil(t, data["authorization_limit"])
		}
	}
	common.ClearCreditsPerUSD()
	for _, raw := range []int{0, 1, 3500000} {
		data, err := oauthBalancePayload(raw)
		require.ErrorIs(t, err, common.ErrCreditUnitsUnavailable)
		require.Nil(t, data)
	}
}

func TestWalletMCPBalanceKeepsRawCreditsAndLegacyPrefillBridge(t *testing.T) {
	db, user, _ := setupWalletMCPTest(t)
	require.NoError(t, common.SetCreditCurrencyBasis(decimal.NewFromInt(3500000), decimal.NewFromInt(500000)))
	oldQ := common.QuotaPerUnit
	t.Cleanup(func() { common.QuotaPerUnit = oldQ })
	common.QuotaPerUnit = 3000000
	session := walletMCPTestSession(t, user.Id, walletMCPTestExtra("a"))
	for _, raw := range []int{1, 1000} {
		require.NoError(t, db.Model(&user).Update("quota", raw).Error)
		data := walletMCPData(t, walletMCPCall(t, session, "wallet.balance", map[string]any{}, ""))
		require.EqualValues(t, raw, data["available_quota"])
		require.EqualValues(t, raw, data["available_credits"])
		require.EqualValues(t, 1, data["credit_unit"])
		require.EqualValues(t, 1, data["quota_per_platform_credit"])
		require.Equal(t, "CREDIT", data["currency_unit"])
		require.Equal(t, "USD", data["currency"])
		require.Equal(t, "3500000", data["credits_per_usd"])
		require.InDelta(t, float64(raw)/3500000, data["available_usd"], 1e-16)
	}
	link := walletMCPData(t, walletMCPCall(t, session, "wallet.topup_link", map[string]any{"amount": 25}, ""))
	require.Equal(t, "https://console.example.test/wallet?topup_amount=25", link["url"])
	require.Equal(t, "LEGACY", link["amount_unit"])
	require.EqualValues(t, 25, link["legacy_batch_amount"])
	require.EqualValues(t, 25, link["platform_credit_amount"])
	common.ClearCreditsPerUSD()
	require.True(t, walletMCPCall(t, session, "wallet.balance", map[string]any{}, "").IsError)
	var stored int
	require.NoError(t, db.Model(&user).Select("quota").Scan(&stored).Error)
	require.Equal(t, 1000, stored)
}
