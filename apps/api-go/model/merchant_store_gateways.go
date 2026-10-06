package model

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
)

type MerchantStoreGateway struct {
	ID               string `json:"id" gorm:"primaryKey;size:64"`
	SellerID         int    `json:"seller_id" gorm:"uniqueIndex:store_gateway_seller_provider,priority:1"`
	Provider         string `json:"provider" gorm:"size:40;uniqueIndex:store_gateway_seller_provider,priority:2"`
	Enabled          bool   `json:"enabled"`
	ConfigCiphertext string `json:"-" gorm:"type:text"`
	UpdatedAt        int64  `json:"updated_at"`
}

func SaveMerchantStoreGateway(sellerID int, provider string, enabled bool, config string) (*MerchantStoreGateway, error) {
	if _, known := MerchantStorePaymentCategory(provider); !known {
		return nil, ErrMerchantStoreInput
	}
	if len(config) > 128<<10 || config != "" && !json.Valid([]byte(config)) {
		return nil, ErrMerchantStoreInput
	}
	var row MerchantStoreGateway
	e := marketTransaction(DB, func(tx *gorm.DB) error {
		if e := marketLockUsers(tx, sellerID); e != nil {
			return e
		}
		u, e := storeUser(tx, sellerID, common.RoleCommonUser)
		if e != nil {
			return e
		}
		if enabled && strings.HasPrefix(provider, "external:") && u.Quota <= MerchantStoreExternalMinimumQuota {
			return ErrMerchantStoreBalance
		}
		id := storeHash("gateway:" + fmtStoreActor(sellerID) + ":" + provider)
		if e = lockForUpdate(tx).First(&row, "id = ?", id).Error; e != nil && !errors.Is(e, gorm.ErrRecordNotFound) {
			return e
		}
		if row.ID == "" {
			row = MerchantStoreGateway{ID: id, SellerID: sellerID, Provider: provider}
		}
		if config != "" {
			cipher, e := storeEncrypt("gateway", id, config)
			if e != nil {
				return e
			}
			row.ConfigCiphertext = cipher
		}
		if enabled && strings.HasPrefix(provider, "external:") && row.ConfigCiphertext == "" {
			return ErrMerchantStoreInput
		}
		row.Enabled = enabled
		row.UpdatedAt = common.GetTimestamp()
		return tx.Save(&row).Error
	})
	return &row, e
}
func ListMerchantStoreGateways(sellerID int) ([]MerchantStoreGateway, error) {
	if _, e := storeUser(DB, sellerID, common.RoleCommonUser); e != nil {
		return nil, e
	}
	var rows []MerchantStoreGateway
	e := DB.Where("seller_id = ? AND provider IN ?", sellerID, MerchantStorePaymentProviders()).Order("provider ASC").Find(&rows).Error
	return rows, e
}

// This internal primitive is used only after the payment service authenticated
// the merchant. Callbacks use the frozen per-order encrypted context instead.
func GetMerchantStoreGatewaySecret(sellerID int, provider string) (string, error) {
	if _, known := MerchantStorePaymentCategory(provider); !known {
		return "", ErrMerchantStoreInput
	}
	if _, e := storeUser(DB, sellerID, common.RoleCommonUser); e != nil {
		return "", e
	}
	var row MerchantStoreGateway
	if e := DB.Where("seller_id = ? AND provider = ?", sellerID, provider).First(&row).Error; e != nil {
		if errors.Is(e, gorm.ErrRecordNotFound) {
			return "", nil
		}
		return "", e
	}
	return storeDecrypt("gateway", row.ID, row.ConfigCiphertext)
}
