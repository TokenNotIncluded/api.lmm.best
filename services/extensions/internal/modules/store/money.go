package store

import (
	"context"
	"errors"
	"time"
)

// Funds is a controlled Rust port, not a balance repository. Execute MUST
// atomically deduplicate Key AND verify the immutable command hash in Rust.
// It must recheck current credential/account authority and team spending limits.
// A timeout is not a rejection. No bearer credential is stored in this module.
type Funds interface {
	Available() bool // Local adapter readiness, no network calls.
	Execute(context.Context, string, MoneyCommand) (Receipt, error)
}
type DisabledFunds struct{}

func (DisabledFunds) Available() bool { return false }
func (DisabledFunds) Execute(context.Context, string, MoneyCommand) (Receipt, error) {
	return Receipt{}, ErrPaymentUnavailable
}

type MoneyCommand struct {
	Key           string              `json:"key"`
	Kind          string              `json:"kind"`
	ShopID        string              `json:"shop_id"`
	OrderID       string              `json:"order_id"`
	Buyer         Account             `json:"buyer"`
	Seller        Account             `json:"seller"`
	Total         Price               `json:"total"`
	PaymentRef    string              `json:"payment_ref,omitempty"`
	SettlementRef string              `json:"settlement_ref,omitempty"`
	OrderSnapshot *MoneyOrderSnapshot `json:"order_snapshot,omitempty"`
}
type MoneyOrderSnapshot struct {
	VariantID string `json:"variant_id"`
	Quantity  int64  `json:"quantity"`
	UnitPrice Price  `json:"unit_price"`
	CreatedBy int64  `json:"created_by"`
}
type Receipt struct {
	Key         string `json:"key"`
	CommandHash string `json:"command_hash"`
	Reference   string `json:"reference"`
	Outcome     string `json:"outcome"` // succeeded or rejected; all other outcomes remain pending.
}
type operation struct {
	Command            MoneyCommand `json:"command"`
	Before             string       `json:"before"`
	Done               bool         `json:"done"`
	Receipt            Receipt      `json:"receipt"`
	PaymentCommandHash string       `json:"payment_command_hash,omitempty"`
	DeliveryReference  string       `json:"delivery_reference,omitempty"`
}

func (s *Service) money(ctx context.Context, token string, c OrderChange) (Order, error) {
	var out Order
	var op operation
	key := ID("store-money-v1", c.ShopID, c.OrderID, c.Action)
	err := s.repo.Within(ctx, c.ShopID, func(tx Tx) error {
		if err := tx.Get("orders", c.OrderID, &out); err != nil {
			return err
		}
		if err := tx.Get("money_operations", key, &op); err == nil {
			if !op.Done {
				return validatePendingMoney(c.ShopID, out, op)
			}
			if validateOrderCommand(c.ShopID, out, op.Command) != nil || !validMoneyReceipt(op.Command, op.Receipt) {
				return ErrConflict
			}
			return nil
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
		if out.Total.Amount > 0 && !s.funds.Available() {
			return ErrPaymentUnavailable
		}
		before := out.State
		switch c.Action {
		case "pay":
			if out.State == Paid && out.Total.Amount == 0 {
				return nil
			}
			if out.State != AwaitingPayment {
				return ErrConflict
			}
			out.State = PaymentPending
		case "refund":
			if out.RefundState != "requested" || (out.State != Paid && out.State != Fulfilled && out.State != Settled) {
				return ErrConflict
			}
			out.State = RefundPending
		case "settle":
			if out.State != Fulfilled || out.RefundState == "requested" {
				return ErrConflict
			}
			out.State = SettlementPending
		default:
			return ErrInvalid
		}
		op = operation{Command: orderMoneyCommand(c.ShopID, out, c.Action), Before: before}
		if err := validatePendingMoney(c.ShopID, out, op); err != nil {
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
	if op.Done {
		if op.Receipt.Outcome == "rejected" {
			return out, ErrConflict
		}
		return out, nil
	}
	if op.Command.Key == "" {
		return out, nil
	} // Explicitly free order already paid.
	receipt := Receipt{Key: key, CommandHash: ID(op.Command), Reference: "free:" + key, Outcome: "succeeded"}
	if op.Command.Total.Amount > 0 {
		if !s.funds.Available() {
			return out, ErrPaymentUnavailable
		}
		bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
		receipt, err = s.funds.Execute(bounded, token, op.Command)
		cancel()
		if err != nil {
			return out, ErrPending
		}
	}
	// If the process dies here, the durable operation is retried with the SAME key.
	return s.finishMoney(ctx, c.ShopID, c.OrderID, op, receipt)
}

// finishMoney is shared by a user response and trusted read-only recovery. The
// receipt can only finish the exact command already persisted in PostgreSQL.
func (s *Service) finishMoney(ctx context.Context, shopID, orderID string, op operation, receipt Receipt) (Order, error) {
	var out Order
	key := op.Command.Key
	if !validMoneyReceipt(op.Command, receipt) {
		return out, ErrPending
	}
	err := s.repo.Within(ctx, shopID, func(tx Tx) error {
		if err := tx.Get("orders", orderID, &out); err != nil {
			return err
		}
		var current operation
		if err := tx.Get("money_operations", key, &current); err != nil {
			return err
		}
		if ID(current.Command) != receipt.CommandHash || current.Before != op.Before ||
			current.PaymentCommandHash != op.PaymentCommandHash || current.DeliveryReference != op.DeliveryReference ||
			validateOrderCommand(shopID, out, current.Command) != nil {
			return ErrConflict
		}
		if current.Done {
			if current.Receipt != receipt {
				return ErrConflict
			}
			return nil
		}
		if err := validatePendingMoney(shopID, out, current); err != nil {
			return err
		}
		if receipt.Outcome == "rejected" {
			out.State = current.Before
			if current.Command.Kind == "pay" {
				if err := release(tx, &out); err != nil {
					return err
				}
				out.State = Cancelled
			}
		} else {
			switch current.Command.Kind {
			case "pay":
				out.State = Paid
				out.PaymentRef = receipt.Reference
				if out.Legacy != nil && out.Legacy.PaidAt == 0 {
					out.Legacy.PaidAt = time.Now().Unix()
				}
			case "settle":
				out.State = Settled
				out.SettlementRef = receipt.Reference
			case "refund":
				// Financial refund is NOT evidence of a physical return. Never restock a
				// delivered item, or reset its consumed sales quota, here.
				if !out.Delivered {
					if err := release(tx, &out); err != nil {
						return err
					}
				}
				out.State = Refunded
				out.RefundState = "completed"
				out.RefundRef = receipt.Reference
			}
		}
		current.Done = true
		current.Receipt = receipt
		if err := tx.Put("orders", out.ID, out); err != nil {
			return err
		}
		return tx.Put("money_operations", key, current)
	})
	if err != nil {
		if errors.Is(err, ErrConflict) {
			return out, ErrConflict
		}
		return out, ErrPending
	}
	if receipt.Outcome == "rejected" {
		return out, ErrConflict
	}
	return out, nil
}
