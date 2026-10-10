package service

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/model"
)

// Exercise the decoded execution, not just the ticket's payment link. A
// succeeded proof can settle funds; a failed proof can release a reservation.
// Neither is valid when the execution belongs to a different payment.
func TestBusinessAuditRefundExecutionPaymentBinding(t *testing.T) {
	const originalPayment = "PAY_AbCdEfGhIjKlMnOpQrStUv"
	for _, currency := range []string{"USD", "CNY"} {
		for _, status := range []string{"succeeded", "failed"} {
			for _, tc := range []struct {
				name, payment string
				valid         bool
			}{
				{"original", originalPayment, true},
				{"other_payment", "PAY_ZbCdEfGhIjKlMnOpQrStUv", false},
				{"missing_payment", "", false},
				{"whitespace", " " + originalPayment, false},
				{"ticket_is_not_payment", "TKT_AbCdEfGhIjKlMnOpQrStUv", false},
			} {
				t.Run(currency+"/"+status+"/"+tc.name, func(t *testing.T) {
					order := &model.MerchantStoreOrder{
						ID: "bi06-order", TradeNo: "MS123456789012345678901234567890",
						PaymentMethod: "external:waffo_pancake", PaymentScopeHash: strings.Repeat("b", 64),
					}
					request := MerchantStoreRefundNativeRequest{
						RefundID: "bi06-refund", OrderID: order.ID, AmountMinor: 25, Currency: currency,
						Payment: MerchantStoreRefundNativePayment{
							ReceiptReference: "ORD_AbCdEfGhIjKlMnOpQrStUv", PaymentReference: originalPayment,
							AmountMinor: 110, Currency: currency, EvidenceHash: strings.Repeat("a", 64),
						},
					}
					amount := "0.25"
					if status == "failed" {
						amount = "0.00"
					}
					execution := map[string]any{
						"id": "bi06-execution", "status": status,
						"orderMerchantExternalId": order.TradeNo, "refundTicketMerchantExternalId": request.RefundID,
						"pspAmountDetails": map[string]any{"amount": amount, "currency": currency},
					}
					if tc.payment != "" {
						execution["paymentId"] = tc.payment
					}
					body, err := json.Marshal(map[string]any{
						"refundTicketsCount": 1, "refundsCount": 1,
						"refundTickets": []any{map[string]any{
							"id": "TKT_AbCdEfGhIjKlMnOpQrStUv", "status": status,
							"refundTicketMerchantExternalId": request.RefundID,
							"requestedAmountDetails":         map[string]any{"amount": "0.25", "currency": currency},
							"payment":                        map[string]any{"id": originalPayment, "status": "succeeded", "onetimeOrder": map[string]any{"id": request.Payment.ReceiptReference}},
						}},
						"refunds": []any{execution},
					})
					if err != nil {
						t.Fatal(err)
					}
					var data merchantStorePancakeRefundQueryData
					if err := json.Unmarshal(body, &data); err != nil {
						t.Fatal(err)
					}
					result, err := merchantStorePancakeRefundExecutionResult(order, request, data)
					proof, completed := result.VerifiedEvidence()
					failure, canRelease := result.VerifiedFailure()
					t.Logf("original=%q execution_payment=%q currency=%s requested_minor=%d observed=%s completed=%t release=%t", originalPayment, tc.payment, currency, request.AmountMinor, result.State, completed, canRelease)
					if !tc.valid {
						if err == nil || result.State != MerchantStoreRefundProviderUnknown || completed || canRelease {
							t.Fatalf("unbound execution granted financial evidence: state=%s error=%v completed=%t release=%t", result.State, err, completed, canRelease)
						}
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					if status == "succeeded" {
						if !completed || canRelease || proof.PaymentReference != originalPayment || proof.RefundReference != "bi06-execution" || proof.AmountMinor != 25 || proof.Currency != currency || !merchantStoreRefundHasHash(proof.EvidenceHash) {
							t.Fatalf("valid success lost its original-payment evidence: %+v", result)
						}
					} else if completed || !canRelease || failure.PaymentReference != originalPayment || failure.RefundReference != "bi06-execution" || failure.RequestedAmountMinor != 25 || failure.Currency != currency || !merchantStoreRefundHasHash(failure.EvidenceHash) {
						t.Fatalf("valid failure lost its original-payment evidence: %+v", result)
					}
				})
			}
		}
	}
}

func TestBusinessAuditRefundExecutionQueryRequestsPaymentID(t *testing.T) {
	// A field on the decoded struct is insufficient when GraphQL never asks for it.
	if !strings.Contains(merchantStorePancakeRefundExecutionQuery, "paymentId") {
		t.Fatal("refund execution query omits the paymentId needed to verify original payment ownership")
	}
}
