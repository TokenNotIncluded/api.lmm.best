package commerceimport

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const fixtureOrigin = "https://commerce.example"
const fixtureVerifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
const fixtureState = "independent-state-value-123456789"
const fixtureAccess = "fixture-access-secret-0001"
const fixtureRefresh = "fixture-refresh-secret-0001"
const fixtureNow int64 = 1800000000

func TestProtocolSchemaCompilesOffline(t *testing.T) {
	schemaOnce.Do(compileSchemas)
	if schemaError != nil {
		t.Fatal(schemaError)
	}
	if len(schemas) != 5 {
		t.Fatal("protocol schema definitions missing")
	}
}

func fixtureMetadata() Metadata {
	return Metadata{Issuer: fixtureOrigin, AuthorizationEndpoint: fixtureOrigin + authorizePath,
		TokenEndpoint: fixtureOrigin + tokenPath, RevocationEndpoint: fixtureOrigin + revokePath,
		ProductsEndpoint: fixtureOrigin + productsPath, CardsEndpoint: fixtureOrigin + cardsPath, MaximumCardsPerRequest: 100}
}

func fixtureDiscovery() map[string]any {
	m := fixtureMetadata()
	return map[string]any{"issuer": m.Issuer, "authorization_endpoint": m.AuthorizationEndpoint,
		"token_endpoint": m.TokenEndpoint, "revocation_endpoint": m.RevocationEndpoint,
		"response_types_supported": []string{"code"}, "grant_types_supported": []string{"authorization_code", "refresh_token"},
		"token_endpoint_auth_methods_supported": []string{"none"}, "code_challenge_methods_supported": []string{"S256"},
		"scopes_supported": []string{"products.read", "cards.issue"}, "authorization_response_iss_parameter_supported": true,
		"extore_commerce": map[string]any{"schema": ProtocolSchema, "schema_uri": fixtureOrigin + "/api/integrations/commerce/schema",
			"products_endpoint": m.ProductsEndpoint, "cards_endpoint": m.CardsEndpoint, "listing_schema": ListingSchema,
			"maximum_products": 100, "maximum_cards_per_request": 100, "issuance_recovery_seconds": 86400}}
}

func fixtureTokens() map[string]any {
	return map[string]any{"access_token": fixtureAccess, "refresh_token": fixtureRefresh, "token_type": "Bearer",
		"expires_in": 900, "scope": "products.read", "grant_id": "grant-fixture", "grant_expires": json.Number("1800000900.5")}
}

func fixtureVariant() map[string]any {
	return map[string]any{"id": "standard", "name": "标准版", "description": "独立夹具规格", "price": "00025.100000",
		"currency": "CNY", "enabled": true, "attributes": map[string]any{"revisions": 0, "feature": true, "unknown": nil}}
}

func fixtureListing() map[string]any {
	return map[string]any{"schema": ListingSchema, "id": "document_service", "shop_id": "shop-fixture",
		"revision": strings.Repeat("a", 64), "redemption_url": fixtureOrigin + "/", "semantics": map[string]any{
			"price": "reference", "inventory": "not_exported", "payment": "external_sales_platform", "redemption": "extore"},
		"product": map[string]any{"name": map[string]string{"zh-CN": "示例文档"}, "description": "保存原资料", "public": false,
			"mode": "manual", "delivery": "content", "view_policy": "repeat", "parameters": []any{}, "outputs": []any{}, "progress_steps": []any{},
			"future_product": map[string]any{"value": true}}, "variants": []any{fixtureVariant()}, "future_listing": "preserved"}
}

func fixtureBatch() map[string]any {
	return map[string]any{"schema": CardBatchSchema, "grant_id": "grant-fixture", "product_id": "document_service", "variant_id": "standard",
		"batch_id": "batch-fixture", "count": 2, "codes": []string{"fixture-card-secret-0001", "fixture-card-secret-0002"},
		"created_at": json.Number("1800000000.0"), "recovery_expires": json.Number("1800086400.0"),
		"quota": map[string]any{"max_count": 100, "issued_count": 2, "remaining": 98}, "variant": fixtureVariant(), "future_batch": "preserved"}
}

func jsonBytes(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(jsonBytes(t, value))
}

// The HTTP/TLS stack is real. Only DNS and dialing are redirected: a validated
// public answer is pinned, while a generated certificate verifies the public
// fixture hostname without InsecureSkipVerify.
func tlsFixture(t *testing.T, handler http.Handler) (*Client, *httptest.Server) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "commerce.example"},
		DNSNames: []string{"commerce.example"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certificate := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	server := httptest.NewUnstartedServer(handler)
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.TLS = &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}
	server.StartTLS()
	t.Cleanup(server.Close)
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(parsed)
	lookup := func(_ context.Context, network, host string) ([]netip.Addr, error) {
		if network != "ip" || host != "commerce.example" {
			return nil, ErrInvalidOrigin
		}
		return []netip.Addr{netip.MustParseAddr("1.1.1.1")}, nil
	}
	dial := func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != "1.1.1.1:443" {
			return nil, ErrInvalidOrigin
		}
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	client := &Client{http: newHTTPClient(lookup, dial, &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}), now: func() time.Time { return time.Unix(fixtureNow, 0) }}
	t.Cleanup(client.CloseIdleConnections)
	return client, server
}

func TestDiscoveryAndPKCE(t *testing.T) {
	client, _ := tlsFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != discoveryPath || r.Host != "commerce.example" || r.Header.Get("Authorization") != "" || r.TLS == nil || r.TLS.ServerName != "commerce.example" {
			t.Error("discovery request boundary violated")
		}
		writeJSON(t, w, fixtureDiscovery())
	}))
	metadata, err := client.Discover(context.Background(), fixtureOrigin)
	if err != nil {
		t.Fatal(err)
	}
	redirect := "https://Sales.example:443/oauth/callback?merchant=owner"
	authorization, err := AuthorizationURL(metadata, "client-fixture", redirect, fixtureState, fixtureVerifier, []string{"cards.issue", "products.read"})
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(authorization)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("code_challenge") != "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM" || q.Get("code_challenge_method") != "S256" || q.Get("redirect_uri") != redirect || q.Get("scope") != "products.read cards.issue" || q.Get("response_type") != "code" {
		t.Fatal("incorrect S256 authorization parameters")
	}
	if strings.Contains(authorization, fixtureVerifier) {
		t.Fatal("verifier entered browser URL")
	}
	for _, scopes := range [][]string{nil, {"admin"}, {"products.read", "products.read"}} {
		if _, err := AuthorizationURL(metadata, "client-fixture", redirect, fixtureState, fixtureVerifier, scopes); !errors.Is(err, ErrInvalidRequest) {
			t.Fatal("invalid scopes accepted")
		}
	}
	if _, err := AuthorizationURL(metadata, "client-fixture", redirect, "short", fixtureVerifier, []string{"products.read"}); err == nil {
		t.Fatal("short state accepted")
	}
	if _, err := AuthorizationURL(metadata, "client-fixture", redirect, fixtureState, strings.Repeat("+", 43), []string{"products.read"}); err == nil {
		t.Fatal("invalid verifier accepted")
	}
}

func callbackQuery(code string) string {
	return url.Values{"code": {code}, "state": {fixtureState}, "iss": {fixtureOrigin}}.Encode()
}

func TestCallbackTargetAndTransaction(t *testing.T) {
	q := callbackQuery("fixture-one-use-code")
	valid := []struct{ registered, actual string }{
		{"https://Sales.example", "https://sales.example:443/?" + q},
		{"https://sales.example:8443/cb?merchant=owner", "https://SALES.example:8443/cb?merchant=owner&" + q},
	}
	for _, value := range valid {
		code, err := ValidateCallback(value.registered, value.actual, fixtureState, fixtureOrigin)
		if err != nil || code != "fixture-one-use-code" {
			t.Fatal("valid callback normalization rejected")
		}
	}
	registered := "https://sales.example/cb?merchant=owner"
	base := "https://sales.example/cb?merchant=owner&" + q
	invalid := []string{
		strings.Replace(base, "sales.example", "other.example", 1), strings.Replace(base, "/cb?", "/other?", 1),
		strings.Replace(base, "sales.example/", "sales.example:8443/", 1), strings.Replace(base, "merchant=owner", "merchant=other", 1),
		base + "&extra=value", base + "&state=wrong", base + "&%73tate=wrong", base + "&code=another", base + "&iss=" + fixtureOrigin,
		base + "&merchant=owner", base + "&error=access_denied", base + "&error_description=secret", base + "#fragment", base + "&bad=%ZZ", base + ";bad=value",
		strings.Replace(base, fixtureState, "different-state-value-123456789", 1), strings.Replace(base, url.QueryEscape(fixtureOrigin), url.QueryEscape("https://other.example"), 1),
		strings.Replace(base, "code=fixture-one-use-code", "code=", 1),
	}
	for i, actual := range invalid {
		if _, err := ValidateCallback(registered, actual, fixtureState, fixtureOrigin); !errors.Is(err, ErrInvalidCallback) {
			t.Fatalf("invalid callback accepted at case %d", i)
		}
	}
	for _, badRegistered := range []string{"https://sales.example/cb?state", "https://sales.example/cb?state=", "https://sales.example/cb?merchant=a&merchant=a", "https://sales.example/cb?iss=x"} {
		if _, err := ValidateCallback(badRegistered, base, fixtureState, fixtureOrigin); err == nil {
			t.Fatal("invalid registration accepted")
		}
	}
	if _, err := ValidateCallback("https://sales.example/a%2Fb", "https://sales.example/a/b?"+q, fixtureState, fixtureOrigin); err == nil {
		t.Fatal("escaped-path difference accepted")
	}
	denial := "https://sales.example/cb?merchant=owner&" + url.Values{"error": {"access_denied"}, "error_description": {fixtureAccess}, "state": {fixtureState}, "iss": {fixtureOrigin}}.Encode()
	if _, err := ValidateCallback(registered, denial, fixtureState, fixtureOrigin); !errors.Is(err, ErrAuthorizationDenied) {
		t.Fatal("valid denial was not recognized")
	}
	if _, err := ValidateCallback(registered, strings.Replace(denial, fixtureState, "wrong-state-value-123456789", 1), fixtureState, fixtureOrigin); !errors.Is(err, ErrInvalidCallback) {
		t.Fatal("denial bypassed transaction verification")
	}
}

func TestOriginsAndPublicAddresses(t *testing.T) {
	for _, origin := range []string{"https://commerce.example", "https://COMMERCE.example:8443"} {
		if ValidateOrigin(origin) != nil {
			t.Fatal("valid public origin rejected")
		}
	}
	for _, origin := range []string{"http://commerce.example", "https://commerce.example/", "https://commerce.example?", "https://commerce.example#", "https://user:password@commerce.example", "https://127.0.0.1", "https://[::1]", "https://localhost", "https://foo.local", "https://foo.internal", "https://foo.localhost", "https://127.1", "https://*.example.com", "https://中文.example", "https://commerce.example:0", "https://commerce.example:", "https://commerce.example:0443", "https://commerce.example.:443"} {
		if ValidateOrigin(origin) == nil {
			t.Fatal("invalid origin accepted")
		}
	}
	for _, raw := range []string{"127.0.0.1", "10.1.1.1", "172.16.0.1", "192.168.1.1", "169.254.169.254", "100.64.1.1", "168.63.129.16", "192.0.2.1", "198.19.1.1", "0.0.0.1", "255.255.255.255", "224.0.0.1", "::1", "::ffff:127.0.0.1", "::127.0.0.1", "fc00::1", "fe80::1", "fec0::1", "100::1", "2001:db8::1", "2001:2::1", "2002:7f00:1::", "64:ff9b::7f00:1", "8000::1"} {
		if publicIP(netip.MustParseAddr(raw)) {
			t.Fatalf("nonpublic address accepted: %s", raw)
		}
	}
	for _, raw := range []string{"1.1.1.1", "8.8.8.8", "2606:4700:4700::1111", "::ffff:1.1.1.1"} {
		if !publicIP(netip.MustParseAddr(raw)) {
			t.Fatal("public address rejected")
		}
	}
}

func TestDNSChecksAllAnswersBeforePinnedDial(t *testing.T) {
	for _, answers := range [][]netip.Addr{
		{netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr("::1")},
		{netip.MustParseAddr("127.0.0.1")}, nil,
	} {
		var dials atomic.Int32
		client := newHTTPClient(func(context.Context, string, string) ([]netip.Addr, error) { return answers, nil },
			func(context.Context, string, string) (net.Conn, error) {
				dials.Add(1)
				return nil, errors.New("must not dial")
			}, nil)
		_, err := client.Get(fixtureOrigin + discoveryPath)
		client.CloseIdleConnections()
		if err == nil || dials.Load() != 0 {
			t.Fatal("invalid DNS answers reached dial")
		}
	}
	var lookups atomic.Int32
	var dials atomic.Int32
	client := newHTTPClient(func(_ context.Context, _, host string) ([]netip.Addr, error) {
		if host != "commerce.example" {
			t.Error("unexpected resolver target")
		}
		if lookups.Add(1) > 1 {
			return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
		}
		return []netip.Addr{netip.MustParseAddr("1.1.1.1")}, nil
	}, func(_ context.Context, _, address string) (net.Conn, error) {
		dials.Add(1)
		if address != "1.1.1.1:443" {
			t.Error("dial did not receive pinned public IP")
		}
		return nil, errors.New("offline fixture connection failure")
	}, nil)
	_, _ = client.Get(fixtureOrigin + discoveryPath)
	client.CloseIdleConnections()
	if lookups.Load() != 1 || dials.Load() != 1 {
		t.Fatal("DNS was resolved twice during one dial")
	}
}

func TestNoProxyRedirectOrTLSBypass(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("ALL_PROXY", "socks5://127.0.0.1:1")
	var calls atomic.Int32
	var redirect atomic.Bool
	client, _ := tlsFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if redirect.Load() {
			http.Redirect(w, r, "https://127.0.0.1/private", http.StatusFound)
			return
		}
		writeJSON(t, w, fixtureDiscovery())
	}))
	if _, err := client.Discover(context.Background(), fixtureOrigin); err != nil {
		t.Fatal("environment proxy affected direct request")
	}
	redirect.Store(true)
	if _, err := client.Discover(context.Background(), fixtureOrigin); !errors.Is(err, ErrInvalidResponse) || calls.Load() != 2 {
		t.Fatal("redirect was followed or accepted")
	}
	transport := client.http.Transport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	untrusted := &Client{http: &http.Client{Transport: transport, Timeout: time.Second, CheckRedirect: client.http.CheckRedirect}, now: client.now}
	t.Cleanup(untrusted.CloseIdleConnections)
	if _, err := untrusted.Discover(context.Background(), fixtureOrigin); !errors.Is(err, ErrNetwork) || calls.Load() != 2 {
		t.Fatal("untrusted TLS certificate was accepted")
	}
}

func TestWireOperationsAndRawReceipts(t *testing.T) {
	const redirect = "https://Sales.example:443/callback?merchant=owner"
	const persisted = "{ \"count\" : 2, \"variant_id\":\"standard\", \"product_id\":\"document_service\", \"expected_revision\":\"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\", \"label\":\"原始批次\" }\n"
	var tokenCalls, cardCalls atomic.Int32
	listing := fixtureListing()
	// Unknown response extensions, even mixed-case collisions, cannot override
	// canonical schema-checked fields during Go struct decoding.
	listing["PRODUCT"] = map[string]any{"name": "unvalidated override"}
	listing["VARIANTS"] = []any{}
	listing["variants"].([]any)[0].(map[string]any)["PRICE"] = "999999"
	batch := fixtureBatch()
	batch["CODES"] = []string{"duplicate-card-secret", "duplicate-card-secret"}
	batch["VARIANT"] = map[string]any{"id": "unvalidated"}
	batchRaw := jsonBytes(t, batch)
	client, _ := tlsFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" || r.Header.Get("Cookie") != "" || r.Header.Get("Proxy-Authorization") != "" {
			t.Error("credential or query boundary violated")
		}
		switch r.URL.Path {
		case tokenPath:
			tokenCalls.Add(1)
			if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" || r.Header.Get("Authorization") != "" {
				t.Error("token request encoding/auth invalid")
			}
			if err := r.ParseForm(); err != nil {
				t.Error(err)
			}
			switch r.Form.Get("grant_type") {
			case "authorization_code":
				if r.Form.Get("redirect_uri") != redirect || r.Form.Get("code_verifier") != fixtureVerifier || r.Form.Get("code") != "one-use-code" {
					t.Error("exchange modified registered URI or verifier")
				}
			case "refresh_token":
				if r.Form.Get("refresh_token") != fixtureRefresh {
					t.Error("incorrect refresh credential")
				}
			default:
				t.Error("unexpected grant type")
			}
			tokens := fixtureTokens()
			tokens["EXPIRES_IN"] = 999999
			tokens["ACCESS_TOKEN"] = "unvalidated-override"
			writeJSON(t, w, tokens)
		case revokePath:
			if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
				t.Error("revoke request encoding invalid")
			}
			_ = r.ParseForm()
			if r.Form.Get("token") != fixtureRefresh || r.Form.Get("client_id") != "client-fixture" || r.Form.Get("token_type_hint") != "refresh_token" {
				t.Error("revoke form invalid")
			}
			w.WriteHeader(http.StatusOK)
		case productsPath:
			if r.Header.Get("Authorization") != "Bearer "+fixtureAccess || r.Method != http.MethodGet {
				t.Error("catalog authorization invalid")
			}
			writeJSON(t, w, map[string]any{"schema": CatalogSchema, "issuer": fixtureOrigin, "grant_id": "grant-fixture", "shop": map[string]any{"id": "shop-fixture", "name": "夹具店铺"}, "products": []any{listing}})
		case productsPath + "/document_service":
			if r.Header.Get("Authorization") != "Bearer "+fixtureAccess || r.Method != http.MethodGet {
				t.Error("listing authorization invalid")
			}
			writeJSON(t, w, listing)
		case cardsPath:
			cardCalls.Add(1)
			body, _ := io.ReadAll(r.Body)
			if string(body) != persisted || r.Method != http.MethodPost || r.Header.Get("Idempotency-Key") != "durable-original-key" || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Authorization") != "Bearer "+fixtureAccess {
				t.Error("issuance failed to preserve durable wire request")
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(batchRaw)
		default:
			t.Error("unexpected protocol endpoint")
		}
	}))
	m := fixtureMetadata()
	tokens, err := client.Exchange(context.Background(), m, "client-fixture", redirect, "one-use-code", fixtureVerifier)
	if err != nil {
		t.Fatal(err)
	}
	if tokens.AccessToken != fixtureAccess || tokens.Scope != "products.read" || tokens.AccessExpires != fixtureNow+900 || tokens.GrantExpires != fixtureNow+900 {
		t.Fatal("token fields were not canonical or expiry was not bounded")
	}
	if _, err := client.Refresh(context.Background(), m, "client-fixture", fixtureRefresh); err != nil {
		t.Fatal(err)
	}
	if tokenCalls.Load() != 2 {
		t.Fatal("single-use requests were retried")
	}
	if err := client.Revoke(context.Background(), m, "client-fixture", fixtureRefresh); err != nil {
		t.Fatal(err)
	}
	catalog, err := client.FetchCatalog(context.Background(), m, fixtureAccess)
	if err != nil || len(catalog.Products) != 1 {
		t.Fatal("valid catalog rejected")
	}
	product, err := client.FetchListing(context.Background(), m, fixtureAccess, "document_service")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(product.Product, []byte("示例文档")) || len(product.Variants) != 1 || product.Variants[0].Price == nil || *product.Variants[0].Price != "00025.100000" || !bytes.Contains(product.Raw, []byte("semantics")) || !bytes.Contains(catalog.Products[0].Raw, []byte("future_listing")) {
		t.Fatal("canonical product data or complete source was lost")
	}
	issued, err := client.IssueCardsJSON(context.Background(), m, fixtureAccess, "durable-original-key", []byte(persisted))
	if err != nil {
		t.Fatal(err)
	}
	if issued.Count != 2 || issued.Codes[0] == issued.Codes[1] || issued.Codes[0] != "fixture-card-secret-0001" || !bytes.Equal(issued.Raw, batchRaw) || issued.RecoveryExpires != fixtureNow+86400 || cardCalls.Load() != 1 {
		t.Fatal("validated batch was overridden or receipt modified")
	}
	parsed, err := ParseCardBatch(issued.Raw)
	if err != nil || parsed.BatchID != issued.BatchID {
		t.Fatal("confidential receipt cannot be recovered locally")
	}
}

func TestMalformedResponsesAndSchemas(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"unknown schema", func(v map[string]any) { v["schema"] = "extore.card-batch.v2" }},
		{"wrong count", func(v map[string]any) { v["count"] = 1 }},
		{"fractional count", func(v map[string]any) { v["count"] = json.Number("2.0") }},
		{"repeated codes", func(v map[string]any) { v["codes"] = []string{"fixture-card-secret-0001", "fixture-card-secret-0001"} }},
		{"invalid code", func(v map[string]any) { v["codes"] = []string{"short", "fixture-card-secret-0002"} }},
		{"numeric price", func(v map[string]any) { v["variant"].(map[string]any)["price"] = 25 }},
		{"bad attribute", func(v map[string]any) {
			v["variant"].(map[string]any)["attributes"] = map[string]any{"__proto__": true}
		}},
		{"nested attribute", func(v map[string]any) {
			v["variant"].(map[string]any)["attributes"] = map[string]any{"nested": map[string]any{"x": true}}
		}},
		{"changed variant", func(v map[string]any) { v["variant"].(map[string]any)["id"] = "other" }},
		{"disabled variant", func(v map[string]any) { v["variant"].(map[string]any)["enabled"] = false }},
		{"bad quota", func(v map[string]any) { v["quota"].(map[string]any)["remaining"] = 99 }},
		{"unbounded recovery", func(v map[string]any) { v["recovery_expires"] = fixtureNow + 86401 }},
		{"nonfinite timestamp", func(v map[string]any) { v["recovery_expires"] = json.Number("1e9999") }},
		{"extreme negative exponent", func(v map[string]any) { v["created_at"] = json.Number("1e-1000001") }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			value := fixtureBatch()
			test.mutate(value)
			if _, err := ParseCardBatch(jsonBytes(t, value)); !errors.Is(err, ErrInvalidResponse) {
				t.Fatal("malformed batch accepted")
			}
		})
	}
	for _, raw := range []string{
		`{"schema":"x","schema":"y"}`, `{"x":{"value":1,"value":2}}`, `{"x":{"\u0076alue":1,"value":2}}`,
		`{"x":NaN}`, `{"x":Infinity}`, `{"x":1e-1000001}`, `{"x":1e9999}`, `{"x":1} {"y":2}`, `[]`,
		`{"x":` + strings.Repeat(`[`, 66) + `0` + strings.Repeat(`]`, 66) + `}`,
	} {
		if _, err := decodeJSON([]byte(raw)); !errors.Is(err, ErrInvalidResponse) {
			t.Fatal("unsafe JSON accepted")
		}
	}
	if _, err := decodeJSON([]byte{'{', '"', 'x', '"', ':', '"', 0xff, '"', '}'}); err == nil {
		t.Fatal("invalid UTF-8 accepted")
	}
	value := fixtureBatch()
	delete(value["variant"].(map[string]any), "price")
	parsed, err := ParseCardBatch(jsonBytes(t, value))
	if err != nil || parsed.Variant.Price != nil {
		t.Fatal("omitted reference price was not preserved as unknown")
	}
	value = fixtureBatch()
	value["variant"].(map[string]any)["price"] = nil
	parsed, err = ParseCardBatch(jsonBytes(t, value))
	if err != nil || parsed.Variant.Price != nil {
		t.Fatal("null reference price was not preserved as unknown")
	}
}

func TestDiscoveryRejectsEndpointAndCapabilityChanges(t *testing.T) {
	for _, mutate := range []func(map[string]any){
		func(v map[string]any) { v["issuer"] = "https://other.example" },
		func(v map[string]any) { v["token_endpoint"] = "https://other.example/token" },
		func(v map[string]any) { v["token_endpoint"] = fixtureOrigin + tokenPath + "?secret=leak" },
		func(v map[string]any) { v["token_endpoint"] = fixtureOrigin + "/other-token" },
		func(v map[string]any) { v["code_challenge_methods_supported"] = []string{"plain"} },
		func(v map[string]any) { v["authorization_response_iss_parameter_supported"] = false },
		func(v map[string]any) { v["extore_commerce"].(map[string]any)["schema"] = "unknown.v2" },
		func(v map[string]any) { v["extore_commerce"].(map[string]any)["maximum_cards_per_request"] = 101 },
	} {
		value := fixtureDiscovery()
		mutate(value)
		client, _ := tlsFixture(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { writeJSON(t, w, value) }))
		if _, err := client.Discover(context.Background(), fixtureOrigin); !errors.Is(err, ErrInvalidResponse) {
			t.Fatal("untrusted discovery metadata accepted")
		}
	}
}

func TestIssuanceRequestValidationBeforeNetwork(t *testing.T) {
	var calls atomic.Int32
	client, _ := tlsFixture(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); writeJSON(t, w, fixtureBatch()) }))
	base := `{"product_id":"document_service","variant_id":"standard","count":2}`
	for _, body := range []string{
		strings.Replace(base, `"count":2`, `"count":2.0`, 1), strings.Replace(base, `"count":2`, `"count":"2"`, 1),
		strings.Replace(base, `"count":2`, `"count":true`, 1), strings.Replace(base, `"count":2`, `"count":0`, 1),
		strings.TrimSuffix(base, "}") + `,"attributes":{}}`, strings.TrimSuffix(base, "}") + `,"COUNT":100}`,
		strings.TrimSuffix(base, "}") + `,"count":2}`, strings.TrimSuffix(base, "}") + `,"label":"bad\nlabel"}`,
		strings.TrimSuffix(base, "}") + `,"expected_revision":"bad"}`, strings.TrimSuffix(base, "}") + `,"expires":0}`,
	} {
		if _, err := client.IssueCardsJSON(context.Background(), fixtureMetadata(), fixtureAccess, "durable-key", []byte(body)); !errors.Is(err, ErrInvalidRequest) {
			t.Fatal("invalid issuance request accepted")
		}
	}
	if _, err := client.IssueCardsJSON(context.Background(), fixtureMetadata(), fixtureAccess, "short", []byte(base)); err == nil {
		t.Fatal("short idempotency key accepted")
	}
	metadata := fixtureMetadata()
	metadata.CardsEndpoint = "https://other.example/cards"
	if _, err := client.IssueCardsJSON(context.Background(), metadata, fixtureAccess, "durable-key", []byte(base)); err == nil {
		t.Fatal("corrupt persisted endpoint accepted")
	}
	if calls.Load() != 0 {
		t.Fatal("invalid local input reached issuer")
	}
}

func TestIssuanceHTTPStatusControlsRecoveryError(t *testing.T) {
	for _, test := range []struct {
		name        string
		status      int
		contentType string
		body        string
		want        string
	}{
		{"500 validation-looking body", 500, "application/json", `{"error":"invalid_request","detail":"PRIVATE_ERROR_DETAIL"}`, "server_error"},
		{"503 validation-looking body", 503, "application/json", `{"error":"quota_exceeded"}`, "server_error"},
		{"500 malformed body", 500, "text/plain", "invalid_request", "server_error"},
		{"429 validation-looking body", 429, "application/json", `{"error":"invalid_request"}`, "rate_limited"},
		{"429 malformed body", 429, "text/plain", "invalid_request", "rate_limited"},
		{"201 validation-looking body", 201, "application/json", `{"error":"invalid_request"}`, "invalid_response"},
		{"202 validation-looking body", 202, "application/json", `{"error":"quota_exceeded"}`, "invalid_response"},
		{"204 no response", 204, "application/json", "", "invalid_response"},
		{"600 unknown status", 600, "application/json", `{"error":"invalid_request"}`, "invalid_response"},
		{"400 known validation error", 400, "application/json", `{"error":"invalid_request"}`, "invalid_request"},
		{"401 known token error", 401, "application/json", `{"error":"invalid_token"}`, "invalid_token"},
		{"403 known permission error", 403, "application/json", `{"error":"insufficient_scope"}`, "insufficient_scope"},
		{"409 known revision error", 409, "application/json", `{"error":"catalog_changed"}`, "catalog_changed"},
		{"409 known quota error", 409, "application/json", `{"error":"quota_exceeded"}`, "quota_exceeded"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			client, _ := tlsFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodPost || r.URL.Path != cardsPath || r.Header.Get("Idempotency-Key") != "original-durable-key" || r.Header.Get("Authorization") != "Bearer "+fixtureAccess {
					t.Error("fixture did not receive the expected issuance request")
				}
				w.Header().Set("Content-Type", test.contentType)
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			_, err := client.IssueCards(context.Background(), fixtureMetadata(), fixtureAccess, "original-durable-key", CardRequest{ProductID: "document_service", VariantID: "standard", Count: 2})
			if err == nil || ErrorCode(err) != test.want {
				t.Fatalf("HTTP %d recovery error = %s; want %s", test.status, ErrorCode(err), test.want)
			}
			if calls.Load() != 1 {
				t.Fatal("issuance was retried implicitly")
			}
			if strings.Contains(err.Error(), "PRIVATE_ERROR_DETAIL") {
				t.Fatal("error detail was reflected")
			}
		})
	}
}

func TestBoundedResponses(t *testing.T) {
	for _, limit := range []int64{maximumTokenResponse, maximumCardResponse, maximumProductResponse} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			client, _ := tlsFixture(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.(http.Flusher).Flush() // no Content-Length: enforce the streaming limit
				_, _ = w.Write([]byte(`{}`))
				_, _ = io.CopyN(w, repeatingSpaceReader{}, limit)
			}))
			if _, _, err := client.response(context.Background(), http.MethodGet, fixtureOrigin+productsPath, "", "", "", nil, limit, false); !errors.Is(err, ErrInvalidResponse) {
				t.Fatal("oversized streamed response accepted")
			}
		})
	}
}

type repeatingSpaceReader struct{}

func (repeatingSpaceReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = ' '
	}
	return len(p), nil
}

func TestErrorsAndFmtDoNotReflectSecrets(t *testing.T) {
	const secretText = "NEVER_PRINT_FIXTURE_SECRET"
	for _, code := range []string{"invalid_grant", secretText} {
		client, _ := tlsFixture(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write(jsonBytes(t, map[string]any{"error": code, "error_description": secretText, "detail": secretText}))
		}))
		_, err := client.Refresh(context.Background(), fixtureMetadata(), "client-fixture", fixtureRefresh)
		if err == nil || strings.Contains(fmt.Sprintf("%v %+v %#v", err, err, err), secretText) || strings.Contains(ErrorCode(err), secretText) {
			t.Fatal("server error reflected secret text")
		}
	}
	tokens := Tokens{AccessToken: secretText, RefreshToken: secretText}
	batch := CardBatch{Codes: []string{secretText}, Raw: json.RawMessage(secretText)}
	for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x"} {
		if strings.Contains(fmt.Sprintf(format, tokens), secretText) || strings.Contains(fmt.Sprintf(format, &tokens), secretText) || strings.Contains(fmt.Sprintf(format, batch), secretText) || strings.Contains(fmt.Sprintf(format, &batch), secretText) {
			t.Fatal("fmt exposed confidential object")
		}
	}
	encoded, err := json.Marshal(batch)
	if err != nil || bytes.Contains(encoded, []byte(`"Raw"`)) || bytes.Contains(encoded, []byte(`"raw"`)) {
		t.Fatal("raw confidential receipt exported implicitly")
	}
}
