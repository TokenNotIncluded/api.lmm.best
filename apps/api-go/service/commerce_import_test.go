package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/internal/commerceimport"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/system_setting"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type commerceServiceFixture struct {
	merchantStoreServiceFixture
	metadata   commerceimport.Metadata
	connection *model.MerchantStoreCommerceConnection
	session    model.UserSession
	secret     string
	listing    commerceimport.Listing
}

func commerceServiceDB(t *testing.T) commerceServiceFixture {
	t.Helper()
	f := commerceServiceFixture{merchantStoreServiceFixture: merchantStoreServiceDB(t, MerchantStoreBalance)}
	require.NoError(t, model.DB.Exec("UPDATE options SET value = '8' WHERE key = ?", model.MerchantStoreWriterCapabilityOption).Error)
	require.NoError(t, model.DB.AutoMigrate(&model.UserSession{}))
	oldOrigin, oldSecure := system_setting.ServerAddress, common.SessionCookieSecure
	system_setting.ServerAddress, common.SessionCookieSecure = "https://sales.example.com", true
	t.Cleanup(func() { system_setting.ServerAddress, common.SessionCookieSecure = oldOrigin, oldSecure })
	t.Setenv("MERCHANT_STORE_COMMERCE_IMPORT_ORIGINS", "https://redemption.example.com")
	f.metadata = commerceimport.Metadata{Issuer: "https://redemption.example.com", AuthorizationEndpoint: "https://redemption.example.com/oauth/authorize", TokenEndpoint: "https://redemption.example.com/api/integrations/commerce/token", RevocationEndpoint: "https://redemption.example.com/api/integrations/commerce/revoke", ProductsEndpoint: "https://redemption.example.com/api/integrations/commerce/products", CardsEndpoint: "https://redemption.example.com/api/integrations/commerce/cards", MaximumCardsPerRequest: 4}
	discover, exchange, refresh, revoke, catalog, listing, cards := commerceDiscover, commerceExchange, commerceRefresh, commerceRevoke, commerceCatalog, commerceListing, commerceCards
	t.Cleanup(func() {
		commerceDiscover, commerceExchange, commerceRefresh, commerceRevoke, commerceCatalog, commerceListing, commerceCards = discover, exchange, refresh, revoke, catalog, listing, cards
	})
	commerceDiscover = func(context.Context, string) (commerceimport.Metadata, error) { return f.metadata, nil }
	commerceRevoke = func(context.Context, commerceimport.Metadata, string, string) error { return nil }
	var err error
	f.connection, err = CreateCommerceImportConnection(context.Background(), f.seller.Id, f.metadata.Issuer, "merchant-selected-client-id")
	require.NoError(t, err)
	require.NoError(t, model.DB.First(&f.seller, f.seller.Id).Error)
	f.secret = strings.Repeat("Z", 64)
	now := time.Now().Unix()
	f.session = model.UserSession{SID: uuid.NewString(), UserID: f.seller.Id, Version: 1, UserAuthVersion: f.seller.AuthVersion, Status: model.UserSessionStatusActive, RefreshHash: hashRefreshSecret(f.secret), CreatedAt: now - 60, ExpiresAt: now + 3600}
	require.NoError(t, model.DB.Create(&f.session).Error)
	f.listing = commerceimport.Listing{Schema: commerceimport.ListingSchema, ID: "arbitrary-service", ShopID: "selected-shop", Revision: strings.Repeat("a", 64), RedemptionURL: "https://redemption.example.com/redeem", Product: json.RawMessage(`{"name":{"ja":"任意の商品","en":"Editable service"},"description":"Merchant instructions","mode":"manual","parameters":[],"outputs":[],"progress_steps":[]}`), Variants: []commerceimport.Variant{{ID: "custom-standard", Name: "Custom standard", Price: commerceString("25.125"), Currency: "CNY", Enabled: true, Attributes: json.RawMessage(`{"revisions":0}`)}, {ID: "unlimited-name", Name: "Any other spec", Price: nil, Currency: "EUR", Enabled: true, Attributes: json.RawMessage(`{"revisions":7}`)}}}
	f.listing.Raw, _ = json.Marshal(map[string]any{"schema": f.listing.Schema, "id": f.listing.ID, "shop_id": f.listing.ShopID, "revision": f.listing.Revision, "redemption_url": f.listing.RedemptionURL, "product": f.listing.Product, "variants": f.listing.Variants, "semantics": map[string]string{"price": "reference", "inventory": "not_exported", "payment": "external_sales_platform", "redemption": "extore"}, "vendor_extension": "retained-original-source"})
	commerceListing = func(_ context.Context, _ commerceimport.Metadata, _ string, productID string) (commerceimport.Listing, error) {
		require.Equal(t, f.listing.ID, productID)
		return f.listing, nil
	}
	commerceCatalog = func(context.Context, commerceimport.Metadata, string) (commerceimport.Catalog, error) {
		var connection model.MerchantStoreCommerceConnection
		require.NoError(t, model.DB.First(&connection, "id = ?", f.connection.ID).Error)
		result := commerceimport.Catalog{Schema: commerceimport.CatalogSchema, Issuer: f.metadata.Issuer, GrantID: connection.GrantID, Products: []commerceimport.Listing{f.listing}}
		result.Shop.ID, result.Shop.Name = f.listing.ShopID, "Selected merchant shop"
		return result, nil
	}
	return f
}

func commerceString(value string) *string { return &value }
func commerceInt(value int) *int          { return &value }

func commerceAuthorizeFixture(t *testing.T, f commerceServiceFixture, cardsIssue bool, accessLife int64) string {
	t.Helper()
	target, err := AuthorizeCommerceImport(context.Background(), f.seller.Id, f.connection.ID, f.session.SID, cardsIssue)
	require.NoError(t, err)
	parsed, err := url.Parse(target)
	require.NoError(t, err)
	state := parsed.Query().Get("state")
	transaction, err := model.GetCommerceImportSession(f.seller.Id, state, f.session.SID)
	require.NoError(t, err)
	scope := "products.read"
	if cardsIssue {
		scope += " cards.issue"
	}
	commerceExchange = func(_ context.Context, _ commerceimport.Metadata, clientID, redirectURI, code, verifier string) (commerceimport.Tokens, error) {
		require.Equal(t, f.connection.ClientID, clientID)
		require.Equal(t, f.connection.RedirectURI, redirectURI)
		require.Equal(t, "fixture-code", code)
		require.Equal(t, transaction.Verifier, verifier)
		return commerceimport.Tokens{AccessToken: "PRIVATE-access-token-0123456789", RefreshToken: "PRIVATE-refresh-token-0123456789", Scope: scope, GrantID: "approved-grant", AccessExpires: time.Now().Unix() + accessLife, GrantExpires: time.Now().Unix() + 3600}, nil
	}
	actual := f.connection.RedirectURI + "?" + url.Values{"code": {"fixture-code"}, "state": {state}, "iss": {f.metadata.Issuer}}.Encode()
	outcome, err := CompleteCommerceImportCallback(context.Background(), f.seller.Id, f.session.SID, actual)
	require.NoError(t, err)
	require.Equal(t, "connected", outcome)
	return actual
}

func TestCommerceImportTrustAndCallbackAccountIsolation(t *testing.T) {
	f := commerceServiceDB(t)
	called := false
	commerceDiscover = func(context.Context, string) (commerceimport.Metadata, error) { called = true; return f.metadata, nil }
	_, err := CreateCommerceImportConnection(context.Background(), f.seller.Id, "https://unreviewed.example.com", "public-client")
	require.ErrorIs(t, err, ErrCommerceImportConfiguration)
	require.False(t, called)
	target, err := AuthorizeCommerceImport(context.Background(), f.seller.Id, f.connection.ID, f.session.SID, false)
	require.NoError(t, err)
	authorize, _ := url.Parse(target)
	require.Equal(t, "products.read", authorize.Query().Get("scope"))
	require.Equal(t, "S256", authorize.Query().Get("code_challenge_method"))
	require.Empty(t, authorize.Query().Get("code_verifier"))
	state := authorize.Query().Get("state")
	actual := f.connection.RedirectURI + "?" + url.Values{"code": {"fixture-code"}, "state": {state}, "iss": {f.metadata.Issuer}}.Encode()
	exchanges := 0
	commerceExchange = func(context.Context, commerceimport.Metadata, string, string, string, string) (commerceimport.Tokens, error) {
		exchanges++
		return commerceimport.Tokens{}, nil
	}
	for _, tc := range []struct {
		actor        int
		session, url string
	}{{f.buyer.Id, f.session.SID, actual}, {f.seller.Id, "other-session", actual}, {f.seller.Id, f.session.SID, actual + "&state=" + state}, {f.seller.Id, f.session.SID, strings.Replace(actual, url.QueryEscape(f.metadata.Issuer), url.QueryEscape("https://other.example.com"), 1)}} {
		_, err = CompleteCommerceImportCallback(context.Background(), tc.actor, tc.session, tc.url)
		require.Error(t, err)
	}
	require.Zero(t, exchanges)
	actual = commerceAuthorizeFixture(t, f, false, 900)
	_, err = CompleteCommerceImportCallback(context.Background(), f.seller.Id, f.session.SID, actual)
	require.Error(t, err)
	var stored model.MerchantStoreCommerceConnection
	require.NoError(t, model.DB.First(&stored, "id = ?", f.connection.ID).Error)
	public, _ := json.Marshal(stored)
	require.NotContains(t, string(public), "PRIVATE-")
	require.NotContains(t, stored.TokensCiphertext, "PRIVATE-")
	request := httptest.NewRequest(http.MethodPost, "https://sales.example.com"+CommerceImportCompletePath, nil)
	request.Header.Set("Origin", "https://sales.example.com")
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	request.AddCookie(&http.Cookie{Name: RefreshCookieName, Value: f.session.SID + "." + f.secret})
	actor, sid, err := CommerceImportCallbackIdentity(request)
	require.NoError(t, err)
	require.Equal(t, f.seller.Id, actor)
	require.Equal(t, f.session.SID, sid)
	request.Header.Set("Origin", "https://attacker.example.com")
	_, _, err = CommerceImportCallbackIdentity(request)
	require.Error(t, err)
	request.Header.Set("Origin", "https://sales.example.com")
	require.NoError(t, model.DB.Model(&model.UserSession{}).Where("sid = ?", f.session.SID).Update("status", model.UserSessionStatusRevoked).Error)
	_, _, err = CommerceImportCallbackIdentity(request)
	require.Error(t, err)
}

func TestCommerceImportConfirmedDraftAndExactBodyRecovery(t *testing.T) {
	f := commerceServiceDB(t)
	commerceAuthorizeFixture(t, f, true, 900)
	catalog, err := FetchCommerceImportCatalog(context.Background(), f.seller.Id, f.connection.ID)
	require.NoError(t, err)
	require.Equal(t, f.listing.ShopID, catalog.Shop.ID)
	input := CommerceImportDraftInput{ProductID: f.listing.ID, Revision: f.listing.Revision, Visibility: commerceString("private"), Confirmed: true, Variants: []CommerceImportDraftVariant{{ExternalID: "custom-standard", PriceQuota: commerceInt(500000), Enabled: true}, {ExternalID: "unlimited-name", PriceQuota: commerceInt(700001), Enabled: true}}}
	mapping, err := ImportCommerceImportDraft(context.Background(), f.seller.Id, f.connection.ID, input)
	require.NoError(t, err)
	duplicate, err := ImportCommerceImportDraft(context.Background(), f.seller.Id, f.connection.ID, input)
	require.NoError(t, err)
	require.Equal(t, mapping.LocalProductID, duplicate.LocalProductID)
	product, err := model.GetMerchantStoreProduct(f.seller.Id, mapping.LocalProductID)
	require.NoError(t, err)
	require.Equal(t, "draft", product.Status)
	require.Equal(t, "private", product.Visibility)
	require.Equal(t, 500000, product.PriceQuota)
	require.Equal(t, "Editable service", product.Title)
	require.Zero(t, product.AvailableStock)
	require.NotContains(t, product.Description, "unlimited")
	require.Contains(t, mapping.RawJSON, "retained-original-source")
	var unknown model.MerchantStoreCommerceVariantMapping
	require.NoError(t, model.DB.Where("product_mapping_id = ? AND external_id = ?", mapping.ID, "unlimited-name").First(&unknown).Error)
	require.Nil(t, unknown.ReferencePrice)
	var originalKey, originalBody string
	calls := 0
	commerceCards = func(_ context.Context, _ commerceimport.Metadata, token, key string, body []byte) (commerceimport.CardBatch, error) {
		calls++
		require.Equal(t, "PRIVATE-access-token-0123456789", token)
		var pending model.MerchantStoreCommerceRestockRequest
		require.NoError(t, model.DB.Where("connection_id = ?", f.connection.ID).First(&pending).Error)
		require.NotEmpty(t, pending.BodyCiphertext)
		require.NotContains(t, pending.BodyCiphertext, string(body))
		require.NotContains(t, pending.KeyCiphertext, key)
		if calls == 1 {
			originalKey, originalBody = key, string(body)
			require.Equal(t, "pending", pending.Status)
			return commerceimport.CardBatch{}, commerceimport.ErrNetwork
		}
		require.Equal(t, originalKey, key)
		require.Equal(t, originalBody, string(body))
		raw, _ := json.Marshal(map[string]any{"schema": commerceimport.CardBatchSchema, "grant_id": "approved-grant", "product_id": f.listing.ID, "variant_id": "custom-standard", "batch_id": "issued-batch", "count": 2, "codes": []string{"CONFIDENTIAL-CODE-ONE", "CONFIDENTIAL-CODE-TWO"}, "created_at": time.Now().Unix(), "recovery_expires": time.Now().Unix() + 86400, "quota": map[string]int{"max_count": 9, "issued_count": 2, "remaining": 7}, "variant": f.listing.Variants[0]})
		return commerceimport.ParseCardBatch(raw)
	}
	saved, err := RestockCommerceImport(context.Background(), f.seller.Id, f.connection.ID, CommerceImportRestockInput{ProductID: f.listing.ID, VariantID: "custom-standard", Count: 2, ExpectedRevision: f.listing.Revision, Label: "Merchant batch"})
	require.ErrorIs(t, err, commerceimport.ErrNetwork)
	require.NotNil(t, saved)
	_, err = RestockCommerceImport(context.Background(), f.seller.Id, f.connection.ID, CommerceImportRestockInput{ProductID: f.listing.ID, VariantID: "custom-standard", Count: 2, ExpectedRevision: f.listing.Revision})
	require.Error(t, err)
	require.Equal(t, 1, calls, "unresolved batch cannot create a new issuance key")
	imported, err := RecoverCommerceImportRestock(context.Background(), f.seller.Id, f.connection.ID, saved.ID)
	require.NoError(t, err)
	require.Equal(t, "imported", imported.Status)
	_, err = RecoverCommerceImportRestock(context.Background(), f.seller.Id, f.connection.ID, saved.ID)
	require.NoError(t, err)
	require.Equal(t, 2, calls)
	var stocks []model.MerchantStoreStock
	require.NoError(t, model.DB.Where("product_id = ?", mapping.LocalProductID).Find(&stocks).Error)
	require.Len(t, stocks, 2)
	for _, stock := range stocks {
		require.NotNil(t, stock.VariantID)
		require.Equal(t, imported.VariantID, *stock.VariantID)
		require.Equal(t, "available", stock.State)
		require.NotContains(t, stock.Ciphertext, "CONFIDENTIAL-")
	}
	view, err := model.ListCommerceImportRestockRequests(f.seller.Id, f.connection.ID)
	require.NoError(t, err)
	encoded, _ := json.Marshal(view)
	require.NotContains(t, string(encoded), "CONFIDENTIAL-")
	require.NotContains(t, string(encoded), originalKey)
	require.NotContains(t, string(encoded), originalBody)
	require.NoError(t, DisconnectCommerceImport(context.Background(), f.seller.Id, f.connection.ID))
	var count int64
	require.NoError(t, model.DB.Model(&model.MerchantStoreStock{}).Where("product_id = ?", mapping.LocalProductID).Count(&count).Error)
	require.EqualValues(t, 2, count)
}

func TestCommerceImportPersistentSettingsDisableImportsButPermitTrustedRevocation(t *testing.T) {
	f := commerceServiceDB(t)
	require.NoError(t, os.Unsetenv("MERCHANT_STORE_COMMERCE_IMPORT_ORIGINS"))
	setOptions := func(enabled, origins string) {
		t.Helper()
		require.NoError(t, model.DB.Save(&model.Option{Key: setting.MerchantStoreCommerceImportEnabledOption, Value: enabled}).Error)
		require.NoError(t, model.DB.Save(&model.Option{Key: setting.MerchantStoreCommerceImportTrustedOriginsOption, Value: origins}).Error)
	}
	setOptions("true", `["https://redemption.example.com"]`)
	config := GetCommerceImportConfiguration()
	require.True(t, config.Enabled)
	require.False(t, config.EnvironmentOverride)
	require.Equal(t, []string{f.metadata.Issuer}, config.TrustedOrigins)
	commerceAuthorizeFixture(t, f, false, 900)
	second := f
	var err error
	second.connection, err = CreateCommerceImportConnection(context.Background(), f.seller.Id, f.metadata.Issuer, "another-selected-client")
	require.NoError(t, err)
	commerceAuthorizeFixture(t, second, false, 900)
	revocations := 0
	commerceRevoke = func(_ context.Context, metadata commerceimport.Metadata, clientID, refreshToken string) error {
		revocations++
		require.Equal(t, f.metadata.Issuer, metadata.Issuer)
		require.Equal(t, f.connection.ClientID, clientID)
		require.Equal(t, "PRIVATE-refresh-token-0123456789", refreshToken)
		return commerceimport.ErrNetwork
	}
	setOptions("false", `["https://redemption.example.com"]`)
	require.False(t, GetCommerceImportConfiguration().Enabled)
	_, err = CreateCommerceImportConnection(context.Background(), f.seller.Id, f.metadata.Issuer, "no-new-connection")
	require.ErrorIs(t, err, ErrCommerceImportConfiguration)
	require.NoError(t, DisconnectCommerceImport(context.Background(), f.seller.Id, f.connection.ID))
	require.Equal(t, 1, revocations, "disabling new imports must not strand a still-trusted authorization")
	disconnected, err := model.GetCommerceImportConnection(f.seller.Id, f.connection.ID)
	require.NoError(t, err)
	require.Equal(t, "disconnected", disconnected.Status)
	require.Empty(t, disconnected.TokensCiphertext, "a revoke timeout still clears local credentials")
	setOptions("false", `[]`)
	require.NoError(t, DisconnectCommerceImport(context.Background(), f.seller.Id, second.connection.ID))
	require.Equal(t, 1, revocations, "removed issuers must not receive credentials")
	t.Setenv("MERCHANT_STORE_COMMERCE_IMPORT_ORIGINS", f.metadata.Issuer)
	require.True(t, GetCommerceImportConfiguration().Enabled)
	require.True(t, GetCommerceImportConfiguration().EnvironmentOverride)
	t.Setenv("MERCHANT_STORE_COMMERCE_IMPORT_ORIGINS", "")
	require.False(t, GetCommerceImportConfiguration().Enabled)
}

func TestCommerceImportRefreshTimeoutCannotReuseOldCredential(t *testing.T) {
	f := commerceServiceDB(t)
	commerceAuthorizeFixture(t, f, false, 20)
	calls := 0
	commerceRefresh = func(context.Context, commerceimport.Metadata, string, string) (commerceimport.Tokens, error) {
		calls++
		var connection model.MerchantStoreCommerceConnection
		require.NoError(t, model.DB.First(&connection, "id = ?", f.connection.ID).Error)
		require.Equal(t, "reauthorize", connection.Status)
		require.NotZero(t, connection.RefreshAttemptAt)
		return commerceimport.Tokens{}, commerceimport.ErrNetwork
	}
	_, err := FetchCommerceImportCatalog(context.Background(), f.seller.Id, f.connection.ID)
	require.ErrorIs(t, err, commerceimport.ErrNetwork)
	_, err = FetchCommerceImportCatalog(context.Background(), f.seller.Id, f.connection.ID)
	require.ErrorIs(t, err, model.ErrCommerceImportReauthorize)
	require.Equal(t, 1, calls)
}
