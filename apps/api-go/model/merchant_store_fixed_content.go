package model

import (
	"strings"
	"unicode/utf8"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const MerchantStoreFixedContentTemplate = "fixed-content"
const MerchantStoreFixedContentMaxBytes = 128 << 10

// Shared content is configuration, never an inventory unit. Its private order
// copy is encrypted for that order and survives subsequent seller edits.
type MerchantStoreFixedContent struct {
	VariantID  string `json:"-" gorm:"primaryKey;type:varchar(36)"`
	ProductID  string `json:"-" gorm:"type:varchar(36);not null;index"`
	Ciphertext string `json:"-" gorm:"type:text;not null"`
	UpdatedAt  int64  `json:"-"`
}

type MerchantStoreOrderFixedDelivery struct {
	OrderID    string `json:"-" gorm:"primaryKey;type:varchar(64)"`
	Ciphertext string `json:"-" gorm:"type:text;not null"`
	CreatedAt  int64  `json:"-"`
}

func storeFixedContentModels() []interface{} {
	return []interface{}{&MerchantStoreFixedContent{}, &MerchantStoreOrderFixedDelivery{}}
}

func storeFixedContentTable(table string) bool {
	return table == "merchant_store_fixed_contents" || table == "merchant_store_order_fixed_deliveries"
}

func storeFixedDelivery(o *MerchantStoreOrder) bool {
	return o.DeliveryTemplate == MerchantStoreFixedContentTemplate
}

func MerchantStoreFixedContentSupported() bool { return storeFixedContentSupported(DB) }

func storeFixedContentSupported(tx *gorm.DB) bool {
	floor, err := storeWriterGateRow(tx, "")
	return err == nil && floor >= 7 && floor <= MerchantStoreWriterCapability
}

func storeRequireFixedContentWriter(tx *gorm.DB) error {
	if err := storePhaseSixFence(tx, true); err != nil {
		return err
	}
	floor, err := storeWriterGateRow(tx, "SHARE")
	if err != nil || floor < 7 || floor > MerchantStoreWriterCapability {
		return ErrMerchantStoreWriterFrozen
	}
	return nil
}

// Called while the owning product row is locked. Omission preserves the
// previous private configuration; callers cannot smuggle content into a public
// template or turn stock rows into unlimited configuration.
func storeSaveFixedContent(tx *gorm.DB, p *MerchantStoreProduct, v *MerchantStoreVariant, content *string) error {
	if v.Template != MerchantStoreFixedContentTemplate {
		if content != nil {
			return ErrMerchantStoreInput
		}
		return nil
	}
	if err := storeRequireFixedContentWriter(tx); err != nil {
		return err
	}
	var count int64
	if err := storeVariantStock(tx.Model(&MerchantStoreStock{}), p.ID, v.ID).Where("state IN ?", []string{"available", "reserved"}).Count(&count).Error; err != nil {
		return err
	}
	if count != 0 {
		return ErrMerchantStoreConflict
	}
	if content == nil {
		_, err := storeReadFixedContent(tx, p.ID, v.ID)
		return err
	}
	if strings.TrimSpace(*content) == "" || len(*content) > MerchantStoreFixedContentMaxBytes || !utf8.ValidString(*content) || strings.ContainsRune(*content, 0) {
		return ErrMerchantStoreInput
	}
	cipher, err := storeEncrypt("fixed-content", p.ID+":"+v.ID, *content)
	if err != nil {
		return err
	}
	row := MerchantStoreFixedContent{VariantID: v.ID, ProductID: p.ID, Ciphertext: cipher, UpdatedAt: common.GetTimestamp()}
	return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "variant_id"}}, DoUpdates: clause.AssignmentColumns([]string{"ciphertext", "updated_at"})}).Create(&row).Error
}

func storeReadFixedContent(tx *gorm.DB, productID, variantID string) (string, error) {
	if !storeFixedContentSupported(tx) {
		return "", ErrMerchantStoreWriterFrozen
	}
	var row MerchantStoreFixedContent
	if err := tx.Where("product_id = ? AND variant_id = ?", productID, variantID).First(&row).Error; err != nil {
		return "", ErrMerchantStoreUnavailable
	}
	return storeDecrypt("fixed-content", productID+":"+variantID, row.Ciphertext)
}

func GetMerchantStoreFixedContent(actor int, productID, variantID string) (string, error) {
	var content string
	err := storeWithActiveProduct(productID, func(tx *gorm.DB, p *MerchantStoreProduct) error {
		if p.SellerID != actor {
			return ErrMerchantStoreDenied
		}
		if _, err := storeUser(tx, actor, common.RoleCommonUser); err != nil {
			return err
		}
		v, err := storeVariant(tx, p, variantID)
		if err != nil {
			return err
		}
		if v.Template != MerchantStoreFixedContentTemplate {
			return ErrMerchantStoreInput
		}
		content, err = storeReadFixedContent(tx, productID, variantID)
		return err
	})
	return content, err
}

func storeSnapshotFixedContent(tx *gorm.DB, p *MerchantStoreProduct, v *MerchantStoreVariant, o *MerchantStoreOrder) error {
	if err := storeRequireFixedContentWriter(tx); err != nil {
		return err
	}
	content, err := storeReadFixedContent(tx, p.ID, v.ID)
	if err != nil {
		return err
	}
	cipher, err := storeEncrypt("order-fixed-delivery", o.ID, content)
	if err != nil {
		return err
	}
	return tx.Create(&MerchantStoreOrderFixedDelivery{OrderID: o.ID, Ciphertext: cipher, CreatedAt: o.CreatedAt}).Error
}

// Existing paid obligations remain readable while a later writer is installed.
// Only configuration and new snapshots require the active phase-seven floor.
func storeReadOrderFixedContent(tx *gorm.DB, o *MerchantStoreOrder) (string, error) {
	var row MerchantStoreOrderFixedDelivery
	if err := tx.First(&row, "order_id = ?", o.ID).Error; err != nil {
		return "", ErrMerchantStoreConflict
	}
	return storeDecrypt("order-fixed-delivery", o.ID, row.Ciphertext)
}

// No synthetic stock IDs are produced. Refund rows are the durable entitlement
// ledger for shared content, while card orders continue to use stock states.
func storeFixedRefundQuantity(tx *gorm.DB, o *MerchantStoreOrder, statuses []string) (int64, error) {
	var quantity int64
	err := tx.Model(&MerchantStoreRefund{}).Where("order_id = ? AND status IN ?", o.ID, statuses).Select("COALESCE(SUM(quantity),0)").Scan(&quantity).Error
	if err == nil && (quantity < 0 || quantity > int64(o.Quantity)) {
		err = ErrMerchantStoreConflict
	}
	return quantity, err
}

func storeFixedAvailableQuantity(tx *gorm.DB, o *MerchantStoreOrder, includeRequests bool) (int, error) {
	statuses := []string{"completed", "awaiting_provider", "reconciliation_required"}
	if includeRequests {
		statuses = append(statuses, "requested")
	}
	refunded, err := storeFixedRefundQuantity(tx, o, statuses)
	return o.Quantity - int(refunded), err
}
