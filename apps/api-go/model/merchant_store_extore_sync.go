package model

import (
	"encoding/json"
	"errors"
	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"strings"
	"time"
)

type MerchantStoreExtoreVariantInput struct {
	SourceID string
	Fields   MerchantStoreVariantInput
}

func SyncMerchantStoreExtoreProduct(seller int, connectionID, sourceID, revision string, in MerchantStoreProductInput, variants []MerchantStoreExtoreVariantInput, saleLimit *int64, summary json.RawMessage) (*MerchantStoreExtoreProduct, error) {
	if len(variants) == 0 || len(variants) > MerchantStoreMaximumVariants || sourceID == "" || len(sourceID) > 100 || len(revision) != 64 || !json.Valid(summary) {
		return nil, ErrMerchantStoreInput
	}
	seen := map[string]bool{}
	names := map[string]bool{}
	enabled := false
	for i := range variants {
		v := &variants[i]
		if v.SourceID == "" || seen[v.SourceID] || names[strings.ToLower(v.Fields.Name)] {
			return nil, ErrMerchantStoreInput
		}
		if err := storeValidateVariant(&v.Fields); err != nil {
			return nil, err
		}
		seen[v.SourceID] = true
		names[strings.ToLower(v.Fields.Name)] = true
		enabled = enabled || v.Fields.Enabled
	}
	if !enabled {
		return nil, ErrMerchantStoreInput
	}
	if saleLimit != nil && (*saleLimit < 0 || *saleLimit > int64(common.MaxWalletQuota)) {
		return nil, ErrMerchantStoreInput
	}
	var source MerchantStoreExtoreProduct
	err := marketTransaction(DB, func(tx *gorm.DB) error {
		if err := marketLockUsers(tx, seller); err != nil {
			return err
		}
		if err := storeRequireVariantWriter(tx); err != nil {
			return err
		}
		var connection MerchantStoreExtoreConnection
		if err := lockForUpdate(tx).Where("id=? AND seller_id=?", connectionID, seller).First(&connection).Error; err != nil {
			return err
		}
		if connection.RefreshPending || connection.ExpiresAt <= time.Now().Unix() {
			return ErrMerchantStoreExtoreReauthorize
		}
		key := storeHash("extore-product:" + fmtStoreActor(seller) + ":" + connection.Origin + ":" + connection.ShopID + ":" + sourceID)
		err := lockForUpdate(tx).First(&source, "id=?", key).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if source.ID == "" {
			source = MerchantStoreExtoreProduct{ID: key, SellerID: seller, ConnectionID: connectionID, SourceID: sourceID, VariantIDs: map[string]string{}}
		}
		if source.VariantIDs == nil {
			source.VariantIDs = map[string]string{}
		}
		defaultIndex := 0
		for i, v := range variants {
			if source.ProductID != "" && source.VariantIDs[v.SourceID] == MerchantStoreDefaultVariantID(source.ProductID) {
				defaultIndex = i
				break
			}
		}
		in.Variants = nil
		in.PriceQuota = variants[defaultIndex].Fields.PriceQuota
		in.Template = variants[defaultIndex].Fields.Template
		in.FixedContent = variants[defaultIndex].Fields.FixedContent
		product, err := saveMerchantStoreProductDB(tx, seller, source.ProductID, in, true)
		if err != nil {
			return err
		}
		firstSync := source.ProductID == ""
		source.ProductID = product.ID
		for i, v := range variants {
			if err := storeRequireMinimumUnitPrice(tx, v.Fields.PriceQuota); err != nil {
				return err
			}
			id := source.VariantIDs[v.SourceID]
			if id == "" {
				if firstSync && i == defaultIndex {
					id = MerchantStoreDefaultVariantID(product.ID)
				} else {
					id = uuid.NewString()
				}
				source.VariantIDs[v.SourceID] = id
			}
			var variant MerchantStoreVariant
			err := tx.Where("id=? AND product_id=?", id, product.ID).First(&variant).Error
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			if variant.ID == "" {
				variant = MerchantStoreVariant{ID: id, ProductID: product.ID, CreatedAt: time.Now().Unix()}
			}
			variant.Name = v.Fields.Name
			variant.PriceQuota = v.Fields.PriceQuota
			variant.Template = v.Fields.Template
			variant.Enabled = v.Fields.Enabled
			variant.UpdatedAt = time.Now().Unix()
			if err := storeSaveFixedContent(tx, product, &variant, v.Fields.FixedContent); err != nil {
				return err
			}
			if err := tx.Select("*").Save(&variant).Error; err != nil {
				return err
			}
		}
		// Removed specs are retired in place: paid orders and stock retain their IDs.
		for remoteID, localID := range source.VariantIDs {
			if !seen[remoteID] {
				if err := tx.Model(&MerchantStoreVariant{}).Where("id=? AND product_id=?", localID, product.ID).Update("enabled", false).Error; err != nil {
					return err
				}
			}
		}
		if err := tx.Model(product).Update("sale_limit", saleLimit).Error; err != nil {
			return err
		}
		source.ConnectionID = connectionID
		source.Revision = revision
		source.CardSummary = summary
		source.LastSyncedAt = time.Now().Unix()
		return tx.Select("*").Save(&source).Error
	})
	return &source, err
}
