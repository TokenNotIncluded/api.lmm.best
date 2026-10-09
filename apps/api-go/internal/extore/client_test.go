package extore

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (fn roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return fn(r) }
func reply(value any) *http.Response {
	raw, _ := json.Marshal(value)
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(raw)))}
}
func fixture(t *testing.T) *Catalog {
	t.Helper()
	var c Catalog
	raw := `{"schema":"extore.commerce-catalog.v1","issuer":"https://extore.example","shop":{"id":"shop","name":"Shop"},"grant_id":"grant","products":[{"schema":"extore.product-listing.v1","id":"document","shop_id":"shop","revision":"` + strings.Repeat("a", 64) + `","redemption_url":"https://extore.example/","semantics":{"price":"reference","inventory":"not_exported","payment":"external_sales_platform","redemption":"extore"},"product":{"name":"Document","description":"Details","public":true},"variants":[{"id":"basic","name":"Basic","price":"25.00","currency":"CNY","enabled":true,"attributes":{}}]}]}`
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatal(err)
	}
	return &c
}
func metadata() map[string]any {
	origin := "https://extore.example"
	return map[string]any{"issuer": origin, "authorization_endpoint": origin + "/oauth/authorize", "token_endpoint": origin + tokenPath, "revocation_endpoint": origin + revokePath, "response_types_supported": []string{"code"}, "grant_types_supported": []string{"authorization_code"}, "code_challenge_methods_supported": []string{"S256"}, "scopes_supported": []string{"products.read", "cards.issue"}, "token_endpoint_auth_methods_supported": []string{"none"}, "authorization_response_iss_parameter_supported": true, "extore_commerce": map[string]any{"schema": "extore.commerce-import.v1", "listing_schema": "extore.product-listing.v1", "products_endpoint": origin + catalogPath, "cards_endpoint": origin + "/api/integrations/commerce/cards"}}
}
func TestNormalizeOrigin(t *testing.T) {
	for input, want := range map[string]string{"": DefaultOrigin, "extore.lmm.best": DefaultOrigin, " HTTPS://EXTORE.LMM.BEST:443/ ": DefaultOrigin, "https://custom.example/": "https://custom.example"} {
		got, err := NormalizeOrigin(input)
		if err != nil || got != want {
			t.Errorf("%q: %q %v", input, got, err)
		}
	}
	for _, input := range []string{"http://extore.example", "https://127.0.0.1", "https://[::1]", "https://localhost", "https://server.local", "https://server.internal", "https://extore.example:8443", "https://extore.example:/", "https://extore.example/api", "https://user:password@extore.example", "https://extore.example?", "https://extore.example#", "https://extore.example./", "https://bad..example", "https://-bad.example", "https://é.example"} {
		if _, err := NormalizeOrigin(input); !errors.Is(err, ErrInput) {
			t.Errorf("accepted %q", input)
		}
	}
}
func TestFlowAndCallback(t *testing.T) {
	flow, err := NewFlow("https://extore.example", "client", "https://shop.example/store/manage")
	if err != nil {
		t.Fatal(err)
	}
	other, _ := NewFlow(flow.Origin, flow.ClientID, flow.RedirectURI)
	if len(flow.Verifier) != 43 || flow.Verifier == other.Verifier {
		t.Fatal("verifier must be random per flow")
	}
	auth, _ := url.Parse(flow.AuthorizationURL(strings.Repeat("s", 32)))
	q := auth.Query()
	digest := sha256.Sum256([]byte(flow.Verifier))
	if q.Get("scope") != "products.read" || q.Get("code_challenge") != base64.RawURLEncoding.EncodeToString(digest[:]) || q.Get("code_challenge_method") != "S256" || strings.Contains(auth.String(), flow.Verifier) {
		t.Fatal("invalid read-only PKCE authorization")
	}
	callback := flow.RedirectURI + "?code=one-time&state=" + strings.Repeat("s", 32) + "&iss=" + url.QueryEscape(flow.Origin)
	if code, err := flow.ValidateCallback(callback); err != nil || code != "one-time" {
		t.Fatal(code, err)
	}
	for _, bad := range []string{callback + "&state=duplicate", callback + "&iss=duplicate", callback + "&code=duplicate", callback + "&unknown=1", callback + "#", strings.Replace(callback, "shop.example", "attacker.example", 1), strings.Replace(callback, "/store/manage", "/store/%6danage", 1), strings.Replace(callback, "extore.example", "attacker.example", 1), strings.Replace(callback, "code=one-time", "code=%0d%0asecret", 1), strings.Replace(callback, "code=one-time", "code=x&error=access_denied", 1)} {
		if _, err := flow.ValidateCallback(bad); !errors.Is(err, ErrInput) {
			t.Errorf("accepted callback %q: %v", bad, err)
		}
	}
	denied := strings.Replace(callback, "code=one-time", "error=access_denied", 1)
	if _, err := flow.ValidateCallback(denied); !errors.Is(err, ErrDenied) {
		t.Fatal(err)
	}
}
func TestDiscoverPinsEndpointsAndRejectsRedirects(t *testing.T) {
	for _, change := range []string{"", "issuer", "token_endpoint", "authorization_endpoint", "revocation_endpoint", "code_challenge_methods_supported"} {
		m := metadata()
		if change != "" {
			if change == "code_challenge_methods_supported" {
				m[change] = []string{"plain"}
			} else {
				m[change] = "https://attacker.example"
			}
		}
		c := Client{HTTP: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
			if r.URL.String() != "https://extore.example/.well-known/oauth-authorization-server" || r.Header.Get("Authorization") != "" {
				t.Fatal("unexpected discovery request")
			}
			return reply(m), nil
		})}}
		err := c.Discover(context.Background(), "https://extore.example")
		if change == "" && err != nil || change != "" && !errors.Is(err, ErrProtocol) {
			t.Errorf("%s: %v", change, err)
		}
	}
	requests := 0
	c := Client{HTTP: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		requests++
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{"https://attacker.example"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}}
	if c.Discover(context.Background(), "https://extore.example") == nil || requests != 1 {
		t.Fatal("followed discovery redirect")
	}
}
func TestReadCatalogIsOneShotAndRevokes(t *testing.T) {
	for _, failCatalog := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[failCatalog], func(t *testing.T) {
			flow, _ := NewFlow("https://extore.example", "client", "https://shop.example/store/manage")
			calls := []string{}
			c := Client{HTTP: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
				calls = append(calls, r.URL.Path)
				if r.URL.Host != "extore.example" {
					t.Fatal("cross-origin credential request")
				}
				switch r.URL.Path {
				case tokenPath:
					_ = r.ParseForm()
					if r.Method != "POST" || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" || r.Form.Get("code_verifier") != flow.Verifier || r.Form.Get("code") != "once" || r.Form.Get("redirect_uri") != flow.RedirectURI {
						t.Fatal("wrong token exchange")
					}
					return reply(map[string]any{"access_token": "access-secret", "refresh_token": "refresh-secret", "token_type": "Bearer", "scope": "products.read", "expires_in": 900, "grant_id": "grant"}), nil
				case catalogPath:
					if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer access-secret" || r.URL.RawQuery != "" {
						t.Fatal("wrong catalog request")
					}
					if failCatalog {
						return nil, errors.New("private upstream failure secret")
					}
					return reply(fixture(t)), nil
				case revokePath:
					_ = r.ParseForm()
					if r.Form.Get("token") != "refresh-secret" || r.Form.Get("client_id") != "client" {
						t.Fatal("missing revoke")
					}
					return reply(nil), nil
				default:
					t.Fatal("must not issue cards or refresh tokens")
					return nil, nil
				}
			})}}
			catalog, err := c.ReadCatalog(context.Background(), flow, "once")
			if failCatalog {
				if !errors.Is(err, ErrUnavailable) || catalog != nil {
					t.Fatal("expected safe failure", err)
				}
			} else {
				if err != nil || catalog == nil {
					t.Fatal(err)
				}
				raw, _ := json.Marshal(catalog)
				if strings.Contains(string(raw), "secret") {
					t.Fatal("token exposed")
				}
			}
			if strings.Join(calls, ",") != strings.Join([]string{tokenPath, catalogPath, revokePath}, ",") {
				t.Fatal(calls)
			}
		})
	}
}
func TestCatalogBoundaries(t *testing.T) {
	for _, mutation := range []string{"issuer", "shop", "revision", "inventory", "duplicate", "redemption"} {
		c := fixture(t)
		switch mutation {
		case "issuer":
			c.Issuer = "https://other.example"
		case "shop":
			c.Shop.ID = "other"
		case "duplicate":
			c.Products = append(c.Products, c.Products[0])
		default:
			raw := string(c.Products[0])
			switch mutation {
			case "revision":
				raw = strings.Replace(raw, strings.Repeat("a", 64), "bad", 1)
			case "inventory":
				raw = strings.Replace(raw, "not_exported", "remaining", 1)
			case "redemption":
				raw = strings.Replace(raw, "https://extore.example/", "https://other.example/", 1)
			}
			c.Products[0] = json.RawMessage(raw)
		}
		if !errors.Is(ValidateCatalog(c, "https://extore.example", "grant"), ErrProtocol) {
			t.Errorf("accepted %s", mutation)
		}
	}
	if err := ValidateCatalog(fixture(t), "https://extore.example", "grant"); err != nil {
		t.Fatal(err)
	}
}
func TestMachineResponsesAreBounded(t *testing.T) {
	c := Client{HTTP: &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(strings.Repeat("x", 65<<10)))}, nil
	})}}
	if !errors.Is(c.Discover(context.Background(), "https://extore.example"), ErrProtocol) {
		t.Fatal("unbounded response")
	}
}

func TestCatalogAllowsEmptyVariantsButRejectsMissingAndOversizedVariants(t *testing.T) {
	for _, count := range []int{0, 100, 101} {
		c := fixture(t)
		var listing map[string]json.RawMessage
		if err := json.Unmarshal(c.Products[0], &listing); err != nil {
			t.Fatal(err)
		}
		variants := make([]map[string]any, count)
		for i := range variants {
			variants[i] = map[string]any{"price": nil, "attributes": map[string]any{}}
		}
		listing["variants"], _ = json.Marshal(variants)
		c.Products[0], _ = json.Marshal(listing)
		err := ValidateCatalog(c, c.Issuer, c.GrantID)
		if (err == nil) != (count <= 100) {
			t.Fatalf("count %d: %v", count, err)
		}
		delete(listing, "variants")
		c.Products[0], _ = json.Marshal(listing)
		if !errors.Is(ValidateCatalog(c, c.Issuer, c.GrantID), ErrProtocol) {
			t.Fatal("accepted missing variants")
		}
	}
}
