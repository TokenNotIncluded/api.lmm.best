package commerceimport

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

const (
	discoveryPath = "/.well-known/oauth-authorization-server"
	authorizePath = "/oauth/authorize"
	tokenPath     = "/api/integrations/commerce/token"
	revokePath    = "/api/integrations/commerce/revoke"
	productsPath  = "/api/integrations/commerce/products"
	cardsPath     = "/api/integrations/commerce/cards"
)

// ValidateOrigin performs syntax checks without making a network request. Each
// network dial separately checks every DNS answer and pins the selected IP.
func ValidateOrigin(origin string) error {
	u, err := publicHTTPSURL(origin)
	if err != nil || u.Path != "" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery {
		return ErrInvalidOrigin
	}
	return nil
}

func publicHTTPSURL(raw string) (*url.URL, error) {
	if len(raw) == 0 || len(raw) > 2048 || !ascii(raw) || strings.ContainsAny(raw, "\\#") {
		return nil, ErrInvalidOrigin
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Opaque != "" || u.User != nil || u.Hostname() == "" || u.Fragment != "" {
		return nil, ErrInvalidOrigin
	}
	host := strings.ToLower(u.Hostname())
	if _, err := netip.ParseAddr(host); err == nil {
		return nil, ErrInvalidOrigin
	}
	if !publicHostname(host) {
		return nil, ErrInvalidOrigin
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port {
			return nil, ErrInvalidOrigin
		}
	} else if strings.HasSuffix(u.Host, ":") {
		return nil, ErrInvalidOrigin
	}
	return u, nil
}

func publicHostname(host string) bool {
	if len(host) > 253 || !strings.Contains(host, ".") || strings.HasSuffix(host, ".") {
		return false
	}
	for _, suffix := range []string{".localhost", ".local", ".internal", ".lan", ".home", ".onion", ".invalid"} {
		if strings.HasSuffix(host, suffix) {
			return false
		}
	}
	labels := strings.Split(host, ".")
	for _, label := range labels {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, ch := range label {
			if !((ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '-') {
				return false
			}
		}
	}
	// A numeric final label can be interpreted as an alternate IPv4 spelling.
	for _, ch := range labels[len(labels)-1] {
		if ch < '0' || ch > '9' {
			return true
		}
	}
	return false
}

func ascii(s string) bool {
	for _, ch := range s {
		if ch < 0x21 || ch > 0x7e {
			return false
		}
	}
	return true
}

func validateMetadata(m Metadata) error {
	if ValidateOrigin(m.Issuer) != nil || m.AuthorizationEndpoint != m.Issuer+authorizePath ||
		m.TokenEndpoint != m.Issuer+tokenPath || m.RevocationEndpoint != m.Issuer+revokePath ||
		m.ProductsEndpoint != m.Issuer+productsPath || m.CardsEndpoint != m.Issuer+cardsPath ||
		m.MaximumCardsPerRequest < 1 || m.MaximumCardsPerRequest > 100 {
		return ErrInvalidResponse
	}
	return nil
}

func validVerifier(s string) bool {
	if len(s) < 43 || len(s) > 128 {
		return false
	}
	for _, ch := range s {
		if !((ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || strings.ContainsRune("-._~", ch)) {
			return false
		}
	}
	return true
}

func validState(s string) bool { return len(s) >= 16 && len(s) <= 512 && ascii(s) }

func scopesValue(scopes []string) (string, error) {
	if len(scopes) < 1 || len(scopes) > 2 {
		return "", ErrInvalidRequest
	}
	seen := map[string]bool{}
	for _, scope := range scopes {
		if (scope != "products.read" && scope != "cards.issue") || seen[scope] {
			return "", ErrInvalidRequest
		}
		seen[scope] = true
	}
	// The protocol response has a canonical order; requests use it as well.
	if seen["products.read"] && seen["cards.issue"] {
		return "products.read cards.issue", nil
	}
	return scopes[0], nil
}

var callbackKeys = map[string]bool{"code": true, "state": true, "iss": true, "error": true, "error_description": true}

func queryValues(u *url.URL) (url.Values, error) {
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return nil, ErrInvalidCallback
	}
	for _, values := range q {
		if len(values) != 1 {
			return nil, ErrInvalidCallback
		}
	}
	return q, nil
}

func registeredCallback(raw string) (*url.URL, url.Values, error) {
	u, err := publicHTTPSURL(raw)
	if err != nil {
		return nil, nil, ErrInvalidCallback
	}
	q, err := queryValues(u)
	if err != nil {
		return nil, nil, err
	}
	for key := range q {
		if callbackKeys[key] {
			return nil, nil, ErrInvalidCallback
		}
	}
	return u, q, nil
}

func AuthorizationURL(metadata Metadata, clientID, redirectURI, state, verifier string, scopes []string) (string, error) {
	if validateMetadata(metadata) != nil || !identity(clientID) || !validState(state) || !validVerifier(verifier) {
		return "", ErrInvalidRequest
	}
	if _, _, err := registeredCallback(redirectURI); err != nil {
		return "", err
	}
	scope, err := scopesValue(scopes)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(verifier))
	q := url.Values{"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {redirectURI}, "scope": {scope}, "state": {state},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(digest[:])}, "code_challenge_method": {"S256"}}
	return metadata.AuthorizationEndpoint + "?" + q.Encode(), nil
}

func callbackPort(u *url.URL) string {
	if u.Port() == "" {
		return "443"
	}
	return u.Port()
}
func callbackPath(u *url.URL) string {
	if u.EscapedPath() == "" {
		return "/"
	}
	return u.EscapedPath()
}

// ValidateCallback accepts browser normalization of hostname case, default port,
// and an empty root path only. It verifies the entire target and transaction
// before considering either an authorization code or a denial.
func ValidateCallback(registered, actual, state, issuer string) (string, error) {
	if !validState(state) || ValidateOrigin(issuer) != nil {
		return "", ErrInvalidCallback
	}
	r, rq, err := registeredCallback(registered)
	if err != nil {
		return "", err
	}
	a, err := publicHTTPSURL(actual)
	if err != nil || !strings.EqualFold(r.Hostname(), a.Hostname()) || callbackPort(r) != callbackPort(a) || callbackPath(r) != callbackPath(a) {
		return "", ErrInvalidCallback
	}
	aq, err := queryValues(a)
	if err != nil {
		return "", err
	}
	if subtle.ConstantTimeCompare([]byte(aq.Get("state")), []byte(state)) != 1 || aq.Get("iss") != issuer {
		return "", ErrInvalidCallback
	}
	for key, values := range aq {
		if callbackKeys[key] {
			continue
		}
		if expected, ok := rq[key]; !ok || values[0] != expected[0] {
			return "", ErrInvalidCallback
		}
	}
	for key, values := range rq {
		if actual, ok := aq[key]; !ok || actual[0] != values[0] {
			return "", ErrInvalidCallback
		}
	}
	code, hasCode := aq["code"]
	denial, hasError := aq["error"]
	if hasCode == hasError {
		return "", ErrInvalidCallback
	}
	if hasError {
		if denial[0] == "access_denied" {
			return "", ErrAuthorizationDenied
		}
		return "", ErrInvalidCallback
	}
	if _, exists := aq["error_description"]; exists || !secret(code[0], 1) {
		return "", ErrInvalidCallback
	}
	return code[0], nil
}
