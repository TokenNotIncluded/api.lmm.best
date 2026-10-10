package model

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func storeGuestCheckoutFixture(t *testing.T, emailRequired bool) (storeFixture, *MerchantStoreTermsView) {
	t.Helper()
	f := newStoreFixture(t, "platform:waffo_pancake", "balance")
	storeAccessActivateTest(t)
	terms, err := SaveMerchantStoreSellerTerms(f.seller.Id, MerchantStoreTermsInput{Content: "Cards are delivered for the selected specification. Contact this merchant for support."})
	require.NoError(t, err)
	public, no := "public", false
	in := storeModeInput(f.product, nil)
	in.Visibility, in.PurchaseLoginRequired = &public, &no
	in.PickupLoginRequired, in.EmailPickupLink = false, emailRequired
	limit := int64(1)
	in.MaxQuantityPerBuyer = &limit
	f.product, err = SaveMerchantStoreProduct(f.seller.Id, f.product.ID, in)
	require.NoError(t, err)
	require.NoError(t, SubmitMerchantStoreProduct(f.seller.Id, f.product.ID))
	require.NoError(t, ReviewMerchantStoreProduct(f.root.Id, f.product.ID, true, ""))
	return f, terms
}

func storeGuestCheckoutInput(f storeFixture, terms *MerchantStoreTermsView, token, key, method string) MerchantStoreCheckoutInput {
	return MerchantStoreCheckoutInput{ProductID: f.product.ID, Quantity: 1, GuestToken: token, RequestKey: key, PaymentMethod: method, PickupCode: "private-guest-code", SellerTermsVersion: terms.Version, AcceptSellerTerms: true}
}

func TestMerchantStoreGuestFreeCheckoutUsesRealConsentAndNoWallet(t *testing.T) {
	f, terms := storeGuestCheckoutFixture(t, false)
	guest, token := storeAccessHistoricalGuest(t)
	bps, uses := 10000, int64(1)
	promotion, err := SaveMerchantStoreDiscountCode(f.seller.Id, f.product.ID, "", MerchantStoreDiscountCodeInput{DiscountBPS: &bps, MaxUses: &uses})
	require.NoError(t, err)
	quote, err := QuoteMerchantStoreDiscountCodeForViewer(0, token, f.product.ID, promotion.Code, "", 1)
	require.NoError(t, err)
	require.True(t, quote.CheckoutAllowed)
	require.True(t, quote.Free)
	require.Equal(t, []string{"free"}, quote.PaymentMethods)
	publicQuote, err := QuoteMerchantStoreDiscountCode(0, f.product.ID, promotion.Code, "", 1)
	require.NoError(t, err)
	require.False(t, publicQuote.CheckoutAllowed, "a public offer is not guest authorization")
	_, err = QuoteMerchantStoreDiscountCodeForViewer(0, "invalid-guest-proof", f.product.ID, promotion.Code, "", 1)
	require.ErrorIs(t, err, ErrMerchantStoreDenied)
	_, err = QuoteMerchantStoreDiscountCodeForViewer(f.buyer.Id, token, f.product.ID, promotion.Code, "", 1)
	require.ErrorIs(t, err, ErrMerchantStoreDenied, "mixed member/guest authority is never inferred")
	in := storeGuestCheckoutInput(f, terms, token, "guest-gift", "free")
	in.PromotionCode, in.AcceptSellerTerms = promotion.Code, false
	_, _, err = CreateMerchantStoreOrder(in)
	require.ErrorIs(t, err, ErrMerchantStoreSellerTerms)
	in.AcceptSellerTerms = true
	_, _, err = CreateMerchantStoreOrder(in)
	require.ErrorIs(t, err, ErrMerchantStoreDisclaimer)
	var agreements int64
	require.NoError(t, DB.Model(&MerchantStoreTermsAcceptance{}).Where("kind = 'seller'").Count(&agreements).Error)
	require.Zero(t, agreements, "a failed checkout rolls back agreement and inventory together")
	require.NoError(t, AcceptMerchantStoreGuestDisclaimer(token, MerchantStoreDisclaimerVersion))
	o, made, err := CreateMerchantStoreOrder(in)
	require.NoError(t, err)
	require.True(t, made)
	require.Equal(t, guest.ID, o.GuestID)
	require.Zero(t, o.BuyerID)
	require.Equal(t, "paid", o.Status)
	require.Positive(t, o.PaidAt)
	require.Zero(t, o.PriceQuota)
	require.Zero(t, o.FeeQuota)
	require.False(t, o.FeeHeld)
	require.Empty(t, o.GatewaySnapshot)
	require.Empty(t, o.ProviderSessionID)
	require.Equal(t, terms.Version, o.SellerTermsVersion)
	require.Equal(t, terms.Content, o.SellerTermsContent)
	storeBalance(t, f.seller.Id, 10000000)
	storeBalance(t, f.buyer.Id, 10000000)
	storeBalance(t, f.root.Id, 0)
	var transfers int64
	require.NoError(t, DB.Model(&MerchantStoreTransfer{}).Where("order_id = ?", o.ID).Count(&transfers).Error)
	require.Zero(t, transfers)
	pickup, err := GetMerchantStoreGuestPickupToken(token, o.ID)
	require.NoError(t, err)
	view, err := GetMerchantStoreGuestOrder(token, o.ID)
	require.NoError(t, err)
	require.Zero(t, view.ClaimedAt)
	claim, err := ClaimMerchantStoreOrder(pickup, in.PickupCode, 0)
	require.NoError(t, err)
	require.Len(t, claim.Items, 1)
	replay, made, err := CreateMerchantStoreOrder(in)
	require.NoError(t, err)
	require.False(t, made)
	require.Equal(t, o.ID, replay.ID)
	_, err = RequestMerchantStoreRefundWithPickupProof(0, MerchantStoreRefundPickupProof{OrderID: o.ID, Token: pickup, Code: in.PickupCode}, MerchantStoreRefundInput{RequestKey: "zero-principal", Mode: "full", Reason: "Gift has no paid principal"})
	require.Error(t, err)
	var delivered int64
	require.NoError(t, DB.Model(&MerchantStoreStock{}).Where("order_id = ? AND state = 'delivered'", o.ID).Count(&delivered).Error)
	require.EqualValues(t, 1, delivered)
}

func TestMerchantStoreGuestPendingLimitsAndRefundProofAreIsolated(t *testing.T) {
	f, terms := storeGuestCheckoutFixture(t, false)
	a, tokenA := storeAccessHistoricalGuest(t)
	b, tokenB := storeAccessHistoricalGuest(t)
	for _, token := range []string{tokenA, tokenB} {
		require.NoError(t, AcceptMerchantStoreGuestDisclaimer(token, MerchantStoreDisclaimerVersion))
	}
	first, made, err := CreateMerchantStoreOrder(storeGuestCheckoutInput(f, terms, tokenA, "guest-pending", "platform:waffo_pancake"))
	require.NoError(t, err)
	require.True(t, made)
	quote, err := QuoteMerchantStoreDiscountCodeForViewer(0, tokenB, f.product.ID, "", "", 1)
	require.NoError(t, err)
	require.True(t, quote.CheckoutAllowed)
	require.NotContains(t, quote.PaymentMethods, "balance")
	require.Equal(t, 1, quote.MaxQuantity, "another guest's hold does not consume this guest's limit")
	second, made, err := CreateMerchantStoreOrder(storeGuestCheckoutInput(f, terms, tokenB, "guest-pending", "platform:waffo_pancake"))
	require.NoError(t, err)
	require.True(t, made)
	require.NotEqual(t, first.ID, second.ID, "the same request key belongs to separate real guest subjects")
	require.Equal(t, a.ID, first.GuestID)
	require.Equal(t, b.ID, second.GuestID)
	_, err = GetMerchantStoreGuestOrder(tokenA, second.ID)
	require.ErrorIs(t, err, ErrMerchantStoreDenied)
	require.ErrorIs(t, CancelMerchantStoreGuestOrder(tokenA, second.ID), ErrMerchantStoreDenied)
	require.NoError(t, CancelMerchantStoreGuestOrder(tokenA, first.ID))
	require.NoError(t, BindMerchantStorePaymentQuote(second.ID, 100, "USD", "1"))
	// A unit-test adapter attests a verified provider payment; no PSP is called.
	require.NoError(t, CompleteMerchantStorePayment(second.ID, "verified-guest-payment"))
	read, err := FindMerchantStoreGuestOrderByRequestKey(tokenB, "guest-pending")
	require.NoError(t, err)
	require.Equal(t, "paid", read.Status)
	pickup, err := GetMerchantStoreGuestPickupToken(tokenB, second.ID)
	require.NoError(t, err)
	refunds, err := GetMerchantStoreRefundsWithPickupProof(0, MerchantStoreRefundPickupProof{OrderID: second.ID, Token: pickup, Code: "private-guest-code"})
	require.NoError(t, err)
	require.Equal(t, second.ID, refunds.OrderID)
	read, err = GetMerchantStoreGuestOrder(tokenB, second.ID)
	require.NoError(t, err)
	require.Zero(t, read.ClaimedAt, "refund proof authentication never claims cards")
	_, err = GetMerchantStoreRefundsWithPickupProof(0, MerchantStoreRefundPickupProof{OrderID: first.ID, Token: pickup, Code: "private-guest-code"})
	require.ErrorIs(t, err, ErrMerchantStoreDenied)
	revised, err := SaveMerchantStoreSellerTerms(f.seller.Id, MerchantStoreTermsInput{Content: "New merchant support terms", ExpectedVersion: terms.Version})
	require.NoError(t, err)
	require.NotEqual(t, revised.Version, terms.Version)
	_, _, err = CreateMerchantStoreOrder(storeGuestCheckoutInput(f, terms, tokenA, "old-version", "platform:waffo_pancake"))
	require.ErrorIs(t, err, ErrMerchantStoreSellerTerms)
	var updated *MerchantStoreTermsUpdatedError
	require.ErrorAs(t, err, &updated)
	require.Equal(t, "old-version", updated.RequestKey())
	_, err = FindMerchantStoreGuestOrderByRequestKey(tokenA, "old-version")
	require.Error(t, err)
	replay, made, err := CreateMerchantStoreOrder(storeGuestCheckoutInput(f, terms, tokenB, "guest-pending", "platform:waffo_pancake"))
	require.NoError(t, err)
	require.False(t, made)
	require.Equal(t, second.ID, replay.ID)
	require.Equal(t, "paid", replay.Status)
	changed := storeGuestCheckoutInput(f, revised, tokenB, "guest-pending", "platform:waffo_pancake")
	_, _, err = CreateMerchantStoreOrder(changed)
	require.ErrorIs(t, err, ErrMerchantStoreConflict)
	require.False(t, errors.As(err, &updated), "an existing order never grants no-creation proof")
	changed.GuestToken, changed.RequestKey = "invalid-guest-proof", "old-version"
	_, _, err = CreateMerchantStoreOrder(changed)
	require.ErrorIs(t, err, ErrMerchantStoreDenied)
	require.False(t, errors.As(err, &updated), "wrong guest authority cannot obtain proof for a key")
	read, err = GetMerchantStoreGuestOrder(tokenB, second.ID)
	require.NoError(t, err)
	require.Equal(t, terms.Version, read.SellerTermsVersion)
	require.Equal(t, terms.Content, read.SellerTermsContent)

	// A paid balance order survives terms revisions without a second debit.
	member := MerchantStoreCheckoutInput{BuyerID: f.buyer.Id, ProductID: f.product.ID, Quantity: 1, RequestKey: "member-old-terms", PaymentMethod: "balance", PickupCode: "safe-pickup-code", SellerTermsVersion: revised.Version, AcceptSellerTerms: true}
	paid, made, err := CreateMerchantStoreOrder(member)
	require.NoError(t, err)
	require.True(t, made)
	_, err = SaveMerchantStoreSellerTerms(f.seller.Id, MerchantStoreTermsInput{Content: "Third merchant version", ExpectedVersion: revised.Version})
	require.NoError(t, err)
	replay, made, err = CreateMerchantStoreOrder(member)
	require.NoError(t, err)
	require.False(t, made)
	require.Equal(t, paid.ID, replay.ID)
	require.Equal(t, "paid", replay.Status)
	member.RequestKey = "member-rejected-version"
	_, _, err = CreateMerchantStoreOrder(member)
	require.ErrorAs(t, err, &updated)
	require.Equal(t, member.RequestKey, updated.RequestKey())
	_, err = FindMerchantStoreOrderByRequestKey(f.buyer.Id, member.RequestKey)
	require.Error(t, err)
	storeBalance(t, f.buyer.Id, 9500000)
}

func TestMerchantStoreGuestCheckoutFreezesVerifiedEmailAndExplicitFalse(t *testing.T) {
	f, terms := storeGuestCheckoutFixture(t, true)
	_, token := storeAccessHistoricalGuest(t)
	require.NoError(t, AcceptMerchantStoreGuestDisclaimer(token, MerchantStoreDisclaimerVersion))
	in := storeGuestCheckoutInput(f, terms, token, "guest-mail", "platform:waffo_pancake")
	in.PickupEmail = "guest@example.test"
	_, _, err := CreateMerchantStoreOrder(in)
	require.ErrorIs(t, err, ErrMerchantStoreEmailUnverified)
	challenge, err := BeginMerchantStoreGuestEmailVerification(token, in.PickupEmail)
	require.NoError(t, err)
	require.NoError(t, ConfirmMerchantStoreGuestEmailVerification(token, in.PickupEmail, challenge.ChallengeID, challenge.Code))
	o, made, err := CreateMerchantStoreOrder(in)
	require.NoError(t, err)
	require.True(t, made)
	require.True(t, o.EmailPickupLink)
	require.Equal(t, storeHash(in.PickupEmail), o.PickupEmailHash)
	require.NoError(t, BindMerchantStorePaymentQuote(o.ID, 100, "USD", "1"))
	require.NoError(t, CompleteMerchantStorePayment(o.ID, "verified-guest-email-payment"))
	read, err := GetMerchantStoreGuestOrder(token, o.ID)
	require.NoError(t, err)
	require.Equal(t, "pending", read.EmailDeliveryStatus)
	public, no := "public", false
	product, err := SaveMerchantStoreProduct(f.seller.Id, "", MerchantStoreProductInput{Title: "New guest-enabled product", PriceQuota: 500000, Visibility: &public, PurchaseLoginRequired: &no})
	require.NoError(t, err)
	var stored MerchantStoreProduct
	require.NoError(t, DB.First(&stored, "id = ?", product.ID).Error)
	require.False(t, stored.PurchaseLoginRequired, "the true migration default must not overwrite an explicit false on insertion")
}
