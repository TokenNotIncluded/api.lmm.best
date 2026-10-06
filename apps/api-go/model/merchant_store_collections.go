package model

import (
	"errors"
	"strconv"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type MerchantStoreCartItem struct {
	ID        string `json:"id" gorm:"primaryKey;type:varchar(64)"`
	UserID    int    `json:"-" gorm:"not null;uniqueIndex:store_cart_identity,priority:1;index"`
	ProductID string `json:"product_id" gorm:"type:varchar(36);not null;uniqueIndex:store_cart_identity,priority:2"`
	VariantID string `json:"variant_id" gorm:"type:varchar(36);not null;uniqueIndex:store_cart_identity,priority:3"`
	Quantity  int64  `json:"quantity" gorm:"type:bigint;not null"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

type MerchantStoreFavorite struct {
	UserID    int    `json:"-" gorm:"primaryKey;not null"`
	ProductID string `json:"product_id" gorm:"primaryKey;type:varchar(36)"`
	CreatedAt int64  `json:"created_at"`
}

type MerchantStoreCartInput struct {
	ProductID string `json:"product_id"`
	VariantID string `json:"variant_id"`
	Quantity  int64  `json:"quantity"`
}

type MerchantStoreCartView struct {
	MerchantStoreCartItem
	Valid             bool                  `json:"valid"`
	UnavailableReason *string               `json:"unavailable_reason"`
	Product           *MerchantStoreProduct `json:"product"`
}

type MerchantStoreFavoriteView struct {
	MerchantStoreFavorite
	Valid             bool                  `json:"valid"`
	UnavailableReason *string               `json:"unavailable_reason"`
	Product           *MerchantStoreProduct `json:"product"`
}

func storeCollectionProduct(tx *gorm.DB, actor int, id string) (*MerchantStoreProduct, error) {
	var p MerchantStoreProduct
	if err := MerchantStoreRetainedProductsForViewer(tx, actor).Where("merchant_store_products.id = ?", id).First(&p).Error; err != nil {
		return nil, err
	}
	if err := populateMerchantStoreProduct(tx, &p, true); err != nil {
		return nil, err
	}
	p.ReviewNote, p.ReviewedBy = "", 0
	return &p, nil
}

// A cart holds only current product/variant identity and quantity. Checking it
// never reserves stock, inserts a variant, or stores a historical price.
func storeCheckCartItem(tx *gorm.DB, actor int, in MerchantStoreCartInput) (*MerchantStoreProduct, string, error) {
	if in.Quantity < 1 || in.Quantity > int64(common.MaxWalletQuota) {
		return nil, "", ErrMerchantStoreInput
	}
	p, err := storeCollectionProduct(tx, actor, in.ProductID)
	if err != nil {
		return nil, "", err
	}
	if err := storeProductNewBuyer(p, actor); err != nil {
		if errors.Is(err, ErrMerchantStoreDenied) {
			return nil, "", gorm.ErrRecordNotFound
		}
		return p, in.VariantID, err
	}
	id := in.VariantID
	if id == "" {
		variants, err := storeVariants(tx, p)
		if err != nil {
			return nil, "", err
		}
		for _, v := range variants {
			if v.ID != MerchantStoreDefaultVariantID(p.ID) {
				return nil, "", ErrMerchantStoreVariantRequired
			}
		}
		id = MerchantStoreDefaultVariantID(p.ID)
	}
	for _, v := range p.Variants {
		if v.ID != id {
			continue
		}
		if v.TradingPaused || p.TradingPaused || v.SaleAvailable < in.Quantity {
			return p, id, ErrMerchantStoreStock
		}
		if err := storeCheckPurchaseLimits(tx, p, actor, int(in.Quantity)); err != nil {
			return p, id, err
		}
		return p, id, nil
	}
	return p, id, ErrMerchantStoreUnavailable
}

func SetMerchantStoreCartItem(actor int, in MerchantStoreCartInput) (*MerchantStoreCartItem, error) {
	var item MerchantStoreCartItem
	err := marketTransaction(DB, func(tx *gorm.DB) error {
		if err := marketLockUsers(tx, actor); err != nil {
			return err
		}
		if _, err := storeUser(tx, actor, common.RoleCommonUser); err != nil {
			return err
		}
		if err := storeRequireCatalogueWriter(tx); err != nil {
			return err
		}
		_, variant, err := storeCheckCartItem(tx, actor, in)
		if err != nil {
			return err
		}
		now := common.GetTimestamp()
		item = MerchantStoreCartItem{ID: storeHash(strconv.Itoa(actor) + ":" + in.ProductID + ":" + variant), UserID: actor, ProductID: in.ProductID, VariantID: variant, Quantity: in.Quantity, CreatedAt: now, UpdatedAt: now}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "user_id"}, {Name: "product_id"}, {Name: "variant_id"}}, DoUpdates: clause.AssignmentColumns([]string{"quantity", "updated_at"})}).Create(&item).Error; err != nil {
			return err
		}
		return tx.Where("id = ? AND user_id = ?", item.ID, actor).First(&item).Error
	})
	return &item, err
}

func storeCollectionReason(err error) (*string, error) {
	if err == nil {
		return nil, nil
	}
	reason := "unavailable"
	if errors.Is(err, gorm.ErrRecordNotFound) || errors.Is(err, ErrMerchantStoreDenied) {
		reason = "not_visible"
	} else if errors.Is(err, ErrMerchantStoreStock) {
		reason = "insufficient_stock"
	} else if errors.Is(err, ErrMerchantStorePurchaseLimit) {
		reason = "purchase_limit"
	} else if !errors.Is(err, ErrMerchantStoreUnavailable) && !errors.Is(err, ErrMerchantStoreVariantRequired) && !errors.Is(err, ErrMerchantStoreInput) {
		return nil, err
	}
	return &reason, nil
}

func ListMerchantStoreCart(actor, offset, limit int) ([]MerchantStoreCartView, error) {
	if _, err := storeUser(DB, actor, common.RoleCommonUser); err != nil {
		return nil, err
	}
	if !MerchantStoreCollectionsSupported() {
		return nil, ErrMerchantStoreUnavailable
	}
	offset, limit = storePage(offset, limit)
	var items []MerchantStoreCartItem
	if err := DB.Where("user_id = ?", actor).Order("updated_at DESC,id ASC").Offset(offset).Limit(limit).Find(&items).Error; err != nil {
		return nil, err
	}
	views := make([]MerchantStoreCartView, 0, len(items))
	for _, item := range items {
		p, _, check := storeCheckCartItem(DB, actor, MerchantStoreCartInput{ProductID: item.ProductID, VariantID: item.VariantID, Quantity: item.Quantity})
		reason, err := storeCollectionReason(check)
		if err != nil {
			return nil, err
		}
		views = append(views, MerchantStoreCartView{MerchantStoreCartItem: item, Valid: check == nil, UnavailableReason: reason, Product: p})
	}
	return views, nil
}

func DeleteMerchantStoreCartItem(actor int, id string) error {
	return marketTransaction(DB, func(tx *gorm.DB) error {
		if _, err := storeUser(tx, actor, common.RoleCommonUser); err != nil {
			return err
		}
		if err := storeRequireCatalogueWriter(tx); err != nil {
			return err
		}
		return tx.Where("user_id = ? AND id = ?", actor, id).Delete(&MerchantStoreCartItem{}).Error
	})
}

func SetMerchantStoreFavorite(actor int, productID string) error {
	return marketTransaction(DB, func(tx *gorm.DB) error {
		if _, err := storeUser(tx, actor, common.RoleCommonUser); err != nil {
			return err
		}
		if err := storeRequireCatalogueWriter(tx); err != nil {
			return err
		}
		if _, err := storeCollectionProduct(tx, actor, productID); err != nil {
			return err
		}
		item := MerchantStoreFavorite{UserID: actor, ProductID: productID, CreatedAt: common.GetTimestamp()}
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&item).Error
	})
}

func DeleteMerchantStoreFavorite(actor int, productID string) error {
	return marketTransaction(DB, func(tx *gorm.DB) error {
		if _, err := storeUser(tx, actor, common.RoleCommonUser); err != nil {
			return err
		}
		if err := storeRequireCatalogueWriter(tx); err != nil {
			return err
		}
		return tx.Where("user_id = ? AND product_id = ?", actor, productID).Delete(&MerchantStoreFavorite{}).Error
	})
}

func ListMerchantStoreFavorites(actor, offset, limit int) ([]MerchantStoreFavoriteView, error) {
	if _, err := storeUser(DB, actor, common.RoleCommonUser); err != nil {
		return nil, err
	}
	if !MerchantStoreCollectionsSupported() {
		return nil, ErrMerchantStoreUnavailable
	}
	offset, limit = storePage(offset, limit)
	var items []MerchantStoreFavorite
	if err := DB.Where("user_id = ?", actor).Order("created_at DESC,product_id ASC").Offset(offset).Limit(limit).Find(&items).Error; err != nil {
		return nil, err
	}
	views := make([]MerchantStoreFavoriteView, 0, len(items))
	for _, item := range items {
		p, check := storeCollectionProduct(DB, actor, item.ProductID)
		reason, err := storeCollectionReason(check)
		if err != nil {
			return nil, err
		}
		views = append(views, MerchantStoreFavoriteView{MerchantStoreFavorite: item, Valid: check == nil, UnavailableReason: reason, Product: p})
	}
	return views, nil
}

// Cleanup pages through account-owned identities; it neither loads the entire
// store nor treats temporary insufficient stock as a deleted product.
func CleanupMerchantStoreCollections(actor int) (int64, int64, error) {
	var carts, favorites int64
	err := marketTransaction(DB, func(tx *gorm.DB) error {
		if _, err := storeUser(tx, actor, common.RoleCommonUser); err != nil {
			return err
		}
		if err := storeRequireCatalogueWriter(tx); err != nil {
			return err
		}
		visible := MerchantStoreRetainedProductsForViewer(tx, actor).Select("merchant_store_products.id")
		result := tx.Where("user_id = ? AND product_id NOT IN (?)", actor, visible).Delete(&MerchantStoreCartItem{})
		if result.Error != nil {
			return result.Error
		}
		carts = result.RowsAffected
		result = tx.Where("user_id = ? AND variant_id <> ? AND NOT EXISTS (SELECT 1 FROM merchant_store_variants v WHERE v.product_id=merchant_store_cart_items.product_id AND v.id=merchant_store_cart_items.variant_id AND v.enabled=TRUE) AND NOT EXISTS (SELECT 1 FROM merchant_store_catalogue_metadata cm WHERE cm.product_id=merchant_store_cart_items.product_id AND cm.default_variant_id=merchant_store_cart_items.variant_id AND NOT EXISTS (SELECT 1 FROM merchant_store_variants dv WHERE dv.id=cm.default_variant_id AND dv.product_id=cm.product_id))", actor, "").Delete(&MerchantStoreCartItem{})
		if result.Error != nil {
			return result.Error
		}
		carts += result.RowsAffected
		result = tx.Where("user_id = ? AND product_id NOT IN (?)", actor, visible).Delete(&MerchantStoreFavorite{})
		favorites = result.RowsAffected
		return result.Error
	})
	return carts, favorites, err
}

func MerchantStoreCollectionsSupported() bool {
	return MerchantStoreCatalogueSupported() && DB.Migrator().HasTable(&MerchantStoreCartItem{}) && DB.Migrator().HasTable(&MerchantStoreFavorite{})
}

func ClearMerchantStoreCollections(actor int, cart bool) error {
	return marketTransaction(DB, func(tx *gorm.DB) error {
		if _, err := storeUser(tx, actor, common.RoleCommonUser); err != nil {
			return err
		}
		if err := storeRequireCatalogueWriter(tx); err != nil {
			return err
		}
		query := tx.Where("user_id = ?", actor)
		if cart {
			return query.Delete(&MerchantStoreCartItem{}).Error
		}
		return query.Delete(&MerchantStoreFavorite{}).Error
	})
}
