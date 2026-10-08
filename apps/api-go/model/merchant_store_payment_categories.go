package model

import (
	"errors"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	MerchantStoreCategoryPlatform = "platform"
	MerchantStoreCategoryExternal = "external"
	storeCategoryPlatformProvider = "category:platform"
	storeCategoryExternalProvider = "category:external"
)

var ErrMerchantStorePaymentCategoryDisabled = errors.New("merchant payment category is disabled")
var ErrMerchantStorePaymentSelection = errors.New("product payment methods must be enabled merchant methods")

// Category rows are policy, never payment providers or credential containers.
// This is the single supported-provider set used by product and gateway APIs.
func MerchantStorePaymentProviders() []string {
	return []string{"external:epay", "external:waffo_pancake", "platform:waffo_pancake", "platform:linuxdo", "balance"}
}

func MerchantStorePaymentCategory(provider string) (string, bool) {
	switch provider {
	case "balance", "platform:waffo_pancake", "platform:linuxdo":
		return MerchantStoreCategoryPlatform, true
	case "external:epay", "external:waffo_pancake":
		return MerchantStoreCategoryExternal, true
	default:
		return "", false
	}
}

type MerchantStorePaymentCategories struct {
	PlatformEnabled bool `json:"platform_enabled"`
	ExternalEnabled bool `json:"external_enabled"`
}

func (s MerchantStorePaymentCategories) Enabled(provider string) bool {
	category, known := MerchantStorePaymentCategory(provider)
	if !known {
		return false
	}
	if category == MerchantStoreCategoryExternal {
		return s.ExternalEnabled
	}
	return s.PlatformEnabled
}

func storePaymentCategories(tx *gorm.DB, sellerID int) (MerchantStorePaymentCategories, error) {
	var rows []MerchantStoreGateway
	if err := tx.Select("provider,enabled").Where("seller_id = ?", sellerID).Find(&rows).Error; err != nil {
		return MerchantStorePaymentCategories{}, err
	}
	var result MerchantStorePaymentCategories
	// Preserve existing explicit channel enablement when no master was saved.
	// A new seller with no enabled channel starts with both categories off.
	for _, row := range rows {
		category, known := MerchantStorePaymentCategory(row.Provider)
		if known && row.Enabled {
			if category == MerchantStoreCategoryExternal {
				result.ExternalEnabled = true
			} else {
				result.PlatformEnabled = true
			}
		}
	}
	for _, row := range rows {
		switch row.Provider {
		case storeCategoryPlatformProvider:
			result.PlatformEnabled = row.Enabled
		case storeCategoryExternalProvider:
			result.ExternalEnabled = row.Enabled
		}
	}
	return result, nil
}

func GetMerchantStorePaymentCategories(sellerID int) (MerchantStorePaymentCategories, error) {
	if _, err := storeUser(DB, sellerID, common.RoleCommonUser); err != nil {
		return MerchantStorePaymentCategories{}, err
	}
	return storePaymentCategories(DB, sellerID)
}

func SetMerchantStorePaymentCategories(sellerID int, value MerchantStorePaymentCategories) error {
	return marketTransaction(DB, func(tx *gorm.DB) error {
		// Checkout and first issuance lock this same seller before reading policy.
		if err := marketLockUsers(tx, sellerID); err != nil {
			return err
		}
		if err := storeRequireWriter(tx); err != nil {
			return err
		}
		if _, err := storeUser(tx, sellerID, common.RoleCommonUser); err != nil {
			return err
		}
		for provider, enabled := range map[string]bool{storeCategoryPlatformProvider: value.PlatformEnabled, storeCategoryExternalProvider: value.ExternalEnabled} {
			row := MerchantStoreGateway{ID: storeHash("gateway:" + fmtStoreActor(sellerID) + ":" + provider), SellerID: sellerID, Provider: provider, Enabled: enabled, UpdatedAt: common.GetTimestamp()}
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "seller_id"}, {Name: "provider"}}, DoUpdates: clause.AssignmentColumns([]string{"enabled", "updated_at"})}).Create(&row).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// Capture compatibility before changing a channel. A first channel enable
// must not turn a new merchant's default-off category into implicit opt-in.
// Existing enabled legacy channels retain their pre-edit category policy.
func storePersistMissingPaymentCategories(tx *gorm.DB, sellerID int) error {
	value, err := storePaymentCategories(tx, sellerID)
	if err != nil {
		return err
	}
	for provider, enabled := range map[string]bool{storeCategoryPlatformProvider: value.PlatformEnabled, storeCategoryExternalProvider: value.ExternalEnabled} {
		row := MerchantStoreGateway{ID: storeHash("gateway:" + fmtStoreActor(sellerID) + ":" + provider), SellerID: sellerID, Provider: provider, Enabled: enabled, UpdatedAt: common.GetTimestamp()}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
			return err
		}
	}
	return nil
}

func storeRequirePaymentCategory(tx *gorm.DB, sellerID int, provider string) error {
	categories, err := storePaymentCategories(tx, sellerID)
	if err != nil {
		return err
	}
	if !categories.Enabled(provider) {
		return ErrMerchantStorePaymentCategoryDisabled
	}
	return nil
}

func storeValidatePaymentSelection(tx *gorm.DB, seller *User, methods []string) error {
	if len(methods) == 0 {
		return nil
	}
	categories, err := storePaymentCategories(tx, seller.Id)
	if err != nil {
		return err
	}
	for _, method := range methods {
		if !categories.Enabled(method) {
			return ErrMerchantStorePaymentSelection
		}
		category, known := MerchantStorePaymentCategory(method)
		if !known || category == MerchantStoreCategoryExternal && seller.Quota <= MerchantStoreExternalMinimumQuota {
			return ErrMerchantStorePaymentSelection
		}
		var count int64
		if err = tx.Model(&MerchantStoreGateway{}).Where("seller_id = ? AND provider = ? AND enabled = ?", seller.Id, method, true).Count(&count).Error; err != nil {
			return err
		}
		if count != 1 {
			return ErrMerchantStorePaymentSelection
		}
	}
	return nil
}
