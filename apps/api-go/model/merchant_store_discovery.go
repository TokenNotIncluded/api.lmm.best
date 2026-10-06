package model

// These viewer-aware entry points share the public catalogue's filtering and
// stock/payment calculation. Private test-product visibility is supplied by
// the test-mode implementation, never by the seller's draft editor query.
func GetMerchantStoreProductForViewer(viewerID int, id string) (*MerchantStoreProduct, error) {
	if viewerID < 0 {
		return nil, ErrMerchantStoreInput
	}
	return GetPublicMerchantStoreProduct(id)
}

func ListMerchantStoreProductsForViewer(viewerID int, search string, offset, limit int) ([]MerchantStoreProduct, error) {
	if viewerID < 0 {
		return nil, ErrMerchantStoreInput
	}
	return ListPublicMerchantStoreProducts(search, offset, limit)
}
