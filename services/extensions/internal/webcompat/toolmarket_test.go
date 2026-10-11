// Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later.
package webcompat

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pb "github.com/TokenNotIncluded/api.lmm.best/extensions/internal/corepb"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/toolmarket"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Transport fixtures only: no real Rust authority, marketplace import or remote
// execution is claimed by these HTTP contract tests.
type marketTransportFake struct {
	query                          toolmarket.WebQuery
	token, account, id, permission string
	authCalls, businessCalls       int
	authErr, businessErr           error
	request                        toolmarket.WebRequest
}

func (f *marketTransportFake) PublicWebConfig(context.Context) (map[string]any, error) {
	f.businessCalls++
	return map[string]any{"enabled": false, "quota_per_unit": 500000, "credits_per_usd": "500000", "capabilities": map[string]bool{"metamcp": false}}, f.businessErr
}
func (f *marketTransportFake) PublicWebCatalogue(_ context.Context, token, account string, q toolmarket.WebQuery) ([]toolmarket.WebSummary, error) {
	f.businessCalls++
	f.token, f.account, f.query = token, account, q
	return nil, f.businessErr
}
func (f *marketTransportFake) PublicWebDetail(_ context.Context, token, account, id string) (toolmarket.WebDetail, error) {
	f.businessCalls++
	f.token, f.account, f.id = token, account, id
	return toolmarket.WebDetail{Tools: []toolmarket.WebTool{}, Pricing: "free"}, f.businessErr
}
func (f *marketTransportFake) WebAuthorize(_ context.Context, token, account, permission string) (int64, error) {
	f.authCalls++
	f.token, f.account, f.permission = token, account, permission
	return 17, f.authErr
}
func (f *marketTransportFake) LegacyWeb(_ context.Context, token, account string, r toolmarket.WebRequest) (any, error) {
	f.businessCalls++
	f.token, f.account, f.request = token, account, r
	return nil, f.businessErr
}
func marketMux(t *testing.T, core Core, service ToolMarketService) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	RegisterToolMarket(mux, newAuth(t, core), service)
	return mux
}
func marketHTTP(method, path, body string, token bool) *http.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	if token {
		r.Header.Set("Authorization", "Bearer "+webToken)
	}
	return r
}
func marketEnvelope(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err, rec.Body.String())
	}
	return body
}

func TestToolMarketPublicCatalogUsesLegacyRawArrayAndRealWebQuery(t *testing.T) {
	core, f := &authCore{}, &marketTransportFake{}
	r := marketHTTP("GET", "/api/tool-market?q=%E4%B8%AD%E6%96%87%25_&offset=30&limit=30&execution_type=remote", "", false)
	r.Header.Set("Cookie", "session="+hostToken)
	r.Header.Set("X-LMM-User-ID", "999")
	r.Header.Set("X-LMM-Account-ID", "999")
	rec := httptest.NewRecorder()
	marketMux(t, core, f).ServeHTTP(rec, r)
	body := marketEnvelope(t, rec)
	if rec.Code != 200 || body["success"] != true || core.calls != 0 || f.businessCalls != 1 || f.token != "" || f.account != "" || f.query.Text != "中文%_" || f.query.Offset != 30 || f.query.Limit != 30 || f.query.ExecutionType != "remote" {
		t.Fatal(rec.Code, body, f)
	}
	if rows, ok := body["data"].([]any); !ok || len(rows) != 0 {
		t.Fatal("catalog must be raw nonnull array", body)
	}
}
func TestToolMarketOptionalInvalidSessionDoesNotBecomeAnonymous(t *testing.T) {
	core := &authCore{fn: func(context.Context, string) (*pb.AuthorizeResponse, error) {
		return nil, status.Error(codes.Unauthenticated, "synthetic revoked")
	}}
	f := &marketTransportFake{}
	for _, path := range []string{"/api/tool-market", "/api/tool-market/config", "/api/tool-market/services/service-1"} {
		rec := httptest.NewRecorder()
		marketMux(t, core, f).ServeHTTP(rec, marketHTTP("GET", path, "", true))
		if rec.Code != 401 || f.businessCalls != 0 || f.authCalls != 0 {
			t.Fatal(path, rec.Code, f)
		}
	}
}

type marketUnreadBody struct{ reads int }

func (b *marketUnreadBody) Read([]byte) (int, error) {
	b.reads++
	return 0, errors.New("private input must not be read")
}
func (*marketUnreadBody) Close() error { return nil }
func TestToolMarketActionAuthorizationPrecedesBodyAndBusinessPort(t *testing.T) {
	core, f := &authCore{}, &marketTransportFake{authErr: toolmarket.ErrDenied}
	reader := &marketUnreadBody{}
	r := marketHTTP("POST", "/api/tool-market/services", "", true)
	r.Body = reader
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-LMM-Account-ID", "999")
	rec := httptest.NewRecorder()
	marketMux(t, core, f).ServeHTTP(rec, r)
	if rec.Code != 403 || core.calls != 1 || f.authCalls != 1 || f.permission != "toolmarket.manage" || f.account != "17" || f.token != webToken || f.businessCalls != 0 || reader.reads != 0 {
		t.Fatal(rec.Code, f, reader.reads)
	}
}
func TestToolMarketIdentityAuthorizationPrecedesActionAndBody(t *testing.T) {
	for _, authorization := range []string{"", "Bearer " + hostToken, "Bearer lmmt_" + strings.Repeat("x", 43)} {
		core, f := &authCore{}, &marketTransportFake{}
		reader := &marketUnreadBody{}
		r := marketHTTP("POST", "/api/tool-market/services", "", false)
		r.Header.Set("Authorization", authorization)
		r.Body = reader
		rec := httptest.NewRecorder()
		marketMux(t, core, f).ServeHTTP(rec, r)
		if rec.Code != 401 || core.calls != 0 || f.authCalls != 0 || f.businessCalls != 0 || reader.reads != 0 {
			t.Fatal(authorization, rec.Code, f, reader.reads)
		}
	}
}
func TestToolMarketDraftBodyKeepsExactSchemaNumbersAndCredits(t *testing.T) {
	core, f := &authCore{}, &marketTransportFake{}
	raw := `{"name":"precise","tools":[{"price_quota":9007199254740991,"input_schema":{"type":"object","properties":{"value":{"type":"integer","maximum":9007199254740993}}}}]}`
	r := marketHTTP("PUT", "/api/tool-market/services/service-1/draft", raw, true)
	delete(r.Header, "Authorization")
	r.Header["authorization"] = []string{"Bearer " + webToken}
	rec := httptest.NewRecorder()
	marketMux(t, core, f).ServeHTTP(rec, r)
	if rec.Code != 200 || f.account != "17" || f.token != webToken || f.request.Action != "save" || f.request.ID != "service-1" || string(f.request.Body) != raw {
		t.Fatal(rec.Code, f)
	}
}
func TestToolMarketPaginationAndQueryAmbiguityRejectBeforeBusiness(t *testing.T) {
	for _, path := range []string{"/api/tool-market?offset=-1", "/api/tool-market?limit=101", "/api/tool-market?offset=10001", "/api/tool-market?q=a&q=b", "/api/tool-market?limit=1&limit=2", "/api/tool-market?limit=wat", "/api/tool-market?unknown=x", "/api/tool-market/mine/services?offset=0&offset=1"} {
		core, f := &authCore{}, &marketTransportFake{}
		rec := httptest.NewRecorder()
		marketMux(t, core, f).ServeHTTP(rec, marketHTTP("GET", path, "", true))
		if rec.Code != 422 || f.businessCalls != 0 {
			t.Fatal(path, rec.Code, f)
		}
	}
}
func TestToolMarketMineAndReviewPathsRequestSeparateRustActions(t *testing.T) {
	for _, tc := range []struct{ path, action, permission string }{{"/api/tool-market/mine/installations?offset=20&limit=100", "mine", "toolmarket.read"}, {"/api/tool-market/reviews", "reviews", "toolmarket.review"}, {"/api/tool-market/services/service-1/review", "review_detail", "toolmarket.review"}} {
		core, f := &authCore{}, &marketTransportFake{}
		rec := httptest.NewRecorder()
		marketMux(t, core, f).ServeHTTP(rec, marketHTTP("GET", tc.path, "", true))
		if rec.Code != 200 || f.request.Action != tc.action || f.permission != tc.permission {
			t.Fatal(tc, rec.Code, f)
		}
	}
}
func TestToolMarketUnavailablePaidOperationsKeepSafeDomainError(t *testing.T) {
	for _, tc := range []struct{ method, path string }{{"POST", "/api/tool-market/invoke"}, {"POST", "/api/tool-market/grants"}, {"PUT", "/api/tool-market/budgets"}} {
		core, f := &authCore{}, &marketTransportFake{businessErr: toolmarket.ErrFunds}
		rec := httptest.NewRecorder()
		marketMux(t, core, f).ServeHTTP(rec, marketHTTP(tc.method, tc.path, `{}`, true))
		body := marketEnvelope(t, rec)
		if rec.Code != 503 || body["success"] != false || body["code"] != "TOOL_MARKET_FUNDS_UNAVAILABLE" {
			t.Fatal(tc, rec.Code, body)
		}
	}
}
func TestToolMarketUnsupportedAndMissingModuleNeverReturnSuccessfulEmptyData(t *testing.T) {
	for _, service := range []ToolMarketService{nil, (*toolmarket.Module)(nil)} {
		core := &authCore{}
		for _, path := range []string{"/api/tool-market", "/api/tool-market/config", "/api/tool-market/services/synthetic"} {
			rec := httptest.NewRecorder()
			marketMux(t, core, service).ServeHTTP(rec, marketHTTP("GET", path, "", false))
			if rec.Code != 503 || marketEnvelope(t, rec)["success"] != false || core.calls != 0 {
				t.Fatal(path, rec.Code)
			}
		}
	}
	core, f := &authCore{}, &marketTransportFake{}
	rec := httptest.NewRecorder()
	marketMux(t, core, f).ServeHTTP(rec, marketHTTP("GET", "/api/tool-market/calls", "", true))
	if rec.Code != 503 || f.businessCalls != 0 || f.authCalls != 1 {
		t.Fatal(rec.Code, f)
	}
}
func TestToolMarketBodyLimitsAndPrivateErrors(t *testing.T) {
	for _, body := range []string{"", strings.Repeat("x", (256<<10)+1)} {
		core, f := &authCore{}, &marketTransportFake{}
		rec := httptest.NewRecorder()
		marketMux(t, core, f).ServeHTTP(rec, marketHTTP("POST", "/api/tool-market/services", body, true))
		if rec.Code != 422 || f.businessCalls != 0 {
			t.Fatal(rec.Code, f)
		}
	}
	core, f := &authCore{}, &marketTransportFake{businessErr: errors.New("postgres://private-password@secret-host/private-db")}
	rec := httptest.NewRecorder()
	marketMux(t, core, f).ServeHTTP(rec, marketHTTP("GET", "/api/tool-market", "", false))
	if rec.Code != 503 || strings.Contains(rec.Body.String(), "private-password") || strings.Contains(rec.Body.String(), "secret-host") {
		t.Fatal(rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal(rec.Header())
	}
}

var _ io.ReadCloser = (*marketUnreadBody)(nil)
