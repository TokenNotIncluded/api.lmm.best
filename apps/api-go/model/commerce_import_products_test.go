package model

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/internal/commerceimport"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type commerceImportInventoryFixture struct {
	seller, other User
	connection    *MerchantStoreCommerceConnection
	lease         CommerceImportLease
}

func commerceImportInventorySetup(t *testing.T, db *gorm.DB) commerceImportInventoryFixture {
	t.Helper()
	require.NoError(t, db.AutoMigrate(MerchantStoreModels()...))
	require.NoError(t, db.Model(&Option{}).Where("key = ?", MerchantStoreWriterCapabilityOption).Update("value", "8").Error)
	t.Setenv("MERCHANT_STORE_ENCRYPTION_KEY", "C5wmMzDh1QsVZb0saEW9ulAPzVN87Boqv3DK6eIrKXc2YLfg")
	f := commerceImportInventoryFixture{seller: marketTestUser(t, db, "import-seller", 10000000, common.RoleCommonUser), other: marketTestUser(t, db, "import-other", 10000000, common.RoleCommonUser)}
	root := marketTestUser(t, db, "import-root", 0, common.RoleRootUser)
	require.NoError(t, SetMerchantStoreConfig(root.Id, MerchantStoreConfig{FeeBPS: 100, RecipientID: root.Id, MinimumUnitPriceQuota: 500000}))
	var err error
	f.connection, err = CreateCommerceImportConnection(f.seller.Id, CommerceImportConnectionInput{Issuer: "https://redeem.example", ClientID: "fixture", RedirectURI: "https://sales.example/api/callback", MetadataJSON: `{}`, MaximumCardsPerRequest: 100})
	require.NoError(t, err)
	session, err := SaveCommerceImportSession(f.seller.Id, f.connection.ID, CommerceImportSessionInput{State: strings.Repeat("s", 32), Verifier: strings.Repeat("v", 43), DashboardSessionID: "ordinary-session", RequestedScope: "products.read cards.issue", ExpiresAt: common.GetTimestamp() + 500})
	require.NoError(t, err)
	_, err = ConsumeCommerceImportSession(f.seller.Id, session.ID, "ordinary-session")
	require.NoError(t, err)
	f.connection, err = CompleteCommerceImportAuthorization(f.seller.Id, session.ID, CommerceImportTokenInput{AccessToken: "synthetic-access", RefreshToken: "synthetic-refresh", Scope: "products.read cards.issue", GrantID: "synthetic-grant", AccessExpiresAt: common.GetTimestamp() + 900, GrantExpiresAt: common.GetTimestamp() + 86400})
	require.NoError(t, err)
	lease, err := AcquireCommerceImportLease(f.seller.Id, f.connection.ID, 300)
	require.NoError(t, err)
	f.lease = *lease
	require.NoError(t, BindCommerceImportShop(f.seller.Id, f.lease, f.connection.GrantID, "fixture-shop", "Fixture"))
	t.Cleanup(func() { _ = ReleaseCommerceImportLease(f.seller.Id, f.lease) })
	return f
}
func commerceImportInventoryDraft() CommerceImportProductInput {
	price := 700000
	visibility := "public"
	standard := 700000
	large := 1000000
	ref := "25.123400"
	return CommerceImportProductInput{ExternalProductID: "document_service", ShopID: "fixture-shop", Revision: strings.Repeat("a", 64), RedemptionURL: "https://redeem.example/", Mode: "manual", RawJSON: `{"original":"retained"}`, PriceQuota: &price, Visibility: &visibility, Product: MerchantStoreProductInput{Title: "Imported documents", Description: "Independent source description"}, Variants: []CommerceImportVariantInput{{ExternalID: "standard", Name: "Standard", ReferencePrice: &ref, Currency: "CNY", RawJSON: `{"id":"standard","price":"25.123400"}`, Enabled: true, PriceQuota: &standard}, {ExternalID: "large", Name: "Large", RawJSON: `{"id":"large","price":null}`, Enabled: true, PriceQuota: &large}}}
}
func commerceImportInventoryRequest(mapped *MerchantStoreCommerceProductMapping, key string) CommerceImportRestockInput {
	body := fmt.Sprintf(`{"product_id":%q,"variant_id":"standard","count":2,"label":"restock","expected_revision":%q}`, mapped.ExternalProductID, mapped.Revision)
	return CommerceImportRestockInput{ExternalProductID: mapped.ExternalProductID, ExternalVariantID: "standard", IdempotencyKey: key, Body: body, Revision: mapped.Revision, Count: 2, Label: "restock"}
}
func commerceImportInventoryBatch(req *MerchantStoreCommerceRestockRequest) CommerceImportBatchInput {
	expires := common.GetTimestamp() + 86400
	in := CommerceImportBatchInput{GrantID: req.GrantID, ExternalProductID: req.ExternalProductID, ExternalVariantID: req.ExternalVariantID, BatchID: "synthetic-batch", Count: 2, Codes: []string{"SYNTHETIC-CARD-ONE", "SYNTHETIC-CARD-TWO"}, RecoveryExpiresAt: expires}
	body := map[string]any{"schema": "extore.card-batch.v1", "grant_id": in.GrantID, "product_id": in.ExternalProductID, "variant_id": in.ExternalVariantID, "batch_id": in.BatchID, "count": in.Count, "codes": in.Codes, "recovery_expires": expires, "quota": map[string]int{"max_count": 5000, "issued_count": 2, "remaining": 4998}, "variant": map[string]any{"id": "standard", "price": "25.123400", "currency": "CNY", "attributes": map[string]int{"revisions": 0}}}
	b, _ := json.Marshal(body)
	in.RawJSON = string(b)
	return in
}

func TestCommerceImportDraftConfirmationStableMappingsAndAccountIsolation(t *testing.T) {
	f := commerceImportInventorySetup(t, marketTestDB(t))
	in := commerceImportInventoryDraft()
	noPrice := in
	noPrice.PriceQuota = nil
	_, err := ImportCommerceImportProduct(f.seller.Id, f.lease, noPrice)
	require.ErrorIs(t, err, ErrMerchantStoreInput)
	noVisibility := in
	noVisibility.Visibility = nil
	_, err = ImportCommerceImportProduct(f.seller.Id, f.lease, noVisibility)
	require.ErrorIs(t, err, ErrMerchantStoreInput)
	_, err = ImportCommerceImportProduct(f.other.Id, f.lease, in)
	require.Error(t, err)
	first, err := ImportCommerceImportProduct(f.seller.Id, f.lease, in)
	require.NoError(t, err)
	require.Len(t, first.Variants, 2)
	var p MerchantStoreProduct
	require.NoError(t, DB.First(&p, "id = ?", first.LocalProductID).Error)
	require.Equal(t, "draft", p.Status)
	require.Equal(t, 700000, p.PriceQuota)
	require.Zero(t, p.ReviewedAt)
	require.Equal(t, "25.123400", *first.Variants[0].ReferencePrice)
	require.Nil(t, first.Variants[1].ReferencePrice)
	require.Equal(t, in.RawJSON, first.RawJSON)
	ids := map[string]string{}
	for _, v := range first.Variants {
		ids[v.ExternalID] = v.LocalVariantID
	}
	require.NoError(t, SetMerchantStoreVariantEnabled(f.seller.Id, first.LocalProductID, ids["large"], false))
	require.NoError(t, DB.Model(&MerchantStoreProduct{}).Where("id = ?", first.LocalProductID).Updates(map[string]any{"status": "published", "reviewed_at": common.GetTimestamp(), "reviewed_by": f.seller.Id}).Error)
	in.Revision = strings.Repeat("b", 64)
	in.Product.Title = "Updated import"
	in.Variants[0], in.Variants[1] = in.Variants[1], in.Variants[0]
	again, err := ImportCommerceImportProduct(f.seller.Id, f.lease, in)
	require.NoError(t, err)
	require.Equal(t, first.LocalProductID, again.LocalProductID)
	require.NoError(t, DB.First(&p, "id = ?", again.LocalProductID).Error)
	require.Equal(t, "draft", p.Status)
	require.Zero(t, p.ReviewedAt)
	require.Zero(t, p.ReviewedBy)
	for _, v := range again.Variants {
		require.Equal(t, ids[v.ExternalID], v.LocalVariantID)
	}
	var disabled MerchantStoreVariant
	require.NoError(t, DB.First(&disabled, "id = ?", ids["large"]).Error)
	require.False(t, disabled.Enabled)
	var count int64
	require.NoError(t, DB.Model(&MerchantStoreProduct{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.NoError(t, DB.Model(&MerchantStoreStock{}).Count(&count).Error)
	require.Zero(t, count)
	_, err = ListCommerceImportMappings(f.other.Id, f.connection.ID)
	require.Error(t, err)
	noSpecPrice := in
	noSpecPrice.Variants = append([]CommerceImportVariantInput(nil), in.Variants...)
	noSpecPrice.Variants[0].PriceQuota = nil
	_, err = ImportCommerceImportProduct(f.seller.Id, f.lease, noSpecPrice)
	require.ErrorIs(t, err, ErrMerchantStoreInput)
}

func TestCommerceImportRequestPersistenceBatchIdempotencyAndOldRevisionRecovery(t *testing.T) {
	f := commerceImportInventorySetup(t, marketTestDB(t))
	in := commerceImportInventoryDraft()
	mapped, err := ImportCommerceImportProduct(f.seller.Id, f.lease, in)
	require.NoError(t, err)
	request := commerceImportInventoryRequest(mapped, "synthetic-request-key")
	row, created, err := PrepareCommerceImportRestock(f.seller.Id, f.lease, request)
	require.NoError(t, err)
	require.True(t, created)
	var stored MerchantStoreCommerceRestockRequest
	require.NoError(t, DB.First(&stored, "id = ?", row.ID).Error)
	require.Equal(t, "pending", stored.Status)
	require.Empty(t, stored.Body)
	require.Empty(t, stored.IdempotencyKey)
	require.NotContains(t, stored.BodyCiphertext, "document_service")
	require.NotContains(t, stored.KeyCiphertext, request.IdempotencyKey)
	replay, newRequest, err := PrepareCommerceImportRestock(f.seller.Id, f.lease, request)
	require.NoError(t, err)
	require.False(t, newRequest)
	require.Equal(t, row.ID, replay.ID)
	require.Equal(t, request.Body, replay.Body)
	changed := request
	changed.Body = strings.ReplaceAll(request.Body, "restock", "different")
	changed.Label = "different"
	_, _, err = PrepareCommerceImportRestock(f.seller.Id, f.lease, changed)
	require.ErrorIs(t, err, ErrMerchantStoreConflict)
	newKey := request
	newKey.IdempotencyKey = "another-request-key"
	_, _, err = PrepareCommerceImportRestock(f.seller.Id, f.lease, newKey)
	require.ErrorIs(t, err, ErrMerchantStoreConflict, "unresolved issuance cannot be replaced with a new key")
	in.Revision = strings.Repeat("c", 64)
	_, err = ImportCommerceImportProduct(f.seller.Id, f.lease, in)
	require.NoError(t, err)
	_, created, err = PrepareCommerceImportRestock(f.seller.Id, f.lease, request)
	require.NoError(t, err)
	require.False(t, created, "same original request may recover after catalog changed")
	batch := commerceImportInventoryBatch(row)
	wrong := batch
	wrong.ExternalVariantID = "large"
	_, _, err = ReceiveCommerceImportBatch(f.seller.Id, f.lease, row.ID, wrong)
	require.Error(t, err)
	result, imported, err := ReceiveCommerceImportBatch(f.seller.Id, f.lease, row.ID, batch)
	require.NoError(t, err)
	require.True(t, imported)
	require.Equal(t, "imported", result.Status)
	result, imported, err = ReceiveCommerceImportBatch(f.seller.Id, f.lease, row.ID, batch)
	require.NoError(t, err)
	require.False(t, imported)
	require.Equal(t, "imported", result.Status)
	var stocks []MerchantStoreStock
	require.NoError(t, DB.Where("product_id = ?", mapped.LocalProductID).Find(&stocks).Error)
	require.Len(t, stocks, 2, "quota remaining never creates stock")
	for _, stock := range stocks {
		require.NotContains(t, stock.Ciphertext, "SYNTHETIC-CARD")
		require.Equal(t, row.VariantID, *stock.VariantID)
	}
	response, err := LoadCommerceImportBatchResponse(f.seller.Id, f.lease, row.ID)
	require.NoError(t, err)
	require.Equal(t, batch.RawJSON, response)
	for _, value := range []any{result, batch, request, f.connection, CommerceImportSessionInput{State: "SECRET-STATE", Verifier: "SECRET-VERIFIER"}, CommerceImportTokenInput{AccessToken: "SECRET-ACCESS"}} {
		encoded, err := json.Marshal(value)
		require.NoError(t, err)
		logged := fmt.Sprintf("%v %+v %#v", value, value, value)
		for _, secret := range []string{"SYNTHETIC-CARD", "synthetic-request-key", "SECRET-STATE", "SECRET-VERIFIER", "SECRET-ACCESS", "synthetic-access", "synthetic-refresh"} {
			require.NotContains(t, string(encoded), secret)
			require.NotContains(t, logged, secret)
		}
	}
}

func TestCommerceImportFailedStockReceiptRetainedAndDisconnectKeepsInventory(t *testing.T) {
	f := commerceImportInventorySetup(t, marketTestDB(t))
	mapped, err := ImportCommerceImportProduct(f.seller.Id, f.lease, commerceImportInventoryDraft())
	require.NoError(t, err)
	row, _, err := PrepareCommerceImportRestock(f.seller.Id, f.lease, commerceImportInventoryRequest(mapped, "failed-stock-request"))
	require.NoError(t, err)
	require.NoError(t, DB.Model(&MerchantStoreVariant{}).Where("id = ?", row.VariantID).Update("enabled", false).Error)
	batch := commerceImportInventoryBatch(row)
	_, _, err = ReceiveCommerceImportBatch(f.seller.Id, f.lease, row.ID, batch)
	require.ErrorIs(t, err, ErrMerchantStoreUnavailable)
	var receipt MerchantStoreCommerceRestockRequest
	require.NoError(t, DB.First(&receipt, "id = ?", row.ID).Error)
	require.Equal(t, "received", receipt.Status)
	require.NotEmpty(t, receipt.ResponseCiphertext)
	require.NotContains(t, receipt.ResponseCiphertext, "SYNTHETIC-CARD")
	var count int64
	require.NoError(t, DB.Model(&MerchantStoreStock{}).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, DB.Model(&MerchantStoreCommerceCardBatch{}).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, SetCommerceImportRestockRecovery(f.seller.Id, f.lease, row.ID, "local_stock_failed"))
	response, err := LoadCommerceImportBatchResponse(f.seller.Id, f.lease, row.ID)
	require.NoError(t, err)
	require.Equal(t, batch.RawJSON, response)
	require.NoError(t, DB.Model(&MerchantStoreVariant{}).Where("id = ?", row.VariantID).Update("enabled", true).Error)
	_, made, err := ReceiveCommerceImportBatch(f.seller.Id, f.lease, row.ID, batch)
	require.NoError(t, err)
	require.True(t, made)
	require.NoError(t, DisconnectCommerceImportConnection(f.seller.Id, f.lease))
	require.NoError(t, DB.Model(&MerchantStoreStock{}).Count(&count).Error)
	require.EqualValues(t, 2, count)
	require.NoError(t, DB.First(&receipt, "id = ?", row.ID).Error)
	require.Empty(t, receipt.ResponseCiphertext)
	require.Equal(t, "imported", receipt.Status)
	require.NoError(t, DB.Model(&MerchantStoreCommerceCardBatch{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestCommerceImportAttemptMarkersSafeRejectionsAndCrossConnectionBlocker(t *testing.T) {
	f := commerceImportInventorySetup(t, marketTestDB(t))
	draft := commerceImportInventoryDraft()
	mapped, err := ImportCommerceImportProduct(f.seller.Id, f.lease, draft)
	require.NoError(t, err)
	input := commerceImportInventoryRequest(mapped, "safe-rejection-key")
	first, _, err := PrepareCommerceImportRestock(f.seller.Id, f.lease, input)
	require.NoError(t, err)
	attempt, err := BeginCommerceImportIssue(f.seller.Id, f.lease, first.ID)
	require.NoError(t, err)
	require.Equal(t, 1, attempt.IssueAttempts)
	require.True(t, attempt.IssuanceUncertain)
	require.NoError(t, SetCommerceImportRestockRecovery(f.seller.Id, f.lease, first.ID, "quota_exceeded"))
	first, err = GetCommerceImportRestockRequest(f.seller.Id, f.connection.ID, first.ID)
	require.NoError(t, err)
	require.Equal(t, "manual_recovery", first.Status)
	require.False(t, first.IssuanceUncertain)
	input.IdempotencyKey = "new-confirmed-batch-key"
	second, created, err := PrepareCommerceImportRestock(f.seller.Id, f.lease, input)
	require.NoError(t, err)
	require.True(t, created, "a definitive first-attempt rejection permits a merchant-confirmed new batch")
	attempt, err = BeginCommerceImportIssue(f.seller.Id, f.lease, second.ID)
	require.NoError(t, err)
	require.Equal(t, 1, attempt.IssueAttempts)
	require.NoError(t, SetCommerceImportRestockRecovery(f.seller.Id, f.lease, second.ID, "network_error"))
	attempt, err = BeginCommerceImportIssue(f.seller.Id, f.lease, second.ID)
	require.NoError(t, err)
	require.Equal(t, 2, attempt.IssueAttempts)
	require.NoError(t, SetCommerceImportRestockRecovery(f.seller.Id, f.lease, second.ID, "invalid_grant"))
	second, err = GetCommerceImportRestockRequest(f.seller.Id, f.connection.ID, second.ID)
	require.NoError(t, err)
	require.True(t, second.IssuanceUncertain, "recovery refusal does not prove the first attempt issued nothing")
	input.IdempotencyKey = "unsafe-new-batch-key"
	_, _, err = PrepareCommerceImportRestock(f.seller.Id, f.lease, input)
	require.ErrorIs(t, err, ErrMerchantStoreConflict)
	// A new connection can import the stable same mapping, but cannot bypass
	// that mapping's uncertain stock request by selecting a different grant.
	connection, err := CreateCommerceImportConnection(f.seller.Id, CommerceImportConnectionInput{Issuer: f.connection.Issuer, ClientID: "new-client", RedirectURI: f.connection.RedirectURI, MetadataJSON: `{}`, MaximumCardsPerRequest: 100})
	require.NoError(t, err)
	require.NoError(t, DB.Model(connection).Updates(map[string]any{"status": "active", "grant_id": "different-grant", "grant_expires_at": common.GetTimestamp() + 86400, "scope": "products.read cards.issue", "shop_id": "fixture-shop"}).Error)
	lease, err := AcquireCommerceImportLease(f.seller.Id, connection.ID, 300)
	require.NoError(t, err)
	defer ReleaseCommerceImportLease(f.seller.Id, *lease)
	newMapping, err := ImportCommerceImportProduct(f.seller.Id, *lease, draft)
	require.NoError(t, err)
	require.Equal(t, mapped.LocalProductID, newMapping.LocalProductID)
	_, _, err = PrepareCommerceImportRestock(f.seller.Id, *lease, input)
	require.ErrorIs(t, err, ErrMerchantStoreConflict, "an unresolved mapped SKU blocks issuance across connections")
}

func TestCommerceImportExpiryCleansReceiptsAndOriginalGrantSecretsWithoutStockDeletion(t *testing.T) {
	f := commerceImportInventorySetup(t, marketTestDB(t))
	mapped, err := ImportCommerceImportProduct(f.seller.Id, f.lease, commerceImportInventoryDraft())
	require.NoError(t, err)
	input := commerceImportInventoryRequest(mapped, "imported-expiry-key")
	first, _, err := PrepareCommerceImportRestock(f.seller.Id, f.lease, input)
	require.NoError(t, err)
	batch := commerceImportInventoryBatch(first)
	_, _, err = ReceiveCommerceImportBatch(f.seller.Id, f.lease, first.ID, batch)
	require.NoError(t, err)
	input.IdempotencyKey = "failed-expiry-key"
	second, _, err := PrepareCommerceImportRestock(f.seller.Id, f.lease, input)
	require.NoError(t, err)
	require.NoError(t, DB.Model(&MerchantStoreVariant{}).Where("id = ?", second.VariantID).Update("enabled", false).Error)
	failed := commerceImportInventoryBatch(second)
	failed.BatchID = "failed-synthetic-batch"
	var raw map[string]any
	require.NoError(t, json.Unmarshal([]byte(failed.RawJSON), &raw))
	raw["batch_id"] = failed.BatchID
	b, _ := json.Marshal(raw)
	failed.RawJSON = string(b)
	_, _, err = ReceiveCommerceImportBatch(f.seller.Id, f.lease, second.ID, failed)
	require.ErrorIs(t, err, ErrMerchantStoreUnavailable)
	now := common.GetTimestamp()
	require.NoError(t, DB.Model(&MerchantStoreCommerceRestockRequest{}).Where("id IN ?", []string{first.ID, second.ID}).Update("recovery_expires_at", now-1).Error)
	require.NoError(t, DB.Model(&MerchantStoreCommerceCardBatch{}).Where("request_id = ?", first.ID).Update("recovery_expires_at", now-1).Error)
	require.NoError(t, ClearExpiredCommerceImportSecrets(1))
	var remaining int64
	require.NoError(t, DB.Model(&MerchantStoreCommerceRestockRequest{}).Where("response_ciphertext <> ''").Count(&remaining).Error)
	require.EqualValues(t, 1, remaining, "per-class cleanup is bounded")
	require.NoError(t, ClearExpiredCommerceImportSecrets(1))
	stored, err := GetCommerceImportRestockRequest(f.seller.Id, f.connection.ID, second.ID)
	require.NoError(t, err)
	require.Equal(t, "manual_recovery", stored.Status)
	require.Equal(t, "issuance_expired", stored.ErrorCode)
	require.Empty(t, stored.ResponseCiphertext)
	_, err = LoadCommerceImportBatchResponse(f.seller.Id, f.lease, second.ID)
	require.Error(t, err, "an expired encrypted receipt never causes a fresh network issuance")
	// Renewal changes the connection's current deadline but leaves each
	// request/batch's original deadline intact for its own retention cutoff.
	originalDeadline := now - 8*86400
	require.NoError(t, DB.Model(&MerchantStoreCommerceRestockRequest{}).Where("id IN ?", []string{first.ID, second.ID}).Update("grant_expires_at", originalDeadline).Error)
	require.NoError(t, DB.Model(&MerchantStoreCommerceCardBatch{}).Where("request_id = ?", first.ID).Update("grant_expires_at", originalDeadline).Error)
	require.NoError(t, DB.Model(&MerchantStoreCommerceConnection{}).Where("id = ?", f.connection.ID).Update("grant_expires_at", now+30*86400).Error)
	require.NoError(t, ClearExpiredCommerceImportSecrets(200))
	var requests []MerchantStoreCommerceRestockRequest
	require.NoError(t, DB.Find(&requests).Error)
	require.Len(t, requests, 1, "known imported tombstone expires; unknown issuance remains as an audit")
	require.Equal(t, second.ID, requests[0].ID)
	require.Empty(t, requests[0].BodyCiphertext)
	require.Empty(t, requests[0].KeyCiphertext)
	require.True(t, requests[0].IssuanceUncertain)
	var stocks []MerchantStoreStock
	require.NoError(t, DB.Find(&stocks).Error)
	require.Len(t, stocks, 2, "receipt and tombstone cleanup never removes merchant inventory")
	connection, err := GetCommerceImportConnection(f.seller.Id, f.connection.ID)
	require.NoError(t, err)
	require.NotEmpty(t, connection.TokensCiphertext, "old request expiry cannot clear the renewed connection")
}

func TestCommerceImportForeignVariantMappingCannotReceiveStock(t *testing.T) {
	f := commerceImportInventorySetup(t, marketTestDB(t))
	mapped, err := ImportCommerceImportProduct(f.seller.Id, f.lease, commerceImportInventoryDraft())
	require.NoError(t, err)
	foreign, err := SaveMerchantStoreProduct(f.other.Id, "", MerchantStoreProductInput{Title: "Another merchant's stock", PriceQuota: 500000})
	require.NoError(t, err)
	input := commerceImportInventoryRequest(mapped, "foreign-variant-request")
	request, _, err := PrepareCommerceImportRestock(f.seller.Id, f.lease, input)
	require.NoError(t, err)
	require.NoError(t, DB.Model(&MerchantStoreCommerceVariantMapping{}).Where("id = ?", request.VariantMappingID).Update("local_variant_id", MerchantStoreDefaultVariantID(foreign.ID)).Error)
	_, _, err = ReceiveCommerceImportBatch(f.seller.Id, f.lease, request.ID, commerceImportInventoryBatch(request))
	require.Error(t, err)
	var count int64
	require.NoError(t, DB.Model(&MerchantStoreStock{}).Count(&count).Error)
	require.Zero(t, count)
	var stored MerchantStoreCommerceRestockRequest
	require.NoError(t, DB.First(&stored, "id = ?", request.ID).Error)
	require.Equal(t, "received", stored.Status, "invalid association preserves the encrypted receipt for manual recovery")
}

func TestCommerceImportMaintenanceDoesNotReactivateConcurrentDisconnect(t *testing.T) {
	f := commerceImportInventorySetup(t, marketTestDB(t))
	require.NoError(t, DB.Model(&MerchantStoreCommerceConnection{}).Where("id = ?", f.connection.ID).Updates(map[string]any{"grant_expires_at": common.GetTimestamp() - 1, "lease_expires_at": 0, "lease_owner": ""}).Error)
	interleaved := false
	callback := "test-commerce-maintenance-disconnect"
	require.NoError(t, DB.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
		if interleaved || tx.Statement.Schema == nil || tx.Statement.Schema.Table != "merchant_store_commerce_connections" {
			return
		}
		changes, ok := tx.Statement.Dest.(map[string]interface{})
		if !ok || changes["status"] != "reauthorize" {
			return
		}
		interleaved = true
		// Simulate a disconnect committing after expiry selection and before
		// the cleanup's conditional UPDATE. Use the same physical test tx.
		tx.AddError(tx.Session(&gorm.Session{NewDB: true}).Exec("UPDATE merchant_store_commerce_connections SET status = ?, grant_expires_at = 0, tokens_ciphertext = '' WHERE id = ?", "disconnected", f.connection.ID).Error)
	}))
	t.Cleanup(func() { _ = DB.Callback().Update().Remove(callback) })
	require.NoError(t, ClearExpiredCommerceImportSecrets(200))
	require.True(t, interleaved)
	connection, err := GetCommerceImportConnection(f.seller.Id, f.connection.ID)
	require.NoError(t, err)
	require.Equal(t, "disconnected", connection.Status)
	require.Empty(t, connection.TokensCiphertext)
}

func TestCommerceImportCaseSensitiveBatchExtensionsRetainReceiptAndRecoverStock(t *testing.T) {
	f := commerceImportInventorySetup(t, marketTestDB(t))
	mapped, err := ImportCommerceImportProduct(f.seller.Id, f.lease, commerceImportInventoryDraft())
	require.NoError(t, err)
	request, _, err := PrepareCommerceImportRestock(f.seller.Id, f.lease, commerceImportInventoryRequest(mapped, "case-extension-request"))
	require.NoError(t, err)
	batch := commerceImportInventoryBatch(request)
	var original map[string]any
	require.NoError(t, json.Unmarshal([]byte(batch.RawJSON), &original))
	original["created_at"] = batch.RecoveryExpiresAt - 86400
	original["variant"].(map[string]any)["enabled"] = true
	canonical, err := json.Marshal(original)
	require.NoError(t, err)
	// Put extensions after canonical fields to reproduce Go's former
	// case-insensitive overwrite, rather than relying on map iteration.
	batch.RawJSON = strings.TrimSuffix(string(canonical), "}") + `,"CODES":["UPPERCASE-EXTENSION-FAKE-CODE"],"COUNT":99,"GRANT_ID":"other-grant","PRODUCT_ID":"other-product","VARIANT_ID":"other-variant","BATCH_ID":"other-batch","SCHEMA":"extension-only","RECOVERY_EXPIRES":1,"VARIANT":{"id":"other-variant","enabled":false},"CoDeS":["MIXED-CASE-EXTENSION-FAKE-CODE"],"Variant_Id":"mixed-extension-variant"}`
	parsed, err := commerceimport.ParseCardBatch([]byte(batch.RawJSON))
	require.NoError(t, err, "the protocol permits case-sensitive additive response extensions")
	require.Equal(t, batch.Codes, parsed.Codes)
	require.Equal(t, batch.Count, parsed.Count)
	require.Equal(t, batch.ExternalVariantID, parsed.Variant.ID)
	input := CommerceImportBatchInput{GrantID: parsed.GrantID, ExternalProductID: parsed.ProductID, ExternalVariantID: parsed.VariantID, BatchID: parsed.BatchID, Count: parsed.Count, Codes: parsed.Codes, RawJSON: string(parsed.Raw), RecoveryExpiresAt: parsed.RecoveryExpires}
	missing := input
	var missingCanonical map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(input.RawJSON), &missingCanonical))
	delete(missingCanonical, "codes")
	delete(missingCanonical, "CODES")
	missingCanonical["CoDeS"], err = json.Marshal(input.Codes)
	require.NoError(t, err)
	missingRaw, err := json.Marshal(missingCanonical)
	require.NoError(t, err)
	missing.RawJSON = string(missingRaw)
	require.False(t, commerceImportValidateBatch(missing), "an extension cannot supply a missing canonical field")
	require.NoError(t, DB.Model(&MerchantStoreVariant{}).Where("id = ?", request.VariantID).Update("enabled", false).Error)
	_, _, err = ReceiveCommerceImportBatch(f.seller.Id, f.lease, request.ID, input)
	require.ErrorIs(t, err, ErrMerchantStoreUnavailable)
	var stored MerchantStoreCommerceRestockRequest
	require.NoError(t, DB.First(&stored, "id = ?", request.ID).Error)
	require.Equal(t, "received", stored.Status)
	require.NotEmpty(t, stored.ResponseCiphertext, "valid extended responses survive an inventory failure")
	require.NotContains(t, stored.ResponseCiphertext, batch.Codes[0])
	receipt, err := LoadCommerceImportBatchResponse(f.seller.Id, f.lease, request.ID)
	require.NoError(t, err)
	require.Equal(t, batch.RawJSON, receipt, "retain the complete response including every extension")
	recovered, err := commerceimport.ParseCardBatch([]byte(receipt))
	require.NoError(t, err)
	input = CommerceImportBatchInput{GrantID: recovered.GrantID, ExternalProductID: recovered.ProductID, ExternalVariantID: recovered.VariantID, BatchID: recovered.BatchID, Count: recovered.Count, Codes: recovered.Codes, RawJSON: string(recovered.Raw), RecoveryExpiresAt: recovered.RecoveryExpires}
	require.NoError(t, DB.Model(&MerchantStoreVariant{}).Where("id = ?", request.VariantID).Update("enabled", true).Error)
	result, created, err := ReceiveCommerceImportBatch(f.seller.Id, f.lease, request.ID, input)
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, "imported", result.Status)
	firstCiphertext := result.ResponseCiphertext
	var firstBatch MerchantStoreCommerceCardBatch
	require.NoError(t, DB.First(&firstBatch, "request_id = ?", request.ID).Error)
	// Reformat and extend the same successful batch. Neither byte changes
	// nor unknown fields authorize additional cards or replace the receipt.
	var extended map[string]any
	require.NoError(t, json.Unmarshal([]byte(input.RawJSON), &extended))
	extended["CODES"] = []string{"ANOTHER-UNKNOWN-EXTENSION-CODE"}
	extended["future_extension"] = map[string]any{"count": 999, "scope": "extra"}
	indented, err := json.MarshalIndent(extended, "", "  ")
	require.NoError(t, err)
	parsedReplay, err := commerceimport.ParseCardBatch(indented)
	require.NoError(t, err)
	reformatted := CommerceImportBatchInput{GrantID: parsedReplay.GrantID, ExternalProductID: parsedReplay.ProductID, ExternalVariantID: parsedReplay.VariantID, BatchID: parsedReplay.BatchID, Count: parsedReplay.Count, Codes: parsedReplay.Codes, RawJSON: string(parsedReplay.Raw), RecoveryExpiresAt: parsedReplay.RecoveryExpires}
	replayed, created, err := ReceiveCommerceImportBatch(f.seller.Id, f.lease, request.ID, reformatted)
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, firstCiphertext, replayed.ResponseCiphertext)
	var replayedBatch MerchantStoreCommerceCardBatch
	require.NoError(t, DB.First(&replayedBatch, "request_id = ?", request.ID).Error)
	require.Equal(t, firstBatch.ResponseCiphertext, replayedBatch.ResponseCiphertext)
	receipt, err = LoadCommerceImportBatchResponse(f.seller.Id, f.lease, request.ID)
	require.NoError(t, err)
	require.Equal(t, batch.RawJSON, receipt, "byte-different replay keeps the first complete encrypted response")
	// A known field changing is an identity conflict even with the same
	// stable batch ID; it cannot introduce another set of codes.
	changed := input
	changed.Codes = []string{"CHANGED-CANONICAL-CARD-ONE", "CHANGED-CANONICAL-CARD-TWO"}
	extended["codes"] = changed.Codes
	changedRaw, err := json.Marshal(extended)
	require.NoError(t, err)
	changed.RawJSON = string(changedRaw)
	_, _, err = ReceiveCommerceImportBatch(f.seller.Id, f.lease, request.ID, changed)
	require.ErrorIs(t, err, ErrMerchantStoreConflict)
	_, created, err = ReceiveCommerceImportBatch(f.seller.Id, f.lease, request.ID, input)
	require.NoError(t, err)
	require.False(t, created)
	var stock []MerchantStoreStock
	require.NoError(t, DB.Where("product_id = ?", mapped.LocalProductID).Find(&stock).Error)
	require.Len(t, stock, 2)
	codes := make([]string, len(stock))
	for i, item := range stock {
		codes[i], err = storeDecrypt("stock", item.ProductID+":"+item.ID, item.Ciphertext)
		require.NoError(t, err)
	}
	require.ElementsMatch(t, batch.Codes, codes, "stock uses only canonical codes, never extension values")
}
