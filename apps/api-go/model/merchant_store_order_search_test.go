package model

import (
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func storeOrderSearchTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := marketTestDB(t)
	require.NoError(t, db.AutoMigrate(append(MerchantStoreModels(), &MerchantStoreOrderSearchChallenge{}, &MerchantStoreOrderSearchAuthorization{})...))
	t.Setenv("MERCHANT_STORE_ENCRYPTION_KEY", "C5wmMzDh1QsVZb0saEW9ulAPzVN87Boqv3DK6eIrKXc2YLfg")
	return db
}

func storeOrderSearchWrongCode(code string) string {
	if code == "000000" {
		return "111111"
	}
	return "000000"
}

func storeOrderSearchChallenge(t *testing.T, email string) MerchantStoreOrderSearchChallenge {
	t.Helper()
	var row MerchantStoreOrderSearchChallenge
	normalized, e := NormalizeMerchantStorePickupEmail(email)
	require.NoError(t, e)
	require.NoError(t, DB.First(&row, "email_hash = ?", storeHash(normalized)).Error)
	return row
}

func storeOrderSearchAllowResend(t *testing.T, email string) {
	t.Helper()
	normalized, e := NormalizeMerchantStorePickupEmail(email)
	require.NoError(t, e)
	require.NoError(t, DB.Model(&MerchantStoreOrderSearchChallenge{}).Where("email_hash = ?", storeHash(normalized)).Update("sent_at", common.GetTimestamp()-61).Error)
}

func TestMerchantStoreOrderSearchChallengePrivacyAndPurposeIsolation(t *testing.T) {
	storeOrderSearchTestDB(t)
	id, code, email, e := BeginMerchantStoreOrderSearch(" Buyer@Example.TEST ")
	require.NoError(t, e)
	require.Equal(t, "Buyer@example.test", email)
	require.True(t, storeTokenValid(id))
	require.Len(t, code, 6)
	for _, digit := range code {
		require.True(t, digit >= '0' && digit <= '9')
	}
	challenge := storeOrderSearchChallenge(t, email)
	require.Equal(t, storeHash(id), challenge.ChallengeTokenHash)
	require.NotEqual(t, id, challenge.ChallengeTokenHash)
	require.NotContains(t, challenge.EmailCiphertext, email)
	require.NotContains(t, challenge.CodeCiphertext, code)
	require.EqualValues(t, 600, challenge.ExpiresAt-challenge.SentAt)
	require.Len(t, challenge.SentTimes, 1)
	raw, e := json.Marshal(challenge)
	require.NoError(t, e)
	require.JSONEq(t, "{}", string(raw))
	address, e := storeDecrypt("order-search-email", challenge.EmailHash, challenge.EmailCiphertext)
	require.NoError(t, e)
	require.Equal(t, email, address)
	_, e = storeDecrypt("email-verification", challenge.ChallengeTokenHash+":"+challenge.EmailHash, challenge.CodeCiphertext)
	require.Error(t, e)
	_, e = storeDecrypt("order-search-code", challenge.ChallengeTokenHash+":other-email-hash", challenge.CodeCiphertext)
	require.Error(t, e)
	_, _, _, e = BeginMerchantStoreOrderSearch(email)
	require.ErrorIs(t, e, ErrMerchantStoreEmailVerificationCooldown)
	_, _, _, e = BeginMerchantStoreOrderSearch("invalid-address")
	require.ErrorIs(t, e, ErrMerchantStoreInput)
	_, _, _, e = BeginMerchantStoreOrderSearch("")
	require.ErrorIs(t, e, ErrMerchantStoreInput)
}

func TestMerchantStoreOrderSearchResendKeepsWindowAndAttempts(t *testing.T) {
	storeOrderSearchTestDB(t)
	id, code, email, e := BeginMerchantStoreOrderSearch("buyer@example.test")
	require.NoError(t, e)
	_, e = ConfirmMerchantStoreOrderSearch(id, storeOrderSearchWrongCode(code))
	require.ErrorIs(t, e, ErrMerchantStoreEmailVerificationInvalid)
	before := storeOrderSearchChallenge(t, email)
	require.Equal(t, 1, before.Attempts)
	storeOrderSearchAllowResend(t, email)
	newID, newCode, _, e := BeginMerchantStoreOrderSearch(email)
	require.NoError(t, e)
	require.NotEqual(t, id, newID)
	after := storeOrderSearchChallenge(t, email)
	require.Equal(t, before.ExpiresAt, after.ExpiresAt)
	require.Equal(t, before.Attempts, after.Attempts)
	require.Len(t, after.SentTimes, 2)
	expiresAt, e := GetMerchantStoreOrderSearchChallengeExpires(newID)
	require.NoError(t, e)
	require.Equal(t, before.ExpiresAt, expiresAt)
	_, e = ConfirmMerchantStoreOrderSearch(id, code)
	require.ErrorIs(t, e, ErrMerchantStoreEmailVerificationInvalid)
	_, e = ConfirmMerchantStoreOrderSearch(newID, newCode)
	require.NoError(t, e)
}

func TestMerchantStoreOrderSearchFiveWrongAttemptsPersistAndLockResend(t *testing.T) {
	storeOrderSearchTestDB(t)
	id, code, email, e := BeginMerchantStoreOrderSearch("buyer@example.test")
	require.NoError(t, e)
	for i := 0; i < 5; i++ {
		wrong := storeOrderSearchWrongCode(code)
		if i == 0 {
			wrong = "bad-shape"
		}
		_, e = ConfirmMerchantStoreOrderSearch(id, wrong)
		require.ErrorIs(t, e, ErrMerchantStoreEmailVerificationInvalid)
		require.Equal(t, i+1, storeOrderSearchChallenge(t, email).Attempts)
	}
	_, e = ConfirmMerchantStoreOrderSearch(id, code)
	require.ErrorIs(t, e, ErrMerchantStoreEmailVerificationInvalid)
	storeOrderSearchAllowResend(t, email)
	_, _, _, e = BeginMerchantStoreOrderSearch(email)
	require.ErrorIs(t, e, ErrMerchantStoreEmailVerificationCooldown)
	now := common.GetTimestamp()
	require.NoError(t, DB.Model(&MerchantStoreOrderSearchChallenge{}).Where("email_hash = ?", storeHash(email)).Update("expires_at", now-1).Error)
	_, _, _, e = BeginMerchantStoreOrderSearch(email)
	require.NoError(t, e)
	challenge := storeOrderSearchChallenge(t, email)
	require.Zero(t, challenge.Attempts)
	require.GreaterOrEqual(t, challenge.ExpiresAt, now+600)
}

func TestMerchantStoreOrderSearchRollingHourlySendLimit(t *testing.T) {
	storeOrderSearchTestDB(t)
	email := "buyer@example.test"
	for i := 0; i < 10; i++ {
		if i != 0 {
			storeOrderSearchAllowResend(t, email)
		}
		_, _, _, e := BeginMerchantStoreOrderSearch(email)
		require.NoError(t, e)
	}
	storeOrderSearchAllowResend(t, email)
	_, _, _, e := BeginMerchantStoreOrderSearch(email)
	require.ErrorIs(t, e, ErrMerchantStoreEmailVerificationCooldown)
	challenge := storeOrderSearchChallenge(t, email)
	require.Len(t, challenge.SentTimes, 10)
	challenge.SentTimes[0] = common.GetTimestamp() - 3601
	require.NoError(t, DB.Save(&challenge).Error)
	_, _, _, e = BeginMerchantStoreOrderSearch(email)
	require.NoError(t, e)
	require.Len(t, storeOrderSearchChallenge(t, email).SentTimes, 10)
	_, _, _, e = BeginMerchantStoreOrderSearch("independent@example.test")
	require.NoError(t, e)
}

func TestMerchantStoreOrderSearchSingleUseAndAddressBoundAuthorization(t *testing.T) {
	storeOrderSearchTestDB(t)
	id, code, email, e := BeginMerchantStoreOrderSearch("buyer@example.test")
	require.NoError(t, e)
	token, e := ConfirmMerchantStoreOrderSearch(id, code)
	require.NoError(t, e)
	require.True(t, storeTokenValid(token))
	require.NotEqual(t, id, token)
	_, e = ConfirmMerchantStoreOrderSearch(id, code)
	require.ErrorIs(t, e, ErrMerchantStoreEmailVerificationInvalid)
	challenge := storeOrderSearchChallenge(t, email)
	require.Empty(t, challenge.ChallengeTokenHash)
	require.Empty(t, challenge.CodeCiphertext)
	require.Empty(t, challenge.EmailCiphertext)
	require.Positive(t, challenge.ConsumedAt)
	_, e = GetMerchantStoreOrderSearchChallengeExpires(id)
	require.ErrorIs(t, e, ErrMerchantStoreEmailVerificationInvalid)
	var authorization MerchantStoreOrderSearchAuthorization
	require.NoError(t, DB.First(&authorization, "token_hash = ?", storeHash(token)).Error)
	require.Equal(t, storeHash(email), authorization.EmailHash)
	require.EqualValues(t, 900, authorization.ExpiresAt-authorization.CreatedAt)
	raw, e := json.Marshal(authorization)
	require.NoError(t, e)
	require.JSONEq(t, "{}", string(raw))
	var accountFacts int64
	require.NoError(t, DB.Model(&MerchantStoreVerifiedEmail{}).Count(&accountFacts).Error)
	require.Zero(t, accountFacts, "public order lookup must not verify an account email")
	rows, e := ListMerchantStoreOrdersByVerifiedEmail(token, 0, 30)
	require.NoError(t, e)
	require.Empty(t, rows, "a valid mailbox proof also works for an address with no orders")
	_, e = ListMerchantStoreOrdersByVerifiedEmail(id, 0, 30)
	require.ErrorIs(t, e, ErrMerchantStoreDenied, "challenge and authorization purposes cannot be mixed")
	for _, page := range [][2]int{{-1, 30}, {100001, 30}, {0, 0}, {0, 101}} {
		_, e = ListMerchantStoreOrdersByVerifiedEmail(token, page[0], page[1])
		require.ErrorIs(t, e, ErrMerchantStoreInput)
	}
	require.NoError(t, DB.Model(&MerchantStoreOrderSearchAuthorization{}).Where("token_hash = ?", storeHash(token)).Update("expires_at", common.GetTimestamp()-1).Error)
	_, e = ListMerchantStoreOrdersByVerifiedEmail(token, 0, 30)
	require.ErrorIs(t, e, ErrMerchantStoreDenied)
	_, e = ListMerchantStoreOrdersByVerifiedEmail("invalid", 0, 30)
	require.ErrorIs(t, e, ErrMerchantStoreDenied)
}

func storeOrderSearchInsert(t *testing.T, id, tradeNo, email, status string, created int64, code bool) MerchantStoreOrder {
	t.Helper()
	token, e := storeToken()
	require.NoError(t, e)
	cipher, e := storeEncrypt("pickup", id, token)
	require.NoError(t, e)
	row := MerchantStoreOrder{ID: id, TradeNo: tradeNo, BuyerID: 41, SellerID: 42, ProductID: "test-product", ProductTitle: "Customer title", Quantity: 2, Status: status, CreatedAt: created, PickupTokenHash: storeHash(token), PickupTokenCiphertext: cipher, PickupLoginRequired: true, PriceQuota: 900000, FeeQuota: 9000, CheckoutURL: "https://secret.example.test/pay"}
	if email != "" {
		row.PickupEmailHash = storeHash(email)
	}
	if code {
		row.PickupCodeHash = "a-real-order-code-hash"
		row.PickupCodeRequired = false // The live hash, not the old setting, is authoritative.
	}
	require.NoError(t, DB.Create(&row).Error)
	return row
}

func TestMerchantStoreOrderSearchSummaryPrivacyAndEmailScope(t *testing.T) {
	storeOrderSearchTestDB(t)
	paid := storeOrderSearchInsert(t, strings.Repeat("a", 64), "MS"+strings.Repeat("Ab", 15), "alice@example.test", "paid", 300, true)
	pending := storeOrderSearchInsert(t, strings.Repeat("b", 64), "MS"+strings.Repeat("CD", 15), "alice@example.test", "pending", 200, false)
	storeOrderSearchInsert(t, strings.Repeat("c", 64), "MS"+strings.Repeat("Ef", 15), "bob@example.test", "paid", 400, true)
	storeOrderSearchInsert(t, strings.Repeat("d", 64), "MS"+strings.Repeat("GH", 15), "", "paid", 500, true)
	storeOrderSearchInsert(t, strings.Repeat("e", 64), "MS"+strings.Repeat("Ij", 15), "Alice@example.test", "paid", 600, true)
	id, code, _, e := BeginMerchantStoreOrderSearch("alice@example.test")
	require.NoError(t, e)
	token, e := ConfirmMerchantStoreOrderSearch(id, code)
	require.NoError(t, e)
	rows, e := ListMerchantStoreOrdersByVerifiedEmail(token, 0, 30)
	require.NoError(t, e)
	require.Len(t, rows, 2)
	require.Equal(t, paid.TradeNo, rows[0].ID)
	require.Equal(t, paid.ID, rows[0].RawOrderID)
	require.True(t, rows[0].PickupCodeRequired)
	require.True(t, storeTokenValid(rows[0].PickupToken))
	require.Equal(t, pending.TradeNo, rows[1].ID)
	require.Empty(t, rows[1].PickupToken)
	raw, e := json.Marshal(rows)
	require.NoError(t, e)
	for _, secret := range []string{paid.ID, rows[0].PickupToken, "buyer_id", "seller_id", "pickup_email", "alice@example", "checkout_url", "price_quota", "fee_quota", "900000"} {
		require.NotContains(t, string(raw), secret)
	}
	one, e := ListMerchantStoreOrdersByVerifiedEmail(token, 1, 1)
	require.NoError(t, e)
	require.Len(t, one, 1)
	require.Equal(t, pending.TradeNo, one[0].ID)
	caseID, caseCode, _, e := BeginMerchantStoreOrderSearch("Alice@EXAMPLE.TEST")
	require.NoError(t, e)
	caseToken, e := ConfirmMerchantStoreOrderSearch(caseID, caseCode)
	require.NoError(t, e)
	caseRows, e := ListMerchantStoreOrdersByVerifiedEmail(caseToken, 0, 30)
	require.NoError(t, e)
	require.Len(t, caseRows, 1, "case-sensitive local parts must never merge mailbox owners")
	summary, e := GetMerchantStoreOrderSearchSummary(paid.TradeNo)
	require.NoError(t, e)
	require.Equal(t, paid.TradeNo, summary.ID)
	require.Empty(t, summary.PickupToken, "knowing an order number never grants pickup authorization")
	_, e = GetMerchantStoreOrderSearchSummary("MS" + strings.Repeat("ab", 15))
	require.ErrorIs(t, e, gorm.ErrRecordNotFound, "number search must be case-sensitive")
	_, e = GetMerchantStoreOrderSearchSummary(paid.TradeNo[:10])
	require.ErrorIs(t, e, ErrMerchantStoreInput)
	_, e = GetMerchantStoreOrderSearchSummary("MS" + strings.Repeat("x", 29) + "_")
	require.ErrorIs(t, e, ErrMerchantStoreInput)
}

func TestMerchantStoreOrderSearchChallengeExpiresAndConcurrentReplay(t *testing.T) {
	storeOrderSearchTestDB(t)
	id, code, email, e := BeginMerchantStoreOrderSearch("expire@example.test")
	require.NoError(t, e)
	require.NoError(t, DB.Model(&MerchantStoreOrderSearchChallenge{}).Where("email_hash = ?", storeHash(email)).Update("expires_at", common.GetTimestamp()-1).Error)
	_, e = ConfirmMerchantStoreOrderSearch(id, code)
	require.ErrorIs(t, e, ErrMerchantStoreEmailVerificationInvalid)
	id, code, _, e = BeginMerchantStoreOrderSearch("race@example.test")
	require.NoError(t, e)
	var successes int
	var mutex sync.Mutex
	var wait sync.WaitGroup
	for i := 0; i < 8; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := ConfirmMerchantStoreOrderSearch(id, code)
			mutex.Lock()
			defer mutex.Unlock()
			if err == nil {
				successes++
			} else if err != ErrMerchantStoreEmailVerificationInvalid {
				t.Errorf("unexpected concurrent confirmation error: %v", err)
			}
		}()
	}
	wait.Wait()
	require.Equal(t, 1, successes)
	var grants int64
	require.NoError(t, DB.Model(&MerchantStoreOrderSearchAuthorization{}).Count(&grants).Error)
	require.EqualValues(t, 1, grants)
}

func TestMerchantStoreOrderSearchConcurrentFirstSendHasOneBudget(t *testing.T) {
	storeOrderSearchTestDB(t)
	var successes int
	var mutex sync.Mutex
	var wait sync.WaitGroup
	for i := 0; i < 8; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, _, _, err := BeginMerchantStoreOrderSearch("race@example.test")
			mutex.Lock()
			defer mutex.Unlock()
			if err == nil {
				successes++
			} else if err != ErrMerchantStoreEmailVerificationCooldown {
				t.Errorf("unexpected concurrent first-send error: %v", err)
			}
		}()
	}
	wait.Wait()
	require.Equal(t, 1, successes)
	challenge := storeOrderSearchChallenge(t, "race@example.test")
	require.Len(t, challenge.SentTimes, 1)
	require.Zero(t, challenge.Attempts)
}

func TestNormalizeMerchantStorePickupEmailPreservesLocalPart(t *testing.T) {
	email, e := NormalizeMerchantStorePickupEmail(" Buyer+tag@EXAMPLE.TEST ")
	require.NoError(t, e)
	require.Equal(t, "Buyer+tag@example.test", email)
	email, e = NormalizeMerchantStorePickupEmail(" ")
	require.NoError(t, e)
	require.Empty(t, email)
	for _, invalid := range []string{"not-an-email", "Name <buyer@example.test>", "buyer@example.test\r\nBcc:other@example.test", strings.Repeat("x", 255) + "@example.test"} {
		_, e = NormalizeMerchantStorePickupEmail(invalid)
		require.ErrorIs(t, e, ErrMerchantStoreInput)
	}
}

func TestMerchantStoreOrderSearchLegacyNumberRequiresOriginalParty(t *testing.T) {
	db := storeOrderSearchTestDB(t)
	buyer := marketTestUser(t, db, "legacy-buyer", 0, common.RoleCommonUser)
	seller := marketTestUser(t, db, "legacy-seller", 0, common.RoleCommonUser)
	outsider := marketTestUser(t, db, "legacy-outsider", 0, common.RoleCommonUser)
	id := strings.Repeat("a", 64)
	row := storeOrderSearchInsert(t, id, "MS"+id[:30], "buyer@example.test", "paid", 100, true)
	require.NoError(t, DB.Model(&MerchantStoreOrder{}).Where("id = ?", row.ID).Updates(map[string]any{"buyer_id": buyer.Id, "seller_id": seller.Id}).Error)
	_, e := GetMerchantStoreOrderSearchSummary(row.TradeNo)
	require.ErrorIs(t, e, gorm.ErrRecordNotFound, "legacy status must not become anonymously enumerable")
	_, e = GetMerchantStoreOrderSearchSummaryForActor(row.TradeNo, outsider.Id)
	require.ErrorIs(t, e, gorm.ErrRecordNotFound, "unknown and forbidden legacy orders share the same response")
	for _, actor := range []int{buyer.Id, seller.Id} {
		summary, err := GetMerchantStoreOrderSearchSummaryForActor(row.TradeNo, actor)
		require.NoError(t, err)
		require.Equal(t, row.TradeNo, summary.ID)
		require.Empty(t, summary.PickupToken)
	}
	require.NoError(t, DB.Model(&User{}).Where("id = ?", buyer.Id).Update("status", common.UserStatusDisabled).Error)
	_, e = GetMerchantStoreOrderSearchSummaryForActor(row.TradeNo, buyer.Id)
	require.ErrorIs(t, e, gorm.ErrRecordNotFound)
}

func TestMerchantStoreOrderSearchCleanupKeepsLiveAndHourlyBudgets(t *testing.T) {
	storeOrderSearchTestDB(t)
	now := common.GetTimestamp()
	require.NoError(t, DB.Create(&MerchantStoreOrderSearchAuthorization{TokenHash: storeHash("expired-token"), EmailHash: storeHash("expired@example.test"), ExpiresAt: now}).Error)
	require.NoError(t, DB.Create(&MerchantStoreOrderSearchAuthorization{TokenHash: storeHash("live-token"), EmailHash: storeHash("live@example.test"), ExpiresAt: now + 900}).Error)
	rows := []MerchantStoreOrderSearchChallenge{
		{EmailHash: storeHash("stale@example.test"), SentAt: now - 86400, ExpiresAt: now - 1},
		{EmailHash: storeHash("old-live@example.test"), SentAt: now - 86401, ExpiresAt: now + 600},
		{EmailHash: storeHash("locked@example.test"), SentAt: now - 61, ExpiresAt: now + 600, Attempts: 5},
		{EmailHash: storeHash("hourly@example.test"), SentAt: now - 61, ExpiresAt: now - 1, SentTimes: []int64{now, now, now, now, now, now, now, now, now, now}},
	}
	for i := range rows {
		require.NoError(t, DB.Create(&rows[i]).Error)
	}
	require.NoError(t, CleanupMerchantStoreOrderSearch(now))
	var grants, challenges int64
	require.NoError(t, DB.Model(&MerchantStoreOrderSearchAuthorization{}).Count(&grants).Error)
	require.EqualValues(t, 1, grants)
	require.NoError(t, DB.Model(&MerchantStoreOrderSearchChallenge{}).Count(&challenges).Error)
	require.EqualValues(t, 3, challenges)
	_, _, _, e := BeginMerchantStoreOrderSearch("locked@example.test")
	require.ErrorIs(t, e, ErrMerchantStoreEmailVerificationCooldown)
	_, _, _, e = BeginMerchantStoreOrderSearch("hourly@example.test")
	require.ErrorIs(t, e, ErrMerchantStoreEmailVerificationCooldown, "cleanup cannot reset the rolling send budget")
	require.ErrorIs(t, CleanupMerchantStoreOrderSearch(0), ErrMerchantStoreInput)
}

func TestMerchantStoreOrderSearchEightConnectionAttemptBudget(t *testing.T) {
	db := storeOrderSearchTestDB(t)
	pool, e := db.DB()
	require.NoError(t, e)
	pool.SetMaxOpenConns(8)
	id, code, email, e := BeginMerchantStoreOrderSearch("concurrent@example.test")
	require.NoError(t, e)
	// All eight requests locate the same challenge before any may validate
	// it. This exercises multiple database connections and stale locators,
	// unlike the general fixture's deliberately single-connection pool.
	ready := make(chan struct{})
	gate := make(chan struct{})
	var located atomic.Int32
	const callback = "test:order_search_locators"
	require.NoError(t, db.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
		if strings.Contains(tx.Statement.SQL.String(), "challenge_token_hash") && tx.Statement.Error == nil {
			if located.Add(1) == 8 {
				close(ready)
			}
			<-gate
		}
	}))
	defer func() { _ = db.Callback().Query().Remove(callback) }()
	results := make(chan error, 8)
	var wait sync.WaitGroup
	for i := 0; i < 8; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := ConfirmMerchantStoreOrderSearch(id, storeOrderSearchWrongCode(code))
			results <- err
		}()
	}
	allLocated := false
	select {
	case <-ready:
		allLocated = true
	case <-time.After(5 * time.Second):
	}
	close(gate)
	wait.Wait()
	require.True(t, allLocated, "all requests must reach the shared challenge before validation")
	close(results)
	for err := range results {
		require.ErrorIs(t, err, ErrMerchantStoreEmailVerificationInvalid)
	}
	require.Equal(t, 5, storeOrderSearchChallenge(t, email).Attempts)
	_, e = ConfirmMerchantStoreOrderSearch(id, code)
	require.ErrorIs(t, e, ErrMerchantStoreEmailVerificationInvalid)
	var grants int64
	require.NoError(t, DB.Model(&MerchantStoreOrderSearchAuthorization{}).Count(&grants).Error)
	require.Zero(t, grants)
}
