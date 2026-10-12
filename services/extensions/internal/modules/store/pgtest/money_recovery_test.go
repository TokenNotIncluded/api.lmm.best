// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package pgtest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/store"
)

func TestPostgresPendingMoneyCompositeCursorAndRecordBinding(t *testing.T) {
	db := legacyDatabase(t)
	r, err := store.NewPostgres(db)
	must(t, err)
	ctx := context.Background()
	shopA, shopB := strings.Repeat("a", 64), strings.Repeat("b", 64)
	for i := 1; i <= 21; i++ {
		shop, id := shopA, fmt.Sprintf("%064x", i)
		if i == 21 {
			// Public IDs stay globally unique. This next shop's order sorts
			// before the first shop's last ID, but after it in composite order.
			shop, id = shopB, fmt.Sprintf("%064x", 0)
		}
		state := []string{store.PaymentPending, store.SettlementPending, store.RefundPending}[i%3]
		must(t, r.Within(ctx, shop, func(tx store.Tx) error {
			return tx.Put("orders", id, store.Order{ID: id, State: state, Quantity: 1,
				UnitPrice: store.Price{Amount: 50, Unit: "credits"}, Total: store.Price{Amount: 50, Unit: "credits"},
				Legacy: &store.LegacyOrderMetadata{ProductID: "synthetic-product"}})
		}))
	}
	must(t, r.Within(ctx, shopA, func(tx store.Tx) error {
		id := strings.Repeat("f", 64)
		return tx.Put("orders", id, store.Order{ID: id, State: store.Paid, Quantity: 1,
			UnitPrice: store.Price{Amount: 50, Unit: "credits"}, Total: store.Price{Amount: 50, Unit: "credits"}})
	}))
	first, err := r.PendingMoneyOrders(ctx, store.MoneyCursor{}, 20)
	must(t, err)
	if len(first) != 20 || first[19].ShopID != shopA || first[19].Order.ID != fmt.Sprintf("%064x", 20) {
		t.Fatal("first page did not use the composite order")
	}
	second, err := r.PendingMoneyOrders(ctx, store.MoneyCursor{ShopID: first[19].ShopID, OrderID: first[19].Order.ID}, 20)
	must(t, err)
	if len(second) != 1 || second[0].ShopID != shopB || second[0].Order.ID != fmt.Sprintf("%064x", 0) || second[0].ProductID != "synthetic-product" {
		t.Fatal("lower ID in the next shop was lost, or non-pending state was returned")
	}
	_, err = r.PendingMoneyOrders(ctx, store.MoneyCursor{ShopID: shopA}, 20)
	if !errors.Is(err, store.ErrInvalid) {
		t.Fatal("accepted partial cursor")
	}
	_, err = r.PendingMoneyOrders(ctx, store.MoneyCursor{}, 101)
	if !errors.Is(err, store.ErrInvalid) {
		t.Fatal("accepted unbounded page")
	}
	_, err = db.Exec(`UPDATE lmm_store.orders SET body=jsonb_set(body,'{id}','"wrong-record-id"'::jsonb) WHERE shop_id=$1 AND id=$2`, shopB, second[0].Order.ID)
	must(t, err)
	_, err = r.PendingMoneyOrders(ctx, store.MoneyCursor{ShopID: shopA, OrderID: first[19].Order.ID}, 20)
	if !errors.Is(err, store.ErrUnavailable) {
		t.Fatal("accepted a body that did not match its database key")
	}
}

// PostgreSQL persists the Go operations; the receipt port is still synthetic.
// This is not a Go-to-Core Commerce ledger or OS-process-restart acceptance.
type countedRecoveryPort struct {
	*recoveryPort
	executes, reads, captures atomic.Int64
}

func (p *countedRecoveryPort) Execute(ctx context.Context, token string, c store.MoneyCommand) (store.Receipt, error) {
	p.executes.Add(1)
	return p.recoveryPort.Execute(ctx, token, c)
}
func (p *countedRecoveryPort) Reconcile(ctx context.Context, c store.MoneyCommand) (store.Receipt, error) {
	p.reads.Add(1)
	return p.recoveryPort.Reconcile(ctx, c)
}
func (p *countedRecoveryPort) CompleteDelivery(ctx context.Context, c store.MoneyCommand, hash, reference string) (store.Receipt, error) {
	p.captures.Add(1)
	return p.recoveryPort.CompleteDelivery(ctx, c, hash, reference)
}

func TestPostgresLostRefundAcknowledgmentUsesOnlyPersistedReceipt(t *testing.T) {
	for _, delivered := range []bool{false, true} {
		t.Run(fmt.Sprintf("delivered=%t", delivered), func(t *testing.T) {
			db := legacyDatabase(t)
			r, err := store.NewPostgres(db)
			must(t, err)
			ctx := context.Background()
			shop, id, variant := store.ID("refund-shop"), store.ID("refund-order"), store.ID("refund-variant")
			o := store.Order{ID: id, Buyer: store.Account{Kind: "personal", ID: 2}, Seller: store.Account{Kind: "personal", ID: 1}, CreatedBy: 2,
				VariantID: variant, Quantity: 2, UnitPrice: store.Price{Amount: 50, Unit: "credits"}, Total: store.Price{Amount: 100, Unit: "credits"},
				State: store.AwaitingPayment, Legacy: &store.LegacyOrderMetadata{ProductID: store.ID("refund-product"), DeliveryTemplate: "fixed-content"}}
			must(t, r.Within(ctx, shop, func(tx store.Tx) error {
				if err := tx.Put("orders", id, o); err != nil {
					return err
				}
				if err := tx.Put("variants", variant, store.Variant{ID: variant, Price: o.UnitPrice, TrackStock: true, Stock: 10, Reserved: 2, Claimed: 2, Quota: -1}); err != nil {
					return err
				}
				return tx.Put("commands", store.ID("legacy-order-delivery-v1", id), map[string]any{"content": "synthetic immutable content"})
			}))
			auth := &revocableAuthority{}
			port := &countedRecoveryPort{recoveryPort: &recoveryPort{receipts: map[string]store.Receipt{}, checkDelivery: func(store.MoneyCommand) {}}}
			s, err := store.New(r, auth, port)
			must(t, err)
			change := func(token, action, text string) error {
				_, err := s.ChangeOrder(ctx, token, store.OrderChange{ShopID: shop, OrderID: id, Action: action, Text: text})
				return err
			}
			must(t, change("buyer", "pay", ""))
			if delivered {
				_, err = s.FinalizeDelivery(ctx, shop, id)
				must(t, err)
			}
			must(t, change("buyer", "request_refund", "synthetic refund request"))
			port.losePay = true // Refund commits at the synthetic port, but its ACK is lost.
			if err := change("seller", "refund", ""); !errors.Is(err, store.ErrPending) {
				t.Fatalf("lost refund ACK: %v", err)
			}
			pending, err := r.FindOrder(ctx, id)
			must(t, err)
			if pending.Order.State != store.RefundPending {
				t.Fatal("refund command was not durable")
			}
			auth.revoked.Store(true)
			beforeExec, beforeCapture := port.executes.Load(), port.captures.Load()
			// New repository and Service instances read the existing PostgreSQL records.
			recreatedRepo, err := store.NewPostgres(db)
			must(t, err)
			recreated, err := store.New(recreatedRepo, auth, port)
			must(t, err)
			must(t, recreated.RecoverDeliveries(ctx))
			current, err := recreatedRepo.FindOrder(ctx, id)
			must(t, err)
			if current.Order.State != store.Refunded || current.Order.Delivered != delivered || port.reads.Load() != 1 {
				t.Fatal("read-only worker did not finish the original refund")
			}
			must(t, recreated.RecoverDeliveries(ctx))
			if port.executes.Load() != beforeExec || port.captures.Load() != beforeCapture || port.reads.Load() != 1 {
				t.Fatal("refund recovery re-executed money, delivery, or a completed query")
			}
			var v store.Variant
			must(t, recreatedRepo.Within(ctx, shop, func(tx store.Tx) error { return tx.Get("variants", variant, &v) }))
			if delivered {
				if v.Stock != 8 || v.Reserved != 0 || v.Claimed != 2 {
					t.Fatal("delivered refund restocked consumed inventory")
				}
			} else if v.Stock != 10 || v.Reserved != 0 || v.Claimed != 0 {
				t.Fatal("undelivered refund did not release its reservation exactly once")
			}
		})
	}
}
