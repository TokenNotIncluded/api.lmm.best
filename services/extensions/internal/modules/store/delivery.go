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
		if !pay.Done || pay.Command.Kind != "pay" || pay.Before != AwaitingPayment ||
			validateOrderCommand(shopID, out, pay.Command) != nil || !validMoneyReceipt(pay.Command, pay.Receipt) ||
			pay.Receipt.Outcome != "succeeded" || pay.Receipt.Reference != out.PaymentRef {
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
			op = operation{Command: orderMoneyCommand(shopID, out, "settle"), Before: Fulfilled, PaymentCommandHash: pay.Receipt.CommandHash, DeliveryReference: reference}
		}
		if op.Done {
			if op.Receipt.Outcome == "rejected" {
				return ErrConflict
			}
			return nil
		}
		out.State = SettlementPending
		if err := validatePendingMoney(shopID, out, op); err != nil {
			return err
		}
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
	// Delivery was committed before capture. A rejected capture restores
	// Fulfilled, without retracting delivery or returning consumed inventory.
	return s.finishMoney(ctx, shopID, orderID, op, receipt)
}

type deliveryIndex interface {
	PendingDeliveries(context.Context, string, int) ([]LocatedOrder, error)
}

// RecoverDeliveries never retries the original pay operation with a service
// token. It only finishes previously attested paid fixed-content deliveries.
func (s *Service) RecoverDeliveries(ctx context.Context) error {
	// Callers of this worker also recover lost refund and settlement ACKs.
	// Legacy test/third-party repositories may provide only the delivery index.
	var result error
	if _, ok := s.repo.(moneyRecoveryIndex); ok {
		result = s.RecoverMoney(ctx)
	}
	if err := ctx.Err(); err != nil {
		return errors.Join(result, err)
	}
	if !s.recoveryMu.TryLock() {
		return result
	}
	defer s.recoveryMu.Unlock()
	funds, ok := s.funds.(deliveryFunds)
	if !ok || !funds.DeliveryAvailable() {
		return result
	}
	idx, ok := s.repo.(deliveryIndex)
	if !ok {
		return ErrUnavailable
	}
	rows, err := idx.PendingDeliveries(ctx, s.recoveryAfter, 20)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if ctx.Err() != nil {
			return errors.Join(result, ctx.Err())
		}
		s.recoveryAfter = row.Order.ID
		if row.Order.State == PaymentPending {
			row.Order, err = s.reconcileOrderMoney(ctx, row.ShopID, row.Order.ID)
			if err != nil {
				if !errors.Is(err, ErrPending) && !errors.Is(err, ErrConflict) {
					result = errors.Join(result, err)
				}
				continue
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
