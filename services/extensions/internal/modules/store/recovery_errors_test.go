// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package store

import (
	"context"
	"errors"
	"testing"
)

type failedDeliveryIndex struct {
	*a06MoneyIndex
	oversized bool
}

func (r *failedDeliveryIndex) PendingDeliveries(context.Context, string, int) ([]LocatedOrder, error) {
	if r.oversized {
		return make([]LocatedOrder, 21), nil
	}
	return nil, context.DeadlineExceeded
}

func TestRecoveryTickPreservesMoneyErrorsAndBoundsDeliveryPage(t *testing.T) {
	for _, oversized := range []bool{false, true} {
		t.Run(map[bool]string{false: "delivery-index-error", true: "oversized-delivery-page"}[oversized], func(t *testing.T) {
			f := a06StateSetup(t)
			f.pendingPay(t)
			op := f.operation(t, "pay")
			op.Command.Buyer.ID = 3
			must(t, f.repo.Within(context.Background(), f.shop, func(tx Tx) error { return tx.Put("money_operations", op.Command.Key, op) }))
			index := &failedDeliveryIndex{a06MoneyIndex: &a06MoneyIndex{memoryRepo: f.repo}, oversized: oversized}
			var err error
			f.s, err = New(index, f.auth, f.funds)
			must(t, err)
			before := f.image(t)
			err = f.s.RecoverDeliveries(context.Background())
			want := context.DeadlineExceeded
			if oversized {
				want = ErrUnavailable
			}
			if !errors.Is(err, ErrConflict) || !errors.Is(err, want) {
				t.Fatalf("worker lost one of the recovery errors: %v", err)
			}
			if f.image(t) != before || f.funds.readCalls.Load() != 0 || f.funds.captureCalls.Load() != 0 {
				t.Fatal("corruption or an unbounded delivery page caused side effects")
			}
		})
	}
}
