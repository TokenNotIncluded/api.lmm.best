package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/internal/commerceimport"
	"github.com/LIghtJUNction/api.lmm.best/model"
)

// These narrow function seams permit an isolated protocol fixture without
// bypassing production URL/DNS policy or replacing the default HTTP client.
var commerceDiscover = commerceimport.Discover
var commerceExchange = commerceimport.Exchange
var commerceRefresh = commerceimport.Refresh
var commerceRevoke = commerceimport.Revoke
var commerceCatalog = commerceimport.FetchCatalog
var commerceListing = commerceimport.FetchListing
var commerceCards = commerceimport.IssueCardsJSON

func commerceRandom() (string, error) {
	var data [32]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", model.ErrMerchantStoreUnavailable
	}
	return base64.RawURLEncoding.EncodeToString(data[:]), nil
}

func commerceMetadata(connection *model.MerchantStoreCommerceConnection) (commerceimport.Metadata, error) {
	var metadata commerceimport.Metadata
	if connection == nil || json.Unmarshal([]byte(connection.MetadataJSON), &metadata) != nil || metadata.Issuer != connection.Issuer || metadata.MaximumCardsPerRequest != connection.MaximumCardsPerRequest {
		return metadata, ErrCommerceImportConfiguration
	}
	return metadata, nil
}

func CreateCommerceImportConnection(ctx context.Context, actor int, origin, clientID string) (*model.MerchantStoreCommerceConnection, error) {
	config, err := commerceImportTrusted(origin)
	if err != nil {
		return nil, err
	}
	metadata, err := commerceDiscover(ctx, origin)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return nil, model.ErrMerchantStoreInput
	}
	return model.CreateCommerceImportConnection(actor, model.CommerceImportConnectionInput{Issuer: origin, ClientID: clientID, RedirectURI: config.RedirectURI, MetadataJSON: string(encoded), MaximumCardsPerRequest: metadata.MaximumCardsPerRequest})
}

func AuthorizeCommerceImport(ctx context.Context, actor int, connectionID, sessionID string, cardsIssue bool) (string, error) {
	if sessionID == "" {
		return "", model.ErrMerchantStoreDenied
	}
	connection, err := model.GetCommerceImportConnection(actor, connectionID)
	if err != nil {
		return "", err
	}
	config, err := commerceImportTrusted(connection.Issuer)
	if err != nil || connection.RedirectURI != config.RedirectURI {
		return "", ErrCommerceImportConfiguration
	}
	metadata, err := commerceMetadata(connection)
	if err != nil {
		return "", err
	}
	state, err := commerceRandom()
	if err != nil {
		return "", err
	}
	verifier, err := commerceRandom()
	if err != nil {
		return "", err
	}
	scopes := []string{"products.read"}
	if cardsIssue {
		scopes = append(scopes, "cards.issue")
	}
	target, err := commerceimport.AuthorizationURL(metadata, connection.ClientID, connection.RedirectURI, state, verifier, scopes)
	if err != nil {
		return "", err
	}
	_, err = model.SaveCommerceImportSession(actor, connectionID, model.CommerceImportSessionInput{State: state, Verifier: verifier, DashboardSessionID: sessionID, RequestedScope: strings.Join(scopes, " "), ExpiresAt: time.Now().Unix() + 600})
	return target, err
}

// A browser redirect cannot carry the ordinary bearer header. This endpoint
// accepts only the current HttpOnly refresh cookie after the dedicated callback
// page makes a same-origin POST; it never rotates or exports login credentials.
func CommerceImportCallbackIdentity(request *http.Request) (int, string, error) {
	if request == nil || request.URL == nil || request.Method != http.MethodPost || request.URL.EscapedPath() != CommerceImportCompletePath || request.URL.RawQuery != "" || request.URL.ForceQuery || MerchantStoreClaimHasAuthorization(request) || !merchantStoreClaimSameOrigin(request) {
		return 0, "", model.ErrMerchantStoreDenied
	}
	var raw string
	count := 0
	for _, cookie := range request.Cookies() {
		if cookie.Name == RefreshCookieName {
			raw = cookie.Value
			count++
		}
	}
	if count != 1 || len(raw) > 512 {
		return 0, "", model.ErrMerchantStoreDenied
	}
	sid, secret, ok := splitRefreshToken(raw)
	if !ok {
		return 0, "", model.ErrMerchantStoreDenied
	}
	var session model.UserSession
	if model.DB.WithContext(request.Context()).Select("user_id").Where("sid = ?", sid).First(&session).Error != nil || merchantStoreClaimSessionValid(model.DB.WithContext(request.Context()), session.UserID, sid, secret, false) != nil {
		return 0, "", model.ErrMerchantStoreDenied
	}
	return session.UserID, sid, nil
}

func CompleteCommerceImportCallback(ctx context.Context, actor int, sessionID, actual string) (string, error) {
	parsed, err := url.Parse(actual)
	if err != nil || len(actual) > 8192 {
		return "", commerceimport.ErrInvalidCallback
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil || len(query["state"]) != 1 {
		return "", commerceimport.ErrInvalidCallback
	}
	transaction, err := model.GetCommerceImportSession(actor, query.Get("state"), sessionID)
	if err != nil {
		return "", err
	}
	config, err := commerceImportTrusted(transaction.Issuer)
	if err != nil || config.RedirectURI != transaction.RedirectURI {
		return "", ErrCommerceImportConfiguration
	}
	code, callbackErr := commerceimport.ValidateCallback(transaction.RedirectURI, actual, transaction.State, transaction.Issuer)
	if callbackErr != nil && !errors.Is(callbackErr, commerceimport.ErrAuthorizationDenied) {
		return "", callbackErr
	}
	lease, err := model.AcquireCommerceImportLease(actor, transaction.ConnectionID, 180)
	if err != nil {
		return "", err
	}
	defer model.ReleaseCommerceImportLease(actor, *lease)
	transaction, err = model.ConsumeCommerceImportSessionWithLease(actor, *lease, transaction.SessionID, sessionID)
	if err != nil {
		return "", err
	}
	if callbackErr != nil {
		return "denied", model.FinishCommerceImportDeniedWithLease(actor, *lease, transaction.SessionID)
	}
	connection, err := model.GetCommerceImportConnection(actor, transaction.ConnectionID)
	if err != nil {
		return "", err
	}
	metadata, err := commerceMetadata(connection)
	if err != nil {
		return "", err
	}
	// The consumed transaction remains consumed on timeout or lost response.
	tokens, err := commerceExchange(ctx, metadata, transaction.ClientID, transaction.RedirectURI, code, transaction.Verifier)
	if err != nil {
		return "", err
	}
	_, err = model.CompleteCommerceImportAuthorizationWithLease(actor, *lease, transaction.SessionID, commerceTokenInput(tokens))
	if err != nil {
		return "", err
	}
	return "connected", nil
}

func commerceTokenInput(tokens commerceimport.Tokens) model.CommerceImportTokenInput {
	return model.CommerceImportTokenInput{AccessToken: tokens.AccessToken, RefreshToken: tokens.RefreshToken, Scope: tokens.Scope, GrantID: tokens.GrantID, AccessExpiresAt: tokens.AccessExpires, GrantExpiresAt: tokens.GrantExpires}
}

func commerceAccess(ctx context.Context, actor int, lease model.CommerceImportLease, connection *model.MerchantStoreCommerceConnection, metadata commerceimport.Metadata) (*model.CommerceImportTokenInput, error) {
	tokens, err := model.LoadCommerceImportTokens(actor, lease)
	if err != nil {
		return nil, err
	}
	if tokens.GrantExpiresAt <= time.Now().Unix() {
		return nil, ErrCommerceImportReauthorize
	}
	if tokens.AccessExpiresAt > time.Now().Unix()+30 {
		return tokens, nil
	}
	tokens, err = model.BeginCommerceImportRefresh(actor, lease)
	if err != nil {
		return nil, err
	}
	rotated, err := commerceRefresh(ctx, metadata, connection.ClientID, tokens.RefreshToken)
	if err != nil {
		return nil, err
	}
	next := commerceTokenInput(rotated)
	if err = model.CompleteCommerceImportRefresh(actor, lease, next); err != nil {
		return nil, err
	}
	return &next, nil
}

func commerceWithConnection(ctx context.Context, actor int, id string, operation func(context.Context, model.CommerceImportLease, *model.MerchantStoreCommerceConnection, commerceimport.Metadata, *model.CommerceImportTokenInput) error) error {
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	connection, err := model.GetCommerceImportConnection(actor, id)
	if err != nil {
		return err
	}
	if _, err = commerceImportTrusted(connection.Issuer); err != nil {
		return err
	}
	lease, err := model.AcquireCommerceImportLease(actor, id, 180)
	if err != nil {
		return err
	}
	defer model.ReleaseCommerceImportLease(actor, *lease)
	// Reload after acquiring the durable lease; no other process may refresh
	// from a stale token snapshot while this operation is active.
	connection, err = model.GetCommerceImportConnection(actor, id)
	if err != nil {
		return err
	}
	metadata, err := commerceMetadata(connection)
	if err != nil {
		return err
	}
	tokens, err := commerceAccess(ctx, actor, *lease, connection, metadata)
	if err != nil {
		return err
	}
	return operation(ctx, *lease, connection, metadata, tokens)
}

type CommerceImportCatalogView struct {
	commerceimport.Catalog
	Mappings          []model.MerchantStoreCommerceProductMapping `json:"mappings"`
	UnsupportedFields []string                                    `json:"unsupported_fields"`
}

func FetchCommerceImportCatalog(ctx context.Context, actor int, id string) (*CommerceImportCatalogView, error) {
	var result CommerceImportCatalogView
	err := commerceWithConnection(ctx, actor, id, func(ctx context.Context, lease model.CommerceImportLease, connection *model.MerchantStoreCommerceConnection, metadata commerceimport.Metadata, tokens *model.CommerceImportTokenInput) error {
		catalog, err := commerceCatalog(ctx, metadata, tokens.AccessToken)
		if err != nil {
			return err
		}
		if catalog.Issuer != connection.Issuer || catalog.GrantID != tokens.GrantID || connection.ShopID != "" && catalog.Shop.ID != connection.ShopID {
			return commerceimport.ErrInvalidResponse
		}
		if err = model.BindCommerceImportShop(actor, lease, catalog.GrantID, catalog.Shop.ID, catalog.Shop.Name); err != nil {
			return err
		}
		result.Catalog = catalog
		result.Mappings, err = model.ListCommerceImportMappings(actor, id)
		result.UnsupportedFields = []string{"parameters", "outputs", "progress_steps", "revision_policy", "remote_media"}
		return err
	})
	return &result, err
}

type CommerceImportDraftInput struct {
	ProductID  string                       `json:"product_id"`
	Revision   string                       `json:"revision"`
	Visibility *string                      `json:"visibility"`
	Confirmed  bool                         `json:"confirmed"`
	Variants   []CommerceImportDraftVariant `json:"variants"`
}
type CommerceImportDraftVariant struct {
	ExternalID string `json:"external_id"`
	PriceQuota *int   `json:"price_quota"`
	Enabled    bool   `json:"enabled"`
}

func commerceLocalized(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var localized map[string]string
	if json.Unmarshal(raw, &localized) != nil {
		return ""
	}
	for _, key := range []string{"zh-CN", "zh", "en", "en-US"} {
		if text = localized[key]; text != "" {
			return text
		}
	}
	keys := make([]string, 0, len(localized))
	for key := range localized {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if text = localized[key]; text != "" {
			return text
		}
	}
	return ""
}

type commerceProductInfo struct {
	Name, Description  json.RawMessage
	Mode, SupportEmail string
}

func commerceProductFields(raw json.RawMessage) (commerceProductInfo, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return commerceProductInfo{}, commerceimport.ErrInvalidResponse
	}
	product := commerceProductInfo{Name: fields["name"], Description: fields["description"]}
	if mode, exists := fields["mode"]; exists && json.Unmarshal(mode, &product.Mode) != nil {
		return product, commerceimport.ErrInvalidResponse
	}
	if contact, exists := fields["support_email"]; exists && json.Unmarshal(contact, &product.SupportEmail) != nil {
		return product, commerceimport.ErrInvalidResponse
	}
	return product, nil
}

func ImportCommerceImportDraft(ctx context.Context, actor int, id string, input CommerceImportDraftInput) (*model.MerchantStoreCommerceProductMapping, error) {
	if !input.Confirmed || input.Visibility == nil || len(input.Variants) < 1 {
		return nil, model.ErrMerchantStoreInput
	}
	var result *model.MerchantStoreCommerceProductMapping
	err := commerceWithConnection(ctx, actor, id, func(ctx context.Context, lease model.CommerceImportLease, connection *model.MerchantStoreCommerceConnection, metadata commerceimport.Metadata, tokens *model.CommerceImportTokenInput) error {
		listing, err := commerceListing(ctx, metadata, tokens.AccessToken, input.ProductID)
		if err != nil {
			return err
		}
		if listing.ShopID != connection.ShopID || listing.Revision != input.Revision || listing.ID != input.ProductID {
			return model.ErrMerchantStoreConflict
		}
		product, err := commerceProductFields(listing.Product)
		if err != nil {
			return err
		}
		confirmed := map[string]CommerceImportDraftVariant{}
		for _, variant := range input.Variants {
			if variant.ExternalID == "" || variant.PriceQuota == nil || confirmed[variant.ExternalID].ExternalID != "" {
				return model.ErrMerchantStoreInput
			}
			confirmed[variant.ExternalID] = variant
		}
		if len(confirmed) != len(listing.Variants) {
			return model.ErrMerchantStoreInput
		}
		encoded := listing.Raw
		if len(encoded) == 0 {
			encoded, _ = json.Marshal(listing)
		}
		in := model.CommerceImportProductInput{ExternalProductID: listing.ID, ShopID: listing.ShopID, Revision: listing.Revision, RedemptionURL: listing.RedemptionURL, Mode: product.Mode, RawJSON: string(encoded), Visibility: input.Visibility}
		in.Product = model.MerchantStoreProductInput{Title: commerceLocalized(product.Name), Description: commerceLocalized(product.Description), Contact: product.SupportEmail, Template: "card-key", DeliveryStrategy: "sequential", PaymentMethods: []string{"balance"}, PickupLoginRequired: true, PickupCodeRequired: true, Visibility: input.Visibility, Links: []model.MerchantStoreLink{{Title: "Redemption", URL: listing.RedemptionURL}}}
		for index, variant := range listing.Variants {
			confirmation, ok := confirmed[variant.ID]
			if !ok || confirmation.Enabled && !variant.Enabled {
				return model.ErrMerchantStoreInput
			}
			if index == 0 {
				in.PriceQuota = confirmation.PriceQuota
				in.Product.PriceQuota = *confirmation.PriceQuota
			}
			raw, _ := json.Marshal(variant)
			in.Variants = append(in.Variants, model.CommerceImportVariantInput{ExternalID: variant.ID, Name: variant.Name, Description: variant.Description, ReferencePrice: variant.Price, Currency: variant.Currency, RawJSON: string(raw), Enabled: confirmation.Enabled, PriceQuota: confirmation.PriceQuota})
		}
		result, err = model.ImportCommerceImportProduct(actor, lease, in)
		return err
	})
	return result, err
}

type CommerceImportRestockInput struct {
	ProductID        string `json:"product_id"`
	VariantID        string `json:"variant_id"`
	Count            int    `json:"count"`
	ExpectedRevision string `json:"expected_revision"`
	Label            string `json:"label"`
}

func RestockCommerceImport(ctx context.Context, actor int, id string, input CommerceImportRestockInput) (*model.MerchantStoreCommerceRestockRequest, error) {
	var result *model.MerchantStoreCommerceRestockRequest
	err := commerceWithConnection(ctx, actor, id, func(ctx context.Context, lease model.CommerceImportLease, connection *model.MerchantStoreCommerceConnection, metadata commerceimport.Metadata, tokens *model.CommerceImportTokenInput) error {
		if !strings.Contains(" "+tokens.Scope+" ", " cards.issue ") {
			return &commerceimport.ProtocolError{Code: "insufficient_scope", StatusCode: http.StatusForbidden}
		}
		if input.Count < 1 || input.Count > connection.MaximumCardsPerRequest {
			return model.ErrMerchantStoreInput
		}
		listing, err := commerceListing(ctx, metadata, tokens.AccessToken, input.ProductID)
		if err != nil {
			return err
		}
		if listing.ShopID != connection.ShopID || listing.Revision != input.ExpectedRevision {
			return model.ErrMerchantStoreConflict
		}
		product, err := commerceProductFields(listing.Product)
		if err != nil || product.Mode == "stock" {
			return &commerceimport.ProtocolError{Code: "unsupported_product", StatusCode: http.StatusConflict}
		}
		found := false
		for _, variant := range listing.Variants {
			if variant.ID == input.VariantID && variant.Enabled {
				found = true
			}
		}
		if !found {
			return &commerceimport.ProtocolError{Code: "variant_unavailable", StatusCode: http.StatusConflict}
		}
		request := commerceimport.CardRequest{ProductID: input.ProductID, VariantID: input.VariantID, Count: input.Count, ExpectedRevision: input.ExpectedRevision, Label: input.Label}
		body, err := json.Marshal(request)
		if err != nil {
			return model.ErrMerchantStoreInput
		}
		key, err := commerceRandom()
		if err != nil {
			return err
		}
		result, _, err = model.PrepareCommerceImportRestock(actor, lease, model.CommerceImportRestockInput{ExternalProductID: input.ProductID, ExternalVariantID: input.VariantID, Count: input.Count, Revision: input.ExpectedRevision, Label: input.Label, IdempotencyKey: key, Body: string(body)})
		if err != nil {
			return err
		}
		result, err = commerceIssueSaved(ctx, actor, lease, metadata, tokens, result)
		return err
	})
	return result, err
}

func commerceIssueSaved(ctx context.Context, actor int, lease model.CommerceImportLease, metadata commerceimport.Metadata, tokens *model.CommerceImportTokenInput, saved *model.MerchantStoreCommerceRestockRequest) (*model.MerchantStoreCommerceRestockRequest, error) {
	if saved.GrantID != tokens.GrantID {
		return saved, ErrCommerceImportManualRecovery
	}
	if saved.Status == "imported" {
		return saved, nil
	}
	if saved.Status == "manual_recovery" {
		return saved, ErrCommerceImportManualRecovery
	}
	var request commerceimport.CardRequest
	if json.Unmarshal([]byte(saved.Body), &request) != nil || request.ProductID != saved.ExternalProductID || request.VariantID != saved.ExternalVariantID || request.Count != saved.Count || request.ExpectedRevision != saved.Revision {
		return saved, ErrCommerceImportManualRecovery
	}
	receipt, err := model.LoadCommerceImportBatchResponse(actor, lease, saved.ID)
	if err != nil {
		return saved, ErrCommerceImportManualRecovery
	}
	var batch commerceimport.CardBatch
	if receipt != "" {
		batch, err = commerceimport.ParseCardBatch([]byte(receipt))
	} else {
		saved, err = model.BeginCommerceImportIssue(actor, lease, saved.ID)
		if err != nil {
			return saved, err
		}
		batch, err = commerceCards(ctx, metadata, tokens.AccessToken, saved.IdempotencyKey, []byte(saved.Body))
	}
	if err != nil {
		_ = model.SetCommerceImportRestockRecovery(actor, lease, saved.ID, commerceimport.ErrorCode(err))
		return saved, err
	}
	result, _, err := model.ReceiveCommerceImportBatch(actor, lease, saved.ID, model.CommerceImportBatchInput{GrantID: batch.GrantID, ExternalProductID: batch.ProductID, ExternalVariantID: batch.VariantID, BatchID: batch.BatchID, Count: batch.Count, Codes: batch.Codes, RawJSON: string(batch.Raw), RecoveryExpiresAt: batch.RecoveryExpires})
	if err != nil {
		_ = model.SetCommerceImportRestockRecovery(actor, lease, saved.ID, "inventory_recovery_required")
	}
	return result, err
}

func RecoverCommerceImportRestock(ctx context.Context, actor int, id, requestID string) (*model.MerchantStoreCommerceRestockRequest, error) {
	var result *model.MerchantStoreCommerceRestockRequest
	err := commerceWithConnection(ctx, actor, id, func(ctx context.Context, lease model.CommerceImportLease, _ *model.MerchantStoreCommerceConnection, metadata commerceimport.Metadata, tokens *model.CommerceImportTokenInput) error {
		saved, err := model.LoadCommerceImportRestock(actor, lease, requestID)
		if err != nil {
			return err
		}
		result, err = commerceIssueSaved(ctx, actor, lease, metadata, tokens, saved)
		return err
	})
	return result, err
}

func DisconnectCommerceImport(ctx context.Context, actor int, id string) error {
	connection, err := model.GetCommerceImportConnection(actor, id)
	if err != nil {
		return err
	}
	lease, err := model.AcquireCommerceImportLease(actor, id, 180)
	if err != nil {
		return err
	}
	defer model.ReleaseCommerceImportLease(actor, *lease)
	// Local disconnect always clears credentials and sessions. A failed remote
	// revocation cannot delete buyer stock or imply remote card cancellation.
	tokens, tokenErr := model.LoadCommerceImportTokens(actor, *lease)
	metadata, metadataErr := commerceMetadata(connection)
	if tokenErr == nil && metadataErr == nil {
		if trustErr := commerceImportTrustedOrigin(GetCommerceImportConfiguration(), connection.Issuer); trustErr == nil {
			_ = commerceRevoke(ctx, metadata, connection.ClientID, tokens.RefreshToken)
		}
	}
	return model.DisconnectCommerceImportConnection(actor, *lease)
}
