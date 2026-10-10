package model

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
)

func TestMerchantStoreLinkPresetsRequireCurrentRootAndPreserveProductLinks(t *testing.T) {
	f := newStoreFixture(t, "balance")
	presets, err := GetMerchantStoreLinkPresets()
	require.NoError(t, err)
	require.Equal(t, []MerchantStoreLinkPreset{}, presets)
	var count int64
	require.NoError(t, DB.Model(&Option{}).Where("key = ?", MerchantStoreProductLinkPresetsOption).Count(&count).Error)
	require.Zero(t, count, "reading defaults must not seed business presets")

	presets = []MerchantStoreLinkPreset{{ID: "merchant-guide", Title: "My custom guide", URL: "https://guides.example.test/custom", Description: "Choose your own setup."}}
	require.ErrorIs(t, SaveMerchantStoreLinkPresets(f.seller.Id, presets), ErrMerchantStoreDenied)
	require.NoError(t, SaveMerchantStoreLinkPresets(f.root.Id, presets))
	require.NoError(t, DB.Model(&User{}).Where("id = ?", f.root.Id).Update("role", common.RoleAdminUser).Error)
	require.ErrorIs(t, SaveMerchantStoreLinkPresets(f.root.Id, []MerchantStoreLinkPreset{}), ErrMerchantStoreDenied)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", f.root.Id).Update("role", common.RoleRootUser).Error)

	links := []MerchantStoreLink{
		{Title: presets[0].Title, URL: presets[0].URL, Description: "Seller edited this copy."},
		{Title: "Merchant's own link", URL: "http://merchant.example.test/help", Description: "Arbitrary links remain supported."},
	}
	product, err := SaveMerchantStoreProduct(f.seller.Id, "", MerchantStoreProductInput{Title: "Independent link copies", Links: links, PriceQuota: 500000, PaymentMethods: []string{"balance"}})
	require.NoError(t, err)
	presets[0].Title, presets[0].URL = "Revised guide", "https://guides.example.test/new"
	require.NoError(t, SaveMerchantStoreLinkPresets(f.root.Id, presets))
	current, err := GetMerchantStoreLinkPresets()
	require.NoError(t, err)
	require.Equal(t, presets, current)
	var retained MerchantStoreProduct
	require.NoError(t, DB.First(&retained, "id = ?", product.ID).Error)
	require.Equal(t, links, retained.Links)
	require.NoError(t, SaveMerchantStoreLinkPresets(f.root.Id, []MerchantStoreLinkPreset{}))
	require.NoError(t, DB.First(&retained, "id = ?", product.ID).Error)
	require.Equal(t, links, retained.Links)
}

func TestMerchantStoreLinkPresetsRejectMalformedConfigurationAtomically(t *testing.T) {
	f := newStoreFixture(t, "balance")
	common.OptionMapRWMutex.Lock()
	previousOptions := common.OptionMap
	common.OptionMap = make(map[string]string)
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previousOptions
		common.OptionMapRWMutex.Unlock()
	})
	valid := []MerchantStoreLinkPreset{{ID: "custom_1", Title: "Custom", URL: "https://merchant.example.test", Description: "Public description"}}
	require.NoError(t, SaveMerchantStoreLinkPresets(f.root.Id, valid))
	for name, value := range map[string]string{
		"null":             `null`,
		"wrong shape":      `{}`,
		"unknown secrets":  `[{"id":"a","title":"Custom","url":"https://example.test","description":"","secret":"private"}]`,
		"duplicate id":     `[{"id":"a","title":"Custom","url":"https://example.test"},{"id":"a","title":"Other","url":"https://example.test"}]`,
		"malformed id":     `[{"id":"../a","title":"Custom","url":"https://example.test"}]`,
		"empty title":      `[{"id":"a","title":"  ","url":"https://example.test"}]`,
		"control in title": `[{"id":"a","title":"Custom\nprivate","url":"https://example.test"}]`,
		"script url":       `[{"id":"a","title":"Custom","url":"javascript:alert(1)"}]`,
		"url credentials":  `[{"id":"a","title":"Custom","url":"https://user:secret@example.test"}]`,
		"trailing value":   `[] []`,
		"oversized json":   strings.Repeat(" ", storeLinkPresetsMaxBytes) + `[]`,
	} {
		t.Run(name, func(t *testing.T) {
			require.ErrorIs(t, UpdateOption(MerchantStoreProductLinkPresetsOption, value), ErrMerchantStoreInput)
			current, err := GetMerchantStoreLinkPresets()
			require.NoError(t, err)
			require.Equal(t, valid, current)
		})
	}
	tooMany := make([]MerchantStoreLinkPreset, 51)
	for i := range tooMany {
		tooMany[i] = valid[0]
		tooMany[i].ID = strings.Repeat("a", i+1)
	}
	require.ErrorIs(t, SaveMerchantStoreLinkPresets(f.root.Id, tooMany), ErrMerchantStoreInput)
	tooLarge := append([]MerchantStoreLinkPreset{}, valid...)
	tooLarge[0].Description = strings.Repeat("a", 4097)
	require.ErrorIs(t, SaveMerchantStoreLinkPresets(f.root.Id, tooLarge), ErrMerchantStoreInput)
	encoded, err := json.Marshal(valid)
	require.NoError(t, err)
	require.NoError(t, UpdateOption(MerchantStoreProductLinkPresetsOption, string(encoded)))
	storeUnsupportedWriterGateForTest(t)
	require.ErrorIs(t, SaveMerchantStoreLinkPresets(f.root.Id, []MerchantStoreLinkPreset{}), ErrMerchantStoreWriterFrozen)
	current, err := GetMerchantStoreLinkPresets()
	require.NoError(t, err)
	require.Equal(t, valid, current, "public reads remain available while writes are frozen")
}
