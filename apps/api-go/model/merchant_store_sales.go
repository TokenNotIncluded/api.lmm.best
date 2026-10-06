package model

import (
	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
)

// Sales limits count this product's lifetime paid obligations and outstanding
// inventory reservations. Importing inventory does not reset that history.
// NULL is unlimited; zero stops new orders without cancelling existing orders.
type merchantStoreSalesUsage struct {
	Paid     int64
	Reserved int64
}

func storeSalesUsage(tx *gorm.DB, productID string) (merchantStoreSalesUsage, error) {
	var usage merchantStoreSalesUsage
	// Verified but unfulfilled payments are real obligations, including payment
	// evidence received after a provider previously attested a closed session.
	if err := tx.Model(&MerchantStoreOrder{}).Where("product_id = ?", productID).
		Where("status = ? OR verified_payment_issue_at > 0", "paid").
		Select("COALESCE(SUM(quantity),0)").Scan(&usage.Paid).Error; err != nil {
		return usage, err
	}
	reservedOrders := tx.Model(&MerchantStoreStock{}).Select("order_id").
		Where("product_id = ? AND state = ?", productID, "reserved")
	// Count each unpaid order once, and only while it holds stock. Status alone
	// cannot prove a reservation. A verified payment is counted above, not twice.
	if err := tx.Model(&MerchantStoreOrder{}).Where("product_id = ?", productID).
		Where("status <> ? AND COALESCE(verified_payment_issue_at,0) = 0", "paid").
		Where("id IN (?)", reservedOrders).
		Select("COALESCE(SUM(quantity),0)").Scan(&usage.Reserved).Error; err != nil {
		return usage, err
	}
	if usage.Paid < 0 || usage.Reserved < 0 || usage.Paid > int64(common.MaxWalletQuota)-usage.Reserved {
		return usage, ErrMerchantStoreConflict
	}
	return usage, nil
}

func storeSaleRemaining(limit *int64, usage merchantStoreSalesUsage) (int64, error) {
	if limit == nil {
		return int64(common.MaxWalletQuota), nil
	}
	if *limit < 0 || *limit > int64(common.MaxWalletQuota) {
		return 0, ErrMerchantStoreInput
	}
	used := usage.Paid + usage.Reserved
	if used >= *limit {
		return 0, nil
	}
	return *limit - used, nil
}

func populateMerchantStoreSales(tx *gorm.DB, p *MerchantStoreProduct) error {
	usage, err := storeSalesUsage(tx, p.ID)
	if err != nil {
		return err
	}
	remaining, err := storeSaleRemaining(p.SaleLimit, usage)
	if err != nil {
		return err
	}
	p.PaidQuantity, p.ReservedQuantity = usage.Paid, usage.Reserved
	p.SaleAvailable = min(p.AvailableStock, remaining)
	return nil
}

func storeCheckSaleLimit(tx *gorm.DB, p *MerchantStoreProduct, quantity int) error {
	if p.SaleLimit == nil {
		return nil
	}
	usage, err := storeSalesUsage(tx, p.ID)
	if err != nil {
		return err
	}
	remaining, err := storeSaleRemaining(p.SaleLimit, usage)
	if err != nil {
		return err
	}
	if int64(quantity) > remaining {
		return ErrMerchantStoreStock
	}
	return nil
}

// Only this explicit endpoint changes a limit. Older product editors that omit
// sale_limit cannot accidentally clear it while saving product content.
func SetMerchantStoreProductSaleLimit(actor int, id string, limit *int64) error {
	if limit != nil && (*limit < 0 || *limit > int64(common.MaxWalletQuota)) {
		return ErrMerchantStoreInput
	}
	return storeWithProduct(id, func(tx *gorm.DB, p *MerchantStoreProduct) error {
		user, err := storeUser(tx, actor, common.RoleCommonUser)
		if err != nil {
			return err
		}
		if p.SellerID != actor && (p.TestMode || user.Role < common.RoleAdminUser) {
			return ErrMerchantStoreDenied
		}
		if err := tx.Model(p).Updates(map[string]any{"sale_limit": limit, "updated_at": common.GetTimestamp()}).Error; err != nil {
			return err
		}
		return storeEvent(tx, actor, p.ID, "sale_limit_updated")
	})
}

// Off-shelf listings retain their unedited approval and all fulfillment data.
// Editing content still moves them to a fresh draft and requires another review.
func SetMerchantStoreProductListed(actor int, id string, listed bool) error {
	return storeWithProduct(id, func(tx *gorm.DB, p *MerchantStoreProduct) error {
		user, err := storeUser(tx, actor, common.RoleCommonUser)
		if err != nil {
			return err
		}
		if p.SellerID != actor && (p.TestMode || user.Role < common.RoleAdminUser) {
			return ErrMerchantStoreDenied
		}
		if listed {
			if p.TestMode {
				return ErrMerchantStoreTestMode
			}
			if p.Status == "published" {
				return nil
			}
			if p.Status != "off_shelf" || p.ReviewedAt == 0 {
				return ErrMerchantStoreConflict
			}
			p.Status = "published"
		} else {
			if p.Status == "off_shelf" {
				return nil
			}
			if p.Status != "published" && p.Status != "paused" && !(p.TestMode && storeProductPurchaseStatus(p)) {
				return ErrMerchantStoreConflict
			}
			if err := invalidateMarketAIReview(tx, ModerationSourceMarketProduct, p.ID, p.AIReviewToken, "market_review_stale"); err != nil {
				return err
			}
			p.AIReviewToken = ""
			p.Status = "off_shelf"
		}
		p.UpdatedAt = common.GetTimestamp()
		if err := tx.Save(p).Error; err != nil {
			return err
		}
		return storeEvent(tx, actor, p.ID, p.Status)
	})
}
