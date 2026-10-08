package model

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var commerceImportRevision = regexp.MustCompile(`^[0-9a-f]{64}$`)
var commerceImportReferencePrice = regexp.MustCompile(`^[0-9]+(?:\.[0-9]{1,6})?$`)

func commerceImportIdentity(s string) bool {
	if len(s) == 0 || utf8.RuneCountInString(s) > 100 || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if r < 32 || r == 127 {
			return false
		}
	}
	return true
}

// Import saves a confirmed local draft and the source identities together. It
// uses the ordinary draft/variant paths, including their review retirement.
func ImportCommerceImportProduct(actor int, lease CommerceImportLease, in CommerceImportProductInput) (*MerchantStoreCommerceProductMapping, error) {
	if !commerceImportIdentity(in.ExternalProductID) || !commerceImportIdentity(in.ShopID) || !commerceImportRevision.MatchString(in.Revision) || in.PriceQuota == nil || *in.PriceQuota <= 0 || in.Visibility == nil || (*in.Visibility != "private" && *in.Visibility != "public") || len(in.Variants) == 0 || len(in.Variants) > MerchantStoreMaximumVariants || len(in.RawJSON) > 16<<20 || !json.Valid([]byte(in.RawJSON)) {
		return nil, ErrMerchantStoreInput
	}
	in.Product.PriceQuota, in.Product.Visibility = *in.PriceQuota, in.Visibility
	if in.Product.Template == MerchantStoreFixedContentTemplate {
		return nil, ErrMerchantStoreInput
	}
	seen := map[string]bool{}
	for _, v := range in.Variants {
		if !commerceImportIdentity(v.ExternalID) || seen[v.ExternalID] || v.PriceQuota == nil || *v.PriceQuota <= 0 || len(v.RawJSON) > 1<<20 || !json.Valid([]byte(v.RawJSON)) || (v.ReferencePrice != nil && (len(*v.ReferencePrice) > 100 || !commerceImportReferencePrice.MatchString(*v.ReferencePrice))) {
			return nil, ErrMerchantStoreInput
		}
		seen[v.ExternalID] = true
	}
	var result MerchantStoreCommerceProductMapping
	err := marketTransaction(DB, func(tx *gorm.DB) error {
		conn, err := commerceImportLeaseConnection(tx, actor, lease)
		if err != nil {
			return err
		}
		if conn.Status != "active" || conn.GrantExpiresAt <= common.GetTimestamp() || conn.ShopID != in.ShopID || !commerceImportHasScope(conn.Scope, "products.read") {
			return ErrCommerceImportReauthorize
		}
		// An account row is the cross-connection serialization point for the
		// seller/issuer/shop/product unique mapping.
		if err := marketLockUsers(tx, actor); err != nil {
			return err
		}
		err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("seller_id = ? AND issuer = ? AND shop_id = ? AND external_product_id = ?", actor, conn.Issuer, conn.ShopID, in.ExternalProductID).First(&result).Error
		id := ""
		if err == nil {
			id = result.LocalProductID
			p, err := storeProductOwner(tx, actor, id)
			if err != nil {
				return err
			}
			if p.Status == "pending" || p.Status == "deleted" {
				return ErrMerchantStoreConflict
			}
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		p, err := storeSaveProductDraft(tx, actor, id, in.Product)
		if err != nil {
			return err
		}
		now := common.GetTimestamp()
		if id == "" {
			result = MerchantStoreCommerceProductMapping{ID: uuid.NewString(), SellerID: actor, Issuer: conn.Issuer, ShopID: conn.ShopID, ExternalProductID: in.ExternalProductID, LocalProductID: p.ID, CreatedAt: now}
		}
		result.ConnectionID, result.Revision, result.RedemptionURL, result.Mode, result.RawJSON, result.UpdatedAt = conn.ID, in.Revision, in.RedemptionURL, in.Mode, in.RawJSON, now
		if err := tx.Save(&result).Error; err != nil {
			return err
		}
		var previous []MerchantStoreCommerceVariantMapping
		if err := tx.Where("product_mapping_id = ?", result.ID).Find(&previous).Error; err != nil {
			return err
		}
		byExternal := map[string]MerchantStoreCommerceVariantMapping{}
		for _, v := range previous {
			byExternal[v.ExternalID] = v
		}
		result.Variants = make([]MerchantStoreCommerceVariantMapping, 0, len(in.Variants))
		for i, external := range in.Variants {
			mapped, exists := byExternal[external.ExternalID]
			localID := ""
			enabled := external.Enabled
			if exists {
				localID = mapped.LocalVariantID
				local, err := storeVariant(tx, p, localID)
				if err != nil {
					return err
				}
				// Syncing an enabled remote spec does not undo a merchant's
				// local stop or resurrect a previously disabled remote spec.
				enabled = enabled && mapped.Enabled && local.Enabled
			} else if id == "" && i == 0 {
				localID = MerchantStoreDefaultVariantID(p.ID)
			}
			name := strings.TrimSpace(external.Name)
			if name == "" {
				name = external.ExternalID
			}
			local, err := storeSaveVariantDraft(tx, actor, p, localID, MerchantStoreVariantInput{Name: name, PriceQuota: *external.PriceQuota, Template: p.Template, Enabled: enabled})
			if err != nil {
				return err
			}
			if !exists {
				mapped = MerchantStoreCommerceVariantMapping{ID: uuid.NewString(), ProductMappingID: result.ID, ExternalID: external.ExternalID, LocalVariantID: local.ID, CreatedAt: now}
			}
			mapped.ReferencePrice, mapped.Currency, mapped.Description, mapped.RawJSON, mapped.Enabled, mapped.UpdatedAt = external.ReferencePrice, external.Currency, external.Description, external.RawJSON, external.Enabled, now
			if err := tx.Save(&mapped).Error; err != nil {
				return err
			}
			result.Variants = append(result.Variants, mapped)
		}
		for _, old := range previous {
			if seen[old.ExternalID] {
				continue
			}
			if err := tx.Model(&MerchantStoreCommerceVariantMapping{}).Where("id = ?", old.ID).Updates(map[string]any{"enabled": false, "updated_at": now}).Error; err != nil {
				return err
			}
			if err := tx.Model(&MerchantStoreVariant{}).Where("id = ? AND product_id = ?", old.LocalVariantID, p.ID).Updates(map[string]any{"enabled": false, "updated_at": now}).Error; err != nil {
				return err
			}
		}
		return storeEvent(tx, actor, p.ID, "commerce_import")
	})
	return &result, err
}

func ListCommerceImportMappings(actor int, connectionID string) ([]MerchantStoreCommerceProductMapping, error) {
	var rows []MerchantStoreCommerceProductMapping
	err := marketTransaction(DB, func(tx *gorm.DB) error {
		if err := storeRequireCommerceImportReadable(tx); err != nil {
			return err
		}
		if _, err := storeUser(tx, actor, common.RoleCommonUser); err != nil {
			return err
		}
		var conn MerchantStoreCommerceConnection
		if err := tx.Where("id = ? AND seller_id = ?", connectionID, actor).First(&conn).Error; err != nil {
			return err
		}
		if err := tx.Where("connection_id = ? AND seller_id = ?", connectionID, actor).Order("created_at ASC,id ASC").Find(&rows).Error; err != nil {
			return err
		}
		for i := range rows {
			if err := tx.Where("product_mapping_id = ?", rows[i].ID).Order("created_at ASC,id ASC").Find(&rows[i].Variants).Error; err != nil {
				return err
			}
		}
		return nil
	})
	return rows, err
}

func commerceImportHasScope(scope, required string) bool {
	for _, s := range strings.Fields(scope) {
		if s == required {
			return true
		}
	}
	return false
}
