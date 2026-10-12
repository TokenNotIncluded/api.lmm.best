// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package store

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
)

func orderDeliveryReference(shopID string, o Order) string {
	digest := sha256.Sum256([]byte(o.Delivery))
	return ID("store-delivery-v1", shopID, o.ID, o.VariantID, o.Quantity, hex.EncodeToString(digest[:]))
}

// orderMoneyCommand uses only persisted order facts. A pay command predates all
// financial references; a settlement predates its own settlement reference.
// This also lets us verify an earlier receipt after a later order transition.
func orderMoneyCommand(shopID string, o Order, kind string) MoneyCommand {
	c := MoneyCommand{
		Key: ID("store-money-v1", shopID, o.ID, kind), Kind: kind,
		ShopID: shopID, OrderID: o.ID, Buyer: o.Buyer, Seller: o.Seller, Total: o.Total,
		OrderSnapshot: &MoneyOrderSnapshot{
			VariantID: o.VariantID, Quantity: o.Quantity, UnitPrice: o.UnitPrice, CreatedBy: o.CreatedBy,
		},
	}
	if kind != "pay" {
		c.PaymentRef = o.PaymentRef
	}
	if kind == "refund" {
		c.SettlementRef = o.SettlementRef
	}
	return c
}

func validateOrderCommand(shopID string, o Order, c MoneyCommand) error {
	if !Text(shopID, 128) || !Text(o.ID, 128) || !Text(o.VariantID, 128) ||
		!o.Buyer.Valid() || !o.Seller.Valid() || o.Buyer == o.Seller || o.CreatedBy <= 0 ||
		o.Quantity <= 0 || !o.UnitPrice.valid() || !o.Total.valid() ||
		o.UnitPrice.Amount > math.MaxInt64/o.Quantity ||
		o.UnitPrice.Currency != o.Total.Currency || o.UnitPrice.Unit != o.Total.Unit ||
		o.UnitPrice.Amount*o.Quantity != o.Total.Amount {
		return ErrConflict
	}
	if c.Kind != "pay" && c.Kind != "settle" && c.Kind != "refund" {
		return ErrConflict
	}
	if ID(c) != ID(orderMoneyCommand(shopID, o, c.Kind)) {
		return ErrConflict
	}
	return nil
}

// Call under the shop transaction lock, both before a remote request and when
// its receipt comes back. The order may have changed while that request ran.
func validatePendingMoney(shopID string, o Order, op operation) error {
	if op.Done || validateOrderCommand(shopID, o, op.Command) != nil {
		return ErrConflict
	}
	switch op.Command.Kind {
	case "pay":
		if o.State != PaymentPending || op.Before != AwaitingPayment || o.Delivered || o.PaymentRef != "" || o.SettlementRef != "" {
			return ErrConflict
		}
	case "settle":
		if o.State != SettlementPending || op.Before != Fulfilled || !o.Delivered || o.RefundState == "requested" || o.SettlementRef != "" {
			return ErrConflict
		}
		if op.PaymentCommandHash != "" || op.DeliveryReference != "" {
			if op.PaymentCommandHash == "" || !Text(o.Delivery, 16000) || op.DeliveryReference != orderDeliveryReference(shopID, o) {
				return ErrConflict
			}
		}
	case "refund":
		if o.State != RefundPending || o.RefundState != "requested" ||
			(op.Before != Paid && op.Before != Fulfilled && op.Before != Settled) ||
			(o.Delivered != (op.Before == Fulfilled || op.Before == Settled)) {
			return ErrConflict
		}
	}
	return nil
}

func validMoneyReceipt(c MoneyCommand, r Receipt) bool {
	return r.Key == c.Key && r.CommandHash == ID(c) && Text(r.Reference, 256) &&
		(r.Outcome == "succeeded" || r.Outcome == "rejected")
}
