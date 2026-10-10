package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/stretchr/testify/require"
	pancake "github.com/waffo-com/waffo-pancake-sdk-go"
)

func merchantStoreOrderRefundFixture(t *testing.T, d *model.MerchantStoreRefundDispatch, refunds []map[string]any) merchantStorePancakeOrderRefundData {
	t.Helper()
	links := []map[string]any{}
	for _, r := range refunds {
		links = append(links, map[string]any{"id": r["id"]})
	}
	body, err := json.Marshal(map[string]any{
		"onetimeOrder": map[string]any{"id": d.Basis.ReceiptReference, "currency": d.Basis.Currency, "testMode": false,
			"payments": []map[string]any{{"id": d.Basis.PaymentReference, "status": "succeeded", "refunds": links}}},
		"refunds": refunds, "refundsCount": len(refunds),
	})
	require.NoError(t, err)
	var data merchantStorePancakeOrderRefundData
	require.NoError(t, json.Unmarshal(body, &data))
	return data
}
func merchantStoreOrderRefundExecution(d *model.MerchantStoreRefundDispatch, id, ref, amount string) map[string]any {
	return map[string]any{"id": id, "status": "succeeded", "orderMerchantExternalId": d.Order.TradeNo,
		"refundTicketMerchantExternalId": ref, "pspAmountDetails": map[string]string{"amount": amount, "currency": d.Basis.Currency}}
}

func TestMerchantStoreRefundSyncQueryValidatesNativeOrderAndActualExecutions(t *testing.T) {
	o, request, frozen, _ := merchantStoreRefundProviderFixture(t)
	d := &model.MerchantStoreRefundDispatch{Order: *o, Basis: model.MerchantStoreRefundPaymentBasis{
		ReceiptReference: request.Payment.ReceiptReference, PaymentReference: request.Payment.PaymentReference,
		AmountMinor: request.Payment.AmountMinor, Currency: request.Payment.Currency, EvidenceHash: request.Payment.EvidenceHash}}
	fixture := func() merchantStorePancakeOrderRefundData {
		return merchantStoreOrderRefundFixture(t, d, []map[string]any{merchantStoreOrderRefundExecution(d, "psp-external-1", "dashboard-ticket", "0.25")})
	}
	data := fixture()
	results, err := merchantStoreVerifyOrderRefunds(d, frozen, data)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.EqualValues(t, 25, results[0].evidence.AmountMinor)
	require.Equal(t, "psp-external-1", results[0].evidence.RefundReference)
	require.NotEqual(t, "dashboard-ticket", results[0].evidence.RefundReference)
	again, err := merchantStoreVerifyOrderRefunds(d, frozen, fixture())
	require.NoError(t, err)
	require.Equal(t, results, again)
	for name, change := range map[string]func(*merchantStorePancakeOrderRefundData){
		"wrong_order":          func(x *merchantStorePancakeOrderRefundData) { x.OnetimeOrder.ID = "ORD_other" },
		"wrong_currency":       func(x *merchantStorePancakeOrderRefundData) { x.OnetimeOrder.Currency = "CNY" },
		"wrong_environment":    func(x *merchantStorePancakeOrderRefundData) { *x.OnetimeOrder.TestMode = true },
		"missing_environment":  func(x *merchantStorePancakeOrderRefundData) { x.OnetimeOrder.TestMode = nil },
		"missing_count":        func(x *merchantStorePancakeOrderRefundData) { x.RefundsCount = nil },
		"truncated_list":       func(x *merchantStorePancakeOrderRefundData) { *x.RefundsCount = 2 },
		"missing_payment_link": func(x *merchantStorePancakeOrderRefundData) { x.OnetimeOrder.Payments[0].Refunds = nil },
		"duplicate_payment": func(x *merchantStorePancakeOrderRefundData) {
			x.OnetimeOrder.Payments = append(x.OnetimeOrder.Payments, x.OnetimeOrder.Payments[0])
		},
		"wrong_business_order":  func(x *merchantStorePancakeOrderRefundData) { x.Refunds[0].OrderMerchantExternalID = "another-order" },
		"wrong_actual_currency": func(x *merchantStorePancakeOrderRefundData) { x.Refunds[0].PSPAmountDetails.Currency = "CNY" },
		"zero_actual_amount":    func(x *merchantStorePancakeOrderRefundData) { x.Refunds[0].PSPAmountDetails.Amount = "0.00" },
		"precision":             func(x *merchantStorePancakeOrderRefundData) { x.Refunds[0].PSPAmountDetails.Amount = "0.001" },
		"over_charge":           func(x *merchantStorePancakeOrderRefundData) { x.Refunds[0].PSPAmountDetails.Amount = "1.11" },
	} {
		t.Run(name, func(t *testing.T) {
			x := fixture()
			change(&x)
			_, err := merchantStoreVerifyOrderRefunds(d, frozen, x)
			require.Error(t, err)
		})
	}
	data.Refunds[0].Status = "pending"
	results, err = merchantStoreVerifyOrderRefunds(d, frozen, data)
	require.NoError(t, err)
	require.Empty(t, results, "a requested refund is not actual money returned")
	data.Refunds[0].Status = "failed"
	results, err = merchantStoreVerifyOrderRefunds(d, frozen, data)
	require.NoError(t, err)
	require.Empty(t, results, "this read does not release reservations on failure")
	data = merchantStoreOrderRefundFixture(t, d, []map[string]any{merchantStoreOrderRefundExecution(d, "psp-1", "", "0.60"), merchantStoreOrderRefundExecution(d, "psp-2", "", "0.60")})
	_, err = merchantStoreVerifyOrderRefunds(d, frozen, data)
	require.Error(t, err, "combined actual amounts cannot exceed original charge")
}

func TestMerchantStoreRefundSyncBothDirectionsAndRepeatsThroughSDK(t *testing.T) {
	for _, method := range []string{MerchantStorePlatformPancake, MerchantStoreExternalPancake} {
		t.Run(method, func(t *testing.T) {
			f, o, local, config := merchantStoreRefundBrokerFixture(t, method)
			require.NoError(t, model.DB.Model(&model.Option{}).Where("key = ?", model.MerchantStoreWriterCapabilityOption).Update("value", "8").Error)
			d, rows, err := model.GetMerchantStoreRefundSyncSnapshot(o.ID)
			require.NoError(t, err)
			sellerBefore, buyerBefore := merchantStoreRefundTestQuota(t, f.seller.Id), merchantStoreRefundTestQuota(t, f.buyer.Id)
			// A local partial refund and a dashboard partial refund sum to the original
			// tax-inclusive charge. The provider reused the ticket reference but
			// execution IDs differ. There is no outward refund POST during sync.
			data := merchantStoreOrderRefundFixture(t, d, []map[string]any{
				merchantStoreOrderRefundExecution(d, "provider-refund-execution-1", local.ID, "0.25"),
				merchantStoreOrderRefundExecution(d, "psp-dashboard-2", local.ID, "0.85"),
			})
			calls := 0
			client, err := pancake.New(pancake.Config{MerchantID: config.MerchantID, PrivateKey: config.PrivateKey, Environment: pancake.Environment(config.Environment), HTTPClient: &http.Client{Transport: merchantStoreTestTransport(func(req *http.Request) (*http.Response, error) {
				calls++
				require.Equal(t, "/v1/graphql", req.URL.Path)
				body, err := io.ReadAll(req.Body)
				require.NoError(t, err)
				require.NotContains(t, string(body), "mutation")
				var result any = data
				if !strings.Contains(string(body), "onetimeOrder(id:") {
					native := merchantStoreDispatchNative(d)
					native.RefundID, native.AmountMinor, native.Currency = local.ID, local.AmountMinor, local.Currency
					known := merchantStoreRefundProviderQueryFixture(native)
					known.Refunds[0].OrderMerchantExternalID = o.TradeNo
					result = known
				}
				encoded, err := json.Marshal(map[string]any{"data": result})
				require.NoError(t, err)
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(string(encoded)))}, nil
			})}})
			require.NoError(t, err)
			for range 3 {
				d, rows, err = model.GetMerchantStoreRefundSyncSnapshot(o.ID)
				require.NoError(t, err)
				proofs, err := merchantStoreQueryOrderRefunds(context.Background(), d, client)
				require.NoError(t, err)
				require.NoError(t, merchantStoreApplySyncedRefunds(context.Background(), d, rows, proofs, client))
			}
			view, err := model.GetMerchantStoreRefunds(f.buyer.Id, o.ID)
			require.NoError(t, err)
			stored, err := model.GetMerchantStorePaymentOrder(o.ID)
			require.NoError(t, err)
			require.Equal(t, "refunded", stored.Status)
			require.Len(t, view.Refunds, 2)
			require.EqualValues(t, 110, *view.RefundedAmountMinor)
			require.Equal(t, buyerBefore, merchantStoreRefundTestQuota(t, f.buyer.Id), "gateway refund is not a platform balance credit")
			wantSeller := sellerBefore
			if method == MerchantStorePlatformPancake {
				wantSeller -= o.PriceQuota
			}
			require.Equal(t, wantSeller, merchantStoreRefundTestQuota(t, f.seller.Id))
			require.Equal(t, 4, calls, "three order reads plus one original local ticket verification")
			require.ErrorIs(t, model.AuthorizeMerchantStoreRefundSync(f.buyer.Id, o.ID), model.ErrMerchantStoreDenied)
			require.NoError(t, model.AuthorizeMerchantStoreRefundSync(f.seller.Id, o.ID))
			require.NoError(t, model.AuthorizeMerchantStoreRefundSync(f.root.Id, o.ID))
		})
	}
}

func TestMerchantStoreRefundSyncExternalNotificationChecksSignatureAndFrozenScopeBeforeQuery(t *testing.T) {
	_, o, _, _ := merchantStoreRefundBrokerFixture(t, MerchantStorePlatformPancake)
	require.NoError(t, model.DB.Model(&model.Option{}).Where("key = ?", model.MerchantStoreWriterCapabilityOption).Update("value", "8").Error)
	key, _ := merchantStorePancakeTestKey(t)
	d, _, err := model.GetMerchantStoreRefundSyncSnapshot(o.ID)
	require.NoError(t, err)
	frozen, err := loadMerchantStorePaymentContext(o)
	require.NoError(t, err)
	request := merchantStoreDispatchNative(d)
	request.RefundID = "external-ticket-not-created-here"
	calls := 0
	query := func(_ context.Context, id string) error { calls++; require.Equal(t, o.ID, id); return nil }
	payload := merchantStoreRefundProviderWebhook(t, o, request, frozen, "refund.succeeded", nil)
	require.NoError(t, merchantStoreExternalRefundNotification(context.Background(), o, payload, merchantStorePancakeSigned(t, key, payload), query))
	require.Equal(t, 1, calls)
	require.Error(t, merchantStoreExternalRefundNotification(context.Background(), o, payload, "bad-signature", query))
	for _, change := range []func(map[string]any){
		func(x map[string]any) { x["paymentId"] = "PAY_ZbCdEfGhIjKlMnOpQrStUv" },
		func(x map[string]any) { x["originalChargedAmount"] = "999.00" },
		func(x map[string]any) { x["currency"] = "CNY" },
		func(x map[string]any) { x["merchantProvidedBuyerIdentity"] = "another-buyer" },
		func(x map[string]any) { x["orderId"] = "ORD_ZbCdEfGhIjKlMnOpQrStUv" },
	} {
		bad := merchantStoreRefundProviderWebhook(t, o, request, frozen, "refund.succeeded", change)
		require.Error(t, merchantStoreExternalRefundNotification(context.Background(), o, bad, merchantStorePancakeSigned(t, key, bad), query))
	}
	require.Equal(t, 1, calls, "untrusted events cannot trigger a privileged provider query")
	var receipts int64
	require.NoError(t, model.DB.Model(&model.MerchantStoreRefund{}).Where("order_id = ? AND requested_role = ?", o.ID, "provider").Count(&receipts).Error)
	require.Zero(t, receipts, "a verified notification alone does not create returned-money evidence")
}
