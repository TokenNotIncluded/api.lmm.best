package webcompat

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules"
)

// These are in-process transport/lifecycle tests using fake domain ports.
// They do not assert that core RPC, database schemas, or production are ready.
func mountTransportHost(t *testing.T, registered []modules.Module) (*modules.Host, *authCore) {
	t.Helper()
	host, err := modules.NewHost(registered, []byte(hostToken))
	if err != nil {
		t.Fatal(err)
	}
	return host, &authCore{}
}
func mountTransportCall(handler http.Handler, method, path string, headers []string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, storeTransportRequest(method, path, headers))
	return rec
}

func TestMountTransportPreparingAndDrainingRejectWebWhileHealthRemainsReadable(t *testing.T) {
	host, core := mountTransportHost(t, nil)
	storeService, supportService := newStoreTransportFake(), &supportFake{}
	handler := Mount(host, newAuth(t, core), storeService, supportService)
	paths := []struct{ method, path string }{{"GET", "/api/store/config"}, {"GET", "/api/store/disclaimer"}, {"GET", "/api/store/products/product-1/terms"}, {"PUT", "/api/store/my/terms"}, {"GET", "/api/store/products"}, {"GET", "/api/store/products/product-1"}, {"POST", "/api/store/orders/order-1/pay"}, {"GET", "/api/store/support/conversations"}, {"GET", "/api/store/my/products"}}
	checkClosed := func(phase string) {
		t.Helper()
		for _, route := range paths {
			rec := mountTransportCall(handler, route.method, route.path, []string{"Bearer " + webToken})
			body := supportEnvelope(t, rec)
			if rec.Code != 503 || rec.Header().Get("Retry-After") != "1" || body["success"] != false || body["code"] != "EXTENSION_NOT_ACCEPTING" {
				t.Fatalf("phase=%s path=%s status=%d body=%v", phase, route.path, rec.Code, body)
			}
		}
		if core.calls != 0 || len(storeService.calls) != 0 || len(supportService.calls) != 0 {
			t.Fatalf("phase=%s core=%d store=%v support=%v", phase, core.calls, storeService.calls, supportService.calls)
		}
		live := mountTransportCall(handler, "GET", "/health/live", nil)
		ready := mountTransportCall(handler, "GET", "/health/ready", nil)
		if live.Code != 200 || ready.Code != 503 || !strings.Contains(ready.Body.String(), phase) {
			t.Fatalf("phase=%s live=%d ready=%d body=%s", phase, live.Code, ready.Code, ready.Body.String())
		}
	}
	checkClosed("preparing")
	if err := host.MarkReady(); err != nil {
		t.Fatal(err)
	}
	if ready := mountTransportCall(handler, "GET", "/health/ready", nil); ready.Code != 200 {
		t.Fatal(ready.Code, ready.Body.String())
	}
	if rec := mountTransportCall(handler, "GET", "/api/store/products", nil); rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	if len(storeService.calls) != 1 || core.calls != 0 {
		t.Fatal(storeService.calls, core.calls)
	}
	storeService.calls = nil
	host.BeginDrain()
	checkClosed("draining")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := host.Wait(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestMountTransportKeepsUserAndHostCredentialsOnSeparateAPIs(t *testing.T) {
	calls := 0
	module := mountTransportModule{http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "" {
			t.Error("host credential propagated to module")
		}
		if r.URL.Path != "/entry" {
			t.Error(r.URL.Path)
		}
		w.WriteHeader(204)
	})}
	host, core := mountTransportHost(t, []modules.Module{module})
	if err := host.MarkReady(); err != nil {
		t.Fatal(err)
	}
	service := newStoreTransportFake()
	handler := Mount(host, newAuth(t, core), service, &supportFake{})
	for _, path := range []string{"/api/store/products", "/api/store/products/product-1", "/api/store/my/products"} {
		rec := mountTransportCall(handler, "GET", path, []string{"Bearer " + hostToken})
		if rec.Code != 401 || len(service.calls) != 0 || core.calls != 0 {
			t.Fatalf("path=%s status=%d calls=%v core=%d", path, rec.Code, service.calls, core.calls)
		}
	}
	for _, credential := range []string{"", webToken} {
		var values []string
		if credential != "" {
			values = []string{"Bearer " + credential}
		}
		rec := mountTransportCall(handler, "GET", "/extensions/v1/transport/entry", values)
		if rec.Code != 401 || calls != 0 {
			t.Fatal(rec.Code, calls)
		}
	}
	rec := mountTransportCall(handler, "GET", "/extensions/v1/transport/entry", []string{"Bearer " + hostToken})
	if rec.Code != 204 || calls != 1 || core.calls != 0 {
		t.Fatal(rec.Code, calls, core.calls)
	}
	rec = mountTransportCall(handler, "GET", "/extensions/v1/modules", []string{"Bearer " + hostToken})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"modules":["transport"]`) {
		t.Fatal(rec.Code, rec.Body.String())
	}
	rec = mountTransportCall(handler, "GET", "/api/store/products", []string{"Bearer " + webToken})
	if rec.Code != 200 || core.calls != 1 || len(service.calls) != 1 {
		t.Fatal(rec.Code, core.calls, service.calls)
	}
	for _, path := range []string{"/health/live", "/health/ready"} {
		if rec := mountTransportCall(handler, "GET", path, nil); rec.Code != 200 {
			t.Fatal(path, rec.Code)
		}
	}
}

type mountTransportModule struct{ handler http.Handler }

func (m mountTransportModule) Name() string          { return "transport" }
func (m mountTransportModule) Handler() http.Handler { return m.handler }

func TestMountTransportUnassembledRoutesFailExplicitlyAndMissingServicesDoNotPanic(t *testing.T) {
	host, core := mountTransportHost(t, nil)
	if err := host.MarkReady(); err != nil {
		t.Fatal(err)
	}
	var catalogue *storeTransportFake
	var supportService *supportFake
	handler := Mount(host, newAuth(t, core), catalogue, supportService)
	for _, route := range []struct {
		method, path string
		values       []string
		want         int
	}{
		{"GET", "/api/store/config", nil, 503},
		{"GET", "/api/store/disclaimer", nil, 503},
		{"GET", "/api/store/products", nil, 503},
		{"GET", "/api/store/products/product-1", nil, 503},
		{"POST", "/api/store/orders/order-1/pay", []string{"Bearer " + webToken}, 503},
		{"GET", "/api/store/support/conversations", []string{"Bearer " + webToken}, 503},
		{"GET", "/api/store/my/products", []string{"Bearer " + webToken}, 503},
		{"GET", "/api/store/my/products", nil, 401},
		{"GET", "/api/store/my/products", []string{"Bearer " + hostToken}, 401},
		{"GET", "/api/store/my/products", []string{"Bearer short"}, 401},
	} {
		rec := mountTransportCall(handler, route.method, route.path, route.values)
		body := supportEnvelope(t, rec)
		if rec.Code != route.want || body["success"] != false {
			t.Fatalf("%s status=%d want=%d body=%v", route.path, rec.Code, route.want, body)
		}
		if _, ok := body["data"]; ok {
			t.Fatal("unavailable route returned successful-looking data")
		}
	}
}

func TestMountTransportDrainWaitsForAcceptedWebWorkAndRejectsNewWork(t *testing.T) {
	host, core := mountTransportHost(t, nil)
	if err := host.MarkReady(); err != nil {
		t.Fatal(err)
	}
	entered, finish := make(chan struct{}), make(chan struct{})
	var closeOnce sync.Once
	defer closeOnce.Do(func() { close(finish) })
	service := newStoreTransportFake()
	service.catalogueFn = func(context.Context, int64, string, int, int) ([]map[string]any, error) {
		close(entered)
		<-finish
		return nil, nil
	}
	handler := Mount(host, newAuth(t, core), service, &supportFake{})
	result := make(chan *httptest.ResponseRecorder, 1)
	go func() { result <- mountTransportCall(handler, "GET", "/api/store/products", nil) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("accepted Web request never entered fake service")
	}
	host.BeginDrain()
	next := mountTransportCall(handler, "GET", "/api/store/products", nil)
	if next.Code != 503 || next.Header().Get("Retry-After") != "1" {
		t.Fatal(next.Code, next.Header())
	}
	if live := mountTransportCall(handler, "GET", "/health/live", nil); live.Code != 200 {
		t.Fatal("liveness blocked by accepted work")
	}
	if ready := mountTransportCall(handler, "GET", "/health/ready", nil); ready.Code != 503 {
		t.Fatal("draining host still ready")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	err := host.Wait(ctx)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("drain returned while Web work still held permit: %v", err)
	}
	closeOnce.Do(func() { close(finish) })
	select {
	case rec := <-result:
		if rec.Code != 200 {
			t.Fatal(rec.Code, rec.Body.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("accepted Web work did not finish")
	}
	ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err = host.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	if len(service.calls) != 1 || core.calls != 0 {
		t.Fatalf("new Web work entered during drain: calls=%v core=%d", service.calls, core.calls)
	}
}

func TestMountTransportReleasesAdmissionOnAuthAndValidationFailures(t *testing.T) {
	for _, tc := range []struct {
		path    string
		headers []string
		want    int
	}{
		{"/api/store/products", []string{"Bearer " + hostToken}, 401},
		{"/api/store/products?limit=0", nil, 422},
		{"/api/store/products?sort=newest", nil, 503},
		{"/api/store/my/products", nil, 401},
	} {
		host, core := mountTransportHost(t, nil)
		if err := host.MarkReady(); err != nil {
			t.Fatal(err)
		}
		handler := Mount(host, newAuth(t, core), newStoreTransportFake(), &supportFake{})
		rec := mountTransportCall(handler, "GET", tc.path, tc.headers)
		if rec.Code != tc.want {
			t.Fatal(tc.path, rec.Code, rec.Body.String())
		}
		host.BeginDrain()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		err := host.Wait(ctx)
		cancel()
		if err != nil {
			t.Fatalf("permit leaked for %s: %v", tc.path, err)
		}
	}
}

func TestMountTransportLegacyCatalogueAndConfigReachOnlyReadyHost(t *testing.T) {
	host, core := mountTransportHost(t, nil)
	service := newStoreLegacyTransportFake()
	handler := Mount(host, newAuth(t, core), service, &supportFake{})
	for _, path := range []string{"/api/store/config", "/api/store/disclaimer", "/api/store/products?sort=comprehensive"} {
		if rec := mountTransportCall(handler, "GET", path, nil); rec.Code != 503 {
			t.Fatal(path, rec.Code)
		}
	}
	if len(service.calls) != 0 || core.calls != 0 {
		t.Fatal(service.calls, core.calls)
	}
	if err := host.MarkReady(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/store/config", "/api/store/disclaimer", "/api/store/products?sort=comprehensive"} {
		if rec := mountTransportCall(handler, "GET", path, nil); rec.Code != 200 {
			t.Fatal(path, rec.Code, rec.Body.String())
		}
	}
	if core.calls != 0 || len(service.calls) != 3 {
		t.Fatal(core.calls, service.calls)
	}
	// No Web fulfill/settle route existed in main, and unsupported pickup proof
	// cannot be replaced with an unguarded generic order mutation.
	for _, path := range []string{"/api/store/orders/order-1/fulfill", "/api/store/orders/order-1/settle", "/api/store/orders/order-1/pickup-link"} {
		rec := mountTransportCall(handler, "POST", path, []string{"Bearer " + webToken})
		if rec.Code != 503 || len(service.calls) != 3 {
			t.Fatal(path, rec.Code, service.calls)
		}
	}
	host.BeginDrain()
	if rec := mountTransportCall(handler, "GET", "/api/store/config", nil); rec.Code != 503 {
		t.Fatal(rec.Code)
	}
	if rec := mountTransportCall(handler, "GET", "/health/live", nil); rec.Code != 200 {
		t.Fatal(rec.Code)
	}
}

func TestCompatibilitySharesGlobalAdmission(t *testing.T) {
	host, e := modules.NewHostWithLimit(nil, []byte(hostToken), 1)
	if e != nil {
		t.Fatal(e)
	}
	_ = host.MarkReady()
	release, e := host.TryAcquire()
	if e != nil {
		t.Fatal(e)
	}
	handler := Mount(host, nil, nil, nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/api/store/items", nil))
	if w.Code != 503 || !strings.Contains(w.Body.String(), "EXTENSION_BUSY") || w.Header().Get("Retry-After") != "1" {
		t.Fatal(w)
	}
	host.BeginDrain()
	release()
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/api/store/items", nil))
	if w.Code != 503 || !strings.Contains(w.Body.String(), "EXTENSION_NOT_ACCEPTING") {
		t.Fatal(w)
	}
}

// No optional catalogue query port: this exercises the ordinary StoreService.
type blockedCatalogue struct {
	StoreService
	enter  func()
	leave  func()
	finish <-chan struct{}
}

func (s blockedCatalogue) PublicCatalogue(context.Context, int64, string, int, int) ([]map[string]any, error) {
	s.enter()
	defer s.leave()
	<-s.finish
	return []map[string]any{}, nil
}
func TestNativeAndCompatibilityMixedGlobalBound(t *testing.T) {
	var mu sync.Mutex
	active, peak := 0, 0
	entered := make(chan struct{}, 4)
	finish := make(chan struct{})
	var finishOnce sync.Once
	unblock := func() { finishOnce.Do(func() { close(finish) }) }
	t.Cleanup(unblock)
	enter := func() {
		mu.Lock()
		active++
		if active > peak {
			peak = active
		}
		mu.Unlock()
		entered <- struct{}{}
	}
	leave := func() { mu.Lock(); active--; mu.Unlock() }
	native := mountTransportModule{http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { enter(); defer leave(); <-finish; w.WriteHeader(204) })}
	host, e := modules.NewHostWithLimit([]modules.Module{native}, []byte(hostToken), 2)
	if e != nil {
		t.Fatal(e)
	}
	_ = host.MarkReady()
	handler := Mount(host, newAuth(t, &authCore{}), blockedCatalogue{newStoreTransportFake(), enter, leave, finish}, &supportFake{})
	done := make(chan int, 2)
	go func() {
		done <- mountTransportCall(handler, "GET", "/extensions/v1/transport/entry", []string{"Bearer " + hostToken}).Code
	}()
	go func() { done <- mountTransportCall(handler, "GET", "/api/store/products", nil).Code }()
	for i := 0; i < 2; i++ {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("mixed route did not enter")
		}
	}
	for _, route := range []string{"/api/store/products", "/extensions/v1/transport/entry"} {
		w := mountTransportCall(handler, "GET", route, []string{"Bearer " + hostToken})
		if w.Code != 503 {
			t.Fatal(route, w.Code, w.Body.String())
		}
	}
	if release, e := host.TryAcquire(); !errors.Is(e, modules.ErrSaturated) {
		if release != nil {
			release()
		}
		t.Fatal("worker bypassed shared bound", e)
	}
	unblock()
	a, b := <-done, <-done
	if !((a == 200 && b == 204) || (a == 204 && b == 200)) {
		t.Fatal(a, b)
	}
	if w := mountTransportCall(handler, "GET", "/api/store/products", nil); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if peak != 2 || active != 0 {
		t.Fatal("mixed peak", peak, "active", active)
	}
}
