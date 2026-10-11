package pgtest

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/store"
)

type priceWindowRepository struct {
	*store.Postgres
	shop    string
	once    sync.Once
	reprice func()
}

func (r *priceWindowRepository) Within(ctx context.Context, shop string, fn func(store.Tx) error) error {
	if shop == r.shop {
		r.once.Do(r.reprice)
	}
	return r.Postgres.Within(ctx, shop, fn)
}

func TestPostgresLegacyCheckoutRereadsPriceBeforeReservation(t *testing.T) {
	db := legacyDatabase(t)
	repo, err := store.NewPostgres(db)
	must(t, err)
	ledger := &funds{seen: map[string]store.Receipt{}}
	s, err := store.New(repo, legacyAuthority{}, ledger)
	must(t, err)
	ctx := context.Background()
	body := func(v any) json.RawMessage { b, e := json.Marshal(v); must(t, e); return b }
	call := func(token string, r store.LegacyRequest) any {
		t.Helper()
		v, e := s.Legacy(ctx, token, store.Account{Kind: "personal", ID: map[string]int64{"seller": 1, "buyer": 2, "admin": 4}[token]}, r)
		must(t, e)
		return v
	}
	terms := call("seller", store.LegacyRequest{Action: "save_terms", Body: body(map[string]any{"content": "Synthetic changing price terms", "expected_version": ""})}).(map[string]any)
	p := call("seller", store.LegacyRequest{Action: "save_product", Body: body(map[string]any{"title": "Synthetic Repriced", "description": "Concurrent price window fixture", "price_quota": 500001, "template": "fixed-content", "fixed_content": "Synthetic private material", "payment_methods": []string{"balance"}, "pickup_login_required": true})}).(map[string]any)
	id := p["id"].(string)
	call("seller", store.LegacyRequest{Action: "submit_product", ID: id})
	call("admin", store.LegacyRequest{Action: "review_product", ID: id, Body: body(map[string]any{"approve": true})})
	call("buyer", store.LegacyRequest{Action: "accept_disclaimer", Body: body(map[string]any{"version": "merchant-store-v1", "accepted": true})})
	row, err := repo.FindProduct(ctx, id)
	must(t, err)
	// The wrapper commits another real PostgreSQL transaction after Legacy has
	// read the old public product, immediately before Purchase's locked reread.
	changed := false
	window := &priceWindowRepository{Postgres: repo, shop: row.ShopID, reprice: func() {
		_, e := db.Exec(`UPDATE lmm_store.variants SET body=jsonb_set(body,'{price}',$1::jsonb) WHERE shop_id=$2 AND id=$3`, string(body(store.Price{Amount: 9007199254740991, Unit: "credits"})), row.ShopID, row.Product.Legacy.DefaultVariantID)
		must(t, e)
		changed = true
	}}
	racing, err := store.New(window, legacyAuthority{}, ledger)
	must(t, err)
	_, err = racing.Legacy(ctx, "buyer", store.Account{Kind: "personal", ID: 2}, store.LegacyRequest{Action: "create_order", Body: body(map[string]any{"product_id": id, "quantity": 2, "payment_method": "balance", "request_key": "synthetic-reprice-window", "disclaimer_version": "merchant-store-v1", "seller_terms_version": terms["version"], "accept_seller_terms": true})})
	if !changed || !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("unsafe real transaction price was accepted: changed=%v err=%v", changed, err)
	}
	var orders, accepted int
	must(t, db.QueryRow(`SELECT count(*) FROM lmm_store.orders`).Scan(&orders))
	must(t, db.QueryRow(`SELECT count(*) FROM lmm_store.commands WHERE body ? 'user_id'`).Scan(&accepted))
	var variant store.Variant
	must(t, repo.Within(ctx, row.ShopID, func(tx store.Tx) error { return tx.Get("variants", row.Product.Legacy.DefaultVariantID, &variant) }))
	if orders != 0 || accepted != 0 || variant.Reserved != 0 || variant.Claimed != 0 || variant.Price.Amount != 9007199254740991 {
		t.Fatal("rejected checkout left order, acceptance, or reservation effects")
	}
}

func TestPostgresGlobalResourceConflictUsesSQLState(t *testing.T) {
	db := legacyDatabase(t)
	repo, err := store.NewPostgres(db)
	must(t, err)
	ctx := context.Background()
	price := store.Price{Amount: 1, Unit: "credits"}
	for _, tc := range []struct {
		table string
		value any
	}{
		{"products", store.Product{ID: "synthetic-shared-product", Title: "Synthetic"}},
		{"variants", store.Variant{ID: "synthetic-shared-variant", ProductID: "synthetic-shared-product", Title: "Synthetic", Price: price, Quota: -1}},
		{"orders", store.Order{ID: "synthetic-shared-order", Quantity: 1, UnitPrice: price, Total: price, State: store.AwaitingPayment}},
	} {
		t.Run(tc.table, func(t *testing.T) {
			id := "synthetic-shared-" + tc.table
			must(t, repo.Within(ctx, "synthetic-shop-one", func(tx store.Tx) error { return tx.Put(tc.table, id, tc.value) }))
			err := repo.Within(ctx, "synthetic-shop-two", func(tx store.Tx) error {
				if err := tx.Put("commands", "rollback-"+tc.table, map[string]string{"synthetic": "rollback"}); err != nil {
					return err
				}
				return tx.Put(tc.table, id, tc.value)
			})
			if !errors.Is(err, store.ErrConflict) {
				t.Fatalf("global duplicate was not ErrConflict: %v", err)
			}
			var effects int
			must(t, db.QueryRow(`SELECT count(*) FROM lmm_store.commands WHERE shop_id='synthetic-shop-two' AND id=$1`, "rollback-"+tc.table).Scan(&effects))
			if effects != 0 {
				t.Fatal("conflict did not roll back transaction")
			}
		})
	}
}
