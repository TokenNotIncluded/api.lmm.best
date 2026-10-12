// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package store

import (
	"context"
	"errors"
	"testing"
)

// An internally consistent body is not enough: it must also match the key under
// which it was loaded. Corrupt/mis-mapped records cannot redirect a retry.
func TestA06MoneyBindsRecordKeysBeforeRemoteCall(t *testing.T) {
	for _, mode := range []string{"order-id", "action"} {
		t.Run(mode, func(t *testing.T) {
			f := a06StateSetup(t)
			f.pendingPay(t)
			o := f.order(t)
			op := f.operation(t, "pay")
			key := op.Command.Key
			if mode == "order-id" {
				o.ID = ID("wrong-record")
				op.Command = orderMoneyCommand(f.shop, o, "pay")
			} else {
				o.State, o.Delivered = SettlementPending, true
				o.Delivery, o.PaymentRef = "synthetic delivery", "synthetic:payment"
				op.Before = Fulfilled
				op.Command = orderMoneyCommand(f.shop, o, "settle")
			}
			must(t, f.repo.Within(context.Background(), f.shop, func(tx Tx) error {
				if err := tx.Put("orders", f.id, o); err != nil {
					return err
				}
				return tx.Put("money_operations", key, op)
			}))
			before := f.image(t)
			_, err := f.change(buyerToken, "pay", "")
			if !errors.Is(err, ErrConflict) || f.funds.execCalls.Load() != 0 || f.image(t) != before {
				t.Fatalf("record alias reached Execute: err=%v calls=%d", err, f.funds.execCalls.Load())
			}
		})
	}
}

func TestA06ReconcileBindsStoredOrderIDBeforeQuery(t *testing.T) {
	f := a06StateSetup(t)
	f.pendingPay(t)
	o, op := f.order(t), f.operation(t, "pay")
	key := op.Command.Key
	o.ID = ID("another-order")
	op.Command = orderMoneyCommand(f.shop, o, "pay")
	must(t, f.repo.Within(context.Background(), f.shop, func(tx Tx) error {
		if err := tx.Put("orders", f.id, o); err != nil {
			return err
		}
		return tx.Put("money_operations", key, op)
	}))
	before := f.image(t)
	_, err := f.s.reconcileOrderMoney(context.Background(), f.shop, f.id)
	if !errors.Is(err, ErrConflict) || f.funds.readCalls.Load() != 0 || f.image(t) != before {
		t.Fatalf("record alias reached Query: err=%v calls=%d", err, f.funds.readCalls.Load())
	}
}

func TestA06CaptureReceiptRequiresUnchangedDelivery(t *testing.T) {
	for _, viaQuery := range []bool{false, true} {
		name := "capture"
		if viaQuery {
			name = "reconcile"
		}
		t.Run(name, func(t *testing.T) {
			f := a06StateSetup(t)
			_, err := f.change(buyerToken, "pay", "")
			must(t, err)
			mutate := func(c MoneyCommand) (Receipt, error) {
				err := f.repo.Within(context.Background(), f.shop, func(tx Tx) error {
					var o Order
					if err := tx.Get("orders", f.id, &o); err != nil {
						return err
					}
					o.Delivery = "different delivery after dispatch"
					return tx.Put("orders", f.id, o)
				})
				return a06StateReceipt(c), err
			}
			if viaQuery {
				f.funds.capture = func(MoneyCommand) (Receipt, error) { return Receipt{}, ErrPending }
				_, err = f.s.FinalizeDelivery(context.Background(), f.shop, f.id)
				wantError(t, err, ErrPending)
				f.funds.query = mutate
				_, err = f.s.reconcileOrderMoney(context.Background(), f.shop, f.id)
			} else {
				f.funds.capture = mutate
				_, err = f.s.FinalizeDelivery(context.Background(), f.shop, f.id)
			}
			if !errors.Is(err, ErrConflict) || f.order(t).State != SettlementPending || f.operation(t, "settle").Done {
				t.Fatalf("capture accepted changed delivery: err=%v state=%s", err, f.order(t).State)
			}
		})
	}
}
