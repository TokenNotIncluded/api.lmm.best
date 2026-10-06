package model

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type MerchantStoreCatalogueQuery struct {
	Sort          string
	Tag           string
	Stock         string
	AutoDelivery  *bool
	AIProcessing  *bool
	GuestPurchase *bool
}

func (MerchantStoreCatalogueMetadata) TableName() string { return "merchant_store_catalogue_metadata" }
func MerchantStoreCatalogueModels() []interface{} {
	return []interface{}{&MerchantStoreCatalogueMetadata{}, &MerchantStoreCartItem{}, &MerchantStoreFavorite{}}
}

func storeCatalogueFeeSQL(price string, bps int) string {
	// Split integer multiplication so every intermediate stays within wallet
	// range. This is the same floor fee as checkout, without floating point.
	rate := strconv.Itoa(bps)
	remainder := "((" + price + ") % 10000)"
	small := "(" + remainder + " * " + rate + ")"
	return "(((" + price + ")-" + remainder + ")/10000)*" + rate + "+(" + small + "-(" + small + " % 10000))/10000"
}

func storeCatalogueTradableSQL(config MerchantStoreConfig) string {
	p := "merchant_store_products"
	seller := "(SELECT quota FROM users WHERE users.id=" + p + ".seller_id)"
	role := "(SELECT role FROM users WHERE users.id=" + p + ".seller_id)"
	gateway := []string{}
	for _, provider := range MerchantStorePaymentProviders() {
		encoded, _ := json.Marshal(provider)
		category, _ := MerchantStorePaymentCategory(provider)
		master := storeCategoryPlatformProvider
		if category == MerchantStoreCategoryExternal {
			master = storeCategoryExternalProvider
		}
		part := "(" + p + ".payment_methods LIKE '%" + string(encoded) + "%' AND (SELECT COUNT(*) FROM merchant_store_gateways g WHERE g.seller_id=" + p + ".seller_id AND g.provider='" + provider + "' AND g.enabled=TRUE)=1 AND NOT EXISTS (SELECT 1 FROM merchant_store_gateways m WHERE m.seller_id=" + p + ".seller_id AND m.provider='" + master + "' AND m.enabled=FALSE)"
		if category == MerchantStoreCategoryExternal {
			part += " AND " + seller + ">" + strconv.Itoa(MerchantStoreExternalMinimumQuota)
		}
		gateway = append(gateway, part+")")
	}
	price := "COALESCE(v.price_quota," + p + ".price_quota)"
	enabled := "(v.enabled=TRUE OR (v.id IS NULL AND (s.variant_id IS NULL OR s.variant_id='' OR s.variant_id=cm.default_variant_id)))"
	variantMatch := "CASE WHEN s.variant_id IS NULL OR s.variant_id='' THEN cm.default_variant_id ELSE s.variant_id END"
	stock := "EXISTS (SELECT 1 FROM merchant_store_stocks s LEFT JOIN merchant_store_variants v ON v.product_id=s.product_id AND v.id=" + variantMatch + " WHERE s.product_id=" + p + ".id AND s.state='available' AND " + enabled + " AND " + price + ">=" + strconv.Itoa(config.MinimumUnitPriceQuota) + " AND (" + role + ">=" + strconv.Itoa(common.RoleRootUser) + " OR " + seller + ">=" + storeCatalogueFeeSQL(price, config.FeeBPS) + "))"
	paid := "COALESCE((SELECT SUM(o.quantity) FROM merchant_store_orders o WHERE o.product_id=" + p + ".id AND (o.paid_at>0 OR o.status='paid' OR o.verified_payment_issue_at>0)),0)"
	reserved := "COALESCE((SELECT SUM(o.quantity) FROM merchant_store_orders o WHERE o.product_id=" + p + ".id AND o.status<>'paid' AND COALESCE(o.paid_at,0)=0 AND COALESCE(o.verified_payment_issue_at,0)=0 AND EXISTS (SELECT 1 FROM merchant_store_stocks r WHERE r.order_id=o.id AND r.state='reserved')),0)"
	return "(" + stock + " AND (" + p + ".sale_limit IS NULL OR " + p + ".sale_limit>" + paid + "+" + reserved + ") AND (" + strings.Join(gateway, " OR ") + "))"
}

func ListMerchantStoreCatalogue(viewer int, search string, sellerID, offset, limit int, in MerchantStoreCatalogueQuery) ([]MerchantStoreProduct, error) {
	if viewer < 0 || sellerID < 0 || int64(sellerID) > 2147483647 || len(search) > 200 || len(in.Tag) > 512 {
		return nil, ErrMerchantStoreInput
	}
	if in.Sort == "" {
		in.Sort = "comprehensive"
	}
	if in.Sort != "comprehensive" && in.Sort != "sales" && in.Sort != "newest" {
		return nil, ErrMerchantStoreInput
	}
	if in.Stock != "" && in.Stock != "in_stock" && in.Stock != "out_of_stock" {
		return nil, ErrMerchantStoreInput
	}
	if !MerchantStoreCatalogueSupported() {
		return nil, ErrMerchantStoreUnavailable
	}
	config, err := storeConfig(DB)
	if err != nil {
		return nil, err
	}
	offset, limit = storePage(offset, limit)
	query := MerchantStoreVisibleProductsForViewer(DB, viewer).Joins("LEFT JOIN merchant_store_catalogue_metadata cm ON cm.product_id=merchant_store_products.id")
	if sellerID != 0 {
		query = query.Where("merchant_store_products.seller_id = ?", sellerID)
	}
	if search != "" {
		literal := strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(search)
		query = query.Where("merchant_store_products.title LIKE ? ESCAPE '!' OR merchant_store_products.description LIKE ? ESCAPE '!'", "%"+literal+"%", "%"+literal+"%")
	}
	if in.Tag != "" {
		encoded, _ := json.Marshal(in.Tag)
		literal := strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(string(encoded))
		query = query.Where("cm.custom_tags LIKE ? ESCAPE '!'", "%"+literal+"%")
	}
	if in.AutoDelivery != nil {
		query = query.Where("COALESCE(cm.auto_delivery,FALSE) = ?", *in.AutoDelivery)
	}
	if in.AIProcessing != nil {
		query = query.Where("COALESCE(cm.ai_processing,FALSE) = ?", *in.AIProcessing)
	}
	if in.GuestPurchase != nil {
		query = query.Where("merchant_store_products.purchase_login_required = ?", !*in.GuestPurchase)
	}
	tradable := storeCatalogueTradableSQL(config)
	if in.Stock != "" {
		query = query.Where(tradable+" = ?", in.Stock == "in_stock")
	}
	sales := storeCatalogueNetSalesSQL()
	order := "merchant_store_products.created_at DESC,merchant_store_products.id ASC"
	var orderArgs []interface{}
	switch in.Sort {
	case "sales":
		order = sales + " DESC," + order
	case "comprehensive":
		// Purchased active promotion, proven tradability, then sales/freshness blend.
		// One net item contributes one day to the ranking score; id stabilizes ties.
		order = "CASE WHEN merchant_store_products.promotion_expires_at > ? AND " + tradable + " THEN 0 ELSE 1 END,CASE WHEN " + tradable + " THEN 0 ELSE 1 END,(" + sales + " * 86400.0 + merchant_store_products.created_at) DESC,merchant_store_products.id ASC"
		orderArgs = []interface{}{common.GetTimestamp()}
	}
	var products []MerchantStoreProduct
	if err := query.Select("merchant_store_products.*").Clauses(clause.OrderBy{Expression: gorm.Expr(order, orderArgs...)}).Offset(offset).Limit(limit).Find(&products).Error; err != nil {
		return nil, err
	}
	for i := range products {
		if err := populateMerchantStoreProduct(DB, &products[i], true); err != nil {
			return nil, err
		}
		if err := PopulateMerchantStoreCatalogue(DB, &products[i]); err != nil {
			return nil, err
		}
		products[i].ReviewNote, products[i].ReviewedBy = "", 0
	}
	return products, nil
}
