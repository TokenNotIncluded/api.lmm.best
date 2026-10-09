package model

import (
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

func MerchantStoreProductVariantsCreateSupported() bool {
	floor, err := storeWriterGateRow(DB, "")
	return err == nil && floor >= 2 && floor <= MerchantStoreWriterCapability
}

// Existing editors retain their default-only contract. A create request may
// provide the complete initial set, but cannot use it to replace live stock IDs.
func storePrepareProductVariants(id string, in *MerchantStoreProductInput) error {
	if in.Variants == nil {
		return nil
	}
	if id != "" || len(in.Variants) == 0 || len(in.Variants) > MerchantStoreMaximumVariants {
		return ErrMerchantStoreInput
	}
	names := make(map[string]bool, len(in.Variants))
	enabled := false
	for i := range in.Variants {
		v := &in.Variants[i]
		if err := storeValidateVariant(v); err != nil {
			return err
		}
		name := strings.ToLower(v.Name)
		if !utf8.ValidString(v.Name) || strings.ContainsRune(v.Name, 0) || names[name] {
			return ErrMerchantStoreInput
		}
		names[name] = true
		enabled = enabled || v.Enabled
	}
	if !enabled {
		return ErrMerchantStoreInput
	}
	first := in.Variants[0]
	// Reject contradictory defaults rather than accepting two price sources.
	if in.PriceQuota != first.PriceQuota || in.Template != first.Template {
		return ErrMerchantStoreInput
	}
	if (in.FixedContent == nil) != (first.FixedContent == nil) ||
		(in.FixedContent != nil && *in.FixedContent != *first.FixedContent) {
		return ErrMerchantStoreInput
	}
	return nil
}

// Called only inside the owning product's creation transaction. A failed price,
// capability, or private-content check rolls back the product and every variant.
func storeCreateProductVariants(tx *gorm.DB, p *MerchantStoreProduct, first *MerchantStoreVariant, inputs []MerchantStoreVariantInput) error {
	if inputs == nil {
		return nil
	}
	if err := storeRequireVariantWriter(tx); err != nil {
		return err
	}
	for i, in := range inputs {
		if err := storeRequireMinimumUnitPrice(tx, in.PriceQuota); err != nil {
			return err
		}
		if i == 0 {
			if err := tx.Model(first).Updates(map[string]any{"name": in.Name, "enabled": in.Enabled}).Error; err != nil {
				return err
			}
			continue
		}
		variant := MerchantStoreVariant{
			ID: uuid.NewString(), ProductID: p.ID, Name: in.Name,
			PriceQuota: in.PriceQuota, Template: in.Template, Enabled: in.Enabled,
			CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
		}
		if err := tx.Create(&variant).Error; err != nil {
			return err
		}
		// GORM fills false with the schema's true default on insert. Preserve
		// the seller's explicit choice, as the existing variant editor does.
		if !in.Enabled {
			if err := tx.Model(&variant).UpdateColumn("enabled", false).Error; err != nil {
				return err
			}
			variant.Enabled = false
		}
		if err := storeSaveFixedContent(tx, p, &variant, in.FixedContent); err != nil {
			return err
		}
	}
	return nil
}
