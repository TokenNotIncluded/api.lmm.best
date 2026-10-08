package model

import (
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
)

func merchantStoreSetMinimumForTest(t *testing.T, root, minimum int) {
	t.Helper()
	config, err := GetMerchantStoreConfig()
	require.NoError(t, err)
	config.MinimumUnitPriceQuota = minimum
	require.NoError(t, SetMerchantStoreConfig(root, config))
}

func TestMerchantStoreMinimumPriceMissingConfigIsVirtualAndFirstExplicitZeroPersists(t *testing.T) {
	db := marketTestDB(t)
	require.NoError(t, db.AutoMigrate(MerchantStoreModels()...))
	root := marketTestUser(t, db, "minimum-default-root", 0, common.RoleRootUser)
	config, err := GetMerchantStoreConfig()
	require.NoError(t, err)
	require.Equal(t, 500000, config.MinimumUnitPriceQuota)
	var count int64
	require.NoError(t, db.Model(&MerchantStoreConfig{}).Count(&count).Error)
	require.Zero(t, count, "reading the initial policy must not create a config row")
	config.MinimumUnitPriceQuota = 0
	require.NoError(t, SetMerchantStoreConfig(root.Id, config))
	for attempt := 0; attempt < 2; attempt++ {
		stored, err := GetMerchantStoreConfig()
		require.NoError(t, err)
		require.Zero(t, stored.MinimumUnitPriceQuota, "explicit zero must survive GORM's create default")
		require.NoError(t, SetMerchantStoreConfig(root.Id, stored))
	}
	require.NoError(t, db.Model(&MerchantStoreConfig{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
}

type merchantStoreMinimumLegacyConfig struct {
	ID                 int `gorm:"primaryKey"`
	FeeBPS             int
	RecipientID        int
	PromotionQuota     int    `gorm:"type:bigint"`
	LinuxDOUnitsPerUSD string `gorm:"size:64"`
}

func (merchantStoreMinimumLegacyConfig) TableName() string { return "merchant_store_configs" }

func TestMerchantStoreMinimumPriceMigrationBackfillsExistingConfigWithoutRewritingEconomics(t *testing.T) {
	db := marketTestDB(t)
	root := marketTestUser(t, db, "minimum-migration-root", 0, common.RoleRootUser)
	require.NoError(t, db.AutoMigrate(&merchantStoreMinimumLegacyConfig{}))
	require.NoError(t, db.Create(&merchantStoreMinimumLegacyConfig{
		ID: 1, FeeBPS: 325, RecipientID: root.Id, PromotionQuota: 750000, LinuxDOUnitsPerUSD: "2.5",
	}).Error)
	require.NoError(t, db.AutoMigrate(&MerchantStoreConfig{}))
	var stored MerchantStoreConfig
	require.NoError(t, db.First(&stored, 1).Error)
	require.Equal(t, 500000, stored.MinimumUnitPriceQuota)
	require.Equal(t, 325, stored.FeeBPS)
	require.Equal(t, root.Id, stored.RecipientID)
	require.Equal(t, 750000, stored.PromotionQuota)
	require.Equal(t, "2.5", stored.LinuxDOUnitsPerUSD)
	columns, err := db.Migrator().ColumnTypes(&MerchantStoreConfig{})
	require.NoError(t, err)
	found := false
	for _, column := range columns {
		if column.Name() != "minimum_unit_price_quota" {
			continue
		}
		found = true
		require.Equal(t, "bigint", strings.ToLower(column.DatabaseTypeName()))
		nullable, ok := column.Nullable()
		require.True(t, ok)
		require.False(t, nullable)
		defaultValue, ok := column.DefaultValue()
		require.True(t, ok)
		require.Equal(t, "500000", strings.Trim(defaultValue, "'\"()"))
	}
	require.True(t, found)
	stored.MinimumUnitPriceQuota = 0
	require.NoError(t, SetMerchantStoreConfig(root.Id, stored))
	current, err := GetMerchantStoreConfig()
	require.NoError(t, err)
	require.Zero(t, current.MinimumUnitPriceQuota)
}

func TestMerchantStoreMinimumPriceConfigPatchPreservesOmittedFloorAndRequiresRoot(t *testing.T) {
	f := newStoreFixture(t, "balance")
	merchantStoreSetMinimumForTest(t, f.root.Id, 500000)
	fee, promo := 325, 750000
	require.NoError(t, PatchMerchantStoreConfig(f.root.Id, MerchantStoreConfigPatch{FeeBPS: &fee, PromotionQuota: &promo}))
	config, err := GetMerchantStoreConfig()
	require.NoError(t, err)
	require.Equal(t, 500000, config.MinimumUnitPriceQuota)
	require.Equal(t, fee, config.FeeBPS)
	require.Equal(t, promo, config.PromotionQuota)
	zero := 0
	require.NoError(t, PatchMerchantStoreConfig(f.root.Id, MerchantStoreConfigPatch{MinimumUnitPriceQuota: &zero}))
	require.NoError(t, PatchMerchantStoreConfig(f.root.Id, MerchantStoreConfigPatch{FeeBPS: &zero}))
	config, err = GetMerchantStoreConfig()
	require.NoError(t, err)
	require.Zero(t, config.MinimumUnitPriceQuota)
	require.Zero(t, config.FeeBPS)
	require.Equal(t, promo, config.PromotionQuota)
	admin := marketTestUser(t, DB, "minimum-config-admin", 0, common.RoleAdminUser)
	for _, actor := range []int{f.seller.Id, admin.Id} {
		config.MinimumUnitPriceQuota = 500000
		require.ErrorIs(t, SetMerchantStoreConfig(actor, config), ErrMerchantStoreDenied)
		minimum := 500000
		require.ErrorIs(t, PatchMerchantStoreConfig(actor, MerchantStoreConfigPatch{MinimumUnitPriceQuota: &minimum}), ErrMerchantStoreDenied)
	}
	config, err = GetMerchantStoreConfig()
	require.NoError(t, err)
	require.Zero(t, config.MinimumUnitPriceQuota)
}

func TestMerchantStoreMinimumPriceConfigBoundsAllowZeroAndWalletMaximum(t *testing.T) {
	f := newStoreFixture(t, "balance")
	for _, minimum := range []int{0, 500000, common.MaxWalletQuota} {
		merchantStoreSetMinimumForTest(t, f.root.Id, minimum)
	}
	for _, invalid := range []int{-1, common.MaxWalletQuota + 1} {
		config, err := GetMerchantStoreConfig()
		require.NoError(t, err)
		config.MinimumUnitPriceQuota = invalid
		require.ErrorIs(t, SetMerchantStoreConfig(f.root.Id, config), ErrMerchantStoreInput)
		require.ErrorIs(t, PatchMerchantStoreConfig(f.root.Id, MerchantStoreConfigPatch{MinimumUnitPriceQuota: &invalid}), ErrMerchantStoreInput)
	}
	config, err := GetMerchantStoreConfig()
	require.NoError(t, err)
	require.Equal(t, common.MaxWalletQuota, config.MinimumUnitPriceQuota)
}

func TestMerchantStoreMinimumPriceSaveAndSubmitUseCurrentFloor(t *testing.T) {
	f := newStoreFixture(t, "balance")
	merchantStoreSetMinimumForTest(t, f.root.Id, 500000)
	input := MerchantStoreProductInput{Title: "Minimum price draft", PriceQuota: 499999, PaymentMethods: []string{"balance"}}
	_, err := SaveMerchantStoreProduct(f.seller.Id, "", input)
	require.ErrorIs(t, err, ErrMerchantStoreMinimumPrice)
	input.PriceQuota = 500000
	boundary, err := SaveMerchantStoreProduct(f.seller.Id, "", input)
	require.NoError(t, err)
	require.NoError(t, SubmitMerchantStoreProduct(f.seller.Id, boundary.ID))
	merchantStoreSetMinimumForTest(t, f.root.Id, 0)
	input.PriceQuota = 1
	low, err := SaveMerchantStoreProduct(f.seller.Id, "", input)
	require.NoError(t, err)
	input.PriceQuota = 0
	_, err = SaveMerchantStoreProduct(f.seller.Id, "", input)
	require.ErrorIs(t, err, ErrMerchantStoreInput, "disabling the floor must not allow free products")
	merchantStoreSetMinimumForTest(t, f.root.Id, 500000)
	require.ErrorIs(t, SubmitMerchantStoreProduct(f.seller.Id, low.ID), ErrMerchantStoreMinimumPrice)
	var unchanged MerchantStoreProduct
	require.NoError(t, DB.First(&unchanged, "id = ?", low.ID).Error)
	require.Equal(t, "draft", unchanged.Status)
	require.Equal(t, 1, unchanged.PriceQuota)
	require.Empty(t, unchanged.AIReviewToken)
	merchantStoreSetMinimumForTest(t, f.root.Id, 500001)
	require.ErrorIs(t, SubmitMerchantStoreProduct(f.seller.Id, boundary.ID), ErrMerchantStoreMinimumPrice, "pending replay must still apply the current product policy")
}

func TestMerchantStoreMinimumPriceChecksUnitPriceAndPublicPauseWithoutRewritingProduct(t *testing.T) {
	f := newStoreFixture(t, "balance")
	merchantStoreSetMinimumForTest(t, f.root.Id, 500001)
	public, err := GetPublicMerchantStoreProduct(f.product.ID)
	require.NoError(t, err)
	require.True(t, public.TradingPaused)
	require.Equal(t, 500000, public.PriceQuota)
	require.Equal(t, "published", public.Status)
	input := f.checkout("quantity-cannot-bypass-unit-floor", "balance")
	input.Quantity = 2 // The total exceeds the floor; each unit remains below it.
	_, created, err := CreateMerchantStoreOrder(input)
	require.ErrorIs(t, err, ErrMerchantStoreMinimumPrice)
	require.False(t, created)
	storeBalance(t, f.buyer.Id, 10000000)
	storeBalance(t, f.seller.Id, 10000000)
	storeBalance(t, f.root.Id, 0)
	var count int64
	require.NoError(t, DB.Model(&MerchantStoreOrder{}).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, DB.Model(&MerchantStoreStock{}).Where("product_id = ? AND state = ?", f.product.ID, "available").Count(&count).Error)
	require.EqualValues(t, 2, count)
	merchantStoreSetMinimumForTest(t, f.root.Id, 500000)
	_, created, err = CreateMerchantStoreOrder(f.checkout("exact-unit-floor", "balance"))
	require.NoError(t, err)
	require.True(t, created)
	merchantStoreSetMinimumForTest(t, f.root.Id, 0)
	public, err = GetPublicMerchantStoreProduct(f.product.ID)
	require.NoError(t, err)
	require.False(t, public.TradingPaused)
	require.Equal(t, 500000, public.PriceQuota)
}

func TestMerchantStoreMinimumPriceIncreasePreservesFrozenOrderPaymentReplayAndClaim(t *testing.T) {
	f := newStoreFixture(t, "platform:waffo_pancake")
	merchantStoreSetMinimumForTest(t, f.root.Id, 500000)
	input := f.checkout("frozen-before-minimum-increase", "platform:waffo_pancake")
	order, created, err := CreateMerchantStoreOrder(input)
	require.NoError(t, err)
	require.True(t, created)
	require.NoError(t, BindMerchantStorePaymentQuote(order.ID, 100, "USD", "1"))
	require.NoError(t, BindMerchantStorePaymentContext(order.ID, "frozen-minimum-price-context"))
	merchantStoreSetMinimumForTest(t, f.root.Id, 1000000)
	replay, created, err := CreateMerchantStoreOrder(input)
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, order.ID, replay.ID)
	require.Equal(t, 500000, replay.UnitPriceQuota)
	require.Equal(t, 500000, replay.PriceQuota)
	require.NoError(t, BindMerchantStorePaymentQuote(order.ID, 100, "USD", "1"))
	require.NoError(t, BindMerchantStorePaymentContext(order.ID, "frozen-minimum-price-context"))
	require.NoError(t, CompleteMerchantStorePayment(order.ID, "minimum-frozen-provider-receipt"))
	require.NoError(t, CompleteMerchantStorePayment(order.ID, "minimum-frozen-provider-receipt"))
	replay, created, err = CreateMerchantStoreOrder(input)
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, "paid", replay.Status)
	require.EqualValues(t, 100, replay.AmountMinor)
	require.Equal(t, "USD", replay.Currency)
	token, err := GetMerchantStoreOrderPickupToken(f.buyer.Id, order.ID)
	require.NoError(t, err)
	claim, err := ClaimMerchantStoreOrder(token, "safe-pickup-code", f.buyer.Id)
	require.NoError(t, err)
	require.Equal(t, []string{"CARD-SECRET-FIRST"}, claim.Items)
	storeBalance(t, f.seller.Id, 10495000)
	storeBalance(t, f.root.Id, 5000)
}
