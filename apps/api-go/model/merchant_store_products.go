package model

import (
	"errors"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func SaveMerchantStoreProduct(actor int, id string, in MerchantStoreProductInput) (*MerchantStoreProduct, error) {
	if e := validateStoreProduct(&in); e != nil {
		return nil, e
	}
	var p MerchantStoreProduct
	e := marketTransaction(DB, func(tx *gorm.DB) error {
		seller, e := storeUser(tx, actor, common.RoleCommonUser)
		if e != nil {
			return e
		}
		now := common.GetTimestamp()
		if id == "" {
			p = MerchantStoreProduct{ID: uuid.NewString(), SellerID: actor, CreatedAt: now}
		} else {
			v, e := storeProductOwner(tx, actor, id)
			if e != nil {
				return e
			}
			p = *v
			if p.Status == "pending" {
				return ErrMerchantStoreConflict
			}
		}
		if e := storeRequireMinimumUnitPrice(tx, in.PriceQuota); e != nil {
			return e
		}
		if e := storeValidatePaymentSelection(tx, seller, in.PaymentMethods); e != nil {
			return e
		}
		p.Title = in.Title
		p.Description = in.Description
		p.ImageURLs = in.ImageURLs
		p.Contact = in.Contact
		p.Links = in.Links
		p.PriceQuota = in.PriceQuota
		p.Template = in.Template
		p.DeliveryStrategy = in.DeliveryStrategy
		p.PaymentMethods = in.PaymentMethods
		p.PickupLoginRequired = in.PickupLoginRequired
		p.PickupCodeRequired = in.PickupCodeRequired
		p.EmailPickupLink = in.EmailPickupLink
		// Every content, price, payment or delivery edit retires the approved listing.
		if p.AIReviewToken != "" {
			if err := invalidateMarketAIReview(tx, ModerationSourceMarketProduct, p.ID, p.AIReviewToken, "market_review_stale"); err != nil {
				return err
			}
		}
		p.AIReviewToken = ""
		p.Status = "draft"
		p.ReviewNote = ""
		p.ReviewedBy = 0
		p.ReviewedAt = 0
		p.UpdatedAt = now
		if e := tx.Save(&p).Error; e != nil {
			return e
		}
		return storeEvent(tx, actor, p.ID, "save_draft")
	})
	return &p, e
}
func SubmitMerchantStoreProduct(actor int, id string) error {
	return storeWithProduct(id, func(tx *gorm.DB, p *MerchantStoreProduct) error {
		if p.SellerID != actor {
			return ErrMerchantStoreDenied
		}
		if _, e := storeUser(tx, actor, common.RoleCommonUser); e != nil {
			return e
		}
		if e := storeRequireMinimumUnitPrice(tx, p.PriceQuota); e != nil {
			return e
		}
		if p.Status == "pending" {
			return nil
		}
		if p.Status != "draft" && p.Status != "rejected" {
			return ErrMerchantStoreConflict
		}
		if err := invalidateMarketAIReview(tx, ModerationSourceMarketProduct, p.ID, p.AIReviewToken, "market_review_resubmitted"); err != nil {
			return err
		}
		p.AIReviewToken = uuid.NewString()
		p.Status = "pending"
		p.UpdatedAt = common.GetTimestamp()
		if e := tx.Save(p).Error; e != nil {
			return e
		}
		if err := queueMarketAIReview(tx, ModerationSourceMarketProduct, p.ID, "", p.AIReviewToken, actor, false); err != nil {
			return err
		}
		return storeEvent(tx, actor, p.ID, "submit")
	})
}
func ReviewMerchantStoreProduct(actor int, id string, approve bool, note string) error {
	if len(note) > 4096 {
		return ErrMerchantStoreInput
	}
	return storeWithProduct(id, func(tx *gorm.DB, p *MerchantStoreProduct) error {
		if _, e := storeUser(tx, actor, common.RoleAdminUser); e != nil {
			return e
		}
		if p.Status != "pending" && !marketAIReviewApplied(tx, ModerationSourceMarketProduct, p.ID, p.AIReviewToken) {
			return ErrMerchantStoreConflict
		}
		if _, e := storeUser(tx, p.SellerID, common.RoleCommonUser); e != nil {
			return e
		}
		if err := invalidateMarketAIReview(tx, ModerationSourceMarketProduct, p.ID, p.AIReviewToken, "market_review_manual_override"); err != nil {
			return err
		}
		p.AIReviewToken = ""
		p.Status = "rejected"
		if approve {
			p.Status = "published"
		}
		p.ReviewNote = note
		p.ReviewedBy = actor
		p.ReviewedAt = common.GetTimestamp()
		p.UpdatedAt = p.ReviewedAt
		if e := tx.Save(p).Error; e != nil {
			return e
		}
		return storeEvent(tx, actor, p.ID, "review_"+p.Status)
	})
}
func SetMerchantStoreProductPaused(actor int, id string, paused bool) error {
	return storeWithProduct(id, func(tx *gorm.DB, p *MerchantStoreProduct) error {
		u, e := storeUser(tx, actor, common.RoleCommonUser)
		if e != nil {
			return e
		}
		if p.SellerID != actor && u.Role < common.RoleAdminUser {
			return ErrMerchantStoreDenied
		}
		if paused {
			if p.Status != "published" && p.Status != "paused" {
				return ErrMerchantStoreConflict
			}
			p.Status = "paused"
		} else {
			if p.Status != "paused" || p.ReviewedAt == 0 {
				return ErrMerchantStoreConflict
			}
			p.Status = "published"
		}
		p.UpdatedAt = common.GetTimestamp()
		if e := tx.Save(p).Error; e != nil {
			return e
		}
		return storeEvent(tx, actor, p.ID, p.Status)
	})
}
func AddMerchantStoreStock(actor int, id string, items []string) (int, error) {
	if len(items) == 0 || len(items) > 10000 {
		return 0, ErrMerchantStoreInput
	}
	rows := make([]MerchantStoreStock, 0, len(items))
	for _, item := range items {
		if len(strings.TrimSpace(item)) == 0 || len(item) > 32768 {
			return 0, ErrMerchantStoreInput
		}
		row := MerchantStoreStock{ID: uuid.NewString(), ProductID: id, State: "available", CreatedAt: common.GetTimestamp()}
		cipher, e := storeEncrypt("stock", id+":"+row.ID, item)
		if e != nil {
			return 0, e
		}
		row.Ciphertext = cipher
		rows = append(rows, row)
	}
	e := storeWithProduct(id, func(tx *gorm.DB, p *MerchantStoreProduct) error {
		if p.SellerID != actor {
			return ErrMerchantStoreDenied
		}
		if _, e := storeUser(tx, actor, common.RoleCommonUser); e != nil {
			return e
		}
		var last int64
		if e := tx.Model(&MerchantStoreStock{}).Where("product_id = ?", p.ID).Select("COALESCE(MAX(position),0)").Scan(&last).Error; e != nil {
			return e
		}
		if last < 0 || last > int64(common.MaxWalletQuota)-int64(len(rows)) {
			return ErrMerchantStoreInput
		}
		for i := range rows {
			rows[i].Position = last + int64(i) + 1
		}
		return tx.CreateInBatches(rows, 100).Error
	})
	if e != nil {
		return 0, e
	}
	return len(rows), nil
}
func RemoveMerchantStoreStock(actor int, productID, stockID string) error {
	return storeWithProduct(productID, func(tx *gorm.DB, p *MerchantStoreProduct) error {
		if p.SellerID != actor {
			return ErrMerchantStoreDenied
		}
		if _, e := storeUser(tx, actor, common.RoleCommonUser); e != nil {
			return e
		}
		r := tx.Where("id = ? AND product_id = ? AND state = ?", stockID, productID, "available").Delete(&MerchantStoreStock{})
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != 1 {
			return ErrMerchantStoreConflict
		}
		return nil
	})
}
func ListMerchantStoreStock(actor int, productID string, offset, limit int) ([]MerchantStoreStock, error) {
	var p MerchantStoreProduct
	if e := DB.First(&p, "id = ?", productID).Error; e != nil {
		return nil, e
	}
	if p.SellerID != actor {
		return nil, ErrMerchantStoreDenied
	}
	if _, e := storeUser(DB, actor, common.RoleCommonUser); e != nil {
		return nil, e
	}
	offset, limit = storePage(offset, limit)
	var rows []MerchantStoreStock
	e := DB.Select("id,product_id,state,position,created_at").Where("product_id = ?", productID).Order("position ASC,id ASC").Offset(offset).Limit(limit).Find(&rows).Error
	return rows, e
}
func storePage(offset, limit int) (int, int) {
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	return offset, limit
}
func populateMerchantStoreProduct(tx *gorm.DB, p *MerchantStoreProduct, public bool) error {
	u, e := storeUser(tx, p.SellerID, common.RoleCommonUser)
	if e != nil {
		return e
	}
	p.Official = u.Role >= common.RoleAdminUser
	if e = tx.Model(&MerchantStoreStock{}).Where("product_id = ? AND state = ?", p.ID, "available").Count(&p.AvailableStock).Error; e != nil {
		return e
	}
	c, e := storeConfig(tx)
	if e != nil {
		return e
	}
	fee := storeFee(p.PriceQuota, c.FeeBPS)
	if u.Role == common.RoleRootUser {
		fee = 0
	}
	enabled := make([]string, 0, len(p.PaymentMethods))
	categories, e := storePaymentCategories(tx, p.SellerID)
	if e != nil {
		return e
	}
	for _, method := range p.PaymentMethods {
		if !categories.Enabled(method) {
			continue
		}
		var count int64
		if e = tx.Model(&MerchantStoreGateway{}).Where("seller_id = ? AND provider = ? AND enabled = ?", p.SellerID, method, true).Count(&count).Error; e != nil {
			return e
		}
		if count == 1 && (!strings.HasPrefix(method, "external:") || u.Quota > MerchantStoreExternalMinimumQuota) {
			enabled = append(enabled, method)
		}
	}
	// Public views describe currently usable methods; seller drafts preserve settings.
	if public {
		p.PaymentMethods = enabled
	}
	p.TradingPaused = u.Quota < fee || p.AvailableStock == 0 || len(enabled) == 0 || p.PriceQuota < c.MinimumUnitPriceQuota
	return nil
}
func GetPublicMerchantStoreProduct(id string) (*MerchantStoreProduct, error) {
	var p MerchantStoreProduct
	if e := DB.Where("id = ? AND status = ?", id, "published").First(&p).Error; e != nil {
		return nil, e
	}
	if e := populateMerchantStoreProduct(DB, &p, true); e != nil {
		return nil, e
	}
	p.ReviewNote = ""
	p.ReviewedBy = 0
	return &p, nil
}
func GetMerchantStoreProduct(actor int, id string) (*MerchantStoreProduct, error) {
	var p MerchantStoreProduct
	if e := DB.First(&p, "id = ?", id).Error; e != nil {
		return nil, e
	}
	u, e := storeUser(DB, actor, common.RoleCommonUser)
	if e != nil {
		return nil, e
	}
	if p.SellerID != actor && u.Role < common.RoleAdminUser {
		return nil, ErrMerchantStoreDenied
	}
	e = populateMerchantStoreProduct(DB, &p, false)
	return &p, e
}
func ListPublicMerchantStoreProducts(search string, offset, limit int) ([]MerchantStoreProduct, error) {
	offset, limit = storePage(offset, limit)
	if len(search) > 200 {
		return nil, ErrMerchantStoreInput
	}
	q := DB.Where("status = ?", "published")
	if search != "" {
		literal := strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(search)
		q = q.Where("title LIKE ? ESCAPE '!' OR description LIKE ? ESCAPE '!'", "%"+literal+"%", "%"+literal+"%")
	}
	var rows []MerchantStoreProduct
	// Active purchased promotion first, then its expiration, then newest listings.
	e := q.Clauses(clause.OrderBy{Expression: gorm.Expr("CASE WHEN promotion_expires_at > ? THEN 0 ELSE 1 END, CASE WHEN promotion_expires_at > ? THEN promotion_expires_at ELSE 0 END DESC,created_at DESC,id ASC", common.GetTimestamp(), common.GetTimestamp())}).Offset(offset).Limit(limit).Find(&rows).Error
	if e != nil {
		return nil, e
	}
	out := make([]MerchantStoreProduct, 0, len(rows))
	for _, p := range rows {
		if e = populateMerchantStoreProduct(DB, &p, true); errors.Is(e, ErrMerchantStoreDenied) {
			continue
		} else if e != nil {
			return nil, e
		}
		p.ReviewNote = ""
		p.ReviewedBy = 0
		out = append(out, p)
	}
	return out, nil
}
func ListMerchantStoreProducts(actor int, review bool, offset, limit int) ([]MerchantStoreProduct, error) {
	min := common.RoleCommonUser
	if review {
		min = common.RoleAdminUser
	}
	if _, e := storeUser(DB, actor, min); e != nil {
		return nil, e
	}
	offset, limit = storePage(offset, limit)
	q := DB
	if review {
		q = q.Where("status = ? OR (status IN ? AND ai_review_token <> '' AND EXISTS (SELECT 1 FROM moderation_jobs WHERE source = ? AND target_id = merchant_store_products.id AND request_id = merchant_store_products.ai_review_token AND status = ? AND market_outcome IN ?))", "pending", []string{"published", "rejected"}, ModerationSourceMarketProduct, ModerationJobCompleted, []string{"approved", "rejected"}).
			Order("CASE WHEN status = 'pending' THEN 0 ELSE 1 END")
	} else {
		q = q.Where("seller_id = ?", actor)
	}
	var rows []MerchantStoreProduct
	e := q.Order("created_at DESC,id ASC").Offset(offset).Limit(limit).Find(&rows).Error
	if e != nil {
		return nil, e
	}
	for i := range rows {
		if e = populateMerchantStoreProduct(DB, &rows[i], false); e != nil {
			return nil, e
		}
	}
	return rows, nil
}
func PurchaseMerchantStorePromotion(actor int, productID string, months int, requestKey string) (*MerchantStorePromotion, error) {
	if months < 1 || months > 12 || requestKey == "" || len(requestKey) > 128 {
		return nil, ErrMerchantStoreInput
	}
	var promo MerchantStorePromotion
	var recipientID int
	e := storeWithProduct(productID, func(tx *gorm.DB, p *MerchantStoreProduct) error {
		if p.SellerID != actor {
			return ErrMerchantStoreDenied
		}
		if p.Status != "published" {
			return ErrMerchantStoreUnavailable
		}
		c, e := storeConfig(tx)
		if e != nil {
			return e
		}
		if e = marketLockUsers(tx, actor, c.RecipientID); e != nil {
			return e
		}
		if _, e = storeUser(tx, actor, common.RoleCommonUser); e != nil {
			return e
		}
		if _, e = storeUser(tx, c.RecipientID, common.RoleRootUser); e != nil {
			return e
		}
		recipientID = c.RecipientID
		id := storeHash("promotion:" + fmtStoreActor(actor) + ":" + requestKey)
		if e = tx.First(&promo, "id = ?", id).Error; e == nil {
			if promo.ProductID != productID || promo.Months != months {
				return ErrMerchantStoreConflict
			}
			return nil
		} else if !errors.Is(e, gorm.ErrRecordNotFound) {
			return e
		}
		if c.PromotionQuota > common.MaxWalletQuota/months {
			return ErrMerchantStoreInput
		}
		quota := c.PromotionQuota * months
		if e = storeDebit(tx, actor, quota); e != nil {
			return e
		}
		if e = storeCredit(tx, c.RecipientID, quota); e != nil {
			return e
		}
		if e = storeTransfer(tx, id, "promotion", actor, c.RecipientID, quota); e != nil {
			return e
		}
		now := common.GetTimestamp()
		start := now
		if p.PromotionExpiresAt > start {
			start = p.PromotionExpiresAt
		}
		p.PromotionExpiresAt = start + int64(months)*30*86400
		promo = MerchantStorePromotion{ID: id, ProductID: productID, SellerID: actor, Months: months, Quota: quota, ExpiresAt: p.PromotionExpiresAt, CreatedAt: now}
		if e = tx.Save(p).Error; e != nil {
			return e
		}
		return tx.Create(&promo).Error
	})
	if e == nil {
		marketInvalidate(actor, recipientID)
	}
	return &promo, e
}
