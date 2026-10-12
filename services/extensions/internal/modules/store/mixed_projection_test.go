package store

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestNativeProductProjectionKeepsUnitsAndRejectsCorruption(t *testing.T) {
	row := LocatedProduct{Owner: Account{Kind: "personal", ID: 1}, Product: Product{ID: "native", Title: "Native", Published: true}, Variants: []Variant{{ID: "v", ProductID: "native", Title: "Native", Price: Price{Unit: "credits", Amount: 123}, Quota: -1}}}
	// The pre-fix legacy-snapshot projector rejects this legitimate native record.
	if _, err := publicView(row); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	for _, price := range []Price{{Unit: "credits", Amount: 123}, {Currency: "USD", Amount: 123}} {
		row.Variants[0].Price = price
		view, err := legacyProductView(row, true, true)
		must(t, err)
		if view["record_kind"] != "native" || view["trading_paused"] != true || len(view["payment_methods"].([]string)) != 0 {
			t.Fatal(view)
		}
		if price.Unit == "credits" {
			if view["price_quota"] != int64(123) {
				t.Fatal(view)
			}
		} else if view["price_quota"] != nil {
			t.Fatal("currency treated as credits")
		}
	}
	row.Variants[0].Price = Price{Unit: "credits", Amount: 123}
	for _, edit := range []func(*LocatedProduct){
		func(r *LocatedProduct) { r.Owner = Account{Kind: "team", ID: 1} },
		func(r *LocatedProduct) { r.Variants = nil },
		func(r *LocatedProduct) { r.Variants[0].Price = Price{} },
		func(r *LocatedProduct) { r.Variants[0].ProductID = "other" },
		func(r *LocatedProduct) { r.Variants[0].Price.Amount = legacySafeInteger + 1 },
		func(r *LocatedProduct) { r.Variants[0].Legacy = &LegacyVariantMetadata{FixedContent: "PRIVATE"} },
		func(r *LocatedProduct) { r.Product.LegacyPublicVersion = 1 },
		func(r *LocatedProduct) { r.Product.LegacyPublic = json.RawMessage(`{"unexpected":"PRIVATE"}`) },
	} {
		copy := row
		copy.Variants = append([]Variant(nil), row.Variants...)
		edit(&copy)
		view, err := legacyProductView(copy, true, true)
		if !errors.Is(err, ErrUnavailable) || view != nil {
			t.Fatalf("bad native view: %v %v", view, err)
		}
	}
}

func TestNativeOrderProjectionNoDeliveryOrInventedMetadata(t *testing.T) {
	row := LocatedOrder{ProductID: "native", Order: Order{ID: "o", Buyer: Account{Kind: "personal", ID: 2}, Seller: Account{Kind: "personal", ID: 1}, Quantity: 1, UnitPrice: Price{Unit: "credits", Amount: 123}, Total: Price{Unit: "credits", Amount: 123}, Delivery: "PRIVATE_DELIVERY", PaymentRef: "PRIVATE_RECEIPT"}}
	for _, state := range []string{AwaitingPayment, PaymentPending, Paid, Fulfilled, SettlementPending, Settled, RefundPending, Refunded, Cancelled} {
		row.Order.State = state
		view, err := legacyOrderView(row)
		must(t, err)
		if view["price_quota"] != int64(123) || view["record_kind"] != "native" || view["created_at"] != nil || view["paid_at"] != nil {
			t.Fatal(view)
		}
		raw, err := json.Marshal(view)
		must(t, err)
		if strings.Contains(string(raw), "PRIVATE") {
			t.Fatal("private order material exposed")
		}
	}
	row.Order.UnitPrice = Price{Currency: "USD", Amount: 123}
	row.Order.Total = row.Order.UnitPrice
	view, err := legacyOrderView(row)
	must(t, err)
	if view["price_quota"] != nil || view["amount_minor"] != int64(123) || view["currency"] != "USD" {
		t.Fatal("native currency price changed")
	}
	for _, edit := range []func(*LocatedOrder){
		func(r *LocatedOrder) { r.Order.State = "unknown" },
		func(r *LocatedOrder) { r.Order.Quantity = 0 },
		func(r *LocatedOrder) { r.Order.Total = Price{} },
		func(r *LocatedOrder) { r.Order.Total.Currency = "EUR" },
		func(r *LocatedOrder) { r.Order.Total.Amount = legacySafeInteger + 1 },
	} {
		copy := row
		edit(&copy)
		view, err := legacyOrderView(copy)
		if !errors.Is(err, ErrUnavailable) || view != nil {
			t.Fatalf("bad order projection: %v %v", view, err)
		}
	}
}
