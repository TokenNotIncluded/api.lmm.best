package store

import (
	"context"
	"errors"
	"math"
)

type Purchase struct {
	ShopID    string  `json:"shop_id"`
	VariantID string  `json:"variant_id"`
	Buyer     Account `json:"buyer"`
	Quantity  int64   `json:"quantity"`
	Key       string  `json:"key"`
}

func (s *Service) Purchase(ctx context.Context, token string, p Purchase) (Order, error) {
	if !Key(p.Key) || p.Quantity <= 0 || !p.Buyer.Valid() {
		return Order{}, ErrInvalid
	}
	actor, err := s.check(ctx, token, p.Buyer, Spend)
	if err != nil {
		return Order{}, err
	}
	id := ID("order", p.Buyer, actor, p.Key)
	hash := ID(p)
	var out Order
	err = s.repo.Within(ctx, p.ShopID, func(tx Tx) error {
		if err := tx.Get("orders", id, &out); err == nil {
			if out.RequestHash != hash {
				return ErrConflict
			}
			return nil
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
		var shop Shop
		if err := tx.Get("shops", p.ShopID, &shop); err != nil {
			return err
		}
		if shop.Owner == p.Buyer {
			return ErrForbidden
		}
		var v Variant
		if err := tx.Get("variants", p.VariantID, &v); err != nil {
			return err
		}
		var product Product
		if err := tx.Get("products", v.ProductID, &product); err != nil {
			return err
		}
		if !product.Published {
			return ErrConflict
		}
		if v.Price.Amount > math.MaxInt64/p.Quantity {
			return ErrInvalid
		}
		total := Price{v.Price.Amount * p.Quantity, v.Price.Currency}
		// Even order reservation is disabled for paid goods until the Rust adapter is ready.
		if total.Amount > 0 && !s.funds.Available() {
			return ErrPaymentUnavailable
		}
		if err := v.reserve(p.Quantity); err != nil {
			return err
		}
		out = Order{ID: id, Buyer: p.Buyer, Seller: shop.Owner, CreatedBy: actor, VariantID: v.ID, Title: v.Title, ProductTitle: product.Title, Quantity: p.Quantity, UnitPrice: v.Price, Total: total, State: AwaitingPayment, RequestHash: hash}
		if total.Amount == 0 {
			out.State = Paid
		}
		if err := tx.Put("variants", v.ID, v); err != nil {
			return err
		}
		return tx.Put("orders", out.ID, out)
	})
	return out, err
}

type OrderChange struct {
	ShopID  string `json:"shop_id"`
	OrderID string `json:"order_id"`
	Action  string `json:"action"`
	Text    string `json:"text"`
}

func (s *Service) ChangeOrder(ctx context.Context, token string, c OrderChange) (Order, error) {
	var previous Order
	if err := s.repo.Within(ctx, c.ShopID, func(tx Tx) error { return tx.Get("orders", c.OrderID, &previous) }); err != nil {
		return Order{}, err
	}
	account, permission := previous.Seller, Manage
	switch c.Action {
	case "cancel", "request_refund", "pay":
		account, permission = previous.Buyer, Spend
	}
	if _, err := s.check(ctx, token, account, permission); err != nil {
		return Order{}, err
	}
	if c.Action == "pay" || c.Action == "refund" || c.Action == "settle" {
		return s.money(ctx, token, c)
	}
	var out Order
	err := s.repo.Within(ctx, c.ShopID, func(tx Tx) error {
		if err := tx.Get("orders", c.OrderID, &out); err != nil {
			return err
		}
		switch c.Action {
		case "cancel":
			if out.State == Cancelled {
				return nil
			}
			if out.State != AwaitingPayment && !(out.State == Paid && out.Total.Amount == 0 && out.RefundState == "") {
				return ErrConflict
			}
			if err := release(tx, &out); err != nil {
				return err
			}
			out.State = Cancelled
		case "fulfill":
			if !Text(c.Text, 4000) {
				return ErrInvalid
			}
			if out.Delivered {
				if out.Delivery != c.Text {
					return ErrConflict
				}
				return nil
			}
			if out.State != Paid || out.RefundState == "requested" {
				return ErrConflict
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
			out.Delivery = c.Text
			out.State = Fulfilled
		case "request_refund":
			if !Text(c.Text, 2000) {
				return ErrInvalid
			}
			if out.RefundState != "" {
				if out.RefundReason == c.Text {
					return nil
				}
				return ErrConflict
			}
			if out.State != Paid && out.State != Fulfilled && out.State != Settled {
				return ErrConflict
			}
			out.RefundState = "requested"
			out.RefundReason = c.Text
		case "reject_refund":
			if out.RefundState == "rejected" {
				return nil
			}
			if out.RefundState != "requested" || out.State == RefundPending {
				return ErrConflict
			}
			out.RefundState = "rejected"
		default:
			return ErrInvalid
		}
		return tx.Put("orders", out.ID, out)
	})
	return out, err
}
func release(tx Tx, o *Order) error {
	var v Variant
	if err := tx.Get("variants", o.VariantID, &v); err != nil {
		return err
	}
	if err := v.release(o.Quantity); err != nil {
		return err
	}
	return tx.Put("variants", v.ID, v)
}
