// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package store

import (
	"context"
	"errors"
	"strconv"
	"time"
)

const legacySafeInteger int64 = 9007199254740991

type LegacyOrderMetadata struct {
	ProductID             string `json:"product_id"`
	PaymentMethod         string `json:"payment_method"`
	CreatedAt             int64  `json:"created_at"`
	PaidAt                int64  `json:"paid_at"`
	DeliveryTemplate      string `json:"delivery_template"`
	SellerTermsVersion    string `json:"seller_terms_version,omitempty"`
	SellerTermsContent    string `json:"seller_terms_content,omitempty"`
	SellerTermsAcceptedAt int64  `json:"seller_terms_accepted_at,omitempty"`
}
type legacyCheckout struct {
	VariantID          string `json:"variant_id,omitempty"`
	ProductID          string `json:"product_id"`
	Quantity           int64  `json:"quantity"`
	PaymentMethod      string `json:"payment_method"`
	PromotionCode      string `json:"promotion_code,omitempty"`
	RequestKey         string `json:"request_key"`
	DisclaimerVersion  string `json:"disclaimer_version,omitempty"`
	SellerTermsVersion string `json:"seller_terms_version,omitempty"`
	AcceptSellerTerms  bool   `json:"accept_seller_terms,omitempty"`
	PickupCode         string `json:"pickup_code,omitempty"`
	PickupEmail        string `json:"pickup_email,omitempty"`
}
type legacyAcceptance struct {
	Version    string `json:"version"`
	AcceptedAt int64  `json:"accepted_at"`
}

func (s *Service) PublicDisclaimer(ctx context.Context, userID int64) (map[string]any, error) {
	if userID < 0 {
		return nil, ErrInvalid
	}
	accepted := false
	if userID > 0 {
		err := s.repo.Within(ctx, "@web-legal", func(tx Tx) error {
			var v legacyAcceptance
			err := tx.Get("commands", "disclaimer:"+strconv.FormatInt(userID, 10), &v)
			if errors.Is(err, ErrNotFound) {
				return nil
			}
			if err != nil {
				return err
			}
			accepted = v.Version == "merchant-store-v1" && v.AcceptedAt > 0
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return map[string]any{"version": "merchant-store-v1", "text": legacyDisclaimerText, "accepted": accepted}, nil
}
func (s *Service) acceptLegacyDisclaimer(ctx context.Context, actor Account, r LegacyRequest) (any, error) {
	var in struct {
		Version  string `json:"version"`
		Accepted bool   `json:"accepted"`
	}
	if legacyDecode(r.Body, &in) != nil || !in.Accepted || in.Version != "merchant-store-v1" {
		return nil, ErrInvalid
	}
	err := s.repo.Within(ctx, "@web-legal", func(tx Tx) error {
		key := "disclaimer:" + strconv.FormatInt(actor.ID, 10)
		var old legacyAcceptance
		err := tx.Get("commands", key, &old)
		if err == nil && old.Version == in.Version {
			return nil
		}
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		return tx.Put("commands", key, legacyAcceptance{Version: in.Version, AcceptedAt: time.Now().Unix()})
	})
	return nil, err
}

func legacyOrderView(row LocatedOrder) (map[string]any, error) {
	o := row.Order
	m := o.Legacy
	if m == nil || o.Total.Unit != "credits" || o.UnitPrice.Unit != "credits" || o.Total.Amount < 0 || o.Total.Amount > legacySafeInteger || o.UnitPrice.Amount > legacySafeInteger {
		return nil, ErrUnavailable
	}
	status := "pending"
	switch o.State {
	case Paid, Fulfilled, Settled:
		status = "paid"
	case PaymentPending, SettlementPending:
		status = "reconciliation_pending"
	case RefundPending:
		status = "refund_pending"
	case Refunded:
		status = "refunded"
	case Cancelled:
		status = "cancelled"
	}
	return map[string]any{"id": o.ID, "trade_no": o.ID, "buyer_id": o.Buyer.ID, "seller_id": o.Seller.ID, "product_id": m.ProductID, "product_title": o.ProductTitle, "variant_id": o.VariantID, "variant_name": o.Title, "delivery_template": m.DeliveryTemplate, "quantity": o.Quantity, "unit_price_quota": o.UnitPrice.Amount, "price_quota": o.Total.Amount, "fee_quota": 0, "payment_method": m.PaymentMethod, "status": status, "amount_minor": 0, "currency": "", "frozen_usd_fx": "0", "created_at": m.CreatedAt, "paid_at": m.PaidAt, "expires_at": 0, "pickup_login_required": true, "pickup_code_required": false, "email_pickup_link": false, "payment_issued": o.PaymentRef != "", "seller_terms_version": m.SellerTermsVersion, "seller_terms_content": m.SellerTermsContent, "seller_terms_accepted_at": m.SellerTermsAcceptedAt}, nil
}
func (s *Service) legacyOrder(ctx context.Context, token string, actor Account, r LegacyRequest) (any, error) {
	switch r.Action {
	case "my_orders":
		if r.Role == "" {
			r.Role = "buyer"
		}
		if r.Role != "buyer" && r.Role != "seller" || r.Offset < 0 || r.Offset > 100000 || !Page(r.Limit) {
			return nil, ErrInvalid
		}
		idx, ok := s.repo.(legacyReadIndex)
		if !ok {
			return nil, ErrUnavailable
		}
		rows, err := idx.ListLegacyOrders(ctx, actor, r.Role, r.Offset, r.Limit)
		if err != nil {
			return nil, err
		}
		out := []map[string]any{}
		for _, row := range rows {
			v, err := legacyOrderView(row)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return legacyPage(out, len(out), r.Offset, r.Limit), nil
	case "create_order":
		var in legacyCheckout
		if legacyDecode(r.Body, &in) != nil || !Key(in.RequestKey) || in.Quantity < 1 {
			return nil, ErrInvalid
		}
		if in.PaymentMethod != "balance" || in.PromotionCode != "" || in.PickupCode != "" || in.PickupEmail != "" {
			return nil, ErrUnavailable
		}
		// Reconcile the immutable original request before consulting live product
		// publication, current prices or a newly revised terms version.
		prior, priorErr := s.LocateOrder(ctx, ID("order", actor, actor.ID, in.RequestKey))
		if priorErr == nil {
			o, err := s.GetOrder(ctx, token, prior.ShopID, prior.Order.ID)
			if err != nil {
				return nil, err
			}
			if o.Legacy == nil || o.Buyer != actor || o.Legacy.ProductID != in.ProductID || o.Quantity != in.Quantity || (in.VariantID != "" && o.VariantID != in.VariantID) || o.Legacy.PaymentMethod != in.PaymentMethod {
				return nil, ErrConflict
			}
			prior.Order = o
			view, err := legacyOrderView(prior)
			return map[string]any{"order": view, "created": false}, err
		}
		if !errors.Is(priorErr, ErrNotFound) {
			return nil, priorErr
		}
		if in.DisclaimerVersion != "merchant-store-v1" {
			return nil, ErrDisclaimerRequired
		}
		d, err := s.PublicDisclaimer(ctx, actor.ID)
		if err != nil {
			return nil, err
		}
		if d["accepted"] != true {
			return nil, ErrDisclaimerRequired
		}
		row, err := s.LocateProduct(ctx, in.ProductID)
		if err != nil {
			return nil, err
		}
		if _, err = s.publicLegacyView(row, true); err != nil {
			return nil, err
		}
		if row.Product.Legacy == nil {
			return nil, ErrUnavailable
		}
		if in.VariantID == "" {
			if len(row.Variants) != 1 {
				return nil, ErrInvalid
			}
			in.VariantID = row.Product.Legacy.DefaultVariantID
		}
		found := false
		for _, v := range row.Variants {
			if v.ID == in.VariantID {
				if v.Price.Amount > legacySafeInteger/in.Quantity {
					return nil, ErrInvalid
				}
				found = true
			}
		}
		if !found {
			return nil, ErrNotFound
		}
		created := false
		o, err := s.Purchase(ctx, token, Purchase{ShopID: row.ShopID, VariantID: in.VariantID, Buyer: actor, Quantity: in.Quantity, Key: in.RequestKey, Policy: &LegacyCheckoutPolicy{Version: in.SellerTermsVersion, Accepted: in.AcceptSellerTerms}, Created: &created})
		if err != nil {
			return nil, err
		}
		v, err := legacyOrderView(LocatedOrder{ShopID: row.ShopID, ProductID: in.ProductID, Order: o})
		return map[string]any{"order": v, "created": created}, err
	}
	row, err := s.LocateOrder(ctx, r.ID)
	if err != nil {
		return nil, err
	}
	o, err := s.GetOrder(ctx, token, row.ShopID, row.Order.ID)
	if err != nil {
		return nil, err
	}
	row.Order = o
	if actor != o.Buyer && actor != o.Seller {
		return nil, ErrForbidden
	}
	if r.Action == "get_order" {
		return legacyOrderView(row)
	}
	if r.Action == "refunds" || r.Action == "request_refund" || r.Action == "decide_refund" {
		return nil, ErrUnavailable
	}
	if actor != o.Buyer {
		return nil, ErrForbidden
	}
	switch r.Action {
	case "cancel_order":
		_, err = s.ChangeOrder(ctx, token, OrderChange{ShopID: row.ShopID, OrderID: o.ID, Action: "cancel"})
		return nil, err
	case "reconcile_order":
		o, err = s.reconcileOrderMoney(ctx, row.ShopID, o.ID)
		if err != nil {
			return nil, err
		}
		row.Order = o
		return legacyOrderView(row)
	case "pay_order":
		var in struct {
			Currency string `json:"currency"`
		}
		if legacyDecode(r.Body, &in) != nil {
			return nil, ErrInvalid
		}
		if in.Currency != "" && in.Currency != "USD" {
			return nil, ErrInvalid
		}
		if o.Total.Amount > 0 && !s.funds.Available() {
			return nil, ErrPaymentUnavailable
		}
		o, err = s.ChangeOrder(ctx, token, OrderChange{ShopID: row.ShopID, OrderID: o.ID, Action: "pay"})
		if err != nil {
			return nil, err
		}
		if o.Legacy != nil && o.Legacy.PaidAt == 0 && o.State == Paid {
			err = s.repo.Within(ctx, row.ShopID, func(tx Tx) error {
				var current Order
				if err := tx.Get("orders", o.ID, &current); err != nil {
					return err
				}
				if current.Legacy == nil {
					return ErrConflict
				}
				if current.Legacy.PaidAt == 0 {
					current.Legacy.PaidAt = time.Now().Unix()
					if err := tx.Put("orders", current.ID, current); err != nil {
						return err
					}
				}
				o = current
				return nil
			})
			if err != nil {
				return nil, ErrPending
			}
		}
		row.Order = o
		if o.Legacy != nil && o.Legacy.DeliveryTemplate == "fixed-content" && (o.State == Paid || o.State == Fulfilled || o.State == SettlementPending) {
			o, err = s.FinalizeDelivery(ctx, row.ShopID, o.ID)
			row.Order = o
			if err != nil {
				view, _ := legacyOrderView(row)
				return view, ErrPending
			}
		}
		return map[string]any{"order_id": o.ID, "method": "balance", "amount_minor": 0, "amount": "0", "currency": "", "status": "paid"}, nil
	}
	return nil, ErrUnavailable
}
