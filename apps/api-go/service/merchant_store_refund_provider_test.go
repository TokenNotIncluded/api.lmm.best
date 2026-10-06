package service

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/stretchr/testify/require"
	pancake "github.com/waffo-com/waffo-pancake-sdk-go"
)

// These tests do not install a database or contact a provider. The original
// paid receipt and encrypted payment snapshot are server-owned fixture facts.
func merchantStoreRefundProviderFixture(t *testing.T) (*model.MerchantStoreOrder, MerchantStoreRefundNativeRequest, merchantStorePaymentContext, *rsa.PrivateKey) {
	t.Helper()
	t.Setenv("MERCHANT_STORE_ENCRYPTION_KEY", "C5wmMzDh1QsVZb0saEW9ulAPzVN87Boqv3DK6eIrKXc2YLfg")
	key, private := merchantStorePancakeTestKey(t)
	order := &model.MerchantStoreOrder{
		ID: "original-order", TradeNo: "MS123456789012345678901234567890", BuyerID: 7, SellerID: 9,
		ProductID: "purchased-product", Status: "paid", PaymentMethod: MerchantStoreExternalPancake,
		AmountMinor: 100, Currency: "USD", FrozenUSDFX: "1", ProviderTradeID: "ORD_AbCdEfGhIjKlMnOpQrStUv",
	}
	frozen := merchantStorePaymentContext{
		Provider: order.PaymentMethod, Config: merchantStoreGatewayConfig{MerchantID: "MER_AbCdEfGhIjKlMnOpQrStUv", PrivateKey: private, StoreID: "STO_AbCdEfGhIjKlMnOpQrStUv", ProductID: "PROD_AbCdEfGhIjKlMnOpQrStUv", Environment: "prod", Currency: "USD"},
		AmountMinor: order.AmountMinor, Currency: order.Currency, FrozenRate: order.FrozenUSDFX,
		Origin: "https://api.example.com", PublicOrigin: "https://api.example.com", ExpiresIn: 1800,
	}
	var err error
	order.GatewaySnapshot, err = encryptMerchantStorePaymentValue(merchantStorePaymentContextPurpose(order.ID), frozen)
	require.NoError(t, err)
	order.PaymentScopeHash, err = merchantStorePaymentScopeHash(frozen)
	require.NoError(t, err)
	request := MerchantStoreRefundNativeRequest{
		RefundID: "immutable-refund-1", OrderID: order.ID, AmountMinor: 25, Currency: "USD",
		Payment: MerchantStoreRefundNativePayment{ReceiptReference: order.ProviderTradeID, PaymentReference: "PAY_AbCdEfGhIjKlMnOpQrStUv", AmountMinor: 110, Currency: "USD", EvidenceHash: strings.Repeat("a", 64)},
	}
	return order, request, frozen, key
}

func merchantStoreRefundProviderWebhook(t *testing.T, order *model.MerchantStoreOrder, request MerchantStoreRefundNativeRequest, frozen merchantStorePaymentContext, eventType string, change func(map[string]any)) []byte {
	t.Helper()
	data := map[string]any{
		"orderId": order.ProviderTradeID, "orderMerchantExternalId": order.TradeNo,
		"merchantProvidedBuyerIdentity": WaffoPancakeBuyerIdentityFromUserID(order.BuyerID),
		"currency":                      order.Currency, "paymentId": request.Payment.PaymentReference,
		"amount": "1.10", "taxAmount": "0.10", "chargedAmount": "1.10", "paymentStatus": "succeeded", "orderStatus": "completed",
		"orderMetadata": map[string]string{"lmm_store_order_id": order.ID, "lmm_store_product_id": order.ProductID, "lmm_pancake_product_id": frozen.Config.ProductID, "lmm_store_seller_id": strconv.Itoa(order.SellerID)},
	}
	if eventType == "refund.succeeded" || eventType == "refund.failed" {
		data["refundTicketMerchantExternalId"] = request.RefundID
		data["originalChargedAmount"] = "1.10"
		data["refundedAmount"] = "0.25"
		data["refundStatus"] = strings.TrimPrefix(eventType, "refund.")
	}
	if change != nil {
		change(data)
	}
	payload, err := json.Marshal(map[string]any{"id": "delivery-uuid", "eventId": "provider-business-id", "eventType": eventType, "mode": frozen.Config.Environment, "storeId": frozen.Config.StoreID, "timestamp": time.Now().UTC().Format(time.RFC3339Nano), "data": data})
	require.NoError(t, err)
	return payload
}

func merchantStoreRefundProviderQueryFixture(request MerchantStoreRefundNativeRequest) merchantStorePancakeRefundQueryData {
	ticket := merchantStorePancakeRefundQueryTicket{ID: "TKT_AbCdEfGhIjKlMnOpQrStUv", Status: "succeeded", RefundTicketMerchantExternalID: request.RefundID, RequestedAmountDetails: merchantStoreRefundQueryAmount{Amount: "0.25", Currency: request.Currency}}
	ticket.Payment.ID, ticket.Payment.Status, ticket.Payment.OnetimeOrder.ID = request.Payment.PaymentReference, "succeeded", request.Payment.ReceiptReference
	return merchantStorePancakeRefundQueryData{
		RefundTickets: []merchantStorePancakeRefundQueryTicket{ticket},
		Refunds:       []merchantStorePancakeRefundQueryExecution{{ID: "provider-refund-execution-1", Status: "succeeded", OrderMerchantExternalID: "MS123456789012345678901234567890", RefundTicketMerchantExternalID: request.RefundID, PSPAmountDetails: merchantStoreRefundQueryAmount{Amount: "0.25", Currency: request.Currency}}},
	}
}

func TestMerchantStoreRefundProviderCapabilitiesRespectNativeChannels(t *testing.T) {
	for _, test := range []struct {
		method, submission, partial string
		query, callback             bool
	}{
		{MerchantStoreBalance, "balance", "supported", false, false},
		{MerchantStorePlatformPancake, "refund_ticket", "conditional", true, true},
		{MerchantStoreExternalPancake, "refund_ticket", "conditional", true, true},
		{MerchantStorePlatformLinuxDO, "full_refund", "unsupported", false, false},
		{MerchantStoreExternalEpay, "manual", "unknown", false, false},
		{"unrecognized", "manual", "unknown", false, false},
	} {
		t.Run(test.method, func(t *testing.T) {
			capability := MerchantStoreRefundProviderCapabilities(test.method)
			require.Equal(t, test.submission, capability.Submission)
			require.Equal(t, test.partial, capability.Partial)
			require.Equal(t, test.query, capability.ExecutionQuery)
			require.Equal(t, test.callback, capability.SignedCallback)
		})
	}
	_, usable := (MerchantStoreRefundProviderResult{State: MerchantStoreRefundProviderSucceeded}).VerifiedEvidence()
	require.False(t, usable, "a plain state value is not verified money-return evidence")
}

func TestMerchantStoreRefundProviderOriginalChargeRequiresSignedActualPAY(t *testing.T) {
	order, request, frozen, key := merchantStoreRefundProviderFixture(t)
	payload := merchantStoreRefundProviderWebhook(t, order, request, frozen, "order.completed", nil)
	proof, err := VerifyMerchantStorePancakeRefundPaymentBasis(order, payload, merchantStorePancakeSigned(t, key, payload))
	require.NoError(t, err)
	basis, valid := proof.VerifiedPayment()
	require.True(t, valid)
	require.EqualValues(t, 110, basis.AmountMinor, "actual collected tax remains native money, unlike the 100 minor quote")
	require.Equal(t, request.Payment.PaymentReference, basis.PaymentReference)
	require.Equal(t, order.ProviderTradeID, basis.ReceiptReference)
	require.True(t, merchantStoreRefundHasHash(basis.EvidenceHash))
	var retryEnvelope map[string]any
	require.NoError(t, json.Unmarshal(payload, &retryEnvelope))
	retryEnvelope["id"] = "another-genuine-delivery-uuid"
	retryPayload, err := json.Marshal(retryEnvelope)
	require.NoError(t, err)
	retryProof, err := VerifyMerchantStorePancakeRefundPaymentBasis(order, retryPayload, merchantStorePancakeSigned(t, key, retryPayload))
	require.NoError(t, err)
	retryBasis, valid := retryProof.VerifiedPayment()
	require.True(t, valid)
	require.Equal(t, basis.EvidenceHash, retryBasis.EvidenceHash, "same original charge is idempotent despite delivery identity changes")
	encoded, err := json.Marshal(basis)
	require.NoError(t, err)
	require.JSONEq(t, `{}`, string(encoded), "private proof must not become a public DTO")
	for _, test := range []struct {
		name   string
		change func(map[string]any)
	}{
		{"legacy_list_price_fallback", func(data map[string]any) { delete(data, "chargedAmount") }},
		{"missing_PAY", func(data map[string]any) { delete(data, "paymentId") }},
		{"ORD_is_not_PAY", func(data map[string]any) { data["paymentId"] = order.ProviderTradeID }},
		{"buyer", func(data map[string]any) { data["merchantProvidedBuyerIdentity"] = "new-api-user-8" }},
		{"original_receipt", func(data map[string]any) { data["orderId"] = "ORD_other" }},
		{"native_currency", func(data map[string]any) { data["currency"] = "CNY" }},
		{"charge_precision", func(data map[string]any) { data["chargedAmount"] = "1.001" }},
		{"zero_charge", func(data map[string]any) { data["chargedAmount"] = "0.00" }},
		{"failed_payment", func(data map[string]any) { data["paymentStatus"] = "failed" }},
		{"different_seller", func(data map[string]any) { data["orderMetadata"].(map[string]string)["lmm_store_seller_id"] = "10" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			payload := merchantStoreRefundProviderWebhook(t, order, request, frozen, "order.completed", test.change)
			proof, err := VerifyMerchantStorePancakeRefundPaymentBasis(order, payload, merchantStorePancakeSigned(t, key, payload))
			require.Error(t, err)
			require.Nil(t, proof)
		})
	}
	_, err = VerifyMerchantStorePancakeRefundPaymentBasis(order, payload, "invalid-signature")
	require.Error(t, err)
	other := *order
	other.PaymentScopeHash = strings.Repeat("f", 64)
	_, err = VerifyMerchantStorePancakeRefundPaymentBasis(&other, payload, merchantStorePancakeSigned(t, key, payload))
	require.Error(t, err, "another tenant's payment scope is not the frozen snapshot")
}

func TestMerchantStoreRefundProviderTicketBuildPreservesFrozenPartialAmount(t *testing.T) {
	order, request, _, _ := merchantStoreRefundProviderFixture(t)
	params, err := BuildMerchantStorePancakeRefundTicket(order, request, "Return one purchased item")
	require.NoError(t, err)
	require.Equal(t, request.Payment.PaymentReference, params.PaymentID)
	require.Equal(t, "0.25", params.RequestedAmount.Amount)
	require.Equal(t, "USD", params.RequestedAmount.Currency)
	require.Equal(t, request.RefundID, *params.RefundTicketMerchantExternalID)
	require.Equal(t, map[string]any{"lmm_store_order_id": order.ID, "lmm_store_refund_id": request.RefundID}, params.Metadata)
	for _, test := range []struct {
		name   string
		change func(*MerchantStoreRefundNativeRequest)
	}{
		{"over_actual_charge", func(r *MerchantStoreRefundNativeRequest) { r.AmountMinor = 111 }},
		{"zero", func(r *MerchantStoreRefundNativeRequest) { r.AmountMinor = 0 }},
		{"wrong_original_receipt", func(r *MerchantStoreRefundNativeRequest) { r.Payment.ReceiptReference = "ORD_other" }},
		{"wrong_currency", func(r *MerchantStoreRefundNativeRequest) { r.Currency = "CNY" }},
		{"missing_verified_basis", func(r *MerchantStoreRefundNativeRequest) { r.Payment.EvidenceHash = "" }},
		{"unrelated_order", func(r *MerchantStoreRefundNativeRequest) { r.OrderID = "other-order" }},
		{"unsupported_huge_native_amount", func(r *MerchantStoreRefundNativeRequest) {
			r.Payment.AmountMinor, r.AmountMinor = math.MaxInt64, math.MaxInt64
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := request
			test.change(&changed)
			_, err := BuildMerchantStorePancakeRefundTicket(order, changed, "Reason")
			require.Error(t, err)
		})
	}
	full := request
	full.AmountMinor = 110
	params, err = BuildMerchantStorePancakeRefundTicket(order, full, "Full native refund")
	require.NoError(t, err)
	require.Equal(t, "1.10", params.RequestedAmount.Amount, "full is actual charge, not 1.00 quoted principal")
}

func TestMerchantStoreRefundProviderTicketSucceededIsNotRefundCompleted(t *testing.T) {
	order, request, _, _ := merchantStoreRefundProviderFixture(t)
	ref := request.RefundID
	ticket := &pancake.RefundTicketResult{Ticket: pancake.RefundTicket{ID: "TKT_AbCdEfGhIjKlMnOpQrStUv", SubjectID: request.Payment.PaymentReference, RefundTicketMerchantExternalID: &ref, VersionData: &pancake.RefundTicketVersionData{RequestedAmount: &pancake.RequestedAmount{Amount: "0.25", Currency: "USD"}}}}
	for _, test := range []struct {
		status string
		state  MerchantStoreRefundProviderState
	}{
		{"pending", MerchantStoreRefundProviderRequested}, {"under_review", MerchantStoreRefundProviderRequested},
		{"approved", MerchantStoreRefundProviderPending}, {"processing", MerchantStoreRefundProviderPending}, {"succeeded", MerchantStoreRefundProviderPending},
		{"failed", MerchantStoreRefundProviderFailed}, {"rejected", MerchantStoreRefundProviderFailed}, {"cancelled", MerchantStoreRefundProviderFailed},
		{"provider_added_state", MerchantStoreRefundProviderUnknown},
	} {
		t.Run(test.status, func(t *testing.T) {
			ticket.Ticket.Status = test.status
			result, err := ObserveMerchantStorePancakeRefundTicket(order, request, ticket)
			require.NoError(t, err)
			require.Equal(t, test.state, result.State)
			_, completed := result.VerifiedEvidence()
			require.False(t, completed)
			_, canRelease := result.VerifiedFailure()
			require.False(t, canRelease, "ticket-only decisions are not a verified terminal PSP execution")
		})
	}
	ticket.Ticket.SubjectID = "PAY_ZbCdEfGhIjKlMnOpQrStUv"
	_, err := ObserveMerchantStorePancakeRefundTicket(order, request, ticket)
	require.Error(t, err)
}

func TestMerchantStoreRefundProviderQueryBindsExecutionPaymentAndNativeAmount(t *testing.T) {
	order, request, frozen, _ := merchantStoreRefundProviderFixture(t)
	data := merchantStoreRefundProviderQueryFixture(request)
	var calls int
	transport := merchantStoreProviderTransport{orderID: order.ID, base: merchantStoreTestTransport(func(req *http.Request) (*http.Response, error) {
		calls++
		require.Equal(t, "https://api.waffo.ai/v1/graphql", req.URL.String())
		require.Equal(t, http.MethodPost, req.Method)
		require.Equal(t, frozen.Config.MerchantID, req.Header.Get("X-Merchant-Id"))
		require.NotEmpty(t, req.Header.Get("X-Signature"))
		require.Empty(t, req.Header.Get("Authorization"))
		require.Empty(t, req.Header.Get("Cookie"))
		require.Empty(t, req.Header.Get("X-Idempotency-Key"), "every refund query must be fresh")
		body, err := io.ReadAll(req.Body)
		require.NoError(t, err)
		var posted pancake.GraphQLParams
		require.NoError(t, json.Unmarshal(body, &posted))
		require.Equal(t, request.RefundID, posted.Variables["ref"])
		require.Equal(t, merchantStorePancakeRefundExecutionQuery, posted.Query)
		require.NotContains(t, string(body), frozen.Config.PrivateKey)
		response, err := json.Marshal(map[string]any{"data": data})
		require.NoError(t, err)
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(response)))}, nil
	})}
	client, err := pancake.New(pancake.Config{MerchantID: frozen.Config.MerchantID, PrivateKey: frozen.Config.PrivateKey, Environment: pancake.EnvironmentProd, HTTPClient: &http.Client{Transport: transport}})
	require.NoError(t, err)
	var firstProofHash string
	for i := 0; i < 2; i++ {
		result, err := merchantStoreQueryPancakeRefundWithClient(context.Background(), order, request, client)
		require.NoError(t, err)
		require.Equal(t, MerchantStoreRefundProviderSucceeded, result.State)
		proof, completed := result.VerifiedEvidence()
		require.True(t, completed)
		require.Equal(t, request.Payment.PaymentReference, proof.PaymentReference)
		require.Equal(t, "provider-refund-execution-1", proof.RefundReference)
		require.EqualValues(t, 25, proof.AmountMinor)
		require.Equal(t, "USD", proof.Currency)
		require.True(t, merchantStoreRefundHasHash(proof.EvidenceHash))
		if i == 0 {
			firstProofHash = proof.EvidenceHash
			data.RefundTickets[0].Status = "processing"
		} else {
			require.Equal(t, firstProofHash, proof.EvidenceHash, "same PSP execution remains idempotent when its ticket status changes")
		}
		encoded, err := json.Marshal(result)
		require.NoError(t, err)
		require.NotContains(t, string(encoded), "PAY_")
		require.NotContains(t, string(encoded), "provider-refund-execution-1")
	}
	require.Equal(t, 2, calls, "replay only re-reads the same immutable business reference, never a create POST")
}

func TestMerchantStoreRefundProviderQueryRejectsWrongOrAmbiguousProof(t *testing.T) {
	order, request, _, _ := merchantStoreRefundProviderFixture(t)
	for _, test := range []struct {
		name   string
		change func(*merchantStorePancakeRefundQueryData)
	}{
		{"different_PAY", func(d *merchantStorePancakeRefundQueryData) { d.RefundTickets[0].Payment.ID = "PAY_other" }},
		{"different_ORD", func(d *merchantStorePancakeRefundQueryData) { d.RefundTickets[0].Payment.OnetimeOrder.ID = "ORD_other" }},
		{"not_paid", func(d *merchantStorePancakeRefundQueryData) { d.RefundTickets[0].Payment.Status = "failed" }},
		{"ticket_other_reference", func(d *merchantStorePancakeRefundQueryData) {
			d.RefundTickets[0].RefundTicketMerchantExternalID = "different-refund"
		}},
		{"ticket_other_amount", func(d *merchantStorePancakeRefundQueryData) {
			d.RefundTickets[0].RequestedAmountDetails.Amount = "0.26"
		}},
		{"execution_other_reference", func(d *merchantStorePancakeRefundQueryData) {
			d.Refunds[0].RefundTicketMerchantExternalID = "different-refund"
		}},
		{"execution_other_order", func(d *merchantStorePancakeRefundQueryData) { d.Refunds[0].OrderMerchantExternalID = "other-order" }},
		{"execution_other_amount", func(d *merchantStorePancakeRefundQueryData) { d.Refunds[0].PSPAmountDetails.Amount = "0.24" }},
		{"execution_other_currency", func(d *merchantStorePancakeRefundQueryData) { d.Refunds[0].PSPAmountDetails.Currency = "CNY" }},
		{"multiple_ticket_PAY_candidates", func(d *merchantStorePancakeRefundQueryData) {
			d.RefundTickets = append(d.RefundTickets, d.RefundTickets[0])
		}},
		{"multiple_PSP_executions", func(d *merchantStorePancakeRefundQueryData) { d.Refunds = append(d.Refunds, d.Refunds[0]) }},
		{"ticket_and_execution_conflict", func(d *merchantStorePancakeRefundQueryData) { d.RefundTickets[0].Status = "rejected" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			data := merchantStoreRefundProviderQueryFixture(request)
			test.change(&data)
			result, err := merchantStorePancakeRefundExecutionResult(order, request, data)
			require.Error(t, err)
			require.Equal(t, MerchantStoreRefundProviderUnknown, result.State)
			_, completed := result.VerifiedEvidence()
			require.False(t, completed)
		})
	}
	empty, err := merchantStorePancakeRefundExecutionResult(order, request, merchantStorePancakeRefundQueryData{})
	require.NoError(t, err)
	require.Equal(t, MerchantStoreRefundProviderUnknown, empty.State)
	withoutExecution := merchantStoreRefundProviderQueryFixture(request)
	withoutExecution.Refunds = nil
	ticketOnly, err := merchantStorePancakeRefundExecutionResult(order, request, withoutExecution)
	require.NoError(t, err)
	require.Equal(t, MerchantStoreRefundProviderPending, ticketOnly.State)
	_, completed := ticketOnly.VerifiedEvidence()
	require.False(t, completed)
	for _, state := range []string{"failed", "provider_added_execution_state"} {
		data := merchantStoreRefundProviderQueryFixture(request)
		data.Refunds[0].Status = state
		data.RefundTickets[0].Status = "failed"
		if state == "failed" {
			data.Refunds[0].PSPAmountDetails.Amount = "0.00"
		}
		result, err := merchantStorePancakeRefundExecutionResult(order, request, data)
		require.NoError(t, err)
		_, completed := result.VerifiedEvidence()
		require.False(t, completed)
		if state == "failed" {
			require.Equal(t, MerchantStoreRefundProviderFailed, result.State)
			failure, canRelease := result.VerifiedFailure()
			require.True(t, canRelease)
			require.Equal(t, request.Payment.PaymentReference, failure.PaymentReference)
			require.EqualValues(t, 25, failure.RequestedAmountMinor, "failed PSP execution may report zero moved, while the ticket freezes what was requested")
			require.Equal(t, "provider-refund-execution-1", failure.RefundReference)
			require.True(t, merchantStoreRefundHasHash(failure.EvidenceHash))
		} else {
			require.Equal(t, MerchantStoreRefundProviderUnknown, result.State)
		}
	}
}

func TestMerchantStoreRefundProviderSignedNotificationsRequireExecutionQuery(t *testing.T) {
	order, request, frozen, key := merchantStoreRefundProviderFixture(t)
	payload := merchantStoreRefundProviderWebhook(t, order, request, frozen, "refund.succeeded", nil)
	result, err := VerifyMerchantStorePancakeRefundNotification(order, request, payload, merchantStorePancakeSigned(t, key, payload))
	require.NoError(t, err)
	require.Equal(t, MerchantStoreRefundProviderPending, result.State)
	_, completed := result.VerifiedEvidence()
	require.False(t, completed, "the delivery UUID/eventId must not become a financial refund receipt")
	for _, test := range []struct {
		name   string
		change func(map[string]any)
	}{
		{"another_refund", func(d map[string]any) { d["refundTicketMerchantExternalId"] = "other-refund" }},
		{"another_payment", func(d map[string]any) { d["paymentId"] = "PAY_other" }},
		{"another_amount", func(d map[string]any) { d["refundedAmount"] = "0.26" }},
		{"another_original_charge", func(d map[string]any) { d["originalChargedAmount"] = "1.00" }},
		{"contradictory_state", func(d map[string]any) { d["refundStatus"] = "failed" }},
		{"another_buyer", func(d map[string]any) { d["merchantProvidedBuyerIdentity"] = "new-api-user-8" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			payload := merchantStoreRefundProviderWebhook(t, order, request, frozen, "refund.succeeded", test.change)
			result, err := VerifyMerchantStorePancakeRefundNotification(order, request, payload, merchantStorePancakeSigned(t, key, payload))
			require.Error(t, err)
			require.Equal(t, MerchantStoreRefundProviderUnknown, result.State)
		})
	}
	// The official refund payload does not promise paymentId. It can still
	// trigger a read-only ticket/payment/execution query without inventing it.
	payload = merchantStoreRefundProviderWebhook(t, order, request, frozen, "refund.succeeded", func(d map[string]any) { delete(d, "paymentId"); delete(d, "refundedAmount") })
	result, err = VerifyMerchantStorePancakeRefundNotification(order, request, payload, merchantStorePancakeSigned(t, key, payload))
	require.NoError(t, err)
	require.Equal(t, MerchantStoreRefundProviderPending, result.State)
	payload = merchantStoreRefundProviderWebhook(t, order, request, frozen, "refund.failed", func(d map[string]any) { d["refundedAmount"] = "0.00" })
	result, err = VerifyMerchantStorePancakeRefundNotification(order, request, payload, merchantStorePancakeSigned(t, key, payload))
	require.NoError(t, err)
	require.Equal(t, MerchantStoreRefundProviderFailed, result.State)
	_, completed = result.VerifiedEvidence()
	require.False(t, completed)
	_, canRelease := result.VerifiedFailure()
	require.False(t, canRelease, "a notification still needs the actual PSP execution query")
}

func TestMerchantStoreRefundProviderUnknownReadsNeverBecomeRefundSuccess(t *testing.T) {
	order, request, frozen, _ := merchantStoreRefundProviderFixture(t)
	plausibleSucceededData, err := json.Marshal(map[string]any{"data": merchantStoreRefundProviderQueryFixture(request), "errors": []map[string]any{{"message": "private provider response"}}})
	require.NoError(t, err)
	for _, test := range []struct {
		name           string
		body           string
		transportError error
	}{
		{"graphql_errors_even_with_succeeded_data", string(plausibleSucceededData), nil},
		{"unparseable_reply", `not-json-private-token`, nil},
		{"ambiguous_timeout", "", errors.New("private-token in timeout detail")},
		{"not_found_after_timeout", `{"data":{"refundTickets":[],"refunds":[]}}`, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			client, err := pancake.New(pancake.Config{MerchantID: frozen.Config.MerchantID, PrivateKey: frozen.Config.PrivateKey, Environment: pancake.EnvironmentProd, HTTPClient: &http.Client{Transport: merchantStoreProviderTransport{orderID: order.ID, base: merchantStoreTestTransport(func(req *http.Request) (*http.Response, error) {
				calls++
				require.Equal(t, "/v1/graphql", req.URL.Path)
				if test.transportError != nil {
					return nil, test.transportError
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(test.body))}, nil
			})}}})
			require.NoError(t, err)
			result, err := merchantStoreQueryPancakeRefundWithClient(context.Background(), order, request, client)
			require.Equal(t, MerchantStoreRefundProviderUnknown, result.State)
			_, completed := result.VerifiedEvidence()
			require.False(t, completed)
			if err != nil {
				require.Equal(t, ErrMerchantStoreRefundProvider, err)
				require.NotContains(t, err.Error(), "private")
			}
			require.Equal(t, 1, calls)
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := QueryMerchantStorePancakeRefund(ctx, order, request)
	require.Error(t, err)
	require.Equal(t, MerchantStoreRefundProviderUnknown, result.State)
}
