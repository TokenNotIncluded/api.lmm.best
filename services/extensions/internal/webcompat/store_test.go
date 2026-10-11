package webcompat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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
	for _, query := range []string{"sort=latest", "category_id=category-1", "tag=featured", "stock=available", "auto_delivery=true", "ai_processing=true", "guest_purchase=true"} {
		core, service := &authCore{}, newStoreTransportFake()
		rec := httptest.NewRecorder()
		storeTransportMux(t, core, service).ServeHTTP(rec, storeTransportRequest("GET", "/api/store/products?"+query, nil))
		body := supportEnvelope(t, rec)
		if rec.Code != 503 || body["success"] != false || body["code"] != "STORE_UNAVAILABLE" || len(service.calls) != 0 {
			t.Fatalf("query=%s status=%d calls=%v body=%v", query, rec.Code, service.calls, body)
		}
	}
	for _, query := range []string{"sort=latest&sort=oldest", "seller_id=0", "seller_id=-1", "seller_id=01", "seller_id=%2B1", "seller_id=2147483648", "seller_id=1&seller_id=2", "offset=100001", "limit=101", "limit=", "q=" + strings.Repeat("x", 201), "unknown=1", "q=%ZZ"} {
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

func TestStoreTransportDisabledPaymentsReadActualOrderAuthorityAndNeverCallFunds(t *testing.T) {
	for _, suffix := range []string{"pay", "reconcile"} {
		for _, deny := range []bool{false, true} {
			repo := &storeReadOnlyRepoFake{order: newStoreTransportFake().located.Order}
			authority, funds := &storeAuthorityFake{deny: deny}, &storeFundsNeverFake{}
			service, err := store.New(repo, authority, funds)
			if err != nil {
				t.Fatal(err)
			}
			rec := httptest.NewRecorder()
			storeTransportMux(t, &authCore{}, service).ServeHTTP(rec, storeTransportRequest("POST", "/api/store/orders/order-1/"+suffix, []string{"Bearer " + webToken}))
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
