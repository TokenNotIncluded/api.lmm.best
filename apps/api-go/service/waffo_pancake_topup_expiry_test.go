package service

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	pancake "github.com/waffo-com/waffo-pancake-sdk-go"
	"gorm.io/gorm"
)

func TestWaffoPancakePaymentsByTradeNoRequiresCompleteMatchingProviderRead(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	privateKey := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))

	for _, tc := range []struct {
		name    string
		body    string
		wantLen int
		wantErr bool
	}{
		{name: "unpaid", body: `{"data":{"paymentsCount":0,"payments":[]}}`, wantLen: 0},
		{name: "succeeded", body: `{"data":{"paymentsCount":1,"payments":[{"status":"succeeded","orderMerchantExternalId":"WAFFO-1"}]}}`, wantLen: 1},
		{name: "missing list", body: `{"data":{}}`, wantErr: true},
		{name: "truncated list", body: `{"data":{"paymentsCount":2,"payments":[{"status":"failed","orderMerchantExternalId":"WAFFO-1"}]}}`, wantErr: true},
		{name: "wrong order", body: `{"data":{"paymentsCount":1,"payments":[{"status":"failed","orderMerchantExternalId":"OTHER"}]}}`, wantErr: true},
		{name: "partial error", body: `{"data":{"paymentsCount":0,"payments":[]},"errors":[{"message":"resolver unavailable"}]}`, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "/v1/graphql", r.URL.Path)
				var request struct {
					Query     string         `json:"query"`
					Variables map[string]any `json:"variables"`
				}
				require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
				require.Contains(t, request.Query, "paymentsCount(filter: { orderMerchantExternalId: { eq: $ref } })")
				require.Contains(t, request.Query, "payments(limit: 100, filter: { orderMerchantExternalId: { eq: $ref } })")
				require.Equal(t, "WAFFO-1", request.Variables["ref"])
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			client, err := pancake.New(pancake.Config{
				MerchantID: "MER_AbCdEfGhIjKlMnOpQrStUv",
				PrivateKey: privateKey,
				BaseURL:    server.URL,
				HTTPClient: server.Client(),
			})
			require.NoError(t, err)
			payments, err := waffoPancakePaymentsByTradeNo(context.Background(), client, "WAFFO-1")
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Len(t, payments, tc.wantLen)
		})
	}
}

func setupWaffoPancakeExpiryTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	previousDB := model.DB
	previousType := common.MainDatabaseType()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "waffo-expiry.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&model.TopUp{}))
	model.DB = db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	t.Cleanup(func() {
		model.DB = previousDB
		common.SetMainDatabaseType(previousType)
		_ = sqlDB.Close()
	})
	return db
}

func TestWaffoPancakeTopUpExpiryReconcilesOnlyUnpaidOrders(t *testing.T) {
	db := setupWaffoPancakeExpiryTestDB(t)
	now := time.Unix(1_800_000_000, 0)
	old := now.Add(-2 * time.Hour).Unix()
	orders := []model.TopUp{
		{TradeNo: "unpaid", PaymentProvider: model.PaymentProviderWaffoPancake, Status: common.TopUpStatusPending, CreateTime: old},
		{TradeNo: "payment-failed", PaymentProvider: model.PaymentProviderWaffoPancake, Status: common.TopUpStatusPending, CreateTime: old},
		{TradeNo: "payment-succeeded", PaymentProvider: model.PaymentProviderWaffoPancake, Status: common.TopUpStatusPending, CreateTime: old},
		{TradeNo: "payment-pending", PaymentProvider: model.PaymentProviderWaffoPancake, Status: common.TopUpStatusPending, CreateTime: old},
		{TradeNo: "provider-error", PaymentProvider: model.PaymentProviderWaffoPancake, Status: common.TopUpStatusPending, CreateTime: old},
		{TradeNo: "webhook-wins", PaymentProvider: model.PaymentProviderWaffoPancake, Status: common.TopUpStatusPending, CreateTime: old},
		{TradeNo: "fresh", PaymentProvider: model.PaymentProviderWaffoPancake, Status: common.TopUpStatusPending, CreateTime: now.Add(-30 * time.Minute).Unix()},
		{TradeNo: "other-provider", PaymentProvider: model.PaymentProviderStripe, Status: common.TopUpStatusPending, CreateTime: old},
	}
	for i := range orders {
		require.NoError(t, db.Create(&orders[i]).Error)
	}
	checked := map[string]bool{}
	check := func(_ context.Context, tradeNo string) ([]waffoPancakePayment, error) {
		checked[tradeNo] = true
		switch tradeNo {
		case "unpaid":
			return nil, nil
		case "payment-failed":
			return []waffoPancakePayment{{Status: pancake.PaymentStatusFailed}, {Status: pancake.PaymentStatusCanceled}}, nil
		case "payment-succeeded":
			return []waffoPancakePayment{{Status: pancake.PaymentStatusSucceeded}}, nil
		case "payment-pending":
			return []waffoPancakePayment{{Status: pancake.PaymentStatusPending}}, nil
		case "provider-error":
			return nil, errors.New("provider unavailable")
		case "webhook-wins":
			require.NoError(t, db.Model(&model.TopUp{}).Where("trade_no = ?", tradeNo).Update("status", common.TopUpStatusSuccess).Error)
			return nil, nil
		default:
			t.Fatalf("unexpected provider lookup: %s", tradeNo)
			return nil, nil
		}
	}
	summary, err := reconcileExpiredWaffoPancakeTopUps(context.Background(), now, 30, check)
	require.NoError(t, err)
	require.Equal(t, 6, summary.Checked)
	require.Equal(t, 2, summary.Failed)
	require.Equal(t, 2, summary.Held)
	require.Equal(t, 1, summary.Succeeded)
	require.Equal(t, 1, summary.Errors)
	require.False(t, checked["fresh"])
	require.False(t, checked["other-provider"])

	for _, order := range orders {
		var stored model.TopUp
		require.NoError(t, db.First(&stored, order.Id).Error)
		switch order.TradeNo {
		case "unpaid", "payment-failed":
			require.Equal(t, common.TopUpStatusFailed, stored.Status)
			require.Equal(t, string(model.PaymentOrderFailureCheckoutTimeout), stored.FailureReasonCode)
			require.Equal(t, now.Unix(), stored.CompleteTime)
		case "webhook-wins":
			require.Equal(t, common.TopUpStatusSuccess, stored.Status)
			require.Empty(t, stored.FailureReasonCode)
		default:
			require.Equal(t, common.TopUpStatusPending, stored.Status)
		}
		if order.TradeNo == "payment-succeeded" || order.TradeNo == "payment-pending" || order.TradeNo == "provider-error" {
			require.Equal(t, now.Unix(), stored.PaymentCheckedAt)
		}
	}
	due, err := WaffoPancakeTopUpExpiryDue(context.Background(), now)
	require.NoError(t, err)
	require.False(t, due, "checked unsettled orders should wait before another provider query")
}
