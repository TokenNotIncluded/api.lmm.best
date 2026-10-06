package service

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/model"
	pancake "github.com/waffo-com/waffo-pancake-sdk-go"
)

// The pinned SDK verifies the complete raw payload before these documented
// additive fields are decoded. Do not upgrade the dependency just to read them,
// or fall back to amount/total: older payloads can carry list-price fallbacks.
type merchantStoreRefundWebhookData struct {
	pancake.WebhookEventData
	ChargedAmount         *string `json:"chargedAmount,omitempty"`
	RefundedAmount        *string `json:"refundedAmount,omitempty"`
	OriginalChargedAmount *string `json:"originalChargedAmount,omitempty"`
}

func merchantStoreRefundVerifyWebhook(order *model.MerchantStoreOrder, frozen merchantStorePaymentContext, payload []byte, signature string) (*pancake.WebhookEvent, merchantStoreRefundWebhookData, error) {
	var data merchantStoreRefundWebhookData
	if len(payload) == 0 || len(payload) > 256<<10 || (order.PaymentMethod != MerchantStorePlatformPancake && order.PaymentMethod != MerchantStoreExternalPancake) {
		return nil, data, ErrMerchantStoreRefundProvider
	}
	event, err := pancake.VerifyWebhook(string(payload), signature, &pancake.VerifyWebhookOptions{Environment: pancake.Environment(frozen.Config.Environment)})
	if err != nil || event == nil || json.Unmarshal(event.Data, &data) != nil {
		return nil, data, ErrMerchantStoreRefundProvider
	}
	if string(event.Mode) != frozen.Config.Environment || event.StoreID != frozen.Config.StoreID || data.OrderID != order.ProviderTradeID || data.Currency != order.Currency || data.OrderMerchantExternalID == nil || *data.OrderMerchantExternalID != order.TradeNo || data.MerchantProvidedBuyerIdentity == nil || *data.MerchantProvidedBuyerIdentity != WaffoPancakeBuyerIdentityFromUserID(order.BuyerID) || data.OrderMetadata["lmm_store_order_id"] != order.ID || data.OrderMetadata["lmm_store_product_id"] != order.ProductID || data.OrderMetadata["lmm_pancake_product_id"] != frozen.Config.ProductID || data.OrderMetadata["lmm_store_seller_id"] != strconv.Itoa(order.SellerID) {
		return nil, data, ErrMerchantStoreRefundProvider
	}
	return event, data, nil
}

// VerifyMerchantStorePancakeRefundPaymentBasis verifies original payment
// evidence only; it does not settle an order or write a payment-basis row. The
// existing paid receipt must match before a PAY identifier or actual collected
// native amount may be frozen by the model's server-only recording primitive.
func VerifyMerchantStorePancakeRefundPaymentBasis(order *model.MerchantStoreOrder, payload []byte, signature string) (*MerchantStoreRefundPaymentProof, error) {
	if !merchantStoreRefundPaymentOrder(order) {
		return nil, ErrMerchantStoreRefundProvider
	}
	frozen, err := loadMerchantStorePaymentContext(order)
	if err != nil {
		return nil, err
	}
	event, data, err := merchantStoreRefundVerifyWebhook(order, frozen, payload, signature)
	if err != nil {
		return nil, err
	}
	if event.EventType != "order.completed" || (data.OrderStatus != nil && *data.OrderStatus != "completed") || data.PaymentStatus == nil || *data.PaymentStatus != "succeeded" {
		return nil, ErrMerchantStoreRefundProvider
	}
	if data.ChargedAmount == nil || data.PaymentID == nil {
		return nil, ErrMerchantStoreRefundBasisUnavailable
	}
	minor, err := merchantStoreMoneyToMinor(*data.ChargedAmount)
	if err != nil || !merchantStorePancakeShortID(*data.PaymentID, "PAY") {
		return nil, ErrMerchantStoreRefundProvider
	}
	// Hash verified immutable facts, not a mutable delivery envelope. The same
	// genuine PAY/native charge must remain idempotent across delivery IDs.
	facts, err := json.Marshal([]any{order.ProviderTradeID, *data.PaymentID, minor, data.Currency, order.TradeNo, *data.MerchantProvidedBuyerIdentity, frozen.Config.MerchantID, event.StoreID, frozen.Config.ProductID, order.SellerID})
	if err != nil {
		return nil, ErrMerchantStoreRefundProvider
	}
	return &MerchantStoreRefundPaymentProof{payment: MerchantStoreRefundNativePayment{
		ReceiptReference: order.ProviderTradeID, PaymentReference: *data.PaymentID,
		AmountMinor: minor, Currency: data.Currency,
		EvidenceHash: merchantStoreRefundEvidenceHash("pancake-original-charge-v1", order, "", facts),
	}}, nil
}

// A verified refund notification wakes reconciliation. A delivery UUID is not
// a PSP refund identifier, and v0.11.0 does not promise that eventId identifies
// a refund execution. Only the authenticated execution query below can produce
// completion evidence with a stable refund ID and its payment linkage.
func VerifyMerchantStorePancakeRefundNotification(order *model.MerchantStoreOrder, request MerchantStoreRefundNativeRequest, payload []byte, signature string) (MerchantStoreRefundProviderResult, error) {
	result := MerchantStoreRefundProviderResult{State: MerchantStoreRefundProviderUnknown, Code: "invalid_notification"}
	frozen, err := merchantStoreRefundValidateRequest(order, request)
	if err != nil {
		return result, err
	}
	event, data, err := merchantStoreRefundVerifyWebhook(order, frozen, payload, signature)
	if err != nil {
		return result, err
	}
	if (event.EventType != "refund.succeeded" && event.EventType != "refund.failed") || data.RefundTicketMerchantExternalID == nil || *data.RefundTicketMerchantExternalID != request.RefundID {
		return result, ErrMerchantStoreRefundProvider
	}
	if data.PaymentID != nil && *data.PaymentID != request.Payment.PaymentReference {
		return result, ErrMerchantStoreRefundProvider
	}
	if data.OriginalChargedAmount != nil {
		minor, err := merchantStoreMoneyToMinor(*data.OriginalChargedAmount)
		if err != nil || minor != request.Payment.AmountMinor {
			return result, ErrMerchantStoreRefundProvider
		}
	}
	if data.RefundedAmount != nil {
		minor, err := merchantStoreMoneyToMinor(*data.RefundedAmount)
		zeroFailed := event.EventType == "refund.failed" && merchantStoreMoneyPattern.MatchString(*data.RefundedAmount) && strings.Trim(*data.RefundedAmount, "0.") == ""
		if !zeroFailed && (err != nil || minor != request.AmountMinor) {
			return result, ErrMerchantStoreRefundProvider
		}
	}
	expected := "succeeded"
	if event.EventType == "refund.failed" {
		expected = "failed"
	}
	if data.RefundStatus != nil && *data.RefundStatus != expected {
		return result, ErrMerchantStoreRefundProvider
	}
	result.State, result.Code = MerchantStoreRefundProviderPending, "execution_query_required"
	if expected == "failed" {
		result.State, result.Code = MerchantStoreRefundProviderFailed, "provider_reported_failure"
	}
	return result, nil
}

// Every selected field is present in the pinned v0.11.0 official GraphQL guide:
// the ticket provides PAY -> original ORD linkage and requested amount; the
// execution provides its own stable ID and actual PSP refunded amount.
const merchantStorePancakeRefundExecutionQuery = `query ($ref: String!) {
    refundTickets(filter: { refundTicketMerchantExternalId: { eq: $ref } }) {
        id status refundTicketMerchantExternalId
        requestedAmountDetails { amount currency }
        payment { id status onetimeOrder { id } }
    }
    refunds(filter: { refundTicketMerchantExternalId: { eq: $ref } }) {
        id status orderMerchantExternalId refundTicketMerchantExternalId
        pspAmountDetails { amount currency }
    }
}`

type merchantStoreRefundQueryAmount struct {
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
}

type merchantStorePancakeRefundQueryTicket struct {
	ID                             string                         `json:"id"`
	Status                         string                         `json:"status"`
	RefundTicketMerchantExternalID string                         `json:"refundTicketMerchantExternalId"`
	RequestedAmountDetails         merchantStoreRefundQueryAmount `json:"requestedAmountDetails"`
	Payment                        struct {
		ID           string `json:"id"`
		Status       string `json:"status"`
		OnetimeOrder struct {
			ID string `json:"id"`
		} `json:"onetimeOrder"`
	} `json:"payment"`
}

type merchantStorePancakeRefundQueryExecution struct {
	ID                             string                         `json:"id"`
	Status                         string                         `json:"status"`
	OrderMerchantExternalID        string                         `json:"orderMerchantExternalId"`
	RefundTicketMerchantExternalID string                         `json:"refundTicketMerchantExternalId"`
	PSPAmountDetails               merchantStoreRefundQueryAmount `json:"pspAmountDetails"`
}

type merchantStorePancakeRefundQueryData struct {
	RefundTickets []merchantStorePancakeRefundQueryTicket    `json:"refundTickets"`
	Refunds       []merchantStorePancakeRefundQueryExecution `json:"refunds"`
}

// QueryMerchantStorePancakeRefund is read-only at the provider. It uses only
// this order's encrypted gateway snapshot, never current merchant/root options.
// GraphQL errors (even alongside plausible data), timeouts and an empty lookup
// remain unknown; none authorizes another create-ticket POST.
func QueryMerchantStorePancakeRefund(ctx context.Context, order *model.MerchantStoreOrder, request MerchantStoreRefundNativeRequest) (MerchantStoreRefundProviderResult, error) {
	result := MerchantStoreRefundProviderResult{State: MerchantStoreRefundProviderUnknown, Code: "provider_query_unavailable"}
	frozen, err := merchantStoreRefundValidateRequest(order, request)
	if err != nil {
		return result, err
	}
	if ctx == nil || ctx.Err() != nil || (order.PaymentMethod != MerchantStorePlatformPancake && order.PaymentMethod != MerchantStoreExternalPancake) {
		return result, ErrMerchantStoreRefundProvider
	}
	client, err := newMerchantStorePancakeClient(frozen.Config, order.ID)
	if err != nil {
		return result, ErrMerchantStoreRefundProvider
	}
	return merchantStoreQueryPancakeRefundWithClient(ctx, order, request, client)
}

func merchantStoreQueryPancakeRefundWithClient(ctx context.Context, order *model.MerchantStoreOrder, request MerchantStoreRefundNativeRequest, client *pancake.Client) (MerchantStoreRefundProviderResult, error) {
	result := MerchantStoreRefundProviderResult{State: MerchantStoreRefundProviderUnknown, Code: "provider_query_unavailable"}
	response, err := pancake.GraphQLQuery[merchantStorePancakeRefundQueryData](ctx, client, pancake.GraphQLParams{Query: merchantStorePancakeRefundExecutionQuery, Variables: map[string]any{"ref": request.RefundID}})
	if err != nil || response == nil || len(response.Errors) != 0 {
		// Do not expose SDK/network errors, which can contain provider bodies or
		// credential-bearing URLs. A failed read is not evidence of failed funds.
		return result, ErrMerchantStoreRefundProvider
	}
	return merchantStorePancakeRefundExecutionResult(order, request, response.Data)
}

func merchantStorePancakeRefundExecutionResult(order *model.MerchantStoreOrder, request MerchantStoreRefundNativeRequest, data merchantStorePancakeRefundQueryData) (MerchantStoreRefundProviderResult, error) {
	result := MerchantStoreRefundProviderResult{State: MerchantStoreRefundProviderUnknown, Code: "refund_not_found"}
	if len(data.RefundTickets) == 0 && len(data.Refunds) == 0 {
		return result, nil
	}
	// Duplicated business references cannot safely be collapsed to the first
	// payment/refund. Keep the durable request held for reconciliation.
	if len(data.RefundTickets) != 1 || len(data.Refunds) > 1 {
		result.Code = "ambiguous_provider_refund"
		return result, ErrMerchantStoreRefundProvider
	}
	ticket := data.RefundTickets[0]
	minor, err := merchantStoreMoneyToMinor(ticket.RequestedAmountDetails.Amount)
	if err != nil || minor != request.AmountMinor || ticket.RequestedAmountDetails.Currency != request.Currency || !merchantStorePancakeShortID(ticket.ID, "TKT") || ticket.RefundTicketMerchantExternalID != request.RefundID || ticket.Payment.ID != request.Payment.PaymentReference || ticket.Payment.Status != "succeeded" || ticket.Payment.OnetimeOrder.ID != request.Payment.ReceiptReference {
		result.Code = "provider_payment_mismatch"
		return result, ErrMerchantStoreRefundProvider
	}
	result.TicketReference = ticket.ID
	if len(data.Refunds) == 0 {
		switch ticket.Status {
		case "pending", "under_review", "returned":
			result.State, result.Code = MerchantStoreRefundProviderRequested, "ticket_requested"
		case "approved", "processing", "succeeded":
			result.State, result.Code = MerchantStoreRefundProviderPending, "execution_proof_required"
		case "rejected", "failed", "cancelled":
			result.State, result.Code = MerchantStoreRefundProviderFailed, "ticket_failed"
		default:
			result.Code = "unknown_ticket_status"
		}
		return result, nil
	}
	execution := data.Refunds[0]
	minor, err = merchantStoreMoneyToMinor(execution.PSPAmountDetails.Amount)
	zeroFailed := execution.Status == "failed" && merchantStoreMoneyPattern.MatchString(execution.PSPAmountDetails.Amount) && strings.Trim(execution.PSPAmountDetails.Amount, "0.") == ""
	if (!zeroFailed && (err != nil || minor != request.AmountMinor)) || execution.PSPAmountDetails.Currency != request.Currency || !merchantStoreProviderIDPattern.MatchString(execution.ID) || execution.OrderMerchantExternalID != order.TradeNo || execution.RefundTicketMerchantExternalID != request.RefundID {
		result.Code = "provider_execution_mismatch"
		return result, ErrMerchantStoreRefundProvider
	}
	if execution.Status == "failed" {
		if ticket.Status == "succeeded" {
			result.Code = "conflicting_provider_status"
			return result, ErrMerchantStoreRefundProvider
		}
		facts, err := json.Marshal([]any{request.Payment.EvidenceHash, request.Payment.ReceiptReference, request.Payment.PaymentReference, execution.ID, request.AmountMinor, request.Currency, order.TradeNo, request.RefundID, "failed"})
		if err != nil {
			return result, ErrMerchantStoreRefundProvider
		}
		result.State, result.Code = MerchantStoreRefundProviderFailed, "provider_execution_failed"
		result.failure = &MerchantStoreRefundVerifiedFailureEvidence{PaymentReference: request.Payment.PaymentReference, RefundReference: execution.ID, RequestedAmountMinor: request.AmountMinor, Currency: request.Currency, EvidenceHash: merchantStoreRefundEvidenceHash("pancake-refund-execution-failed-v1", order, request.RefundID, facts)}
		return result, nil
	}
	if execution.Status != "succeeded" {
		result.Code = "unknown_execution_status"
		return result, nil
	}
	if ticket.Status == "failed" || ticket.Status == "rejected" || ticket.Status == "cancelled" || ticket.Status == "returned" {
		result.Code = "conflicting_provider_status"
		return result, ErrMerchantStoreRefundProvider
	}
	// Provider ticket status can change after a PSP refund succeeds. Keep the
	// financial evidence identity stable for local reconciliation retries.
	facts, err := json.Marshal([]any{request.Payment.EvidenceHash, request.Payment.ReceiptReference, request.Payment.PaymentReference, execution.ID, request.AmountMinor, request.Currency, order.TradeNo, request.RefundID})
	if err != nil {
		return result, ErrMerchantStoreRefundProvider
	}
	result.State, result.Code = MerchantStoreRefundProviderSucceeded, "verified_provider_execution"
	result.evidence = &MerchantStoreRefundVerifiedNativeEvidence{
		PaymentReference: request.Payment.PaymentReference, RefundReference: execution.ID,
		AmountMinor: request.AmountMinor, Currency: request.Currency,
		EvidenceHash: merchantStoreRefundEvidenceHash("pancake-refund-execution-v1", order, request.RefundID, facts),
	}
	return result, nil
}
