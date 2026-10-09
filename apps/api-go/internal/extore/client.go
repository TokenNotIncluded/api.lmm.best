// Package extore implements the read-only extore.commerce-import.v1 client.
// It never requests cards.issue, changes prices, or creates sales inventory.
package extore

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
)

const DefaultOrigin = "https://extore.lmm.best"
const catalogPath = "/api/integrations/commerce/products"
const tokenPath = "/api/integrations/commerce/token"
const revokePath = "/api/integrations/commerce/revoke"

var (
	ErrInput        = errors.New("invalid Extore connection or callback")
	ErrProtocol     = errors.New("unsupported or invalid Extore response")
	ErrUnavailable  = errors.New("Extore request failed; authorize again")
	ErrDenied       = errors.New("Extore authorization was denied")
	identityPattern = regexp.MustCompile(`^[^\x00-\x1f\x7f]{1,100}$`)
	revisionPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

// Flow is encrypted by the caller and bound to one authenticated user/session.
// Verifier is never sent to a browser or included in an authorization URL.
type Flow struct {
	Origin      string `json:"origin"`
	ClientID    string `json:"client_id"`
	RedirectURI string `json:"redirect_uri"`
	Verifier    string `json:"verifier"`
}

type Catalog struct {
	Schema string `json:"schema"`
	Issuer string `json:"issuer"`
	Shop   struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"shop"`
	GrantID  string            `json:"grant_id"`
	Products []json.RawMessage `json:"products"`
}

type Client struct{ HTTP *http.Client }

// NormalizeOrigin accepts an HTTPS origin, not an arbitrary API or media URL.
// Production dialing must additionally resolve and reject private addresses.
func NormalizeOrigin(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = DefaultOrigin
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(u.Scheme, "https") || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(raw, "#") || (u.Path != "" && u.Path != "/") || (u.Port() != "" && u.Port() != "443") {
		return "", ErrInput
	}
	if strings.HasSuffix(u.Host, ":") {
		return "", ErrInput
	}
	host := strings.ToLower(u.Hostname())
	if net.ParseIP(host) != nil || !strings.Contains(host, ".") || strings.HasSuffix(host, ".") {
		return "", ErrInput
	}
	for _, suffix := range []string{".localhost", ".local", ".internal", ".lan", ".home", ".onion"} {
		if strings.HasSuffix(host, suffix) {
			return "", ErrInput
		}
	}
	if len(host) > 253 {
		return "", ErrInput
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) < 1 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", ErrInput
		}
		for _, ch := range label {
			if !(ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '-') {
				return "", ErrInput
			}
		}
	}
	return "https://" + host, nil
}

func NewFlow(origin, clientID, redirectURI string) (Flow, error) {
	origin, err := NormalizeOrigin(origin)
	if err != nil || len(clientID) < 1 || len(clientID) > 200 || strings.ContainsAny(clientID, " \t\r\n") {
		return Flow{}, ErrInput
	}
	redirect, err := url.Parse(redirectURI)
	if err != nil || redirect.Path != "/store/manage" || redirect.RawQuery != "" || redirect.ForceQuery || redirect.Fragment != "" {
		return Flow{}, ErrInput
	}
	callbackOrigin, err := NormalizeOrigin(redirect.Scheme + "://" + redirect.Host)
	if err != nil || redirect.User != nil || callbackOrigin+"/store/manage" != redirectURI {
		return Flow{}, ErrInput
	}
	random := make([]byte, 32)
	if _, err = rand.Read(random); err != nil {
		return Flow{}, ErrUnavailable
	}
	return Flow{Origin: origin, ClientID: clientID, RedirectURI: redirectURI, Verifier: base64.RawURLEncoding.EncodeToString(random)}, nil
}

func (c Client) request(ctx context.Context, method, endpoint, token string, form url.Values, target any, limit int64) error {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil || c.HTTP == nil {
		return ErrInput
	}
	req.Header.Set("Accept", "application/json")
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	// Do not let a caller's default redirect policy disclose a code or token.
	httpClient := *c.HTTP
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := httpClient.Do(req)
	if err != nil {
		return ErrUnavailable
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil || int64(len(data)) > limit {
		return ErrProtocol
	}
	if response.StatusCode != http.StatusOK {
		return ErrUnavailable
	}
	if target == nil {
		return nil
	}
	if err := json.Unmarshal(data, target); err != nil {
		return ErrProtocol
	}
	return nil
}

func (c Client) Discover(ctx context.Context, origin string) error {
	normalized, err := NormalizeOrigin(origin)
	if err != nil || normalized != origin {
		return ErrInput
	}
	var metadata struct {
		Issuer        string   `json:"issuer"`
		Authorization string   `json:"authorization_endpoint"`
		Token         string   `json:"token_endpoint"`
		Revoke        string   `json:"revocation_endpoint"`
		PKCE          []string `json:"code_challenge_methods_supported"`
		Scopes        []string `json:"scopes_supported"`
		Responses     []string `json:"response_types_supported"`
		Grants        []string `json:"grant_types_supported"`
		AuthMethods   []string `json:"token_endpoint_auth_methods_supported"`
		IssParameter  bool     `json:"authorization_response_iss_parameter_supported"`
		Commerce      struct {
			Schema        string `json:"schema"`
			ListingSchema string `json:"listing_schema"`
			Products      string `json:"products_endpoint"`
			Cards         string `json:"cards_endpoint"`
		} `json:"extore_commerce"`
	}
	if err := c.request(ctx, http.MethodGet, origin+"/.well-known/oauth-authorization-server", "", nil, &metadata, 64<<10); err != nil {
		return err
	}
	if metadata.Issuer != origin || metadata.Authorization != origin+"/oauth/authorize" || metadata.Token != origin+tokenPath || metadata.Revoke != origin+revokePath || metadata.Commerce.Products != origin+catalogPath || metadata.Commerce.Cards != origin+"/api/integrations/commerce/cards" || metadata.Commerce.Schema != "extore.commerce-import.v1" || metadata.Commerce.ListingSchema != "extore.product-listing.v1" || !metadata.IssParameter || !slices.Contains(metadata.PKCE, "S256") || !slices.Contains(metadata.Scopes, "products.read") || !slices.Contains(metadata.Responses, "code") || !slices.Contains(metadata.Grants, "authorization_code") || !slices.Contains(metadata.AuthMethods, "none") {
		return ErrProtocol
	}
	return nil
}

func (flow Flow) AuthorizationURL(state string) string {
	digest := sha256.Sum256([]byte(flow.Verifier))
	values := url.Values{"response_type": {"code"}, "client_id": {flow.ClientID}, "redirect_uri": {flow.RedirectURI}, "scope": {"products.read"}, "state": {state}, "code_challenge": {base64.RawURLEncoding.EncodeToString(digest[:])}, "code_challenge_method": {"S256"}}
	return flow.Origin + "/oauth/authorize?" + values.Encode()
}

// ParseCallback checks duplicates before looking up server-side state.
func ParseCallback(raw string) (*url.URL, url.Values, error) {
	if len(raw) > 8192 {
		return nil, nil, ErrInput
	}
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || strings.Contains(raw, "#") || u.Fragment != "" || u.Path != "/store/manage" {
		return nil, nil, ErrInput
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return nil, nil, ErrInput
	}
	for key, values := range query {
		if len(values) != 1 || !slices.Contains([]string{"code", "state", "iss", "error", "error_description"}, key) {
			return nil, nil, ErrInput
		}
	}
	if len(query.Get("state")) < 16 || len(query.Get("state")) > 512 || query.Get("iss") == "" {
		return nil, nil, ErrInput
	}
	if (query.Get("code") == "") == (query.Get("error") == "") {
		return nil, nil, ErrInput
	}
	return u, query, nil
}

func (flow Flow) ValidateCallback(raw string) (string, error) {
	u, query, err := ParseCallback(raw)
	if err != nil {
		return "", err
	}
	origin, err := NormalizeOrigin(u.Scheme + "://" + u.Host)
	if err != nil || origin+u.EscapedPath() != flow.RedirectURI || query.Get("iss") != flow.Origin {
		return "", ErrInput
	}
	if query.Get("error") != "" {
		return "", ErrDenied
	}
	code := query.Get("code")
	if len(code) > 1024 || strings.ContainsAny(code, "\r\n\x00") {
		return "", ErrInput
	}
	return code, nil
}

func (c Client) ReadCatalog(ctx context.Context, flow Flow, code string) (*Catalog, error) {
	var token struct {
		Access  string `json:"access_token"`
		Refresh string `json:"refresh_token"`
		Type    string `json:"token_type"`
		Scope   string `json:"scope"`
		Expires int    `json:"expires_in"`
		GrantID string `json:"grant_id"`
	}
	form := url.Values{"grant_type": {"authorization_code"}, "client_id": {flow.ClientID}, "code": {code}, "redirect_uri": {flow.RedirectURI}, "code_verifier": {flow.Verifier}}
	if err := c.request(ctx, http.MethodPost, flow.Origin+tokenPath, "", form, &token, 64<<10); err != nil {
		return nil, err
	}
	// This is a one-shot import, not a stored connection. Even failed reads revoke
	// the grant. No refresh tokens or credentials are returned to the browser.
	if token.Refresh != "" {
		defer func() {
			revokeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
			defer cancel()
			_ = c.request(revokeCtx, http.MethodPost, flow.Origin+revokePath, "", url.Values{"token": {token.Refresh}, "client_id": {flow.ClientID}, "token_type_hint": {"refresh_token"}}, nil, 64<<10)
		}()
	}
	if token.Access == "" || token.Refresh == "" || !strings.EqualFold(token.Type, "Bearer") || token.Scope != "products.read" || token.Expires < 1 || token.Expires > 900 || token.GrantID == "" {
		return nil, ErrProtocol
	}
	var catalog Catalog
	if err := c.request(ctx, http.MethodGet, flow.Origin+catalogPath, token.Access, nil, &catalog, 16<<20); err != nil {
		return nil, err
	}
	if err := ValidateCatalog(&catalog, flow.Origin, token.GrantID); err != nil {
		return nil, err
	}
	return &catalog, nil
}

func ValidateCatalog(catalog *Catalog, origin, grantID string) error {
	if catalog.Schema != "extore.commerce-catalog.v1" || catalog.Issuer != origin || catalog.GrantID != grantID || catalog.Shop.ID == "" || catalog.Products == nil || len(catalog.Products) > 100 {
		return ErrProtocol
	}
	seen := make(map[string]bool)
	for _, raw := range catalog.Products {
		var listing struct {
			Schema        string `json:"schema"`
			ID            string `json:"id"`
			ShopID        string `json:"shop_id"`
			Revision      string `json:"revision"`
			RedemptionURL string `json:"redemption_url"`
			Semantics     struct {
				Price      string `json:"price"`
				Inventory  string `json:"inventory"`
				Payment    string `json:"payment"`
				Redemption string `json:"redemption"`
			} `json:"semantics"`
			Product  json.RawMessage   `json:"product"`
			Variants []json.RawMessage `json:"variants"`
		}
		if json.Unmarshal(raw, &listing) != nil || listing.Schema != "extore.product-listing.v1" || !identityPattern.MatchString(listing.ID) || listing.ShopID != catalog.Shop.ID || !revisionPattern.MatchString(listing.Revision) || seen[listing.ID] || len(listing.Product) < 2 || listing.Product[0] != '{' || listing.Variants == nil || len(listing.Variants) > 100 {
			return ErrProtocol
		}
		seen[listing.ID] = true
		if listing.Semantics.Price != "reference" || listing.Semantics.Inventory != "not_exported" || listing.Semantics.Payment != "external_sales_platform" || listing.Semantics.Redemption != "extore" {
			return ErrProtocol
		}
		u, err := url.Parse(listing.RedemptionURL)
		if err != nil || u.User != nil || u.Scheme+"://"+u.Host != origin || u.Fragment != "" {
			return ErrProtocol
		}
	}
	return nil
}
