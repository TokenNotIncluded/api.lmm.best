// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
)

type a06MoneyIndex struct {
	*memoryRepo
	cursors []MoneyCursor
}

func (r *a06MoneyIndex) PendingMoneyOrders(ctx context.Context, after MoneyCursor, limit int) ([]LocatedOrder, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.cursors = append(r.cursors, after)
	var rows []LocatedOrder
	for shop, records := range r.data {
		for key, b := range records {
			if !strings.HasPrefix(key, "orders/") {
				continue
			}
			var o Order
			if err := json.Unmarshal(b, &o); err != nil {
				return nil, err
			}
			if o.State != PaymentPending && o.State != SettlementPending && o.State != RefundPending {
				continue
			}
			if shop < after.ShopID || (shop == after.ShopID && o.ID <= after.OrderID) {
				continue
			}
			rows = append(rows, LocatedOrder{ShopID: shop, Order: o})
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].ShopID != rows[j].ShopID {
			return rows[i].ShopID < rows[j].ShopID
		}
		return rows[i].Order.ID < rows[j].Order.ID
	})
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, nil
}
func (*a06MoneyIndex) PendingDeliveries(context.Context, string, int) ([]LocatedOrder, error) {
	return nil, nil
}

func TestA06RecoveryWorkerIncludesRefundsThroughExistingTick(t *testing.T) {
	f := a06StateSetup(t)
	_, err := f.change(buyerToken, "pay", "")
	must(t, err)
	_, err = f.s.FinalizeDelivery(context.Background(), f.shop, f.id)
	must(t, err)
	_, err = f.change(buyerToken, "request_refund", "synthetic refund")
	must(t, err)
	f.funds.execute = func(MoneyCommand) (Receipt, error) { return Receipt{}, ErrPending }
	_, err = f.change(sellerToken, "refund", "")
	wantError(t, err, ErrPending)
	index := &a06MoneyIndex{memoryRepo: f.repo}
	f.s, err = New(index, f.auth, f.funds)
	must(t, err)
	exec, capture := f.funds.execCalls.Load(), f.funds.captureCalls.Load()
	must(t, f.s.RecoverDeliveries(context.Background()))
	if f.order(t).State != Refunded || f.funds.readCalls.Load() != 1 || f.funds.execCalls.Load() != exec || f.funds.captureCalls.Load() != capture {
		t.Fatal("existing worker tick did not recover refund without execution")
	}
	must(t, f.s.RecoverDeliveries(context.Background()))
	if f.funds.readCalls.Load() != 1 {
		t.Fatal("completed refund was queried again")
	}
}

func TestA06RecoveryCompositeCursorAndUnknownFairness(t *testing.T) {
	f := a06StateSetup(t)
	f.pendingPay(t)
	original := f.order(t)
	op := f.operation(t, "pay")
	// Twenty rows in shop A and a lower ID in B exercise the page boundary.
	// An ID-only cursor would skip B despite its later composite position.
	must(t, f.repo.Within(context.Background(), f.shop, func(tx Tx) error {
		o := original
		o.State = Cancelled
		return tx.Put("orders", o.ID, o)
	}))
	for i := 0; i < 21; i++ {
		shop := strings.Repeat("a", 64)
		id := fmt.Sprintf("%064x", i+1)
		if i == 20 {
			shop = strings.Repeat("b", 64)
			id = fmt.Sprintf("%064x", 0)
		}
		o := original
		o.ID = id
		c := op
		c.Command = orderMoneyCommand(shop, o, "pay")
		must(t, f.repo.Within(context.Background(), shop, func(tx Tx) error {
			if err := tx.Put("orders", o.ID, o); err != nil {
				return err
			}
			return tx.Put("money_operations", c.Command.Key, c)
		}))
	}
	index := &a06MoneyIndex{memoryRepo: f.repo}
	var err error
	f.s, err = New(index, f.auth, f.funds)
	must(t, err)
	f.funds.query = func(c MoneyCommand) (Receipt, error) {
		if c.ShopID == strings.Repeat("a", 64) {
			return Receipt{}, ErrPending
		}
		return a06StateReceipt(c), nil
	}
	must(t, f.s.RecoverMoney(context.Background()))
	if f.funds.readCalls.Load() != 20 {
		t.Fatalf("page size=%d", f.funds.readCalls.Load())
	}
	must(t, f.s.RecoverMoney(context.Background()))
	if f.funds.readCalls.Load() != 21 || len(index.cursors) != 2 || index.cursors[1] != (MoneyCursor{ShopID: strings.Repeat("a", 64), OrderID: fmt.Sprintf("%064x", 20)}) {
		t.Fatalf("lost composite page boundary: reads=%d cursors=%v", f.funds.readCalls.Load(), index.cursors)
	}
	var recovered Order
	must(t, f.repo.Within(context.Background(), strings.Repeat("b", 64), func(tx Tx) error { return tx.Get("orders", fmt.Sprintf("%064x", 0), &recovered) }))
	if recovered.State != Paid {
		t.Fatal("unknown shop starved the next shop")
	}
	must(t, f.s.RecoverMoney(context.Background()))
	if f.funds.readCalls.Load() != 41 {
		t.Fatal("UNKNOWN commands were not visited on the next pass")
	}
	if f.funds.execCalls.Load() != 0 || f.funds.captureCalls.Load() != 0 {
		t.Fatal("worker executed a financial action")
	}
}

func TestA06RecoveryDrainDoesNotQueueOrAdvanceCursor(t *testing.T) {
	f := a06StateSetup(t)
	f.pendingPay(t)
	index := &a06MoneyIndex{memoryRepo: f.repo}
	var err error
	f.s, err = New(index, f.auth, f.funds)
	must(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	wantError(t, f.s.RecoverMoney(ctx), context.Canceled)
	if len(index.cursors) != 0 || f.funds.readCalls.Load() != 0 {
		t.Fatal("cancelled tick scanned or queried")
	}
	f.s.recoveryMu.Lock()
	err = f.s.RecoverMoney(context.Background())
	if err == nil {
		err = f.s.RecoverDeliveries(context.Background())
	}
	f.s.recoveryMu.Unlock()
	must(t, err)
	if len(index.cursors) != 0 {
		t.Fatal("overlapping worker tick was not skipped")
	}
	must(t, f.s.RecoverMoney(context.Background()))
	if f.order(t).State != Paid {
		t.Fatal("worker did not resume")
	}
}

func TestA06RecoveryKeepsOtherOrdersMovingAfterCorruption(t *testing.T) {
	f := a06StateSetup(t)
	f.pendingPay(t)
	bad := f.operation(t, "pay")
	bad.Command.Buyer.ID = 3
	must(t, f.repo.Within(context.Background(), f.shop, func(tx Tx) error { return tx.Put("money_operations", bad.Command.Key, bad) }))
	healthy := f.order(t)
	healthy.ID = ID("healthy-order")
	healthyShop := ID("healthy-shop")
	good := operation{Command: orderMoneyCommand(healthyShop, healthy, "pay"), Before: AwaitingPayment}
	must(t, f.repo.Within(context.Background(), healthyShop, func(tx Tx) error {
		if err := tx.Put("orders", healthy.ID, healthy); err != nil {
			return err
		}
		return tx.Put("money_operations", good.Command.Key, good)
	}))
	index := &a06MoneyIndex{memoryRepo: f.repo}
	var err error
	f.s, err = New(index, f.auth, f.funds)
	must(t, err)
	err = f.s.RecoverMoney(context.Background())
	if !errors.Is(err, ErrConflict) || f.funds.readCalls.Load() != 1 || f.order(t).State != PaymentPending {
		t.Fatalf("corrupt operation was hidden or blocked a healthy order: %v", err)
	}
	must(t, f.repo.Within(context.Background(), healthyShop, func(tx Tx) error { return tx.Get("orders", healthy.ID, &healthy) }))
	if healthy.State != Paid {
		t.Fatal("healthy order was not recovered")
	}
}

type a06NoRecoveryFunds struct{ *a06StateFunds }

func (a06NoRecoveryFunds) ReconcileAvailable() bool { return false }

func TestA06RecoveryDisabledCapabilityLeavesOrdersUntouched(t *testing.T) {
	f := a06StateSetup(t)
	f.pendingPay(t)
	index := &a06MoneyIndex{memoryRepo: f.repo}
	var err error
	f.s, err = New(index, f.auth, a06NoRecoveryFunds{f.funds})
	must(t, err)
	before := f.image(t)
	must(t, f.s.RecoverMoney(context.Background()))
	if len(index.cursors) != 0 || f.image(t) != before || f.funds.readCalls.Load() != 0 || f.funds.execCalls.Load() != 0 {
		t.Fatal("disabled recovery produced a side effect")
	}
	_, err = f.s.reconcileOrderMoney(context.Background(), f.shop, f.id)
	wantError(t, err, ErrPaymentUnavailable)
}
