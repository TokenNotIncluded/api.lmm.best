// These tests validate real PostgreSQL persistence with an explicitly synthetic
// authority and money port. They do not substitute for real Core RPC acceptance.
package pgtest

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/store"
)

type legacyAuthority struct{}

func (legacyAuthority) Check(_ context.Context, token string, a store.Account, _ store.Permission) (int64, error) {
	id := map[string]int64{"seller": 1, "buyer": 2, "outsider": 3, "admin": 4}[token]
	if id > 0 && a == (store.Account{Kind: "personal", ID: id}) {
		return id, nil
	}
	return 0, store.ErrForbidden
}
func (legacyAuthority) CheckAdmin(_ context.Context, token string, _ bool) (int64, error) {
	if token == "admin" {
		return 4, nil
	}
	return 0, store.ErrForbidden
}

func legacyDatabase(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("LMM_STORE_TEST_DSN")
	if dsn == "" {
		t.Skip("set LMM_STORE_TEST_DSN to an isolated empty *_test DB")
	}
	db, err := sql.Open("postgres", dsn)
	must(t, err)
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(4)
	var name string
	must(t, db.QueryRow(`SELECT current_database()`).Scan(&name))
	if !strings.HasSuffix(name, "_test") {
		t.Fatal("refusing non-test database")
	}
	var exists bool
	must(t, db.QueryRow(`SELECT EXISTS(SELECT FROM pg_namespace WHERE nspname='lmm_store')`).Scan(&exists))
	if exists {
		t.Fatal("fresh standalone test DB required")
	}
	_, err = db.Exec(store.Schema)
	must(t, err)
	t.Cleanup(func() { _, _ = db.Exec(`DROP SCHEMA lmm_store CASCADE`) })
	return db
}

func TestPostgresLegacyReviewedCatalogueAndOrders(t *testing.T) {
	db := legacyDatabase(t)
	repo, err := store.NewPostgres(db)
	must(t, err)
	ledger := &funds{seen: map[string]store.Receipt{}}
	s, err := store.New(repo, legacyAuthority{}, ledger)
	must(t, err)
	ctx := context.Background()
	call := func(token string, r store.LegacyRequest) (any, error) {
		return s.Legacy(ctx, token, store.Account{Kind: "personal", ID: map[string]int64{"seller": 1, "buyer": 2, "outsider": 3, "admin": 4}[token]}, r)
	}
	invoke := func(token string, r store.LegacyRequest) map[string]any {
		t.Helper()
		v, err := call(token, r)
		must(t, err)
		if v == nil {
			return nil
		}
		return v.(map[string]any)
	}
	body := func(v any) json.RawMessage { b, e := json.Marshal(v); must(t, e); return b }
	terms := invoke("seller", store.LegacyRequest{Action: "save_terms", Body: body(map[string]any{"content": "Synthetic seller terms", "expected_version": ""})})
	input := map[string]any{"title": "Synthetic Alpha", "description": "Unique description needle %_", "image_urls": []string{}, "contact": "Synthetic contact", "links": []any{}, "price_quota": int64(500001), "template": "fixed-content", "fixed_content": "Synthetic private delivery", "delivery_strategy": "sequential", "payment_methods": []string{"balance"}, "pickup_login_required": true, "pickup_code_required": false, "email_pickup_link": false}
	p := invoke("seller", store.LegacyRequest{Action: "save_product", Body: body(input)})
	id := p["id"].(string)
	if _, e := s.PublicProduct(ctx, id); !errors.Is(e, store.ErrNotFound) {
		t.Fatalf("draft visible: %v", e)
	}
	if _, e := call("outsider", store.LegacyRequest{Action: "draft_product", ID: id}); !errors.Is(e, store.ErrForbidden) {
		t.Fatalf("outsider draft: %v", e)
	}
	invoke("seller", store.LegacyRequest{Action: "submit_product", ID: id})
	if _, e := call("seller", store.LegacyRequest{Action: "review_product", ID: id, Body: body(map[string]any{"approve": true, "note": "private review"})}); !errors.Is(e, store.ErrForbidden) {
		t.Fatalf("seller self review: %v", e)
	}
	invoke("admin", store.LegacyRequest{Action: "review_product", ID: id, Body: body(map[string]any{"approve": true, "note": "Synthetic private review"})})
	pub, err := s.PublicProduct(ctx, id)
	must(t, err)
	raw := body(pub)
	if strings.Contains(string(raw), "Synthetic private delivery") || strings.Contains(string(raw), "Synthetic private review") {
		t.Fatal("private material leaked")
	}
	if pub["price_quota"] != int64(500001) || pub["status"] != "published" {
		t.Fatalf("public DTO wrong: %#v", pub)
	}
	for _, sort := range []string{"comprehensive", "sales", "newest"} {
		rows, err := s.PublicCatalogueQuery(ctx, store.CatalogueQuery{Text: "description needle %_", Sort: sort, Limit: 24})
		must(t, err)
		if len(rows) != 1 || rows[0]["id"] != id {
			t.Fatalf("query %s did not match description literally", sort)
		}
	}
	rows, err := s.PublicCatalogueQuery(ctx, store.CatalogueQuery{Text: "nonexistent", Sort: "newest", Limit: 1})
	must(t, err)
	if len(rows) != 0 {
		t.Fatal("search returned unrelated product")
	}
	rows, err = s.PublicCatalogueQuery(ctx, store.CatalogueQuery{Offset: 1, Limit: 1, Sort: "sales"})
	must(t, err)
	if len(rows) != 0 {
		t.Fatal("offset ignored")
	}
	invoke("seller", store.LegacyRequest{Action: "sale_limit", ID: id, Body: body(map[string]any{"sale_limit": int64(1)})})
	invoke("buyer", store.LegacyRequest{Action: "accept_disclaimer", Body: body(map[string]any{"version": "merchant-store-v1", "accepted": true})})
	checkout := map[string]any{"product_id": id, "quantity": int64(1), "payment_method": "balance", "request_key": "synthetic-order-1", "disclaimer_version": "merchant-store-v1", "seller_terms_version": terms["version"], "accept_seller_terms": true}
	created := invoke("buyer", store.LegacyRequest{Action: "create_order", Body: body(checkout)})
	order := created["order"].(map[string]any)
	oid := order["id"].(string)
	if created["created"] != true || order["price_quota"] != int64(500001) || order["seller_terms_content"] != "Synthetic seller terms" {
		t.Fatal("order snapshot wrong")
	}
	rows, err = s.PublicCatalogueQuery(ctx, store.CatalogueQuery{Stock: "in_stock", Limit: 24})
	must(t, err)
	if len(rows) != 0 {
		t.Fatal("global sale limit ignored by stock filter")
	}
	rows, err = s.PublicCatalogueQuery(ctx, store.CatalogueQuery{Stock: "out_of_stock", Limit: 24})
	must(t, err)
	if len(rows) != 1 {
		t.Fatal("out-of-stock filter mismatch")
	}
	invoke("seller", store.LegacyRequest{Action: "unlist_product", ID: id})
	replay := invoke("buyer", store.LegacyRequest{Action: "create_order", Body: body(checkout)})
	if replay["created"] != false || replay["order"].(map[string]any)["id"] != oid {
		t.Fatal("replay after unlist changed immutable order")
	}
	if _, e := call("outsider", store.LegacyRequest{Action: "get_order", ID: oid}); !errors.Is(e, store.ErrForbidden) {
		t.Fatalf("outsider order read: %v", e)
	}
	for _, role := range []string{"buyer", "seller"} {
		v := invoke(role, store.LegacyRequest{Action: "my_orders", Role: role, Limit: 24})
		if len(v["items"].([]map[string]any)) != 1 {
			t.Fatalf("%s list missing order", role)
		}
	}
	invoke("buyer", store.LegacyRequest{Action: "cancel_order", ID: oid})
	invoke("seller", store.LegacyRequest{Action: "delete_product", ID: id})
	if _, e := call("seller", store.LegacyRequest{Action: "listing", ID: id, Body: body(map[string]any{"listed": true})}); !errors.Is(e, store.ErrNotFound) {
		t.Fatalf("deleted product resurrected: %v", e)
	}
}
