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

// These are module tests with synthetic receipts, not Core/PG acceptance.
type a06ReconcileRepo struct {
	*memoryRepo
	shopID string
}

func (r *a06ReconcileRepo) FindOrder(ctx context.Context, id string) (LocatedOrder, error) {
	var o Order
	err := r.Within(ctx, r.shopID, func(tx Tx) error { return tx.Get("orders", id, &o) })
	return LocatedOrder{ShopID: r.shopID, Order: o}, err
}
func (*a06ReconcileRepo) FindProduct(context.Context, string) (LocatedProduct, error) {
	return LocatedProduct{}, ErrNotFound
}
func (*a06ReconcileRepo) ListPublished(context.Context, int64, string, int, int) ([]LocatedProduct, error) {
	return nil, ErrUnavailable
}

type a06ReconcileFunds struct {
	execReady bool
	readReady bool
	execCalls atomic.Int64
	readCalls atomic.Int64
	captures  atomic.Int64
	reply     func(MoneyCommand) (Receipt, error)
}

func (f *a06ReconcileFunds) Available() bool          { return f.execReady }
func (f *a06ReconcileFunds) ReconcileAvailable() bool { return f.readReady }
func (*a06ReconcileFunds) DeliveryAvailable() bool    { return true }
func (f *a06ReconcileFunds) Execute(context.Context, string, MoneyCommand) (Receipt, error) {
	f.execCalls.Add(1)
	return Receipt{}, errors.New("reconciliation must not execute money")
}
func (f *a06ReconcileFunds) CompleteDelivery(context.Context, MoneyCommand, string, string) (Receipt, error) {
	f.captures.Add(1)
	return Receipt{}, errors.New("reconciliation must not capture or deliver")
}
func (f *a06ReconcileFunds) Reconcile(ctx context.Context, cmd MoneyCommand) (Receipt, error) {
	f.readCalls.Add(1)
	if _, ok := ctx.Deadline(); !ok {
		return Receipt{}, errors.New("missing query deadline")
	}
	if f.reply != nil {
		return f.reply(cmd)
	}
	return Receipt{Key: cmd.Key, CommandHash: ID(cmd), Reference: "synthetic:" + cmd.Key, Outcome: "succeeded"}, nil
}

type a06ReconcileFixture struct {
	s     *Service
	repo  *a06ReconcileRepo
	auth  *authority
	funds *a06ReconcileFunds
	order Order
	op    operation
}

func a06ReconcileSetup(t *testing.T, state string, pending bool) *a06ReconcileFixture {
	t.Helper()
	f := &a06ReconcileFixture{
		repo: &a06ReconcileRepo{memoryRepo: newRepo(), shopID: ID("a06", "shop")},
		auth: &authority{}, funds: &a06ReconcileFunds{execReady: true, readReady: true},
	}
	var err error
	f.s, err = New(f.repo, f.auth, f.funds)
	must(t, err)
	f.order = Order{
		ID: ID("a06", "order"), Buyer: Account{"personal", 2}, Seller: Account{"personal", 1},
		CreatedBy: 2, VariantID: ID("a06", "variant"), Quantity: 2,
		UnitPrice: Price{Amount: 50, Unit: "credits"}, Total: Price{Amount: 100, Unit: "credits"},
		State: state, Legacy: &LegacyOrderMetadata{ProductID: ID("a06", "product"), PaymentMethod: "balance", DeliveryTemplate: "fixed-content"},
	}
	kind, before := "pay", AwaitingPayment
	if state == SettlementPending || state == RefundPending {
		kind, before = "settle", Fulfilled
		f.order.PaymentRef = "synthetic:reserve"
		f.order.Delivered, f.order.Delivery = true, "synthetic immutable delivery"
	}
	if state == RefundPending {
		kind, before = "refund", Settled
		f.order.SettlementRef, f.order.RefundState = "synthetic:capture", "requested"
	}
	f.op = operation{Before: before, Command: MoneyCommand{
		Key: ID("store-money-v1", f.repo.shopID, f.order.ID, kind), Kind: kind,
		ShopID: f.repo.shopID, OrderID: f.order.ID, Buyer: f.order.Buyer, Seller: f.order.Seller,
		Total: f.order.Total, PaymentRef: f.order.PaymentRef, SettlementRef: f.order.SettlementRef,
		OrderSnapshot: &MoneyOrderSnapshot{VariantID: f.order.VariantID, Quantity: 2, UnitPrice: f.order.UnitPrice, CreatedBy: 2},
	}}
	must(t, f.repo.Within(context.Background(), f.repo.shopID, func(tx Tx) error {
		if err := tx.Put("orders", f.order.ID, f.order); err != nil {
			return err
		}
		variant := Variant{ID: f.order.VariantID, TrackStock: true, Stock: 10, Reserved: 2, Claimed: 2, Quota: -1}
		if f.order.Delivered {
			variant.Stock, variant.Reserved = 8, 0
		}
		if err := tx.Put("variants", variant.ID, variant); err != nil {
			return err
		}
		if pending {
			return tx.Put("money_operations", f.op.Command.Key, f.op)
		}
		return nil
	}))
	return f
}
func (f *a06ReconcileFixture) call(token string, actor Account) (any, error) {
	return f.s.legacyOrder(context.Background(), token, actor, LegacyRequest{Action: "reconcile_order", ID: f.order.ID})
}
func (f *a06ReconcileFixture) image(t *testing.T) string {
	t.Helper()
	f.repo.mu.Lock()
	defer f.repo.mu.Unlock()
	b, err := json.Marshal(f.repo.data)
	must(t, err)
	return string(b)
}
func (f *a06ReconcileFixture) noExecution(t *testing.T) {
	t.Helper()
	if f.funds.execCalls.Load() != 0 || f.funds.captures.Load() != 0 {
		t.Fatalf("execute=%d capture=%d", f.funds.execCalls.Load(), f.funds.captures.Load())
	}
}

func TestA06ReconcileNeverStartsPaymentOrDelivery(t *testing.T) {
	for _, state := range []string{AwaitingPayment, Paid, Fulfilled, Settled, Refunded, Cancelled} {
		t.Run(state, func(t *testing.T) {
			f := a06ReconcileSetup(t, state, false)
			before := f.image(t)
			_, err := f.call(buyerToken, f.order.Buyer)
			must(t, err)
			f.noExecution(t)
			if f.funds.readCalls.Load() != 0 || f.image(t) != before {
				t.Fatal("polling a non-pending order changed data or contacted Core")
			}
		})
	}
}

func TestA06ExplicitPayStillCreatesThePaymentOperation(t *testing.T) {
	f := a06ReconcileSetup(t, AwaitingPayment, false)
	_, err := f.s.legacyOrder(context.Background(), buyerToken, f.order.Buyer, LegacyRequest{
		Action: "pay_order", ID: f.order.ID, Body: json.RawMessage(`{}`),
	})
	// The probe intentionally loses the payment response; only explicit pay may
	// reach Execute and leave a durable operation for later reconciliation.
	wantError(t, err, ErrPending)
	if f.funds.execCalls.Load() != 1 || f.funds.readCalls.Load() != 0 {
		t.Fatal("explicit payment did not use the existing payment port")
	}
	o, err := f.s.GetOrder(context.Background(), buyerToken, f.repo.shopID, f.order.ID)
	must(t, err)
	if o.State != PaymentPending {
		t.Fatal("explicit payment did not persist its pending operation")
	}
}

func TestA06ReconcileRecoversTheCurrentOperationWithoutExecuteCapability(t *testing.T) {
	for _, tc := range []struct{ pending, final, status string }{
		{PaymentPending, Paid, "paid"}, {SettlementPending, Settled, "paid"}, {RefundPending, Refunded, "refunded"},
	} {
		t.Run(tc.pending, func(t *testing.T) {
			f := a06ReconcileSetup(t, tc.pending, true)
			f.funds.execReady = false
			value, err := f.call(buyerToken, f.order.Buyer)
			must(t, err)
			if value.(map[string]any)["status"] != tc.status {
				t.Fatal(value)
			}
			must(t, f.repo.Within(context.Background(), f.repo.shopID, func(tx Tx) error {
				var o Order
				if err := tx.Get("orders", f.order.ID, &o); err != nil {
					return err
				}
				if o.State != tc.final || o.Delivery != f.order.Delivery || o.Delivered != f.order.Delivered {
					t.Fatalf("unexpected recovered order: %+v", o)
				}
				var v Variant
				if err := tx.Get("variants", o.VariantID, &v); err != nil {
					return err
				}
				if o.Delivered && (v.Stock != 8 || v.Reserved != 0 || v.Claimed != 2) {
					t.Fatal("recovery restocked a delivered order")
				}
				return nil
			}))
			_, err = f.call(buyerToken, f.order.Buyer)
			must(t, err)
			if f.funds.readCalls.Load() != 1 {
				t.Fatal("a completed operation was queried again")
			}
			f.noExecution(t)
		})
	}
}

func TestA06ReconcileUnknownAndInvalidReceiptsKeepOriginalState(t *testing.T) {
	for _, mode := range []string{"unknown", "timeout", "hash", "key", "reference", "outcome"} {
		t.Run(mode, func(t *testing.T) {
			f := a06ReconcileSetup(t, PaymentPending, true)
			f.funds.reply = func(c MoneyCommand) (Receipt, error) {
				r := Receipt{Key: c.Key, CommandHash: ID(c), Reference: "synthetic:reserve", Outcome: "succeeded"}
				switch mode {
				case "unknown":
					return Receipt{}, ErrPending
				case "timeout":
					return Receipt{}, context.DeadlineExceeded
				case "hash":
					r.CommandHash = ID("different-command")
				case "key":
					r.Key = ID("different-key")
				case "reference":
					r.Reference = ""
				case "outcome":
					r.Outcome = "unknown"
				}
				return r, nil
			}
			before := f.image(t)
			_, err := f.call(buyerToken, f.order.Buyer)
			wantError(t, err, ErrPending)
			if f.image(t) != before {
				t.Fatal("UNKNOWN changed the order, operation or stock")
			}
			f.noExecution(t)
		})
	}
}

func TestA06ReconcileRejectsChangedFrozenCommandBeforeQuery(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*operation)
	}{
		{"payer", func(o *operation) { o.Command.Buyer.ID++ }},
		{"recipient", func(o *operation) { o.Command.Seller.ID++ }},
		{"amount", func(o *operation) { o.Command.Total.Amount++ }},
		{"unit", func(o *operation) { o.Command.Total = Price{Amount: 100, Currency: "USD"} }},
		{"quantity", func(o *operation) { o.Command.OrderSnapshot.Quantity++ }},
		{"actor", func(o *operation) { o.Command.OrderSnapshot.CreatedBy++ }},
		{"shop", func(o *operation) { o.Command.ShopID = ID("other-shop") }},
		{"order", func(o *operation) { o.Command.OrderID = ID("other-order") }},
		{"key", func(o *operation) { o.Command.Key = ID("other-key") }},
		{"snapshot", func(o *operation) { o.Command.OrderSnapshot = nil }},
		{"before", func(o *operation) { o.Before = Settled }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := a06ReconcileSetup(t, PaymentPending, true)
			key := f.op.Command.Key
			tc.edit(&f.op)
			must(t, f.repo.Within(context.Background(), f.repo.shopID, func(tx Tx) error { return tx.Put("money_operations", key, f.op) }))
			before := f.image(t)
			_, err := f.call(buyerToken, f.order.Buyer)
			wantError(t, err, ErrConflict)
			if f.funds.readCalls.Load() != 0 || f.image(t) != before {
				t.Fatal("a changed command reached Core or changed local data")
			}
			f.noExecution(t)
		})
	}
}

func TestA06ReconcileMissingOperationOrCapabilityDoesNotCreateOne(t *testing.T) {
	for _, missing := range []string{"operation", "capability"} {
		t.Run(missing, func(t *testing.T) {
			f := a06ReconcileSetup(t, PaymentPending, missing != "operation")
			want := ErrNotFound
			if missing == "capability" {
				f.funds.readReady, want = false, ErrPaymentUnavailable
			}
			before := f.image(t)
			_, err := f.call(buyerToken, f.order.Buyer)
			wantError(t, err, want)
			if f.image(t) != before || f.funds.readCalls.Load() != 0 {
				t.Fatal("missing recovery input caused a mutation")
			}
			f.noExecution(t)
		})
	}
}

func TestA06ReconcileChecksCurrentUserAccess(t *testing.T) {
	for _, mode := range []string{"other-user", "authority-down"} {
		t.Run(mode, func(t *testing.T) {
			f := a06ReconcileSetup(t, PaymentPending, true)
			token, actor, want := otherToken, Account{"personal", 3}, ErrForbidden
			if mode == "authority-down" {
				f.auth.down.Store(true)
				token, actor, want = buyerToken, f.order.Buyer, ErrUnavailable
			}
			before := f.image(t)
			_, err := f.call(token, actor)
			wantError(t, err, want)
			if f.funds.readCalls.Load() != 0 || f.image(t) != before {
				t.Fatal("unauthorized reconciliation accessed funds")
			}
			f.noExecution(t)
		})
	}
}

func TestA06ReconcileCommitFailureThenServiceRecreation(t *testing.T) {
	f := a06ReconcileSetup(t, SettlementPending, true)
	var failOnce atomic.Bool
	failOnce.Store(true)
	f.funds.reply = func(c MoneyCommand) (Receipt, error) {
		if failOnce.Swap(false) {
			f.repo.failCommit.Store(true)
		}
		return Receipt{Key: c.Key, CommandHash: ID(c), Reference: "synthetic:capture", Outcome: "succeeded"}, nil
	}
	before := f.image(t)
	_, err := f.call(buyerToken, f.order.Buyer)
	wantError(t, err, ErrPending)
	if f.image(t) != before {
		t.Fatal("failed commit did not roll back")
	}
	// This recreates a Service only. It does not claim a Go OS process restart.
	f.s, err = New(f.repo, f.auth, f.funds)
	must(t, err)
	_, err = f.call(buyerToken, f.order.Buyer)
	must(t, err)
	f.noExecution(t)
}

func TestA06ConcurrentReconcileOnlyReadsCore(t *testing.T) {
	f := a06ReconcileSetup(t, SettlementPending, true)
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := f.call(buyerToken, f.order.Buyer)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		must(t, err)
	}
	f.noExecution(t)
	o, err := f.s.GetOrder(context.Background(), buyerToken, f.repo.shopID, f.order.ID)
	must(t, err)
	if o.State != Settled || o.Delivery != f.order.Delivery {
		t.Fatalf("unexpected final order: %+v", o)
	}
}
