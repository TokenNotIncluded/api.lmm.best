package model

import (
	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Likes are independent of a customer's saved favorites and never alter an
// order, inventory reservation, price, or wallet balance.
type MerchantStoreProductLike struct {
	UserID    int    `json:"-" gorm:"primaryKey;not null"`
	ProductID string `json:"product_id" gorm:"primaryKey;type:varchar(36);not null;index:store_product_likes_product"`
	CreatedAt int64  `json:"created_at" gorm:"type:bigint;not null"`
}

func (MerchantStoreProductLike) TableName() string { return "merchant_store_product_likes" }

func MerchantStoreSocialModels() []interface{} {
	return []interface{}{&MerchantStoreProductLike{}}
}

type MerchantStoreProductLikes struct {
	Supported bool   `json:"supported"`
	Count     *int64 `json:"count"`
	Liked     bool   `json:"liked"`
}

func storeLikesSupported(tx *gorm.DB) bool {
	return storeRequirePhaseSixReadable(tx) == nil && tx.Migrator().HasTable(&MerchantStoreProductLike{})
}

func MerchantStoreLikesSupported() bool { return storeLikesSupported(DB) }

func storeSocialProduct(tx *gorm.DB, actor int, productID string, lock bool) error {
	query := MerchantStoreVisibleProductsForViewer(tx, actor).Select("merchant_store_products.id")
	if lock {
		query = lockForUpdate(query)
	} else if tx.Dialector.Name() != "sqlite" {
		query = query.Clauses(clause.Locking{Strength: "SHARE"})
	}
	var product MerchantStoreProduct
	return query.Where("merchant_store_products.id = ?", productID).First(&product).Error
}

// Call only after the product visibility scope has authorized this viewer.
// An unsupported schema has an unknown count, rather than a fabricated zero.
func storeProductLikes(tx *gorm.DB, actor int, productID string) (MerchantStoreProductLikes, error) {
	view := MerchantStoreProductLikes{}
	if !storeLikesSupported(tx) {
		return view, nil
	}
	var summary struct {
		Count int64
		Liked int64
	}
	// One aggregate statement keeps the total and this account's state in the
	// same database snapshot, even when another tab is changing the same like.
	if err := tx.Model(&MerchantStoreProductLike{}).Where("product_id = ?", productID).
		Select("COUNT(*) AS count, COALESCE(MAX(CASE WHEN user_id = ? THEN 1 ELSE 0 END), 0) AS liked", actor).Scan(&summary).Error; err != nil {
		return view, err
	}
	view.Supported, view.Count, view.Liked = true, &summary.Count, actor > 0 && summary.Liked > 0
	return view, nil
}

func GetMerchantStoreProductLikes(actor int, productID string) (MerchantStoreProductLikes, error) {
	view := MerchantStoreProductLikes{}
	if actor < 0 {
		return view, ErrMerchantStoreDenied
	}
	err := marketTransaction(DB, func(tx *gorm.DB) error {
		if actor > 0 {
			if _, err := storeUser(tx, actor, common.RoleCommonUser); err != nil {
				return err
			}
		}
		if err := storeSocialProduct(tx, actor, productID, false); err != nil {
			return err
		}
		var err error
		view, err = storeProductLikes(tx, actor, productID)
		return err
	})
	return view, err
}

// The caller supplies the authenticated account and an absolute desired state.
// Account/product locks and the composite key make retries and parallel updates
// idempotent; no client-supplied count or read-then-toggle is trusted.
func SetMerchantStoreProductLike(actor int, productID string, liked bool) (MerchantStoreProductLikes, error) {
	view := MerchantStoreProductLikes{}
	if actor < 1 {
		return view, ErrMerchantStoreDenied
	}
	err := marketTransaction(DB, func(tx *gorm.DB) error {
		if err := storeRequireSocialWriter(tx); err != nil {
			return err
		}
		if !storeLikesSupported(tx) {
			return ErrMerchantStoreWriterFrozen
		}
		if err := storeSocialProduct(tx, actor, productID, true); err != nil {
			return err
		}
		if err := marketLockUsers(tx, actor); err != nil {
			return err
		}
		if _, err := storeUser(tx, actor, common.RoleCommonUser); err != nil {
			return err
		}
		if liked {
			item := MerchantStoreProductLike{UserID: actor, ProductID: productID, CreatedAt: common.GetTimestamp()}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&item).Error; err != nil {
				return err
			}
		} else if err := tx.Where("user_id = ? AND product_id = ?", actor, productID).Delete(&MerchantStoreProductLike{}).Error; err != nil {
			return err
		}
		var err error
		view, err = storeProductLikes(tx, actor, productID)
		return err
	})
	return view, err
}
