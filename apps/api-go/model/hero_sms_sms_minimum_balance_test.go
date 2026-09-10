package model

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/service/herosms"
	"github.com/stretchr/testify/require"
)

func setupHeroSMSSMSMinimumBalanceProvider(t *testing.T) *atomic.Int32 {
	t.Helper()
	require.NoError(t, UpdateHeroSMSSettings(HeroSMSSettingsUpdate{
		Enabled: ptrBool(true), SMSEnabled: ptrBool(true),
		APIKey: "test-secret-key-12345", PriceMultiplier: "1",
	}))
	var purchases atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if writeHeroSMSTestOffer(w, r, "1", 5) {
			return
		}
		switch r.URL.Query().Get("action") {
		case "getActiveActivations":
			_, _ = w.Write([]byte(`{"data":[]}`))
		case "getNumberV2":
			id := purchases.Add(1)
			_, _ = fmt.Fprintf(w, `{"activationId":%d,"phoneNumber":"79001234567","activationCost":1,"currencyCode":840,"countryCode":6,"canGetAnotherSms":false}`, 990+id)
		default:
			http.Error(w, "unexpected action", http.StatusBadRequest)
		}
	}))
	t.Cleanup(server.Close)
	t.Cleanup(SetHeroSMSClientFactoryForTest(
		func(_ string, _ string) herosms.Client { return herosms.NewClient(server.URL+"/api/v1", "secret") },
		server.URL+"/api/v1",
	))
	return &purchases
}

func TestHeroSMSSMSMinimumBalanceRejectsBeforePurchase(t *testing.T) {
	for _, startingQuota := range []int{0, common.GetTrustQuota() - 1} {
		t.Run(fmt.Sprint(startingQuota), func(t *testing.T) {
			db := setupHeroSMSTestDB(t)
			user := createHeroSMSTestUser(t, db, 840, startingQuota)
			purchases := setupHeroSMSSMSMinimumBalanceProvider(t)
			offer, err := GetHeroSMSSMSOffer(t.Context(), user.Id, 6, "tg", "")
			require.NoError(t, err)

			order, _, _, err := CreateHeroSMSSMSOrder(t.Context(), user.Id, HeroSMSSMSPurchaseRequest{OfferID: offer.ID}, "below-floor")
			var apiErr *HeroSMSError
			require.ErrorAs(t, err, &apiErr)
			require.Equal(t, http.StatusPaymentRequired, apiErr.Status)
			require.Equal(t, "TEMPORARY_SMS_MINIMUM_BALANCE", apiErr.Code)
			require.Equal(t, "Temporary SMS purchases require a balance of at least USD 10", apiErr.Message)
			require.Nil(t, order)
			require.Zero(t, purchases.Load())
			require.Equal(t, startingQuota, getUserQuotaValue(user.Id))
			var orders, ledgers int64
			require.NoError(t, db.Model(&HeroSMSSMSOrder{}).Count(&orders).Error)
			require.NoError(t, db.Model(&HeroSMSSMSQuotaLedger{}).Count(&ledgers).Error)
			require.Zero(t, orders)
			require.Zero(t, ledgers)
		})
	}
}

func TestHeroSMSSMSMinimumBalanceStillRequiresActualCharge(t *testing.T) {
	db := setupHeroSMSTestDB(t)
	user := createHeroSMSTestUser(t, db, 841, common.GetTrustQuota())
	order := HeroSMSSMSOrder{UserID: user.Id, ChargeQuota: user.Quota + 1}
	_, err := reserveHeroSMSSMSQuota(&order)
	var apiErr *HeroSMSError
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, "INSUFFICIENT_BALANCE", apiErr.Code)
	require.Equal(t, user.Quota, getUserQuotaValue(user.Id))
	var orders, ledgers int64
	require.NoError(t, db.Model(&HeroSMSSMSOrder{}).Count(&orders).Error)
	require.NoError(t, db.Model(&HeroSMSSMSQuotaLedger{}).Count(&ledgers).Error)
	require.Zero(t, orders)
	require.Zero(t, ledgers)
}

func TestHeroSMSSMSDistinctConcurrentPurchasesRespectMinimumBalance(t *testing.T) {
	db := setupHeroSMSTestDB(t)
	user := createHeroSMSTestUser(t, db, 842, common.GetTrustQuota())
	purchases := setupHeroSMSSMSMinimumBalanceProvider(t)
	offer, err := GetHeroSMSSMSOffer(t.Context(), user.Id, 6, "tg", "")
	require.NoError(t, err)

	start := make(chan struct{})
	results := make(chan error, 2)
	for _, key := range []string{"distinct-first", "distinct-second"} {
		go func() {
			<-start
			_, _, _, purchaseErr := CreateHeroSMSSMSOrder(t.Context(), user.Id, HeroSMSSMSPurchaseRequest{OfferID: offer.ID}, key)
			results <- purchaseErr
		}()
	}
	close(start)
	first, second := <-results, <-results
	if first == nil {
		first, second = second, first
	}
	require.NoError(t, second)
	var apiErr *HeroSMSError
	require.ErrorAs(t, first, &apiErr)
	require.Equal(t, "TEMPORARY_SMS_MINIMUM_BALANCE", apiErr.Code)
	require.EqualValues(t, 1, purchases.Load())
	require.Equal(t, user.Quota-offer.ChargeQuota, getUserQuotaValue(user.Id))
	var orders, ledgers int64
	require.NoError(t, db.Model(&HeroSMSSMSOrder{}).Count(&orders).Error)
	require.NoError(t, db.Model(&HeroSMSSMSQuotaLedger{}).Count(&ledgers).Error)
	require.EqualValues(t, 1, orders)
	require.EqualValues(t, 1, ledgers)
}
