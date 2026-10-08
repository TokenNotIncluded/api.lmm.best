package model

import (
	"errors"
	"fmt"
)

var (
	ErrCommerceImportLease       = errors.New("commerce connection is busy")
	ErrCommerceImportReauthorize = errors.New("commerce connection requires authorization")
)

// Secret-bearing storage and internal input fields are never serialized by the
// ordinary merchant API. A connection owns exactly one issuer/shop/grant.
type MerchantStoreCommerceConnection struct {
	ID                     string `json:"id" gorm:"primaryKey;size:36"`
	SellerID               int    `json:"-" gorm:"not null;index"`
	Issuer                 string `json:"issuer" gorm:"size:512;not null"`
	ClientID               string `json:"client_id" gorm:"size:100;not null"`
	RedirectURI            string `json:"redirect_uri" gorm:"type:text;not null"`
	MetadataJSON           string `json:"-" gorm:"type:text;not null"`
	MaximumCardsPerRequest int    `json:"maximum_cards_per_request" gorm:"not null"`
	ShopID                 string `json:"shop_id" gorm:"size:100;not null;default:''"`
	ShopName               string `json:"shop_name" gorm:"size:200;not null;default:''"`
	GrantID                string `json:"grant_id" gorm:"size:100;not null;default:''"`
	Scope                  string `json:"scope" gorm:"size:100;not null;default:''"`
	Status                 string `json:"status" gorm:"size:24;not null"`
	TokensCiphertext       string `json:"-" gorm:"type:text;not null;default:''"`
	AccessExpiresAt        int64  `json:"access_expires_at" gorm:"not null;default:0"`
	GrantExpiresAt         int64  `json:"grant_expires_at" gorm:"not null;default:0"`
	LeaseOwner             string `json:"-" gorm:"size:64;not null;default:''"`
	LeaseExpiresAt         int64  `json:"-" gorm:"not null;default:0"`
	AuthSessionID          string `json:"-" gorm:"size:36;not null;default:''"`
	TokenVersion           int64  `json:"-" gorm:"not null;default:0"`
	RefreshAttemptAt       int64  `json:"-" gorm:"not null;default:0"`
	CreatedAt              int64  `json:"created_at"`
	UpdatedAt              int64  `json:"updated_at"`
}

type MerchantStoreCommerceSession struct {
	ID                string `json:"id" gorm:"primaryKey;size:36"`
	ConnectionID      string `json:"connection_id" gorm:"size:36;not null;index"`
	SellerID          int    `json:"-" gorm:"not null;index"`
	StateHash         string `json:"-" gorm:"size:64;not null;uniqueIndex"`
	SecretsCiphertext string `json:"-" gorm:"type:text;not null"`
	Status            string `json:"-" gorm:"size:16;not null"`
	ExpiresAt         int64  `json:"expires_at" gorm:"not null;index"`
	CreatedAt         int64  `json:"created_at"`
}

type MerchantStoreCommerceProductMapping struct {
	ID                string                                `json:"-" gorm:"primaryKey;size:36"`
	SellerID          int                                   `json:"-" gorm:"not null;uniqueIndex:commerce_external_product,priority:1"`
	Issuer            string                                `json:"-" gorm:"size:512;not null;uniqueIndex:commerce_external_product,priority:2"`
	ShopID            string                                `json:"-" gorm:"size:100;not null;uniqueIndex:commerce_external_product,priority:3"`
	ExternalProductID string                                `json:"external_product_id" gorm:"size:100;not null;uniqueIndex:commerce_external_product,priority:4"`
	ConnectionID      string                                `json:"-" gorm:"size:36;not null;index"`
	LocalProductID    string                                `json:"local_product_id" gorm:"size:36;not null;uniqueIndex"`
	Revision          string                                `json:"revision" gorm:"size:64;not null"`
	RedemptionURL     string                                `json:"redemption_url" gorm:"type:text;not null"`
	Mode              string                                `json:"mode" gorm:"size:16;not null"`
	RawJSON           string                                `json:"-" gorm:"type:text;not null"`
	CreatedAt         int64                                 `json:"created_at"`
	UpdatedAt         int64                                 `json:"updated_at"`
	Variants          []MerchantStoreCommerceVariantMapping `json:"variants" gorm:"-"`
}

type MerchantStoreCommerceVariantMapping struct {
	ID               string  `json:"-" gorm:"primaryKey;size:36"`
	ProductMappingID string  `json:"-" gorm:"size:36;not null;uniqueIndex:commerce_external_variant,priority:1"`
	ExternalID       string  `json:"external_id" gorm:"size:100;not null;uniqueIndex:commerce_external_variant,priority:2"`
	LocalVariantID   string  `json:"local_variant_id" gorm:"size:36;not null;uniqueIndex"`
	ReferencePrice   *string `json:"reference_price" gorm:"size:100"`
	Currency         string  `json:"currency" gorm:"size:5;not null;default:''"`
	Description      string  `json:"description" gorm:"type:text"`
	RawJSON          string  `json:"-" gorm:"type:text;not null"`
	Enabled          bool    `json:"enabled" gorm:"not null"`
	CreatedAt        int64   `json:"created_at"`
	UpdatedAt        int64   `json:"updated_at"`
}

type MerchantStoreCommerceRestockRequest struct {
	ID                 string `json:"id" gorm:"primaryKey;size:36"`
	ConnectionID       string `json:"-" gorm:"size:36;not null;uniqueIndex:commerce_request_key,priority:1;index"`
	SellerID           int    `json:"-" gorm:"not null;index"`
	GrantExpiresAt     int64  `json:"-" gorm:"not null"`
	GrantID            string `json:"-" gorm:"size:100;not null;uniqueIndex:commerce_request_key,priority:2"`
	IdempotencyKey     string `json:"-" gorm:"-"`
	KeyHash            string `json:"-" gorm:"size:64;not null;uniqueIndex:commerce_request_key,priority:3"`
	KeyCiphertext      string `json:"-" gorm:"type:text;not null"`
	Body               string `json:"-" gorm:"-"`
	BodyCiphertext     string `json:"-" gorm:"type:text;not null"`
	ResponseCiphertext string `json:"-" gorm:"type:text;not null;default:''"`
	ResponseHash       string `json:"-" gorm:"size:64;not null;default:''"`
	BodyHash           string `json:"-" gorm:"size:64;not null"`
	ProductMappingID   string `json:"-" gorm:"size:36;not null"`
	VariantMappingID   string `json:"-" gorm:"size:36;not null"`
	ProductID          string `json:"product_id" gorm:"size:36;not null"`
	VariantID          string `json:"variant_id" gorm:"size:36;not null"`
	ExternalProductID  string `json:"-" gorm:"size:100;not null"`
	ExternalVariantID  string `json:"-" gorm:"size:100;not null"`
	Revision           string `json:"-" gorm:"size:64;not null"`
	Count              int    `json:"count" gorm:"not null"`
	Label              string `json:"label" gorm:"size:400"`
	IssueAttempts      int    `json:"-" gorm:"not null;default:0"`
	IssuanceUncertain  bool   `json:"issuance_uncertain" gorm:"not null;default:false"`
	Status             string `json:"status" gorm:"size:24;not null"`
	BatchID            string `json:"batch_id" gorm:"size:100;not null;default:''"`
	ErrorCode          string `json:"error_code" gorm:"size:64;not null;default:''"`
	RecoveryExpiresAt  int64  `json:"recovery_expires" gorm:"not null;default:0"`
	CreatedAt          int64  `json:"created_at"`
	UpdatedAt          int64  `json:"updated_at"`
}

type MerchantStoreCommerceCardBatch struct {
	ID                 string `json:"-" gorm:"primaryKey;size:36"`
	ConnectionID       string `json:"-" gorm:"size:36;not null;uniqueIndex:commerce_batch_identity,priority:1"`
	GrantExpiresAt     int64  `json:"-" gorm:"not null"`
	GrantID            string `json:"-" gorm:"size:100;not null;uniqueIndex:commerce_batch_identity,priority:2"`
	BatchID            string `json:"batch_id" gorm:"size:100;not null;uniqueIndex:commerce_batch_identity,priority:3"`
	RequestID          string `json:"-" gorm:"size:36;not null;uniqueIndex"`
	ResponseCiphertext string `json:"-" gorm:"type:text;not null"`
	ResponseHash       string `json:"-" gorm:"size:64;not null"`
	RecoveryExpiresAt  int64  `json:"recovery_expires" gorm:"not null;index"`
	Count              int    `json:"count" gorm:"not null"`
	CreatedAt          int64  `json:"created_at"`
}

func CommerceImportModels() []interface{} {
	return []interface{}{&MerchantStoreCommerceConnection{}, &MerchantStoreCommerceSession{}, &MerchantStoreCommerceProductMapping{}, &MerchantStoreCommerceVariantMapping{}, &MerchantStoreCommerceRestockRequest{}, &MerchantStoreCommerceCardBatch{}}
}

type CommerceImportConnectionInput struct {
	Issuer, ClientID, RedirectURI, MetadataJSON string
	MaximumCardsPerRequest                      int
}
type CommerceImportLease struct {
	ConnectionID string `json:"connection_id"`
	Owner        string `json:"-"`
	ExpiresAt    int64  `json:"-"`
}
type CommerceImportSessionInput struct {
	State              string `json:"-"`
	Verifier           string `json:"-"`
	DashboardSessionID string `json:"-"`
	RequestedScope     string `json:"-"`
	ExpiresAt          int64  `json:"-"`
}
type CommerceImportSessionSecrets struct {
	SessionID          string `json:"-"`
	ConnectionID       string `json:"-"`
	Issuer             string `json:"-"`
	ClientID           string `json:"-"`
	RedirectURI        string `json:"-"`
	State              string `json:"-"`
	Verifier           string `json:"-"`
	DashboardSessionID string `json:"-"`
	RequestedScope     string `json:"-"`
}
type CommerceImportTokenInput struct {
	AccessToken     string `json:"-"`
	RefreshToken    string `json:"-"`
	Scope           string `json:"-"`
	GrantID         string `json:"-"`
	AccessExpiresAt int64  `json:"-"`
	GrantExpiresAt  int64  `json:"-"`
}

// Internal wire encryption requires a private representation because public
// JSON deliberately hides the secrets.
type commerceImportTokenPayload struct {
	AccessToken, RefreshToken, Scope, GrantID string
	AccessExpiresAt, GrantExpiresAt           int64
}
type commerceImportSessionPayload struct{ State, Verifier, DashboardSessionID, RequestedScope string }
type CommerceImportProductInput struct {
	ExternalProductID, ShopID, Revision, RedemptionURL, Mode, RawJSON string
	Product                                                           MerchantStoreProductInput
	PriceQuota                                                        *int
	Visibility                                                        *string
	Variants                                                          []CommerceImportVariantInput
}
type CommerceImportVariantInput struct {
	ExternalID, Name, Description string
	ReferencePrice                *string
	Currency, RawJSON             string
	Enabled                       bool
	PriceQuota                    *int
}
type CommerceImportRestockInput struct {
	ExternalProductID, ExternalVariantID, Revision string
	IdempotencyKey                                 string `json:"-"`
	Body                                           string `json:"-"`
	Count                                          int
	Label                                          string
}
type CommerceImportBatchInput struct {
	GrantID, ExternalProductID, ExternalVariantID, BatchID string
	RawJSON                                                string `json:"-"`
	Count                                                  int
	Codes                                                  []string `json:"-"`
	RecoveryExpiresAt                                      int64
}

func (CommerceImportTokenInput) Format(f fmt.State, verb rune) {
	_, _ = f.Write([]byte("CommerceImportTokenInput{redacted}"))
}
func (CommerceImportSessionInput) Format(f fmt.State, verb rune) {
	_, _ = f.Write([]byte("CommerceImportSessionInput{redacted}"))
}
func (CommerceImportSessionSecrets) Format(f fmt.State, verb rune) {
	_, _ = f.Write([]byte("CommerceImportSessionSecrets{redacted}"))
}
func (CommerceImportBatchInput) Format(f fmt.State, verb rune) {
	_, _ = f.Write([]byte("CommerceImportBatchInput{redacted}"))
}
func (CommerceImportRestockInput) Format(f fmt.State, verb rune) {
	_, _ = f.Write([]byte("CommerceImportRestockInput{redacted}"))
}
func (CommerceImportLease) Format(f fmt.State, verb rune) {
	_, _ = f.Write([]byte("CommerceImportLease{redacted}"))
}
func (MerchantStoreCommerceConnection) Format(f fmt.State, verb rune) {
	_, _ = f.Write([]byte("MerchantStoreCommerceConnection{redacted}"))
}
func (MerchantStoreCommerceSession) Format(f fmt.State, verb rune) {
	_, _ = f.Write([]byte("MerchantStoreCommerceSession{redacted}"))
}
func (MerchantStoreCommerceRestockRequest) Format(f fmt.State, verb rune) {
	_, _ = f.Write([]byte("MerchantStoreCommerceRestockRequest{redacted}"))
}
func (MerchantStoreCommerceCardBatch) Format(f fmt.State, verb rune) {
	_, _ = f.Write([]byte("MerchantStoreCommerceCardBatch{redacted}"))
}
