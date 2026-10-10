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
	Key           string  `json:"key"`
	Kind          string  `json:"kind"`
	ShopID        string  `json:"shop_id"`
	OrderID       string  `json:"order_id"`
	Buyer         Account `json:"buyer"`
	Seller        Account `json:"seller"`
	Total         Price   `json:"total"`
	PaymentRef    string  `json:"payment_ref,omitempty"`
	SettlementRef string  `json:"settlement_ref,omitempty"`
}
type Receipt struct {
	Key         string `json:"key"`
	CommandHash string `json:"command_hash"`
	Reference   string `json:"reference"`
	Outcome     string `json:"outcome"` // succeeded or rejected; all other outcomes remain pending.
}
type operation struct {
	Command MoneyCommand `json:"command"`
	Before  string       `json:"before"`
	Done    bool         `json:"done"`
	Receipt Receipt      `json:"receipt"`
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
		op = operation{Command: MoneyCommand{Key: key, Kind: c.Action, ShopID: c.ShopID, OrderID: out.ID, Buyer: out.Buyer, Seller: out.Seller, Total: out.Total, PaymentRef: out.PaymentRef, SettlementRef: out.SettlementRef}, Before: before}
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
	if receipt.Key != key || receipt.CommandHash != ID(op.Command) || !Text(receipt.Reference, 256) || (receipt.Outcome != "succeeded" && receipt.Outcome != "rejected") {
		return out, ErrPending
	}
	// If the process dies here, the durable operation is retried with the SAME key.
	err = s.repo.Within(ctx, c.ShopID, func(tx Tx) error {
		if err := tx.Get("orders", c.OrderID, &out); err != nil {
			return err
		}
		var current operation
		if err := tx.Get("money_operations", key, &current); err != nil {
			return err
		}
		if current.Done {
			return nil
		}
		if ID(current.Command) != receipt.CommandHash {
			return ErrConflict
		}
		if receipt.Outcome == "rejected" {
			out.State = current.Before
			if c.Action == "pay" {
				if err := release(tx, &out); err != nil {
					return err
				}
				out.State = Cancelled
			}
		} else {
			switch c.Action {
			case "pay":
				out.State = Paid
				out.PaymentRef = receipt.Reference
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
		return out, ErrPending
	}
	if receipt.Outcome == "rejected" {
		return out, ErrConflict
	}
	return out, nil
}
