package model

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func commerceConnectionFixture(t *testing.T) (storeFixture, *MerchantStoreCommerceConnection) {
	t.Helper()
	f := newStoreFixture(t, "balance")
	storeActivateFixedTest(t, DB)
	require.NoError(t, ActivateMerchantStoreCommerceImport(DB, 7))
	connection, err := CreateCommerceImportConnection(f.seller.Id, CommerceImportConnectionInput{Issuer: "https://issuer.example.com", ClientID: "merchant-client", RedirectURI: "https://merchant.example.com/callback", MetadataJSON: `{"issuer":"https://issuer.example.com"}`, MaximumCardsPerRequest: 100})
	require.NoError(t, err)
	return f, connection
}

func commerceConnectionSession(t *testing.T, actor int, connectionID, suffix, scope string) (*MerchantStoreCommerceSession, CommerceImportSessionInput) {
	t.Helper()
	in := CommerceImportSessionInput{State: "session-state-" + suffix + strings.Repeat("s", 32), Verifier: strings.Repeat("v", 43), DashboardSessionID: "dashboard-" + suffix, RequestedScope: scope, ExpiresAt: common.GetTimestamp() + 600}
	session, err := SaveCommerceImportSession(actor, connectionID, in)
	require.NoError(t, err)
	return session, in
}

func commerceConnectionTokens(scope string) CommerceImportTokenInput {
	now := common.GetTimestamp()
	return CommerceImportTokenInput{AccessToken: "PRIVATE-ACCESS-TOKEN", RefreshToken: "PRIVATE-REFRESH-TOKEN", Scope: scope, GrantID: "grant-first", AccessExpiresAt: now + 300, GrantExpiresAt: now + 3600}
}

func commerceConnectionAuthorize(t *testing.T, actor int, connectionID string) CommerceImportTokenInput {
	t.Helper()
	session, in := commerceConnectionSession(t, actor, connectionID, "authorize", "products.read cards.issue")
	_, err := ConsumeCommerceImportSession(actor, session.ID, in.DashboardSessionID)
	require.NoError(t, err)
	tokens := commerceConnectionTokens("products.read cards.issue")
	_, err = CompleteCommerceImportAuthorization(actor, session.ID, tokens)
	require.NoError(t, err)
	return tokens
}

func TestCommerceImportConnectionAccountAndCapabilityIsolation(t *testing.T) {
	f, connection := commerceConnectionFixture(t)
	list, err := ListCommerceImportConnections(f.seller.Id)
	require.NoError(t, err)
	require.Len(t, list, 1)
	list, err = ListCommerceImportConnections(f.buyer.Id)
	require.NoError(t, err)
	require.Empty(t, list)
	_, err = GetCommerceImportConnection(f.buyer.Id, connection.ID)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	_, err = AcquireCommerceImportLease(f.buyer.Id, connection.ID, 30)
	require.ErrorIs(t, err, ErrCommerceImportLease)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", f.seller.Id).Update("status", common.UserStatusDisabled).Error)
	_, err = ListCommerceImportConnections(f.seller.Id)
	require.ErrorIs(t, err, ErrMerchantStoreDenied)
	_, err = AcquireCommerceImportLease(f.seller.Id, connection.ID, 30)
	require.ErrorIs(t, err, ErrMerchantStoreDenied)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", f.seller.Id).Update("status", common.UserStatusEnabled).Error)
	require.NoError(t, DB.Model(&Option{}).Where("key = ?", MerchantStoreWriterCapabilityOption).Update("value", "7").Error)
	_, err = ListCommerceImportConnections(f.seller.Id)
	require.ErrorIs(t, err, ErrMerchantStoreWriterFrozen)
	_, err = AcquireCommerceImportLease(f.seller.Id, connection.ID, 30)
	require.ErrorIs(t, err, ErrMerchantStoreWriterFrozen)
	_, err = SaveCommerceImportSession(f.seller.Id, connection.ID, CommerceImportSessionInput{State: strings.Repeat("s", 32), Verifier: strings.Repeat("v", 43), DashboardSessionID: "dashboard", RequestedScope: "products.read", ExpiresAt: common.GetTimestamp() + 600})
	require.ErrorIs(t, err, ErrMerchantStoreWriterFrozen)
}

func TestCommerceImportConnectionLimitIsSellerScopedAndDisconnectReleasesSlot(t *testing.T) {
	f, connection := commerceConnectionFixture(t)
	in := CommerceImportConnectionInput{Issuer: connection.Issuer, ClientID: connection.ClientID, RedirectURI: connection.RedirectURI, MetadataJSON: connection.MetadataJSON, MaximumCardsPerRequest: connection.MaximumCardsPerRequest}
	for i := 1; i < 49; i++ {
		_, err := CreateCommerceImportConnection(f.seller.Id, in)
		require.NoError(t, err)
	}
	// All three live states consume the same limit, including authorizations
	// awaiting completion and connections that need another authorization.
	require.NoError(t, DB.Model(&MerchantStoreCommerceConnection{}).Where("id = ?", connection.ID).Update("status", "active").Error)
	var pending MerchantStoreCommerceConnection
	require.NoError(t, DB.Where("seller_id = ? AND status = ?", f.seller.Id, "pending").First(&pending).Error)
	require.NoError(t, DB.Model(&pending).Update("status", "reauthorize").Error)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := CreateCommerceImportConnection(f.seller.Id, in)
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		} else {
			require.ErrorIs(t, err, ErrMerchantStoreInput)
		}
	}
	require.Equal(t, 1, winners)
	list, err := ListCommerceImportConnections(f.seller.Id)
	require.NoError(t, err)
	require.Len(t, list, 50)
	_, err = CreateCommerceImportConnection(f.seller.Id, in)
	require.ErrorIs(t, err, ErrMerchantStoreInput)
	other, err := CreateCommerceImportConnection(f.buyer.Id, in)
	require.NoError(t, err)
	require.Equal(t, f.buyer.Id, other.SellerID)
	lease, err := AcquireCommerceImportLease(f.seller.Id, connection.ID, 90)
	require.NoError(t, err)
	require.NoError(t, DisconnectCommerceImportConnection(f.seller.Id, *lease))
	require.NoError(t, ReleaseCommerceImportLease(f.seller.Id, *lease))
	_, err = CreateCommerceImportConnection(f.seller.Id, in)
	require.NoError(t, err)
	_, err = CreateCommerceImportConnection(f.seller.Id, in)
	require.ErrorIs(t, err, ErrMerchantStoreInput)
	// Reauthorizing an old disconnected connection must also consume a slot;
	// otherwise disconnecting history could bypass the creation limit.
	session, sessionInput := commerceConnectionSession(t, f.seller.Id, connection.ID, "limit-reconnect", "products.read")
	_, err = ConsumeCommerceImportSession(f.seller.Id, session.ID, sessionInput.DashboardSessionID)
	require.NoError(t, err)
	_, err = CompleteCommerceImportAuthorization(f.seller.Id, session.ID, commerceConnectionTokens("products.read"))
	require.ErrorIs(t, err, ErrMerchantStoreInput)
	current, err := GetCommerceImportConnection(f.seller.Id, connection.ID)
	require.NoError(t, err)
	require.Equal(t, "disconnected", current.Status)
	lease, err = AcquireCommerceImportLease(f.seller.Id, pending.ID, 90)
	require.NoError(t, err)
	require.NoError(t, DisconnectCommerceImportConnection(f.seller.Id, *lease))
	require.NoError(t, ReleaseCommerceImportLease(f.seller.Id, *lease))
	current, err = CompleteCommerceImportAuthorization(f.seller.Id, session.ID, commerceConnectionTokens("products.read"))
	require.NoError(t, err)
	require.Equal(t, "active", current.Status)
	_, err = CreateCommerceImportConnection(f.seller.Id, in)
	require.ErrorIs(t, err, ErrMerchantStoreInput)
	list, err = ListCommerceImportConnections(f.seller.Id)
	require.NoError(t, err)
	require.Len(t, list, 51, "disconnected history remains visible without consuming a live connection slot")
}

func TestCommerceImportSessionEncryptedBoundSingleUseAndNoScopeUpgrade(t *testing.T) {
	f, connection := commerceConnectionFixture(t)
	session, in := commerceConnectionSession(t, f.seller.Id, connection.ID, "single-use", "products.read")
	for _, secret := range []string{in.State, in.Verifier, in.DashboardSessionID} {
		require.NotContains(t, session.SecretsCiphertext, secret)
	}
	plain, err := storeDecrypt("commerce-session", session.ID, session.SecretsCiphertext)
	require.NoError(t, err)
	require.Contains(t, plain, in.Verifier)
	_, err = storeDecrypt("commerce-session", "different-session", session.SecretsCiphertext)
	require.Error(t, err)
	encoded, err := json.Marshal(session)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "ciphertext")
	_, err = GetCommerceImportSession(f.buyer.Id, in.State, in.DashboardSessionID)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	_, err = GetCommerceImportSession(f.seller.Id, in.State, "wrong-dashboard")
	require.ErrorIs(t, err, ErrMerchantStoreDenied)
	_, err = ConsumeCommerceImportSession(f.seller.Id, session.ID, "wrong-dashboard")
	require.ErrorIs(t, err, ErrMerchantStoreDenied)
	_, err = CompleteCommerceImportAuthorization(f.seller.Id, session.ID, commerceConnectionTokens("products.read"))
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := ConsumeCommerceImportSession(f.seller.Id, session.ID, in.DashboardSessionID)
			results <- e
		}()
	}
	wg.Wait()
	close(results)
	winners := 0
	for e := range results {
		if e == nil {
			winners++
		} else {
			require.ErrorIs(t, e, gorm.ErrRecordNotFound)
		}
	}
	require.Equal(t, 1, winners)
	_, err = GetCommerceImportSession(f.seller.Id, in.State, in.DashboardSessionID)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	_, err = CompleteCommerceImportAuthorization(f.seller.Id, session.ID, commerceConnectionTokens("products.read cards.issue"))
	require.ErrorIs(t, err, ErrMerchantStoreDenied)
	current, err := CompleteCommerceImportAuthorization(f.seller.Id, session.ID, commerceConnectionTokens("products.read"))
	require.NoError(t, err)
	require.Equal(t, "active", current.Status)
	require.Equal(t, int64(1), current.TokenVersion)
	require.NotContains(t, current.TokensCiphertext, "PRIVATE-ACCESS")
	require.NoError(t, DB.First(session, "id = ?", session.ID).Error)
	require.Equal(t, "completed", session.Status)
	require.Empty(t, session.SecretsCiphertext)
	encoded, err = json.Marshal(current)
	require.NoError(t, err)
	for _, private := range []string{"ciphertext", "PRIVATE", "seller_id", "lease_owner", "metadata_json", "auth_session_id"} {
		require.NotContains(t, string(encoded), private)
	}
	lease, err := AcquireCommerceImportLease(f.seller.Id, connection.ID, 90)
	require.NoError(t, err)
	tokens, err := LoadCommerceImportTokens(f.seller.Id, *lease)
	require.NoError(t, err)
	require.Equal(t, "PRIVATE-ACCESS-TOKEN", tokens.AccessToken)
	require.NoError(t, BindCommerceImportShop(f.seller.Id, *lease, tokens.GrantID, "shop-first", "First shop"))
	require.ErrorIs(t, BindCommerceImportShop(f.seller.Id, *lease, tokens.GrantID, "another-shop", "Another"), ErrMerchantStoreDenied)
	require.NoError(t, ReleaseCommerceImportLease(f.seller.Id, *lease))
}

func TestCommerceImportNewAuthorizationSupersedesOldCallbackAndDenial(t *testing.T) {
	f, connection := commerceConnectionFixture(t)
	old, oldIn := commerceConnectionSession(t, f.seller.Id, connection.ID, "old", "products.read")
	_, err := ConsumeCommerceImportSession(f.seller.Id, old.ID, oldIn.DashboardSessionID)
	require.NoError(t, err)
	current, currentIn := commerceConnectionSession(t, f.seller.Id, connection.ID, "new", "products.read")
	_, err = CompleteCommerceImportAuthorization(f.seller.Id, old.ID, commerceConnectionTokens("products.read"))
	require.ErrorIs(t, err, ErrMerchantStoreDenied)
	_, err = ConsumeCommerceImportSession(f.seller.Id, current.ID, currentIn.DashboardSessionID)
	require.NoError(t, err)
	require.NoError(t, FinishCommerceImportDenied(f.seller.Id, current.ID))
	require.NoError(t, DB.First(current, "id = ?", current.ID).Error)
	require.Equal(t, "denied", current.Status)
	require.Empty(t, current.SecretsCiphertext)
	connection, err = GetCommerceImportConnection(f.seller.Id, connection.ID)
	require.NoError(t, err)
	require.Equal(t, "pending", connection.Status)
	require.Empty(t, connection.AuthSessionID)
	_, err = CompleteCommerceImportAuthorization(f.seller.Id, current.ID, commerceConnectionTokens("products.read"))
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	expired, expiredIn := commerceConnectionSession(t, f.seller.Id, connection.ID, "expired", "products.read")
	require.NoError(t, DB.Model(expired).Update("expires_at", common.GetTimestamp()-1).Error)
	_, err = GetCommerceImportSession(f.seller.Id, expiredIn.State, expiredIn.DashboardSessionID)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	_, err = ConsumeCommerceImportSession(f.seller.Id, expired.ID, expiredIn.DashboardSessionID)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestCommerceImportAuthorizationHeldLeaseAndRefreshNarrowing(t *testing.T) {
	f, connection := commerceConnectionFixture(t)
	session, in := commerceConnectionSession(t, f.seller.Id, connection.ID, "held", "products.read cards.issue")
	lease, err := AcquireCommerceImportLease(f.seller.Id, connection.ID, 90)
	require.NoError(t, err)
	_, err = ConsumeCommerceImportSession(f.seller.Id, session.ID, in.DashboardSessionID)
	require.ErrorIs(t, err, ErrCommerceImportLease)
	_, err = ConsumeCommerceImportSessionWithLease(f.seller.Id, *lease, session.ID, in.DashboardSessionID)
	require.NoError(t, err)
	tokens := commerceConnectionTokens("products.read cards.issue")
	_, err = CompleteCommerceImportAuthorizationWithLease(f.seller.Id, *lease, session.ID, tokens)
	require.NoError(t, err)
	require.NoError(t, ReleaseCommerceImportLease(f.seller.Id, *lease))
	lease, err = AcquireCommerceImportLease(f.seller.Id, connection.ID, 90)
	require.NoError(t, err)
	_, err = BeginCommerceImportRefresh(f.seller.Id, *lease)
	require.NoError(t, err)
	current, err := GetCommerceImportConnection(f.seller.Id, connection.ID)
	require.NoError(t, err)
	require.Equal(t, "reauthorize", current.Status)
	require.Positive(t, current.RefreshAttemptAt)
	_, err = BeginCommerceImportRefresh(f.seller.Id, *lease)
	require.ErrorIs(t, err, ErrCommerceImportReauthorize)
	_, err = LoadCommerceImportTokens(f.seller.Id, *lease)
	require.ErrorIs(t, err, ErrCommerceImportReauthorize)
	bad := tokens
	bad.GrantID = "other-grant"
	require.ErrorIs(t, CompleteCommerceImportRefresh(f.seller.Id, *lease, bad), ErrMerchantStoreDenied)
	bad = tokens
	bad.GrantExpiresAt++
	require.ErrorIs(t, CompleteCommerceImportRefresh(f.seller.Id, *lease, bad), ErrMerchantStoreDenied)
	tokens.Scope, tokens.AccessToken, tokens.RefreshToken = "products.read", "ROTATED-ACCESS", "ROTATED-REFRESH"
	require.NoError(t, CompleteCommerceImportRefresh(f.seller.Id, *lease, tokens))
	loaded, err := LoadCommerceImportTokens(f.seller.Id, *lease)
	require.NoError(t, err)
	require.Equal(t, "ROTATED-REFRESH", loaded.RefreshToken)
	current, err = GetCommerceImportConnection(f.seller.Id, connection.ID)
	require.NoError(t, err)
	require.Equal(t, int64(2), current.TokenVersion)
	require.Zero(t, current.RefreshAttemptAt)
	_, err = BeginCommerceImportRefresh(f.seller.Id, *lease)
	require.NoError(t, err)
	bad = tokens
	bad.Scope = "products.read cards.issue"
	require.ErrorIs(t, CompleteCommerceImportRefresh(f.seller.Id, *lease, bad), ErrMerchantStoreDenied)
	require.NoError(t, DB.Model(&MerchantStoreCommerceConnection{}).Where("id = ?", connection.ID).Update("lease_expires_at", common.GetTimestamp()-1).Error)
	newLease, err := AcquireCommerceImportLease(f.seller.Id, connection.ID, 90)
	require.NoError(t, err)
	require.NotEqual(t, lease.Owner, newLease.Owner)
	require.ErrorIs(t, CompleteCommerceImportRefresh(f.seller.Id, *lease, tokens), ErrCommerceImportLease)
	require.ErrorIs(t, ReleaseCommerceImportLease(f.seller.Id, *lease), ErrCommerceImportLease)
	_, err = BeginCommerceImportRefresh(f.seller.Id, *newLease)
	require.ErrorIs(t, err, ErrCommerceImportReauthorize)
	_, err = LoadCommerceImportTokens(f.seller.Id, *newLease)
	require.ErrorIs(t, err, ErrCommerceImportReauthorize)
	require.NoError(t, ReleaseCommerceImportLease(f.seller.Id, *newLease))
}

func TestCommerceImportDisconnectDeletesSecretsRetainsBusinessAndTombstones(t *testing.T) {
	f, connection := commerceConnectionFixture(t)
	commerceConnectionAuthorize(t, f.seller.Id, connection.ID)
	terms, err := SaveMerchantStoreSellerTerms(f.seller.Id, MerchantStoreTermsInput{Content: "The selected cards are delivered after payment."})
	require.NoError(t, err)
	checkout := f.checkout("commerce-order", "balance")
	checkout.SellerTermsVersion, checkout.AcceptSellerTerms = terms.Version, true
	_, _, err = CreateMerchantStoreOrder(checkout)
	require.NoError(t, err)
	session, _ := commerceConnectionSession(t, f.seller.Id, connection.ID, "pending-at-disconnect", "products.read")
	mapping := MerchantStoreCommerceProductMapping{ID: uuid.NewString(), SellerID: f.seller.Id, Issuer: connection.Issuer, ShopID: "shop-first", ExternalProductID: "external-product", ConnectionID: connection.ID, LocalProductID: f.product.ID, Revision: "rev-first", Mode: "finite", RawJSON: `{}`}
	require.NoError(t, DB.Create(&mapping).Error)
	request := MerchantStoreCommerceRestockRequest{ID: uuid.NewString(), ConnectionID: connection.ID, SellerID: f.seller.Id, GrantID: "grant-first", KeyHash: storeHash("idempotency"), KeyCiphertext: "key-cipher", BodyCiphertext: "body-cipher", BodyHash: storeHash("body"), ProductMappingID: mapping.ID, ProductID: f.product.ID, VariantID: "variant-first", ExternalProductID: "external-product", ExternalVariantID: "external-variant", Revision: "rev-first", Count: 2, Status: "imported", BatchID: "batch-first", ResponseCiphertext: "request-response-cipher", ResponseHash: storeHash("response"), RecoveryExpiresAt: common.GetTimestamp() + 600}
	require.NoError(t, DB.Create(&request).Error)
	batch := MerchantStoreCommerceCardBatch{ID: uuid.NewString(), ConnectionID: connection.ID, GrantID: "grant-first", BatchID: "batch-first", RequestID: request.ID, ResponseCiphertext: "batch-response-cipher", ResponseHash: storeHash("response"), RecoveryExpiresAt: common.GetTimestamp() + 600, Count: 2}
	require.NoError(t, DB.Create(&batch).Error)
	var stocksBefore []MerchantStoreStock
	var ordersBefore []MerchantStoreOrder
	require.NoError(t, DB.Order("id").Find(&stocksBefore).Error)
	require.NoError(t, DB.Order("id").Find(&ordersBefore).Error)
	lease, err := AcquireCommerceImportLease(f.seller.Id, connection.ID, 90)
	require.NoError(t, err)
	require.NoError(t, DisconnectCommerceImportConnection(f.seller.Id, *lease))
	_, err = LoadCommerceImportTokens(f.seller.Id, *lease)
	require.ErrorIs(t, err, ErrCommerceImportReauthorize)
	current, err := GetCommerceImportConnection(f.seller.Id, connection.ID)
	require.NoError(t, err)
	require.Equal(t, "disconnected", current.Status)
	require.Empty(t, current.TokensCiphertext)
	require.Empty(t, current.AuthSessionID)
	var sessionCount int64
	require.NoError(t, DB.Model(session).Where("connection_id = ?", connection.ID).Count(&sessionCount).Error)
	require.Zero(t, sessionCount)
	require.NoError(t, DB.First(&request, "id = ?", request.ID).Error)
	require.Empty(t, request.ResponseCiphertext)
	require.Zero(t, request.RecoveryExpiresAt)
	require.Equal(t, "imported", request.Status)
	require.Equal(t, "batch-first", request.BatchID)
	require.Equal(t, "key-cipher", request.KeyCiphertext)
	require.Equal(t, "body-cipher", request.BodyCiphertext)
	require.NoError(t, DB.First(&batch, "id = ?", batch.ID).Error)
	require.Empty(t, batch.ResponseCiphertext)
	require.Equal(t, storeHash("response"), batch.ResponseHash)
	require.NoError(t, DB.First(&mapping, "id = ?", mapping.ID).Error)
	var stocksAfter []MerchantStoreStock
	var ordersAfter []MerchantStoreOrder
	require.NoError(t, DB.Order("id").Find(&stocksAfter).Error)
	require.NoError(t, DB.Order("id").Find(&ordersAfter).Error)
	require.Equal(t, stocksBefore, stocksAfter)
	require.Equal(t, ordersBefore, ordersAfter)
	require.NoError(t, ReleaseCommerceImportLease(f.seller.Id, *lease))
}

// This helper is executed as a separate OS process against the same isolated
// database, so a process-local mutex cannot make the contention test pass.
func TestCommerceImportLeaseProcessHelper(t *testing.T) {
	dsn := os.Getenv("COMMERCE_IMPORT_PROCESS_DB")
	if dsn == "" {
		t.Skip("subprocess helper")
	}
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	DB = db
	seller, err := strconv.Atoi(os.Getenv("COMMERCE_IMPORT_PROCESS_SELLER"))
	require.NoError(t, err)
	ready, start := os.Getenv("COMMERCE_IMPORT_PROCESS_READY"), os.Getenv("COMMERCE_IMPORT_PROCESS_START")
	if ready != "" {
		require.NoError(t, os.WriteFile(ready, []byte("ready"), 0600))
		deadline := time.Now().Add(10 * time.Second)
		for {
			if _, err := os.Stat(start); err == nil {
				break
			}
			require.True(t, time.Now().Before(deadline))
			time.Sleep(5 * time.Millisecond)
		}
	}
	result := "busy"
	lease, err := AcquireCommerceImportLease(seller, os.Getenv("COMMERCE_IMPORT_PROCESS_CONNECTION"), 90)
	if errors.Is(err, ErrCommerceImportLease) {
		result = "busy"
	} else {
		require.NoError(t, err)
		_, err = BeginCommerceImportRefresh(seller, *lease)
		if errors.Is(err, ErrCommerceImportReauthorize) {
			result = "reauthorize"
		} else {
			require.NoError(t, err)
			result = "winner"
		}
	}
	require.NoError(t, os.WriteFile(os.Getenv("COMMERCE_IMPORT_PROCESS_RESULT"), []byte(result), 0600))
}

func TestCommerceImportTwoProcessesRefreshLeaseAndCrashFence(t *testing.T) {
	root := t.TempDir()
	dsn := filepath.Join(root, "commerce.sqlite") + "?_txlock=immediate&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	defer sqlDB.Close()
	oldDB := DB
	DB = db
	defer func() { DB = oldDB }()
	t.Setenv("MERCHANT_STORE_ENCRYPTION_KEY", "C5wmMzDh1QsVZb0saEW9ulAPzVN87Boqv3DK6eIrKXc2YLfg")
	require.NoError(t, db.AutoMigrate(append([]interface{}{&User{}, &Option{}}, CommerceImportModels()...)...))
	require.NoError(t, db.Create(&Option{Key: MerchantStoreWriterCapabilityOption, Value: "8"}).Error)
	seller := marketTestUser(t, db, "process-seller", 0, common.RoleCommonUser)
	connection, err := CreateCommerceImportConnection(seller.Id, CommerceImportConnectionInput{Issuer: "https://issuer.example.com", ClientID: "client", RedirectURI: "https://merchant.example.com/callback", MetadataJSON: `{}`, MaximumCardsPerRequest: 100})
	require.NoError(t, err)
	tokens := commerceConnectionAuthorize(t, seller.Id, connection.ID)
	start := filepath.Join(root, "start")
	var commands []*exec.Cmd
	var outputs []*bytes.Buffer
	for i := 0; i < 2; i++ {
		command := exec.Command(os.Args[0], "-test.run=^TestCommerceImportLeaseProcessHelper$", "-test.count=1")
		command.Env = append(os.Environ(), "COMMERCE_IMPORT_PROCESS_DB="+dsn, "COMMERCE_IMPORT_PROCESS_SELLER="+strconv.Itoa(seller.Id), "COMMERCE_IMPORT_PROCESS_CONNECTION="+connection.ID,
			"COMMERCE_IMPORT_PROCESS_READY="+filepath.Join(root, "ready-"+strconv.Itoa(i)), "COMMERCE_IMPORT_PROCESS_START="+start, "COMMERCE_IMPORT_PROCESS_RESULT="+filepath.Join(root, "result-"+strconv.Itoa(i)), "GOMAXPROCS=2")
		output := &bytes.Buffer{}
		command.Stdout, command.Stderr = output, output
		require.NoError(t, command.Start())
		commands, outputs = append(commands, command), append(outputs, output)
	}
	deadline := time.Now().Add(10 * time.Second)
	for i := 0; i < 2; i++ {
		for {
			if _, err := os.Stat(filepath.Join(root, "ready-"+strconv.Itoa(i))); err == nil {
				break
			}
			require.True(t, time.Now().Before(deadline))
			time.Sleep(5 * time.Millisecond)
		}
	}
	require.NoError(t, os.WriteFile(start, []byte("start"), 0600))
	winners := 0
	for i, command := range commands {
		require.NoError(t, command.Wait(), outputs[i].String())
		result, err := os.ReadFile(filepath.Join(root, "result-"+strconv.Itoa(i)))
		require.NoError(t, err)
		if string(result) == "winner" {
			winners++
		} else {
			require.Equal(t, "busy", string(result))
		}
	}
	require.Equal(t, 1, winners, "only one independent process obtains the refresh credential")
	current, err := GetCommerceImportConnection(seller.Id, connection.ID)
	require.NoError(t, err)
	require.Equal(t, "reauthorize", current.Status)
	require.Positive(t, current.RefreshAttemptAt)
	stale := CommerceImportLease{ConnectionID: current.ID, Owner: current.LeaseOwner, ExpiresAt: current.LeaseExpiresAt}
	require.NoError(t, db.Model(current).Update("lease_expires_at", common.GetTimestamp()-1).Error)
	// A third process represents restart after the refresh winner died. It
	// acquires a fresh lease but must not receive the uncertain old credential.
	command := exec.Command(os.Args[0], "-test.run=^TestCommerceImportLeaseProcessHelper$", "-test.count=1")
	command.Env = append(os.Environ(), "COMMERCE_IMPORT_PROCESS_DB="+dsn, "COMMERCE_IMPORT_PROCESS_SELLER="+strconv.Itoa(seller.Id), "COMMERCE_IMPORT_PROCESS_CONNECTION="+connection.ID,
		"COMMERCE_IMPORT_PROCESS_RESULT="+filepath.Join(root, "restart-result"), "GOMAXPROCS=2")
	output, err := command.CombinedOutput()
	require.NoError(t, err, string(output))
	result, err := os.ReadFile(filepath.Join(root, "restart-result"))
	require.NoError(t, err)
	require.Equal(t, "reauthorize", string(result))
	require.ErrorIs(t, CompleteCommerceImportRefresh(seller.Id, stale, tokens), ErrCommerceImportLease)
	require.ErrorIs(t, ReleaseCommerceImportLease(seller.Id, stale), ErrCommerceImportLease)
	current, err = GetCommerceImportConnection(seller.Id, connection.ID)
	require.NoError(t, err)
	require.Equal(t, "reauthorize", current.Status)
	require.NotEqual(t, stale.Owner, current.LeaseOwner)
}
