package model

import (
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
)

func storeIssuedFixtureOrder(t *testing.T, f storeFixture, key, method, scope string) *MerchantStoreOrder {
	t.Helper()
	o, _, e := CreateMerchantStoreOrder(f.checkout(key, method))
	require.NoError(t, e)
	require.NoError(t, BindMerchantStorePaymentQuote(o.ID, 100, "USD", "1"))
	require.NoError(t, BindMerchantStorePaymentContext(o.ID, "v1:opaque-context-"+key, scope))
	return o
}
func TestMerchantStoreVerifiedPaymentIssuePersistsAndRetriesOriginalReceipt(t *testing.T) {
	f := newStoreFixture(t, "platform:waffo_pancake")
	scope := storeHash("pancake:public-account:sandbox")
	o := storeIssuedFixtureOrder(t, f, "root-unavailable", "platform:waffo_pancake", scope)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", f.root.Id).Update("role", common.RoleCommonUser).Error)
	require.ErrorIs(t, CompleteMerchantStorePayment(o.ID, "true-paid"), ErrMerchantStoreDenied)
	require.NoError(t, RecordMerchantStoreVerifiedPaymentIssue(o.ID, "true-paid", "settlement_unavailable"))
	require.NoError(t, RecordMerchantStoreVerifiedPaymentIssue(o.ID, "true-paid", "settlement_unavailable"))
	fresh, e := GetMerchantStorePaymentOrder(o.ID)
	require.NoError(t, e)
	require.Equal(t, "reconciliation_pending", fresh.Status)
	require.Equal(t, "pending", fresh.PaymentIssueOriginalStatus)
	require.Equal(t, "settlement_unavailable", fresh.PaymentIssueCode)
	require.Greater(t, fresh.VerifiedPaymentIssueAt, int64(0))
	require.True(t, fresh.FeeHeld)
	require.Equal(t, "true-paid", fresh.ProviderTradeID)
	storeBalance(t, f.seller.Id, 9995000)
	storeBalance(t, f.root.Id, 0)
	require.ErrorIs(t, RecordMerchantStoreVerifiedPaymentIssue(o.ID, "different-paid", "settlement_unavailable"), ErrMerchantStoreConflict)
	require.ErrorIs(t, ConfirmMerchantStoreOrderPaymentClosed(o.ID, "stale-empty-provider-ledger"), ErrMerchantStoreConflict)
	require.ErrorIs(t, CancelMerchantStoreOrder(f.buyer.Id, o.ID), ErrMerchantStoreConflict)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", f.root.Id).Update("role", common.RoleRootUser).Error)
	require.NoError(t, CompleteMerchantStorePayment(o.ID, "true-paid"))
	require.NoError(t, RecordMerchantStoreVerifiedPaymentIssue(o.ID, "true-paid", "settlement_unavailable"))
	fresh, e = GetMerchantStorePaymentOrder(o.ID)
	require.NoError(t, e)
	require.Equal(t, "paid", fresh.Status)
	require.Empty(t, fresh.PaymentIssueCode)
	require.Empty(t, fresh.PaymentIssueOriginalStatus)
	require.Zero(t, fresh.VerifiedPaymentIssueAt)
	storeBalance(t, f.seller.Id, 10495000)
	storeBalance(t, f.root.Id, 5000)
	var n int64
	require.NoError(t, DB.Model(&MerchantStorePaymentReceipt{}).Count(&n).Error)
	require.EqualValues(t, 1, n)
}
func TestMerchantStoreVerifiedPaymentAfterClosedRecordsEvidenceWithoutRebilling(t *testing.T) {
	f := newStoreFixture(t, "platform:waffo_pancake")
	o := storeIssuedFixtureOrder(t, f, "already-closed", "platform:waffo_pancake", storeHash("provider-account"))
	require.NoError(t, ConfirmMerchantStoreOrderPaymentClosed(o.ID, "verified-closed"))
	storeBalance(t, f.seller.Id, 10000000)
	require.ErrorIs(t, RecordMerchantStoreVerifiedPaymentIssue(o.ID, "late-paid", "arbitrary error with secret"), ErrMerchantStoreInput)
	require.NoError(t, RecordMerchantStoreVerifiedPaymentIssue(o.ID, "late-paid", "settlement_unavailable"))
	fresh, e := GetMerchantStorePaymentOrder(o.ID)
	require.NoError(t, e)
	require.Equal(t, "cancelled", fresh.PaymentIssueOriginalStatus)
	require.Equal(t, "verified_payment_after_closed", fresh.PaymentIssueCode)
	require.Equal(t, "reconciliation_pending", fresh.Status)
	require.False(t, fresh.FeeHeld)
	require.ErrorIs(t, CompleteMerchantStorePayment(o.ID, "late-paid"), ErrMerchantStoreConflict)
	storeBalance(t, f.seller.Id, 10000000)
	storeBalance(t, f.root.Id, 0)
	var n int64
	require.NoError(t, DB.Model(&MerchantStoreStock{}).Where("state = ?", "available").Count(&n).Error)
	require.EqualValues(t, 2, n)
}
func TestMerchantStoreVerifiedPaymentMissingSellerKeepsEvidenceAndInventory(t *testing.T) {
	f := newStoreFixture(t, "platform:waffo_pancake")
	o := storeIssuedFixtureOrder(t, f, "missing-seller", "platform:waffo_pancake", storeHash("provider-account"))
	require.NoError(t, DB.Delete(&User{}, f.seller.Id).Error)
	require.ErrorIs(t, CompleteMerchantStorePayment(o.ID, "received-money"), ErrMerchantStoreDenied)
	require.NoError(t, RecordMerchantStoreVerifiedPaymentIssue(o.ID, "received-money", "seller_unavailable"))
	fresh, e := GetMerchantStorePaymentOrder(o.ID)
	require.NoError(t, e)
	require.True(t, fresh.FeeHeld)
	require.Equal(t, "reconciliation_pending", fresh.Status)
	var reserved int64
	require.NoError(t, DB.Model(&MerchantStoreStock{}).Where("order_id = ? AND state = ?", o.ID, "reserved").Count(&reserved).Error)
	require.EqualValues(t, 1, reserved)
	storeBalance(t, f.root.Id, 0)
}
func storeScopeSecondProduct(t *testing.T, f storeFixture, owner int, method string) *MerchantStoreProduct {
	t.Helper()
	p, e := SaveMerchantStoreProduct(owner, "", MerchantStoreProductInput{Title: "Second scoped product", PriceQuota: 500000, PaymentMethods: []string{method}})
	require.NoError(t, e)
	require.NoError(t, SubmitMerchantStoreProduct(owner, p.ID))
	require.NoError(t, ReviewMerchantStoreProduct(f.root.Id, p.ID, true, ""))
	_, e = AddMerchantStoreStock(owner, p.ID, []string{"scoped-item"})
	require.NoError(t, e)
	config := ""
	if method == "external:epay" {
		config = `{"key":"test-private-material"}`
	}
	_, e = SaveMerchantStoreGateway(owner, method, true, config)
	require.NoError(t, e)
	return p
}
func storeScopeOrder(t *testing.T, f storeFixture, p *MerchantStoreProduct, key, method, scope string) *MerchantStoreOrder {
	t.Helper()
	in := f.checkout(key, method)
	in.ProductID = p.ID
	in.PickupCode = ""
	o, _, e := CreateMerchantStoreOrder(in)
	require.NoError(t, e)
	require.NoError(t, BindMerchantStorePaymentQuote(o.ID, 100, "USD", "1"))
	require.NoError(t, BindMerchantStorePaymentContext(o.ID, "v1:context-"+key, scope))
	return o
}
func TestMerchantStorePaymentScopePlatformAccountIsGlobalAcrossSellers(t *testing.T) {
	f := newStoreFixture(t, "platform:waffo_pancake")
	scope := storeHash("pancake:actual-public-account:sandbox")
	first := storeIssuedFixtureOrder(t, f, "first-seller", "platform:waffo_pancake", scope)
	require.NoError(t, CompleteMerchantStorePayment(first.ID, "shared-transaction"))
	seller2 := marketTestUser(t, DB, "another-seller", 10000000, common.RoleCommonUser)
	product2 := storeScopeSecondProduct(t, f, seller2.Id, "platform:waffo_pancake")
	second := storeScopeOrder(t, f, product2, "second-seller", "platform:waffo_pancake", scope)
	require.ErrorIs(t, CompleteMerchantStorePayment(second.ID, "shared-transaction"), ErrMerchantStoreConflict)
	require.ErrorIs(t, RecordMerchantStoreVerifiedPaymentIssue(second.ID, "shared-transaction", "settlement_conflict"), ErrMerchantStoreConflict)
	storeBalance(t, f.root.Id, 5000)
	storeBalance(t, seller2.Id, 9995000)
}
func TestMerchantStorePaymentScopeExternalAccountAndSellerIsolation(t *testing.T) {
	for _, scenario := range []string{"account-changed", "private-key-rotated", "different-seller"} {
		t.Run(scenario, func(t *testing.T) {
			f := newStoreFixture(t, "external:epay")
			_, e := SaveMerchantStoreGateway(f.seller.Id, "external:epay", true, `{"key":"private-key-one"}`)
			require.NoError(t, e)
			publicScope := storeHash("https://provider.example:443/path:public-partner-1")
			first := storeIssuedFixtureOrder(t, f, "first-account", "external:epay", publicScope)
			require.NoError(t, CompleteMerchantStorePayment(first.ID, "provider-reused-id"))
			owner := f.seller.Id
			scope := publicScope
			if scenario == "account-changed" {
				scope = storeHash("https://provider.example:443/path:public-partner-2")
			}
			if scenario == "different-seller" {
				seller := marketTestUser(t, DB, "external-seller", 10000000, common.RoleCommonUser)
				owner = seller.Id
			}
			secondProduct := storeScopeSecondProduct(t, f, owner, "external:epay")
			_, e = SaveMerchantStoreGateway(owner, "external:epay", true, `{"key":"private-key-two"}`)
			require.NoError(t, e)
			second := storeScopeOrder(t, f, secondProduct, "second-account", "external:epay", scope)
			e = CompleteMerchantStorePayment(second.ID, "provider-reused-id")
			if scenario == "private-key-rotated" {
				require.ErrorIs(t, e, ErrMerchantStoreConflict)
				storeBalance(t, f.root.Id, 5000)
			} else {
				require.NoError(t, e)
				storeBalance(t, f.root.Id, 10000)
			}
		})
	}
}
func TestMerchantStorePaymentContextScopeCannotBeReplaced(t *testing.T) {
	f := newStoreFixture(t, "platform:waffo_pancake")
	o, _, e := CreateMerchantStoreOrder(f.checkout("scope-immutable", "platform:waffo_pancake"))
	require.NoError(t, e)
	scope := storeHash("public-account")
	require.NoError(t, BindMerchantStorePaymentContext(o.ID, "v1:opaque", scope))
	require.NoError(t, BindMerchantStorePaymentContext(o.ID, "v1:opaque", scope))
	require.ErrorIs(t, BindMerchantStorePaymentContext(o.ID, "v1:opaque", storeHash("other-public-account")), ErrMerchantStoreConflict)
	require.ErrorIs(t, BindMerchantStorePaymentContext(o.ID, "v1:opaque", "not-a-hash"), ErrMerchantStoreInput)
}
