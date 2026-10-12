// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package store

import (
	"context"
	"time"
)

// reconcileOrderMoney reads an existing Core receipt. The caller must first
// authorize access to the order, as legacyOrder does through GetOrder. This is
// not a payment or delivery port: it must never create or execute a command.
func (s *Service) reconcileOrderMoney(ctx context.Context, shopID, orderID string) (Order, error) {
	var out Order
	var op operation
	err := s.repo.Within(ctx, shopID, func(tx Tx) error {
		if err := tx.Get("orders", orderID, &out); err != nil {
			return err
		}
		var kind string
		switch out.State {
		case PaymentPending:
			kind = "pay"
		case SettlementPending:
			kind = "settle"
		case RefundPending:
			kind = "refund"
		default:
			// An unpaid order has no financial result to recover. In particular,
			// polling it is not consent to start its first payment.
			return nil
		}
		key := ID("store-money-v1", shopID, orderID, kind)
		if err := tx.Get("money_operations", key, &op); err != nil {
			return err
		}
		// Bind recovery to the same persisted order and its frozen economics.
		// Do not forward a changed payer, recipient, amount or snapshot to Core.
		expected := MoneyCommand{
			Key: key, Kind: kind, ShopID: shopID, OrderID: orderID,
			Buyer: out.Buyer, Seller: out.Seller, Total: out.Total,
			PaymentRef: out.PaymentRef, SettlementRef: out.SettlementRef,
			OrderSnapshot: &MoneyOrderSnapshot{
				VariantID: out.VariantID, Quantity: out.Quantity,
				UnitPrice: out.UnitPrice, CreatedBy: out.CreatedBy,
			},
		}
		if op.Done || out.ID != orderID || out.Total.Amount <= 0 || ID(op.Command) != ID(expected) {
			return ErrConflict
		}
		if (kind == "pay" && op.Before != AwaitingPayment) ||
			(kind == "settle" && op.Before != Fulfilled) ||
			(kind == "refund" && op.Before != Paid && op.Before != Fulfilled && op.Before != Settled) {
			return ErrConflict
		}
		return nil
	})
	if err != nil || op.Command.Key == "" {
		return out, err
	}
	funds, ok := s.funds.(recoveryFunds)
	if !ok || Missing(funds) || !funds.ReconcileAvailable() {
		return out, ErrPaymentUnavailable
	}
	bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
	receipt, err := funds.Reconcile(bounded, op.Command)
	cancel()
	if err != nil {
		// UNKNOWN retains the original operation and stock reservation. It is
		// never permission to retry Execute, refund, or start delivery.
		return out, ErrPending
	}
	return s.finishMoney(ctx, shopID, orderID, op, receipt)
}
