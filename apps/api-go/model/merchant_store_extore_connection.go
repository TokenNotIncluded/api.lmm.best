package model

import (
	"encoding/json"
	"errors"
	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"time"
)

var ErrMerchantStoreExtoreReauthorize = errors.New("Extore connection requires authorization again")
var ErrMerchantStoreExtoreSourceManaged = errors.New("Edit this imported product in Extore, then synchronize it")

type MerchantStoreExtoreConnection struct {
	ID             string `json:"id" gorm:"primaryKey;size:36"`
	SellerID       int    `json:"-" gorm:"not null;uniqueIndex:store_extore_shop,priority:1"`
	Origin         string `json:"origin" gorm:"size:300;not null;uniqueIndex:store_extore_shop,priority:2"`
	ShopID         string `json:"shop_id" gorm:"size:100;not null;uniqueIndex:store_extore_shop,priority:3"`
	ShopName       string `json:"shop_name" gorm:"size:300"`
	ClientID       string `json:"-" gorm:"size:200"`
	Ciphertext     string `json:"-" gorm:"type:text;not null"`
	ExpiresAt      int64  `json:"expires_at"`
	RefreshPending bool   `json:"-"`
	Version        int64  `json:"-"`
	UpdatedAt      int64  `json:"updated_at"`
}
type MerchantStoreExtoreProduct struct {
	ID           string            `json:"-" gorm:"primaryKey;size:64"`
	SellerID     int               `json:"-" gorm:"not null;index"`
	ConnectionID string            `json:"connection_id" gorm:"size:36;index"`
	SourceID     string            `json:"source_id" gorm:"size:100"`
	ProductID    string            `json:"product_id" gorm:"size:36;uniqueIndex"`
	Revision     string            `json:"revision" gorm:"size:64"`
	VariantIDs   map[string]string `json:"-" gorm:"serializer:json;type:text"`
	CardSummary  json.RawMessage   `json:"card_summary,omitempty" gorm:"type:text"`
	LastSyncedAt int64             `json:"last_synced_at"`
}

func MerchantStoreExtoreModels() []interface{} {
	return []interface{}{&MerchantStoreExtoreConnection{}, &MerchantStoreExtoreProduct{}}
}
func SaveMerchantStoreExtoreConnection(seller int, origin, shopID, shopName, clientID, secret string, expires int64) (*MerchantStoreExtoreConnection, error) {
	var row MerchantStoreExtoreConnection
	err := marketTransaction(DB, func(tx *gorm.DB) error {
		if err := marketLockUsers(tx, seller); err != nil {
			return err
		}
		if _, err := storeUser(tx, seller, common.RoleCommonUser); err != nil {
			return err
		}
		if err := storeRequireWriter(tx); err != nil {
			return err
		}
		err := lockForUpdate(tx).Where("seller_id=? AND origin=? AND shop_id=?", seller, origin, shopID).First(&row).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if row.ID == "" {
			row = MerchantStoreExtoreConnection{ID: uuid.NewString(), SellerID: seller, Origin: origin, ShopID: shopID}
		}
		cipher, err := storeEncrypt("extore-connection", row.ID, secret)
		if err != nil {
			return err
		}
		row.Ciphertext = cipher
		row.ClientID = clientID
		row.ShopName = shopName
		row.ExpiresAt = expires
		row.RefreshPending = false
		row.Version++
		row.UpdatedAt = time.Now().Unix()
		return tx.Select("*").Save(&row).Error
	})
	return &row, err
}
func GetMerchantStoreExtoreConnection(seller int, id string) (*MerchantStoreExtoreConnection, string, error) {
	var row MerchantStoreExtoreConnection
	if err := DB.Where("id=? AND seller_id=?", id, seller).First(&row).Error; err != nil {
		return nil, "", err
	}
	if row.RefreshPending || row.ExpiresAt <= time.Now().Unix() {
		return nil, "", ErrMerchantStoreExtoreReauthorize
	}
	secret, err := storeDecrypt("extore-connection", row.ID, row.Ciphertext)
	return &row, secret, err
}
func ListMerchantStoreExtoreConnections(seller int) ([]MerchantStoreExtoreConnection, error) {
	var rows []MerchantStoreExtoreConnection
	err := DB.Where("seller_id=?", seller).Order("updated_at DESC,id").Find(&rows).Error
	return rows, err
}

// Persist an in-flight marker before rotating a one-use refresh token. An
// uncertain response cannot cause another process to replay that token.
func BeginMerchantStoreExtoreRefresh(row *MerchantStoreExtoreConnection) error {
	result := DB.Model(&MerchantStoreExtoreConnection{}).Where("id=? AND seller_id=? AND version=? AND refresh_pending=?", row.ID, row.SellerID, row.Version, false).Update("refresh_pending", true)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrMerchantStoreExtoreReauthorize
	}
	return nil
}
func FinishMerchantStoreExtoreRefresh(row *MerchantStoreExtoreConnection, secret string) error {
	cipher, err := storeEncrypt("extore-connection", row.ID, secret)
	if err != nil {
		return err
	}
	result := DB.Model(&MerchantStoreExtoreConnection{}).Where("id=? AND seller_id=? AND version=? AND refresh_pending=?", row.ID, row.SellerID, row.Version, true).Updates(map[string]any{"ciphertext": cipher, "refresh_pending": false, "version": row.Version + 1, "updated_at": time.Now().Unix()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrMerchantStoreExtoreReauthorize
	}
	return nil
}
func MerchantStoreExtoreProductSources(seller int) ([]MerchantStoreExtoreProduct, error) {
	var rows []MerchantStoreExtoreProduct
	err := DB.Where("seller_id=?", seller).Find(&rows).Error
	return rows, err
}
func storeRequireLocalProductSource(tx *gorm.DB, id string) error {
	if !tx.Migrator().HasTable(&MerchantStoreExtoreProduct{}) {
		return nil
	}
	var count int64
	if err := tx.Model(&MerchantStoreExtoreProduct{}).Where("product_id=?", id).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return ErrMerchantStoreExtoreSourceManaged
	}
	return nil
}
