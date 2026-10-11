package pgtest

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/store"
)

type revocableAuthority struct{ revoked atomic.Bool }

func (a *revocableAuthority) Check(ctx context.Context, token string, account store.Account, p store.Permission) (int64, error) {
	if a.revoked.Load() {
		return 0, store.ErrForbidden
	}
	return legacyAuthority{}.Check(ctx, token, account, p)
}
func (a *revocableAuthority) CheckAdmin(ctx context.Context, token string, root bool) (int64, error) {
	return legacyAuthority{}.CheckAdmin(ctx, token, root)
}

// This port models durable Core receipt semantics, including a committed result
// whose ACK was lost. Real Rust commerce coverage is a separate joint test.
type recoveryPort struct {
	mu                            sync.Mutex
	receipts                      map[string]store.Receipt
	payEffects, captureEffects    int
	losePay, loseCapture, unknown bool
	deliveryRef                   string
	checkDelivery                 func(store.MoneyCommand)
}

func (*recoveryPort) Available() bool          { return true }
func (*recoveryPort) DeliveryAvailable() bool  { return true }
func (*recoveryPort) ReconcileAvailable() bool { return true }
func (f *recoveryPort) Execute(_ context.Context, _ string, c store.MoneyCommand) (store.Receipt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.unknown {
		return store.Receipt{}, store.ErrPending
	}
	r, ok := f.receipts[c.Key]
	if !ok {
		r = store.Receipt{Key: c.Key, CommandHash: store.ID(c), Reference: "synthetic-core:" + c.Key, Outcome: "succeeded"}
		f.receipts[c.Key] = r
		f.payEffects++
	}
	if f.losePay {
		f.losePay = false
		return store.Receipt{}, store.ErrPending
	}
	return r, nil
}
func (f *recoveryPort) Reconcile(_ context.Context, c store.MoneyCommand) (store.Receipt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.receipts[c.Key]
	if !ok {
		return store.Receipt{}, store.ErrPending
	}
	if r.CommandHash != store.ID(c) {
		return store.Receipt{}, store.ErrConflict
	}
	return r, nil
}
func (f *recoveryPort) CompleteDelivery(_ context.Context, c store.MoneyCommand, payHash, deliveryRef string) (store.Receipt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checkDelivery(c)
	pay := f.receipts[store.ID("store-money-v1", c.ShopID, c.OrderID, "pay")]
	if pay.CommandHash != payHash || pay.Reference != c.PaymentRef || len(deliveryRef) != 64 {
		return store.Receipt{}, store.ErrConflict
	}
	r, ok := f.receipts[c.Key]
	if !ok {
		f.deliveryRef = deliveryRef
		r = store.Receipt{Key: c.Key, CommandHash: store.ID(c), Reference: "synthetic-capture:" + c.Key, Outcome: "succeeded"}
		f.receipts[c.Key] = r
		f.captureEffects++
	} else if f.deliveryRef != deliveryRef || r.CommandHash != store.ID(c) {
		return store.Receipt{}, store.ErrConflict
	}
	if f.loseCapture {
		f.loseCapture = false
		return store.Receipt{}, store.ErrPending
	}
	return r, nil
}

func TestPostgresLostAcknowledgmentRecoveryWithoutUserCredential(t *testing.T) {
	db := legacyDatabase(t)
	repo, err := store.NewPostgres(db)
	must(t, err)
	auth := &revocableAuthority{}
	port := &recoveryPort{receipts: map[string]store.Receipt{}, losePay: true, loseCapture: true}
	s, err := store.New(repo, auth, port)
	must(t, err)
	ctx := context.Background()
	body := func(v any) json.RawMessage { b, e := json.Marshal(v); must(t, e); return b }
	call := func(token string, r store.LegacyRequest) any {
		t.Helper()
		v, e := s.Legacy(ctx, token, store.Account{Kind: "personal", ID: map[string]int64{"seller": 1, "buyer": 2, "admin": 4}[token]}, r)
		must(t, e)
		return v
	}
	terms := call("seller", store.LegacyRequest{Action: "save_terms", Body: body(map[string]any{"content": "Synthetic delivery terms", "expected_version": ""})}).(map[string]any)
	p := call("seller", store.LegacyRequest{Action: "save_product", Body: body(map[string]any{"title": "Synthetic Recovery", "description": "Recovery fixture", "price_quota": 500001, "template": "fixed-content", "fixed_content": "Synthetic delivered secret", "delivery_strategy": "sequential", "payment_methods": []string{"balance"}, "pickup_login_required": true})}).(map[string]any)
	id := p["id"].(string)
	call("seller", store.LegacyRequest{Action: "submit_product", ID: id})
	call("admin", store.LegacyRequest{Action: "review_product", ID: id, Body: body(map[string]any{"approve": true})})
	row, err := s.LocateProduct(ctx, id)
	must(t, err)
	o, err := s.Purchase(ctx, "buyer", store.Purchase{ShopID: row.ShopID, VariantID: row.Product.Legacy.DefaultVariantID, Buyer: store.Account{Kind: "personal", ID: 2}, Quantity: 1, Key: "synthetic-lost-pay", Policy: &store.LegacyCheckoutPolicy{Version: terms["version"].(string), Accepted: true}})
	must(t, err)
	_, err = s.ChangeOrder(ctx, "buyer", store.OrderChange{ShopID: row.ShopID, OrderID: o.ID, Action: "pay"})
	if !errors.Is(err, store.ErrPending) {
		t.Fatalf("lost ACK should be pending: %v", err)
	}
	auth.revoked.Store(true)
	port.checkDelivery = func(c store.MoneyCommand) {
		t.Helper()
		var raw []byte
		must(t, db.QueryRow(`SELECT body FROM lmm_store.orders WHERE shop_id=$1 AND id=$2`, c.ShopID, c.OrderID).Scan(&raw))
		var current store.Order
		must(t, json.Unmarshal(raw, &current))
		if !current.Delivered || current.Delivery != "Synthetic delivered secret" || current.State != store.SettlementPending {
			t.Fatal("capture happened before durable delivery")
		}
	}
	restarted, err := store.New(repo, auth, port)
	must(t, err)
	must(t, restarted.RecoverDeliveries(ctx))
	current, err := repo.FindOrder(ctx, o.ID)
	must(t, err)
	if current.Order.State != store.SettlementPending || !current.Order.Delivered {
		t.Fatal("capture ACK loss did not retain delivery")
	}
	restarted, err = store.New(repo, auth, port)
	must(t, err)
	must(t, restarted.RecoverDeliveries(ctx))
	current, err = repo.FindOrder(ctx, o.ID)
	must(t, err)
	if current.Order.State != store.Settled || port.payEffects != 1 || port.captureEffects != 1 {
		t.Fatalf("recovery duplicated or lost money: %s pay=%d capture=%d", current.Order.State, port.payEffects, port.captureEffects)
	}
	var variant store.Variant
	must(t, repo.Within(ctx, row.ShopID, func(tx store.Tx) error { return tx.Get("variants", o.VariantID, &variant) }))
	if variant.Reserved != 0 || variant.Claimed != 1 {
		t.Fatal("delivery inventory not exactly consumed once")
	}
	// A local pending command with no authoritative Core receipt must never be
	// converted into a new service-authorized payment or delivery after logout.
	auth.revoked.Store(false)
	port.unknown = true
	unknown, err := s.Purchase(ctx, "buyer", store.Purchase{ShopID: row.ShopID, VariantID: o.VariantID, Buyer: o.Buyer, Quantity: 1, Key: "synthetic-unknown", Policy: &store.LegacyCheckoutPolicy{Version: terms["version"].(string), Accepted: true}})
	must(t, err)
	_, err = s.ChangeOrder(ctx, "buyer", store.OrderChange{ShopID: row.ShopID, OrderID: unknown.ID, Action: "pay"})
	if !errors.Is(err, store.ErrPending) {
		t.Fatal(err)
	}
	auth.revoked.Store(true)
	must(t, restarted.RecoverDeliveries(ctx))
	unchanged, err := repo.FindOrder(ctx, unknown.ID)
	must(t, err)
	if unchanged.Order.State != store.PaymentPending || unchanged.Order.Delivered || port.payEffects != 1 || port.captureEffects != 1 {
		t.Fatal("UNKNOWN became a payment")
	}
}
