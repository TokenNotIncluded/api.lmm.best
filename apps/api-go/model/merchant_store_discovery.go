package model

import (
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func merchantStoreViewerCatalogue(viewerID int) *gorm.DB {
	return MerchantStoreVisibleProductsForViewer(DB, viewerID)
}

// These viewer-aware entry points share the public catalogue's filtering and
// stock/payment calculation. Private test-product visibility is supplied by
// the test-mode implementation, never by the seller's draft editor query.
func GetMerchantStoreProductForViewer(viewerID int, id string) (*MerchantStoreProduct, error) {
	if viewerID < 0 {
		return nil, ErrMerchantStoreInput
	}
	var product MerchantStoreProduct
	if err := merchantStoreViewerCatalogue(viewerID).Where("merchant_store_products.id = ?", id).First(&product).Error; err != nil {
		return nil, err
	}
	if err := populateMerchantStoreProduct(DB, &product, true); err != nil {
		return nil, err
	}
	product.ReviewNote, product.ReviewedBy = "", 0
	return &product, nil
}

func ListMerchantStoreProductsForViewer(viewerID int, search string, offset, limit int) ([]MerchantStoreProduct, error) {
	if viewerID < 0 {
		return nil, ErrMerchantStoreInput
	}
	if len(search) > 200 {
		return nil, ErrMerchantStoreInput
	}
	offset, limit = storePage(offset, limit)
	query := merchantStoreViewerCatalogue(viewerID)
	if search != "" {
		literal := strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(search)
		query = query.Where("title LIKE ? ESCAPE '!' OR description LIKE ? ESCAPE '!'", "%"+literal+"%", "%"+literal+"%")
	}
	var products []MerchantStoreProduct
	if err := query.Clauses(clause.OrderBy{Expression: gorm.Expr("CASE WHEN promotion_expires_at > ? THEN 0 ELSE 1 END, CASE WHEN promotion_expires_at > ? THEN promotion_expires_at ELSE 0 END DESC,created_at DESC,id ASC", common.GetTimestamp(), common.GetTimestamp())}).Offset(offset).Limit(limit).Find(&products).Error; err != nil {
		return nil, err
	}
	for index := range products {
		if err := populateMerchantStoreProduct(DB, &products[index], true); err != nil {
			return nil, err
		}
		products[index].ReviewNote, products[index].ReviewedBy = "", 0
	}
	return products, nil
}
