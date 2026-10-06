package model

import (
	"errors"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrMerchantStoreMinimumPrice = errors.New("product unit price is below the configured minimum")

type MerchantStoreConfigPatch struct {
	FeeBPS                *int    `json:"fee_bps"`
	RecipientID           *int    `json:"recipient_id"`
	PromotionQuota        *int    `json:"promotion_quota"`
	LinuxDOUnitsPerUSD    *string `json:"linuxdo_units_per_usd"`
	MinimumUnitPriceQuota *int    `json:"minimum_unit_price_quota"`
}

// Explicit map values preserve a Root-selected zero on the first insert.
// A struct Create/Save would replace zero with the GORM column default.
func storeWriteConfig(tx *gorm.DB, c MerchantStoreConfig) error {
	columns := []string{"fee_bps", "recipient_id", "promotion_quota", "linux_do_units_per_usd", "minimum_unit_price_quota"}
	values := map[string]any{"id": 1, "fee_bps": c.FeeBPS, "recipient_id": c.RecipientID, "promotion_quota": c.PromotionQuota, "linux_do_units_per_usd": c.LinuxDOUnitsPerUSD, "minimum_unit_price_quota": c.MinimumUnitPriceQuota}
	return tx.Model(&MerchantStoreConfig{}).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoUpdates: clause.AssignmentColumns(columns)}).Create(values).Error
}

// Merge the provided fields under the singleton row lock. A fee/promotion
// update must not overwrite a concurrently changed, omitted minimum price.
func PatchMerchantStoreConfig(actor int, patch MerchantStoreConfigPatch) error {
	return marketTransaction(DB, func(tx *gorm.DB) error {
		initial, err := storeConfig(tx)
		if err != nil {
			return err
		}
		recipient := initial.RecipientID
		if patch.RecipientID != nil {
			recipient = *patch.RecipientID
		}
		if recipient <= 0 {
			return ErrMerchantStoreInput
		}
		if err = marketLockUsers(tx, actor, initial.RecipientID, recipient); err != nil {
			return err
		}
		if _, err = storeUser(tx, actor, common.RoleRootUser); err != nil {
			return err
		}
		if err = storeRequireWriter(tx); err != nil {
			return err
		}
		var current MerchantStoreConfig
		if err = lockForUpdate(tx).First(&current, 1).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			current = initial // Initial default only; persisted zero is never replaced.
		} else if err != nil {
			return err
		}
		if patch.FeeBPS != nil {
			current.FeeBPS = *patch.FeeBPS
		}
		if patch.RecipientID != nil {
			current.RecipientID = *patch.RecipientID
		}
		if patch.PromotionQuota != nil {
			current.PromotionQuota = *patch.PromotionQuota
		}
		if patch.LinuxDOUnitsPerUSD != nil {
			current.LinuxDOUnitsPerUSD = *patch.LinuxDOUnitsPerUSD
		}
		if patch.MinimumUnitPriceQuota != nil {
			current.MinimumUnitPriceQuota = *patch.MinimumUnitPriceQuota
		}
		if current.FeeBPS < 0 || current.FeeBPS > 10000 || !marketQuotaValid(current.PromotionQuota) || !marketQuotaValid(current.MinimumUnitPriceQuota) || !storeLinuxDORateValid(current.LinuxDOUnitsPerUSD) || current.RecipientID <= 0 {
			return ErrMerchantStoreInput
		}
		if _, err = storeUser(tx, current.RecipientID, common.RoleRootUser); err != nil {
			return err
		}
		return storeWriteConfig(tx, current)
	})
}

func storeRequireMinimumUnitPrice(tx *gorm.DB, unitPrice int) error {
	var row struct{ MinimumUnitPriceQuota int }
	err := lockForUpdate(tx).Model(&MerchantStoreConfig{}).Select("minimum_unit_price_quota").Where("id = ?", 1).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// Only the missing singleton uses the initial policy. This read never
		// inserts a row or treats an explicitly saved zero as missing.
		row.MinimumUnitPriceQuota = MerchantStoreCreditsPerUSD
	} else if err != nil {
		return err
	}
	if !marketQuotaValid(row.MinimumUnitPriceQuota) {
		return ErrMerchantStoreInput
	}
	if unitPrice < row.MinimumUnitPriceQuota {
		return ErrMerchantStoreMinimumPrice
	}
	return nil
}
