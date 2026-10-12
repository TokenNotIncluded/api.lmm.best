package extore

import (
	"context"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

type Credentials struct {
	Access        string  `json:"access_token"`
	Refresh       string  `json:"refresh_token"`
	Type          string  `json:"token_type"`
	Scope         string  `json:"scope"`
	Expires       int     `json:"expires_in"`
	GrantID       string  `json:"grant_id"`
	GrantExpires  float64 `json:"grant_expires"`
	AccessExpires int64   `json:"access_expires"`
}

func (c Client) credentials(ctx context.Context, origin string, form url.Values) (*Credentials, error) {
	var result Credentials
	if err := c.request(ctx, http.MethodPost, origin+tokenPath, "", form, &result, 64<<10); err != nil {
		return nil, err
	}
	scopes := strings.Fields(result.Scope)
	if result.Access == "" || result.Refresh == "" || !strings.EqualFold(result.Type, "Bearer") || !slices.Contains(scopes, "products.read") || !slices.Contains(scopes, "marketplace.sync") || result.GrantID == "" || result.Expires < 1 || result.Expires > 900 || result.GrantExpires <= float64(time.Now().Unix()) {
		return nil, ErrProtocol
	}
	// Catalogue synchronisation must never acquire issuance authority implicitly.
	for _, scope := range scopes {
		if scope != "products.read" && scope != "marketplace.sync" {
			return nil, ErrProtocol
		}
	}
	result.AccessExpires = time.Now().Unix() + int64(result.Expires)
	return &result, nil
}
func (c Client) Connect(ctx context.Context, flow Flow, code string) (*Credentials, error) {
	return c.credentials(ctx, flow.Origin, url.Values{"grant_type": {"authorization_code"}, "client_id": {flow.ClientID}, "code": {code}, "redirect_uri": {flow.RedirectURI}, "code_verifier": {flow.Verifier}})
}
func (c Client) RefreshConnection(ctx context.Context, origin, clientID string, old Credentials) (*Credentials, error) {
	next, err := c.credentials(ctx, origin, url.Values{"grant_type": {"refresh_token"}, "client_id": {clientID}, "refresh_token": {old.Refresh}})
	if err != nil {
		return nil, err
	}
	if next.GrantID != old.GrantID {
		return nil, ErrProtocol
	}
	return next, nil
}
func (c Client) ConnectionCatalog(ctx context.Context, origin string, credentials Credentials) (*Catalog, error) {
	var catalog Catalog
	if err := c.request(ctx, http.MethodGet, origin+catalogPath, credentials.Access, nil, &catalog, 16<<20); err != nil {
		return nil, err
	}
	if err := ValidateCatalog(&catalog, origin, credentials.GrantID); err != nil {
		return nil, err
	}
	return &catalog, nil
}
func (flow Flow) SyncAuthorizationURL(state string) string {
	parsed, _ := url.Parse(flow.AuthorizationURL(state))
	query := parsed.Query()
	query.Set("scope", "products.read marketplace.sync")
	parsed.RawQuery = query.Encode()
	return parsed.String()
}
