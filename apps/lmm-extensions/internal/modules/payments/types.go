// Package payments owns provider orders and evidence, never core balances.
package payments

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"time"
)

var (
	ErrInvalid     = errors.New("invalid_payment_request")
	ErrConflict    = errors.New("payment_conflict")
	ErrNotFound    = errors.New("payment_not_found")
	ErrDenied      = errors.New("payment_access_denied")
	ErrUnavailable = errors.New("payment_dependency_unavailable")
	ErrUnsupported = errors.New("payment_operation_unsupported")
	ErrEvidence    = errors.New("independent_provider_evidence_required")
)

const MaxEvidence = 32 << 10 // Fits the core's 64 KiB RPC limit, with overhead.
const maxAmount int64 = 9_000_000_000_000

type Channel struct {
	ID          int64  `json:"id"`
	Provider    string `json:"provider"`
	Merchant    string `json:"merchant"`
	Environment string `json:"environment"`
	Currency    string `json:"currency"`
	Enabled     bool   `json:"enabled"` // A disabled channel still accepts existing callbacks.
}

func (c Channel) valid() bool {
	return c.ID > 0 && validID(c.Provider) && validID(c.Merchant) &&
		(c.Environment == "test" || c.Environment == "live") && validCurrency(c.Currency)
}
func (c Channel) namespace() string { return c.Provider + "/" + c.Merchant + "/" + c.Environment }
func validID(s string) bool {
	if len(s) == 0 || len(s) > 128 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '.') {
			return false
		}
	}
	return true
}
func validKey(s string) bool { return len(s) >= 16 && validID(s) }
func validCurrency(s string) bool {
	if len(s) != 3 {
		return false
	}
	for _, c := range s {
		if c < 'A' || c > 'Z' {
			return false
		}
	}
	return true
}
func validAmount(n int64) bool { return n > 0 && n <= maxAmount }
func stableKey(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(strconv.Itoa(len(p)) + ":" + p))
	}
	return "pay_" + hex.EncodeToString(h.Sum(nil))
}

type PrepareRequest struct {
	Key         string `json:"key"`
	AccountID   int64  `json:"account_id"` // Explicit Rust personal OR team account ID.
	ChannelID   int64  `json:"channel_id"`
	Currency    string `json:"currency"`
	AmountMinor int64  `json:"amount_minor"`
}
type Intent struct {
	ID          int64  `json:"id"`
	AccountID   int64  `json:"account_id"`
	CreatedBy   int64  `json:"created_by"`
	Provider    string `json:"provider"`
	Merchant    string `json:"merchant"`
	Environment string `json:"environment"`
	Currency    string `json:"currency"`
	AmountMinor int64  `json:"amount_minor"`
	CreditUnits int64  `json:"credit_units"` // An opaque frozen quote returned by Rust.
	CreditUnit  string `json:"credit_unit"`
	Key         string `json:"key"`
	RequestHash []byte `json:"request_hash"`
}

type Receipt struct {
	ID            string `json:"id"`
	IntentID      int64  `json:"intent_id"`
	Key           string `json:"key"`
	RequestHash   []byte `json:"request_hash"`
	State         string `json:"state"`
	JournalID     int64  `json:"journal_id"`
	RejectionCode string `json:"rejection_code,omitempty"`
}

const (
	CreditApplied         = "credit_applied"
	RefundHeld            = "refund_held"
	RefundCommitted       = "refund_committed"
	RefundReleased        = "refund_released"
	ExternalRefundApplied = "external_refund_applied"
	Rejected              = "rejected"
)

func receiptMatches(r Receipt, intent int64, key, state string) bool {
	if r.ID == "" || r.IntentID != intent || r.Key != key || len(r.RequestHash) != 32 {
		return false
	}
	if r.State == Rejected {
		return r.RejectionCode != ""
	}
	if r.State != state {
		return false
	}
	return r.JournalID > 0
}

// Core must recheck current user/resource authority, including on replay. A
// service credential does not grant account access or certify provider evidence.
// Provider mutations use a core-issued intent plus independently verified proof.
type Core interface {
	Prepare(context.Context, string, PrepareRequest) (Intent, error)
	Read(context.Context, string, Intent) error
	Credit(context.Context, int64, string, Evidence) (Receipt, error)
	Hold(context.Context, string, int64, string, int64) (Receipt, error)
	Commit(context.Context, int64, string, string, Evidence) (Receipt, error)
	Release(context.Context, int64, string, string, Evidence) (Receipt, error)
	ExternalRefund(context.Context, int64, string, Evidence) (Receipt, error)
	Receipt(context.Context, int64, string) (Receipt, error)
}

type Evidence struct {
	Provider    string `json:"provider"`
	Merchant    string `json:"merchant"`
	Environment string `json:"environment"`
	Transaction string `json:"transaction"`
	EventID     string `json:"event_id"`
	Payload     []byte `json:"payload"`
	Signature   []byte `json:"signature"`
	Source      string `json:"source"` // webhook or api; api is NOT signed webhook evidence.
}
type Event struct {
	Key           string    `json:"key"`
	ChannelID     int64     `json:"channel_id"`
	OrderID       string    `json:"order_id"`
	Type          string    `json:"type"`
	Currency      string    `json:"currency"`
	AmountMinor   int64     `json:"amount_minor"`
	RefundID      string    `json:"refund_id,omitempty"`
	LocalRefundID string    `json:"local_refund_id,omitempty"`
	Evidence      Evidence  `json:"evidence"`
	Digest        string    `json:"digest"`
	Status        string    `json:"status"`
	ErrorCode     string    `json:"error_code,omitempty"`
	NextAttempt   time.Time `json:"next_attempt"`
}

const (
	Paid            = "paid"
	PaymentFailed   = "payment_failed"
	RefundSucceeded = "refund_succeeded"
	RefundFailed    = "refund_failed"
	RefundPending   = "refund_pending"
)

type Checkout struct {
	ID        string `json:"id"`
	URL       string `json:"url"`
	PaymentID string `json:"payment_id,omitempty"`
}
type RefundResult struct {
	ID     string
	Status string // Informational provider status. Never a core receipt.
}

// Adapter configuration is server-owned and immutable per channel ID. Do not
// reuse a channel ID for another merchant or environment during rotation.
type Adapter interface {
	Channel() Channel
	Checkout(context.Context, Order, string) (Checkout, error)
	Verify(*http.Request, time.Time) (Event, error)
	Lookup(context.Context, Order) ([]Event, error)
	Refund(context.Context, Order, Refund, string) (RefundResult, error)
	RefundEnabled() bool
	PartialRefunds() bool
	ReplayWindow(operation string) time.Duration // Zero means NEVER retry an ambiguous provider write.
}

type Refund struct {
	ID            string `json:"id"`
	AmountMinor   int64  `json:"amount_minor"`
	ProviderID    string `json:"provider_id,omitempty"`
	HoldKey       string `json:"hold_key"`
	HoldReceipt   string `json:"hold_receipt,omitempty"`
	State         string `json:"state"`
	ProviderState string `json:"provider_state,omitempty"`
}
type Job struct {
	Key          string    `json:"key"`
	Kind         string    `json:"kind"`
	RefundID     string    `json:"refund_id,omitempty"`
	Evidence     Evidence  `json:"evidence"`
	State        string    `json:"state"`
	Attempts     int       `json:"attempts"`
	FirstAttempt time.Time `json:"first_attempt"`
	NextAttempt  time.Time `json:"next_attempt"`
	Lease        string    `json:"lease,omitempty"`
	LeaseUntil   time.Time `json:"lease_until"`
	ErrorCode    string    `json:"error_code,omitempty"`
	Receipt      *Receipt  `json:"receipt,omitempty"`
}
type Order struct {
	ID            string            `json:"id"`
	ChannelID     int64             `json:"channel_id"`
	Namespace     string            `json:"namespace"`
	Intent        Intent            `json:"intent"`
	Checkout      Checkout          `json:"checkout"`
	PaymentID     string            `json:"payment_id,omitempty"`
	ProviderPaid  bool              `json:"provider_paid"`
	CreditState   string            `json:"credit_state"`
	CreditReceipt string            `json:"credit_receipt,omitempty"`
	RefundFence   bool              `json:"refund_fence"`
	Refunds       map[string]Refund `json:"refunds"`
	Jobs          map[string]Job    `json:"jobs"`
	Version       int64             `json:"version"`
	ErrorCode     string            `json:"error_code,omitempty"`
}

// Credited says only that Rust durably posted the original credit. It does NOT
// expose a spendable balance. A refund may have removed some or all of it.
func (o Order) Credited() bool { return o.CreditState == "confirmed" && o.CreditReceipt != "" }
func (o Order) State() string {
	if o.ErrorCode != "" {
		return "review"
	}
	var refunded int64
	pending := false
	for _, r := range o.Refunds {
		if r.State == "committed" {
			refunded += r.AmountMinor
		} else if r.State != "released" && r.State != "rejected" {
			pending = true
		}
	}
	if refunded == o.Intent.AmountMinor {
		return "refunded"
	}
	if pending || o.RefundFence && !o.Credited() {
		return "refund_pending"
	}
	if refunded > 0 {
		return "partially_refunded"
	}
	if o.Credited() {
		return "credited"
	}
	if o.ProviderPaid {
		return "credit_pending"
	}
	if o.Checkout.ID == "" {
		return "checkout_pending"
	}
	return "awaiting_payment"
}
func (o Order) due() time.Time {
	var due time.Time
	for _, j := range o.Jobs {
		if j.State != "pending" && j.State != "running" {
			continue
		}
		t := j.NextAttempt
		if j.State == "running" && j.LeaseUntil.After(t) {
			t = j.LeaseUntil
		}
		if due.IsZero() || t.Before(due) {
			due = t
		}
	}
	return due
}

// Store must isolate Mutate by order and roll back on callback errors. No core
// or provider call is made inside Mutate. PutEvent deduplicates across all
// orders in a provider/merchant/environment namespace, not just by order ID.
type Store interface {
	Insert(context.Context, Order) error
	Get(context.Context, string) (Order, error)
	FindPayment(context.Context, string, string) (Order, error)
	Mutate(context.Context, string, func(*Order) error) error
	PutEvent(context.Context, Event) error
	Events(context.Context, time.Time, int) ([]Event, error)
	FinishEvent(context.Context, string, string, string, time.Time) error
	DueOrders(context.Context, time.Time, int) ([]string, error)
}

func errorCode(err error) string {
	for _, e := range []error{ErrInvalid, ErrConflict, ErrNotFound, ErrDenied, ErrUnsupported, ErrEvidence} {
		if errors.Is(err, e) {
			return e.Error()
		}
	}
	return ErrUnavailable.Error() // Never persist credential-bearing HTTP/SQL errors.
}
func eventDigest(e Event) string {
	// Ignore delivery signatures/timestamps. Stripe re-signs the same payload on
	// redelivery; ePay may reorder query parameters. Compare normalized meaning.
	return stableKey(e.Type, e.OrderID, e.Currency, strconv.FormatInt(e.AmountMinor, 10), e.RefundID, e.LocalRefundID, e.Evidence.Transaction)
}
func eventKey(e Event) string {
	return stableKey(e.Evidence.Provider, e.Evidence.Merchant, e.Evidence.Environment, e.Evidence.EventID)
}
func sameChannel(e Evidence, c Channel) bool {
	return e.Provider == c.Provider && e.Merchant == c.Merchant && e.Environment == c.Environment
}
func eventHash(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
