package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/stretchr/testify/require"
	pancake "github.com/waffo-com/waffo-pancake-sdk-go"
)

func merchantStoreRefundBrokerFixture(t *testing.T, method string) (merchantStoreServiceFixture, *model.MerchantStoreOrder, *model.MerchantStoreRefund, merchantStoreGatewayConfig) {
	f := merchantStoreServiceDB(t, method)
	_, private := merchantStorePancakeTestKey(t)
	config := merchantStoreGatewayConfig{MerchantID: "MER_AbCdEfGhIjKlMnOpQrStUv", PrivateKey: private, StoreID: "STO_AbCdEfGhIjKlMnOpQrStUv", ProductID: "PROD_AbCdEfGhIjKlMnOpQrStUv", Environment: "prod", Currency: "USD"}
	if method == MerchantStorePlatformLinuxDO {
		config = merchantStoreGatewayConfig{GatewayURL: "https://credit.linux.do/epay", PartnerID: "123", Key: "PRIVATE-LDC-KEY", PaymentType: "epay", Currency: "LDC", UnitsPerUSD: "1"}
	}
	currency := config.Currency
	o := merchantStoreTestOrder(t, f, method, config, 100, currency)
	receipt := "ORD_AbCdEfGhIjKlMnOpQrStUv"
	pay := "PAY_AbCdEfGhIjKlMnOpQrStUv"
	if method == MerchantStorePlatformLinuxDO {
		receipt = "ldc-original-payment"
		pay = receipt
	}
	require.NoError(t, model.CompleteMerchantStorePayment(o.ID, receipt))
	var e error
	o, e = model.GetMerchantStorePaymentOrder(o.ID)
	require.NoError(t, e)
	require.NoError(t, model.DB.Model(&model.Option{}).Where("key = ?", model.MerchantStoreWriterCapabilityOption).Update("value", "4").Error)
	amount := int64(110)
	if method == MerchantStorePlatformLinuxDO {
		amount = 100
	}
	require.NoError(t, model.RecordMerchantStoreRefundPaymentBasis(o.ID, model.VerifiedMerchantStoreRefundPaymentBasis{ReceiptReference: receipt, PaymentReference: pay, AmountMinor: amount, Currency: currency, EvidenceHash: strings.Repeat("a", 64)}))
	in := model.MerchantStoreRefundInput{RequestKey: "broker", Reason: "Broken item", Mode: "amount", AmountMinor: 25}
	if method == MerchantStorePlatformLinuxDO {
		in.Mode = "full"
		in.AmountMinor = 0
	}
	r, e := model.RequestMerchantStoreRefund(f.buyer.Id, o.ID, in)
	require.NoError(t, e)
	r, e = model.DecideMerchantStoreRefund(f.seller.Id, o.ID, r.ID, model.MerchantStoreRefundDecision{Decision: "approve"})
	require.NoError(t, e)
	o, e = model.GetMerchantStorePaymentOrder(o.ID)
	require.NoError(t, e)
	return f, o, r, config
}

func merchantStoreRefundTestDue(t *testing.T, id string) {
	t.Helper()
	require.NoError(t, model.DB.Model(&model.MerchantStoreRefundProviderAttempt{}).Where("refund_id = ?", id).Updates(map[string]any{"next_check_at": 0, "lease_until": 0}).Error)
}
func merchantStoreRefundTestQuota(t *testing.T, id int) int {
	t.Helper()
	var u model.User
	require.NoError(t, model.DB.First(&u, id).Error)
	return u.Quota
}

func merchantStoreRefundFakeCustomer(t *testing.T, config merchantStoreGatewayConfig, fn func(*http.Request) (*http.Response, error)) *pancake.CustomerSession {
	t.Helper()
	client, e := pancake.New(pancake.Config{MerchantID: config.MerchantID, PrivateKey: config.PrivateKey, Environment: pancake.Environment(config.Environment), HTTPClient: &http.Client{Transport: merchantStoreRefundCustomerTransport{base: merchantStoreTestTransport(fn), token: "ORDER-BOUND-TOKEN", environment: config.Environment}}})
	require.NoError(t, e)
	return client.Customer("ORDER-BOUND-TOKEN")
}

func TestMerchantStoreRefundBrokerTicketTimeoutNeverRepostsAndQueryCompletes(t *testing.T) {
	for _, method := range []string{MerchantStorePlatformPancake, MerchantStoreExternalPancake} {
		t.Run(method, func(t *testing.T) {
			f, o, r, config := merchantStoreRefundBrokerFixture(t, method)
			sellerBefore := merchantStoreRefundTestQuota(t, f.seller.Id)
			buyerBefore := merchantStoreRefundTestQuota(t, f.buyer.Id)
			rootBefore := merchantStoreRefundTestQuota(t, f.root.Id)
			posts := 0
			observed := false
			broker := merchantStoreRefundBroker{query: func(_ context.Context, order *model.MerchantStoreOrder, req MerchantStoreRefundNativeRequest) (MerchantStoreRefundProviderResult, error) {
				if !observed {
					return MerchantStoreRefundProviderResult{State: MerchantStoreRefundProviderUnknown, Code: "refund_not_found"}, nil
				}
				data := merchantStoreRefundProviderQueryFixture(req)
				data.Refunds[0].OrderMerchantExternalID = o.TradeNo
				data.Refunds[0].PSPAmountDetails.Amount = merchantStoreMinorMoney(r.AmountMinor)
				data.RefundTickets[0].RequestedAmountDetails.Amount = merchantStoreMinorMoney(r.AmountMinor)
				return merchantStorePancakeRefundExecutionResult(order, req, data)
			}, customer: func(_ context.Context, d *model.MerchantStoreRefundDispatch) (*pancake.CustomerSession, error) {
				return merchantStoreRefundFakeCustomer(t, config, func(req *http.Request) (*http.Response, error) {
					posts++
					var a model.MerchantStoreRefundProviderAttempt
					require.NoError(t, model.DB.First(&a, "refund_id = ?", r.ID).Error)
					require.Equal(t, 1, a.SubmitCount)
					require.Equal(t, "unknown", a.State, "DB must commit before POST")
					body, e := io.ReadAll(req.Body)
					require.NoError(t, e)
					require.Contains(t, string(body), r.ID)
					require.Contains(t, string(body), "PAY_")
					require.NotContains(t, string(body), "ORD_")
					require.NotContains(t, string(body), f.buyer.Email)
					return nil, errors.New("connection lost after provider accepted")
				}), nil
			}}
			require.NoError(t, broker.process(context.Background(), r.ID))
			require.Equal(t, 1, posts)
			merchantStoreRefundTestDue(t, r.ID)
			require.NoError(t, broker.process(context.Background(), r.ID))
			require.Equal(t, 1, posts, "empty query after timeout is not permission to rePOST")
			observed = true
			merchantStoreRefundTestDue(t, r.ID)
			require.NoError(t, broker.process(context.Background(), r.ID))
			var done model.MerchantStoreRefund
			require.NoError(t, model.DB.First(&done, "id = ?", r.ID).Error)
			require.Equal(t, "completed", done.Status)
			require.Equal(t, 1, posts)
			want := sellerBefore
			if method == MerchantStorePlatformPancake {
				want -= done.PrincipalQuota
			}
			require.Equal(t, want, merchantStoreRefundTestQuota(t, f.seller.Id))
			require.Equal(t, buyerBefore, merchantStoreRefundTestQuota(t, f.buyer.Id))
			require.Equal(t, rootBefore, merchantStoreRefundTestQuota(t, f.root.Id), "fee retained")
			merchantStoreRefundTestDue(t, r.ID)
			require.NoError(t, broker.process(context.Background(), r.ID))
			require.Equal(t, want, merchantStoreRefundTestQuota(t, f.seller.Id))
			require.Equal(t, 1, posts)
		})
	}
}

func TestMerchantStoreRefundBrokerCrashAfterFenceAndFailedProofReleaseOnce(t *testing.T) {
	f, o, r, _ := merchantStoreRefundBrokerFixture(t, MerchantStoreExternalPancake)
	d, e := model.PrepareMerchantStoreRefundDispatch(r.ID, time.Now().Unix())
	require.NoError(t, e)
	claimed, e := model.ClaimMerchantStoreRefundSubmit(r.ID, d.Attempt.LeaseToken, d.Attempt.RequestHash, time.Now().Unix())
	require.NoError(t, e)
	require.True(t, claimed)
	merchantStoreRefundTestDue(t, r.ID)
	posts := 0
	broker := merchantStoreRefundBroker{customer: func(context.Context, *model.MerchantStoreRefundDispatch) (*pancake.CustomerSession, error) {
		posts++
		return nil, nil
	}, query: func(_ context.Context, order *model.MerchantStoreOrder, req MerchantStoreRefundNativeRequest) (MerchantStoreRefundProviderResult, error) {
		data := merchantStoreRefundProviderQueryFixture(req)
		data.Refunds[0].OrderMerchantExternalID = o.TradeNo
		data.Refunds[0].Status = "failed"
		data.Refunds[0].PSPAmountDetails.Amount = "0.00"
		data.RefundTickets[0].Status = "failed"
		return merchantStorePancakeRefundExecutionResult(order, req, data)
	}}
	require.NoError(t, broker.process(context.Background(), r.ID))
	require.Zero(t, posts)
	view, e := model.GetMerchantStoreRefunds(f.buyer.Id, o.ID)
	require.NoError(t, e)
	require.Zero(t, view.ReservedQuota)
	require.Equal(t, o.PriceQuota, view.RemainingQuota)
	merchantStoreRefundTestDue(t, r.ID)
	require.NoError(t, broker.process(context.Background(), r.ID))
	view, e = model.GetMerchantStoreRefunds(f.buyer.Id, o.ID)
	require.NoError(t, e)
	require.Zero(t, view.ReservedQuota)
	require.Zero(t, posts)
}

func TestMerchantStoreRefundBrokerSucceededProofSurvivesSellerBalanceReconcile(t *testing.T) {
	f, o, r, _ := merchantStoreRefundBrokerFixture(t, MerchantStorePlatformPancake)
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", f.seller.Id).Update("quota", 0).Error)
	queries := 0
	broker := merchantStoreRefundBroker{query: func(_ context.Context, order *model.MerchantStoreOrder, req MerchantStoreRefundNativeRequest) (MerchantStoreRefundProviderResult, error) {
		queries++
		data := merchantStoreRefundProviderQueryFixture(req)
		data.Refunds[0].OrderMerchantExternalID = o.TradeNo
		return merchantStorePancakeRefundExecutionResult(order, req, data)
	}}
	require.ErrorIs(t, broker.process(context.Background(), r.ID), model.ErrMerchantStoreBalance)
	var proof model.MerchantStoreRefund
	require.NoError(t, model.DB.First(&proof, "id = ?", r.ID).Error)
	require.Equal(t, "reconciliation_required", proof.Status)
	require.NotNil(t, proof.ProviderRefundReference)
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", f.seller.Id).Update("quota", r.PrincipalQuota).Error)
	merchantStoreRefundTestDue(t, r.ID)
	require.NoError(t, broker.process(context.Background(), r.ID))
	require.Equal(t, 1, queries, "local reconcile must reuse proof without querying or POSTing")
	require.Equal(t, 0, merchantStoreRefundTestQuota(t, f.seller.Id))
	require.NoError(t, model.DB.First(&proof, "id = ?", r.ID).Error)
	require.Equal(t, "completed", proof.Status)
}

func TestMerchantStoreRefundBrokerLinuxDOFullAckAndUnknownRemainSingleFlight(t *testing.T) {
	for _, success := range []bool{true, false} {
		t.Run(map[bool]string{true: "success", false: "timeout"}[success], func(t *testing.T) {
			f, o, r, _ := merchantStoreRefundBrokerFixture(t, MerchantStorePlatformLinuxDO)
			before := merchantStoreRefundTestQuota(t, f.seller.Id)
			posts := 0
			client := &http.Client{Transport: merchantStoreLinuxDORefundTransport{base: merchantStoreTestTransport(func(req *http.Request) (*http.Response, error) {
				posts++
				var a model.MerchantStoreRefundProviderAttempt
				require.NoError(t, model.DB.First(&a, "refund_id = ?", r.ID).Error)
				require.Equal(t, 1, a.SubmitCount)
				body, e := io.ReadAll(req.Body)
				require.NoError(t, e)
				var params map[string]string
				require.NoError(t, json.Unmarshal(body, &params))
				require.Equal(t, o.ProviderTradeID, params["trade_no"])
				require.Equal(t, "1.00", params["money"])
				require.Equal(t, "PRIVATE-LDC-KEY", params["key"])
				if !success {
					return nil, errors.New("ambiguous timeout")
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"code":1,"msg":"退款成功"}`)), Header: make(http.Header)}, nil
			})}}
			broker := merchantStoreRefundBroker{linuxdo: func(ctx context.Context, order *model.MerchantStoreOrder, request MerchantStoreRefundNativeRequest) (MerchantStoreRefundProviderResult, error) {
				return merchantStoreSubmitLinuxDORefundWithClient(ctx, order, request, client)
			}}
			require.NoError(t, broker.process(context.Background(), r.ID))
			merchantStoreRefundTestDue(t, r.ID)
			require.NoError(t, broker.process(context.Background(), r.ID))
			require.Equal(t, 1, posts)
			var row model.MerchantStoreRefund
			require.NoError(t, model.DB.First(&row, "id = ?", r.ID).Error)
			if success {
				require.Equal(t, "completed", row.Status)
				require.Equal(t, before-r.PrincipalQuota, merchantStoreRefundTestQuota(t, f.seller.Id))
			} else {
				require.Equal(t, "awaiting_provider", row.Status)
				require.Equal(t, before, merchantStoreRefundTestQuota(t, f.seller.Id))
			}
		})
	}
}

func TestMerchantStoreRefundBrokerAcceptedTicketAndAuthQueryRetryStayPending(t *testing.T) {
	_, o, r, config := merchantStoreRefundBrokerFixture(t, MerchantStoreExternalPancake)
	posts, queries, auth := 0, 0, 0
	broker := merchantStoreRefundBroker{query: func(context.Context, *model.MerchantStoreOrder, MerchantStoreRefundNativeRequest) (MerchantStoreRefundProviderResult, error) {
		queries++
		if queries == 1 {
			return MerchantStoreRefundProviderResult{}, errors.New("query unavailable")
		}
		return MerchantStoreRefundProviderResult{State: MerchantStoreRefundProviderUnknown, Code: "refund_not_found"}, nil
	}, customer: func(context.Context, *model.MerchantStoreRefundDispatch) (*pancake.CustomerSession, error) {
		auth++
		if auth == 1 {
			return nil, errors.New("auth unavailable")
		}
		return merchantStoreRefundFakeCustomer(t, config, func(req *http.Request) (*http.Response, error) {
			posts++
			payload, _ := json.Marshal(map[string]any{"data": map[string]any{"ticket": map[string]any{"id": "TKT_AbCdEfGhIjKlMnOpQrStUv", "status": "succeeded", "subjectId": "PAY_AbCdEfGhIjKlMnOpQrStUv", "refundTicketMerchantExternalId": r.ID, "versionData": map[string]any{"requestedAmount": map[string]string{"amount": "0.25", "currency": "USD"}}}}})
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(payload))), Header: make(http.Header)}, nil
		}), nil
	}}
	require.NoError(t, broker.process(context.Background(), r.ID))
	require.Zero(t, posts)
	merchantStoreRefundTestDue(t, r.ID)
	require.NoError(t, broker.process(context.Background(), r.ID))
	require.Zero(t, posts)
	merchantStoreRefundTestDue(t, r.ID)
	require.NoError(t, broker.process(context.Background(), r.ID))
	require.Equal(t, 1, posts)
	var row model.MerchantStoreRefund
	require.NoError(t, model.DB.First(&row, "id = ?", r.ID).Error)
	require.Equal(t, "awaiting_provider", row.Status)
	var a model.MerchantStoreRefundProviderAttempt
	require.NoError(t, model.DB.First(&a, "refund_id = ?", r.ID).Error)
	require.Equal(t, "pending", a.State)
	require.Empty(t, a.EvidenceHash)
	merchantStoreRefundTestDue(t, r.ID)
	require.NoError(t, broker.process(context.Background(), r.ID))
	require.Equal(t, 1, posts)
	require.NoError(t, model.DB.First(o, "id = ?", o.ID).Error)
	require.Equal(t, "refund_pending", o.Status)
}

func TestMerchantStoreRefundBrokerTransportCannotForwardOtherTargetsOrCredentials(t *testing.T) {
	calls := 0
	transport := merchantStoreRefundCustomerTransport{token: "BOUND", environment: "prod", base: merchantStoreTestTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		require.Empty(t, r.Header.Get("Cookie"))
		require.Empty(t, r.Header.Get("Proxy-Authorization"))
		require.Empty(t, r.Header.Get("X-Idempotency-Key"))
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{}`)), Header: make(http.Header)}, nil
	})}
	for _, target := range []string{"https://evil.example/v1/actions/refund-ticket/create-ticket", "https://api.waffo.ai/v1/graphql", "https://api.waffo.ai/v1/actions/refund-ticket/resubmit-ticket", "https://api.waffo.ai/v1/actions/refund-ticket/create-ticket?leak=1"} {
		r, e := http.NewRequest("POST", target, nil)
		require.NoError(t, e)
		r.Header.Set("Authorization", "Bearer BOUND")
		r.Header.Set("X-Environment", "prod")
		_, e = transport.RoundTrip(r)
		require.Error(t, e)
	}
	require.Zero(t, calls)
	r, e := http.NewRequest("POST", pancake.DefaultBaseURL+"/v1/actions/refund-ticket/create-ticket", nil)
	require.NoError(t, e)
	r.Header.Set("Authorization", "Bearer OTHER")
	r.Header.Set("X-Environment", "prod")
	_, e = transport.RoundTrip(r)
	require.Error(t, e)
	r.Header.Set("Authorization", "Bearer BOUND")
	r.Header.Set("Cookie", "PRIVATE")
	r.Header.Set("Proxy-Authorization", "PRIVATE")
	r.Header.Set("X-Idempotency-Key", "ROTATING")
	resp, e := transport.RoundTrip(r)
	require.NoError(t, e)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, 1, calls)
}

func TestMerchantStoreRefundBrokerBatchAdvancesLaterOrderAfterEarlierLocalFailure(t *testing.T) {
	f, o, first, config := merchantStoreRefundBrokerFixture(t, MerchantStorePlatformPancake)
	// Build a second historical paid obligation before enabling refund writes.
	require.NoError(t, model.DB.Model(&model.Option{}).Where("key = ?", model.MerchantStoreWriterCapabilityOption).Update("value", "1").Error)
	other := f
	other.buyer.Id = 0
	other.buyer.Username = "other-refund-buyer"
	other.buyer.AffCode = "other-refund-buyer"
	require.NoError(t, model.DB.Create(&other.buyer).Error)
	require.NoError(t, model.AcceptMerchantStoreDisclaimer(other.buyer.Id, model.MerchantStoreDisclaimerVersion))
	secondOrder := merchantStoreTestOrder(t, other, MerchantStorePlatformPancake, config, 100, "USD")
	require.NoError(t, model.CompleteMerchantStorePayment(secondOrder.ID, "ORD_ZbCdEfGhIjKlMnOpQrStUv"))
	require.NoError(t, model.DB.Model(&model.Option{}).Where("key = ?", model.MerchantStoreWriterCapabilityOption).Update("value", "4").Error)
	require.NoError(t, model.RecordMerchantStoreRefundPaymentBasis(secondOrder.ID, model.VerifiedMerchantStoreRefundPaymentBasis{ReceiptReference: "ORD_ZbCdEfGhIjKlMnOpQrStUv", PaymentReference: "PAY_ZbCdEfGhIjKlMnOpQrStUv", AmountMinor: 110, Currency: "USD", EvidenceHash: strings.Repeat("b", 64)}))
	second, e := model.RequestMerchantStoreRefund(other.buyer.Id, secondOrder.ID, model.MerchantStoreRefundInput{RequestKey: "later", Reason: "Small price correction", Mode: "amount", AmountMinor: 1})
	require.NoError(t, e)
	_, e = model.DecideMerchantStoreRefund(f.seller.Id, secondOrder.ID, second.ID, model.MerchantStoreRefundDecision{Decision: "approve"})
	require.NoError(t, e)
	require.NoError(t, model.DB.Model(first).Update("created_at", 1000).Error)
	require.NoError(t, model.DB.Model(second).Update("created_at", 1001).Error)
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", f.seller.Id).Update("quota", 10000).Error)
	queried := []string{}
	broker := merchantStoreRefundBroker{query: func(_ context.Context, order *model.MerchantStoreOrder, request MerchantStoreRefundNativeRequest) (MerchantStoreRefundProviderResult, error) {
		queried = append(queried, order.ID)
		data := merchantStoreRefundProviderQueryFixture(request)
		data.Refunds[0].ID = request.RefundID
		data.Refunds[0].OrderMerchantExternalID = order.TradeNo
		data.Refunds[0].PSPAmountDetails.Amount = merchantStoreMinorMoney(request.AmountMinor)
		data.RefundTickets[0].RequestedAmountDetails.Amount = merchantStoreMinorMoney(request.AmountMinor)
		return merchantStorePancakeRefundExecutionResult(order, request, data)
	}}
	require.ErrorIs(t, broker.batch(context.Background(), 5), model.ErrMerchantStoreBalance)
	require.Equal(t, []string{o.ID, secondOrder.ID}, queried, "old failing item must not prevent the rest of this batch")
	require.NoError(t, model.DB.First(first, "id = ?", first.ID).Error)
	require.Equal(t, "reconciliation_required", first.Status)
	require.NoError(t, model.DB.First(second, "id = ?", second.ID).Error)
	require.Equal(t, "completed", second.Status)
	require.Equal(t, 10000-second.PrincipalQuota, merchantStoreRefundTestQuota(t, f.seller.Id))
}

func TestMerchantStoreRefundBrokerSignedPaymentFreezesActualPAYAndNotificationOnlyWakes(t *testing.T) {
	f := merchantStoreServiceDB(t, MerchantStoreExternalPancake)
	key, private := merchantStorePancakeTestKey(t)
	config := merchantStoreGatewayConfig{MerchantID: "MER_AbCdEfGhIjKlMnOpQrStUv", PrivateKey: private, StoreID: "STO_AbCdEfGhIjKlMnOpQrStUv", ProductID: "PROD_AbCdEfGhIjKlMnOpQrStUv", Environment: "prod", Currency: "USD"}
	o := merchantStoreTestOrder(t, f, MerchantStoreExternalPancake, config, 100, "USD")
	frozen, e := loadMerchantStorePaymentContext(o)
	require.NoError(t, e)
	callbackOrder := *o
	callbackOrder.ProviderTradeID = "ORD_AbCdEfGhIjKlMnOpQrStUv"
	request := MerchantStoreRefundNativeRequest{Payment: MerchantStoreRefundNativePayment{PaymentReference: "PAY_AbCdEfGhIjKlMnOpQrStUv"}}
	payload := merchantStoreRefundProviderWebhook(t, &callbackOrder, request, frozen, "order.completed", nil)
	require.NoError(t, model.DB.Model(&model.Option{}).Where("key = ?", model.MerchantStoreWriterCapabilityOption).Update("value", "4").Error)
	require.NoError(t, HandleMerchantStorePancakeWebhook(context.Background(), "external", f.seller.Id, "prod", payload, merchantStorePancakeSigned(t, key, payload)))
	var basis model.MerchantStoreRefundPaymentBasis
	require.NoError(t, model.DB.First(&basis, "order_id = ?", o.ID).Error)
	require.EqualValues(t, 110, basis.AmountMinor)
	require.Equal(t, "PAY_AbCdEfGhIjKlMnOpQrStUv", basis.PaymentReference)
	require.NoError(t, HandleMerchantStorePancakeWebhook(context.Background(), "external", f.seller.Id, "prod", payload, merchantStorePancakeSigned(t, key, payload)))
	r, e := model.RequestMerchantStoreRefund(f.buyer.Id, o.ID, model.MerchantStoreRefundInput{RequestKey: "notify", Reason: "Issue", Mode: "amount", AmountMinor: 25})
	require.NoError(t, e)
	r, e = model.DecideMerchantStoreRefund(f.seller.Id, o.ID, r.ID, model.MerchantStoreRefundDecision{Decision: "approve"})
	require.NoError(t, e)
	d, e := model.GetMerchantStoreRefundDispatchSnapshot(r.ID)
	require.NoError(t, e)
	request = merchantStoreDispatchNative(d)
	payload = merchantStoreRefundProviderWebhook(t, &callbackOrder, request, frozen, "refund.succeeded", nil)
	require.NoError(t, HandleMerchantStorePancakeWebhook(context.Background(), "external", f.seller.Id, "prod", payload, merchantStorePancakeSigned(t, key, payload)))
	require.NoError(t, model.DB.First(r, "id = ?", r.ID).Error)
	require.Equal(t, "awaiting_provider", r.Status)
	require.Nil(t, r.ProviderRefundReference)
	var a model.MerchantStoreRefundProviderAttempt
	require.NoError(t, model.DB.First(&a, "refund_id = ?", r.ID).Error)
	require.Equal(t, 1, a.SubmitCount)
	require.Equal(t, "unknown", a.State)
	require.LessOrEqual(t, a.NextCheckAt, time.Now().Unix())
	require.Error(t, HandleMerchantStorePancakeWebhook(context.Background(), "external", f.seller.Id, "prod", payload, "invalid"))
	posts := 0
	broker := merchantStoreRefundBroker{query: func(context.Context, *model.MerchantStoreOrder, MerchantStoreRefundNativeRequest) (MerchantStoreRefundProviderResult, error) {
		return MerchantStoreRefundProviderResult{State: MerchantStoreRefundProviderUnknown, Code: "refund_not_found"}, nil
	}, customer: func(context.Context, *model.MerchantStoreRefundDispatch) (*pancake.CustomerSession, error) {
		posts++
		return nil, nil
	}}
	require.NoError(t, broker.process(context.Background(), r.ID))
	require.Zero(t, posts, "signed notification without prior broker row must fence a possible provider operation")
}
