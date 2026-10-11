// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package store

import (
	"context"
	"errors"
	"testing"
)

// This exercises the real checkout policy with test-only persistence and
// authority ports; it is not PostgreSQL or Core authorization acceptance.
func TestLegacyCheckoutRequiresCurrentSellerTermsBeforeAnyReservation(t *testing.T) {
	ctx := context.Background()
	f := setup(t, 500001, 4, -1, false, true)
	current := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	must(t, f.repo.Within(ctx, f.shop.ID, func(tx Tx) error {
		var product Product
		if err := tx.Get("products", f.product.ID, &product); err != nil {
			return err
		}
		product.Legacy = &LegacyProductMetadata{Status: "published", Approved: true, DefaultVariantID: f.variant.ID}
		if err := tx.Put("products", product.ID, product); err != nil {
			return err
		}
		var variant Variant
		if err := tx.Get("variants", f.variant.ID, &variant); err != nil {
			return err
		}
		variant.Price = Price{Amount: 500001, Unit: "credits"}
		variant.Legacy = &LegacyVariantMetadata{Enabled: true, Template: "fixed-content", FixedContent: "Synthetic delivery"}
		if err := tx.Put("variants", variant.ID, variant); err != nil {
			return err
		}
		return tx.Put("commands", "@web:seller-terms", legacySellerTerms{Version: current, Content: "Synthetic current terms", UpdatedAt: 1})
	}))
	for _, tc := range []struct {
		name   string
		policy *LegacyCheckoutPolicy
		stale  bool
	}{
		{name: "missing-policy"},
		{name: "missing-version", policy: &LegacyCheckoutPolicy{Accepted: true}, stale: true},
		{name: "outdated-version", policy: &LegacyCheckoutPolicy{Version: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", Accepted: true}, stale: true},
		{name: "not-accepted", policy: &LegacyCheckoutPolicy{Version: current}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			key := "synthetic-terms-" + tc.name
			_, err := f.s.Purchase(ctx, buyerToken, Purchase{ShopID: f.shop.ID, VariantID: f.variant.ID, Buyer: Account{"personal", 2}, Quantity: 1, Key: key, Policy: tc.policy})
			if tc.stale {
				var updated *TermsUpdatedError
				if !errors.As(err, &updated) || updated.RequestKey != key {
					t.Fatalf("expected version conflict for the same request key, got %v", err)
				}
			} else {
				wantError(t, err, ErrSellerTermsRequired)
			}
			if stock := f.stock(t); stock.Reserved != 0 || stock.Claimed != 0 {
				t.Fatalf("rejected terms changed inventory: %+v", stock)
			}
			must(t, f.repo.Within(ctx, f.shop.ID, func(tx Tx) error {
				orders, err := tx.List("orders", "", 10)
				if err != nil {
					return err
				}
				if len(orders) != 0 {
					t.Fatal("rejected terms created an order")
				}
				var acceptance legacyTermsAcceptance
				wantError(t, tx.Get("commands", ID("legacy-seller-terms-v1", int64(2), current), &acceptance), ErrNotFound)
				return nil
			}))
		})
	}
	order, err := f.s.Purchase(ctx, buyerToken, Purchase{ShopID: f.shop.ID, VariantID: f.variant.ID, Buyer: Account{"personal", 2}, Quantity: 1, Key: "synthetic-terms-current", Policy: &LegacyCheckoutPolicy{Version: current, Accepted: true}})
	must(t, err)
	if order.Legacy == nil || order.Legacy.SellerTermsVersion != current || order.Legacy.SellerTermsContent != "Synthetic current terms" || order.Legacy.SellerTermsAcceptedAt <= 0 || order.State != AwaitingPayment {
		t.Fatalf("accepted terms were not frozen in the order: %+v", order)
	}
	if stock := f.stock(t); stock.Reserved != 1 || stock.Claimed != 1 {
		t.Fatalf("valid checkout did not reserve exactly one item: %+v", stock)
	}
	if len(f.funds.commands) != 0 {
		t.Fatal("creating or refusing a checkout moved funds")
	}
}
