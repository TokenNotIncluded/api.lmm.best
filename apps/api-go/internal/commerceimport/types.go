// Package commerceimport implements the public extore.commerce-import.v1
// protocol. It does not approve grants, retry single-use credentials, or persist
// secrets. Callers must serialize refreshes and persist issuance requests before
// calling IssueCards.
package commerceimport

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const (
	ProtocolSchema  = "extore.commerce-import.v1"
	ListingSchema   = "extore.product-listing.v1"
	CatalogSchema   = "extore.commerce-catalog.v1"
	CardBatchSchema = "extore.card-batch.v1"
)

type Metadata struct {
	Issuer                 string `json:"issuer"`
	AuthorizationEndpoint  string `json:"authorization_endpoint"`
	TokenEndpoint          string `json:"token_endpoint"`
	RevocationEndpoint     string `json:"revocation_endpoint"`
	ProductsEndpoint       string `json:"products_endpoint"`
	CardsEndpoint          string `json:"cards_endpoint"`
	MaximumCardsPerRequest int    `json:"maximum_cards_per_request"`
}

type Tokens struct {
	AccessToken   string `json:"access_token"`
	RefreshToken  string `json:"refresh_token"`
	Scope         string `json:"scope"`
	GrantID       string `json:"grant_id"`
	GrantExpires  int64  `json:"grant_expires"`
	AccessExpires int64  `json:"access_expires"`
}

// Explicit field access/JSON encoding is a confidential export. Ordinary fmt
// output must never expose either token or a confidential batch response.
func (Tokens) String() string               { return "commerceimport.Tokens{redacted}" }
func (t Tokens) GoString() string           { return t.String() }
func (t Tokens) Format(w fmt.State, _ rune) { _, _ = io.WriteString(w, t.String()) }

type Listing struct {
	Schema        string          `json:"schema"`
	ID            string          `json:"id"`
	ShopID        string          `json:"shop_id"`
	Revision      string          `json:"revision"`
	RedemptionURL string          `json:"redemption_url"`
	Product       json.RawMessage `json:"product"`
	Variants      []Variant       `json:"variants"`
	Raw           json.RawMessage `json:"-"`
}

type Variant struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Currency    string          `json:"currency"`
	Price       *string         `json:"price"`
	Enabled     bool            `json:"enabled"`
	Attributes  json.RawMessage `json:"attributes"`
}

type Catalog struct {
	Schema string `json:"schema"`
	Issuer string `json:"issuer"`
	Shop   struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"shop"`
	GrantID  string    `json:"grant_id"`
	Products []Listing `json:"products"`
}

type CardRequest struct {
	ProductID        string `json:"product_id"`
	VariantID        string `json:"variant_id"`
	ExpectedRevision string `json:"expected_revision,omitempty"`
	Label            string `json:"label,omitempty"`
	Count            int    `json:"count"`
}

type CardBatch struct {
	Schema          string   `json:"schema"`
	GrantID         string   `json:"grant_id"`
	ProductID       string   `json:"product_id"`
	VariantID       string   `json:"variant_id"`
	BatchID         string   `json:"batch_id"`
	Count           int      `json:"count"`
	Codes           []string `json:"codes"`
	RecoveryExpires int64    `json:"recovery_expires"`
	Variant         Variant  `json:"variant"`
	// Raw is the complete validated confidential response, including frozen
	// variant/quota extensions. It must be encrypted by its persistence owner.
	Raw json.RawMessage `json:"-"`
}

func (CardBatch) String() string               { return "commerceimport.CardBatch{redacted}" }
func (b CardBatch) GoString() string           { return b.String() }
func (b CardBatch) Format(w fmt.State, _ rune) { _, _ = io.WriteString(w, b.String()) }

// ProtocolError contains only a fixed, recognized error code and HTTP status.
// Server descriptions and underlying URL/network/schema errors are discarded.
type ProtocolError struct {
	Code       string
	StatusCode int
}

func (e *ProtocolError) Error() string { return "commerce import: " + safeErrorCode(e.Code) }

var (
	ErrAuthorizationDenied = &ProtocolError{Code: "access_denied"}
	ErrInvalidOrigin       = &ProtocolError{Code: "invalid_origin"}
	ErrInvalidCallback     = &ProtocolError{Code: "invalid_callback"}
	ErrInvalidRequest      = &ProtocolError{Code: "invalid_request"}
	ErrInvalidResponse     = &ProtocolError{Code: "invalid_response"}
	ErrNetwork             = &ProtocolError{Code: "network_error"}
)

func ErrorCode(err error) string {
	if err == nil {
		return ""
	}
	var protocol *ProtocolError
	if errors.As(err, &protocol) {
		return safeErrorCode(protocol.Code)
	}
	return "commerce_error"
}

func safeErrorCode(code string) string {
	switch code {
	case "access_denied", "invalid_origin", "invalid_callback", "invalid_request",
		"invalid_response", "network_error", "invalid_grant", "invalid_scope",
		"unsupported_grant_type", "invalid_token", "insufficient_scope",
		"product_unavailable", "variant_unavailable", "unsupported_product",
		"review_changed", "quota_exceeded", "catalog_changed", "idempotency_conflict",
		"issuance_expired", "rate_limited", "server_error":
		return code
	default:
		return "commerce_error"
	}
}
