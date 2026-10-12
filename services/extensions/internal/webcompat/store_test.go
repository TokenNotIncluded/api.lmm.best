package webcompat

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	pb "github.com/TokenNotIncluded/api.lmm.best/extensions/internal/corepb"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Fake ports exercise routing and forwarding only. They do not prove Rust RPC,
// catalogue import, PostgreSQL persistence, or actual payment availability.
type storeTransportFake struct {
	calls                                         []string
	seller                                        int64
	query                                         string
	offset, limit                                 int
	token, shop, id                               string
	rows                                          []map[string]any
	product                                       map[string]any
	located                                       store.LocatedOrder
	catalogueErr, productErr, locateErr, orderErr error
	catalogueFn                                   func(context.Context, int64, string, int, int) ([]map[string]any, error)
}

func (s *storeTransportFake) PublicCatalogue(ctx context.Context, seller int64, q string, offset, limit int) ([]map[string]any, error) {
	s.calls = append(s.calls, "catalogue")
	s.seller, s.query, s.offset, s.limit = seller, q, offset, limit
	if s.catalogueFn != nil {
		return s.catalogueFn(ctx, seller, q, offset, limit)
	}
	return s.rows, s.catalogueErr
}
func (s *storeTransportFake) PublicProduct(_ context.Context, id string) (map[string]any, error) {
	s.calls = append(s.calls, "product")
	s.id = id
	return s.product, s.productErr
}
func (s *storeTransportFake) LocateOrder(_ context.Context, id string) (store.LocatedOrder, error) {
	s.calls = append(s.calls, "locate")
	s.id = id
	return s.located, s.locateErr
}
func (s *storeTransportFake) GetOrder(_ context.Context, token, shop, id string) (store.Order, error) {
	s.calls = append(s.calls, "order")
	s.token, s.shop, s.id = token, shop, id
	return s.located.Order, s.orderErr
}
func newStoreTransportFake() *storeTransportFake {
	return &storeTransportFake{product: map[string]any{"id": "product-1", "price_quota": json.Number("500000"), "trading_paused": true, "payment_methods": []string{}}, located: store.LocatedOrder{ShopID: "shop-1", Order: store.Order{ID: "order-1", Buyer: store.Account{Kind: "personal", ID: 17}, Seller: store.Account{Kind: "personal", ID: 99}, Total: store.Price{Amount: 123, Currency: "CNY"}, State: store.AwaitingPayment}}}
}
func storeTransportMux(t *testing.T, core Core, service StoreService) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	RegisterStore(mux, newAuth(t, core), service)
	return mux
}
func storeTransportRequest(method, path string, headers []string) *http.Request {
	r := httptest.NewRequest(method, path, nil)
	if headers != nil {
		r.Header["Authorization"] = headers
	}
	return r
}

func TestStoreTransportPublicCatalogueAndProductAllowAnonymousReads(t *testing.T) {
	for _, path := range []string{"/api/store/products", "/api/store/products/product-1"} {
		core, service := &authCore{}, newStoreTransportFake()
		r := storeTransportRequest("GET", path, nil)
		r.Header.Set("Cookie", "session="+hostToken)
		r.Header.Set("X-LMM-User-Credential", hostToken)
		r.Header.Set("X-LMM-User-ID", "999")
		rec := httptest.NewRecorder()
		storeTransportMux(t, core, service).ServeHTTP(rec, r)
		body := supportEnvelope(t, rec)
		if rec.Code != 200 || body["success"] != true || core.calls != 0 || len(service.calls) != 1 {
			t.Fatalf("path=%s status=%d core=%d service=%v body=%v", path, rec.Code, core.calls, service.calls, body)
		}
		if path == "/api/store/products" {
			data := body["data"].(map[string]any)
			if !reflect.DeepEqual(data["items"], []any{}) || data["offset"] != float64(0) || data["limit"] != float64(30) || data["has_more"] != false || data["seller"] != nil {
				t.Fatal(data)
			}
			if _, ok := data["total"]; ok {
				t.Fatal("invented total")
			}
		}
	}
	core, service := &authCore{}, newStoreTransportFake()
	service.rows = []map[string]any{service.product}
	rec := httptest.NewRecorder()
	storeTransportMux(t, core, service).ServeHTTP(rec, storeTransportRequest("GET", "/api/store/products?seller_id=99&q=needle&offset=2&limit=1", nil))
	data := supportEnvelope(t, rec)["data"].(map[string]any)
	if rec.Code != 200 || service.seller != 99 || service.query != "needle" || service.offset != 2 || service.limit != 1 || data["has_more"] != true {
		t.Fatalf("status=%d service=%+v data=%v", rec.Code, service, data)
	}
}

func TestStoreTransportPublicRoutesRejectEverySuppliedInvalidCredential(t *testing.T) {
	for _, path := range []string{"/api/store/products", "/api/store/products/product-1"} {
		for _, values := range [][]string{{""}, {"Bearer short"}, {"Basic " + webToken}, {"Bearer " + hostToken}, {"Bearer eyJhbGciOiJIUzI1NiJ9.e30.signature"}, {"Bearer " + webToken, "Bearer " + webToken}} {
			core, service := &authCore{}, newStoreTransportFake()
			rec := httptest.NewRecorder()
			storeTransportMux(t, core, service).ServeHTTP(rec, storeTransportRequest("GET", path, values))
			if rec.Code != 401 || core.calls != 0 || len(service.calls) != 0 {
				t.Fatalf("path=%s values=%v status=%d core=%d calls=%v", path, values, rec.Code, core.calls, service.calls)
			}
		}
		core, service := &authCore{}, newStoreTransportFake()
		core.fn = func(context.Context, string) (*pb.AuthorizeResponse, error) {
			return nil, status.Error(codes.Unauthenticated, "synthetic private core error")
		}
		rec := httptest.NewRecorder()
		storeTransportMux(t, core, service).ServeHTTP(rec, storeTransportRequest("GET", path, []string{"Bearer " + webToken}))
		if rec.Code != 401 || core.calls != 1 || len(service.calls) != 0 || strings.Contains(rec.Body.String(), "synthetic") {
			t.Fatalf("status=%d core=%d calls=%v body=%s", rec.Code, core.calls, service.calls, rec.Body.String())
		}
	}
	core, service := &authCore{}, newStoreTransportFake()
	r := storeTransportRequest("GET", "/api/store/products", nil)
	r.Header["authorization"] = []string{"Bearer " + hostToken}
	rec := httptest.NewRecorder()
	storeTransportMux(t, core, service).ServeHTTP(rec, r)
	if rec.Code != 401 || len(service.calls) != 0 {
		t.Fatal(rec.Code, service.calls)
	}
}

func TestStoreTransportValidOptionalSessionIsReverified(t *testing.T) {
	core, service := &authCore{}, newStoreTransportFake()
	core.fn = func(context.Context, string) (*pb.AuthorizeResponse, error) {
		if core.calls == 1 {
			return sessionSelf(), nil
		}
		return nil, status.Error(codes.Unauthenticated, "revoked")
	}
	mux := storeTransportMux(t, core, service)
	for _, want := range []int{200, 401} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, storeTransportRequest("GET", "/api/store/products", []string{"Bearer " + webToken}))
		if rec.Code != want {
			t.Fatal(rec.Code, want)
		}
	}
	if core.calls != 2 || len(service.calls) != 1 {
		t.Fatal(core.calls, service.calls)
	}
}

func TestStoreTransportUnsupportedCatalogueFiltersFailExplicitly(t *testing.T) {
	for _, query := range []string{"sort=newest", "category_id=00000000-0000-4000-8000-000000000001", "tag=featured", "stock=in_stock", "auto_delivery=true", "ai_processing=true", "guest_purchase=true"} {
		core, service := &authCore{}, newStoreTransportFake()
		rec := httptest.NewRecorder()
		storeTransportMux(t, core, service).ServeHTTP(rec, storeTransportRequest("GET", "/api/store/products?"+query, nil))
		body := supportEnvelope(t, rec)
		if rec.Code != 503 || body["success"] != false || body["code"] != "STORE_UNAVAILABLE" || len(service.calls) != 0 {
			t.Fatalf("query=%s status=%d calls=%v body=%v", query, rec.Code, service.calls, body)
		}
	}
	for _, query := range []string{"sort=latest&sort=oldest", "sort=latest", "stock=available", "category_id=category-1", "category_id=", "auto_delivery=1", "guest_purchase=", "ai_processing=False", "q=%FF", "tag=" + strings.Repeat("x", 513), "seller_id=0", "seller_id=-1", "seller_id=01", "seller_id=%2B1", "seller_id=2147483648", "seller_id=1&seller_id=2", "offset=100001", "limit=101", "limit=", "q=" + strings.Repeat("x", 201), "unknown=1", "q=%ZZ"} {
		service := newStoreTransportFake()
		rec := httptest.NewRecorder()
		storeTransportMux(t, &authCore{}, service).ServeHTTP(rec, storeTransportRequest("GET", "/api/store/products?"+query, nil))
		if rec.Code != 422 || len(service.calls) != 0 {
			t.Fatalf("query=%s status=%d calls=%v", query, rec.Code, service.calls)
		}
	}
}

func TestStoreTransportPayAndReconcileRequireVerifiedPersonalBuyerAndActualRead(t *testing.T) {
	for _, suffix := range []string{"pay", "reconcile"} {
		for _, tc := range []struct {
			name                string
			buyer               store.Account
			locateErr, orderErr error
			status              int
			calls               []string
		}{
			{"authorized-but-disabled", store.Account{Kind: "personal", ID: 17}, nil, nil, 503, []string{"locate", "order"}},
			{"other-buyer", store.Account{Kind: "personal", ID: 18}, nil, nil, 403, []string{"locate"}},
			{"team-buyer", store.Account{Kind: "team", ID: 17}, nil, nil, 403, []string{"locate"}},
			{"missing-order", store.Account{Kind: "personal", ID: 17}, store.ErrNotFound, nil, 404, []string{"locate"}},
			{"revoked-real-order-read", store.Account{Kind: "personal", ID: 17}, nil, store.ErrForbidden, 403, []string{"locate", "order"}},
			{"storage-unavailable", store.Account{Kind: "personal", ID: 17}, nil, store.ErrUnavailable, 503, []string{"locate", "order"}},
		} {
			t.Run(suffix+"/"+tc.name, func(t *testing.T) {
				core, service := &authCore{}, newStoreTransportFake()
				service.located.Order.Buyer = tc.buyer
				service.locateErr, service.orderErr = tc.locateErr, tc.orderErr
				r := storeTransportRequest("POST", "/api/store/orders/order-1/"+suffix, []string{"Bearer " + webToken})
				r.Header.Set("X-LMM-User-ID", "18")
				r.Header.Set("X-LMM-Account-ID", "18")
				r.Header.Set("Cookie", "session="+hostToken)
				rec := httptest.NewRecorder()
				storeTransportMux(t, core, service).ServeHTTP(rec, r)
				if rec.Code != tc.status || core.calls != 1 || !reflect.DeepEqual(service.calls, tc.calls) {
					t.Fatalf("status=%d core=%d calls=%v", rec.Code, core.calls, service.calls)
				}
				if len(tc.calls) > 1 && (service.token != webToken || service.shop != "shop-1" || service.id != "order-1") {
					t.Fatalf("forwarded=%+v", service)
				}
				body := supportEnvelope(t, rec)
				if body["success"] != false {
					t.Fatal(body)
				}
				if _, ok := body["data"]; ok {
					t.Fatal("private order exposed in failed pay")
				}
				if tc.name == "authorized-but-disabled" && body["code"] != "STORE_PAYMENT_UNAVAILABLE" {
					t.Fatal(body)
				}
			})
		}
		for _, values := range [][]string{nil, {"Bearer " + hostToken}, {"Bearer " + webToken, "Bearer " + webToken}} {
			service := newStoreTransportFake()
			rec := httptest.NewRecorder()
			storeTransportMux(t, &authCore{}, service).ServeHTTP(rec, storeTransportRequest("POST", "/api/store/orders/order-1/"+suffix, values))
			if rec.Code != 401 || len(service.calls) != 0 {
				t.Fatal(rec.Code, service.calls)
			}
		}
	}
}

type storeReadOnlyRepoFake struct {
	order                 store.Order
	readCalls, writeCalls int
}
type storeReadOnlyTxFake struct{ repo *storeReadOnlyRepoFake }

func (r *storeReadOnlyRepoFake) Within(_ context.Context, shop string, fn func(store.Tx) error) error {
	r.readCalls++
	if shop != "shop-1" {
		return store.ErrNotFound
	}
	return fn(&storeReadOnlyTxFake{r})
}
func (t *storeReadOnlyTxFake) Get(kind, id string, out any) error {
	if kind != "orders" || id != "order-1" {
		return store.ErrNotFound
	}
	raw, err := json.Marshal(t.repo.order)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}
func (t *storeReadOnlyTxFake) Put(string, string, any) error {
	t.repo.writeCalls++
	return fmt.Errorf("unexpected mutation in disabled payment transport")
}
func (*storeReadOnlyTxFake) List(string, string, int) ([]json.RawMessage, error) {
	return nil, store.ErrUnavailable
}
func (*storeReadOnlyRepoFake) FindProduct(context.Context, string) (store.LocatedProduct, error) {
	return store.LocatedProduct{}, store.ErrUnavailable
}
func (r *storeReadOnlyRepoFake) FindOrder(_ context.Context, id string) (store.LocatedOrder, error) {
	if id != "order-1" {
		return store.LocatedOrder{}, store.ErrNotFound
	}
	return store.LocatedOrder{ShopID: "shop-1", Order: r.order}, nil
}
func (*storeReadOnlyRepoFake) ListPublished(context.Context, int64, string, int, int) ([]store.LocatedProduct, error) {
	return nil, store.ErrUnavailable
}

type storeAuthorityFake struct {
	deny     bool
	checks   []store.Permission
	accounts []store.Account
}

func (a *storeAuthorityFake) Check(_ context.Context, token string, account store.Account, permission store.Permission) (int64, error) {
	a.checks = append(a.checks, permission)
	a.accounts = append(a.accounts, account)
	if a.deny || token != webToken || account != (store.Account{Kind: "personal", ID: 17}) {
		return 0, store.ErrForbidden
	}
	return 17, nil
}

type storeFundsNeverFake struct{ availableCalls, executeCalls int }

func (f *storeFundsNeverFake) Available() bool { f.availableCalls++; return true }
func (f *storeFundsNeverFake) Execute(context.Context, string, store.MoneyCommand) (store.Receipt, error) {
	f.executeCalls++
	return store.Receipt{}, fmt.Errorf("funds must not be called by disabled Web pay")
}

// Restrict the optional method set to exercise an older host deliberately.
type storeReadOnlyTransportPort struct{ StoreService }

func TestStoreTransportLegacyFallbackReadsActualOrderAuthorityAndNeverCallsFunds(t *testing.T) {
	for _, suffix := range []string{"pay", "reconcile"} {
		for _, deny := range []bool{false, true} {
			repo := &storeReadOnlyRepoFake{order: newStoreTransportFake().located.Order}
			authority, funds := &storeAuthorityFake{deny: deny}, &storeFundsNeverFake{}
			service, err := store.New(repo, authority, funds)
			if err != nil {
				t.Fatal(err)
			}
			rec := httptest.NewRecorder()
			storeTransportMux(t, &authCore{}, storeReadOnlyTransportPort{service}).ServeHTTP(rec, storeTransportRequest("POST", "/api/store/orders/order-1/"+suffix, []string{"Bearer " + webToken}))
			expected := 503
			if deny {
				expected = 403
			}
			if rec.Code != expected || repo.readCalls != 1 || repo.writeCalls != 0 || funds.availableCalls != 0 || funds.executeCalls != 0 || len(authority.checks) == 0 || authority.checks[0] != store.Read || authority.accounts[0] != (store.Account{Kind: "personal", ID: 17}) {
				t.Fatalf("status=%d repo=%+v auth=%+v funds=%+v", rec.Code, repo, authority, funds)
			}
		}
	}
}

func TestStoreTransportMissingServicesFailWithoutPanic(t *testing.T) {
	var typedNil *storeTransportFake
	for _, service := range []StoreService{nil, typedNil} {
		for _, tc := range []struct {
			method, path string
			headers      []string
		}{{"GET", "/api/store/products", nil}, {"GET", "/api/store/products/product-1", nil}, {"POST", "/api/store/orders/order-1/pay", []string{"Bearer " + webToken}}, {"POST", "/api/store/orders/order-1/reconcile", []string{"Bearer " + webToken}}} {
			rec := httptest.NewRecorder()
			storeTransportMux(t, &authCore{}, service).ServeHTTP(rec, storeTransportRequest(tc.method, tc.path, tc.headers))
			if rec.Code != 503 || supportEnvelope(t, rec)["code"] != "STORE_UNAVAILABLE" {
				t.Fatal(rec.Code, rec.Body.String())
			}
		}
	}
}

var _ StoreService = (*storeTransportFake)(nil)
var _ store.CatalogueIndex = (*storeReadOnlyRepoFake)(nil)

// Optional-port fixtures prove HTTP contract forwarding, not the underlying
// database, Rust authorization, review transitions, or funds implementation.
type storeLegacyTransportFake struct {
	termsActor int64
	termsID    string
	*storeTransportFake
	catalogueQuery     store.CatalogueQuery
	config, disclaimer map[string]any
	disclaimerActor    int64
	actor              store.Account
	requests           []store.LegacyRequest
	value              any
	legacyErr          error
}

func newStoreLegacyTransportFake() *storeLegacyTransportFake {
	return &storeLegacyTransportFake{storeTransportFake: newStoreTransportFake(), config: map[string]any{"credits_per_usd": json.Number("500000"), "store_catalogue_supported": true, "platform_payment_methods": []string{}}, disclaimer: map[string]any{"version": "merchant-store-v1", "text": "Terms", "accepted": false}, value: map[string]any{"accepted": true}}
}
func (s *storeLegacyTransportFake) PublicConfig(context.Context) (map[string]any, error) {
	s.calls = append(s.calls, "config")
	return s.config, nil
}
func (s *storeLegacyTransportFake) PublicDisclaimer(_ context.Context, actor int64) (map[string]any, error) {
	s.calls = append(s.calls, "disclaimer")
	s.disclaimerActor = actor
	return s.disclaimer, nil
}
func (s *storeLegacyTransportFake) PublicProductTerms(_ context.Context, actor int64, id string) (map[string]any, error) {
	s.calls = append(s.calls, "terms")
	s.termsActor = actor
	s.termsID = id
	return map[string]any{"seller_id": 99, "version": "v1", "content": "Actual seller terms", "configured": true}, nil
}
func (s *storeLegacyTransportFake) PublicCatalogueQuery(_ context.Context, q store.CatalogueQuery) ([]map[string]any, error) {
	s.calls = append(s.calls, "catalogue_query")
	s.catalogueQuery = q
	return s.rows, s.catalogueErr
}
func (s *storeLegacyTransportFake) Legacy(_ context.Context, token string, actor store.Account, r store.LegacyRequest) (any, error) {
	s.calls = append(s.calls, "legacy")
	s.token = token
	s.actor = actor
	s.requests = append(s.requests, r)
	return s.value, s.legacyErr
}

func TestStoreTransportConfigAndDisclaimerUseOptionalVerifiedIdentity(t *testing.T) {
	for _, path := range []string{"/api/store/config", "/api/store/disclaimer"} {
		for _, signedIn := range []bool{false, true} {
			core, service := &authCore{}, newStoreLegacyTransportFake()
			var headers []string
			if signedIn {
				headers = []string{"Bearer " + webToken}
			}
			r := storeTransportRequest("GET", path, headers)
			r.Header.Set("Cookie", "session="+webToken)
			r.Header.Set("X-LMM-User-ID", "999")
			rec := httptest.NewRecorder()
			storeTransportMux(t, core, service).ServeHTTP(rec, r)
			data := supportEnvelope(t, rec)["data"].(map[string]any)
			wantCalls := 0
			var actor int64
			if signedIn {
				wantCalls = 1
				actor = 17
			}
			if rec.Code != 200 || core.calls != wantCalls || len(service.calls) != 1 || service.disclaimerActor != func() int64 {
				if path == "/api/store/disclaimer" {
					return actor
				}
				return 0
			}() {
				t.Fatalf("path=%s signed=%t status=%d core=%d service=%+v", path, signedIn, rec.Code, core.calls, service)
			}
			if path == "/api/store/config" && (data["credits_per_usd"] != float64(500000) || !reflect.DeepEqual(data["platform_payment_methods"], []any{})) {
				t.Fatal(data)
			}
			if path == "/api/store/disclaimer" && data["accepted"] != false {
				t.Fatal("transport fabricated acceptance", data)
			}
		}
		for _, headers := range [][]string{{"Bearer " + hostToken}, {""}, {"Basic " + webToken}, {"Bearer " + webToken, "Bearer " + webToken}} {
			service := newStoreLegacyTransportFake()
			rec := httptest.NewRecorder()
			storeTransportMux(t, &authCore{}, service).ServeHTTP(rec, storeTransportRequest("GET", path, headers))
			if rec.Code != 401 || len(service.calls) != 0 {
				t.Fatal(path, rec.Code, service.calls)
			}
		}
		for _, service := range []StoreService{nil, newStoreTransportFake()} {
			rec := httptest.NewRecorder()
			storeTransportMux(t, &authCore{}, service).ServeHTTP(rec, storeTransportRequest("GET", path, nil))
			if rec.Code != 503 {
				t.Fatal(path, rec.Code)
			}
		}
		service := newStoreLegacyTransportFake()
		rec := httptest.NewRecorder()
		storeTransportMux(t, &authCore{}, service).ServeHTTP(rec, storeTransportRequest("GET", path+"?user_id=999", nil))
		if rec.Code != 422 || len(service.calls) != 0 {
			t.Fatal(path, rec.Code, service.calls)
		}
	}
}

func TestStoreTransportCatalogueForwardsMainFiltersAndFalsePresence(t *testing.T) {
	core, service := &authCore{}, newStoreLegacyTransportFake()
	service.rows = []map[string]any{service.product}
	params := url.Values{"q": {"50%_! coffee"}, "sort": {"comprehensive"}, "seller_id": {"99"}, "category_id": {"00000000-0000-4000-8000-000000000001"}, "tag": {"tag name"}, "stock": {"out_of_stock"}, "auto_delivery": {"false"}, "ai_processing": {"true"}, "guest_purchase": {"false"}, "offset": {"24"}, "limit": {"1"}}
	rec := httptest.NewRecorder()
	storeTransportMux(t, core, service).ServeHTTP(rec, storeTransportRequest("GET", "/api/store/products?"+params.Encode(), nil))
	q := service.catalogueQuery
	if rec.Code != 200 || core.calls != 0 || !reflect.DeepEqual(service.calls, []string{"catalogue_query"}) || q.Seller != 99 || q.Text != "50%_! coffee" || q.Sort != "comprehensive" || q.CategoryID != params.Get("category_id") || q.Tag != "tag name" || q.Stock != "out_of_stock" || q.AutoDelivery == nil || *q.AutoDelivery || q.AIProcessing == nil || !*q.AIProcessing || q.GuestPurchase == nil || *q.GuestPurchase || q.Offset != 24 || q.Limit != 1 {
		t.Fatalf("status=%d calls=%v query=%+v", rec.Code, service.calls, q)
	}
	data := supportEnvelope(t, rec)["data"].(map[string]any)
	if data["has_more"] != true || data["seller"] != nil {
		t.Fatal(data)
	}
	for _, sort := range []string{"", "comprehensive", "newest", "sales"} {
		service := newStoreLegacyTransportFake()
		rec := httptest.NewRecorder()
		storeTransportMux(t, &authCore{}, service).ServeHTTP(rec, storeTransportRequest("GET", "/api/store/products?sort="+sort, nil))
		want := sort
		if want == "" {
			want = "comprehensive"
		}
		if rec.Code != 200 || service.catalogueQuery.Sort != want || service.catalogueQuery.AutoDelivery != nil {
			t.Fatal(sort, rec.Code, service.catalogueQuery)
		}
	}
	service = newStoreLegacyTransportFake()
	service.catalogueErr = store.ErrUnavailable
	rec = httptest.NewRecorder()
	storeTransportMux(t, &authCore{}, service).ServeHTTP(rec, storeTransportRequest("GET", "/api/store/products?sort=sales", nil))
	if rec.Code != 503 || supportEnvelope(t, rec)["success"] != false {
		t.Fatal(rec.Code, rec.Body.String())
	}
	// A default sort is also compatible with the old catalogue-only port.
	old := newStoreTransportFake()
	rec = httptest.NewRecorder()
	storeTransportMux(t, &authCore{}, old).ServeHTTP(rec, storeTransportRequest("GET", "/api/store/products?sort=comprehensive", nil))
	if rec.Code != 200 || len(old.calls) != 1 {
		t.Fatal(rec.Code, old.calls)
	}
}

type storeLegacyRouteCase struct {
	method, path, action, id, child, body string
	offset, limit                         int
	role                                  string
}

func storeLegacyRouteCases() []storeLegacyRouteCase {
	return []storeLegacyRouteCase{
		{method: "GET", path: "/api/store/my/terms", action: "my_terms"},
		{method: "PUT", path: "/api/store/my/terms", action: "save_terms", body: `{"content":"Actual seller terms","expected_version":"v1"}`},
		{method: "POST", path: "/api/store/disclaimer/accept", action: "accept_disclaimer", body: `{"version":"merchant-store-v1","accepted":true}`},
		{method: "GET", path: "/api/store/my/products?offset=2&limit=7", action: "my_products", offset: 2, limit: 7},
		{method: "GET", path: "/api/store/my/products/product-1", action: "draft_product", id: "product-1"},
		{method: "GET", path: "/api/store/my/products/product-1/preview", action: "preview_product", id: "product-1"},
		{method: "POST", path: "/api/store/products", action: "save_product", body: `{"title":"Title","price_quota":9007199254740991}`},
		{method: "PUT", path: "/api/store/products/product-1", action: "save_product", id: "product-1", body: `{"title":"Title","price_quota":500000}`},
		{method: "POST", path: "/api/store/products/product-1/submit", action: "submit_product", id: "product-1"},
		{method: "POST", path: "/api/store/products/product-1/unlist", action: "unlist_product", id: "product-1", body: `{}`},
		{method: "DELETE", path: "/api/store/products/product-1", action: "delete_product", id: "product-1"},
		{method: "PUT", path: "/api/store/products/product-1/paused", action: "paused", id: "product-1", body: `{"paused":false}`},
		{method: "PUT", path: "/api/store/products/product-1/sale-limit", action: "sale_limit", id: "product-1", body: `{"sale_limit":null}`},
		{method: "PUT", path: "/api/store/products/product-1/listing", action: "listing", id: "product-1", body: `{"listed":false}`},
		{method: "POST", path: "/api/store/products/product-1/variants", action: "create_variant", id: "product-1", body: `{"name":"Variant","price_quota":500000,"enabled":true}`},
		{method: "PUT", path: "/api/store/products/product-1/variants/variant-1", action: "save_variant", id: "product-1", child: "variant-1", body: `{"name":"Variant","price_quota":500001}`},
		{method: "PUT", path: "/api/store/products/product-1/variants/variant-1/enabled", action: "variant_enabled", id: "product-1", child: "variant-1", body: `{"enabled":false}`},
		{method: "GET", path: "/api/store/reviews", action: "reviews", limit: 30},
		{method: "POST", path: "/api/store/products/product-1/review", action: "review_product", id: "product-1", body: `{"approve":true,"note":"Approved"}`},
		{method: "POST", path: "/api/store/orders", action: "create_order", body: `{"product_id":"product-1","quantity":1,"request_key":"key-1","payment_method":"balance"}`},
		{method: "GET", path: "/api/store/my/orders?role=seller&offset=3&limit=9", action: "my_orders", offset: 3, limit: 9, role: "seller"},
		{method: "GET", path: "/api/store/orders/order-1", action: "get_order", id: "order-1"},
		{method: "POST", path: "/api/store/orders/order-1/pay", action: "pay_order", id: "order-1", body: `{"currency":"CNY"}`},
		{method: "POST", path: "/api/store/orders/order-1/reconcile", action: "reconcile_order", id: "order-1"},
		{method: "POST", path: "/api/store/orders/order-1/cancel", action: "cancel_order", id: "order-1"},
		{method: "GET", path: "/api/store/orders/order-1/refunds", action: "refunds", id: "order-1"},
		{method: "POST", path: "/api/store/orders/order-1/refunds", action: "request_refund", id: "order-1", body: `{"request_key":"refund-1","reason":"Refund","mode":"full"}`},
		{method: "POST", path: "/api/store/orders/order-1/refunds/refund-1/decision", action: "decide_refund", id: "order-1", child: "refund-1", body: `{"decision":"reject","reason":"Reason"}`},
	}
}
func TestStoreTransportLegacyMainRoutesForwardOnlyVerifiedPersonalActor(t *testing.T) {
	for _, tc := range storeLegacyRouteCases() {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			core, service := &authCore{}, newStoreLegacyTransportFake()
			r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			r.Header.Set("Authorization", "Bearer "+webToken)
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("X-LMM-User-ID", "999")
			r.Header.Set("X-LMM-Account-ID", "999")
			r.Header.Set("Cookie", "session="+hostToken)
			rec := httptest.NewRecorder()
			storeTransportMux(t, core, service).ServeHTTP(rec, r)
			if rec.Code != 200 || core.calls != 1 || !reflect.DeepEqual(service.calls, []string{"legacy"}) || len(service.requests) != 1 || service.actor != (store.Account{Kind: "personal", ID: 17}) || service.token != webToken {
				t.Fatalf("status=%d core=%d service=%+v body=%s", rec.Code, core.calls, service, rec.Body.String())
			}
			request := service.requests[0]
			if request.Action != tc.action || request.ID != tc.id || request.ChildID != tc.child || string(request.Body) != tc.body || request.Offset != tc.offset || request.Limit != tc.limit || request.Role != tc.role {
				t.Fatalf("request=%+v want=%+v", request, tc)
			}
		})
	}
}

// Reading this body would be a bug before credential verification succeeds.
type storeBodyProbe struct{ reads int }

func (p *storeBodyProbe) Read([]byte) (int, error) { p.reads++; return 0, io.EOF }
func (p *storeBodyProbe) Close() error             { return nil }
func TestStoreTransportLegacyAuthenticatesBeforeReadingBody(t *testing.T) {
	for _, tc := range storeLegacyRouteCases() {
		for _, headers := range [][]string{nil, {"Bearer " + hostToken}, {"Bearer " + webToken, "Bearer " + webToken}} {
			service := newStoreLegacyTransportFake()
			probe := &storeBodyProbe{}
			r := storeTransportRequest(tc.method, tc.path, headers)
			r.Body = probe
			rec := httptest.NewRecorder()
			storeTransportMux(t, &authCore{}, service).ServeHTTP(rec, r)
			if rec.Code != 401 || probe.reads != 0 || len(service.calls) != 0 {
				t.Fatalf("path=%s status=%d reads=%d calls=%v", tc.path, rec.Code, probe.reads, service.calls)
			}
		}
	}
	core := &authCore{fn: func(context.Context, string) (*pb.AuthorizeResponse, error) {
		return nil, status.Error(codes.Unauthenticated, "revoked")
	}}
	service := newStoreLegacyTransportFake()
	probe := &storeBodyProbe{}
	r := storeTransportRequest("POST", "/api/store/products", []string{"Bearer " + webToken})
	r.Body = probe
	rec := httptest.NewRecorder()
	storeTransportMux(t, core, service).ServeHTTP(rec, r)
	if rec.Code != 401 || probe.reads != 0 || len(service.calls) != 0 {
		t.Fatal(rec.Code, probe.reads, service.calls)
	}
}

func TestStoreTransportListRejectsExpiredAndRevokedSessions(t *testing.T) {
	for _, reason := range []string{"expired", "revoked"} {
		for _, path := range []string{"/api/store/products", "/api/store/my/orders?role=buyer", "/api/store/my/orders?role=seller"} {
			core := &authCore{fn: func(context.Context, string) (*pb.AuthorizeResponse, error) {
				return nil, status.Error(codes.Unauthenticated, reason)
			}}
			service := newStoreLegacyTransportFake()
			rec := httptest.NewRecorder()
			storeTransportMux(t, core, service).ServeHTTP(rec, storeTransportRequest("GET", path, []string{"Bearer " + webToken}))
			if rec.Code != 401 || len(service.calls) != 0 || core.calls != 1 {
				t.Fatalf("%s %s: status=%d calls=%v core=%d", reason, path, rec.Code, service.calls, core.calls)
			}
		}
	}
}

func TestStoreTransportLegacyRejectsMalformedBodiesBeforeDomain(t *testing.T) {
	bodies := []string{"", `null`, `[]`, `true`, `{"title":"one","title":"two"}`, `{"title":"one","Title":"two"}`, `{"variants":[{"enabled":true,"enabled":false}]}`, `{"links":[{"url":"https://example.com","URL":"https://other.example"}]}`, `{"title":true} {}`, `{"title":`, "{\"title\":\"\xff\"}", `{"nested":` + strings.Repeat(`[`, 66) + `0` + strings.Repeat(`]`, 66) + `}`}
	for i, body := range bodies {
		service := newStoreLegacyTransportFake()
		r := httptest.NewRequest("POST", "/api/store/products", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+webToken)
		rec := httptest.NewRecorder()
		storeTransportMux(t, &authCore{}, service).ServeHTTP(rec, r)
		if rec.Code != 422 || len(service.calls) != 0 {
			t.Fatalf("case=%d status=%d calls=%v body=%s", i, rec.Code, service.calls, rec.Body.String())
		}
	}
	for _, ct := range [][]string{{"text/plain"}, {"application/json; charset=latin1"}, {"application/json", "application/json"}} {
		service := newStoreLegacyTransportFake()
		r := httptest.NewRequest("POST", "/api/store/products", strings.NewReader(`{}`))
		r.Header.Set("Authorization", "Bearer "+webToken)
		r.Header["Content-Type"] = ct
		rec := httptest.NewRecorder()
		storeTransportMux(t, &authCore{}, service).ServeHTTP(rec, r)
		if rec.Code != 422 || len(service.calls) != 0 {
			t.Fatal(ct, rec.Code, service.calls)
		}
	}
}

func TestStoreTransportLegacyUsesMainBodyByteLimits(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		max          int
	}{
		{"POST", "/api/store/products", 512 << 10}, {"PUT", "/api/store/products/product-1/variants/variant-1", 512 << 10}, {"POST", "/api/store/orders", 8 << 10}, {"POST", "/api/store/products/product-1/review", 8 << 10}, {"POST", "/api/store/orders/order-1/refunds", 64 << 10}, {"POST", "/api/store/orders/order-1/refunds/refund-1/decision", 8 << 10}, {"PUT", "/api/store/my/terms", 128 << 10},
		{"POST", "/api/store/disclaimer/accept", 4 << 10}, {"PUT", "/api/store/products/product-1/paused", 4 << 10},
	} {
		for _, extra := range []int{0, 1} {
			// Fake domain accepts the padding object; this tests the byte limit alone.
			body := `{"padding":"` + strings.Repeat("x", tc.max-len(`{"padding":""}`)+extra) + `"}`
			service := newStoreLegacyTransportFake()
			r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(body))
			r.Header.Set("Authorization", "Bearer "+webToken)
			rec := httptest.NewRecorder()
			storeTransportMux(t, &authCore{}, service).ServeHTTP(rec, r)
			want, calls := 200, 1
			if extra > 0 {
				want, calls = 422, 0
			}
			if rec.Code != want || len(service.calls) != calls {
				t.Fatalf("path=%s bytes=%d status=%d calls=%v", tc.path, len(body), rec.Code, service.calls)
			}
		}
	}
}

func TestStoreTransportLegacyPaginationPermissionsAndSafeErrors(t *testing.T) {
	for _, path := range []string{"/api/store/my/products?offset=100001", "/api/store/reviews?limit=101", "/api/store/my/orders?role=admin", "/api/store/my/orders?role=", "/api/store/my/orders?role=buyer&role=seller", "/api/store/orders/order-1?user_id=999", "/api/store/products/product-1/variants/variant.1/enabled"} {
		method := "GET"
		if strings.Contains(path, "/enabled") {
			method = "PUT"
		}
		service := newStoreLegacyTransportFake()
		rec := httptest.NewRecorder()
		storeTransportMux(t, &authCore{}, service).ServeHTTP(rec, storeTransportRequest(method, path, []string{"Bearer " + webToken}))
		if rec.Code != 422 || len(service.calls) != 0 {
			t.Fatal(path, rec.Code, service.calls)
		}
	}
	service := newStoreLegacyTransportFake()
	r := httptest.NewRequest("POST", "/api/store/orders", strings.NewReader(`{}`))
	r.Header.Set("Authorization", "Bearer "+webToken)
	r.Header["X-Store-Guest"] = []string{""}
	rec := httptest.NewRecorder()
	storeTransportMux(t, &authCore{}, service).ServeHTTP(rec, r)
	if rec.Code != 403 || len(service.calls) != 0 {
		t.Fatal(rec.Code, service.calls)
	}
	for _, tc := range []struct {
		err    error
		status int
	}{{store.ErrForbidden, 403}, {store.ErrPaymentUnavailable, 503}, {fmt.Errorf("synthetic private SQL provider secret"), 500}} {
		service := newStoreLegacyTransportFake()
		service.legacyErr = tc.err
		service.value = map[string]any{"private": "not exposed"}
		r := httptest.NewRequest("POST", "/api/store/orders/order-1/pay", strings.NewReader(`{"currency":"CNY"}`))
		r.Header.Set("Authorization", "Bearer "+webToken)
		rec := httptest.NewRecorder()
		storeTransportMux(t, &authCore{}, service).ServeHTTP(rec, r)
		body := supportEnvelope(t, rec)
		if rec.Code != tc.status || body["success"] != false || strings.Contains(rec.Body.String(), "private") || strings.Contains(rec.Body.String(), "secret") {
			t.Fatal(rec.Code, rec.Body.String())
		}
		if _, ok := body["data"]; ok {
			t.Fatal(body)
		}
	}
	// The service already returns the legacy list data; transport must not wrap it.
	service = newStoreLegacyTransportFake()
	service.value = map[string]any{"items": []any{}, "offset": 0, "limit": 30, "has_more": false}
	rec = httptest.NewRecorder()
	storeTransportMux(t, &authCore{}, service).ServeHTTP(rec, storeTransportRequest("GET", "/api/store/my/orders", []string{"Bearer " + webToken}))
	data := supportEnvelope(t, rec)["data"].(map[string]any)
	if rec.Code != 200 || data["has_more"] != false || service.requests[0].Role != "buyer" || service.requests[0].Limit != 30 {
		t.Fatal(rec.Code, data, service.requests)
	}
	service = newStoreLegacyTransportFake()
	service.value = nil
	rec = httptest.NewRecorder()
	storeTransportMux(t, &authCore{}, service).ServeHTTP(rec, storeTransportRequest("POST", "/api/store/products/product-1/submit", []string{"Bearer " + webToken}))
	if rec.Code != 200 || supportEnvelope(t, rec)["data"] != nil {
		t.Fatal(rec.Code, rec.Body.String())
	}
}

func TestStoreTransportPublicProductTermsUseOnlyVerifiedOrAnonymousActor(t *testing.T) {
	for _, signedIn := range []bool{false, true} {
		core, service := &authCore{}, newStoreLegacyTransportFake()
		var headers []string
		var wantActor int64
		if signedIn {
			headers = []string{"Bearer " + webToken}
			wantActor = 17
		}
		r := storeTransportRequest("GET", "/api/store/products/product-1/terms", headers)
		r.Header.Set("Cookie", "session="+webToken)
		r.Header.Set("X-LMM-User-ID", "999")
		rec := httptest.NewRecorder()
		storeTransportMux(t, core, service).ServeHTTP(rec, r)
		wantCore := 0
		if signedIn {
			wantCore = 1
		}
		if rec.Code != 200 || service.termsActor != wantActor || service.termsID != "product-1" || core.calls != wantCore || !reflect.DeepEqual(service.calls, []string{"terms"}) {
			t.Fatal(rec.Code, service.termsActor, service.termsID, core.calls, service.calls)
		}
	}
	for _, headers := range [][]string{{"Bearer " + hostToken}, {"Bearer short"}, {"Bearer " + webToken, "Bearer " + webToken}} {
		service := newStoreLegacyTransportFake()
		rec := httptest.NewRecorder()
		storeTransportMux(t, &authCore{}, service).ServeHTTP(rec, storeTransportRequest("GET", "/api/store/products/product-1/terms", headers))
		if rec.Code != 401 || len(service.calls) != 0 {
			t.Fatal(rec.Code, service.calls)
		}
	}
	for _, path := range []string{"/api/store/products/product.1/terms", "/api/store/products/product-1/terms?user_id=999"} {
		service := newStoreLegacyTransportFake()
		rec := httptest.NewRecorder()
		storeTransportMux(t, &authCore{}, service).ServeHTTP(rec, storeTransportRequest("GET", path, nil))
		if rec.Code != 422 || len(service.calls) != 0 {
			t.Fatal(path, rec.Code, service.calls)
		}
	}
	rec := httptest.NewRecorder()
	storeTransportMux(t, &authCore{}, newStoreTransportFake()).ServeHTTP(rec, storeTransportRequest("GET", "/api/store/products/product-1/terms", nil))
	if rec.Code != 503 {
		t.Fatal(rec.Code)
	}
}
