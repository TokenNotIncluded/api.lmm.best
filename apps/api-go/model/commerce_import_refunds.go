package model

import "gorm.io/gorm"

// Local refunds cannot verify or revoke redemption at the external issuer.
// Keep this read after refund access checks and retain the warning when a
// connection is disconnected: the product's source mapping remains intact.
func storeCommerceImportRefundRedemptionStatus(tx *gorm.DB, o *MerchantStoreOrder, v *MerchantStoreRefundView) error {
	floor, err := storeWriterGateRow(tx, "")
	if err != nil || floor < 8 || floor > MerchantStoreWriterCapability {
		// Earlier stores (and frozen legacy reads) must not touch new tables.
		return nil
	}
	var count int64
	if err := tx.Model(&MerchantStoreCommerceProductMapping{}).
		Where("seller_id = ? AND local_product_id = ?", o.SellerID, o.ProductID).
		Limit(1).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		v.ExternalRedemptionStatus = "unknown"
	}
	return nil
}
