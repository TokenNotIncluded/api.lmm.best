package model

import (
	"encoding/json"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Catalogue annotations never change pricing, delivery or access permissions.
type MerchantStoreCatalogueMetadata struct {
	ProductID        string   `json:"-" gorm:"primaryKey;type:varchar(36)"`
	DefaultVariantID string   `json:"-" gorm:"type:varchar(36);not null"`
	CustomTags       []string `json:"custom_tags" gorm:"serializer:json;type:text"`
	AutoDelivery     bool     `json:"auto_delivery" gorm:"not null;default:false"`
	AIProcessing     bool     `json:"ai_processing" gorm:"not null;default:false"`
	UpdatedAt        int64    `json:"-"`
}

func storeRequireCatalogueWriter(tx *gorm.DB) error {
	required, err := storeWriterGateRow(tx, "SHARE")
	if err != nil || required < 5 || required > MerchantStoreWriterCapability {
		return ErrMerchantStoreWriterFrozen
	}
	return nil
}

func validateStoreCatalogueMetadata(in *MerchantStoreCatalogueMetadata) error {
	tags := make([]string, 0, len(in.CustomTags))
	seen := map[string]bool{}
	for _, raw := range in.CustomTags {
		tag := strings.TrimSpace(raw)
		if tag == "" || !utf8.ValidString(tag) || utf8.RuneCountInString(tag) > 128 {
			return ErrMerchantStoreInput
		}
		for _, r := range tag {
			if unicode.IsControl(r) {
				return ErrMerchantStoreInput
			}
		}
		if !seen[tag] {
			tags = append(tags, tag)
			seen[tag] = true
		}
	}
	encoded, err := json.Marshal(tags)
	if err != nil || len(encoded) > 32768 {
		return ErrMerchantStoreInput
	}
	in.CustomTags = tags
	return nil
}

func SetMerchantStoreCatalogueMetadata(actor int, id string, in MerchantStoreCatalogueMetadata) error {
	if err := validateStoreCatalogueMetadata(&in); err != nil {
		return err
	}
	return marketTransaction(DB, func(tx *gorm.DB) error {
		if err := marketLockUsers(tx, actor); err != nil {
			return err
		}
		if err := storeRequireCatalogueWriter(tx); err != nil {
			return err
		}
		if _, err := storeUser(tx, actor, common.RoleCommonUser); err != nil {
			return err
		}
		p, err := storeProductOwner(tx, actor, id)
		if err != nil {
			return err
		}
		if p.Status == "deleted" {
			return ErrMerchantStoreDenied
		}
		in.ProductID = p.ID
		in.DefaultVariantID = MerchantStoreDefaultVariantID(p.ID)
		in.UpdatedAt = common.GetTimestamp()
		return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "product_id"}}, DoUpdates: clause.AssignmentColumns([]string{"custom_tags", "auto_delivery", "ai_processing", "default_variant_id", "updated_at"})}).Create(&in).Error
	})
}

// Paid quantities are real attested orders, reduced only by completed quantity
// refunds. Pending/cancelled checkout sessions and monetary-only refunds do not
// manufacture or subtract delivered item sales.
func storeCatalogueNetSalesSQL() string {
	refunded := "COALESCE((SELECT SUM(r.quantity) FROM merchant_store_refunds r WHERE r.order_id=o.id AND r.status='completed' AND r.mode='quantity' AND r.completed_at>0),0)"
	return "COALESCE((SELECT SUM(CASE WHEN o.quantity>" + refunded + " THEN o.quantity-" + refunded + " ELSE 0 END) FROM merchant_store_orders o WHERE o.product_id=merchant_store_products.id AND o.price_quota>0 AND o.buyer_id<>o.seller_id AND (o.buyer_id>0 OR (o.buyer_id=0 AND LENGTH(COALESCE(o.guest_id,''))=36)) AND (o.paid_at>0 OR o.status='paid' OR o.verified_payment_issue_at>0)),0)"
}

func PopulateMerchantStoreCatalogue(tx *gorm.DB, p *MerchantStoreProduct) error {
	metadata := MerchantStoreCatalogueMetadata{ProductID: p.ID, CustomTags: []string{}}
	if tx.Migrator().HasTable(&MerchantStoreCatalogueMetadata{}) {
		if err := tx.Where("product_id = ?", p.ID).Find(&metadata).Error; err != nil {
			return err
		}
	}
	if MerchantStoreCatalogueSupported() && metadata.DefaultVariantID != MerchantStoreDefaultVariantID(p.ID) {
		return ErrMerchantStoreUnavailable
	}
	if metadata.CustomTags == nil {
		metadata.CustomTags = []string{}
	}
	p.Catalogue = &metadata
	p.DisplayTags = []string{}
	if p.SaleAvailable > 0 && !p.TradingPaused {
		p.DisplayTags = append(p.DisplayTags, "in_stock")
	} else {
		p.DisplayTags = append(p.DisplayTags, "out_of_stock")
	}
	if !p.PurchaseLoginRequired {
		p.DisplayTags = append(p.DisplayTags, "guest_purchase")
	}
	if metadata.AutoDelivery {
		p.DisplayTags = append(p.DisplayTags, "auto_delivery")
	}
	if metadata.AIProcessing {
		p.DisplayTags = append(p.DisplayTags, "ai_processing")
	}
	// Older floors cannot prove the guest identity/refund schema: unknown net
	// sales remain null rather than executing newer-column SQL or faking gross.
	if MerchantStoreCatalogueSupported() && tx.Migrator().HasTable(&MerchantStoreRefund{}) {
		var net int64
		if err := tx.Model(&MerchantStoreProduct{}).Where("merchant_store_products.id = ?", p.ID).Select(storeCatalogueNetSalesSQL()).Scan(&net).Error; err != nil {
			return err
		}
		p.NetPaidQuantity = &net
		return nil
	}
	p.NetPaidQuantity = nil
	return nil
}

func storeEnsureCatalogueMetadata(tx *gorm.DB, p *MerchantStoreProduct) error {
	required, err := storeWriterGateRow(tx, "")
	if err != nil {
		return err
	}
	if required < 5 {
		return nil
	}
	if err := storeRequireCatalogueWriter(tx); err != nil {
		return err
	}
	row := MerchantStoreCatalogueMetadata{ProductID: p.ID, DefaultVariantID: MerchantStoreDefaultVariantID(p.ID), CustomTags: []string{}}
	return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error
}

// Reviewed migration preparation only: bounded pages write identity projections
// to the new metadata table, preserving all product, inventory and order rows.
// This does not activate any writer floor and is never called from a read API.
func BackfillMerchantStoreCatalogueMappings(tx *gorm.DB) error {
	last := ""
	for {
		var products []MerchantStoreProduct
		if err := tx.Select("id").Where("id > ?", last).Order("id ASC").Limit(100).Find(&products).Error; err != nil {
			return err
		}
		if len(products) == 0 {
			return nil
		}
		for _, p := range products {
			row := MerchantStoreCatalogueMetadata{ProductID: p.ID, DefaultVariantID: MerchantStoreDefaultVariantID(p.ID), CustomTags: []string{}}
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "product_id"}}, DoUpdates: clause.AssignmentColumns([]string{"default_variant_id"})}).Create(&row).Error; err != nil {
				return err
			}
			last = p.ID
		}
	}
}

func MerchantStoreCatalogueSupported() bool {
	required, err := storeWriterGateRow(DB, "")
	if err != nil || required < 5 || required > MerchantStoreWriterCapability || !MerchantStoreAccessSupported() {
		return false
	}
	for _, item := range MerchantStoreCatalogueModels() {
		if !DB.Migrator().HasTable(item) {
			return false
		}
	}
	return true
}
