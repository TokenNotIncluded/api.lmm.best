package model

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/service/herosms"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/stretchr/testify/require"
)

func heroSMSSMSExpiringTestQuote(t *testing.T, userID int, expiresAt time.Time) string {
	t.Helper()
	quoteID, err := encodeHeroSMSSMSQuote(heroSMSSMSQuoteToken{
		Version: heroSMSSMSQuoteVersion, UserID: userID, CountryID: 6, Service: "tg",
		CostCNY: "1", Multiplier: "1", CurrencyCode: setting.HeroSMSCurrencyCode,
		IssuedAt: expiresAt.Add(-heroSMSSMSQuoteTTL).Unix(),
	})
	require.NoError(t, err)
	return quoteID
}

func TestHeroSMSSMSExpiredQuoteConfirmsNoOrderWithoutProviderAccess(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(map[bool]string{true: "enabled", false: "disabled"}[enabled], func(t *testing.T) {
			db := setupHeroSMSTestDB(t)
			user := createHeroSMSTestUser(t, db, 9025, common.GetTrustQuota())
			require.NoError(t, UpdateHeroSMSSettings(HeroSMSSettingsUpdate{
				Enabled: ptrBool(enabled), SMSEnabled: ptrBool(enabled), APIKey: "expired-quote-fixture-key", PriceMultiplier: "1",
			}))
			if !enabled {
				require.NoError(t, ClearHeroSMSAPIKey())
			}
			var providerCalls atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				providerCalls.Add(1)
				w.WriteHeader(http.StatusBadRequest)
			}))
			t.Cleanup(provider.Close)
			t.Cleanup(SetHeroSMSClientFactoryForTest(
				func(_ string, _ string) herosms.Client { return herosms.NewClient(provider.URL, "fixture") }, provider.URL,
			))
			request := HeroSMSSMSPurchaseRequest{OfferID: heroSMSSMSExpiringTestQuote(t, user.Id, time.Now().Add(-time.Second))}
			order, _, _, err := CreateHeroSMSSMSOrder(t.Context(), user.Id, request, "expired-quote-not-created")
			require.Nil(t, order)
			var apiErr *HeroSMSError
			require.ErrorAs(t, err, &apiErr)
			require.Equal(t, http.StatusConflict, apiErr.Status)
			require.Equal(t, "PURCHASE_NOT_CREATED", apiErr.Code)
			require.Zero(t, providerCalls.Load())
			require.Equal(t, user.Quota, getUserQuotaValue(user.Id))
			var orders, ledgers int64
			require.NoError(t, db.Model(&HeroSMSSMSOrder{}).Count(&orders).Error)
			require.NoError(t, db.Model(&HeroSMSSMSQuotaLedger{}).Count(&ledgers).Error)
			require.Zero(t, orders)
			require.Zero(t, ledgers)
		})
	}
}

func TestHeroSMSSMSExpiredQuoteDatabaseFailureCannotConfirmNoOrder(t *testing.T) {
	db := setupHeroSMSTestDB(t)
	user := createHeroSMSTestUser(t, db, 9029, common.GetTrustQuota())
	request := HeroSMSSMSPurchaseRequest{OfferID: heroSMSSMSExpiringTestQuote(t, user.Id, time.Now().Add(-time.Second))}
	// Break the second lookup, after the initial idempotency miss, to exercise
	// the actual resolution transaction rather than the early replay shortcut.
	heroSMSSMSIdempotencyMissHook = func() {
		require.NoError(t, db.Migrator().DropTable(&HeroSMSSMSOrder{}))
	}
	t.Cleanup(func() { heroSMSSMSIdempotencyMissHook = nil })
	order, _, _, err := CreateHeroSMSSMSOrder(t.Context(), user.Id, request, "expired-database-failure")
	require.Nil(t, order)
	require.Error(t, err)
	var apiErr *HeroSMSError
	require.False(t, errors.As(err, &apiErr), "database failure must not become an absence proof")
	require.Equal(t, user.Quota, getUserQuotaValue(user.Id))
	var ledgers int64
	require.NoError(t, db.Model(&HeroSMSSMSQuotaLedger{}).Count(&ledgers).Error)
	require.Zero(t, ledgers)
}

func TestHeroSMSSMSExpiredInvalidQuoteCannotConfirmNoOrder(t *testing.T) {
	setupHeroSMSTestDB(t)
	require.NoError(t, UpdateHeroSMSSettings(HeroSMSSettingsUpdate{
		Enabled: ptrBool(true), SMSEnabled: ptrBool(true), APIKey: "expired-quote-fixture-key", PriceMultiplier: "1",
	}))
	for _, invalid := range []struct {
		name   string
		change func(*heroSMSSMSQuoteToken)
	}{
		{name: "other_user", change: func(q *heroSMSSMSQuoteToken) { q.UserID++ }},
		{name: "wrong_version", change: func(q *heroSMSSMSQuoteToken) { q.Version++ }},
		{name: "wrong_currency", change: func(q *heroSMSSMSQuoteToken) { q.CurrencyCode++ }},
	} {
		t.Run(invalid.name, func(t *testing.T) {
			quote := heroSMSSMSQuoteToken{
				Version: heroSMSSMSQuoteVersion, UserID: 9026, CountryID: 6, Service: "tg",
				CostCNY: "1", Multiplier: "1", CurrencyCode: setting.HeroSMSCurrencyCode,
				IssuedAt: time.Now().Add(-heroSMSSMSQuoteTTL - time.Second).Unix(),
			}
			invalid.change(&quote)
			quoteID, err := encodeHeroSMSSMSQuote(quote)
			require.NoError(t, err)
			_, _, _, err = CreateHeroSMSSMSOrder(t.Context(), 9026, HeroSMSSMSPurchaseRequest{OfferID: quoteID}, "invalid-"+invalid.name)
			var apiErr *HeroSMSError
			require.ErrorAs(t, err, &apiErr)
			require.Equal(t, "PRICE_CHANGED", apiErr.Code)
		})
	}
	t.Run("tampered", func(t *testing.T) {
		quoteID := heroSMSSMSExpiringTestQuote(t, 9026, time.Now().Add(-time.Second))
		_, _, _, err := CreateHeroSMSSMSOrder(t.Context(), 9026, HeroSMSSMSPurchaseRequest{OfferID: quoteID[:len(quoteID)-8] + "AAAAAAAA"}, "invalid-tampered")
		var apiErr *HeroSMSError
		require.ErrorAs(t, err, &apiErr)
		require.Equal(t, "PRICE_CHANGED", apiErr.Code)
	})
}
