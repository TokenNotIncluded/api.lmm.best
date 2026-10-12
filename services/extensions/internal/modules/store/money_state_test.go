// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

// The funds port is a synthetic receipt source. These tests do not prove that
// Core posted funds, that PostgreSQL committed, or that an OS process restarted.
type a06StateFunds struct {
	execCalls    atomic.Int64
	readCalls    atomic.Int64
	captureCalls atomic.Int64
	execute      func(MoneyCommand) (Receipt, error)
	query        func(MoneyCommand) (Receipt, error)
	capture      func(MoneyCommand) (Receipt, error)
}

func (*a06StateFunds) Available() bool          { return true }
func (*a06StateFunds) ReconcileAvailable() bool { return true }
func (*a06StateFunds) DeliveryAvailable() bool  { return true }
func a06StateReceipt(c MoneyCommand) Receipt {
	return Receipt{Key: c.Key, CommandHash: ID(c), Reference: "synthetic:" + c.Key, Outcome: "succeeded"}
}
func (f *a06StateFunds) Execute(ctx context.Context, _ string, c MoneyCommand) (Receipt, error) {
	f.execCalls.Add(1)
	if err := ctx.Err(); err != nil {
		return Receipt{}, err
	}
	if f.execute != nil {
		return f.execute(c)
	}
	return a06StateReceipt(c), nil
}
func (f *a06StateFunds) Reconcile(ctx context.Context, c MoneyCommand) (Receipt, error) {
	f.readCalls.Add(1)
	if err := ctx.Err(); err != nil {
		return Receipt{}, err
	}
	if f.query != nil {
		return f.query(c)
	}
	return a06StateReceipt(c), nil
}
func (f *a06StateFunds) CompleteDelivery(ctx context.Context, c MoneyCommand, _, _ string) (Receipt, error) {
	f.captureCalls.Add(1)
	if err := ctx.Err(); err != nil {
		return Receipt{}, err
	}
	if f.capture != nil {
		return f.capture(c)
	}
	return a06StateReceipt(c), nil
}

type a06StateFixture struct {
	s     *Service
	repo  *memoryRepo
	funds *a06StateFunds
	auth  *authority
	shop  string
	id    string
}

func a06StateSetup(t *testing.T) *a06StateFixture {
	t.Helper()
	f := &a06StateFixture{repo: newRepo(), funds: &a06StateFunds{}, auth: &authority{}, shop: ID("state-shop"), id: ID("state-order")}
	var err error
	f.s, err = New(f.repo, f.auth, f.funds)
	must(t, err)
	o := Order{ID: f.id, Buyer: Account{"personal", 2}, Seller: Account{"personal", 1}, CreatedBy: 2,
		VariantID: ID("state-variant"), Quantity: 2, UnitPrice: Price{Amount: 50, Unit: "credits"}, Total: Price{Amount: 100, Unit: "credits"},
		State: AwaitingPayment, Legacy: &LegacyOrderMetadata{ProductID: ID("state-product"), PaymentMethod: "balance", DeliveryTemplate: "fixed-content"}}
	must(t, f.repo.Within(context.Background(), f.shop, func(tx Tx) error {
		if err := tx.Put("orders", o.ID, o); err != nil {
			return err
		}
		if err := tx.Put("variants", o.VariantID, Variant{ID: o.VariantID, TrackStock: true, Stock: 10, Reserved: 2, Claimed: 2, Quota: -1}); err != nil {
			return err
		}
		return tx.Put("commands", ID("legacy-order-delivery-v1", o.ID), struct {
			Content string `json:"content"`
		}{"synthetic delivery"})
	}))
	return f
}
func (f *a06StateFixture) change(token, action, text string) (Order, error) {
	return f.s.ChangeOrder(context.Background(), token, OrderChange{ShopID: f.shop, OrderID: f.id, Action: action, Text: text})
}
func (f *a06StateFixture) order(t *testing.T) Order {
	t.Helper()
	var o Order
	must(t, f.repo.Within(context.Background(), f.shop, func(tx Tx) error { return tx.Get("orders", f.id, &o) }))
	return o
}
func (f *a06StateFixture) operation(t *testing.T, kind string) operation {
	t.Helper()
	var op operation
	must(t, f.repo.Within(context.Background(), f.shop, func(tx Tx) error { return tx.Get("money_operations", ID("store-money-v1", f.shop, f.id, kind), &op) }))
	return op
}
func (f *a06StateFixture) image(t *testing.T) string {
	t.Helper()
	f.repo.mu.Lock()
	defer f.repo.mu.Unlock()
	b, err := json.Marshal(f.repo.data)
	must(t, err)
	return string(b)
}
func (f *a06StateFixture) pendingPay(t *testing.T) {
	t.Helper()
	f.funds.execute = func(MoneyCommand) (Receipt, error) { return Receipt{}, ErrPending }
	_, err := f.change(buyerToken, "pay", "")
	wantError(t, err, ErrPending)
	f.funds.execute = nil
	f.funds.execCalls.Store(0)
}

func TestA06DeliveryChecksPaymentEconomicsBeforeStockChanges(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*Order)
	}{
		{"buyer", func(o *Order) { o.Buyer.ID = 3 }},
		{"seller", func(o *Order) { o.Seller.ID = 3 }},
		{"price", func(o *Order) { o.UnitPrice.Amount = 100; o.Total.Amount = 200 }},
		{"quantity", func(o *Order) { o.Quantity = 1; o.Total.Amount = 50 }},
		{"actor", func(o *Order) { o.CreatedBy = 3 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := a06StateSetup(t)
			_, err := f.change(buyerToken, "pay", "")
			must(t, err)
			must(t, f.repo.Within(context.Background(), f.shop, func(tx Tx) error {
				var o Order
				if err := tx.Get("orders", f.id, &o); err != nil {
					return err
				}
				tc.edit(&o)
				return tx.Put("orders", f.id, o)
			}))
			before := f.image(t)
			_, err = f.s.FinalizeDelivery(context.Background(), f.shop, f.id)
			if !errors.Is(err, ErrConflict) || f.funds.captureCalls.Load() != 0 || f.image(t) != before {
				t.Fatalf("changed economics reached delivery: error=%v capture=%d changed=%v", err, f.funds.captureCalls.Load(), f.image(t) != before)
			}
		})
	}
}

func TestA06MoneyRetryRejectsChangedPersistedCommand(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*operation)
	}{
		{"buyer", func(o *operation) { o.Command.Buyer.ID = 3 }},
		{"seller", func(o *operation) { o.Command.Seller.ID = 3 }},
		{"total", func(o *operation) { o.Command.Total.Amount++ }},
		{"quantity", func(o *operation) { o.Command.OrderSnapshot.Quantity++ }},
		{"snapshot", func(o *operation) { o.Command.OrderSnapshot = nil }},
		{"before", func(o *operation) { o.Before = Settled }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := a06StateSetup(t)
			f.pendingPay(t)
			op := f.operation(t, "pay")
			tc.edit(&op)
			must(t, f.repo.Within(context.Background(), f.shop, func(tx Tx) error { return tx.Put("money_operations", op.Command.Key, op) }))
			before := f.image(t)
			_, err := f.change(buyerToken, "pay", "")
			if !errors.Is(err, ErrConflict) || f.funds.execCalls.Load() != 0 || f.image(t) != before {
				t.Fatalf("changed operation was executed: error=%v execute=%d", err, f.funds.execCalls.Load())
			}
		})
	}
}

func TestA06ReconcileRechecksFrozenOrderAfterQuery(t *testing.T) {
	f := a06StateSetup(t)
	f.pendingPay(t)
	f.funds.query = func(c MoneyCommand) (Receipt, error) {
		err := f.repo.Within(context.Background(), f.shop, func(tx Tx) error {
			var o Order
			if err := tx.Get("orders", f.id, &o); err != nil {
				return err
			}
			o.Buyer.ID = 3
			return tx.Put("orders", f.id, o)
		})
		return a06StateReceipt(c), err
	}
	_, err := f.s.reconcileOrderMoney(context.Background(), f.shop, f.id)
	if !errors.Is(err, ErrConflict) || f.order(t).State != PaymentPending || f.operation(t, "pay").Done {
		t.Fatalf("receipt applied to changed order: error=%v state=%s", err, f.order(t).State)
	}
}

func TestA06FinishMoneyRejectsConflictingTerminalReceipt(t *testing.T) {
	for _, mode := range []string{"reference", "outcome", "before", "grant", "delivery"} {
		t.Run(mode, func(t *testing.T) {
			f := a06StateSetup(t)
			_, err := f.change(buyerToken, "pay", "")
			must(t, err)
			op := f.operation(t, "pay")
			receipt := op.Receipt
			switch mode {
			case "reference":
				receipt.Reference = "synthetic:other"
			case "outcome":
				receipt.Outcome = "rejected"
			case "before":
				op.Before = Settled
			case "grant":
				op.PaymentCommandHash = ID("other-grant")
			case "delivery":
				op.DeliveryReference = ID("other-delivery")
			}
			before := f.image(t)
			_, err = f.s.finishMoney(context.Background(), f.shop, f.id, op, receipt)
			if !errors.Is(err, ErrConflict) || f.image(t) != before {
				t.Fatalf("terminal receipt conflict: error=%v", err)
			}
		})
	}
}

func TestA06PaymentRejectionReleasesStockOnlyOnce(t *testing.T) {
	f := a06StateSetup(t)
	f.pendingPay(t)
	f.funds.query = func(c MoneyCommand) (Receipt, error) { r := a06StateReceipt(c); r.Outcome = "rejected"; return r, nil }
	_, err := f.s.reconcileOrderMoney(context.Background(), f.shop, f.id)
	wantError(t, err, ErrConflict)
	op := f.operation(t, "pay")
	before := f.image(t)
	_, err = f.s.finishMoney(context.Background(), f.shop, f.id, op, op.Receipt)
	wantError(t, err, ErrConflict)
	if f.image(t) != before || f.order(t).State != Cancelled {
		t.Fatal("duplicate rejection changed data")
	}
	var v Variant
	must(t, f.repo.Within(context.Background(), f.shop, func(tx Tx) error { return tx.Get("variants", f.orderIDVariant(), &v) }))
	if v.Stock != 10 || v.Reserved != 0 || v.Claimed != 0 {
		t.Fatalf("inventory=%+v", v)
	}
}
func (f *a06StateFixture) orderIDVariant() string { return ID("state-variant") }

func TestA06LostRefundReceiptUsesReadOnlyRecovery(t *testing.T) {
	for _, delivered := range []bool{false, true} {
		t.Run(map[bool]string{false: "undelivered", true: "delivered"}[delivered], func(t *testing.T) {
			f := a06StateSetup(t)
			_, err := f.change(buyerToken, "pay", "")
			must(t, err)
			if delivered {
				_, err = f.s.FinalizeDelivery(context.Background(), f.shop, f.id)
				must(t, err)
			}
			_, err = f.change(buyerToken, "request_refund", "synthetic refund")
			must(t, err)
			f.funds.execute = func(c MoneyCommand) (Receipt, error) { return Receipt{}, ErrPending }
			_, err = f.change(sellerToken, "refund", "")
			wantError(t, err, ErrPending)
			executes, captures := f.funds.execCalls.Load(), f.funds.captureCalls.Load()
			// Recreate only the Service; the synthetic repository remains the same.
			f.s, err = New(f.repo, f.auth, f.funds)
			must(t, err)
			o, err := f.s.reconcileOrderMoney(context.Background(), f.shop, f.id)
			must(t, err)
			if o.State != Refunded || o.Delivered != delivered {
				t.Fatalf("refund=%+v", o)
			}
			var v Variant
			must(t, f.repo.Within(context.Background(), f.shop, func(tx Tx) error { return tx.Get("variants", o.VariantID, &v) }))
			if delivered {
				if v.Stock != 8 || v.Reserved != 0 || v.Claimed != 2 {
					t.Fatal("delivered refund restocked inventory")
				}
			} else if v.Stock != 10 || v.Reserved != 0 || v.Claimed != 0 {
				t.Fatal("undelivered refund did not release reservation")
			}
			before := f.image(t)
			_, err = f.s.reconcileOrderMoney(context.Background(), f.shop, f.id)
			must(t, err)
			if f.image(t) != before || f.funds.execCalls.Load() != executes || f.funds.captureCalls.Load() != captures {
				t.Fatal("refund polling executed money or repeated inventory changes")
			}
		})
	}
}

func TestA06ConcurrentDeliveryConsumesInventoryOnce(t *testing.T) {
	f := a06StateSetup(t)
	_, err := f.change(buyerToken, "pay", "")
	must(t, err)
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := f.s.FinalizeDelivery(context.Background(), f.shop, f.id)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		must(t, err)
	}
	o := f.order(t)
	var v Variant
	must(t, f.repo.Within(context.Background(), f.shop, func(tx Tx) error { return tx.Get("variants", o.VariantID, &v) }))
	if o.State != Settled || !o.Delivered || v.Stock != 8 || v.Reserved != 0 || v.Claimed != 2 {
		t.Fatalf("order=%+v inventory=%+v", o, v)
	}
}

func TestA06InventoryCommitFailureDoesNotCapture(t *testing.T) {
	f := a06StateSetup(t)
	_, err := f.change(buyerToken, "pay", "")
	must(t, err)
	before := f.image(t)
	f.repo.failCommit.Store(true)
	_, err = f.s.FinalizeDelivery(context.Background(), f.shop, f.id)
	if err == nil || f.funds.captureCalls.Load() != 0 || f.image(t) != before {
		t.Fatal("capture or partial delivery after failed commit")
	}
	_, err = f.s.FinalizeDelivery(context.Background(), f.shop, f.id)
	must(t, err)
	if f.order(t).State != Settled {
		t.Fatal("delivery did not recover")
	}
}

func TestA06TeamPurchaseTermsBelongToActorNotAccountID(t *testing.T) {
	for _, accepted := range []bool{false, true} {
		t.Run(map[bool]string{false: "other-user-consent", true: "actor-consent"}[accepted], func(t *testing.T) {
			f := a06StateSetup(t)
			productID, variantID := ID("terms-product"), ID("terms-variant")
			version := "aebad8df-68c4-4cbc-8f03-df9a74d07413"
			must(t, f.repo.Within(context.Background(), f.shop, func(tx Tx) error {
				if err := tx.Put("shops", f.shop, Shop{ID: f.shop, Owner: Account{"personal", 3}}); err != nil {
					return err
				}
				if err := tx.Put("products", productID, Product{ID: productID, Published: true, Legacy: &LegacyProductMetadata{Status: "published", Approved: true}}); err != nil {
					return err
				}
				if err := tx.Put("variants", variantID, Variant{ID: variantID, ProductID: productID, Price: Price{Amount: 50, Unit: "credits"}, Quota: -1, Legacy: &LegacyVariantMetadata{Enabled: true, Template: "fixed-content", FixedContent: "synthetic"}}); err != nil {
					return err
				}
				if err := tx.Put("commands", "@web:seller-terms", legacySellerTerms{Version: version, Content: "Synthetic terms", UpdatedAt: 1}); err != nil {
					return err
				}
				// User 10 and team 10 are different identities. The actor is user 1.
				return tx.Put("commands", ID("legacy-seller-terms-v1", int64(10), version), legacyTermsAcceptance{Version: version, UserID: 10, AcceptedAt: 1})
			}))
			before := f.image(t)
			o, err := f.s.Purchase(context.Background(), sellerToken, Purchase{ShopID: f.shop, VariantID: variantID, Buyer: Account{"team", 10}, Quantity: 1, Key: "actor-terms", Policy: &LegacyCheckoutPolicy{Version: version, Accepted: accepted}})
			if !accepted {
				if !errors.Is(err, ErrSellerTermsRequired) || f.image(t) != before {
					t.Fatalf("reused unrelated user's consent: error=%v", err)
				}
				return
			}
			must(t, err)
			if o.CreatedBy != 1 || o.Buyer != (Account{"team", 10}) {
				t.Fatalf("actor/payer=%+v", o)
			}
			var consent legacyTermsAcceptance
			must(t, f.repo.Within(context.Background(), f.shop, func(tx Tx) error {
				return tx.Get("commands", ID("legacy-seller-terms-v1", int64(1), version), &consent)
			}))
			if consent.UserID != 1 || consent.AcceptedAt <= 0 {
				t.Fatal("consent was not stored for the acting user")
			}
		})
	}
}

func TestA06InvalidStockQuantityDoesNotChangeCounters(t *testing.T) {
	for _, quantity := range []int64{-1, 0} {
		v := Variant{Stock: 5, Reserved: 2, Claimed: 2, Quota: -1}
		before := v
		wantError(t, v.release(quantity), ErrInvalid)
		if v != before {
			t.Fatal("invalid release changed counters")
		}
	}
	for _, v := range []Variant{{Stock: 5, Reserved: -1, Claimed: 2, Quota: -1}, {Stock: 5, Reserved: 0, Claimed: -1, Quota: -1}} {
		before := v
		wantError(t, v.reserve(1), ErrInvalid)
		if v != before {
			t.Fatal("negative counter was used for a new reservation")
		}
	}
}

func TestA06LostCaptureReceiptDoesNotRepeatDelivery(t *testing.T) {
	f := a06StateSetup(t)
	_, err := f.change(buyerToken, "pay", "")
	must(t, err)
	var captured Receipt
	f.funds.capture = func(c MoneyCommand) (Receipt, error) { captured = a06StateReceipt(c); return Receipt{}, ErrPending }
	_, err = f.s.FinalizeDelivery(context.Background(), f.shop, f.id)
	wantError(t, err, ErrPending)
	if !f.order(t).Delivered || f.order(t).State != SettlementPending {
		t.Fatal("delivery was not durable before capture")
	}
	f.funds.query = func(c MoneyCommand) (Receipt, error) { return captured, nil }
	f.s, err = New(f.repo, f.auth, f.funds)
	must(t, err)
	_, err = f.s.reconcileOrderMoney(context.Background(), f.shop, f.id)
	must(t, err)
	if f.order(t).State != Settled || f.funds.captureCalls.Load() != 1 || f.funds.execCalls.Load() != 1 {
		t.Fatal("capture recovery repeated a financial call")
	}
	var v Variant
	must(t, f.repo.Within(context.Background(), f.shop, func(tx Tx) error { return tx.Get("variants", f.orderIDVariant(), &v) }))
	if v.Stock != 8 || v.Reserved != 0 || v.Claimed != 2 {
		t.Fatalf("inventory=%+v", v)
	}
}

func TestA06ReconcileFreeRefundWithoutFundsCapability(t *testing.T) {
	f := a06StateSetup(t)
	must(t, f.repo.Within(context.Background(), f.shop, func(tx Tx) error {
		var o Order
		if err := tx.Get("orders", f.id, &o); err != nil {
			return err
		}
		o.UnitPrice.Amount = 0
		o.Total.Amount = 0
		o.State = RefundPending
		o.RefundState = "requested"
		if err := tx.Put("orders", o.ID, o); err != nil {
			return err
		}
		op := operation{Command: orderMoneyCommand(f.shop, o, "refund"), Before: Paid}
		return tx.Put("money_operations", op.Command.Key, op)
	}))
	var err error
	f.s, err = New(f.repo, f.auth, DisabledFunds{})
	must(t, err)
	o, err := f.s.reconcileOrderMoney(context.Background(), f.shop, f.id)
	must(t, err)
	if o.State != Refunded || o.RefundRef != "free:"+ID("store-money-v1", f.shop, f.id, "refund") {
		t.Fatal("free refund did not finish its original local operation")
	}
	before := f.image(t)
	_, err = f.s.reconcileOrderMoney(context.Background(), f.shop, f.id)
	must(t, err)
	if f.image(t) != before {
		t.Fatal("free refund changed inventory twice")
	}
}

func TestA06RefundAndCaptureCannotChangeTheSamePendingOrder(t *testing.T) {
	for _, refundFirst := range []bool{true, false} {
		t.Run(map[bool]string{true: "refund-first", false: "capture-first"}[refundFirst], func(t *testing.T) {
			f := a06StateSetup(t)
			_, err := f.change(buyerToken, "pay", "")
			must(t, err)
			if refundFirst {
				_, err = f.change(buyerToken, "request_refund", "synthetic refund")
				must(t, err)
				before := f.image(t)
				_, err = f.s.FinalizeDelivery(context.Background(), f.shop, f.id)
				wantError(t, err, ErrConflict)
				if f.image(t) != before || f.funds.captureCalls.Load() != 0 {
					t.Fatal("delivery ran after refund request")
				}
			} else {
				entered, release := make(chan struct{}), make(chan struct{})
				var once sync.Once
				t.Cleanup(func() { once.Do(func() { close(release) }) })
				f.funds.capture = func(c MoneyCommand) (Receipt, error) { close(entered); <-release; return a06StateReceipt(c), nil }
				done := make(chan error, 1)
				go func() { _, err := f.s.FinalizeDelivery(context.Background(), f.shop, f.id); done <- err }()
				<-entered
				before := f.image(t)
				_, err = f.change(buyerToken, "request_refund", "synthetic refund")
				wantError(t, err, ErrConflict)
				if f.image(t) != before {
					t.Fatal("refund changed an unresolved capture")
				}
				once.Do(func() { close(release) })
				must(t, <-done)
				_, err = f.change(buyerToken, "request_refund", "synthetic refund")
				must(t, err)
			}
			_, err = f.change(sellerToken, "refund", "")
			must(t, err)
			executed := f.funds.execCalls.Load()
			before := f.image(t)
			_, err = f.change(sellerToken, "refund", "")
			must(t, err)
			if f.funds.execCalls.Load() != executed || f.image(t) != before || f.order(t).State != Refunded {
				t.Fatal("duplicate refund changed funds or inventory")
			}
		})
	}
}
