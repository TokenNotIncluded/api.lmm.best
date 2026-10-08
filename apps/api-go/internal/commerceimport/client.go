package commerceimport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
)

const (
	maximumTokenResponse   = 64 << 10
	maximumProductResponse = 16 << 20
	maximumCardResponse    = 2 << 20
)

func Discover(ctx context.Context, origin string) (Metadata, error) {
	return defaultClient.Discover(ctx, origin)
}
func Exchange(ctx context.Context, metadata Metadata, clientID, redirectURI, code, verifier string) (Tokens, error) {
	return defaultClient.Exchange(ctx, metadata, clientID, redirectURI, code, verifier)
}
func Refresh(ctx context.Context, metadata Metadata, clientID, refreshToken string) (Tokens, error) {
	return defaultClient.Refresh(ctx, metadata, clientID, refreshToken)
}
func Revoke(ctx context.Context, metadata Metadata, clientID, refreshToken string) error {
	return defaultClient.Revoke(ctx, metadata, clientID, refreshToken)
}
func FetchCatalog(ctx context.Context, metadata Metadata, accessToken string) (Catalog, error) {
	return defaultClient.FetchCatalog(ctx, metadata, accessToken)
}
func FetchListing(ctx context.Context, metadata Metadata, accessToken, productID string) (Listing, error) {
	return defaultClient.FetchListing(ctx, metadata, accessToken, productID)
}
func IssueCards(ctx context.Context, metadata Metadata, accessToken, idempotencyKey string, request CardRequest) (CardBatch, error) {
	return defaultClient.IssueCards(ctx, metadata, accessToken, idempotencyKey, request)
}

func IssueCardsJSON(ctx context.Context, metadata Metadata, accessToken, idempotencyKey string, body []byte) (CardBatch, error) {
	return defaultClient.IssueCardsJSON(ctx, metadata, accessToken, idempotencyKey, body)
}

func (c *Client) response(ctx context.Context, method, endpoint, contentType, accessToken, idempotencyKey string, body []byte, limit int64, empty bool) ([]byte, map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, nil, ErrInvalidRequest
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Cache-Control", "no-store")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	// A non-nil GetBody enables net/http transport's opportunistic POST retry
	// when it sees an idempotency header. Protocol retries belong to the durable
	// caller; even issuance must never be retried implicitly here.
	req.GetBody = nil
	resp, err := c.http.Do(req)
	if err != nil {
		if errors.Is(err, ErrInvalidOrigin) {
			return nil, nil, ErrInvalidOrigin
		}
		return nil, nil, ErrNetwork
	}
	defer resp.Body.Close()
	// Status is authoritative for ambiguous issuance. A 5xx or rate-limit
	// response cannot be downgraded by a body that resembles a definitive
	// validation rejection; callers must keep the original issuance uncertain.
	if resp.StatusCode >= 500 && resp.StatusCode <= 599 {
		return nil, nil, &ProtocolError{Code: "server_error", StatusCode: resp.StatusCode}
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, nil, &ProtocolError{Code: "rate_limited", StatusCode: resp.StatusCode}
	}
	if resp.StatusCode != http.StatusOK && (resp.StatusCode < 400 || resp.StatusCode >= 500) {
		return nil, nil, ErrInvalidResponse
	}
	if resp.ContentLength > limit {
		return nil, nil, ErrInvalidResponse
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, nil, ErrNetwork
	}
	if int64(len(raw)) > limit {
		return nil, nil, ErrInvalidResponse
	}
	if empty && resp.StatusCode == http.StatusOK {
		if len(raw) != 0 {
			return nil, nil, ErrInvalidResponse
		}
		return raw, nil, nil
	}
	mediaType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return nil, nil, ErrInvalidResponse
	}
	object, err := decodeJSON(raw)
	if err != nil {
		return nil, nil, err
	}
	if resp.StatusCode != http.StatusOK {
		code, _ := object["error"].(string)
		if safeErrorCode(code) == "commerce_error" {
			code = statusErrorCode(resp.StatusCode)
		}
		return nil, nil, &ProtocolError{Code: code, StatusCode: resp.StatusCode}
	}
	return raw, object, nil
}

func statusErrorCode(status int) string {
	switch status {
	case 400:
		return "invalid_request"
	case 401:
		return "invalid_token"
	case 403:
		return "access_denied"
	case 429:
		return "rate_limited"
	default:
		if status >= 500 && status <= 599 {
			return "server_error"
		}
		return "invalid_response"
	}
}

func (c *Client) Discover(ctx context.Context, origin string) (Metadata, error) {
	var result Metadata
	if err := ValidateOrigin(origin); err != nil {
		return result, err
	}
	_, object, err := c.response(ctx, http.MethodGet, origin+discoveryPath, "", "", "", nil, maximumTokenResponse, false)
	if err != nil {
		return result, err
	}
	var document struct {
		Issuer                string   `json:"issuer"`
		AuthorizationEndpoint string   `json:"authorization_endpoint"`
		TokenEndpoint         string   `json:"token_endpoint"`
		RevocationEndpoint    string   `json:"revocation_endpoint"`
		ResponseTypes         []string `json:"response_types_supported"`
		GrantTypes            []string `json:"grant_types_supported"`
		TokenAuthMethods      []string `json:"token_endpoint_auth_methods_supported"`
		ChallengeMethods      []string `json:"code_challenge_methods_supported"`
		Scopes                []string `json:"scopes_supported"`
		ResponseIssuer        bool     `json:"authorization_response_iss_parameter_supported"`
		Commerce              struct {
			Schema           string `json:"schema"`
			SchemaURI        string `json:"schema_uri"`
			ProductsEndpoint string `json:"products_endpoint"`
			CardsEndpoint    string `json:"cards_endpoint"`
			ListingSchema    string `json:"listing_schema"`
			MaximumProducts  int    `json:"maximum_products"`
			MaximumCards     int    `json:"maximum_cards_per_request"`
			RecoverySeconds  int    `json:"issuance_recovery_seconds"`
		} `json:"extore_commerce"`
	}
	if decodeFields(object, &document) != nil {
		return result, ErrInvalidResponse
	}
	if document.Issuer != origin || !document.ResponseIssuer || document.Commerce.Schema != ProtocolSchema ||
		document.Commerce.ListingSchema != ListingSchema || document.Commerce.SchemaURI != origin+"/api/integrations/commerce/schema" ||
		document.Commerce.MaximumProducts < 1 || document.Commerce.MaximumProducts > 100 ||
		document.Commerce.RecoverySeconds < 1 || document.Commerce.RecoverySeconds > 86400 ||
		!supported(document.ResponseTypes, "code") || !supported(document.GrantTypes, "authorization_code", "refresh_token") ||
		!supported(document.TokenAuthMethods, "none") || !supported(document.ChallengeMethods, "S256") ||
		!supported(document.Scopes, "products.read", "cards.issue") {
		return result, ErrInvalidResponse
	}
	result = Metadata{Issuer: document.Issuer, AuthorizationEndpoint: document.AuthorizationEndpoint, TokenEndpoint: document.TokenEndpoint,
		RevocationEndpoint: document.RevocationEndpoint, ProductsEndpoint: document.Commerce.ProductsEndpoint,
		CardsEndpoint: document.Commerce.CardsEndpoint, MaximumCardsPerRequest: document.Commerce.MaximumCards}
	if err := validateMetadata(result); err != nil {
		return Metadata{}, err
	}
	return result, nil
}

func supported(values []string, required ...string) bool {
	if len(values) == 0 || len(values) > 32 {
		return false
	}
	seen := map[string]bool{}
	for _, value := range values {
		if seen[value] || len(value) > 100 {
			return false
		}
		seen[value] = true
	}
	for _, value := range required {
		if !seen[value] {
			return false
		}
	}
	return true
}

func (c *Client) Exchange(ctx context.Context, metadata Metadata, clientID, redirectURI, code, verifier string) (Tokens, error) {
	if validateMetadata(metadata) != nil || !identity(clientID) || !secret(code, 1) || !validVerifier(verifier) {
		return Tokens{}, ErrInvalidRequest
	}
	if _, _, err := registeredCallback(redirectURI); err != nil {
		return Tokens{}, err
	}
	form := url.Values{"grant_type": {"authorization_code"}, "client_id": {clientID}, "code": {code}, "redirect_uri": {redirectURI}, "code_verifier": {verifier}}
	return c.tokens(ctx, metadata, form)
}

// Refresh performs exactly one attempt. The caller must hold a connection-level
// database lock and atomically replace both tokens; ambiguous failure requires
// reauthorization rather than replay of the same refresh credential.
func (c *Client) Refresh(ctx context.Context, metadata Metadata, clientID, refreshToken string) (Tokens, error) {
	if validateMetadata(metadata) != nil || !identity(clientID) || !secret(refreshToken, 16) {
		return Tokens{}, ErrInvalidRequest
	}
	return c.tokens(ctx, metadata, url.Values{"grant_type": {"refresh_token"}, "client_id": {clientID}, "refresh_token": {refreshToken}})
}

func (c *Client) tokens(ctx context.Context, metadata Metadata, form url.Values) (Tokens, error) {
	_, object, err := c.response(ctx, http.MethodPost, metadata.TokenEndpoint, "application/x-www-form-urlencoded", "", "", []byte(form.Encode()), maximumTokenResponse, false)
	if err != nil {
		return Tokens{}, err
	}
	if validateSchema("TokenResponse", object) != nil {
		return Tokens{}, ErrInvalidResponse
	}
	var wire struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		Scope        string `json:"scope"`
		GrantID      string `json:"grant_id"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if decodeFields(object, &wire) != nil || !secret(wire.AccessToken, 16) || !secret(wire.RefreshToken, 16) {
		return Tokens{}, ErrInvalidResponse
	}
	grantExpires, err := unixSeconds(object["grant_expires"])
	if err != nil {
		return Tokens{}, err
	}
	now := c.now().Unix()
	if grantExpires <= now {
		return Tokens{}, ErrInvalidResponse
	}
	accessExpires := now + wire.ExpiresIn
	if accessExpires > grantExpires {
		accessExpires = grantExpires
	}
	return Tokens{AccessToken: wire.AccessToken, RefreshToken: wire.RefreshToken, Scope: wire.Scope, GrantID: wire.GrantID,
		GrantExpires: grantExpires, AccessExpires: accessExpires}, nil
}

func (c *Client) Revoke(ctx context.Context, metadata Metadata, clientID, refreshToken string) error {
	if validateMetadata(metadata) != nil || !identity(clientID) || !secret(refreshToken, 16) {
		return ErrInvalidRequest
	}
	form := url.Values{"token": {refreshToken}, "client_id": {clientID}, "token_type_hint": {"refresh_token"}}
	_, _, err := c.response(ctx, http.MethodPost, metadata.RevocationEndpoint, "application/x-www-form-urlencoded", "", "", []byte(form.Encode()), maximumTokenResponse, true)
	return err
}

func (c *Client) FetchCatalog(ctx context.Context, metadata Metadata, accessToken string) (Catalog, error) {
	if validateMetadata(metadata) != nil || !secret(accessToken, 16) {
		return Catalog{}, ErrInvalidRequest
	}
	raw, object, err := c.response(ctx, http.MethodGet, metadata.ProductsEndpoint, "", accessToken, "", nil, maximumProductResponse, false)
	if err != nil {
		return Catalog{}, err
	}
	if validateSchema("Catalog", object) != nil {
		return Catalog{}, ErrInvalidResponse
	}
	var catalog Catalog
	if decodeFields(object, &catalog) != nil || catalog.Issuer != metadata.Issuer {
		return Catalog{}, ErrInvalidResponse
	}
	var source map[string]json.RawMessage
	var listings []json.RawMessage
	if json.Unmarshal(raw, &source) != nil || json.Unmarshal(source["products"], &listings) != nil || len(listings) != len(catalog.Products) {
		return Catalog{}, ErrInvalidResponse
	}
	for i := range catalog.Products {
		catalog.Products[i].Raw = append(json.RawMessage(nil), listings[i]...)
	}
	seen := map[string]bool{}
	for _, listing := range catalog.Products {
		if listing.ShopID != catalog.Shop.ID || seen[listing.ID] || validateListing(listing, metadata.Issuer) != nil {
			return Catalog{}, ErrInvalidResponse
		}
		seen[listing.ID] = true
	}
	return catalog, nil
}

func (c *Client) FetchListing(ctx context.Context, metadata Metadata, accessToken, productID string) (Listing, error) {
	if validateMetadata(metadata) != nil || !secret(accessToken, 16) || !productIDPattern.MatchString(productID) {
		return Listing{}, ErrInvalidRequest
	}
	raw, object, err := c.response(ctx, http.MethodGet, metadata.ProductsEndpoint+"/"+url.PathEscape(productID), "", accessToken, "", nil, maximumProductResponse, false)
	if err != nil {
		return Listing{}, err
	}
	if validateSchema("Listing", object) != nil {
		return Listing{}, ErrInvalidResponse
	}
	var listing Listing
	if decodeFields(object, &listing) != nil || listing.ID != productID || validateListing(listing, metadata.Issuer) != nil {
		return Listing{}, ErrInvalidResponse
	}
	listing.Raw = append(json.RawMessage(nil), raw...)
	return listing, nil
}

// IssueCards makes one explicit request with the caller's durable idempotency
// key. Neither network failures nor protocol errors generate a different key.
func (c *Client) IssueCards(ctx context.Context, metadata Metadata, accessToken, idempotencyKey string, request CardRequest) (CardBatch, error) {
	body, err := json.Marshal(request)
	if err != nil {
		return CardBatch{}, ErrInvalidRequest
	}
	return c.IssueCardsJSON(ctx, metadata, accessToken, idempotencyKey, body)
}

// IssueCardsJSON sends the exact persisted bytes. Recovery must not re-marshal
// a historical request after a software upgrade or optional-field change.
func (c *Client) IssueCardsJSON(ctx context.Context, metadata Metadata, accessToken, idempotencyKey string, body []byte) (CardBatch, error) {
	if validateMetadata(metadata) != nil || !secret(accessToken, 16) || len(idempotencyKey) < 8 || len(idempotencyKey) > 200 || !ascii(idempotencyKey) || len(body) > maximumTokenResponse {
		return CardBatch{}, ErrInvalidRequest
	}
	body = append([]byte(nil), body...)
	value, err := decodeJSON(body)
	if err != nil || validateSchema("CardIssueRequest", value) != nil {
		return CardBatch{}, ErrInvalidRequest
	}
	var request CardRequest
	if decodeFields(value, &request) != nil || request.Count > metadata.MaximumCardsPerRequest {
		return CardBatch{}, ErrInvalidRequest
	}
	raw, _, err := c.response(ctx, http.MethodPost, metadata.CardsEndpoint, "application/json", accessToken, idempotencyKey, body, maximumCardResponse, false)
	if err != nil {
		return CardBatch{}, err
	}
	batch, err := ParseCardBatch(raw)
	if err != nil {
		return CardBatch{}, err
	}
	if batch.ProductID != request.ProductID || batch.VariantID != request.VariantID || batch.Count != request.Count {
		return CardBatch{}, ErrInvalidResponse
	}
	return batch, nil
}

// ParseCardBatch validates a confidential persisted receipt without contacting
// the issuer. Callers additionally bind GrantID/product/variant/count to their
// own saved connection and request before using its codes.
func ParseCardBatch(raw []byte) (CardBatch, error) {
	if len(raw) > maximumCardResponse {
		return CardBatch{}, ErrInvalidResponse
	}
	raw = append([]byte(nil), raw...)
	object, err := decodeJSON(raw)
	if err != nil {
		return CardBatch{}, err
	}
	if validateSchema("CardBatch", object) != nil {
		return CardBatch{}, ErrInvalidResponse
	}
	var wire struct {
		Schema    string   `json:"schema"`
		GrantID   string   `json:"grant_id"`
		ProductID string   `json:"product_id"`
		VariantID string   `json:"variant_id"`
		BatchID   string   `json:"batch_id"`
		Count     int      `json:"count"`
		Codes     []string `json:"codes"`
		Variant   Variant  `json:"variant"`
		Quota     struct {
			Maximum   int `json:"max_count"`
			Issued    int `json:"issued_count"`
			Remaining int `json:"remaining"`
		} `json:"quota"`
	}
	if decodeFields(object, &wire) != nil || wire.Variant.ID != wire.VariantID || !wire.Variant.Enabled || !currencyPattern.MatchString(wire.Variant.Currency) ||
		len(wire.Codes) != wire.Count || wire.Quota.Issued < wire.Count ||
		wire.Quota.Issued > wire.Quota.Maximum || wire.Quota.Remaining != wire.Quota.Maximum-wire.Quota.Issued {
		return CardBatch{}, ErrInvalidResponse
	}
	// uniqueItems and the code alphabet/length are checked by the schema, but
	// count equality and quota arithmetic require explicit semantic checks.
	recovery, err := unixSeconds(object["recovery_expires"])
	if err != nil {
		return CardBatch{}, err
	}
	created, err := unixSeconds(object["created_at"])
	if err != nil || recovery <= created || recovery-created > 86400 {
		return CardBatch{}, ErrInvalidResponse
	}
	return CardBatch{Schema: wire.Schema, GrantID: wire.GrantID, ProductID: wire.ProductID, VariantID: wire.VariantID,
		BatchID: wire.BatchID, Count: wire.Count, Codes: wire.Codes, RecoveryExpires: recovery, Variant: wire.Variant,
		Raw: json.RawMessage(raw)}, nil
}
