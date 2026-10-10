package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/model"
	pancake "github.com/waffo-com/waffo-pancake-sdk-go"
)

// These are provider observations, not the durable refund model's decision
// states. In particular, accepting a refund ticket is not a completed refund.
type MerchantStoreRefundProviderState string

const (
	MerchantStoreRefundProviderRequested MerchantStoreRefundProviderState = "refund_requested"
	MerchantStoreRefundProviderPending   MerchantStoreRefundProviderState = "pending"
	MerchantStoreRefundProviderSucceeded MerchantStoreRefundProviderState = "succeeded"
	MerchantStoreRefundProviderFailed    MerchantStoreRefundProviderState = "failed"
	MerchantStoreRefundProviderUnknown   MerchantStoreRefundProviderState = "unknown"
)

var (
	ErrMerchantStoreRefundBasisUnavailable = errors.New("merchant store original payment evidence is unavailable")
	ErrMerchantStoreRefundProvider         = errors.New("merchant store refund provider evidence is invalid")
)

// Capabilities describe the documented provider contract. They do not enable
// dispatch: the caller still needs a verified payment basis, an approved durable
// refund and a single-flight dispatch claim before a money-moving HTTP request.
type MerchantStoreRefundProviderSupport struct {
	Submission           string `json:"submission"`
	Partial              string `json:"partial"`
	ExecutionQuery       bool   `json:"execution_query"`
	SignedCallback       bool   `json:"signed_callback"`
	RequiresPaymentBasis bool   `json:"requires_payment_basis"`
}

func MerchantStoreRefundProviderCapabilities(method string) MerchantStoreRefundProviderSupport {
	switch method {
	case MerchantStoreBalance:
		return MerchantStoreRefundProviderSupport{Submission: "balance", Partial: "supported"}
	case MerchantStorePlatformPancake, MerchantStoreExternalPancake:
		return MerchantStoreRefundProviderSupport{Submission: "refund_ticket", Partial: "conditional", ExecutionQuery: true, SignedCallback: true, RequiresPaymentBasis: true}
	case MerchantStorePlatformLinuxDO:
		return MerchantStoreRefundProviderSupport{Submission: "full_refund", Partial: "unsupported", RequiresPaymentBasis: true}
	default:
		// Epay is a payment protocol, not a universal refund API. A merchant's
		// arbitrary Epay host must not acquire Linux DO capabilities by name.
		return MerchantStoreRefundProviderSupport{Submission: "manual", Partial: "unknown", RequiresPaymentBasis: true}
	}
}

// NativePayment must come from the model's immutable, verified payment-basis
// row. AmountMinor is what the channel actually charged, including collected
// tax, not the product quote or the provider's list-price snapshot. All current
// merchant store currency contracts use 100 minor units per display unit.
type MerchantStoreRefundNativePayment struct {
	ReceiptReference string `json:"-"`
	PaymentReference string `json:"-"`
	AmountMinor      int64  `json:"-"`
	Currency         string `json:"-"`
	EvidenceHash     string `json:"-"`
}

// The refund core freezes the requested native amount, including any partial
// allocation. This adapter never computes it from credits or the current FX.
type MerchantStoreRefundNativeRequest struct {
	RefundID    string                           `json:"-"`
	OrderID     string                           `json:"-"`
	AmountMinor int64                            `json:"-"`
	Currency    string                           `json:"-"`
	Payment     MerchantStoreRefundNativePayment `json:"-"`
}

// VerifiedNativeEvidence is only available through VerifiedEvidence on a
// result produced by a provider verifier. It is server-only model input.
type MerchantStoreRefundVerifiedNativeEvidence struct {
	PaymentReference string `json:"-"`
	RefundReference  string `json:"-"`
	AmountMinor      int64  `json:"-"`
	Currency         string `json:"-"`
	EvidenceHash     string `json:"-"`
}

type MerchantStoreRefundVerifiedFailureEvidence struct {
	PaymentReference     string `json:"-"`
	RefundReference      string `json:"-"`
	RequestedAmountMinor int64  `json:"-"`
	Currency             string `json:"-"`
	EvidenceHash         string `json:"-"`
}

type MerchantStoreRefundProviderResult struct {
	State           MerchantStoreRefundProviderState `json:"state"`
	Code            string                           `json:"code"`
	TicketReference string                           `json:"-"`
	evidence        *MerchantStoreRefundVerifiedNativeEvidence
	failure         *MerchantStoreRefundVerifiedFailureEvidence
}

func (result MerchantStoreRefundProviderResult) VerifiedFailure() (MerchantStoreRefundVerifiedFailureEvidence, bool) {
	if result.State != MerchantStoreRefundProviderFailed || result.failure == nil {
		return MerchantStoreRefundVerifiedFailureEvidence{}, false
	}
	return *result.failure, true
}

func (result MerchantStoreRefundProviderResult) VerifiedEvidence() (MerchantStoreRefundVerifiedNativeEvidence, bool) {
	if result.State != MerchantStoreRefundProviderSucceeded || result.evidence == nil {
		return MerchantStoreRefundVerifiedNativeEvidence{}, false
	}
	return *result.evidence, true
}

// PaymentProof cannot be constructed from a caller-supplied JSON DTO. A signed
// original payment verifier below is its only producer in this package.
type MerchantStoreRefundPaymentProof struct {
	payment MerchantStoreRefundNativePayment
}

func (proof *MerchantStoreRefundPaymentProof) VerifiedPayment() (MerchantStoreRefundNativePayment, bool) {
	if proof == nil || proof.payment.EvidenceHash == "" {
		return MerchantStoreRefundNativePayment{}, false
	}
	return proof.payment, true
}

func merchantStoreRefundHasHash(value string) bool {
	if len(value) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

func merchantStoreRefundEvidenceHash(kind string, order *model.MerchantStoreOrder, refundID string, payload []byte) string {
	context, _ := json.Marshal([]string{kind, order.ID, order.PaymentMethod, order.PaymentScopeHash, refundID})
	digest := sha256.New()
	digest.Write(context)
	digest.Write([]byte{0})
	digest.Write(payload)
	return hex.EncodeToString(digest.Sum(nil))
}

func merchantStoreRefundPaymentOrder(order *model.MerchantStoreOrder) bool {
	return order != nil && order.ID != "" && order.ProviderTradeID != "" &&
		(order.Status == "paid" || order.Status == "refund_pending" || order.Status == "refunded")
}

func merchantStoreRefundValidateRequest(order *model.MerchantStoreOrder, request MerchantStoreRefundNativeRequest) (merchantStorePaymentContext, error) {
	var frozen merchantStorePaymentContext
	if !merchantStoreRefundPaymentOrder(order) || request.OrderID != order.ID || !merchantStoreProviderIDPattern.MatchString(request.RefundID) {
		return frozen, ErrMerchantStoreRefundProvider
	}
	if request.Payment.AmountMinor <= 0 || !merchantStoreRefundHasHash(request.Payment.EvidenceHash) || request.Payment.PaymentReference == "" {
		return frozen, ErrMerchantStoreRefundBasisUnavailable
	}
	if request.AmountMinor <= 0 || request.AmountMinor > request.Payment.AmountMinor || request.Payment.ReceiptReference != order.ProviderTradeID || request.Currency != order.Currency || request.Payment.Currency != request.Currency {
		return frozen, ErrMerchantStoreRefundProvider
	}
	// Keep the stored int64 and the provider display string in the same exact
	// supported range; no float conversion or overflowing high-value product.
	for _, minor := range []int64{request.AmountMinor, request.Payment.AmountMinor} {
		parsed, err := merchantStoreMoneyToMinor(merchantStoreMinorMoney(minor))
		if err != nil || parsed != minor {
			return frozen, ErrMerchantStoreRefundProvider
		}
	}
	frozen, err := loadMerchantStorePaymentContext(order)
	if err != nil {
		return frozen, err
	}
	if (order.PaymentMethod == MerchantStoreExternalPancake || order.PaymentMethod == MerchantStorePlatformPancake) && !merchantStorePancakeShortID(request.Payment.PaymentReference, "PAY") {
		return frozen, ErrMerchantStoreRefundProvider
	}
	return frozen, nil
}

// BuildMerchantStorePancakeRefundTicket does not send a request. The execution
// owner must durably claim a single dispatch before using these parameters.
// The pinned v0.11.0 customer SDK provides no idempotency-key guarantee; an
// ambiguous first dispatch is queried using this same immutable refund ID.
func BuildMerchantStorePancakeRefundTicket(order *model.MerchantStoreOrder, request MerchantStoreRefundNativeRequest, reason string) (pancake.CreateRefundTicketParams, error) {
	var params pancake.CreateRefundTicketParams
	_, err := merchantStoreRefundValidateRequest(order, request)
	if err != nil {
		return params, err
	}
	if order.PaymentMethod != MerchantStoreExternalPancake && order.PaymentMethod != MerchantStorePlatformPancake {
		return params, ErrMerchantStoreRefundProvider
	}
	if strings.TrimSpace(reason) == "" || len(reason) > 4000 || strings.ContainsRune(reason, '\x00') {
		return params, ErrMerchantStoreRefundProvider
	}
	return pancake.CreateRefundTicketParams{
		PaymentID: request.Payment.PaymentReference, Reason: reason,
		RequestedAmount:                pancake.RequestedAmount{Amount: merchantStoreMinorMoney(request.AmountMinor), Currency: request.Currency},
		RefundTicketMerchantExternalID: &request.RefundID,
		Metadata:                       map[string]any{"lmm_store_order_id": order.ID, "lmm_store_refund_id": request.RefundID},
	}, nil
}

// Ticket observations never carry completed-refund evidence, even if the
// ticket itself says succeeded. Completion requires the native PSP execution.
func ObserveMerchantStorePancakeRefundTicket(order *model.MerchantStoreOrder, request MerchantStoreRefundNativeRequest, ticket *pancake.RefundTicketResult) (MerchantStoreRefundProviderResult, error) {
	result := MerchantStoreRefundProviderResult{State: MerchantStoreRefundProviderUnknown, Code: "invalid_ticket"}
	if _, err := merchantStoreRefundValidateRequest(order, request); err != nil {
		return result, err
	}
	if order.PaymentMethod != MerchantStoreExternalPancake && order.PaymentMethod != MerchantStorePlatformPancake {
		return result, ErrMerchantStoreRefundProvider
	}
	if ticket == nil || !merchantStorePancakeShortID(ticket.Ticket.ID, "TKT") || ticket.Ticket.SubjectID != request.Payment.PaymentReference || ticket.Ticket.RefundTicketMerchantExternalID == nil || *ticket.Ticket.RefundTicketMerchantExternalID != request.RefundID || ticket.Ticket.VersionData == nil || ticket.Ticket.VersionData.RequestedAmount == nil {
		return result, ErrMerchantStoreRefundProvider
	}
	amount := ticket.Ticket.VersionData.RequestedAmount
	minor, err := merchantStoreMoneyToMinor(amount.Amount)
	if err != nil || minor != request.AmountMinor || amount.Currency != request.Currency {
		return result, ErrMerchantStoreRefundProvider
	}
	result.TicketReference = ticket.Ticket.ID
	switch ticket.Ticket.Status {
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
