package model

import (
	"errors"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

const MerchantStoreMaximumVariants = 200

var ErrMerchantStoreVariantRequired = errors.New("select an exact store variant")

type MerchantStoreVariant struct {
	ID                 string `json:"id" gorm:"primaryKey;type:varchar(36)"`
	ProductID          string `json:"product_id" gorm:"type:varchar(36);not null;index"`
	Name               string `json:"name" gorm:"type:varchar(200);not null"`
	PriceQuota         int    `json:"price_quota" gorm:"type:bigint;not null"`
	Template           string `json:"template" gorm:"type:varchar(32);not null"`
	Enabled            bool   `json:"enabled" gorm:"not null"`
	CreatedAt          int64  `json:"created_at"`
	UpdatedAt          int64  `json:"updated_at"`
	IsDefault          bool   `json:"is_default" gorm:"-"`
	UnlimitedSupply    bool   `json:"unlimited_supply" gorm:"-"`
	InventoryTotal     int64  `json:"inventory_total" gorm:"-"`
	InventoryAvailable int64  `json:"inventory_available" gorm:"-"`
	ReservedStock      int64  `json:"reserved_stock" gorm:"-"`
	SaleAvailable      int64  `json:"sale_available" gorm:"-"`
	TradingPaused      bool   `json:"trading_paused" gorm:"-"`
}

type MerchantStoreVariantInput struct {
	Name         string  `json:"name"`
	PriceQuota   int     `json:"price_quota"`
	Template     string  `json:"template"`
	Enabled      bool    `json:"enabled"`
	FixedContent *string `json:"fixed_content,omitempty"`
}

func MerchantStoreDefaultVariantID(productID string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("merchant-store-default:"+productID)).String()
}

func storeVirtualDefaultVariant(p *MerchantStoreProduct) MerchantStoreVariant {
	return MerchantStoreVariant{ID: MerchantStoreDefaultVariantID(p.ID), ProductID: p.ID, Name: "Default", PriceQuota: p.PriceQuota, Template: p.Template, Enabled: true, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt, IsDefault: true}
}

func storeRequireVariantWriter(tx *gorm.DB) error {
	required, err := storeWriterGateRow(tx, "SHARE")
	if err != nil || required < 2 || required > MerchantStoreWriterCapability || MerchantStoreWriterCapability < 2 {
		return ErrMerchantStoreWriterFrozen
	}
	return nil
}

func storeRequireVariantPublication(tx *gorm.DB, p *MerchantStoreProduct) error {
	var count int64
	if err := tx.Model(&MerchantStoreVariant{}).Where("product_id = ? AND id <> ?", p.ID, MerchantStoreDefaultVariantID(p.ID)).Count(&count).Error; err != nil {
		return err
	}
	if count != 0 {
		return storeRequireVariantWriter(tx)
	}
	return nil
}

// Reads never write: legacy stock and historical orders stay byte-for-byte intact.
func storeVariants(tx *gorm.DB, p *MerchantStoreProduct) ([]MerchantStoreVariant, error) {
	var variants []MerchantStoreVariant
	if err := tx.Where("product_id = ?", p.ID).Order("created_at ASC,id ASC").Find(&variants).Error; err != nil {
		return nil, err
	}
	// A gate-1 rolling upgrade still permits the compatibility writer, which
	// knows only the product price/template. No durable spec may predate gate 2.
	if required, _ := storeWriterGateRow(tx, ""); required == 1 && len(variants) != 0 {
		return nil, ErrMerchantStoreWriterFrozen
	}
	defaultID := MerchantStoreDefaultVariantID(p.ID)
	defaultSeen := false
	for i := range variants {
		variants[i].IsDefault = variants[i].ID == defaultID
		defaultSeen = defaultSeen || variants[i].IsDefault
	}
	if !defaultSeen {
		variants = append([]MerchantStoreVariant{storeVirtualDefaultVariant(p)}, variants...)
	}
	return variants, nil
}

// Called only while the product row is locked, so default creation and all
// variant mutations serialize with checkout and the shared product sale limit.
func storeEnsureDefaultVariant(tx *gorm.DB, p *MerchantStoreProduct) (*MerchantStoreVariant, error) {
	required, err := storeWriterGateRow(tx, "SHARE")
	if err != nil {
		return nil, err
	}
	if required == 1 {
		var count int64
		if err := tx.Model(&MerchantStoreVariant{}).Where("product_id = ?", p.ID).Count(&count).Error; err != nil {
			return nil, err
		}
		if count != 0 {
			return nil, ErrMerchantStoreWriterFrozen
		}
		variant := storeVirtualDefaultVariant(p)
		return &variant, nil // No new dual price authority during mixed writers.
	}
	var variant MerchantStoreVariant
	err = tx.Where("id = ? AND product_id = ?", MerchantStoreDefaultVariantID(p.ID), p.ID).First(&variant).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		variant = storeVirtualDefaultVariant(p)
		err = tx.Create(&variant).Error
	}
	return &variant, err
}

func storeVariant(tx *gorm.DB, p *MerchantStoreProduct, id string) (*MerchantStoreVariant, error) {
	if id == MerchantStoreDefaultVariantID(p.ID) {
		return storeEnsureDefaultVariant(tx, p)
	}
	if err := storeRequireVariantWriter(tx); err != nil {
		return nil, err
	}
	var variant MerchantStoreVariant
	err := tx.Where("id = ? AND product_id = ?", id, p.ID).First(&variant).Error
	return &variant, err
}

func storeVariantStock(q *gorm.DB, productID, variantID string) *gorm.DB {
	q = q.Where("product_id = ?", productID)
	if variantID == MerchantStoreDefaultVariantID(productID) {
		return q.Where("variant_id IS NULL OR variant_id = ? OR variant_id = ?", "", variantID)
	}
	return q.Where("variant_id = ?", variantID)
}

func storeSelectedVariant(tx *gorm.DB, p *MerchantStoreProduct, id string) (*MerchantStoreVariant, error) {
	if err := storeRequireVariantPublication(tx, p); err != nil {
		return nil, err
	}
	if id == "" {
		var other int64
		if err := tx.Model(&MerchantStoreVariant{}).Where("product_id = ? AND id <> ?", p.ID, MerchantStoreDefaultVariantID(p.ID)).Count(&other).Error; err != nil {
			return nil, err
		}
		if other != 0 {
			return nil, ErrMerchantStoreVariantRequired
		}
		id = MerchantStoreDefaultVariantID(p.ID)
	}
	variant, err := storeVariant(tx, p, id)
	if err != nil {
		return nil, err
	}
	if !variant.Enabled {
		return nil, ErrMerchantStoreUnavailable
	}
	if err = storeRequireMinimumUnitPrice(tx, variant.PriceQuota); err != nil {
		return nil, err
	}
	return variant, nil
}

func storeValidateVariant(in *MerchantStoreVariantInput) error {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 200 || in.PriceQuota <= 0 || !marketQuotaValid(in.PriceQuota) || !storeDeliveryTemplateSupported(in.Template) {
		return ErrMerchantStoreInput
	}
	return nil
}

func storeRetireVariantListing(tx *gorm.DB, p *MerchantStoreProduct) error {
	if p.Status == "pending" {
		return ErrMerchantStoreConflict
	}
	if p.AIReviewToken != "" {
		if err := invalidateMarketAIReview(tx, ModerationSourceMarketProduct, p.ID, p.AIReviewToken, "market_review_stale"); err != nil {
			return err
		}
	}
	p.AIReviewToken = ""
	priorStatus := p.Status
	p.Status, p.ReviewNote = "draft", ""
	if MerchantStoreProductVisibility(p) == "private" && (priorStatus == "paused" || priorStatus == "off_shelf") {
		p.Status = priorStatus
	}
	p.ReviewedBy, p.ReviewedAt = 0, 0
	p.UpdatedAt = common.GetTimestamp()
	return tx.Save(p).Error
}

func SaveMerchantStoreVariant(actor int, productID, id string, in MerchantStoreVariantInput) (*MerchantStoreVariant, error) {
	if err := storeValidateVariant(&in); err != nil {
		return nil, err
	}
	var result MerchantStoreVariant
	err := storeWithActiveProduct(productID, func(tx *gorm.DB, p *MerchantStoreProduct) error {
		if err := storeRequireVariantWriter(tx); err != nil {
			return err
		}
		if p.SellerID != actor {
			return ErrMerchantStoreDenied
		}
		if _, err := storeUser(tx, actor, common.RoleCommonUser); err != nil {
			return err
		}
		if p.Status == "pending" {
			return ErrMerchantStoreConflict
		}
		if err := storeRequireMinimumUnitPrice(tx, in.PriceQuota); err != nil {
			return err
		}
		if _, err := storeEnsureDefaultVariant(tx, p); err != nil {
			return err
		}
		now := common.GetTimestamp()
		if id == "" {
			var count int64
			if err := tx.Model(&MerchantStoreVariant{}).Where("product_id = ?", p.ID).Count(&count).Error; err != nil {
				return err
			}
			if count >= MerchantStoreMaximumVariants {
				return ErrMerchantStoreInput
			}
			result = MerchantStoreVariant{ID: uuid.NewString(), ProductID: p.ID, CreatedAt: now}
		} else {
			v, err := storeVariant(tx, p, id)
			if err != nil {
				return err
			}
			result = *v
		}
		result.Name, result.PriceQuota, result.Template, result.Enabled, result.UpdatedAt = in.Name, in.PriceQuota, in.Template, in.Enabled, now
		if err := storeSaveFixedContent(tx, p, &result, in.FixedContent); err != nil {
			return err
		}
		if err := tx.Save(&result).Error; err != nil {
			return err
		}
		if result.ID == MerchantStoreDefaultVariantID(p.ID) {
			p.PriceQuota, p.Template = result.PriceQuota, result.Template
		}
		if err := storeRetireVariantListing(tx, p); err != nil {
			return err
		}
		return storeEvent(tx, actor, result.ID, "variant_saved")
	})
	return &result, err
}

func SetMerchantStoreVariantEnabled(actor int, productID, id string, enabled bool) error {
	return storeWithActiveProduct(productID, func(tx *gorm.DB, p *MerchantStoreProduct) error {
		if err := storeRequireVariantWriter(tx); err != nil {
			return err
		}
		if p.SellerID != actor {
			return ErrMerchantStoreDenied
		}
		if _, err := storeUser(tx, actor, common.RoleCommonUser); err != nil {
			return err
		}
		v, err := storeVariant(tx, p, id)
		if err != nil {
			return err
		}
		if enabled {
			if err = storeRequireMinimumUnitPrice(tx, v.PriceQuota); err != nil {
				return err
			}
		}
		if err = tx.Model(v).Updates(map[string]any{"enabled": enabled, "updated_at": common.GetTimestamp()}).Error; err != nil {
			return err
		}
		return storeEvent(tx, actor, id, "variant_enabled_updated")
	})
}

func GetMerchantStoreVariant(actor int, productID, id string) (*MerchantStoreVariant, error) {
	p, err := GetMerchantStoreProduct(actor, productID)
	if err != nil {
		return nil, err
	}
	for _, v := range p.Variants {
		if v.ID == id {
			return &v, nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}

func storeValidateEnabledVariants(tx *gorm.DB, p *MerchantStoreProduct) error {
	if err := storeRequireVariantPublication(tx, p); err != nil {
		return err
	}
	variants, err := storeVariants(tx, p)
	if err != nil {
		return err
	}
	enabled := false
	for _, v := range variants {
		if v.Enabled {
			enabled = true
			if err := storeRequireMinimumUnitPrice(tx, v.PriceQuota); err != nil {
				return err
			}
			if v.Template == MerchantStoreFixedContentTemplate {
				if _, err := storeReadFixedContent(tx, p.ID, v.ID); err != nil {
					return err
				}
			}
		}
	}
	if !enabled {
		return ErrMerchantStoreUnavailable
	}
	return nil
}

func populateMerchantStoreVariants(tx *gorm.DB, p *MerchantStoreProduct, seller *User, config MerchantStoreConfig, methods []string, public bool) error {
	variants, err := storeVariants(tx, p)
	if err != nil {
		return err
	}
	remaining, err := storeSaleRemaining(p.SaleLimit, merchantStoreSalesUsage{Paid: p.PaidQuantity, Reserved: p.ReservedQuantity})
	if err != nil {
		return err
	}
	p.DefaultVariantID = MerchantStoreDefaultVariantID(p.ID)
	p.Variants = make([]MerchantStoreVariant, 0, len(variants))
	p.InventoryTotal, p.InventoryAvailable, p.PriceMinQuota, p.PriceMaxQuota = 0, 0, 0, 0
	p.TradingPaused, p.UnlimitedSupply = true, false
	fixedReady := map[string]bool{}
	if storeFixedContentSupported(tx) {
		var ids []string
		if err := tx.Model(&MerchantStoreFixedContent{}).Where("product_id = ? AND ciphertext <> ?", p.ID, "").Pluck("variant_id", &ids).Error; err != nil {
			return err
		}
		for _, id := range ids {
			fixedReady[id] = true
		}
	}
	// Aggregate in one query rather than two queries per spec on every public
	// catalog entry. Legacy NULL/empty associations remain logical defaults.
	var stockCounts []struct {
		VariantID *string
		State     string
		Count     int64
	}
	if err := tx.Model(&MerchantStoreStock{}).Select("variant_id,state,COUNT(*) AS count").Where("product_id = ? AND state IN ?", p.ID, []string{"available", "reserved"}).Group("variant_id,state").Scan(&stockCounts).Error; err != nil {
		return err
	}
	available, reserved := make(map[string]int64), make(map[string]int64)
	for _, count := range stockCounts {
		id := p.DefaultVariantID
		if count.VariantID != nil && *count.VariantID != "" {
			id = *count.VariantID
		}
		if count.State == "available" {
			available[id] += count.Count
		} else {
			reserved[id] += count.Count
		}
	}
	var eligible int64
	for i := range variants {
		v := &variants[i]
		v.UnlimitedSupply = v.Template == MerchantStoreFixedContentTemplate && fixedReady[v.ID]
		v.InventoryAvailable, v.ReservedStock = available[v.ID], reserved[v.ID]
		v.InventoryTotal = v.InventoryAvailable + v.ReservedStock
		p.InventoryTotal += v.InventoryTotal
		p.InventoryAvailable += v.InventoryAvailable
		fee := storeFee(v.PriceQuota, config.FeeBPS)
		if seller.Role == common.RoleRootUser {
			fee = 0
		}
		eligibleVariant := v.Enabled && v.PriceQuota >= config.MinimumUnitPriceQuota && seller.Quota >= fee && len(methods) != 0
		if eligibleVariant {
			eligible += v.InventoryAvailable
			v.SaleAvailable = min(v.InventoryAvailable, remaining)
			if v.UnlimitedSupply {
				p.UnlimitedSupply = true
				if p.SaleLimit != nil {
					v.SaleAvailable = remaining
				}
			}
		}
		v.TradingPaused = !eligibleVariant || !storeProductPurchaseStatus(p) || (v.SaleAvailable == 0 && !(v.UnlimitedSupply && p.SaleLimit == nil))
		if !v.TradingPaused {
			p.TradingPaused = false
		}
		if v.Enabled && v.PriceQuota >= config.MinimumUnitPriceQuota {
			if p.PriceMinQuota == 0 || v.PriceQuota < p.PriceMinQuota {
				p.PriceMinQuota = v.PriceQuota
			}
			p.PriceMaxQuota = max(p.PriceMaxQuota, v.PriceQuota)
		}
		if !public || v.Enabled {
			p.Variants = append(p.Variants, *v)
		}
	}
	p.SaleAvailable = min(eligible, remaining)
	if p.UnlimitedSupply && p.SaleLimit != nil {
		p.SaleAvailable = remaining
	}
	if public {
		p.AvailableStock = p.SaleAvailable
	}
	return nil
}

func storeOrderStock(tx *gorm.DB, o *MerchantStoreOrder) *gorm.DB {
	q := tx.Where("order_id = ? AND product_id = ?", o.ID, o.ProductID)
	if o.VariantID != "" {
		return storeVariantStock(q, o.ProductID, o.VariantID)
	}
	return q // History is resolved by its original order reservation, not today's product.
}
