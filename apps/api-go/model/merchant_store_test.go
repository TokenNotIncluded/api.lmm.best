package model

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
)

type storeFixture struct {
	buyer, seller, root User
	product             *MerchantStoreProduct
}

func newStoreFixture(t *testing.T, methods ...string) storeFixture {
	t.Helper()
	db := marketTestDB(t)
	require.NoError(t, db.AutoMigrate(MerchantStoreModels()...))
	t.Setenv("MERCHANT_STORE_ENCRYPTION_KEY", "C5wmMzDh1QsVZb0saEW9ulAPzVN87Boqv3DK6eIrKXc2YLfg")
	f := storeFixture{buyer: marketTestUser(t, db, "store-buyer", 10000000, common.RoleCommonUser), seller: marketTestUser(t, db, "store-seller", 10000000, common.RoleCommonUser), root: marketTestUser(t, db, "store-root", 0, common.RoleRootUser)}
	require.NoError(t, SetMerchantStoreConfig(f.root.Id, MerchantStoreConfig{FeeBPS: 100, RecipientID: f.root.Id, PromotionQuota: 500000}))
	var e error
	f.product, e = SaveMerchantStoreProduct(f.seller.Id, "", MerchantStoreProductInput{Title: "Card store", Description: "Useful text", PriceQuota: 500000, PaymentMethods: methods, PickupLoginRequired: true, PickupCodeRequired: true, EmailPickupLink: true})
	require.NoError(t, e)
	require.NoError(t, SubmitMerchantStoreProduct(f.seller.Id, f.product.ID))
	require.NoError(t, ReviewMerchantStoreProduct(f.root.Id, f.product.ID, true, "private reviewer note"))
	_, e = AddMerchantStoreStock(f.seller.Id, f.product.ID, []string{"CARD-SECRET-FIRST", "CARD-SECRET-SECOND"})
	require.NoError(t, e)
	for _, method := range methods {
		if !strings.HasPrefix(method, "external:") {
			_, e := SaveMerchantStoreGateway(f.seller.Id, method, true, "")
			require.NoError(t, e)
		}
	}
	require.NoError(t, DB.Model(&User{}).Where("id = ?", f.buyer.Id).Update("email", "store-buyer@example.test").Error)
	require.NoError(t, MarkMerchantStoreEmailVerified(f.buyer.Id, "store-buyer@example.test"))
	require.NoError(t, AcceptMerchantStoreDisclaimer(f.buyer.Id, MerchantStoreDisclaimerVersion))
	return f
}
func (f storeFixture) checkout(key, method string) MerchantStoreCheckoutInput {
	return MerchantStoreCheckoutInput{BuyerID: f.buyer.Id, ProductID: f.product.ID, Quantity: 1, RequestKey: key, PaymentMethod: method, PickupCode: "safe-pickup-code"}
}
func storeBalance(t *testing.T, userID, expected int) {
	t.Helper()
	var u User
	require.NoError(t, DB.First(&u, userID).Error)
	require.Equal(t, expected, u.Quota)
}
func TestMerchantStoreBalanceTransfersSnapshotsAndReplay(t *testing.T) {
	f := newStoreFixture(t, "balance")
	o, created, e := CreateMerchantStoreOrder(f.checkout("purchase", "balance"))
	require.NoError(t, e)
	require.True(t, created)
	require.Equal(t, "paid", o.Status)
	require.Equal(t, 500000, o.PriceQuota)
	require.Equal(t, 5000, o.FeeQuota)
	storeBalance(t, f.buyer.Id, 9500000)
	storeBalance(t, f.seller.Id, 10495000)
	storeBalance(t, f.root.Id, 5000)
	replay, made, e := CreateMerchantStoreOrder(f.checkout("purchase", "balance"))
	require.NoError(t, e)
	require.False(t, made)
	require.Equal(t, o.ID, replay.ID)
	changed := f.checkout("purchase", "balance")
	changed.Quantity = 2
	_, _, e = CreateMerchantStoreOrder(changed)
	require.ErrorIs(t, e, ErrMerchantStoreConflict)
	var transfers int64
	require.NoError(t, DB.Model(&MerchantStoreTransfer{}).Where("order_id = ?", o.ID).Count(&transfers).Error)
	require.EqualValues(t, 2, transfers)
	var emails int64
	require.NoError(t, DB.Model(&MerchantStoreEmailDelivery{}).Count(&emails).Error)
	require.EqualValues(t, 1, emails)
}
func TestMerchantStoreAdminSelfReviewAndCurrentRoleOfficial(t *testing.T) {
	f := newStoreFixture(t, "balance")
	require.ErrorIs(t, ReviewMerchantStoreProduct(f.seller.Id, f.product.ID, true, ""), ErrMerchantStoreDenied)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", f.seller.Id).Update("role", common.RoleAdminUser).Error)
	p, e := SaveMerchantStoreProduct(f.seller.Id, "", MerchantStoreProductInput{Title: "Official", PriceQuota: 500000, PaymentMethods: []string{"balance"}})
	require.NoError(t, e)
	require.NoError(t, SubmitMerchantStoreProduct(f.seller.Id, p.ID))
	require.NoError(t, ReviewMerchantStoreProduct(f.seller.Id, p.ID, true, "do not show this public"))
	public, e := GetPublicMerchantStoreProduct(p.ID)
	require.NoError(t, e)
	require.True(t, public.Official)
	require.Empty(t, public.ReviewNote)
	require.Zero(t, public.ReviewedBy)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", f.seller.Id).Update("role", common.RoleCommonUser).Error)
	public, e = GetPublicMerchantStoreProduct(p.ID)
	require.NoError(t, e)
	require.False(t, public.Official)
}
func TestMerchantStoreEditingRequiresReviewAndNoPaymentsDefault(t *testing.T) {
	f := newStoreFixture(t, "balance")
	p, e := SaveMerchantStoreProduct(f.seller.Id, f.product.ID, MerchantStoreProductInput{Title: "Updated", PriceQuota: 999999})
	require.NoError(t, e)
	require.Empty(t, p.PaymentMethods)
	require.Equal(t, "draft", p.Status)
	_, e = GetPublicMerchantStoreProduct(p.ID)
	require.Error(t, e)
	require.NoError(t, SubmitMerchantStoreProduct(f.seller.Id, p.ID))
	_, e = SaveMerchantStoreProduct(f.seller.Id, p.ID, MerchantStoreProductInput{Title: "Race edit", PriceQuota: 1})
	require.ErrorIs(t, e, ErrMerchantStoreConflict)
}
func TestMerchantStoreDisclaimerExplicitAndFeeInsufficientAtomic(t *testing.T) {
	f := newStoreFixture(t, "balance")
	require.NoError(t, DB.Where("user_id = ?", f.buyer.Id).Delete(&MerchantStoreDisclaimerAcceptance{}).Error)
	in := f.checkout("no-ack", "balance")
	in.DisclaimerVersion = MerchantStoreDisclaimerVersion
	_, _, e := CreateMerchantStoreOrder(in)
	require.ErrorIs(t, e, ErrMerchantStoreDisclaimer)
	require.ErrorIs(t, AcceptMerchantStoreDisclaimer(f.buyer.Id, "old"), ErrMerchantStoreDisclaimer)
	require.NoError(t, AcceptMerchantStoreDisclaimer(f.buyer.Id, MerchantStoreDisclaimerVersion))
	require.NoError(t, DB.Model(&User{}).Where("id = ?", f.seller.Id).Update("quota", 4999).Error)
	_, _, e = CreateMerchantStoreOrder(f.checkout("not-enough-fee", "balance"))
	require.ErrorIs(t, e, ErrMerchantStoreBalance)
	storeBalance(t, f.buyer.Id, 10000000)
	storeBalance(t, f.seller.Id, 4999)
	var count int64
	require.NoError(t, DB.Model(&MerchantStoreStock{}).Where("state = ?", "available").Count(&count).Error)
	require.EqualValues(t, 2, count)
	require.NoError(t, DB.Model(&MerchantStoreOrder{}).Count(&count).Error)
	require.Zero(t, count)
	p, e := GetPublicMerchantStoreProduct(f.product.ID)
	require.NoError(t, e)
	require.True(t, p.TradingPaused)
}
func TestMerchantStoreGatewayThresholdEncryptionAndExternalSettlement(t *testing.T) {
	f := newStoreFixture(t, "external:epay")
	require.NoError(t, DB.Model(&User{}).Where("id = ?", f.seller.Id).Update("quota", 5000000).Error)
	_, e := SaveMerchantStoreGateway(f.seller.Id, "external:epay", true, `{"key":"gateway-secret"}`)
	require.ErrorIs(t, e, ErrMerchantStoreBalance)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", f.seller.Id).Update("quota", 5000001).Error)
	g, e := SaveMerchantStoreGateway(f.seller.Id, "external:epay", true, `{"key":"gateway-secret"}`)
	require.NoError(t, e)
	require.NotContains(t, g.ConfigCiphertext, "gateway-secret")
	j, e := json.Marshal(g)
	require.NoError(t, e)
	require.NotContains(t, string(j), "secret")
	o, created, e := CreateMerchantStoreOrder(f.checkout("external", "external:epay"))
	require.NoError(t, e)
	require.True(t, created)
	require.True(t, o.FeeHeld)
	storeBalance(t, f.seller.Id, 4995001)
	require.ErrorIs(t, CompleteMerchantStorePayment(o.ID, "trade-1"), ErrMerchantStoreConflict)
	require.NoError(t, BindMerchantStorePaymentQuote(o.ID, 100, "USD", "1"))
	require.NoError(t, BindMerchantStorePaymentQuote(o.ID, 100, "USD", "1"))
	require.ErrorIs(t, BindMerchantStorePaymentQuote(o.ID, 101, "USD", "1"), ErrMerchantStoreConflict)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", f.seller.Id).Update("quota", 0).Error)
	require.NoError(t, CompleteMerchantStorePayment(o.ID, "trade-1"))
	require.NoError(t, CompleteMerchantStorePayment(o.ID, "trade-1"))
	require.ErrorIs(t, CompleteMerchantStorePayment(o.ID, "other-trade"), ErrMerchantStoreConflict)
	storeBalance(t, f.seller.Id, 0)
	storeBalance(t, f.root.Id, 5000)
	storeBalance(t, f.buyer.Id, 10000000)
}
func TestMerchantStorePlatformSettlementFeeHoldAndCancelRace(t *testing.T) {
	f := newStoreFixture(t, "platform:waffo_pancake")
	o, _, e := CreateMerchantStoreOrder(f.checkout("platform", "platform:waffo_pancake"))
	require.NoError(t, e)
	storeBalance(t, f.seller.Id, 9995000)
	require.NoError(t, BindMerchantStorePaymentQuote(o.ID, 671, "CNY", "6.71"))
	require.NoError(t, BindMerchantStorePaymentContext(o.ID, "encrypted-frozen-context"))
	issued, ie := GetMerchantStoreOrder(f.buyer.Id, o.ID)
	require.NoError(t, ie)
	require.True(t, issued.PaymentIssued)
	require.ErrorIs(t, CancelMerchantStoreOrder(f.buyer.Id, o.ID), ErrMerchantStoreConflict)
	require.NoError(t, DB.Model(&MerchantStoreOrder{}).Where("id = ?", o.ID).Update("expires_at", common.GetTimestamp()-1).Error)
	n, e := ExpireMerchantStoreOrders(30)
	require.NoError(t, e)
	require.Equal(t, 1, n)
	reconciled, re := GetMerchantStorePaymentOrder(o.ID)
	require.NoError(t, re)
	require.Equal(t, "reconciliation_pending", reconciled.Status)
	require.NoError(t, CompleteMerchantStorePayment(o.ID, "platform-paid"))
	storeBalance(t, f.seller.Id, 10495000)
	storeBalance(t, f.root.Id, 5000)
}
func TestMerchantStoreUnissuedCancellationRefundsOnce(t *testing.T) {
	f := newStoreFixture(t, "platform:waffo_pancake")
	o, _, e := CreateMerchantStoreOrder(f.checkout("cancel", "platform:waffo_pancake"))
	require.NoError(t, e)
	storeBalance(t, f.seller.Id, 9995000)
	require.NoError(t, CancelMerchantStoreOrder(f.buyer.Id, o.ID))
	require.NoError(t, CancelMerchantStoreOrder(f.buyer.Id, o.ID))
	storeBalance(t, f.seller.Id, 10000000)
	var count int64
	require.NoError(t, DB.Model(&MerchantStoreStock{}).Where("state = ?", "available").Count(&count).Error)
	require.EqualValues(t, 2, count)
}
func TestMerchantStoreClaimPrivacyLoginCodeAndRecovery(t *testing.T) {
	f := newStoreFixture(t, "balance")
	o, _, e := CreateMerchantStoreOrder(f.checkout("claim", "balance"))
	require.NoError(t, e)
	token, e := GetMerchantStoreOrderPickupToken(f.buyer.Id, o.ID)
	require.NoError(t, e)
	require.Len(t, token, 43)
	j, e := json.Marshal(o)
	require.NoError(t, e)
	for _, secret := range []string{token, "safe-pickup-code", "CARD-SECRET", "pickup_token", "gateway_snapshot"} {
		require.NotContains(t, string(j), secret)
	}
	meta, e := InspectMerchantStoreClaim(token)
	require.NoError(t, e)
	require.Equal(t, "paid", meta.Status)
	require.True(t, meta.PickupLoginRequired)
	require.True(t, meta.PickupCodeRequired)
	j, e = json.Marshal(meta)
	require.NoError(t, e)
	require.NotContains(t, string(j), "buyer")
	_, e = ClaimMerchantStoreOrder(token, "wrong-code", f.buyer.Id)
	require.ErrorIs(t, e, ErrMerchantStoreDenied)
	_, e = ClaimMerchantStoreOrder(token, "safe-pickup-code", f.seller.Id)
	require.ErrorIs(t, e, ErrMerchantStoreDenied)
	claim, e := ClaimMerchantStoreOrder(token, "safe-pickup-code", f.buyer.Id)
	require.NoError(t, e)
	require.Equal(t, []string{"CARD-SECRET-FIRST"}, claim.Items)
	again, e := ClaimMerchantStoreOrder(token, "safe-pickup-code", f.buyer.Id)
	require.NoError(t, e)
	require.Equal(t, claim.Items, again.Items)
	_, e = GetMerchantStoreOrderPickupToken(f.seller.Id, o.ID)
	require.ErrorIs(t, e, ErrMerchantStoreDenied)
	_, e = ClaimMerchantStoreOrder(strings.Repeat("a", 43), "", 0)
	require.ErrorIs(t, e, ErrMerchantStoreDenied)
}
func TestMerchantStoreConcurrentCheckoutDoesNotOversellAndReplayOnce(t *testing.T) {
	f := newStoreFixture(t, "balance")
	require.NoError(t, DB.Where("product_id = ?", f.product.ID).Delete(&MerchantStoreStock{}).Error)
	_, e := AddMerchantStoreStock(f.seller.Id, f.product.ID, []string{"ONLY-CARD"})
	require.NoError(t, e)
	var wg sync.WaitGroup
	var mu sync.Mutex
	made := 0
	failures := 0
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, created, e := CreateMerchantStoreOrder(f.checkout("same-key", "balance"))
			mu.Lock()
			defer mu.Unlock()
			if e != nil {
				failures++
			}
			if created {
				made++
			}
		}()
	}
	wg.Wait()
	require.Equal(t, 1, made)
	require.Zero(t, failures)
	storeBalance(t, f.buyer.Id, 9500000)
	_, _, e = CreateMerchantStoreOrder(f.checkout("different-key", "balance"))
	require.ErrorIs(t, e, ErrMerchantStoreStock)
}
func TestMerchantStoreRootSellerFeeExemptAndSafeIntegerBoundary(t *testing.T) {
	f := newStoreFixture(t, "balance")
	require.NoError(t, DB.Model(&MerchantStoreProduct{}).Where("id = ?", f.product.ID).Update("seller_id", f.root.Id).Error)
	_, e := SaveMerchantStoreGateway(f.root.Id, "balance", true, "")
	require.NoError(t, e)
	in := f.checkout("root-seller", "balance")
	o, _, e := CreateMerchantStoreOrder(in)
	require.NoError(t, e)
	require.Zero(t, o.FeeQuota)
	storeBalance(t, f.root.Id, 500000)
	require.NoError(t, DB.Model(&MerchantStoreProduct{}).Where("id = ?", f.product.ID).Update("price_quota", common.MaxWalletQuota).Error)
	in = f.checkout("overflow", "balance")
	in.Quantity = 2
	_, _, e = CreateMerchantStoreOrder(in)
	require.ErrorIs(t, e, ErrMerchantStoreInput)
}
func TestMerchantStorePromotionRanksAndSnapshotsConfig(t *testing.T) {
	f := newStoreFixture(t, "balance")
	p, e := PurchaseMerchantStorePromotion(f.seller.Id, f.product.ID, 1, "promote")
	require.NoError(t, e)
	require.Equal(t, 500000, p.Quota)
	require.Greater(t, p.ExpiresAt, common.GetTimestamp())
	storeBalance(t, f.seller.Id, 9500000)
	storeBalance(t, f.root.Id, 500000)
	_, e = PurchaseMerchantStorePromotion(f.seller.Id, f.product.ID, 1, "promote")
	require.NoError(t, e)
	storeBalance(t, f.seller.Id, 9500000)
	rows, e := ListPublicMerchantStoreProducts("", 0, 30)
	require.NoError(t, e)
	require.Len(t, rows, 1)
	require.Empty(t, rows[0].ReviewNote)
	require.NoError(t, SetMerchantStoreConfig(f.root.Id, MerchantStoreConfig{FeeBPS: 500, RecipientID: f.root.Id, PromotionQuota: 100000}))
	in := f.checkout("new-fee", "balance")
	o, _, e := CreateMerchantStoreOrder(in)
	require.NoError(t, e)
	require.Equal(t, 25000, o.FeeQuota)
}
func TestMerchantStoreEmailOutboxLeaseIsolation(t *testing.T) {
	f := newStoreFixture(t, "balance")
	o, _, e := CreateMerchantStoreOrder(f.checkout("email", "balance"))
	require.NoError(t, e)
	now := common.GetTimestamp()
	row, e := ClaimMerchantStoreEmailDelivery(now)
	require.NoError(t, e)
	require.Equal(t, o.ID, row.OrderID)
	require.Len(t, row.LeaseToken, 43)
	require.ErrorIs(t, AckMerchantStoreEmailDelivery(row.ID, strings.Repeat("a", 43)), ErrMerchantStoreConflict)
	require.NoError(t, RetryMerchantStoreEmailDelivery(row.ID, row.LeaseToken, now, "smtp_unavailable"))
	_, e = ClaimMerchantStoreEmailDelivery(now)
	require.Error(t, e)
	next, e := ClaimMerchantStoreEmailDelivery(now + 10000)
	require.NoError(t, e)
	require.NotEqual(t, row.LeaseToken, next.LeaseToken)
	require.ErrorIs(t, AckMerchantStoreEmailDelivery(row.ID, row.LeaseToken), ErrMerchantStoreConflict)
	require.NoError(t, AckMerchantStoreEmailDelivery(next.ID, next.LeaseToken))
}
func TestMerchantStoreWalletOverflowRollsBackWholeSale(t *testing.T) {
	f := newStoreFixture(t, "balance")
	require.NoError(t, DB.Model(&User{}).Where("id = ?", f.root.Id).Update("quota", common.MaxWalletQuota).Error)
	_, _, e := CreateMerchantStoreOrder(f.checkout("rollback", "balance"))
	require.ErrorIs(t, e, ErrWalletQuotaOutOfRange)
	storeBalance(t, f.buyer.Id, 10000000)
	storeBalance(t, f.seller.Id, 10000000)
	var n int64
	require.NoError(t, DB.Model(&MerchantStoreOrder{}).Count(&n).Error)
	require.Zero(t, n)
	require.NoError(t, DB.Model(&MerchantStoreStock{}).Where("state = ?", "available").Count(&n).Error)
	require.EqualValues(t, 2, n)
}

func TestMerchantStoreProviderReceiptCannotSettleTwoOrders(t *testing.T) {
	f := newStoreFixture(t, "platform:waffo_pancake")
	first, _, e := CreateMerchantStoreOrder(f.checkout("one", "platform:waffo_pancake"))
	require.NoError(t, e)
	require.NoError(t, BindMerchantStorePaymentQuote(first.ID, 100, "USD", "1"))
	require.NoError(t, CompleteMerchantStorePayment(first.ID, "single-provider-receipt"))
	second, _, e := CreateMerchantStoreOrder(f.checkout("two", "platform:waffo_pancake"))
	require.NoError(t, e)
	require.NoError(t, BindMerchantStorePaymentQuote(second.ID, 100, "USD", "1"))
	require.ErrorIs(t, CompleteMerchantStorePayment(second.ID, "single-provider-receipt"), ErrMerchantStoreConflict)
	remaining, e := GetMerchantStorePaymentOrder(second.ID)
	require.NoError(t, e)
	require.Equal(t, "pending", remaining.Status)
	storeBalance(t, f.root.Id, 5000)
}
func TestMerchantStoreGatewayDisabledAndProductMethodsPreserved(t *testing.T) {
	f := newStoreFixture(t, "balance")
	_, e := SaveMerchantStoreGateway(f.seller.Id, "balance", false, "")
	require.NoError(t, e)
	_, _, e = CreateMerchantStoreOrder(f.checkout("disabled", "balance"))
	require.ErrorIs(t, e, ErrMerchantStoreUnavailable)
	public, e := GetPublicMerchantStoreProduct(f.product.ID)
	require.NoError(t, e)
	require.Empty(t, public.PaymentMethods)
	require.True(t, public.TradingPaused)
	seller, e := GetMerchantStoreProduct(f.seller.Id, f.product.ID)
	require.NoError(t, e)
	require.Equal(t, []string{"balance"}, seller.PaymentMethods)
	secret, e := GetMerchantStoreGatewaySecret(f.seller.Id, "external:epay")
	require.NoError(t, e)
	require.Empty(t, secret)
}
func TestMerchantStoreAdministratorPromotionSettingCannotChangeFees(t *testing.T) {
	f := newStoreFixture(t, "balance")
	require.ErrorIs(t, SetMerchantStorePromotionPrice(f.seller.Id, 750000), ErrMerchantStoreDenied)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", f.seller.Id).Update("role", common.RoleAdminUser).Error)
	require.NoError(t, SetMerchantStorePromotionPrice(f.seller.Id, 750000))
	c, e := GetMerchantStoreConfig()
	require.NoError(t, e)
	require.Equal(t, 750000, c.PromotionQuota)
	require.Equal(t, 100, c.FeeBPS)
	require.Equal(t, f.root.Id, c.RecipientID)
}
func TestMerchantStoreRandomAnonymousDeliveryHasNoDuplicates(t *testing.T) {
	f := newStoreFixture(t, "balance")
	require.NoError(t, DB.Model(&MerchantStoreProduct{}).Where("id = ?", f.product.ID).Updates(map[string]any{"pickup_login_required": false, "pickup_code_required": false, "delivery_strategy": "random"}).Error)
	in := f.checkout("random", "balance")
	in.Quantity = 2
	in.PickupCode = ""
	o, _, e := CreateMerchantStoreOrder(in)
	require.NoError(t, e)
	token, e := GetMerchantStoreOrderPickupToken(f.buyer.Id, o.ID)
	require.NoError(t, e)
	claim, e := ClaimMerchantStoreOrder(token, "", 0)
	require.NoError(t, e)
	require.ElementsMatch(t, []string{"CARD-SECRET-FIRST", "CARD-SECRET-SECOND"}, claim.Items)
}
func TestMerchantStoreExternalIssuedClosureRefundsAndNeverPaysAfterClose(t *testing.T) {
	f := newStoreFixture(t, "platform:waffo_pancake")
	o, _, e := CreateMerchantStoreOrder(f.checkout("closed", "platform:waffo_pancake"))
	require.NoError(t, e)
	require.NoError(t, BindMerchantStorePaymentQuote(o.ID, 100, "USD", "1"))
	require.NoError(t, BindMerchantStorePaymentContext(o.ID, "v1:opaque-trusted-context"))
	require.ErrorIs(t, CancelMerchantStoreOrder(f.buyer.Id, o.ID), ErrMerchantStoreConflict)
	require.NoError(t, ConfirmMerchantStoreOrderPaymentClosed(o.ID, "verified-provider-closed"))
	storeBalance(t, f.seller.Id, 10000000)
	require.ErrorIs(t, CompleteMerchantStorePayment(o.ID, "late-paid-invalid"), ErrMerchantStoreConflict)
}

func TestMerchantStorePendingLimitsReplayAndReconciliation(t *testing.T) {
	f := newStoreFixture(t, "platform:waffo_pancake")
	first, _, e := CreateMerchantStoreOrder(f.checkout("first", "platform:waffo_pancake"))
	require.NoError(t, e)
	_, _, e = CreateMerchantStoreOrder(f.checkout("same-product-new-key", "platform:waffo_pancake"))
	require.ErrorIs(t, e, ErrMerchantStorePendingLimit)
	for i := 0; i < 3; i++ {
		p, e := SaveMerchantStoreProduct(f.seller.Id, "", MerchantStoreProductInput{Title: "Another product", PriceQuota: 500000, PaymentMethods: []string{"platform:waffo_pancake"}})
		require.NoError(t, e)
		require.NoError(t, SubmitMerchantStoreProduct(f.seller.Id, p.ID))
		require.NoError(t, ReviewMerchantStoreProduct(f.root.Id, p.ID, true, ""))
		_, e = AddMerchantStoreStock(f.seller.Id, p.ID, []string{"text"})
		require.NoError(t, e)
		in := f.checkout(p.ID, "platform:waffo_pancake")
		in.ProductID = p.ID
		in.PickupCode = ""
		_, _, e = CreateMerchantStoreOrder(in)
		if i < 2 {
			require.NoError(t, e)
		} else {
			require.ErrorIs(t, e, ErrMerchantStorePendingLimit)
		}
	}
	replay, made, e := CreateMerchantStoreOrder(f.checkout("first", "platform:waffo_pancake"))
	require.NoError(t, e)
	require.False(t, made)
	require.Equal(t, first.ID, replay.ID)
}
func TestMerchantStoreStockEnvelopeCannotBeCopiedAcrossEntries(t *testing.T) {
	f := newStoreFixture(t, "balance")
	o, _, e := CreateMerchantStoreOrder(f.checkout("bound", "balance"))
	require.NoError(t, e)
	var other MerchantStoreStock
	require.NoError(t, DB.Where("product_id = ? AND state = ?", f.product.ID, "available").First(&other).Error)
	require.NoError(t, DB.Model(&MerchantStoreStock{}).Where("order_id = ?", o.ID).Update("ciphertext", other.Ciphertext).Error)
	token, e := GetMerchantStoreOrderPickupToken(f.buyer.Id, o.ID)
	require.NoError(t, e)
	_, e = ClaimMerchantStoreOrder(token, "safe-pickup-code", f.buyer.Id)
	require.Error(t, e)
}
func TestMerchantStoreUnpaidQuantityBoundAndActualProviderExpiry(t *testing.T) {
	f := newStoreFixture(t, "platform:waffo_pancake")
	in := f.checkout("bulk", "platform:waffo_pancake")
	in.Quantity = 101
	_, _, e := CreateMerchantStoreOrder(in)
	require.ErrorIs(t, e, ErrMerchantStoreInput)
	o, _, e := CreateMerchantStoreOrder(f.checkout("expiry", "platform:waffo_pancake"))
	require.NoError(t, e)
	expiry := common.GetTimestamp() + 1200
	require.NoError(t, BindMerchantStoreCheckoutSession(o.ID, "https://checkout.example.test/session", "remote-session", expiry))
	reloaded, e := GetMerchantStorePaymentOrder(o.ID)
	require.NoError(t, e)
	require.Equal(t, expiry, reloaded.ProviderCheckoutExpiresAt)
	require.NoError(t, BindMerchantStoreCheckoutSession(o.ID, "https://checkout.example.test/session", "remote-session", expiry))
	require.ErrorIs(t, BindMerchantStoreCheckoutSession(o.ID, "https://checkout.example.test/session", "remote-session", expiry+1), ErrMerchantStoreConflict)
}

func TestMerchantStoreExpiredPromotionDoesNotOutrankNewListings(t *testing.T) {
	f := newStoreFixture(t, "balance")
	p, e := SaveMerchantStoreProduct(f.seller.Id, "", MerchantStoreProductInput{Title: "New%_literal", PriceQuota: 500000, PaymentMethods: []string{"balance"}})
	require.NoError(t, e)
	require.NoError(t, SubmitMerchantStoreProduct(f.seller.Id, p.ID))
	require.NoError(t, ReviewMerchantStoreProduct(f.root.Id, p.ID, true, ""))
	require.NoError(t, DB.Model(&MerchantStoreProduct{}).Where("id = ?", p.ID).Update("created_at", common.GetTimestamp()+1).Error)
	require.NoError(t, DB.Model(&MerchantStoreProduct{}).Where("id = ?", f.product.ID).Update("promotion_expires_at", common.GetTimestamp()-1).Error)
	rows, e := ListPublicMerchantStoreProducts("", 0, 30)
	require.NoError(t, e)
	require.Equal(t, p.ID, rows[0].ID)
	rows, e = ListPublicMerchantStoreProducts("%_", 0, 30)
	require.NoError(t, e)
	require.Len(t, rows, 1)
	require.Equal(t, p.ID, rows[0].ID)
	require.NoError(t, DB.Model(&MerchantStoreProduct{}).Where("id = ?", f.product.ID).Update("promotion_expires_at", common.GetTimestamp()+1000).Error)
	rows, e = ListPublicMerchantStoreProducts("", 0, 30)
	require.NoError(t, e)
	require.Equal(t, f.product.ID, rows[0].ID)
}

func TestMerchantStoreIssuedPaymentFulfillsAfterSellerDisabled(t *testing.T) {
	for _, method := range []string{"platform:waffo_pancake", "external:epay"} {
		t.Run(method, func(t *testing.T) {
			f := newStoreFixture(t, method)
			if method == "external:epay" {
				_, e := SaveMerchantStoreGateway(f.seller.Id, method, true, `{"key":"fixture"}`)
				require.NoError(t, e)
			}
			o, _, e := CreateMerchantStoreOrder(f.checkout("before-disable", method))
			require.NoError(t, e)
			require.NoError(t, BindMerchantStorePaymentQuote(o.ID, 100, "USD", "1"))
			require.NoError(t, BindMerchantStorePaymentContext(o.ID, "v1:trusted-frozen"))
			require.NoError(t, DB.Model(&User{}).Where("id = ?", f.seller.Id).Update("status", common.UserStatusDisabled).Error)
			_, _, e = CreateMerchantStoreOrder(f.checkout("after-disable", method))
			require.ErrorIs(t, e, ErrMerchantStoreDenied)
			require.NoError(t, CompleteMerchantStorePayment(o.ID, "paid-before-disable"))
			require.NoError(t, CompleteMerchantStorePayment(o.ID, "paid-before-disable"))
			storeBalance(t, f.root.Id, 5000)
			expected := 9995000
			if method == "platform:waffo_pancake" {
				expected += 500000
			}
			storeBalance(t, f.seller.Id, expected)
			token, e := GetMerchantStoreOrderPickupToken(f.buyer.Id, o.ID)
			require.NoError(t, e)
			claim, e := ClaimMerchantStoreOrder(token, "safe-pickup-code", f.buyer.Id)
			require.NoError(t, e)
			require.Equal(t, []string{"CARD-SECRET-FIRST"}, claim.Items)
		})
	}
}
func TestMerchantStoreLinuxDOExplicitRateAndAdministratorIsolation(t *testing.T) {
	f := newStoreFixture(t, "balance")
	c, e := GetMerchantStoreConfig()
	require.NoError(t, e)
	require.Empty(t, c.LinuxDOUnitsPerUSD)
	for _, invalid := range []string{"0", "0.00", "-1", "+1", "NaN", "Infinity", "1e3", " 6.7", "6.7 ", strings.Repeat("1", 65), strings.Repeat("1", 13), "0." + strings.Repeat("1", 13)} {
		c.LinuxDOUnitsPerUSD = invalid
		require.ErrorIs(t, SetMerchantStoreConfig(f.root.Id, c), ErrMerchantStoreInput)
	}
	c.LinuxDOUnitsPerUSD = "7.142857"
	require.NoError(t, SetMerchantStoreConfig(f.root.Id, c))
	require.NoError(t, DB.Model(&User{}).Where("id = ?", f.seller.Id).Update("role", common.RoleAdminUser).Error)
	require.NoError(t, SetMerchantStorePromotionPrice(f.seller.Id, 600000))
	c, e = GetMerchantStoreConfig()
	require.NoError(t, e)
	require.Equal(t, "7.142857", c.LinuxDOUnitsPerUSD)
	require.Equal(t, 100, c.FeeBPS)
	require.Equal(t, f.root.Id, c.RecipientID)
	c.LinuxDOUnitsPerUSD = ""
	require.NoError(t, SetMerchantStoreConfig(f.root.Id, c))
}

func TestMerchantStoreIssuedSessionResponseSurvivesLocalExpiryReconciliation(t *testing.T) {
	f := newStoreFixture(t, "platform:waffo_pancake")
	o, _, e := CreateMerchantStoreOrder(f.checkout("slow-provider", "platform:waffo_pancake"))
	require.NoError(t, e)
	require.NoError(t, BindMerchantStorePaymentContext(o.ID, "v1:issued-context"))
	require.NoError(t, DB.Model(&MerchantStoreOrder{}).Where("id = ?", o.ID).Update("expires_at", common.GetTimestamp()-1).Error)
	n, e := ExpireMerchantStoreOrders(30)
	require.NoError(t, e)
	require.Equal(t, 1, n)
	expiry := common.GetTimestamp() + 1200
	require.NoError(t, BindMerchantStoreCheckoutSession(o.ID, "https://checkout.example.test/slow", "provider-slow", expiry))
	fresh, e := GetMerchantStorePaymentOrder(o.ID)
	require.NoError(t, e)
	require.Equal(t, "reconciliation_pending", fresh.Status)
	require.Equal(t, expiry, fresh.ProviderCheckoutExpiresAt)
	require.Equal(t, "provider-slow", fresh.ProviderSessionID)
	require.NoError(t, BindMerchantStoreCheckoutSession(o.ID, "https://checkout.example.test/slow", "provider-slow", expiry))
	require.ErrorIs(t, BindMerchantStoreCheckoutSession(o.ID, "https://checkout.example.test/replaced", "provider-other", expiry), ErrMerchantStoreConflict)
}
