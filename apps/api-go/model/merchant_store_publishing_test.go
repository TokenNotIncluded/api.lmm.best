package model

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func storePublishingInput() MerchantStoreProductInput {
	return MerchantStoreProductInput{
		Title: "Two specifications", Description: "Independent stock and prices",
		PriceQuota: 500000, Template: "card-key", DeliveryStrategy: "sequential",
		PaymentMethods: []string{"balance"},
		Variants: []MerchantStoreVariantInput{
			{Name: "Monthly", PriceQuota: 500000, Template: "card-key", Enabled: true},
			{Name: "Annual", PriceQuota: 6000000, Template: "text", Enabled: true},
		},
	}
}

func TestMerchantStorePublishingCreatesNamedVariantsAtomically(t *testing.T) {
	f := newVariantsFixture(t, "balance")
	raw, err := json.Marshal(storePublishingInput())
	require.NoError(t, err)
	var in MerchantStoreProductInput
	require.NoError(t, json.Unmarshal(raw, &in))
	require.Len(t, in.Variants, 2, "custom input decoding must preserve specifications")
	p, err := SaveMerchantStoreProduct(f.seller.Id, "", in)
	require.NoError(t, err)
	got, err := GetMerchantStoreProduct(f.seller.Id, p.ID)
	require.NoError(t, err)
	require.Len(t, got.Variants, 2)
	var first, other MerchantStoreVariant
	require.NoError(t, DB.First(&first, "id = ?", MerchantStoreDefaultVariantID(p.ID)).Error)
	require.Equal(t, "Monthly", first.Name)
	require.Equal(t, p.PriceQuota, first.PriceQuota)
	require.NoError(t, DB.First(&other, "product_id = ? AND id <> ?", p.ID, first.ID).Error)
	require.Equal(t, "Annual", other.Name)
	require.Equal(t, 6000000, other.PriceQuota)
	require.Equal(t, "text", other.Template)
	require.Equal(t, "draft", got.Status)
}

func TestMerchantStorePublishingRollsBackEveryVariantOnFailure(t *testing.T) {
	f := newVariantsFixture(t, "balance")
	in := storePublishingInput()
	forbidden := "private body on a stock template"
	in.Variants[1].FixedContent = &forbidden
	before := storeWriterSnapshot(t)
	_, err := SaveMerchantStoreProduct(f.seller.Id, "", in)
	require.ErrorIs(t, err, ErrMerchantStoreInput)
	require.Equal(t, before, storeWriterSnapshot(t), "no product, variant, or audit fragment survives")
}

func TestMerchantStorePublishingRejectsUnsafeVariantSets(t *testing.T) {
	f := newVariantsFixture(t, "balance")
	cases := map[string]func(*MerchantStoreProductInput){
		"empty":      func(in *MerchantStoreProductInput) { in.Variants = []MerchantStoreVariantInput{} },
		"duplicate":  func(in *MerchantStoreProductInput) { in.Variants[1].Name = " monthly " },
		"long name":  func(in *MerchantStoreProductInput) { in.Variants[1].Name = strings.Repeat("规", 67) },
		"zero price": func(in *MerchantStoreProductInput) { in.Variants[1].PriceQuota = 0 },
		"all disabled": func(in *MerchantStoreProductInput) {
			for i := range in.Variants {
				in.Variants[i].Enabled = false
			}
		},
		"conflicting default": func(in *MerchantStoreProductInput) { in.PriceQuota++ },
		"over limit":          func(in *MerchantStoreProductInput) { in.Variants = make([]MerchantStoreVariantInput, 201) },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			before := storeWriterSnapshot(t)
			in := storePublishingInput()
			change(&in)
			_, err := SaveMerchantStoreProduct(f.seller.Id, "", in)
			require.ErrorIs(t, err, ErrMerchantStoreInput)
			require.Equal(t, before, storeWriterSnapshot(t))
		})
	}
	_, err := SaveMerchantStoreProduct(f.seller.Id, f.product.ID, storePublishingInput())
	require.ErrorIs(t, err, ErrMerchantStoreInput, "batch creation must not replace existing variant identities")
}

func TestMerchantStorePublishingLegacyGateCannotSilentlyDropVariants(t *testing.T) {
	f := newStoreFixture(t, "balance")
	require.False(t, MerchantStoreProductVariantsCreateSupported())
	before := storeWriterSnapshot(t)
	_, err := SaveMerchantStoreProduct(f.seller.Id, "", storePublishingInput())
	require.ErrorIs(t, err, ErrMerchantStoreWriterFrozen)
	require.Equal(t, before, storeWriterSnapshot(t))
	in := storePublishingInput()
	in.Variants = nil
	_, err = SaveMerchantStoreProduct(f.seller.Id, "", in)
	require.NoError(t, err, "old single-price clients retain their create contract")
}

func TestMerchantStorePublishingTermsRecoveryDoesNotAcceptForBuyers(t *testing.T) {
	f := newVariantsFixture(t, "balance")
	storeAccessActivateTest(t)
	require.NoError(t, DB.Where("seller_id = ?", f.seller.Id).Delete(&MerchantStoreSellerTerms{}).Error)
	require.NoError(t, SetMerchantStoreProductListed(f.seller.Id, f.product.ID, false))
	err := SetMerchantStoreProductListed(f.seller.Id, f.product.ID, true)
	require.ErrorIs(t, err, ErrMerchantStoreSellerTermsNotConfigured)
	require.ErrorIs(t, err, ErrMerchantStoreSellerTerms)
	var status string
	require.NoError(t, DB.Model(&MerchantStoreProduct{}).Where("id = ?", f.product.ID).Pluck("status", &status).Error)
	require.Equal(t, "off_shelf", status)
	// Persist terms through the real seller API, never manufacture acceptance.
	terms, err := SaveMerchantStoreSellerTerms(f.seller.Id, MerchantStoreTermsInput{Content: "Delivery and after-sales terms"})
	require.NoError(t, err)
	require.True(t, terms.Configured)
	require.False(t, terms.Accepted)
	require.NoError(t, SetMerchantStoreProductListed(f.seller.Id, f.product.ID, true))
}

func TestMerchantStorePublishingKeepsDisabledSpecificationsDisabled(t *testing.T) {
	f := newVariantsFixture(t, "balance")
	in := storePublishingInput()
	in.Variants[1].Enabled = false
	p, err := SaveMerchantStoreProduct(f.seller.Id, "", in)
	require.NoError(t, err)
	var variant MerchantStoreVariant
	require.NoError(t, DB.First(&variant, "product_id = ? AND name = ?", p.ID, "Annual").Error)
	require.False(t, variant.Enabled)
	in.Variants[0].Enabled = false
	in.Variants[1].Enabled = true
	p, err = SaveMerchantStoreProduct(f.seller.Id, "", in)
	require.NoError(t, err)
	var first MerchantStoreVariant
	require.NoError(t, DB.First(&first, "id = ?", MerchantStoreDefaultVariantID(p.ID)).Error)
	require.False(t, first.Enabled)
}
