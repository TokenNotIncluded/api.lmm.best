package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func publicSnapshot(t *testing.T, changes map[string]any) json.RawMessage {
	t.Helper()
	v := map[string]any{"id": "product-1", "seller_id": json.Number("1"), "title": "stale snapshot title", "visibility": "public", "price_quota": json.Number("500000"), "trading_paused": false, "payment_methods": []string{"alipay"}, "review_note": "PRIVATE_REVIEW_NOTE", "reviewed_by": 9}
	for key, value := range changes {
		v[key] = value
	}
	b, err := json.Marshal(v)
	must(t, err)
	return b
}
func publicRow(t *testing.T) LocatedProduct {
	return LocatedProduct{ShopID: "shop", Owner: Account{Kind: "personal", ID: 1}, Product: Product{ID: "product-1", Title: "Current persisted title", Published: true, LegacyPublicVersion: 1, LegacyPublic: publicSnapshot(t, nil)}}
}

func TestPublicViewPreservesExactIntegerCreditsAndDisablesPayments(t *testing.T) {
	for _, credits := range []int64{0, 500000, 9007199254740993, math.MaxInt64} {
		t.Run(strconv.FormatInt(credits, 10), func(t *testing.T) {
			row := publicRow(t)
			row.Product.LegacyPublic = publicSnapshot(t, map[string]any{"price_quota": json.Number(strconv.FormatInt(credits, 10)), "variants": []any{map[string]any{"id": "variant-1", "product_id": "product-1", "name": "Current variant", "price_quota": json.Number(strconv.FormatInt(credits, 10)), "enabled": true}}})
			original := append([]byte(nil), row.Product.LegacyPublic...)
			view, err := publicView(row)
			must(t, err)
			price, ok := view["price_quota"].(json.Number)
			if !ok {
				t.Fatalf("price type %T", view["price_quota"])
			}
			amount, err := price.Int64()
			must(t, err)
			if amount != credits || view["title"] != row.Product.Title || view["trading_paused"] != true || !reflect.DeepEqual(view["payment_methods"], []string{}) || view["review_note"] != "" || view["reviewed_by"] != 0 {
				t.Fatalf("view=%v credits=%d", view, credits)
			}
			variantPrice := view["variants"].([]any)[0].(map[string]any)["price_quota"].(json.Number)
			if variantPrice.String() != price.String() {
				t.Fatalf("variant credits=%s product credits=%s", variantPrice, price)
			}
			encoded, err := json.Marshal(view)
			must(t, err)
			if !strings.Contains(string(encoded), `"price_quota":`+strconv.FormatInt(credits, 10)) || strings.Contains(string(encoded), "PRIVATE_REVIEW_NOTE") {
				t.Fatal(string(encoded))
			}
			if string(row.Product.LegacyPublic) != string(original) {
				t.Fatal("read mutated persisted snapshot")
			}
			view["price_quota"] = json.Number("1")
			again, err := publicView(row)
			must(t, err)
			if again["price_quota"].(json.Number).String() != strconv.FormatInt(credits, 10) {
				t.Fatal("caller mutation escaped into persisted snapshot")
			}
		})
	}
}

func TestPublicViewRejectsPrivateOrUntrustedSnapshots(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*LocatedProduct)
		want error
	}{
		{"unpublished", func(r *LocatedProduct) { r.Product.Published = false }, ErrNotFound},
		{"unversioned", func(r *LocatedProduct) { r.Product.LegacyPublicVersion = 0 }, ErrUnavailable},
		{"unknown-version", func(r *LocatedProduct) { r.Product.LegacyPublicVersion = 2 }, ErrUnavailable},
		{"absent-snapshot", func(r *LocatedProduct) { r.Product.LegacyPublic = nil }, ErrUnavailable},
		{"wrong-resource", func(r *LocatedProduct) { r.Product.ID = "other-product" }, ErrUnavailable},
		{"wrong-owner", func(r *LocatedProduct) { r.Owner.ID = 2 }, ErrUnavailable},
		{"team-owner", func(r *LocatedProduct) { r.Owner.Kind = "team" }, ErrUnavailable},
		{"missing-owner", func(r *LocatedProduct) { r.Owner = Account{} }, ErrUnavailable},
		{"private", func(r *LocatedProduct) {
			r.Product.LegacyPublic = publicSnapshot(t, map[string]any{"visibility": "private"})
		}, ErrNotFound},
		{"restricted", func(r *LocatedProduct) {
			r.Product.LegacyPublic = publicSnapshot(t, map[string]any{"visibility": "unlisted"})
		}, ErrNotFound},
		{"null-visibility", func(r *LocatedProduct) { r.Product.LegacyPublic = publicSnapshot(t, map[string]any{"visibility": nil}) }, ErrUnavailable},
		{"object-visibility", func(r *LocatedProduct) {
			r.Product.LegacyPublic = publicSnapshot(t, map[string]any{"visibility": map[string]any{"private": true}})
		}, ErrUnavailable},
		{"array-visibility", func(r *LocatedProduct) {
			r.Product.LegacyPublic = publicSnapshot(t, map[string]any{"visibility": []string{"private"}})
		}, ErrUnavailable},
		{"unknown-private-field", func(r *LocatedProduct) {
			r.Product.LegacyPublic = publicSnapshot(t, map[string]any{"pickup_code": "PRIVATE_CODE"})
		}, ErrUnavailable},
		{"unknown-ciphertext", func(r *LocatedProduct) {
			r.Product.LegacyPublic = publicSnapshot(t, map[string]any{"ciphertext": "PRIVATE_CIPHER"})
		}, ErrUnavailable},
		{"nested-private-variant", func(r *LocatedProduct) {
			r.Product.LegacyPublic = publicSnapshot(t, map[string]any{"variants": []any{map[string]any{"id": "v", "price_quota": 500000, "pickup_code": "PRIVATE_CODE"}}})
		}, ErrUnavailable},
		{"nested-private-link", func(r *LocatedProduct) {
			r.Product.LegacyPublic = publicSnapshot(t, map[string]any{"links": []any{map[string]any{"url": "https://example.invalid", "provider_secret": "PRIVATE_SECRET"}}})
		}, ErrUnavailable},
		{"object-in-string-list", func(r *LocatedProduct) {
			r.Product.LegacyPublic = publicSnapshot(t, map[string]any{"image_urls": []any{map[string]any{"secret": "PRIVATE"}}})
		}, ErrUnavailable},
		{"object-in-scalar", func(r *LocatedProduct) {
			r.Product.LegacyPublic = publicSnapshot(t, map[string]any{"description": map[string]any{"private": "PRIVATE"}})
		}, ErrUnavailable},
		{"nested-object-in-variant", func(r *LocatedProduct) {
			r.Product.LegacyPublic = publicSnapshot(t, map[string]any{"variants": []any{map[string]any{"template": map[string]any{"secret": "PRIVATE"}}}})
		}, ErrUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := publicRow(t)
			tc.edit(&row)
			view, err := publicView(row)
			if !errors.Is(err, tc.want) || view != nil {
				t.Fatalf("view=%v err=%v want=%v", view, err, tc.want)
			}
		})
	}
}

func TestPublicViewRejectsMalformedOrNonIntegerPrices(t *testing.T) {
	for _, price := range []any{nil, "500000", -1, 1.5, json.Number("1e6"), json.Number("9223372036854775808"), true, []int{1}} {
		t.Run(fmt.Sprintf("%v", price), func(t *testing.T) {
			row := publicRow(t)
			row.Product.LegacyPublic = publicSnapshot(t, map[string]any{"price_quota": price})
			view, err := publicView(row)
			if !errors.Is(err, ErrUnavailable) || view != nil {
				t.Fatalf("view=%v err=%v", view, err)
			}
		})
	}
	for _, raw := range []string{``, `null`, `[]`, `{"id":`, `{"id":"product-1","seller_id":1}`, `{"id":"product-1","seller_id":"1","price_quota":500000}`, `{"id":"product-1","seller_id":1.5,"price_quota":500000}`, `{"id":"product-1","seller_id":1,"price_quota":500000} {}`, `{"id":"product-1","seller_id":1,"price_quota":500000} trailing`} {
		row := publicRow(t)
		row.Product.LegacyPublic = json.RawMessage(raw)
		view, err := publicView(row)
		if !errors.Is(err, ErrUnavailable) || view != nil {
			t.Fatalf("raw=%q view=%v err=%v", raw, view, err)
		}
	}
	for _, price := range []any{-1, 1.5, "500000", json.Number("9223372036854775808")} {
		row := publicRow(t)
		row.Product.LegacyPublic = publicSnapshot(t, map[string]any{"variants": []any{map[string]any{"id": "v", "price_quota": price}}})
		view, err := publicView(row)
		if !errors.Is(err, ErrUnavailable) || view != nil {
			t.Fatalf("variant price=%v view=%v err=%v", price, view, err)
		}
	}
}

type webCatalogueRepo struct {
	*memoryRepo
	rows []LocatedProduct
	err  error
}

func (r *webCatalogueRepo) FindProduct(_ context.Context, id string) (LocatedProduct, error) {
	if r.err != nil {
		return LocatedProduct{}, r.err
	}
	for _, row := range r.rows {
		if row.Product.ID == id {
			return row, nil
		}
	}
	return LocatedProduct{}, ErrNotFound
}
func (r *webCatalogueRepo) FindOrder(context.Context, string) (LocatedOrder, error) {
	return LocatedOrder{}, ErrNotFound
}
func (r *webCatalogueRepo) ListPublished(context.Context, int64, string, int, int) ([]LocatedProduct, error) {
	return r.rows, r.err
}

func TestPublicCatalogueFailsClosedInsteadOfReturningPartialOrFreeItems(t *testing.T) {
	ctx := context.Background()
	healthy := publicRow(t)
	bad := publicRow(t)
	bad.Product.ID = "missing-price"
	bad.Product.LegacyPublic = nil
	repo := &webCatalogueRepo{memoryRepo: newRepo(), rows: []LocatedProduct{healthy, bad}}
	service, err := New(repo, &authority{}, nil)
	must(t, err)
	rows, err := service.PublicCatalogue(ctx, 0, "", 0, 30)
	if !errors.Is(err, ErrUnavailable) || rows != nil {
		t.Fatalf("partial rows=%v err=%v", rows, err)
	}
	value, err := service.PublicProduct(ctx, bad.Product.ID)
	if !errors.Is(err, ErrUnavailable) || value != nil {
		t.Fatalf("fabricated free value=%v err=%v", value, err)
	}
	repo.rows = []LocatedProduct{healthy}
	rows, err = service.PublicCatalogue(ctx, 0, "", 0, 30)
	must(t, err)
	if len(rows) != 1 || rows[0]["price_quota"].(json.Number).String() != "500000" {
		t.Fatal(rows)
	}
	repo.rows = nil
	rows, err = service.PublicCatalogue(ctx, 0, "", 0, 30)
	must(t, err)
	if rows == nil || len(rows) != 0 {
		t.Fatalf("empty catalogue=%v", rows)
	}
	repo.err = ErrUnavailable
	value, err = service.PublicProduct(ctx, healthy.Product.ID)
	if !errors.Is(err, ErrUnavailable) || value != nil {
		t.Fatalf("value=%v err=%v", value, err)
	}
}
