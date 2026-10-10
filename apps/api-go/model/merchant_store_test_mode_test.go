package model

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func TestMerchantStoreTestModeJSONDistinguishesOmittedFalseAndNull(t *testing.T) {
	for name, body := range map[string]string{
		"omitted": `{}`, "false": `{"test_mode":false}`, "true": `{"test_mode":true}`,
	} {
		t.Run(name, func(t *testing.T) {
			var in MerchantStoreProductInput
			require.NoError(t, json.Unmarshal([]byte(body), &in))
			if name == "omitted" {
				require.Nil(t, in.TestMode)
			} else {
				require.NotNil(t, in.TestMode)
				require.Equal(t, name == "true", *in.TestMode)
			}
		})
	}
	for _, body := range []string{`{"test_mode":null}`, `{"TEST_MODE":null}`, `{"test_mode":"false"}`, `{"test_mode":0}`, `{"test_mode":[]}`, `{"test_mode":{}}`} {
		var in MerchantStoreProductInput
		require.Error(t, json.Unmarshal([]byte(body), &in), body)
	}
}

func storeModeInput(p *MerchantStoreProduct, mode *bool) MerchantStoreProductInput {
	return MerchantStoreProductInput{Title: p.Title, Description: p.Description, ImageURLs: p.ImageURLs, Contact: p.Contact, Links: p.Links, PriceQuota: p.PriceQuota, TestMode: mode, Template: p.Template, DeliveryStrategy: p.DeliveryStrategy, PaymentMethods: p.PaymentMethods, PickupLoginRequired: p.PickupLoginRequired, PickupCodeRequired: p.PickupCodeRequired, EmailPickupLink: p.EmailPickupLink}
}

func storeSetTestMode(t *testing.T, f *storeFixture, enabled bool) {
	t.Helper()
	p, err := GetMerchantStoreProduct(f.seller.Id, f.product.ID)
	require.NoError(t, err)
	f.product, err = SaveMerchantStoreProduct(f.seller.Id, p.ID, storeModeInput(p, &enabled))
	require.NoError(t, err)
}

func storeSellerCheckout(t *testing.T, f storeFixture, key, method string) MerchantStoreCheckoutInput {
	t.Helper()
	require.NoError(t, AcceptMerchantStoreDisclaimer(f.seller.Id, MerchantStoreDisclaimerVersion))
	in := f.checkout(key, method)
	in.BuyerID = f.seller.Id
	in.PickupEmail = "owner-test-delivery@example.test"
	return in
}

func TestMerchantStoreTestModeOptionalInputAndExistingDataMigration(t *testing.T) {
	f := newStoreFixture(t, "balance")
	require.False(t, f.product.TestMode)
	paid, _, err := CreateMerchantStoreOrder(f.checkout("before-test-column", "balance"))
	require.NoError(t, err)
	// Simulate a pre-feature table in this disposable SQLite fixture.
	require.NoError(t, DB.Migrator().DropColumn(&MerchantStoreProduct{}, "TestMode"))
	require.NoError(t, DB.AutoMigrate(&MerchantStoreProduct{}))
	p, err := GetPublicMerchantStoreProduct(f.product.ID)
	require.NoError(t, err)
	require.False(t, p.TestMode)
	require.Equal(t, f.product.Title, p.Title)
	require.Equal(t, "published", p.Status)
	require.EqualValues(t, 1, p.AvailableStock)
	stillPaid, err := GetMerchantStoreOrder(f.buyer.Id, paid.ID)
	require.NoError(t, err)
	require.Equal(t, "paid", stillPaid.Status)
	storeBalance(t, f.buyer.Id, 9500000)
	storeBalance(t, f.seller.Id, 10495000)
	storeBalance(t, f.root.Id, 5000)
	parsed, err := schema.Parse(&MerchantStoreProduct{}, &sync.Map{}, schema.NamingStrategy{})
	require.NoError(t, err)
	field := parsed.LookUpField("TestMode")
	require.NotNil(t, field)
	require.True(t, field.NotNull)
	require.True(t, field.HasDefaultValue)
	require.Equal(t, "false", field.DefaultValue)
	storeSetTestMode(t, &f, true)
	require.True(t, f.product.TestMode)
	f.product, err = SaveMerchantStoreProduct(f.seller.Id, f.product.ID, storeModeInput(f.product, nil))
	require.NoError(t, err)
	require.True(t, f.product.TestMode, "an older editor's omitted flag must not publish the test")
	storeSetTestMode(t, &f, false)
	require.False(t, f.product.TestMode)
	require.Equal(t, "draft", f.product.Status)
	_, err = GetPublicMerchantStoreProduct(f.product.ID)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestMerchantStoreTestModeVisibilityHasNoAdministratorOverride(t *testing.T) {
	f := newStoreFixture(t, "balance")
	storeSetTestMode(t, &f, true)
	admin := marketTestUser(t, DB, "test-mode-other-admin", 1000000, common.RoleAdminUser)
	job := ModerationJob{EventKey: "test-mode-old-applied", UserID: f.seller.Id, Source: ModerationSourceMarketProduct, TargetID: f.product.ID, RequestID: "old-token", Status: ModerationJobCompleted, MarketOutcome: "approved"}
	require.NoError(t, DB.Create(&job).Error)
	for _, status := range []string{"published", "pending", "rejected"} {
		require.NoError(t, DB.Model(f.product).Updates(map[string]any{"status": status, "ai_review_token": job.RequestID}).Error)
		rows, err := ListPublicMerchantStoreProducts("", 0, 1)
		require.NoError(t, err)
		require.Empty(t, rows, "hidden listings must not consume pagination or public counts")
		rows, err = ListPublicMerchantStoreProducts(f.product.Title, 0, 1)
		require.NoError(t, err)
		require.Empty(t, rows)
		_, err = GetPublicMerchantStoreProduct(f.product.ID)
		require.ErrorIs(t, err, gorm.ErrRecordNotFound)
		for _, actor := range []int{f.buyer.Id, admin.Id, f.root.Id} {
			_, err = GetMerchantStoreProduct(actor, f.product.ID)
			require.ErrorIs(t, err, ErrMerchantStoreDenied)
			_, err = GetMerchantStoreProductPreview(actor, f.product.ID)
			require.ErrorIs(t, err, gorm.ErrRecordNotFound)
			_, err = ListMarketAIReviews(context.Background(), actor, ModerationSourceMarketProduct, f.product.ID, "")
			require.ErrorIs(t, err, ErrToolMarketDenied)
		}
		for _, actor := range []int{admin.Id, f.root.Id} {
			rows, err = ListMerchantStoreProducts(actor, true, 0, 100)
			require.NoError(t, err)
			require.Empty(t, rows, "the applied-result OR branch must not leak a test into review queues")
		}
	}
	preview, err := GetMerchantStoreProductPreview(f.seller.Id, f.product.ID)
	require.NoError(t, err)
	require.True(t, preview.TestMode)
	require.Empty(t, preview.ReviewNote)
	require.Zero(t, preview.ReviewedBy)
	rows, err := ListMerchantStoreProducts(f.seller.Id, false, 0, 100)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.ErrorIs(t, SetMerchantStoreProductSaleLimit(f.root.Id, f.product.ID, storeLimit(0)), ErrMerchantStoreDenied)
	require.ErrorIs(t, SetMerchantStoreProductPaused(f.root.Id, f.product.ID, true), ErrMerchantStoreDenied)
	require.ErrorIs(t, SetMerchantStoreProductListed(f.root.Id, f.product.ID, false), ErrMerchantStoreDenied)
}

func TestMerchantStoreTestModeBlocksPublicReviewAndRetiresStaleAI(t *testing.T) {
	db, seller, root, p := marketAIProduct(t, setting.MarketAIReviewAuto)
	j := marketAIClaim(t)
	enabled := true
	private, err := SaveMerchantStoreProduct(seller.Id, p.ID, storeModeInput(p, &enabled))
	require.NoError(t, err, "an explicit mode change can withdraw the pending version")
	require.Equal(t, "draft", private.Status)
	require.Empty(t, private.AIReviewToken)
	var retired ModerationJob
	require.NoError(t, db.First(&retired, j.ID).Error)
	require.Equal(t, ModerationJobCancelled, retired.Status)
	require.Empty(t, retired.Payload)
	require.ErrorIs(t, marketAIComplete(t, j, false, false), ErrModerationLeaseLost)
	require.ErrorIs(t, SubmitMerchantStoreProduct(seller.Id, p.ID), ErrMerchantStoreTestMode)
	require.ErrorIs(t, ReviewMerchantStoreProduct(root.Id, p.ID, true, ""), ErrMerchantStoreDenied)
	require.ErrorIs(t, SetMerchantStoreProductListed(seller.Id, p.ID, true), ErrMerchantStoreTestMode)
	_, err = PurchaseMerchantStorePromotion(seller.Id, p.ID, 1, "private-promotion")
	require.ErrorIs(t, err, ErrMerchantStoreTestMode)
	var jobs int64
	require.NoError(t, db.Model(&ModerationJob{}).Count(&jobs).Error)
	require.EqualValues(t, 1, jobs, "test submissions must not create another AI job")
	// Defense in depth: even a restored stale token cannot make a test eligible.
	require.NoError(t, db.Model(private).Updates(map[string]any{"status": "pending", "ai_review_token": j.RequestID}).Error)
	_, _, _, current, err := marketAIReviewTarget(db, j, false)
	require.NoError(t, err)
	require.False(t, current)
	disabled := false
	publicDraft, err := SaveMerchantStoreProduct(seller.Id, p.ID, storeModeInput(private, &disabled))
	require.NoError(t, err)
	require.Equal(t, "draft", publicDraft.Status)
	require.Zero(t, publicDraft.ReviewedAt)
	_, err = GetPublicMerchantStoreProduct(p.ID)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	require.NoError(t, SubmitMerchantStoreProduct(seller.Id, p.ID))
	require.NoError(t, db.First(publicDraft, "id = ?", p.ID).Error)
	require.NotEqual(t, j.RequestID, publicDraft.AIReviewToken)
	require.NoError(t, ReviewMerchantStoreProduct(root.Id, p.ID, true, "fresh normal review"))
	_, err = GetPublicMerchantStoreProduct(p.ID)
	require.NoError(t, err)
}

func TestMerchantStoreSelfPurchaseNormalAndPrivateUseRealLedgerAndUniqueLocks(t *testing.T) {
	for _, mode := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal", true: "private-draft"}[mode], func(t *testing.T) {
			f := newStoreFixture(t, "balance")
			if mode {
				storeSetTestMode(t, &f, true)
			}
			in := storeSellerCheckout(t, f, "self-paid", "balance")
			var locked []int
			callback := "observe-deduplicated-self-wallet-locks"
			require.NoError(t, DB.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
				if tx.Statement.Table == "users" && len(tx.Statement.Selects) == 1 && tx.Statement.Selects[0] == "id" && len(tx.Statement.Vars) == 1 {
					if id, ok := tx.Statement.Vars[0].(int); ok {
						locked = append(locked, id)
					}
				}
			}))
			o, created, err := CreateMerchantStoreOrder(in)
			require.NoError(t, err)
			require.True(t, created)
			require.NoError(t, DB.Callback().Query().Remove(callback))
			require.Equal(t, []int{f.seller.Id, f.root.Id}, locked, "buyer==seller is locked once, in sorted order")
			require.Equal(t, f.seller.Id, o.BuyerID)
			require.Equal(t, f.seller.Id, o.SellerID)
			require.Equal(t, "paid", o.Status)
			require.Equal(t, f.product.Template, o.DeliveryTemplate)
			require.Equal(t, 500000, o.PriceQuota)
			require.Equal(t, 5000, o.FeeQuota)
			email, err := GetMerchantStoreOrderDeliveryEmail(f.seller.Id, o.ID)
			require.NoError(t, err)
			require.Equal(t, in.PickupEmail, email)
			storeBalance(t, f.seller.Id, 9995000)
			storeBalance(t, f.root.Id, 5000)
			var transfers []MerchantStoreTransfer
			require.NoError(t, DB.Where("order_id = ?", o.ID).Order("kind").Find(&transfers).Error)
			require.Len(t, transfers, 2)
			require.Equal(t, "fee", transfers[0].Kind)
			require.Equal(t, 5000, transfers[0].Quota)
			require.Equal(t, "sale", transfers[1].Kind)
			require.Equal(t, 500000, transfers[1].Quota)
			require.Equal(t, f.seller.Id, transfers[1].FromUserID)
			require.Equal(t, f.seller.Id, transfers[1].ToUserID)
			token, err := GetMerchantStoreOrderPickupToken(f.seller.Id, o.ID)
			require.NoError(t, err)
			claim, err := ClaimMerchantStoreOrder(token, in.PickupCode, f.seller.Id)
			require.NoError(t, err)
			require.Equal(t, []string{"CARD-SECRET-FIRST"}, claim.Items)
			require.Equal(t, o.DeliveryTemplate, claim.DeliveryTemplate)
			if mode {
				storeSetTestMode(t, &f, false)
			}
			replay, created, err := CreateMerchantStoreOrder(in)
			require.NoError(t, err)
			require.False(t, created)
			require.Equal(t, o.ID, replay.ID)
			storeBalance(t, f.seller.Id, 9995000)
			var count int64
			require.NoError(t, DB.Model(&MerchantStoreStock{}).Where("product_id = ? AND state = ?", f.product.ID, "delivered").Count(&count).Error)
			require.EqualValues(t, 1, count)
		})
	}
}

func TestMerchantStoreTestModeKeepsAllNewOrderRequirements(t *testing.T) {
	for _, policy := range []string{"full-price-balance", "minimum", "sale-limit", "stock", "category", "method", "disclaimer", "pickup-code", "pickup-email-required", "pickup-email-invalid"} {
		t.Run(policy, func(t *testing.T) {
			f := newStoreFixture(t, "balance")
			storeSetTestMode(t, &f, true)
			in := storeSellerCheckout(t, f, "must-still-enforce", "balance")
			var expected error
			switch policy {
			case "full-price-balance":
				require.NoError(t, DB.Model(&User{}).Where("id = ?", f.seller.Id).Update("quota", 499999).Error)
				expected = ErrMerchantStoreBalance
			case "minimum":
				merchantStoreSetMinimumForTest(t, f.root.Id, 500001)
				expected = ErrMerchantStoreMinimumPrice
			case "sale-limit":
				require.NoError(t, SetMerchantStoreProductSaleLimit(f.seller.Id, f.product.ID, storeLimit(0)))
				expected = ErrMerchantStoreStock
			case "stock":
				require.NoError(t, DB.Where("product_id = ?", f.product.ID).Delete(&MerchantStoreStock{}).Error)
				expected = ErrMerchantStoreStock
			case "category":
				require.NoError(t, SetMerchantStorePaymentCategories(f.seller.Id, MerchantStorePaymentCategories{}))
				expected = ErrMerchantStorePaymentCategoryDisabled
			case "method":
				require.NoError(t, DB.Model(f.product).Update("payment_methods", "[]").Error)
				expected = ErrMerchantStoreDenied
			case "disclaimer":
				require.NoError(t, DB.Where("user_id = ?", f.seller.Id).Delete(&MerchantStoreDisclaimerAcceptance{}).Error)
				expected = ErrMerchantStoreDisclaimer
			case "pickup-code":
				in.PickupCode = ""
				expected = ErrMerchantStoreInput
			case "pickup-email-required":
				in.PickupEmail = ""
				expected = ErrMerchantStoreInput
			case "pickup-email-invalid":
				in.PickupEmail = "not-an-email"
				expected = ErrMerchantStoreInput
			}
			_, created, err := CreateMerchantStoreOrder(in)
			require.ErrorIs(t, err, expected)
			require.False(t, created)
			var count int64
			require.NoError(t, DB.Model(&MerchantStoreOrder{}).Count(&count).Error)
			require.Zero(t, count)
			require.NoError(t, DB.Model(&MerchantStoreTransfer{}).Count(&count).Error)
			require.Zero(t, count)
		})
	}
}

func TestMerchantStoreTestModeStopsPausedOffShelfAndRejectedPurchases(t *testing.T) {
	f := newStoreFixture(t, "balance")
	storeSetTestMode(t, &f, true)
	in := storeSellerCheckout(t, f, "stopped-test", "balance")
	require.NoError(t, SetMerchantStoreProductPaused(f.seller.Id, f.product.ID, true))
	preview, err := GetMerchantStoreProductPreview(f.seller.Id, f.product.ID)
	require.NoError(t, err)
	require.True(t, preview.TradingPaused)
	_, _, err = CreateMerchantStoreOrder(in)
	require.ErrorIs(t, err, ErrMerchantStoreUnavailable)
	_, err = SaveMerchantStoreProduct(f.seller.Id, f.product.ID, storeModeInput(preview, nil))
	require.NoError(t, err)
	require.NoError(t, DB.First(&preview, "id = ?", f.product.ID).Error)
	require.Equal(t, "paused", preview.Status, "editing must not undo an explicit trading stop")
	require.NoError(t, SetMerchantStoreProductPaused(f.seller.Id, f.product.ID, false))
	preview, err = GetMerchantStoreProductPreview(f.seller.Id, f.product.ID)
	require.NoError(t, err)
	require.Equal(t, "draft", preview.Status)
	require.False(t, preview.TradingPaused)
	require.NoError(t, SetMerchantStoreProductListed(f.seller.Id, f.product.ID, false))
	_, _, err = CreateMerchantStoreOrder(in)
	require.ErrorIs(t, err, ErrMerchantStoreUnavailable)
	require.ErrorIs(t, SetMerchantStoreProductListed(f.seller.Id, f.product.ID, true), ErrMerchantStoreTestMode)
	storeSetTestMode(t, &f, true)
	require.Equal(t, "off_shelf", f.product.Status)
	require.NoError(t, DB.Model(f.product).Update("status", "rejected").Error)
	_, _, err = CreateMerchantStoreOrder(in)
	require.ErrorIs(t, err, ErrMerchantStoreUnavailable)
}

func TestMerchantStoreTestModeSelfExternalPaymentRequiresFrozenRealReceipt(t *testing.T) {
	f := newStoreFixture(t, "platform:waffo_pancake")
	storeSetTestMode(t, &f, true)
	in := storeSellerCheckout(t, f, "self-real-invoice", "platform:waffo_pancake")
	o, created, err := CreateMerchantStoreOrder(in)
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, "pending", o.Status)
	storeBalance(t, f.seller.Id, 9995000)
	storeBalance(t, f.root.Id, 0)
	require.ErrorIs(t, CompleteMerchantStorePayment(o.ID, "no-frozen-invoice"), ErrMerchantStoreConflict)
	require.NoError(t, BindMerchantStorePaymentQuote(o.ID, 100, "USD", "1"))
	require.NoError(t, BindMerchantStorePaymentContext(o.ID, "self-frozen-provider-context"))
	storeSetTestMode(t, &f, false)
	require.NoError(t, CompleteMerchantStorePayment(o.ID, "verified-real-self-receipt"))
	require.NoError(t, CompleteMerchantStorePayment(o.ID, "verified-real-self-receipt"))
	storeBalance(t, f.seller.Id, 10495000)
	storeBalance(t, f.root.Id, 5000)
	var receipts int64
	require.NoError(t, DB.Model(&MerchantStorePaymentReceipt{}).Where("order_id = ?", o.ID).Count(&receipts).Error)
	require.EqualValues(t, 1, receipts)
	replay, created, err := CreateMerchantStoreOrder(in)
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, "paid", replay.Status)
	require.EqualValues(t, 100, replay.AmountMinor)
}

func TestMerchantStoreTestModeSwitchPreservesOrdinaryBuyerFrozenObligations(t *testing.T) {
	for _, method := range []string{"balance", "platform:waffo_pancake"} {
		t.Run(method, func(t *testing.T) {
			f := newStoreFixture(t, method)
			in := f.checkout("ordinary-before-private", method)
			o, _, err := CreateMerchantStoreOrder(in)
			require.NoError(t, err)
			if method != "balance" {
				require.NoError(t, BindMerchantStorePaymentQuote(o.ID, 100, "USD", "1"))
				require.NoError(t, BindMerchantStorePaymentContext(o.ID, "ordinary-frozen-before-private"))
			}
			storeSetTestMode(t, &f, true)
			inNew := in
			inNew.RequestKey = "ordinary-new-is-denied"
			_, _, err = CreateMerchantStoreOrder(inNew)
			require.ErrorIs(t, err, ErrMerchantStoreDenied)
			replay, created, err := CreateMerchantStoreOrder(in)
			require.NoError(t, err)
			require.False(t, created)
			require.Equal(t, o.ID, replay.ID)
			if method != "balance" {
				require.NoError(t, CompleteMerchantStorePayment(o.ID, "ordinary-valid-old-receipt"))
			}
			token, err := GetMerchantStoreOrderPickupToken(f.buyer.Id, o.ID)
			require.NoError(t, err)
			claim, err := ClaimMerchantStoreOrder(token, in.PickupCode, f.buyer.Id)
			require.NoError(t, err)
			require.Equal(t, []string{"CARD-SECRET-FIRST"}, claim.Items)
			_, err = GetPublicMerchantStoreProduct(f.product.ID)
			require.ErrorIs(t, err, gorm.ErrRecordNotFound)
			view, err := GetMerchantStoreOrder(f.buyer.Id, o.ID)
			require.NoError(t, err)
			require.Equal(t, o.ProductTitle, view.ProductTitle)
			require.NotContains(t, strings.ToLower(view.Status), "test")
		})
	}
}
