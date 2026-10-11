package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"
)

type deliveryFunds interface {
	DeliveryAvailable() bool
	CompleteDelivery(context.Context, MoneyCommand, string, string) (Receipt, error)
}
type recoveryFunds interface {
	ReconcileAvailable() bool
	Reconcile(context.Context, MoneyCommand) (Receipt, error)
}

// FinalizeDelivery is a trusted module recovery port, not an unauthenticated
// user route. Delivery is committed before consuming the original Rust capture
// grant. No new charge, amount, buyer or merchant can be selected here.
func (s *Service) FinalizeDelivery(ctx context.Context, shopID, orderID string) (Order, error) {
	funds, ok := s.funds.(deliveryFunds)
	if !ok || !funds.DeliveryAvailable() {
		return Order{}, ErrPaymentUnavailable
	}
	var out Order
	var op operation
	key := ID("store-money-v1", shopID, orderID, "settle")
	err := s.repo.Within(ctx, shopID, func(tx Tx) error {
		if err := tx.Get("orders", orderID, &out); err != nil {
			return err
		}
		if out.Legacy == nil || out.Legacy.DeliveryTemplate != "fixed-content" || out.Total.Amount <= 0 {
			return ErrInvalid
		}
		if out.State == Settled {
			return nil
		}
		if out.RefundState == "requested" || out.State == RefundPending || out.State == Refunded {
			return ErrConflict
		}
		if out.State != Paid && out.State != Fulfilled && out.State != SettlementPending {
			return ErrConflict
		}
		var pay operation
		if err := tx.Get("money_operations", ID("store-money-v1", shopID, orderID, "pay"), &pay); err != nil {
			return err
		}
		if !pay.Done || pay.Receipt.Outcome != "succeeded" || pay.Receipt.CommandHash != ID(pay.Command) || pay.Receipt.Reference != out.PaymentRef {
			return ErrConflict
		}
		if out.State == Paid {
			var private struct {
				Content string `json:"content"`
			}
			if err := tx.Get("commands", ID("legacy-order-delivery-v1", out.ID), &private); err != nil {
				return err
			}
			if !Text(private.Content, 16000) {
				return ErrUnavailable
			}
			var v Variant
			if err := tx.Get("variants", out.VariantID, &v); err != nil {
				return err
			}
			if v.Reserved < out.Quantity || (v.TrackStock && v.Stock < out.Quantity) {
				return ErrConflict
			}
			v.Reserved -= out.Quantity
			if v.TrackStock {
				v.Stock -= out.Quantity
			}
			if err := tx.Put("variants", v.ID, v); err != nil {
				return err
			}
			out.Delivered = true
			out.Delivery = private.Content
			out.State = Fulfilled
		}
		digest := sha256.Sum256([]byte(out.Delivery))
		reference := ID("store-delivery-v1", shopID, out.ID, out.VariantID, out.Quantity, hex.EncodeToString(digest[:]))
		if err := tx.Get("money_operations", key, &op); err == nil {
			if op.DeliveryReference != reference || op.PaymentCommandHash != pay.Receipt.CommandHash {
				return ErrConflict
			}
		} else if !errors.Is(err, ErrNotFound) {
			return err
		} else {
			op = operation{Command: MoneyCommand{Key: key, Kind: "settle", ShopID: shopID, OrderID: out.ID, Buyer: out.Buyer, Seller: out.Seller, Total: out.Total, PaymentRef: out.PaymentRef, OrderSnapshot: &MoneyOrderSnapshot{VariantID: out.VariantID, Quantity: out.Quantity, UnitPrice: out.UnitPrice, CreatedBy: out.CreatedBy}}, Before: Fulfilled, PaymentCommandHash: pay.Receipt.CommandHash, DeliveryReference: reference}
		}
		if op.Done {
			if op.Receipt.Outcome == "rejected" {
				return ErrConflict
			}
			return nil
		}
		out.State = SettlementPending
		if err := tx.Put("orders", out.ID, out); err != nil {
			return err
		}
		return tx.Put("money_operations", key, op)
	})
	if err != nil {
		return out, err
	}
	if out.State == Settled {
		return out, nil
	}
	bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
	receipt, err := funds.CompleteDelivery(bounded, op.Command, op.PaymentCommandHash, op.DeliveryReference)
	cancel()
	if err != nil {
		return out, ErrPending
	}
	if receipt.Key != key || receipt.CommandHash != ID(op.Command) || !Text(receipt.Reference, 256) || (receipt.Outcome != "succeeded" && receipt.Outcome != "rejected") {
		return out, ErrPending
	}
	err = s.repo.Within(ctx, shopID, func(tx Tx) error {
		var current operation
		if err := tx.Get("money_operations", key, &current); err != nil {
			return err
		}
		if err := tx.Get("orders", orderID, &out); err != nil {
			return err
		}
		if current.Done {
			return nil
		}
		if ID(current.Command) != receipt.CommandHash || current.DeliveryReference != op.DeliveryReference || current.PaymentCommandHash != op.PaymentCommandHash {
			return ErrConflict
		}
		// A financial capture rejection cannot retract already committed delivery.
		// Keep the immutable delivery record and require explicit reconciliation.
		if receipt.Outcome == "rejected" {
			out.State = Fulfilled
		} else {
			out.State = Settled
			out.SettlementRef = receipt.Reference
		}
		current.Done = true
		current.Receipt = receipt
		if err := tx.Put("orders", out.ID, out); err != nil {
			return err
		}
		return tx.Put("money_operations", key, current)
	})
	if err != nil {
		return out, ErrPending
	}
	if receipt.Outcome == "rejected" {
		return out, ErrConflict
	}
	return out, nil
}

type deliveryIndex interface {
	PendingDeliveries(context.Context, string, int) ([]LocatedOrder, error)
}

// RecoverDeliveries never retries the original pay operation with a service
// token. It only finishes previously attested paid fixed-content deliveries.
func (s *Service) RecoverDeliveries(ctx context.Context) error {
	s.recoveryMu.Lock()
	defer s.recoveryMu.Unlock()
	funds, ok := s.funds.(deliveryFunds)
	if !ok || !funds.DeliveryAvailable() {
		return nil
	}
	idx, ok := s.repo.(deliveryIndex)
	if !ok {
		return ErrUnavailable
	}
	rows, err := idx.PendingDeliveries(ctx, s.recoveryAfter, 20)
	if err != nil {
		return err
	}
	var result error
	for _, row := range rows {
		s.recoveryAfter = row.Order.ID
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if row.Order.State == PaymentPending {
			recovery, ok := s.funds.(recoveryFunds)
			if !ok || !recovery.ReconcileAvailable() {
				continue
			}
			var op operation
			err := s.repo.Within(ctx, row.ShopID, func(tx Tx) error {
				return tx.Get("money_operations", ID("store-money-v1", row.ShopID, row.Order.ID, "pay"), &op)
			})
			if err != nil {
				result = errors.Join(result, err)
				continue
			}
			if !op.Done {
				bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
				got, err := recovery.Reconcile(bounded, op.Command)
				cancel()
				if err != nil {
					continue
				} // UNKNOWN is pending, never permission to execute pay.
				row.Order, err = s.finishMoney(ctx, row.ShopID, row.Order.ID, op, got)
				if err != nil {
					continue
				}
			}
		}
		_, err := s.FinalizeDelivery(ctx, row.ShopID, row.Order.ID)
		if err != nil && !errors.Is(err, ErrPending) && !errors.Is(err, ErrConflict) {
			result = errors.Join(result, err)
		}
	}
	if len(rows) < 20 {
		s.recoveryAfter = ""
	}
	return result
}
