package model

import (
	"errors"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
)

var ErrMerchantStorePurchaseLimit = errors.New("store purchase quantity limit reached")

func storePurchaseLimitValid(value *int64) bool {
	return value == nil || (*value >= 1 && *value <= int64(common.MaxWalletQuota))
}

// These fields are meaningful only after all serving writers enforce capability
// 4. Never let an older bridge admit orders that bypass a configured limit.
func storeRequirePurchaseLimitWriter(tx *gorm.DB) error {
	required, err := storeWriterGateRow(tx, "SHARE")
	if err != nil || required < 4 || required > MerchantStoreWriterCapability {
		return ErrMerchantStoreWriterFrozen
	}
	return nil
}

func MerchantStorePurchaseLimitsSupported() bool {
	required, err := storeWriterGateRow(DB, "")
	return err == nil && required >= 4 && required <= MerchantStoreWriterCapability
}

func storeApplyPurchaseLimits(tx *gorm.DB, p *MerchantStoreProduct, in MerchantStoreProductInput) error {
	orderPresent := in.maxQuantityPerOrderPresent || in.MaxQuantityPerOrder != nil
	buyerPresent := in.maxQuantityPerBuyerPresent || in.MaxQuantityPerBuyer != nil
	if !orderPresent && !buyerPresent {
		return nil
	}
	if err := storeRequirePurchaseLimitWriter(tx); err != nil {
		return err
	}
	if orderPresent {
		p.MaxQuantityPerOrder = in.MaxQuantityPerOrder
	}
	if buyerPresent {
		p.MaxQuantityPerBuyer = in.MaxQuantityPerBuyer
	}
	return nil
}

// Matches storeOrderStock, including historical empty-variant orders and the
// default variant's NULL/empty compatibility stock. Unrelated pools cannot
// release this buyer's entitlement by changing a refund's requested items.
const storeBuyerStockPool = `s.order_id = o.id AND s.product_id = o.product_id AND
 (COALESCE(o.variant_id,'') = '' OR s.variant_id = o.variant_id OR
 (o.variant_id = ? AND (s.variant_id IS NULL OR s.variant_id = '')))`

const storeBuyerPaidOrder = `(COALESCE(o.paid_at,0) > 0 OR o.status IN ('paid','refund_pending','refunded') OR COALESCE(o.verified_payment_issue_at,0) > 0)`

// Entitlement is net paid quantity plus actual unpaid stock holds, across all
// variants of the product. Only completed refunds move frozen stock to refunded;
// amount-only refunds and refund requests never release quantity here.
func storeBuyerPurchaseUsage(tx *gorm.DB, productID string, buyerID int) (int64, error) {
	var paid []struct {
		Quantity int64
		Refunded int64
	}
	if err := tx.Table("merchant_store_orders AS o").
		Joins("LEFT JOIN merchant_store_stocks AS s ON "+storeBuyerStockPool+" AND s.state = 'refunded'", MerchantStoreDefaultVariantID(productID)).
		Where("o.product_id = ? AND o.buyer_id = ?", productID, buyerID).
		Where(storeBuyerPaidOrder).
		Select("o.quantity AS quantity, COUNT(s.id) AS refunded").
		Group("o.id, o.quantity").Scan(&paid).Error; err != nil {
		return 0, err
	}
	var used int64
	for _, order := range paid {
		if order.Quantity < 1 || order.Quantity > int64(common.MaxWalletQuota) || order.Refunded < 0 || order.Refunded > order.Quantity || used > int64(common.MaxWalletQuota)-(order.Quantity-order.Refunded) {
			return 0, ErrMerchantStoreConflict
		}
		used += order.Quantity - order.Refunded
	}
	var held int64
	if err := tx.Table("merchant_store_orders AS o").
		Joins("JOIN merchant_store_stocks AS s ON "+storeBuyerStockPool, MerchantStoreDefaultVariantID(productID)).
		Where("o.product_id = ? AND o.buyer_id = ? AND s.state = 'reserved'", productID, buyerID).
		Where("NOT " + storeBuyerPaidOrder).
		Count(&held).Error; err != nil {
		return 0, err
	}
	if held < 0 || used > int64(common.MaxWalletQuota)-held {
		return 0, ErrMerchantStoreConflict
	}
	return used + held, nil
}

func storeBuyerPurchaseRemaining(tx *gorm.DB, p *MerchantStoreProduct, buyerID int) (int64, error) {
	if !storePurchaseLimitValid(p.MaxQuantityPerBuyer) || p.MaxQuantityPerBuyer == nil {
		return 0, ErrMerchantStoreInput
	}
	used, err := storeBuyerPurchaseUsage(tx, p.ID, buyerID)
	if err != nil {
		return 0, err
	}
	return max(0, *p.MaxQuantityPerBuyer-used), nil
}

func storeCheckPurchaseLimits(tx *gorm.DB, p *MerchantStoreProduct, buyerID, quantity int) error {
	if p.MaxQuantityPerOrder == nil && p.MaxQuantityPerBuyer == nil {
		return nil
	}
	if err := storeRequirePurchaseLimitWriter(tx); err != nil {
		return err
	}
	if !storePurchaseLimitValid(p.MaxQuantityPerOrder) || !storePurchaseLimitValid(p.MaxQuantityPerBuyer) {
		return ErrMerchantStoreInput
	}
	if p.MaxQuantityPerOrder != nil && int64(quantity) > *p.MaxQuantityPerOrder {
		return ErrMerchantStorePurchaseLimit
	}
	if p.MaxQuantityPerBuyer != nil {
		remaining, err := storeBuyerPurchaseRemaining(tx, p, buyerID)
		if err != nil {
			return err
		}
		if int64(quantity) > remaining {
			return ErrMerchantStorePurchaseLimit
		}
	}
	return nil
}

// Only the authenticated viewer's remainder is returned; public lists and
// anonymous requests never expose another account's purchase history.
func PopulateMerchantStoreBuyerPurchaseRemaining(actor int, p *MerchantStoreProduct) error {
	p.BuyerPurchaseRemaining = nil
	if actor < 1 || p.MaxQuantityPerBuyer == nil {
		return nil
	}
	remaining, err := storeBuyerPurchaseRemaining(DB, p, actor)
	if err != nil {
		return err
	}
	p.BuyerPurchaseRemaining = &remaining
	return nil
}
