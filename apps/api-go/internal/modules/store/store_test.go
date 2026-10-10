package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// Test-only durable image. JSON copies prevent pointer aliasing from hiding
// rollback bugs. Production must inject Postgres, never this implementation.
type memoryRepo struct {
	mu         sync.Mutex
	data       map[string]map[string][]byte
	failCommit atomic.Bool
}
type memoryTx struct{ data map[string][]byte }

func newRepo() *memoryRepo { return &memoryRepo{data: map[string]map[string][]byte{}} }
func (m *memoryRepo) Within(ctx context.Context, scope string, fn func(Tx) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	image := map[string][]byte{}
	for k, v := range m.data[scope] {
		image[k] = append([]byte(nil), v...)
	}
	if err := fn(&memoryTx{image}); err != nil {
		return err
	}
	if m.failCommit.Swap(false) {
		return errors.New("injected commit failure")
	}
	m.data[scope] = image
	return nil
}
func (t *memoryTx) Get(kind, id string, out any) error {
	b, ok := t.data[kind+"/"+id]
	if !ok {
		return ErrNotFound
	}
	return json.Unmarshal(b, out)
}
func (t *memoryTx) Put(kind, id string, v any) error {
	b, err := json.Marshal(v)
	if err == nil {
		t.data[kind+"/"+id] = b
	}
	return err
}
func (t *memoryTx) List(kind, after string, limit int) ([]json.RawMessage, error) {
	keys := []string{}
	for k := range t.data {
		if strings.HasPrefix(k, kind+"/") && strings.TrimPrefix(k, kind+"/") > after {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	out := []json.RawMessage{}
	for _, k := range keys {
		if len(out) == limit {
			break
		}
		out = append(out, json.RawMessage(t.data[k]))
	}
	return out, nil
}

var sellerToken = strings.Repeat("s", 32)
var buyerToken = strings.Repeat("b", 32)
var otherToken = strings.Repeat("x", 32)

type authority struct {
	revoked atomic.Bool
	down    atomic.Bool
}

func (a *authority) Check(_ context.Context, token string, account Account, p Permission) (int64, error) {
	if a.down.Load() {
		return 0, ErrUnavailable
	}
	switch token {
	case sellerToken:
		if a.revoked.Load() {
			return 0, ErrForbidden
		}
		if account == (Account{"personal", 1}) || account == (Account{"team", 10}) {
			return 1, nil
		}
	case buyerToken:
		if account == (Account{"personal", 2}) {
			return 2, nil
		}
	case otherToken:
		if account == (Account{"personal", 3}) {
			return 3, nil
		}
		if account == (Account{"team", 10}) && p == Read {
			return 3, nil
		}
	default:
		return 0, ErrUnauthorized
	}
	return 0, ErrForbidden
}

type fundsProbe struct {
	mu           sync.Mutex
	seen         map[string]Receipt
	commands     map[string]MoneyCommand
	effects      map[string]int
	loseResponse bool
	reject       bool
	bad          bool
	after        func()
}

func newFunds() *fundsProbe {
	return &fundsProbe{seen: map[string]Receipt{}, commands: map[string]MoneyCommand{}, effects: map[string]int{}}
}
func (*fundsProbe) Available() bool { return true }
func (f *fundsProbe) Execute(_ context.Context, _ string, c MoneyCommand) (Receipt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r, ok := f.seen[c.Key]; ok {
		if r.CommandHash != ID(c) {
			return Receipt{}, ErrConflict
		}
		return r, nil
	}
	outcome := "succeeded"
	if f.reject {
		outcome = "rejected"
	}
	r := Receipt{Key: c.Key, CommandHash: ID(c), Reference: "rust:" + c.Key, Outcome: outcome}
	f.seen[c.Key] = r
	f.commands[c.Key] = c
	if !f.reject {
		f.effects[c.Kind]++
	}
	if f.after != nil {
		f.after()
		f.after = nil
	}
	if f.bad {
		r.CommandHash = "invalid"
		return r, nil
	}
	if f.loseResponse {
		f.loseResponse = false
		return Receipt{}, errors.New("response lost after core commit")
	}
	return r, nil
}

type fixture struct {
	s       *Service
	repo    *memoryRepo
	auth    *authority
	funds   *fundsProbe
	shop    Shop
	product Product
	variant Variant
}

func setup(t *testing.T, price int64, stock, quota int64, team bool, enabled bool) *fixture {
	t.Helper()
	f := &fixture{repo: newRepo(), auth: &authority{}, funds: newFunds()}
	var funds Funds
	if enabled {
		funds = f.funds
	}
	var err error
	f.s, err = New(f.repo, f.auth, funds)
	must(t, err)
	owner := Account{"personal", 1}
	if team {
		owner = Account{"team", 10}
	}
	f.catalog(t, CatalogChange{Action: "create_shop", Owner: owner, Title: "Example shop", Key: "shop"}, &f.shop)
	f.catalog(t, CatalogChange{Action: "create_product", Title: "Document", Key: "product"}, &f.product)
	f.catalog(t, CatalogChange{Action: "create_variant", ProductID: f.product.ID, Title: "DOCX", Price: Price{price, "CNY"}, TrackStock: true, Quota: quota, Key: "variant"}, &f.variant)
	if stock > 0 {
		f.catalog(t, CatalogChange{Action: "stock_adjust", ID: f.variant.ID, Delta: stock, Reason: "received actual goods", Key: "receive"}, &f.variant)
	}
	f.catalog(t, CatalogChange{Action: "product_status", ID: f.product.ID, Published: true, Key: "publish"}, &f.product)
	return f
}
func (f *fixture) catalog(t *testing.T, c CatalogChange, out any) {
	t.Helper()
	if c.ShopID == "" {
		c.ShopID = f.shop.ID
	}
	b, err := f.s.Catalog(context.Background(), sellerToken, c)
	must(t, err)
	must(t, json.Unmarshal(b, out))
}
func (f *fixture) purchase(key string) (Order, error) {
	return f.s.Purchase(context.Background(), buyerToken, Purchase{f.shop.ID, f.variant.ID, Account{"personal", 2}, 1, key})
}
func (f *fixture) change(token string, o Order, action, text string) (Order, error) {
	return f.s.ChangeOrder(context.Background(), token, OrderChange{f.shop.ID, o.ID, action, text})
}
func (f *fixture) stock(t *testing.T) Variant {
	t.Helper()
	var v Variant
	must(t, f.repo.Within(context.Background(), f.shop.ID, func(tx Tx) error { return tx.Get("variants", f.variant.ID, &v) }))
	return v
}
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func wantError(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("error=%v want=%v", err, want)
	}
}

func TestCatalogStockQuotaAndReplay(t *testing.T) {
	f := setup(t, 0, 7, -1, false, false)
	for i, quota := range []int64{-1, 5, 0, -1} {
		f.catalog(t, CatalogChange{Action: "quota_set", ID: f.variant.ID, Quota: quota, Key: fmt.Sprint("quota", i)}, &f.variant)
		v := f.stock(t)
		if v.Stock != 7 || v.Reserved != 0 || v.Claimed != 0 {
			t.Fatalf("quota mutated inventory: %+v", v)
		}
	}
	f.catalog(t, CatalogChange{Action: "stock_adjust", ID: f.variant.ID, Delta: 7, Reason: "received actual goods", Key: "receive"}, &f.variant)
	if f.stock(t).Stock != 7 {
		t.Fatal("duplicate stock receipt")
	}
	_, err := f.s.Catalog(context.Background(), sellerToken, CatalogChange{Action: "stock_adjust", ShopID: f.shop.ID, ID: f.variant.ID, Delta: 8, Reason: "received actual goods", Key: "receive"})
	wantError(t, err, ErrConflict)
	f.catalog(t, CatalogChange{Action: "create_variant", ProductID: f.product.ID, Title: "PDF", Price: Price{14900, "CNY"}, Quota: -1, Key: "pdf"}, &Variant{})
	o, err := f.purchase("one")
	must(t, err)
	_, err = f.s.Catalog(context.Background(), sellerToken, CatalogChange{Action: "stock_adjust", ShopID: f.shop.ID, ID: f.variant.ID, Delta: -7, Reason: "correction", Key: "bad-remove"})
	wantError(t, err, ErrConflict)
	f.catalog(t, CatalogChange{Action: "quota_set", ID: f.variant.ID, Quota: 0, Key: "stop"}, &f.variant)
	_, err = f.purchase("two")
	wantError(t, err, ErrSoldOut)
	_, err = f.change(sellerToken, o, "fulfill", "delivered")
	must(t, err)
	if v := f.stock(t); v.Stock != 6 || v.Reserved != 0 || v.Claimed != 1 {
		t.Fatalf("bad fulfillment %+v", v)
	}
}
func TestDisabledPaymentsNeverCreateFreeOrder(t *testing.T) {
	f := setup(t, 9900, 5, -1, false, false)
	_, err := f.purchase("paid")
	wantError(t, err, ErrPaymentUnavailable)
	v := f.stock(t)
	if v.Reserved != 0 || v.Claimed != 0 {
		t.Fatal("unavailable money reserved stock")
	}
	f.catalog(t, CatalogChange{Action: "variant_details", ID: v.ID, Title: "Explicit free sample", Price: Price{0, "CNY"}, Key: "free"}, &f.variant)
	o, err := f.purchase("free")
	must(t, err)
	if o.State != Paid || o.Total.Amount != 0 {
		t.Fatal(o)
	}
	_, err = f.change(buyerToken, o, "cancel", "")
	must(t, err)
	_, err = f.change(buyerToken, o, "cancel", "")
	must(t, err)
	if v = f.stock(t); v.Stock != 5 || v.Reserved != 0 || v.Claimed != 0 {
		t.Fatal(v)
	}
}
func TestConcurrentPurchasesDoNotOversell(t *testing.T) {
	for _, quantity := range []struct{ stock, quota, want int64 }{{5, -1, 5}, {50, 3, 3}, {2, 20, 2}} {
		t.Run(fmt.Sprint(quantity), func(t *testing.T) {
			f := setup(t, 9900, quantity.stock, quantity.quota, false, true)
			var accepted atomic.Int64
			var wg sync.WaitGroup
			for i := 0; i < 80; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					_, err := f.purchase(fmt.Sprint(i))
					if err == nil {
						accepted.Add(1)
					} else if !errors.Is(err, ErrSoldOut) {
						t.Error(err)
					}
				}(i)
			}
			wg.Wait()
			if accepted.Load() != quantity.want {
				t.Fatalf("accepted=%d", accepted.Load())
			}
			v := f.stock(t)
			if v.Stock != quantity.stock || v.Reserved != quantity.want || v.Claimed != quantity.want {
				t.Fatal(v)
			}
		})
	}
}
func TestOrderIdempotencyAndPriceSnapshot(t *testing.T) {
	f := setup(t, 9900, 5, -1, false, true)
	o, err := f.purchase("same")
	must(t, err)
	f.catalog(t, CatalogChange{Action: "variant_details", ID: f.variant.ID, Title: "New title", Price: Price{14900, "CNY"}, Key: "reprice"}, &f.variant)
	repeat, err := f.purchase("same")
	must(t, err)
	if repeat.ID != o.ID || repeat.Total.Amount != 9900 || repeat.Title != "DOCX" {
		t.Fatal(repeat)
	}
	_, err = f.s.Purchase(context.Background(), buyerToken, Purchase{f.shop.ID, f.variant.ID, o.Buyer, 2, "same"})
	wantError(t, err, ErrConflict)
	if f.stock(t).Reserved != 1 {
		t.Fatal("duplicate reservation")
	}
	_, err = f.change(buyerToken, o, "cancel", "")
	must(t, err)
	_, err = f.change(buyerToken, o, "pay", "")
	wantError(t, err, ErrConflict)
	_, err = f.s.Purchase(context.Background(), buyerToken, Purchase{f.shop.ID, f.variant.ID, o.Buyer, math.MaxInt64, "overflow"})
	wantError(t, err, ErrInvalid)
}
func TestLostPaymentResponseAndRestart(t *testing.T) {
	for _, commitFailure := range []bool{false, true} {
		t.Run(fmt.Sprint(commitFailure), func(t *testing.T) {
			f := setup(t, 9900, 3, -1, false, true)
			o, err := f.purchase("order")
			must(t, err)
			if commitFailure {
				f.funds.after = func() { f.repo.failCommit.Store(true) }
			} else {
				f.funds.loseResponse = true
			}
			_, err = f.change(buyerToken, o, "pay", "")
			wantError(t, err, ErrPending)
			for _, action := range []string{"cancel", "request_refund"} {
				_, err = f.change(buyerToken, o, action, "reason")
				wantError(t, err, ErrConflict)
			}
			_, err = f.change(sellerToken, o, "fulfill", "too soon")
			wantError(t, err, ErrConflict)
			f.s, err = New(f.repo, f.auth, f.funds)
			must(t, err) // cold service, same durable DB and Rust ledger
			o, err = f.change(buyerToken, o, "pay", "")
			must(t, err)
			if o.State != Paid || f.funds.effects["pay"] != 1 {
				t.Fatalf("order=%+v effects=%v", o, f.funds.effects)
			}
		})
	}
}
func TestRepeatedMoneyEffectsAndDeliveredRefund(t *testing.T) {
	f := setup(t, 9900, 3, -1, true, true)
	o, err := f.purchase("order")
	must(t, err)
	run := func(token, action, text string) {
		var wg sync.WaitGroup
		for i := 0; i < 40; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := f.change(token, o, action, text)
				if err != nil {
					t.Error(err)
				}
			}()
		}
		wg.Wait()
	}
	run(buyerToken, "pay", "")
	run(sellerToken, "fulfill", "delivery reference")
	run(sellerToken, "settle", "")
	run(buyerToken, "request_refund", "not suitable")
	run(sellerToken, "refund", "")
	result, err := f.s.GetOrder(context.Background(), buyerToken, f.shop.ID, o.ID)
	must(t, err)
	if result.State != Refunded {
		t.Fatal(result)
	}
	v := f.stock(t)
	if v.Stock != 2 || v.Reserved != 0 || v.Claimed != 1 {
		t.Fatalf("refund invented stock: %+v", v)
	}
	for _, kind := range []string{"pay", "settle", "refund"} {
		if f.funds.effects[kind] != 1 {
			t.Fatalf("duplicate %s: %v", kind, f.funds.effects)
		}
	}
	for _, cmd := range f.funds.commands {
		if cmd.Kind == "refund" && (cmd.PaymentRef == "" || cmd.SettlementRef == "" || cmd.Total.Amount != 9900 || cmd.Seller != f.shop.Owner) {
			t.Fatal(cmd)
		}
	}
}
func TestRefundBeforeDeliveryAndRejection(t *testing.T) {
	f := setup(t, 100, 2, 2, false, true)
	o, err := f.purchase("order")
	must(t, err)
	_, err = f.change(buyerToken, o, "pay", "")
	must(t, err)
	_, err = f.change(buyerToken, o, "request_refund", "refund please")
	must(t, err)
	_, err = f.change(sellerToken, o, "fulfill", "blocked")
	wantError(t, err, ErrConflict)
	_, err = f.change(sellerToken, o, "refund", "")
	must(t, err)
	_, err = f.change(sellerToken, o, "refund", "")
	must(t, err)
	v := f.stock(t)
	if v.Stock != 2 || v.Reserved != 0 || v.Claimed != 0 {
		t.Fatal(v)
	}
	o, err = f.purchase("rejected")
	must(t, err)
	f.funds.reject = true
	_, err = f.change(buyerToken, o, "pay", "")
	wantError(t, err, ErrConflict)
	_, err = f.change(buyerToken, o, "pay", "")
	wantError(t, err, ErrConflict)
	if f.stock(t).Reserved != 0 {
		t.Fatal("known rejection kept inventory")
	}
}
func TestAuthorityAndTeamRevocation(t *testing.T) {
	f := setup(t, 100, 2, 2, true, true)
	_, err := f.s.Catalog(context.Background(), otherToken, CatalogChange{Action: "quota_set", ShopID: f.shop.ID, ID: f.variant.ID, Quota: 0, Key: "unauthorized"})
	wantError(t, err, ErrForbidden)
	o, err := f.purchase("order")
	must(t, err)
	_, err = f.s.GetOrder(context.Background(), otherToken, f.shop.ID, o.ID)
	wantError(t, err, ErrForbidden)
	_, err = f.change(buyerToken, o, "fulfill", "not seller")
	wantError(t, err, ErrForbidden)
	f.auth.revoked.Store(true)
	_, err = f.s.Catalog(context.Background(), sellerToken, CatalogChange{Action: "product_status", ShopID: f.shop.ID, ID: f.product.ID, Published: true, Key: "publish"})
	wantError(t, err, ErrForbidden)
	f.auth.down.Store(true)
	_, err = f.purchase("core-down")
	wantError(t, err, ErrUnavailable)
}
func TestInvalidReceiptDoesNotMarkPaid(t *testing.T) {
	f := setup(t, 100, 2, 2, false, true)
	o, err := f.purchase("order")
	must(t, err)
	f.funds.bad = true
	_, err = f.change(buyerToken, o, "pay", "")
	wantError(t, err, ErrPending)
	out, err := f.s.GetOrder(context.Background(), buyerToken, f.shop.ID, o.ID)
	must(t, err)
	if out.State != PaymentPending {
		t.Fatal(out)
	}
	_, err = f.change(buyerToken, o, "pay", "")
	must(t, err)
	if f.funds.effects["pay"] != 1 {
		t.Fatal("retry changed key")
	}
}
func TestHTTPValidationAndPrivateCatalog(t *testing.T) {
	f := setup(t, 100, 2, 2, false, false)
	handler := f.s.Handler()
	for _, body := range []string{`{"action":"create_shop","user_id":1}`, `{} {}`, strings.Repeat("x", 70000)} {
		r := httptest.NewRequest("POST", "/catalog", strings.NewReader(body))
		r.Header.Set("X-LMM-User-Credential", sellerToken)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatalf("status=%d", w.Code)
		}
	}
	r := httptest.NewRequest("GET", "/shops/"+f.shop.ID+"/manage/variants", nil)
	r.Header.Set("X-LMM-User-Credential", sellerToken)
	r.Header.Add("X-LMM-User-Credential", sellerToken)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
	p := Purchase{f.shop.ID, f.variant.ID, Account{"personal", 2}, 1, "http"}
	body, _ := json.Marshal(p)
	r = httptest.NewRequest("POST", "/orders", strings.NewReader(string(body)))
	r.Header.Set("X-LMM-User-Credential", buyerToken)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "payments_unavailable") {
		t.Fatal(w.Code, w.Body.String())
	}
	f.catalog(t, CatalogChange{Action: "product_status", ID: f.product.ID, Published: false, Key: "hide"}, &f.product)
	listing, err := f.s.Browse(context.Background(), f.shop.ID, "", 1)
	must(t, err)
	if len(listing.Items) != 0 || listing.Next == "" {
		t.Fatal("hidden page lost cursor")
	}
	variants, err := f.s.Variants(context.Background(), f.shop.ID, "", 10)
	must(t, err)
	if len(variants.Items) != 0 {
		t.Fatal("draft variants leaked")
	}
}
func TestPostgresTableAllowlistAndNilDependencies(t *testing.T) {
	if _, err := storeTable("core.balances"); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := storeTable("orders; DROP TABLE lmm_store.orders"); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := NewPostgres(nil); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := New(nil, &authority{}, nil); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}
