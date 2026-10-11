// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
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
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/support"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type supportCall struct {
	method, token, id, other, body string
	actor, number, revision        int64
	offset, limit                  int
	unread                         bool
	tags                           []string
}
type supportFake struct {
	calls        []supportCall
	err          error
	conversation support.WebConversation
	history      support.WebHistory
	message      support.WebMessage
	threads      []support.WebThread
	customers    []support.WebCustomer
}

func (s *supportFake) call(c supportCall)                                        { s.calls = append(s.calls, c) }
func (s *supportFake) RateLimitWeb(context.Context, string, int64, string) error { return nil }
func (s *supportFake) OpenWeb(_ context.Context, token string, actor int64, productID, orderID string) (support.WebConversation, error) {
	s.call(supportCall{method: "open", token: token, actor: actor, id: productID, other: orderID})
	return s.conversation, s.err
}
func (s *supportFake) ListWeb(_ context.Context, token string, actor int64, role, state string, unread bool, offset, limit int) ([]support.WebThread, error) {
	s.call(supportCall{method: "list", token: token, actor: actor, id: role, other: state, unread: unread, offset: offset, limit: limit})
	return s.threads, s.err
}
func (s *supportFake) HistoryWeb(_ context.Context, token string, actor int64, id string, before int64, limit int) (support.WebHistory, error) {
	s.call(supportCall{method: "history", token: token, actor: actor, id: id, number: before, limit: limit})
	return s.history, s.err
}
func (s *supportFake) SendWeb(_ context.Context, token string, actor int64, id, key, body string) (support.WebMessage, error) {
	s.call(supportCall{method: "send", token: token, actor: actor, id: id, other: key, body: body})
	return s.message, s.err
}
func (s *supportFake) MarkReadWeb(_ context.Context, token string, actor int64, id string, through int64) error {
	s.call(supportCall{method: "read", token: token, actor: actor, id: id, number: through})
	return s.err
}
func (s *supportFake) StatusWeb(_ context.Context, token string, actor int64, id, state string) error {
	s.call(supportCall{method: "status", token: token, actor: actor, id: id, other: state})
	return s.err
}
func (s *supportFake) AssistantContextWeb(_ context.Context, token string, actor int64, id string) (map[string]any, error) {
	s.call(supportCall{method: "context", token: token, actor: actor, id: id})
	return map[string]any{"conversation_id": id, "messages": []any{}}, s.err
}
func (s *supportFake) ListCustomersWeb(_ context.Context, token string, actor int64, q string, offset, limit int) ([]support.WebCustomer, error) {
	s.call(supportCall{method: "customers", token: token, actor: actor, id: q, offset: offset, limit: limit})
	return s.customers, s.err
}
func (s *supportFake) SaveCustomerWeb(_ context.Context, token string, actor, buyer int64, note string, tags []string, revision int64) error {
	s.call(supportCall{method: "save", token: token, actor: actor, number: buyer, body: note, tags: tags, revision: revision})
	return s.err
}

type supportRouteCase struct {
	method, path, body string
	call               supportCall
}

func supportRoutes() []supportRouteCase {
	return []supportRouteCase{
		{"GET", "/api/store/support/conversations?role=seller&status=resolved&unread=true&offset=2&limit=3", "", supportCall{method: "list", id: "seller", other: "resolved", unread: true, offset: 2, limit: 3}},
		{"POST", "/api/store/support/conversations", `{"product_id":"` + strings.Repeat("a", 64) + `"}`, supportCall{method: "open", id: strings.Repeat("a", 64)}},
		{"GET", "/api/store/support/conversations/thread-1/messages?before=9223372036854775807&limit=100", "", supportCall{method: "history", id: "thread-1", number: 9223372036854775807, limit: 100}},
		{"POST", "/api/store/support/conversations/thread-1/messages", `{"request_key":"request-0001","body":"  hello\n世界  "}`, supportCall{method: "send", id: "thread-1", other: "request-0001", body: "hello\n世界"}},
		{"PUT", "/api/store/support/conversations/thread-1/read", `{"through_id":42}`, supportCall{method: "read", id: "thread-1", number: 42}},
		{"PUT", "/api/store/support/conversations/thread-1/status", `{"status":"resolved"}`, supportCall{method: "status", id: "thread-1", other: "resolved"}},
		{"GET", "/api/store/support/conversations/thread-1/assistant-context", "", supportCall{method: "context", id: "thread-1"}},
		{"GET", "/api/store/my/customers?q=%20hello%20&offset=3&limit=2", "", supportCall{method: "customers", id: "hello", offset: 3, limit: 2}},
		{"PUT", "/api/store/my/customers/99", `{"note":"  customer note  ","tags":[" hello ","hello","world"],"revision":3}`, supportCall{method: "save", number: 99, body: "customer note", tags: []string{"hello", "world"}, revision: 3}},
	}
}
func supportMux(t *testing.T, core Core, service SupportService) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	RegisterSupport(mux, newAuth(t, core), service)
	return mux
}
func supportRequest(tc supportRouteCase, bearer string) *http.Request {
	r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Cookie", "session="+webToken)
	r.Header.Set("X-LMM-User-ID", "999")
	r.Header.Set("X-LMM-Account-ID", "999")
	r.Header.Set("X-LMM-User-Credential", hostToken)
	return r
}
func supportEnvelope(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body
}

func TestSupportRoutesUseVerifiedActorAndPreserveLegacyArguments(t *testing.T) {
	for _, tc := range supportRoutes() {
		t.Run(tc.call.method, func(t *testing.T) {
			core, service := &authCore{}, &supportFake{}
			rec := httptest.NewRecorder()
			supportMux(t, core, service).ServeHTTP(rec, supportRequest(tc, webToken))
			want := tc.call
			want.actor, want.token = 17, webToken
			if rec.Code != 200 || core.calls != 1 || len(service.calls) != 1 || !reflect.DeepEqual(service.calls[0], want) {
				t.Fatalf("status=%d core=%d calls=%+v want=%+v body=%s", rec.Code, core.calls, service.calls, want, rec.Body.String())
			}
			body := supportEnvelope(t, rec)
			if body["success"] != true || body["message"] != "" || rec.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("body=%v headers=%v", body, rec.Header())
			}
			if tc.call.method == "list" || tc.call.method == "customers" {
				data := body["data"].(map[string]any)
				if !reflect.DeepEqual(data["items"], []any{}) || data["has_more"] != false || data["offset"] != float64(tc.call.offset) || data["limit"] != float64(tc.call.limit) {
					t.Fatal(data)
				}
				if _, ok := data["total"]; ok {
					t.Fatal("invented total")
				}
			}
			if tc.call.method == "read" || tc.call.method == "status" || tc.call.method == "save" {
				if !reflect.DeepEqual(body["data"], map[string]any{}) {
					t.Fatal(body)
				}
			}
			if strings.Contains(rec.Body.String(), "buyer-17") || strings.Contains(rec.Body.String(), "seller-17") {
				t.Fatal("invented display name")
			}
		})
	}
}

func TestSupportRoutesAllRequireBearerAndCoreVerification(t *testing.T) {
	for _, tc := range supportRoutes() {
		t.Run(tc.call.method, func(t *testing.T) {
			for _, bearer := range []string{"", hostToken, "eyJhbGciOiJIUzI1NiJ9.e30.signature"} {
				core, service := &authCore{}, &supportFake{}
				rec := httptest.NewRecorder()
				supportMux(t, core, service).ServeHTTP(rec, supportRequest(tc, bearer))
				if rec.Code != 401 || core.calls != 0 || len(service.calls) != 0 {
					t.Fatalf("status=%d core=%d service=%+v", rec.Code, core.calls, service.calls)
				}
			}
			core, service := &authCore{}, &supportFake{}
			core.fn = func(context.Context, string) (*pb.AuthorizeResponse, error) {
				return nil, status.Error(codes.Unauthenticated, "synthetic-secret")
			}
			rec := httptest.NewRecorder()
			supportMux(t, core, service).ServeHTTP(rec, supportRequest(tc, webToken))
			if rec.Code != 401 || core.calls != 1 || len(service.calls) != 0 || strings.Contains(rec.Body.String(), "synthetic-secret") {
				t.Fatalf("status=%d core=%d body=%s", rec.Code, core.calls, rec.Body.String())
			}
		})
	}
}

func TestSupportRoutesDoNotCacheAuthorityOrTrustIdentityHeaders(t *testing.T) {
	core, service := &authCore{}, &supportFake{}
	core.fn = func(context.Context, string) (*pb.AuthorizeResponse, error) {
		if core.calls == 1 {
			return sessionSelf(), nil
		}
		return nil, status.Error(codes.Unauthenticated, "revoked")
	}
	mux := supportMux(t, core, service)
	tc := supportRoutes()[0]
	for i, want := range []int{200, 401} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, supportRequest(tc, webToken))
		if rec.Code != want {
			t.Fatalf("request %d status=%d", i, rec.Code)
		}
	}
	if core.calls != 2 || len(service.calls) != 1 || service.calls[0].actor != 17 {
		t.Fatalf("core=%d calls=%+v", core.calls, service.calls)
	}
}

func TestSupportRoutesRejectNonInteractiveIdentityAndAmbiguousBearer(t *testing.T) {
	for _, self := range []*pb.AuthorizeResponse{
		{UserId: 17, CredentialKind: pb.CredentialKind_CREDENTIAL_KIND_API_KEY, Owner: sessionSelf().Owner},
		{UserId: 17, CredentialKind: pb.CredentialKind_CREDENTIAL_KIND_SESSION, Owner: &pb.Account{Kind: pb.AccountKind_ACCOUNT_KIND_PERSONAL, Id: 999}},
	} {
		core, service := &authCore{}, &supportFake{}
		core.fn = func(context.Context, string) (*pb.AuthorizeResponse, error) { return self, nil }
		rec := httptest.NewRecorder()
		supportMux(t, core, service).ServeHTTP(rec, supportRequest(supportRoutes()[0], webToken))
		if rec.Code != 403 || len(service.calls) != 0 {
			t.Fatalf("status=%d calls=%v", rec.Code, service.calls)
		}
	}
	core, service := &authCore{}, &supportFake{}
	r := supportRequest(supportRoutes()[0], webToken)
	r.Header.Add("Authorization", "Bearer "+webToken)
	rec := httptest.NewRecorder()
	supportMux(t, core, service).ServeHTTP(rec, r)
	if rec.Code != 401 || core.calls != 0 || len(service.calls) != 0 {
		t.Fatalf("status=%d", rec.Code)
	}
	r = supportRequest(supportRoutes()[0], webToken)
	delete(r.Header, "Authorization")
	r.Header["authorization"] = []string{"Bearer " + webToken}
	rec = httptest.NewRecorder()
	supportMux(t, core, service).ServeHTTP(rec, r)
	if rec.Code != 200 || len(service.calls) != 1 || service.calls[0].token != webToken {
		t.Fatalf("lowercase header status=%d calls=%+v", rec.Code, service.calls)
	}
}

func TestSupportRoutesPreservePaginationDefaultsAndOrderOpening(t *testing.T) {
	for _, tc := range []supportRouteCase{
		{"GET", "/api/store/support/conversations", "", supportCall{method: "list", id: "buyer", limit: 30}},
		{"GET", "/api/store/support/conversations/thread-1/messages", "", supportCall{method: "history", id: "thread-1", limit: 50}},
		{"GET", "/api/store/my/customers", "", supportCall{method: "customers", limit: 30}},
		{"POST", "/api/store/support/conversations", `{"order_id":"order-1"}`, supportCall{method: "open", other: "order-1"}},
	} {
		service := &supportFake{}
		rec := httptest.NewRecorder()
		supportMux(t, &authCore{}, service).ServeHTTP(rec, supportRequest(tc, webToken))
		want := tc.call
		want.token, want.actor = webToken, 17
		if rec.Code != 200 || len(service.calls) != 1 || !reflect.DeepEqual(service.calls[0], want) {
			t.Fatalf("status=%d calls=%+v want=%+v", rec.Code, service.calls, want)
		}
	}
}

func TestSupportRoutesRejectMalformedBodyBeforeBusinessCall(t *testing.T) {
	message := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	send := func(body string) string { return message(map[string]any{"request_key": "request-0001", "body": body}) }
	save := func(v any) string { return message(v) }
	for _, tc := range []supportRouteCase{
		{"POST", "/api/store/support/conversations", `{}`, supportCall{}},
		{"POST", "/api/store/support/conversations", `{"product_id":"a","order_id":"b"}`, supportCall{}},
		{"POST", "/api/store/support/conversations", `{"product_id":"unsafe/id"}`, supportCall{}},
		{"POST", "/api/store/support/conversations", `{"product_id":"` + strings.Repeat("x", 129) + `"}`, supportCall{}},
		{"POST", "/api/store/support/conversations", `{"product_id":"a","user_id":999}`, supportCall{}},
		{"POST", "/api/store/support/conversations", `{"Product_ID":"a"}`, supportCall{}},
		{"POST", "/api/store/support/conversations", `{"product_id":"a","product_id":"b"}`, supportCall{}},
		{"POST", "/api/store/support/conversations", `{"product_id":"a","product_\u0069d":"b"}`, supportCall{}},
		{"POST", "/api/store/support/conversations", `{"product_id":"a"}{}`, supportCall{}},
		{"POST", "/api/store/support/conversations", `null`, supportCall{}},
		{"POST", "/api/store/support/conversations", `[]`, supportCall{}},
		{"POST", "/api/store/support/conversations", `{"product_id":null}`, supportCall{}},
		{"POST", "/api/store/support/conversations/thread-1/messages", `{"request_key":"short","body":"hello"}`, supportCall{}},
		{"POST", "/api/store/support/conversations/thread-1/messages", `{"request_key":"invalid:key","body":"hello"}`, supportCall{}},
		{"POST", "/api/store/support/conversations/thread-1/messages", send("   "), supportCall{}},
		{"POST", "/api/store/support/conversations/thread-1/messages", send(strings.Repeat("界", 4001)), supportCall{}},
		{"POST", "/api/store/support/conversations/thread-1/messages", send("a\x00b"), supportCall{}},
		{"POST", "/api/store/support/conversations/thread-1/messages", `{"request_key":"request-0001","body":"a","body":"b"}`, supportCall{}},
		{"PUT", "/api/store/support/conversations/thread-1/read", `{"through_id":0}`, supportCall{}},
		{"PUT", "/api/store/support/conversations/thread-1/read", `{"through_id":-1}`, supportCall{}},
		{"PUT", "/api/store/support/conversations/thread-1/read", `{"through_id":1.1}`, supportCall{}},
		{"PUT", "/api/store/support/conversations/thread-1/read", `{"through_id":9223372036854775808}`, supportCall{}},
		{"PUT", "/api/store/support/conversations/thread-1/read", `{"through_id":1,"actor":999}`, supportCall{}},
		{"PUT", "/api/store/support/conversations/thread-1/status", `{"status":"closed"}`, supportCall{}},
		{"PUT", "/api/store/support/conversations/thread-1/status", `{"status":"resolved","status":"open"}`, supportCall{}},
		{"PUT", "/api/store/my/customers/0", `{}`, supportCall{}},
		{"PUT", "/api/store/my/customers/wrong", `{}`, supportCall{}},
		{"PUT", "/api/store/my/customers/9223372036854775808", `{}`, supportCall{}},
		{"PUT", "/api/store/my/customers/99", `{"revision":-1}`, supportCall{}},
		{"PUT", "/api/store/my/customers/99", `{"revision":9007199254740991}`, supportCall{}},
		{"PUT", "/api/store/my/customers/99", `{"tags":[""]}`, supportCall{}},
		{"PUT", "/api/store/my/customers/99", `{"tags":null}`, supportCall{}},
		{"PUT", "/api/store/my/customers/99", save(map[string]any{"tags": []string{strings.Repeat("界", 33)}}), supportCall{}},
		{"PUT", "/api/store/my/customers/99", save(map[string]any{"tags": []string{"a\nb"}}), supportCall{}},
		{"PUT", "/api/store/my/customers/99", save(map[string]any{"tags": strings.Split("a,b,c,d,e,f,g,h,i,j,k", ",")}), supportCall{}},
		{"PUT", "/api/store/my/customers/99", save(map[string]any{"note": strings.Repeat("界", 4001)}), supportCall{}},
		{"PUT", "/api/store/my/customers/99", `{"note":"a","note":"b"}`, supportCall{}},
	} {
		t.Run(fmt.Sprintf("%s-%d", tc.method, len(tc.body)), func(t *testing.T) {
			core, service := &authCore{}, &supportFake{}
			rec := httptest.NewRecorder()
			supportMux(t, core, service).ServeHTTP(rec, supportRequest(tc, webToken))
			if rec.Code != 422 || core.calls != 1 || len(service.calls) != 0 {
				t.Fatalf("path=%s status=%d calls=%+v body=%s", tc.path, rec.Code, service.calls, rec.Body.String())
			}
		})
	}
}

func TestSupportRoutesRejectMalformedOrAmbiguousQuery(t *testing.T) {
	for _, target := range []string{
		"/api/store/support/conversations?role=admin", "/api/store/support/conversations?role=", "/api/store/support/conversations?status=closed",
		"/api/store/support/conversations?unread=1", "/api/store/support/conversations?unread=true&unread=false", "/api/store/support/conversations?offset=1&offset=2",
		"/api/store/support/conversations?limit=0", "/api/store/support/conversations?limit=101", "/api/store/support/conversations?offset=100001",
		"/api/store/support/conversations?actor=999", "/api/store/support/conversations?role=%ZZ", "/api/store/support/conversations?role=buyer;unread=true",
		"/api/store/support/conversations/thread-1/messages?before=-1", "/api/store/support/conversations/thread-1/messages?before=9223372036854775808",
		"/api/store/support/conversations/thread-1/messages?before=1&before=1", "/api/store/support/conversations/thread-1/messages?limit=101",
		"/api/store/support/conversations/thread-1/messages?offset=1", "/api/store/support/conversations/thread-1/assistant-context?actor=999",
		"/api/store/my/customers?q=a&q=b", "/api/store/my/customers?q=" + strings.Repeat("x", 101), "/api/store/my/customers?offset=-1",
		"/api/store/my/customers?user_id=999", "/api/store/my/customers?q=" + strings.Repeat("x", 8193),
	} {
		t.Run(target, func(t *testing.T) {
			core, service := &authCore{}, &supportFake{}
			rec := httptest.NewRecorder()
			supportMux(t, core, service).ServeHTTP(rec, supportRequest(supportRouteCase{method: "GET", path: target}, webToken))
			if rec.Code != 422 || core.calls != 1 || len(service.calls) != 0 {
				t.Fatalf("status=%d calls=%+v body=%s", rec.Code, service.calls, rec.Body.String())
			}
		})
	}
}

func TestSupportRoutesEnforceBodyLimitsAndJSONContentType(t *testing.T) {
	for _, tc := range []supportRouteCase{
		{method: "POST", path: "/api/store/support/conversations", body: `{"product_id":"a"}` + strings.Repeat(" ", 4<<10)},
		{method: "POST", path: "/api/store/support/conversations/thread-1/messages", body: `{"request_key":"request-0001","body":"a"}` + strings.Repeat(" ", 32<<10)},
		{method: "PUT", path: "/api/store/support/conversations/thread-1/read", body: `{"through_id":1}` + strings.Repeat(" ", 1<<10)},
		{method: "PUT", path: "/api/store/support/conversations/thread-1/status", body: `{"status":"open"}` + strings.Repeat(" ", 1<<10)},
		{method: "PUT", path: "/api/store/my/customers/99", body: `{}` + strings.Repeat(" ", 32<<10)},
	} {
		core, service := &authCore{}, &supportFake{}
		rec := httptest.NewRecorder()
		supportMux(t, core, service).ServeHTTP(rec, supportRequest(tc, webToken))
		if rec.Code != 422 || len(service.calls) != 0 {
			t.Fatalf("path=%s status=%d", tc.path, rec.Code)
		}
	}
	for _, typ := range []string{"text/plain", "application/json; charset=latin-1", "application/json, application/json"} {
		service := &supportFake{}
		r := supportRequest(supportRoutes()[1], webToken)
		r.Header.Set("Content-Type", typ)
		rec := httptest.NewRecorder()
		supportMux(t, &authCore{}, service).ServeHTTP(rec, r)
		if rec.Code != 422 || len(service.calls) != 0 {
			t.Fatalf("type=%s status=%d", typ, rec.Code)
		}
	}
	service := &supportFake{}
	r := supportRequest(supportRoutes()[1], webToken)
	r.Header.Add("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	supportMux(t, &authCore{}, service).ServeHTTP(rec, r)
	if rec.Code != 422 || len(service.calls) != 0 {
		t.Fatalf("duplicate type status=%d", rec.Code)
	}
}

func TestSupportRoutesMapServiceFailuresWithoutRawData(t *testing.T) {
	for _, tc := range supportRoutes() {
		service := &supportFake{err: fmt.Errorf("synthetic SQL/provider secret: %w", store.ErrForbidden)}
		rec := httptest.NewRecorder()
		supportMux(t, &authCore{}, service).ServeHTTP(rec, supportRequest(tc, webToken))
		body := supportEnvelope(t, rec)
		if rec.Code != 403 || body["success"] != false || body["code"] != "STORE_ACCESS_DENIED" || strings.Contains(rec.Body.String(), "synthetic") {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		if _, ok := body["data"]; ok {
			t.Fatal("private service value on error")
		}
	}
	var absent *supportFake
	for _, service := range []SupportService{nil, absent} {
		rec := httptest.NewRecorder()
		supportMux(t, &authCore{}, service).ServeHTTP(rec, supportRequest(supportRoutes()[0], webToken))
		if rec.Code != 503 || !strings.Contains(rec.Body.String(), "STORE_UNAVAILABLE") {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
	}
}

func TestSupportRoutePatternsDoNotExposeModuleInternalRoutes(t *testing.T) {
	service := &supportFake{}
	mux := supportMux(t, &authCore{}, service)
	for _, tc := range []supportRouteCase{{method: "GET", path: "/api/store/support/conversations/thread-1"}, {method: "POST", path: "/api/store/support/conversations/change"}, {method: "DELETE", path: "/api/store/support/conversations"}, {method: "GET", path: "/support/conversations"}} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, supportRequest(tc, webToken))
		if rec.Code != 404 && rec.Code != 405 {
			t.Fatalf("%s %s status=%d", tc.method, tc.path, rec.Code)
		}
	}
	if len(service.calls) != 0 {
		t.Fatal(service.calls)
	}
}

var _ SupportService = (*supportFake)(nil)

// These are explicit transport fixtures, not database or identity integration.
func TestSupportRoutesForwardServiceDTOsWithoutRewritingThem(t *testing.T) {
	conversation := support.WebConversation{ID: "thread-1", BuyerID: 17, SellerID: 99, ProductID: "product-1", Subject: "service subject", Status: "resolved", LastMessageID: 42, BuyerReadID: 21, SellerReadID: 40, CreatedAt: 100, UpdatedAt: 200}
	message := support.WebMessage{ID: 42, ConversationID: "thread-1", SenderID: 99, Body: "service body", CreatedAt: 200}
	history := support.WebHistory{Conversation: conversation, Items: []support.WebMessage{message}, HasMore: true}
	thread := support.WebThread{WebConversation: conversation, UnreadCount: 3}
	customer := support.WebCustomer{BuyerID: 17, DisplayName: "service supplied name", OrderCount: 2, PaidQuota: 500000, LastOrderAt: 200, LastOrderID: "order-1", ConversationID: "thread-1", Note: "service private note", Tags: []string{"service tag"}, Revision: 3}
	routes := supportRoutes()
	for _, tc := range []struct {
		request supportRouteCase
		want    any
	}{
		{routes[1], conversation}, {routes[2], history}, {routes[3], message},
		{supportRouteCase{method: "GET", path: "/api/store/support/conversations?limit=1"}, map[string]any{"items": []support.WebThread{thread}, "offset": 0, "limit": 1, "has_more": true}},
		{supportRouteCase{method: "GET", path: "/api/store/my/customers?limit=1"}, map[string]any{"items": []support.WebCustomer{customer}, "offset": 0, "limit": 1, "has_more": true}},
	} {
		service := &supportFake{conversation: conversation, message: message, history: history, threads: []support.WebThread{thread}, customers: []support.WebCustomer{customer}}
		rec := httptest.NewRecorder()
		supportMux(t, &authCore{}, service).ServeHTTP(rec, supportRequest(tc.request, webToken))
		raw, err := json.Marshal(tc.want)
		if err != nil {
			t.Fatal(err)
		}
		var want any
		if err = json.Unmarshal(raw, &want); err != nil {
			t.Fatal(err)
		}
		got := supportEnvelope(t, rec)
		if rec.Code != 200 || !reflect.DeepEqual(got["data"], want) {
			t.Fatalf("status=%d got=%v want=%v", rec.Code, got, want)
		}
	}
}

type supportNeverStorage struct{ calls int }

func (s *supportNeverStorage) Within(context.Context, string, func(support.Tx) error) error {
	s.calls++
	return store.ErrUnavailable
}

type supportNeverDirectory struct{ calls int }

func (d *supportNeverDirectory) Owner(context.Context, string) (store.Account, error) {
	d.calls++
	return store.Account{}, store.ErrUnavailable
}
func (d *supportNeverDirectory) Parties(context.Context, string, string) (store.Account, store.Account, error) {
	d.calls++
	return store.Account{}, store.Account{}, store.ErrUnavailable
}

type supportLiveAuthority struct{ calls int }

func (a *supportLiveAuthority) Check(_ context.Context, token string, account store.Account, _ store.Permission) (int64, error) {
	a.calls++
	if token != webToken || account.Kind != "personal" || account.ID != 17 {
		return 0, store.ErrForbidden
	}
	return 17, nil
}

// Actual customer methods are deliberately unavailable until their required core
// profile and credit aggregate ports exist. A successful empty list would lie.
func TestSupportCustomerRoutesKeepActualMissingCapabilitiesUnavailable(t *testing.T) {
	for _, tc := range []supportRouteCase{supportRoutes()[7], supportRoutes()[8]} {
		storage, directory, authority := &supportNeverStorage{}, &supportNeverDirectory{}, &supportLiveAuthority{}
		service, err := support.New(storage, authority, directory)
		if err != nil {
			t.Fatal(err)
		}
		core := &authCore{}
		rec := httptest.NewRecorder()
		supportMux(t, core, service).ServeHTTP(rec, supportRequest(tc, webToken))
		body := supportEnvelope(t, rec)
		if rec.Code != 503 || body["success"] != false || body["code"] != "STORE_UNAVAILABLE" || core.calls != 1 || authority.calls != 1 || storage.calls != 0 || directory.calls != 0 {
			t.Fatalf("status=%d body=%v core=%d authority=%d storage=%d directory=%d", rec.Code, body, core.calls, authority.calls, storage.calls, directory.calls)
		}
		if _, ok := body["data"]; ok {
			t.Fatal("unavailable customer capability reported fabricated data")
		}
	}
}
