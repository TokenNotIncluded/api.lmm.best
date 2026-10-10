package model

import (
	"errors"
	"math"
	"net/url"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

var ErrMerchantStoreDiscountUnavailable = errors.New("store promotion unavailable")
var ErrMerchantStoreDiscountLimit = errors.New("store promotion use limit reached")

// These bounds limit one HTTP/database operation, never the number of codes a
// merchant may own. Expiry and usage cleanup can be paged until it returns zero.
const merchantStoreDiscountPageSize = 100

type MerchantStoreDiscountCode struct {
	ID            string   `json:"id" gorm:"primaryKey;size:36"`
	ProductID     string   `json:"product_id" gorm:"size:36;not null;index;uniqueIndex:store_discount_product_code,priority:1"`
	SellerID      int      `json:"seller_id" gorm:"not null;index"`
	Code          string   `json:"code" gorm:"size:64;not null;uniqueIndex:store_discount_product_code,priority:2"`
	DiscountBPS   int      `json:"discount_bps" gorm:"not null"`
	VariantIDs    []string `json:"variant_ids" gorm:"serializer:json;type:text"`
	ExpiresAt     *int64   `json:"expires_at" gorm:"type:bigint;index"`
	MaxUses       *int64   `json:"max_uses" gorm:"type:bigint"`
	Status        string   `json:"status" gorm:"size:16;not null;index"`
	CreatedAt     int64    `json:"created_at"`
	UpdatedAt     int64    `json:"updated_at"`
	UsesCount     int64    `json:"uses_count" gorm:"-"`
	ReservedCount int64    `json:"reserved_count" gorm:"-"`
	SharePath     string   `json:"share_path" gorm:"-"`
}

// Save is a complete replacement of the editable configuration. A nil expiry
// or use limit means unlimited; an empty variant set means all product specs.
// The generated, case-sensitive code and its identity can never be edited.
type MerchantStoreDiscountCodeInput struct {
	DiscountBPS *int     `json:"discount_bps"`
	VariantIDs  []string `json:"variant_ids"`
	ExpiresAt   *int64   `json:"expires_at"`
	MaxUses     *int64   `json:"max_uses"`
	Status      string   `json:"status"`
}

type MerchantStoreDiscountQuote struct {
	SellerID           int      `json:"-"`
	ProductID          string   `json:"product_id"`
	VariantID          string   `json:"variant_id"`
	PromotionCode      string   `json:"promotion_code"`
	Quantity           int      `json:"quantity"`
	DiscountBPS        int      `json:"discount_bps"`
	OriginalPriceQuota int      `json:"original_price_quota"`
	DiscountQuota      int      `json:"discount_quota"`
	PriceQuota         int      `json:"price_quota"`
	Free               bool     `json:"free"`
	CheckoutAllowed    bool     `json:"checkout_allowed"`
	MaxQuantity        int      `json:"max_quantity"`
	PaymentMethods     []string `json:"payment_methods"`
}

type merchantStoreDiscountUsage struct {
	Used     int64
	Reserved int64
}

// Every order consumes one use, irrespective of its item quantity. PaidAt is
// lifetime evidence, so refunds and later status changes never reset uses.
// Issued payment sessions, reserved stock and verified payment issues retain
// unpaid obligations. A local TTL or a changed status cannot release them.
func storeDiscountCodeUsage(tx *gorm.DB, productID string, ids []string) (map[string]merchantStoreDiscountUsage, error) {
	usage := make(map[string]merchantStoreDiscountUsage, len(ids))
	if len(ids) == 0 {
		return usage, nil
	}
	type row struct {
		PromotionID string
		Count       int64
	}
	var paid []row
	if err := tx.Model(&MerchantStoreOrder{}).
		Where("product_id = ? AND promotion_id IN ? AND paid_at > 0", productID, ids).
		Select("promotion_id, COUNT(*) AS count").Group("promotion_id").Scan(&paid).Error; err != nil {
		return nil, err
	}
	for _, value := range paid {
		usage[value.PromotionID] = merchantStoreDiscountUsage{Used: value.Count}
	}
	stockOrders := tx.Model(&MerchantStoreStock{}).Select("order_id").
		Where("product_id = ? AND state = ?", productID, "reserved")
	var reserved []row
	if err := tx.Model(&MerchantStoreOrder{}).
		Where("product_id = ? AND promotion_id IN ? AND COALESCE(paid_at,0) <= 0", productID, ids).
		Where("COALESCE(status,'') NOT IN ? OR id IN (?) OR COALESCE(verified_payment_issue_at,0) > 0 OR ((COALESCE(gateway_snapshot,'') <> '' OR COALESCE(provider_session_id,'') <> '') AND COALESCE(provider_closure_reference,'') = '')", []string{"cancelled", "expired"}, stockOrders).
		Select("promotion_id, COUNT(*) AS count").Group("promotion_id").Scan(&reserved).Error; err != nil {
		return nil, err
	}
	for _, value := range reserved {
		current := usage[value.PromotionID]
		current.Reserved = value.Count
		usage[value.PromotionID] = current
	}
	for _, value := range usage {
		if value.Used < 0 || value.Reserved < 0 || value.Used > math.MaxInt64-value.Reserved {
			return nil, ErrMerchantStoreConflict
		}
	}
	return usage, nil
}

func storePopulateDiscountCodes(tx *gorm.DB, productID string, codes []MerchantStoreDiscountCode) error {
	ids := make([]string, len(codes))
	for i := range codes {
		ids[i] = codes[i].ID
	}
	usage, err := storeDiscountCodeUsage(tx, productID, ids)
	if err != nil {
		return err
	}
	for i := range codes {
		codes[i].UsesCount, codes[i].ReservedCount = usage[codes[i].ID].Used, usage[codes[i].ID].Reserved
		codes[i].SharePath = "/store/products/" + url.PathEscape(productID) + "?promotion=" + url.QueryEscape(codes[i].Code)
	}
	return nil
}

func storeDiscountCodeActor(tx *gorm.DB, p *MerchantStoreProduct, actor int) error {
	user, err := storeUser(tx, actor, common.RoleCommonUser)
	if err != nil {
		return err
	}
	if p.SellerID != actor && user.Role != common.RoleRootUser {
		return ErrMerchantStoreDenied
	}
	return nil
}

func storeDiscountCodeStatus(status string) bool {
	return status == "active" || status == "paused" || status == "revoked"
}

func storeValidateDiscountCode(tx *gorm.DB, p *MerchantStoreProduct, in *MerchantStoreDiscountCodeInput) error {
	if in.DiscountBPS == nil || *in.DiscountBPS < 0 || *in.DiscountBPS > 10000 ||
		(in.ExpiresAt != nil && *in.ExpiresAt <= 0) || (in.MaxUses != nil && *in.MaxUses < 0) {
		return ErrMerchantStoreInput
	}
	if in.Status == "" {
		in.Status = "active"
	}
	if !storeDiscountCodeStatus(in.Status) {
		return ErrMerchantStoreInput
	}
	if len(in.VariantIDs) == 0 {
		return nil
	}
	variants, err := storeVariants(tx, p)
	if err != nil {
		return err
	}
	known := make(map[string]bool, len(variants))
	for _, variant := range variants {
		known[variant.ID] = true
	}
	seen := make(map[string]bool, len(in.VariantIDs))
	for _, id := range in.VariantIDs {
		if !known[id] || seen[id] {
			return ErrMerchantStoreInput
		}
		seen[id] = true
	}
	return nil
}

func storeDiscountCodeLimit(limit int) int {
	if limit <= 0 {
		return 30
	}
	return min(limit, merchantStoreDiscountPageSize)
}

// Coupon checkout must never mix with writers that cannot freeze its price or
// usage. Unlike the compatibility writer guard, this feature requires the
// operator's capability-4 floor to be active in the same business transaction.
func storeRequireDiscountCodeWriter(tx *gorm.DB) error {
	required, err := storeWriterGateRow(tx, "SHARE")
	if err != nil || required < 4 || required > MerchantStoreWriterCapability {
		return ErrMerchantStoreWriterFrozen
	}
	return nil
}

func ListMerchantStoreDiscountCodes(actor int, productID string, offset, limit int) ([]MerchantStoreDiscountCode, int64, error) {
	if offset < 0 {
		return nil, 0, ErrMerchantStoreInput
	}
	rows := make([]MerchantStoreDiscountCode, 0)
	var total int64
	err := storeWithProduct(productID, func(tx *gorm.DB, p *MerchantStoreProduct) error {
		if err := storeDiscountCodeActor(tx, p, actor); err != nil {
			return err
		}
		q := tx.Model(&MerchantStoreDiscountCode{}).Where("product_id = ? AND seller_id = ?", p.ID, p.SellerID)
		if err := q.Count(&total).Error; err != nil {
			return err
		}
		if err := q.Order("created_at DESC,id DESC").Offset(offset).Limit(storeDiscountCodeLimit(limit)).Find(&rows).Error; err != nil {
			return err
		}
		return storePopulateDiscountCodes(tx, p.ID, rows)
	})
	if err != nil {
		return nil, 0, err
	}
	return rows, total, err
}

func SaveMerchantStoreDiscountCode(actor int, productID, id string, in MerchantStoreDiscountCodeInput) (*MerchantStoreDiscountCode, error) {
	var result MerchantStoreDiscountCode
	err := storeWithActiveProduct(productID, func(tx *gorm.DB, p *MerchantStoreProduct) error {
		if err := storeDiscountCodeActor(tx, p, actor); err != nil {
			return err
		}
		if err := storeRequireDiscountCodeWriter(tx); err != nil {
			return err
		}
		if err := storeValidateDiscountCode(tx, p, &in); err != nil {
			return err
		}
		now := common.GetTimestamp()
		if id == "" {
			code, err := storeToken()
			if err != nil {
				return err
			}
			result = MerchantStoreDiscountCode{ID: uuid.NewString(), ProductID: p.ID, SellerID: p.SellerID, Code: code, CreatedAt: now}
		} else {
			if err := lockForUpdate(tx).Where("id = ? AND product_id = ? AND seller_id = ?", id, p.ID, p.SellerID).First(&result).Error; err != nil {
				return err
			}
			if result.Status == "revoked" && in.Status != "revoked" {
				return ErrMerchantStoreConflict
			}
		}
		result.DiscountBPS, result.VariantIDs = *in.DiscountBPS, append([]string(nil), in.VariantIDs...)
		result.ExpiresAt, result.MaxUses, result.Status, result.UpdatedAt = in.ExpiresAt, in.MaxUses, in.Status, now
		if err := tx.Save(&result).Error; err != nil {
			return err
		}
		if err := storeEvent(tx, actor, result.ID, "promotion_saved"); err != nil {
			return err
		}
		rows := []MerchantStoreDiscountCode{result}
		if err := storePopulateDiscountCodes(tx, p.ID, rows); err != nil {
			return err
		}
		result = rows[0]
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func BatchMerchantStoreDiscountCodes(actor int, productID string, ids []string, action string) (int64, error) {
	if len(ids) == 0 || len(ids) > merchantStoreDiscountPageSize {
		return 0, ErrMerchantStoreInput
	}
	status := map[string]string{"pause": "paused", "resume": "active", "revoke": "revoked"}[action]
	if action != "delete" && status == "" {
		return 0, ErrMerchantStoreInput
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id == "" || seen[id] {
			return 0, ErrMerchantStoreInput
		}
		seen[id] = true
	}
	var affected int64
	err := storeWithProduct(productID, func(tx *gorm.DB, p *MerchantStoreProduct) error {
		if err := storeDiscountCodeActor(tx, p, actor); err != nil {
			return err
		}
		if p.Status == "deleted" && action != "delete" {
			return gorm.ErrRecordNotFound
		}
		if err := storeRequireDiscountCodeWriter(tx); err != nil {
			return err
		}
		var rows []MerchantStoreDiscountCode
		q := tx.Where("product_id = ? AND seller_id = ? AND id IN ?", p.ID, p.SellerID, ids)
		if err := lockForUpdate(q).Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) != len(ids) {
			return gorm.ErrRecordNotFound
		}
		if action == "resume" || action == "pause" {
			for _, row := range rows {
				if row.Status == "revoked" {
					return ErrMerchantStoreConflict
				}
			}
		}
		var result *gorm.DB
		if action == "delete" {
			// No associations/cascades: orders retain their frozen promotion
			// identity, code, percentage and credit amounts after hard deletion.
			result = q.Delete(&MerchantStoreDiscountCode{})
		} else {
			result = q.Model(&MerchantStoreDiscountCode{}).Updates(map[string]any{"status": status, "updated_at": common.GetTimestamp()})
		}
		if result.Error != nil {
			return result.Error
		}
		affected = result.RowsAffected
		return storeEvent(tx, actor, p.ID, "promotions_"+action)
	})
	if err != nil {
		return 0, err
	}
	return affected, err
}

func CleanupMerchantStoreDiscountCodes(actor int, productID string, limit int) (int64, error) {
	var deleted int64
	err := storeWithProduct(productID, func(tx *gorm.DB, p *MerchantStoreProduct) error {
		if err := storeDiscountCodeActor(tx, p, actor); err != nil {
			return err
		}
		if err := storeRequireDiscountCodeWriter(tx); err != nil {
			return err
		}
		now := common.GetTimestamp()
		// Filter in SQL before LIMIT, so many active rows cannot conceal later
		// expired/exhausted/revoked codes. The count is permanent paid history, not a
		// temporary reservation; temporarily busy codes remain resumable.
		paidOrders := tx.Model(&MerchantStoreOrder{}).Select("COUNT(*)").
			Where("product_id = ? AND paid_at > 0 AND promotion_id = merchant_store_discount_codes.id", p.ID)
		var ids []string
		if err := tx.Model(&MerchantStoreDiscountCode{}).Where("product_id = ? AND seller_id = ?", p.ID, p.SellerID).
			Where("status = ? OR (expires_at IS NOT NULL AND expires_at <= ?) OR (max_uses IS NOT NULL AND max_uses <= (?))", "revoked", now, paidOrders).
			Order("created_at ASC,id ASC").Limit(storeDiscountCodeLimit(limit)).Pluck("id", &ids).Error; err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		result := tx.Where("product_id = ? AND seller_id = ? AND id IN ?", p.ID, p.SellerID, ids).Delete(&MerchantStoreDiscountCode{})
		if result.Error != nil {
			return result.Error
		}
		deleted = result.RowsAffected
		return storeEvent(tx, actor, p.ID, "promotions_cleaned")
	})
	if err != nil {
		return 0, err
	}
	return deleted, err
}

func storeDiscountVisible(tx *gorm.DB, p *MerchantStoreProduct, actor int) error {
	var count int64
	if err := MerchantStoreVisibleProductsForViewer(tx, actor).Where("merchant_store_products.id = ?", p.ID).Count(&count).Error; err != nil {
		return err
	}
	if count != 1 {
		return ErrMerchantStoreDiscountUnavailable
	}
	return nil
}

func storeResolveDiscountCode(tx *gorm.DB, p *MerchantStoreProduct, code string) (*MerchantStoreDiscountCode, error) {
	code = strings.TrimSpace(code)
	if len(code) != 43 {
		return nil, ErrMerchantStoreDiscountUnavailable
	}
	var row MerchantStoreDiscountCode
	if err := tx.Where("product_id = ? AND seller_id = ? AND code = ?", p.ID, p.SellerID, code).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrMerchantStoreDiscountUnavailable
		}
		return nil, err
	}
	// SQL collations may be case insensitive. Never accept a different spelling
	// of the bearer code, even if the database matched that row.
	if row.Code != code || row.Status != "active" || row.DiscountBPS < 0 || row.DiscountBPS > 10000 ||
		(row.ExpiresAt != nil && *row.ExpiresAt <= common.GetTimestamp()) || (row.MaxUses != nil && *row.MaxUses < 0) {
		return nil, ErrMerchantStoreDiscountUnavailable
	}
	rows := []MerchantStoreDiscountCode{row}
	if err := storePopulateDiscountCodes(tx, p.ID, rows); err != nil {
		return nil, err
	}
	row = rows[0]
	if row.MaxUses != nil && row.UsesCount+row.ReservedCount >= *row.MaxUses {
		return nil, ErrMerchantStoreDiscountLimit
	}
	return &row, nil
}

func ResolveMerchantStoreDiscountCode(actor int, productID, code string) (*MerchantStoreDiscountCode, error) {
	var result *MerchantStoreDiscountCode
	err := storeWithActiveProduct(productID, func(tx *gorm.DB, p *MerchantStoreProduct) error {
		if err := storeDiscountVisible(tx, p, actor); err != nil {
			return err
		}
		var err error
		result, err = storeResolveDiscountCode(tx, p, code)
		return err
	})
	return result, err
}

func storeDiscountNetQuota(originalQuota, bps int) (int, error) {
	if originalQuota <= 0 || !marketQuotaValid(originalQuota) || bps < 0 || bps > 10000 {
		return 0, ErrMerchantStoreInput
	}
	remaining := 10000 - bps
	// The quotient product is at most originalQuota. The remainder product is
	// at most 9999*10000, so neither intermediate multiplies a large balance.
	net := (originalQuota/10000)*remaining + ((originalQuota%10000)*remaining+9999)/10000
	if bps != 10000 && net <= 0 {
		return 0, ErrMerchantStoreConflict
	}
	return net, nil
}

// Caller must hold this exact product row's checkout lock, and must create the
// frozen order snapshot in the same transaction before releasing that lock.
// Checking a coupon never consumes a use or changes a pending/paid order.
func storeDiscountCodeTx(tx *gorm.DB, p *MerchantStoreProduct, code, variantID string, quantity, originalQuota int) (*MerchantStoreDiscountCode, int, error) {
	if strings.TrimSpace(code) == "" {
		return nil, originalQuota, nil
	}
	if err := storeRequireDiscountCodeWriter(tx); err != nil {
		return nil, 0, err
	}
	if quantity < 1 || quantity > 1000 || p == nil || variantID == "" {
		return nil, 0, ErrMerchantStoreInput
	}
	row, err := storeResolveDiscountCode(tx, p, code)
	if err != nil {
		return nil, 0, err
	}
	if len(row.VariantIDs) > 0 {
		found := false
		for _, id := range row.VariantIDs {
			found = found || id == variantID
		}
		if !found {
			return nil, 0, ErrMerchantStoreDiscountUnavailable
		}
	}
	price, err := storeDiscountNetQuota(originalQuota, row.DiscountBPS)
	return row, price, err
}

func QuoteMerchantStoreDiscountCode(actor int, productID, code, variantID string, quantity int) (*MerchantStoreDiscountQuote, error) {
	return QuoteMerchantStoreDiscountCodeForViewer(actor, "", productID, code, variantID, quantity)
}

func QuoteMerchantStoreDiscountCodeForViewer(actor int, guestToken, productID, code, variantID string, quantity int) (*MerchantStoreDiscountQuote, error) {
	if quantity < 1 || quantity > 1000 {
		return nil, ErrMerchantStoreInput
	}
	var result MerchantStoreDiscountQuote
	err := storeWithActiveProduct(productID, func(tx *gorm.DB, p *MerchantStoreProduct) error {
		if err := storeDiscountVisible(tx, p, actor); err != nil {
			return err
		}
		if !storeProductPurchaseStatus(p) {
			return ErrMerchantStoreUnavailable
		}
		guestID := ""
		if actor > 0 || guestToken != "" {
			var err error
			guestID, err = CheckMerchantStoreProductPurchaseAccess(tx, p, actor, guestToken)
			if err != nil {
				return err
			}
			if err := storeRequireConfiguredSellerTerms(tx, p.SellerID); err != nil {
				return err
			}
		}
		// Match config mutation's gate -> config lock order. Holding config
		// first and then queuing behind activation's exclusive gate could
		// deadlock with a config writer that already holds the shared gate.
		if strings.TrimSpace(code) != "" {
			if err := storeRequireDiscountCodeWriter(tx); err != nil {
				return err
			}
		} else if (actor > 0 || guestID != "") && (p.MaxQuantityPerOrder != nil || p.MaxQuantityPerBuyer != nil) {
			if err := storeRequirePurchaseLimitWriter(tx); err != nil {
				return err
			}
		}
		seller, err := storeUser(tx, p.SellerID, common.RoleCommonUser)
		if err != nil {
			return err
		}
		if actor > 0 {
			if _, err := storeUser(tx, actor, common.RoleCommonUser); err != nil {
				return err
			}
		}
		// Unlike checkout's storeSelectedVariant, this read must not materialize
		// a legacy virtual default variant or write any other durable state.
		variants, err := storeVariants(tx, p)
		if err != nil {
			return err
		}
		if variantID == "" {
			if len(variants) != 1 {
				return ErrMerchantStoreVariantRequired
			}
			variantID = variants[0].ID
		}
		var selected *MerchantStoreVariant
		for i := range variants {
			if variants[i].ID == variantID {
				selected = &variants[i]
				break
			}
		}
		if selected == nil || !selected.Enabled {
			return ErrMerchantStoreUnavailable
		}
		if selected.PriceQuota <= 0 || !marketQuotaValid(selected.PriceQuota) {
			return ErrMerchantStoreInput
		}
		if err := storeRequireMinimumUnitPrice(tx, selected.PriceQuota); err != nil {
			return err
		}
		if selected.PriceQuota > common.MaxWalletQuota/quantity {
			return ErrMerchantStoreInput
		}
		original := selected.PriceQuota * quantity
		promotion, price, err := storeDiscountCodeTx(tx, p, code, selected.ID, quantity, original)
		if err != nil {
			return err
		}
		if err := storeCheckSaleLimit(tx, p, quantity); err != nil {
			return err
		}
		var stock int64
		if err := storeVariantStock(tx.Model(&MerchantStoreStock{}), p.ID, selected.ID).Where("state = ?", "available").Count(&stock).Error; err != nil {
			return err
		}
		if stock < int64(quantity) {
			return ErrMerchantStoreStock
		}
		result = MerchantStoreDiscountQuote{SellerID: p.SellerID, ProductID: p.ID, VariantID: selected.ID, Quantity: quantity, OriginalPriceQuota: original, PriceQuota: price, DiscountQuota: original - price, Free: price == 0, PaymentMethods: append([]string(nil), p.PaymentMethods...)}
		if promotion != nil {
			result.PromotionCode, result.DiscountBPS = promotion.Code, promotion.DiscountBPS
		}
		if result.Free {
			result.PaymentMethods = []string{"free"}
		} else {
			// Intersect the merchant's category, enabled channels and listing.
			// The controller additionally checks provider configuration.
			if err := populateMerchantStoreProduct(tx, p, true); err != nil {
				return err
			}
			result.PaymentMethods = p.PaymentMethods
		}
		if guestID != "" && !result.Free {
			methods := make([]string, 0, len(result.PaymentMethods))
			for _, method := range result.PaymentMethods {
				if method != "balance" {
					methods = append(methods, method)
				}
			}
			result.PaymentMethods = methods
		}
		usage, err := storeSalesUsage(tx, p.ID)
		if err != nil {
			return err
		}
		remaining, err := storeSaleRemaining(p.SaleLimit, usage)
		if err != nil {
			return err
		}
		maximum := min(stock, remaining, int64(1000), int64(common.MaxWalletQuota/selected.PriceQuota))
		if !storePurchaseLimitValid(p.MaxQuantityPerOrder) || !storePurchaseLimitValid(p.MaxQuantityPerBuyer) {
			return ErrMerchantStoreInput
		}
		if p.MaxQuantityPerOrder != nil {
			maximum = min(maximum, *p.MaxQuantityPerOrder)
		}
		if actor > 0 || guestID != "" {
			if err := storeCheckPurchaseLimitsForSubject(tx, p, actor, guestID, quantity); err != nil {
				return err
			}
			if p.MaxQuantityPerBuyer != nil {
				used, err := storeBuyerPurchaseUsageForSubject(tx, p.ID, actor, guestID)
				if err != nil {
					return err
				}
				buyerRemaining := max(0, *p.MaxQuantityPerBuyer-used)
				maximum = min(maximum, buyerRemaining)
			}
		}
		if !result.Free {
			balance := false
			for _, method := range result.PaymentMethods {
				balance = balance || method == "balance"
			}
			if !balance {
				maximum = min(maximum, int64(100))
			}
			if len(result.PaymentMethods) == 0 {
				maximum = 0
			}
		}
		// Checkout charges the seller on the discounted total, rather than on
		// an original unit-price aggregate. Bound this offer using that same
		// integer fee, including quantity rounding, without reserving funds.
		config, err := storeConfig(tx)
		if err != nil {
			return err
		}
		feeBPS := config.FeeBPS
		if seller.Role == common.RoleRootUser {
			feeBPS = 0
		}
		if seller.Quota < storeFee(price, feeBPS) {
			return ErrMerchantStoreBalance
		}
		if !result.Free && feeBPS > 0 {
			low, high := int64(0), maximum
			for low < high {
				mid := low + (high-low+1)/2
				net, err := storeDiscountNetQuota(selected.PriceQuota*int(mid), result.DiscountBPS)
				if err != nil {
					return err
				}
				if storeFee(net, feeBPS) <= seller.Quota {
					low = mid
				} else {
					high = mid - 1
				}
			}
			maximum = low
		}
		if int64(quantity) > maximum {
			return ErrMerchantStoreUnavailable
		}
		// Use physical stock, quantity obligations and this offer's net fee;
		// original-price aggregates may report zero for discounted offers.
		result.MaxQuantity = int(maximum)
		result.CheckoutAllowed = actor > 0 || guestID != ""
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}
