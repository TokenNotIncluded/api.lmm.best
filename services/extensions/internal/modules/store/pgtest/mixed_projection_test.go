package pgtest

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/store"
)

// Handcrafted persisted business records exercise projections only. No payment,
// refund, delivery execution or Core database access occurs in this fixture.
func TestPostgresMixedNativeLegacyProjection(t *testing.T) {
	db := legacyDatabase(t)
	repo, err := store.NewPostgres(db)
	must(t, err)
	svc, err := store.New(repo, legacyAuthority{}, nil)
	must(t, err)
	ctx := context.Background()
	seller := store.Account{Kind: "personal", ID: 1}
	buyer := store.Account{Kind: "personal", ID: 2}
	outsider := store.Account{Kind: "personal", ID: 3}
	credits := store.Price{Unit: "credits", Amount: 700001}
	usd := store.Price{Currency: "USD", Amount: 1299}
	must(t, repo.Within(ctx, "mixed-shop", func(tx store.Tx) error {
		values := []struct {
			table, id string
			body      any
		}{
			{"shops", "mixed-shop", store.Shop{ID: "mixed-shop", Owner: seller, Title: "Synthetic"}},
			{"products", "native", store.Product{ID: "native", Title: "Native credit product", Published: true}},
			{"products", "currency", store.Product{ID: "currency", Title: "Native currency product", Published: true}},
			{"products", "legacy", store.Product{ID: "legacy", Title: "Legacy product", Published: true, Legacy: &store.LegacyProductMetadata{Approved: true, Status: "published", Visibility: "public", DefaultVariantID: "legacy-v", Template: "fixed-content", PurchaseLoginRequired: true}}},
			{"products", "draft", store.Product{ID: "draft", Title: "Unpublished"}},
			{"products", "private", store.Product{ID: "private", Title: "Private", Published: true, Legacy: &store.LegacyProductMetadata{Approved: true, Status: "published", Visibility: "private", DefaultVariantID: "private-v"}}},
			{"variants", "native-v", store.Variant{ID: "native-v", ProductID: "native", Title: "Native", Price: credits, Quota: -1}},
			{"variants", "currency-v", store.Variant{ID: "currency-v", ProductID: "currency", Title: "USD", Price: usd, Quota: -1}},
			{"variants", "legacy-v", store.Variant{ID: "legacy-v", ProductID: "legacy", Title: "Legacy", Price: credits, Quota: -1, Legacy: &store.LegacyVariantMetadata{Enabled: true, Template: "fixed-content", FixedContent: "PRIVATE_DELIVERY"}}},
		}
		for _, v := range values {
			if err := tx.Put(v.table, v.id, v.body); err != nil {
				return err
			}
		}
		orders := []store.Order{
			{ID: "a-native", Buyer: buyer, Seller: seller, VariantID: "native-v", ProductTitle: "Native", Title: "Native", Quantity: 1, UnitPrice: credits, Total: credits, State: store.Settled, Delivered: true, Delivery: "PRIVATE_DELIVERY", PaymentRef: "PRIVATE_RECEIPT"},
			{ID: "b-legacy", Buyer: buyer, Seller: seller, VariantID: "legacy-v", Quantity: 1, UnitPrice: credits, Total: credits, State: store.Paid, Delivery: "PRIVATE_DELIVERY", Legacy: &store.LegacyOrderMetadata{ProductID: "legacy", CreatedAt: 123, PaymentMethod: "balance", DeliveryTemplate: "fixed-content"}},
			{ID: "c-sale", Buyer: outsider, Seller: buyer, VariantID: "currency-v", Quantity: 1, UnitPrice: usd, Total: usd, State: store.Cancelled},
			{ID: "d-foreign", Buyer: outsider, Seller: seller, VariantID: "native-v", Quantity: 1, UnitPrice: credits, Total: credits, State: store.Refunded, Delivery: "FOREIGN_PRIVATE"},
		}
		for _, o := range orders {
			if err := tx.Put("orders", o.ID, o); err != nil {
				return err
			}
		}
		return nil
	}))
	// JSON NULL/absent optional metadata must behave as Go's nil/zero defaults.
	_, err = db.Exec(`UPDATE lmm_store.products SET body=body || '{"legacy":null,"legacy_public":null}'::jsonb WHERE id='native'; UPDATE lmm_store.variants SET body=(body-'stock'-'reserved'-'claimed') || '{"track_stock":null,"legacy":null}'::jsonb WHERE id='native-v'; UPDATE lmm_store.orders SET body=body || '{"legacy":null}'::jsonb WHERE id='a-native'`)
	must(t, err)
	t.Run("public_catalogue", func(t *testing.T) {
		for _, sort := range []string{"comprehensive", "newest", "sales"} {
			items, err := svc.PublicCatalogueQuery(ctx, store.CatalogueQuery{Sort: sort, Limit: 24})
			must(t, err)
			if len(items) != 3 {
				t.Fatalf("%s: %d products", sort, len(items))
			}
			for offset := 0; offset <= 3; offset++ {
				page, err := svc.PublicCatalogueQuery(ctx, store.CatalogueQuery{Sort: sort, Offset: offset, Limit: 1})
				must(t, err)
				if offset < 3 {
					if len(page) != 1 || page[0]["id"] != items[offset]["id"] {
						t.Fatal("mixed catalogue pagination lost or duplicated a record")
					}
				} else if len(page) != 0 {
					t.Fatal("catalogue end offset ignored")
				}
			}
			kinds := map[string]string{}
			for _, item := range items {
				kinds[item["id"].(string)] = item["record_kind"].(string)
			}
			if kinds["native"] != "native" || kinds["currency"] != "native" || kinds["legacy"] != "legacy" {
				t.Fatal(kinds)
			}
			raw, err := json.Marshal(items)
			must(t, err)
			if strings.Contains(string(raw), "PRIVATE") {
				t.Fatal("private delivery leaked")
			}
		}
		for _, stock := range []string{"in_stock", "out_of_stock"} {
			located, err := repo.ListPublishedQuery(ctx, store.CatalogueQuery{Stock: stock, Sort: "comprehensive", Limit: 24, FundsReady: true})
			must(t, err)
			want := 2 // Native records cannot use legacy Web checkout.
			if stock == "in_stock" {
				want = 1
			}
			if len(located) != want {
				t.Fatalf("%s: got %d, want %d", stock, len(located), want)
			}
		}
		currency, err := svc.PublicProduct(ctx, "currency")
		must(t, err)
		if currency["price_quota"] != nil || currency["trading_paused"] != true {
			t.Fatal("currency relabeled as credits or native checkout advertised")
		}
		native, err := svc.PublicProduct(ctx, "native")
		must(t, err)
		if native["price_quota"] != credits.Amount || native["created_at"] != nil {
			t.Fatal("native price or unknown timestamp fabricated")
		}
		for _, id := range []string{"draft", "private"} {
			_, err := svc.PublicProduct(ctx, id)
			if !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("%s: %v", id, err)
			}
		}
	})
	t.Run("role_order_pages", func(t *testing.T) {
		for _, tc := range []struct {
			token, role string
			actor       store.Account
			total       int64
			ids         []string
		}{
			{"buyer", "buyer", buyer, 2, []string{"a-native", "b-legacy"}},
			{"buyer", "seller", buyer, 1, []string{"c-sale"}},
			{"seller", "buyer", seller, 0, nil},
			{"seller", "seller", seller, 3, []string{"a-native", "b-legacy", "d-foreign"}},
		} {
			for offset := 0; offset <= int(tc.total)+1; offset++ {
				value, err := svc.Legacy(ctx, tc.token, tc.actor, store.LegacyRequest{Action: "my_orders", Role: tc.role, Offset: offset, Limit: 1})
				must(t, err)
				page := value.(map[string]any)
				items := page["items"].([]map[string]any)
				if page["total"] != tc.total || page["has_more"] != (int64(offset+len(items)) < tc.total) {
					t.Fatalf("%s/%s/%d bad total/page: %v", tc.token, tc.role, offset, page)
				}
				if offset < len(tc.ids) {
					if len(items) != 1 || items[0]["id"] != tc.ids[offset] {
						t.Fatal("wrong account/role page")
					}
				} else if len(items) != 0 {
					t.Fatal("beyond-end page not empty")
				}
				raw, err := json.Marshal(page)
				must(t, err)
				if strings.Contains(string(raw), "PRIVATE") || strings.Contains(string(raw), "FOREIGN_PRIVATE") {
					t.Fatal("private fields leaked")
				}
			}
		}
		value, err := svc.Legacy(ctx, "buyer", buyer, store.LegacyRequest{Action: "get_order", ID: "a-native"})
		must(t, err)
		if value.(map[string]any)["record_kind"] != "native" {
			t.Fatal("native order hidden")
		}
		_, err = svc.Legacy(ctx, "outsider", outsider, store.LegacyRequest{Action: "get_order", ID: "a-native"})
		if !errors.Is(err, store.ErrForbidden) {
			t.Fatalf("foreign read: %v", err)
		}
	})
	// Schema-valid prices outside the Web integer range remain errors, never
	// silently dropped rows (the schema itself rejects NULL monetary amounts).
	_, err = db.Exec(`UPDATE lmm_store.variants SET body=jsonb_set(body,'{price}','{"amount_credits":9007199254740992,"unit":"credits"}') WHERE id='native-v'`)
	must(t, err)
	items, err := svc.PublicCatalogueQuery(ctx, store.CatalogueQuery{Limit: 24})
	if err == nil || items != nil {
		t.Fatal("corrupt record converted into successful partial catalogue")
	}
}
