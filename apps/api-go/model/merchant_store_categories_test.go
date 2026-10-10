package model

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func storeCustomCategoryFixture(t *testing.T) storeFixture {
	t.Helper()
	require.GreaterOrEqual(t, MerchantStoreWriterCapability, 6)
	f := newStoreFixture(t, "balance")
	storeActivatePhaseFiveForSixTest(t, DB)
	require.NoError(t, PrepareMerchantStoreSchema(DB, 5))
	require.NoError(t, ActivateMerchantStorePhaseSix(DB, 5))
	return f
}

func storeCustomCategoryBool(value bool) *bool { return &value }

func storeCustomCategoryInput(product *MerchantStoreProduct, categoryID *string) MerchantStoreProductInput {
	return MerchantStoreProductInput{
		Title: product.Title, Description: product.Description, ImageURLs: product.ImageURLs,
		Contact: product.Contact, Links: product.Links, PriceQuota: product.PriceQuota,
		Template: product.Template, DeliveryStrategy: product.DeliveryStrategy,
		PaymentMethods: product.PaymentMethods, PickupLoginRequired: product.PickupLoginRequired,
		PickupCodeRequired: product.PickupCodeRequired, EmailPickupLink: product.EmailPickupLink,
		CategoryID: categoryID,
	}
}

func storeCustomCategoryJSON(t *testing.T, product *MerchantStoreProduct) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(product)
	require.NoError(t, err)
	var value map[string]any
	require.NoError(t, json.Unmarshal(encoded, &value))
	return value
}

func TestMerchantStoreCategoriesNormalizeNamesRequireCurrentAdminAndOrderActiveLists(t *testing.T) {
	f := storeCustomCategoryFixture(t)
	admin := marketTestUser(t, DB, "store-category-admin", 0, common.RoleAdminUser)
	_, err := SaveMerchantStoreCategory(f.seller.Id, "", MerchantStoreCategoryInput{Name: "Unauthorized"})
	require.ErrorIs(t, err, ErrMerchantStoreDenied)
	category, err := SaveMerchantStoreCategory(admin.Id, "", MerchantStoreCategoryInput{Name: "  Cafe\u0301   cards  ", SortOrder: 10})
	require.NoError(t, err)
	require.Equal(t, "Café cards", category.Name)
	_, err = uuid.Parse(category.ID)
	require.NoError(t, err)
	require.True(t, category.Active)
	require.Greater(t, category.CreatedAt, int64(0))
	require.GreaterOrEqual(t, category.UpdatedAt, category.CreatedAt)
	_, err = SaveMerchantStoreCategory(f.root.Id, "", MerchantStoreCategoryInput{Name: "CAFÉ\tCARDS"})
	require.ErrorIs(t, err, ErrMerchantStoreConflict)

	disabled, err := SaveMerchantStoreCategory(f.root.Id, "", MerchantStoreCategoryInput{Name: "Archived", SortOrder: -1, Active: storeCustomCategoryBool(false)})
	require.NoError(t, err)
	require.False(t, disabled.Active, "an explicit false must survive GORM create defaults")
	public, err := ListMerchantStoreCategories(0, false, 0, 30)
	require.NoError(t, err)
	require.Len(t, public, 1)
	require.Equal(t, category.ID, public[0].ID)
	_, err = ListMerchantStoreCategories(f.seller.Id, true, 0, 30)
	require.ErrorIs(t, err, ErrMerchantStoreDenied)
	all, err := ListMerchantStoreCategories(admin.Id, true, 0, 30)
	require.NoError(t, err)
	require.Len(t, all, 2)
	require.Equal(t, disabled.ID, all[0].ID)
	require.Equal(t, category.ID, all[1].ID)
	page, err := ListMerchantStoreCategories(admin.Id, true, 1, 1)
	require.NoError(t, err)
	require.Len(t, page, 1)
	require.Equal(t, category.ID, page[0].ID)
	updated, err := SaveMerchantStoreCategory(admin.Id, disabled.ID, MerchantStoreCategoryInput{Name: " Archived ", SortOrder: -1000000})
	require.NoError(t, err)
	require.False(t, updated.Active, "omitting active on an update preserves a disabled category")
	require.Equal(t, disabled.CreatedAt, updated.CreatedAt)
	_, err = SaveMerchantStoreCategory(admin.Id, "", MerchantStoreCategoryInput{Name: " archived "})
	require.ErrorIs(t, err, ErrMerchantStoreConflict, "disabled categories retain their unique normalized names")
	require.NoError(t, DB.Model(&User{}).Where("id = ?", admin.Id).Update("role", common.RoleCommonUser).Error)
	_, err = SaveMerchantStoreCategory(admin.Id, category.ID, MerchantStoreCategoryInput{Name: "Revoked authority"})
	require.ErrorIs(t, err, ErrMerchantStoreDenied)
	_, err = ListMerchantStoreCategories(admin.Id, true, 0, 30)
	require.ErrorIs(t, err, ErrMerchantStoreDenied)
}

func TestMerchantStoreCategoriesRejectMalformedNamesAndSortBoundsAtomically(t *testing.T) {
	f := storeCustomCategoryFixture(t)
	for name, input := range map[string]MerchantStoreCategoryInput{
		"empty": {Name: ""}, "whitespace": {Name: " \t "},
		"control": {Name: "category\x00private"}, "invalid UTF-8": {Name: string([]byte{0xff})},
		"too many runes":  {Name: strings.Repeat("类", 81)},
		"low sort order":  {Name: "Low", SortOrder: -1000001},
		"high sort order": {Name: "High", SortOrder: 1000001},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := SaveMerchantStoreCategory(f.root.Id, "", input)
			require.ErrorIs(t, err, ErrMerchantStoreInput)
		})
	}
	var count int64
	require.NoError(t, DB.Model(&MerchantStoreCategory{}).Count(&count).Error)
	require.Zero(t, count)
	category, err := SaveMerchantStoreCategory(f.root.Id, "", MerchantStoreCategoryInput{Name: strings.Repeat("类", 80), SortOrder: 1000000})
	require.NoError(t, err)
	_, err = SaveMerchantStoreCategory(f.root.Id, "not-a-uuid", MerchantStoreCategoryInput{Name: "Invalid identifier"})
	require.ErrorIs(t, err, ErrMerchantStoreInput)
	_, err = SaveMerchantStoreCategory(f.root.Id, category.ID, MerchantStoreCategoryInput{Name: "", Active: storeCustomCategoryBool(false)})
	require.ErrorIs(t, err, ErrMerchantStoreInput)
	var retained MerchantStoreCategory
	require.NoError(t, DB.First(&retained, "id = ?", category.ID).Error)
	require.Equal(t, *category, retained)
}

func TestMerchantStoreProductCategoriesAreOwnerOnlyAndDisablingPreservesPublishedFacts(t *testing.T) {
	f := storeCustomCategoryFixture(t)
	category, err := SaveMerchantStoreCategory(f.root.Id, "", MerchantStoreCategoryInput{Name: "Digital cards"})
	require.NoError(t, err)
	var before MerchantStoreProduct
	var stockBefore []MerchantStoreStock
	require.NoError(t, DB.First(&before, "id = ?", f.product.ID).Error)
	require.NoError(t, DB.Where("product_id = ?", f.product.ID).Order("id").Find(&stockBefore).Error)
	_, err = SetMerchantStoreProductCategory(f.root.Id, f.product.ID, category.ID)
	require.ErrorIs(t, err, ErrMerchantStoreDenied)
	_, err = SetMerchantStoreProductCategory(f.buyer.Id, f.product.ID, category.ID)
	require.ErrorIs(t, err, ErrMerchantStoreDenied)
	_, err = SetMerchantStoreProductCategory(f.seller.Id, f.product.ID, uuid.NewString())
	require.ErrorIs(t, err, ErrMerchantStoreInput)
	_, err = SetMerchantStoreProductCategory(f.seller.Id, f.product.ID, "not-a-uuid")
	require.ErrorIs(t, err, ErrMerchantStoreInput)
	_, err = SetMerchantStoreProductCategory(f.seller.Id, f.product.ID, category.ID)
	require.NoError(t, err)
	var assigned MerchantStoreProduct
	require.NoError(t, DB.First(&assigned, "id = ?", f.product.ID).Error)
	require.Equal(t, category.ID, storeCustomCategoryJSON(t, &assigned)["category_id"])
	require.Equal(t, before.Status, assigned.Status)
	require.Equal(t, before.ReviewNote, assigned.ReviewNote)
	require.Equal(t, before.ReviewedBy, assigned.ReviewedBy)
	require.Equal(t, before.ReviewedAt, assigned.ReviewedAt)
	require.Equal(t, before.AIReviewToken, assigned.AIReviewToken)
	require.Equal(t, before.PriceQuota, assigned.PriceQuota)
	require.Equal(t, before.PaymentMethods, assigned.PaymentMethods)

	_, err = SaveMerchantStoreCategory(f.root.Id, category.ID, MerchantStoreCategoryInput{Name: "Retired digital cards", Active: storeCustomCategoryBool(false)})
	require.NoError(t, err)
	var retained MerchantStoreProduct
	var stockAfter []MerchantStoreStock
	require.NoError(t, DB.First(&retained, "id = ?", f.product.ID).Error)
	require.NoError(t, DB.Where("product_id = ?", f.product.ID).Order("id").Find(&stockAfter).Error)
	require.Equal(t, assigned, retained, "disabling a classification cannot rewrite its products")
	require.Equal(t, stockBefore, stockAfter)
	public, err := GetPublicMerchantStoreProduct(f.product.ID)
	require.NoError(t, err)
	publicBadge := storeCustomCategoryJSON(t, public)["category"].(map[string]any)
	require.Equal(t, category.ID, publicBadge["id"])
	require.Equal(t, "Retired digital cards", publicBadge["name"])
	require.NotContains(t, publicBadge, "active")
	owner, err := GetMerchantStoreProduct(f.seller.Id, f.product.ID)
	require.NoError(t, err)
	ownerBadge := storeCustomCategoryJSON(t, owner)["category"].(map[string]any)
	require.Equal(t, false, ownerBadge["active"])
	_, err = SetMerchantStoreProductCategory(f.seller.Id, f.product.ID, category.ID)
	require.NoError(t, err, "an already assigned inactive category may be retained")
	other, err := SaveMerchantStoreCategory(f.root.Id, "", MerchantStoreCategoryInput{Name: "Other disabled", Active: storeCustomCategoryBool(false)})
	require.NoError(t, err)
	_, err = SetMerchantStoreProductCategory(f.seller.Id, f.product.ID, other.ID)
	require.ErrorIs(t, err, ErrMerchantStoreInput)
	input := storeCustomCategoryInput(&retained, &other.ID)
	input.Title = "Cannot newly select inactive"
	_, err = SaveMerchantStoreProduct(f.seller.Id, "", input)
	require.ErrorIs(t, err, ErrMerchantStoreInput)
	input = storeCustomCategoryInput(&retained, nil)
	saved, err := SaveMerchantStoreProduct(f.seller.Id, retained.ID, input)
	require.NoError(t, err)
	require.Equal(t, category.ID, storeCustomCategoryJSON(t, saved)["category_id"], "omitting category_id preserves the historical assignment")
	empty := ""
	saved, err = SaveMerchantStoreProduct(f.seller.Id, retained.ID, storeCustomCategoryInput(saved, &empty))
	require.NoError(t, err)
	require.Equal(t, "", storeCustomCategoryJSON(t, saved)["category_id"], "an explicit empty string clears the optional category")
}

func TestMerchantStoreCategoriesRequirePhaseSixForEveryMutation(t *testing.T) {
	f := storeCustomCategoryFixture(t)
	category, err := SaveMerchantStoreCategory(f.root.Id, "", MerchantStoreCategoryInput{Name: "Preserved"})
	require.NoError(t, err)
	_, err = SetMerchantStoreProductCategory(f.seller.Id, f.product.ID, category.ID)
	require.NoError(t, err)
	var before MerchantStoreProduct
	require.NoError(t, DB.First(&before, "id = ?", f.product.ID).Error)
	storeWriterGateForTest(t, "5")
	_, err = SaveMerchantStoreCategory(f.root.Id, category.ID, MerchantStoreCategoryInput{Name: "Forbidden downgrade edit", Active: storeCustomCategoryBool(false)})
	require.ErrorIs(t, err, ErrMerchantStoreWriterFrozen)
	_, err = SetMerchantStoreProductCategory(f.seller.Id, f.product.ID, "")
	require.ErrorIs(t, err, ErrMerchantStoreWriterFrozen)
	empty := ""
	_, err = SaveMerchantStoreProduct(f.seller.Id, f.product.ID, storeCustomCategoryInput(&before, &empty))
	require.ErrorIs(t, err, ErrMerchantStoreWriterFrozen)
	var after MerchantStoreProduct
	var retained MerchantStoreCategory
	require.NoError(t, DB.First(&after, "id = ?", f.product.ID).Error)
	require.NoError(t, DB.First(&retained, "id = ?", category.ID).Error)
	require.Equal(t, before, after)
	require.Equal(t, *category, retained)
}
