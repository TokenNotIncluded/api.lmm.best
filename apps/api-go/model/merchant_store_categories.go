package model

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/google/uuid"
	"golang.org/x/text/unicode/norm"
	"gorm.io/gorm"
)

// Categories are global catalogue labels. Disabling a label never deletes the
// products or their retained classification.
type MerchantStoreCategory struct {
	ID             string `json:"id" gorm:"primaryKey;size:36"`
	Name           string `json:"name" gorm:"size:320;not null"`
	NormalizedName string `json:"-" gorm:"size:320;not null;uniqueIndex:store_category_name"`
	SortOrder      int    `json:"sort_order" gorm:"not null"`
	Active         bool   `json:"active" gorm:"not null;index"`
	CreatedAt      int64  `json:"created_at"`
	UpdatedAt      int64  `json:"updated_at"`
}

type MerchantStoreCategoryInput struct {
	Name      string `json:"name"`
	SortOrder int    `json:"sort_order"`
	Active    *bool  `json:"active,omitempty"`
}

type MerchantStoreCategoryBadge struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Active *bool  `json:"active,omitempty"`
}

func (MerchantStoreCategory) TableName() string { return "merchant_store_categories" }

func MerchantStoreCategoryModels() []interface{} {
	return []interface{}{&MerchantStoreCategory{}}
}

func storeCategoriesSupported(tx *gorm.DB) bool {
	return tx != nil && storeRequirePhaseSixReadable(tx) == nil &&
		tx.Migrator().HasTable(&MerchantStoreCategory{}) && tx.Migrator().HasColumn(&MerchantStoreProduct{}, "category_id")
}

func MerchantStoreCategoriesSupported() bool { return storeCategoriesSupported(DB) }

func storeCategoryIDValid(id string) bool {
	if id == "" {
		return true
	}
	parsed, err := uuid.Parse(id)
	return err == nil && parsed.String() == id
}

func normalizeStoreCategoryName(name string) (string, error) {
	if !utf8.ValidString(name) {
		return "", ErrMerchantStoreInput
	}
	name = norm.NFC.String(strings.Join(strings.Fields(name), " "))
	if name == "" || utf8.RuneCountInString(name) > 80 {
		return "", ErrMerchantStoreInput
	}
	for _, character := range name {
		if unicode.IsControl(character) {
			return "", ErrMerchantStoreInput
		}
	}
	return name, nil
}

func SaveMerchantStoreCategory(actor int, id string, in MerchantStoreCategoryInput) (*MerchantStoreCategory, error) {
	name, err := normalizeStoreCategoryName(in.Name)
	if err != nil || !storeCategoryIDValid(id) || in.SortOrder < -1000000 || in.SortOrder > 1000000 {
		return nil, ErrMerchantStoreInput
	}
	var category MerchantStoreCategory
	err = marketTransaction(DB, func(tx *gorm.DB) error {
		if err := marketLockUsers(tx, actor); err != nil {
			return err
		}
		if _, err := storeUser(tx, actor, common.RoleAdminUser); err != nil {
			return err
		}
		if err := storeRequireCategoriesWriter(tx); err != nil {
			return err
		}
		if !storeCategoriesSupported(tx) {
			return ErrMerchantStoreWriterFrozen
		}
		now := common.GetTimestamp()
		if id == "" {
			category = MerchantStoreCategory{ID: uuid.NewString(), Active: true, CreatedAt: now}
		} else if err := lockForUpdate(tx).Where("id = ?", id).First(&category).Error; err != nil {
			return err
		}
		category.Name, category.NormalizedName = name, strings.ToLower(name)
		category.SortOrder, category.UpdatedAt = in.SortOrder, now
		if in.Active != nil {
			category.Active = *in.Active
		}
		var count int64
		if err := tx.Model(&MerchantStoreCategory{}).Where("normalized_name = ? AND id <> ?", category.NormalizedName, category.ID).Count(&count).Error; err != nil {
			return err
		}
		if count != 0 {
			return ErrMerchantStoreConflict
		}
		write := tx.Create
		if id != "" {
			write = tx.Select("*").Save
		}
		if err := write(&category).Error; err != nil {
			var state interface{ SQLState() string }
			if uniqueConstraintError(err) || errors.Is(err, gorm.ErrDuplicatedKey) || (errors.As(err, &state) && state.SQLState() == "23505") {
				return ErrMerchantStoreConflict
			}
			return err
		}
		return storeEvent(tx, actor, category.ID, "category_saved")
	})
	return &category, err
}

func ListMerchantStoreCategories(actor int, includeInactive bool, offset, limit int) ([]MerchantStoreCategory, error) {
	if includeInactive {
		if _, err := storeUser(DB, actor, common.RoleAdminUser); err != nil {
			return nil, err
		}
	}
	if !MerchantStoreCategoriesSupported() {
		return nil, ErrMerchantStoreUnavailable
	}
	offset, limit = storePage(offset, limit)
	query := DB.Model(&MerchantStoreCategory{})
	if !includeInactive {
		query = query.Where("active = ?", true)
	}
	var categories []MerchantStoreCategory
	err := query.Order("sort_order ASC,normalized_name ASC,id ASC").Offset(offset).Limit(limit).Find(&categories).Error
	return categories, err
}

func storeApplyProductCategory(tx *gorm.DB, product *MerchantStoreProduct, requested *string) error {
	if requested == nil {
		return nil
	}
	if !storeCategoryIDValid(*requested) {
		return ErrMerchantStoreInput
	}
	if err := storeRequireCategoriesWriter(tx); err != nil {
		return err
	}
	if !storeCategoriesSupported(tx) {
		return ErrMerchantStoreWriterFrozen
	}
	if *requested != "" {
		var category MerchantStoreCategory
		if err := lockForUpdate(tx).Where("id = ?", *requested).First(&category).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrMerchantStoreInput
			}
			return err
		}
		if !category.Active && *requested != product.CategoryID {
			return ErrMerchantStoreInput
		}
	}
	product.CategoryID = *requested
	return nil
}

// Classification is not a content/price edit. A published listing can change
// its category while retaining its current approval and review token.
func SetMerchantStoreProductCategory(actor int, id, categoryID string) (*MerchantStoreProduct, error) {
	err := marketTransaction(DB, func(tx *gorm.DB) error {
		if err := storeRequireCategoriesWriter(tx); err != nil {
			return err
		}
		product, err := storeProductOwner(tx, actor, id)
		if err != nil {
			return err
		}
		if err := marketLockUsers(tx, actor); err != nil {
			return err
		}
		if _, err := storeUser(tx, actor, common.RoleCommonUser); err != nil {
			return err
		}
		if err := storeApplyProductCategory(tx, product, &categoryID); err != nil {
			return err
		}
		if err := tx.Model(product).Updates(map[string]interface{}{"category_id": product.CategoryID, "updated_at": common.GetTimestamp()}).Error; err != nil {
			return err
		}
		return storeEvent(tx, actor, product.ID, "category_updated")
	})
	if err != nil {
		return nil, err
	}
	return GetMerchantStoreProduct(actor, id)
}

func populateMerchantStoreCategory(tx *gorm.DB, product *MerchantStoreProduct, public bool) error {
	product.Category = nil
	if product.CategoryID == "" || !storeCategoriesSupported(tx) {
		return nil
	}
	var category MerchantStoreCategory
	if err := tx.Where("id = ?", product.CategoryID).First(&category).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	product.Category = &MerchantStoreCategoryBadge{ID: category.ID, Name: category.Name}
	if !public {
		product.Category.Active = &category.Active
	}
	return nil
}
