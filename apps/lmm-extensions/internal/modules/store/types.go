// Package store owns commerce data, never core balances or credentials.
package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
)

var (
	ErrInvalid            = errors.New("invalid_request")
	ErrUnauthorized       = errors.New("unauthorized")
	ErrForbidden          = errors.New("forbidden")
	ErrNotFound           = errors.New("not_found")
	ErrConflict           = errors.New("state_or_idempotency_conflict")
	ErrUnavailable        = errors.New("dependency_unavailable")
	ErrPaymentUnavailable = errors.New("payments_unavailable")
	ErrPending            = errors.New("money_result_pending")
	ErrSoldOut            = errors.New("stock_or_quota_exhausted")
)

type Account struct {
	Kind string `json:"kind"`
	ID   int64  `json:"id"`
}

func (a Account) Valid() bool { return a.ID > 0 && (a.Kind == "personal" || a.Kind == "team") }

type Permission string

const (
	Read   Permission = "read"
	Manage Permission = "manage"
	Spend  Permission = "spend"
)

// Authority must query Rust on every call. Platform level is not shop ownership.
// Return the verified acting user ID. Never trust an ID or role from an HTTP header.
type Authority interface {
	Check(context.Context, string, Account, Permission) (int64, error)
}

type Price struct {
	Amount   int64  `json:"amount_minor"`
	Currency string `json:"currency"`
}

func (p Price) valid() bool {
	if p.Amount < 0 || len(p.Currency) != 3 {
		return false
	}
	for _, r := range p.Currency {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}

type Shop struct {
	ID    string  `json:"id"`
	Owner Account `json:"owner"`
	Title string  `json:"title"`
}
type Product struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Published bool   `json:"published"`
}
type Variant struct {
	ID         string `json:"id"`
	ProductID  string `json:"product_id"`
	Title      string `json:"title"`
	Price      Price  `json:"price"`
	TrackStock bool   `json:"track_stock"`
	Stock      int64  `json:"stock"`
	Reserved   int64  `json:"reserved"`
	Quota      int64  `json:"quota"`
	Claimed    int64  `json:"claimed"`
}

// Stock includes reserved physical units. Quota is an absolute sales ceiling;
// -1 means unlimited. Lowering it below Claimed stops new sales, not existing orders.
func (v *Variant) reserve(n int64) error {
	if n <= 0 || v.Reserved > math.MaxInt64-n || v.Claimed > math.MaxInt64-n {
		return ErrInvalid
	}
	if (v.TrackStock && v.Stock-v.Reserved < n) || (v.Quota >= 0 && (v.Claimed >= v.Quota || v.Quota-v.Claimed < n)) {
		return ErrSoldOut
	}
	v.Reserved += n
	v.Claimed += n
	return nil
}
func (v *Variant) release(n int64) error {
	if v.Reserved < n || v.Claimed < n {
		return ErrConflict
	}
	v.Reserved -= n
	v.Claimed -= n
	return nil
}

type Order struct {
	ID            string  `json:"id"`
	Buyer         Account `json:"buyer"`
	Seller        Account `json:"seller"`
	CreatedBy     int64   `json:"created_by"`
	VariantID     string  `json:"variant_id"`
	Title         string  `json:"title"`
	ProductTitle  string  `json:"product_title"`
	Quantity      int64   `json:"quantity"`
	UnitPrice     Price   `json:"unit_price"`
	Total         Price   `json:"total"`
	State         string  `json:"state"`
	RequestHash   string  `json:"request_hash"`
	Delivered     bool    `json:"delivered"`
	Delivery      string  `json:"delivery,omitempty"`
	RefundReason  string  `json:"refund_reason,omitempty"`
	RefundState   string  `json:"refund_state,omitempty"`
	PaymentRef    string  `json:"payment_ref,omitempty"`
	SettlementRef string  `json:"settlement_ref,omitempty"`
	RefundRef     string  `json:"refund_ref,omitempty"`
}

const (
	AwaitingPayment   = "awaiting_payment"
	PaymentPending    = "payment_pending"
	Paid              = "paid"
	Fulfilled         = "fulfilled"
	SettlementPending = "settlement_pending"
	Settled           = "settled"
	RefundPending     = "refund_pending"
	Refunded          = "refunded"
	Cancelled         = "cancelled"
)

// Repository serializes all writes in one shop across processes and commits the
// callback atomically. Callbacks must not perform network calls or keep Tx alive.
// The adapter must scope every lookup and list to the supplied shop ID.
type Repository interface {
	Within(context.Context, string, func(Tx) error) error
}
type Tx interface {
	Get(string, string, any) error
	Put(string, string, any) error
	List(string, string, int) ([]json.RawMessage, error)
}

func ID(parts ...any) string {
	b, _ := json.Marshal(parts)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
func Text(s string, max int) bool {
	return strings.TrimSpace(s) != "" && len(s) <= max && !strings.ContainsRune(s, 0)
}
func Key(s string) bool   { return Text(s, 128) }
func Page(limit int) bool { return limit > 0 && limit <= 100 }

type Service struct {
	repo  Repository
	auth  Authority
	funds Funds
}

func New(repo Repository, auth Authority, funds Funds) (*Service, error) {
	if Missing(repo) || Missing(auth) {
		return nil, ErrInvalid
	}
	if Missing(funds) {
		funds = DisabledFunds{}
	}
	return &Service{repo: repo, auth: auth, funds: funds}, nil
}
func (s *Service) check(ctx context.Context, token string, account Account, permission Permission) (int64, error) {
	if !account.Valid() {
		return 0, ErrInvalid
	}
	id, err := s.auth.Check(ctx, token, account, permission)
	if err != nil {
		return 0, err
	}
	if id <= 0 {
		return 0, ErrForbidden
	}
	return id, nil
}

// Owner and Parties are read-only in-process ports for the support module.
// Do not expose them as unauthenticated HTTP endpoints.
func (s *Service) Owner(ctx context.Context, shopID string) (Account, error) {
	var shop Shop
	err := s.repo.Within(ctx, shopID, func(tx Tx) error { return tx.Get("shops", shopID, &shop) })
	return shop.Owner, err
}
func (s *Service) Parties(ctx context.Context, shopID, orderID string) (Account, Account, error) {
	var o Order
	err := s.repo.Within(ctx, shopID, func(tx Tx) error { return tx.Get("orders", orderID, &o) })
	return o.Buyer, o.Seller, err
}
func (s *Service) GetOrder(ctx context.Context, token, shopID, orderID string) (Order, error) {
	var o Order
	err := s.repo.Within(ctx, shopID, func(tx Tx) error { return tx.Get("orders", orderID, &o) })
	if err != nil {
		return Order{}, err
	}
	if _, err = s.check(ctx, token, o.Buyer, Read); err != nil {
		if !errors.Is(err, ErrForbidden) {
			return Order{}, err
		}
		if _, err = s.check(ctx, token, o.Seller, Manage); err != nil {
			return Order{}, err
		}
	}
	return o, nil
}

// Missing also rejects a typed nil held in a dependency interface.
func Missing(v any) bool {
	if v == nil {
		return true
	}
	r := reflect.ValueOf(v)
	switch r.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan:
		return r.IsNil()
	}
	return false
}
