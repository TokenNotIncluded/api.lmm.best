package model

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Anonymous traffic is separate from the permanent order and payment ledger.
// A receipt contains a hashed page identity, never an account, IP or user agent.
type MerchantStoreProductTrafficDay struct {
	ProductID   string `gorm:"primaryKey;size:36;not null"`
	Day         int64  `gorm:"primaryKey;type:bigint;not null;index"`
	Impressions int64  `gorm:"type:bigint;not null;default:0"`
	Clicks      int64  `gorm:"type:bigint;not null;default:0"`
}

func (MerchantStoreProductTrafficDay) TableName() string {
	return "merchant_store_product_traffic_days"
}

type MerchantStoreProductTrafficReceipt struct {
	ID            string `gorm:"primaryKey;size:64;not null"`
	ProductID     string `gorm:"size:36;not null;index"`
	Kind          string `gorm:"size:16;not null"`
	PageStartedAt int64  `gorm:"type:bigint;not null;index"`
	CreatedAt     int64  `gorm:"type:bigint;not null;index"`
}

func (MerchantStoreProductTrafficReceipt) TableName() string {
	return "merchant_store_product_traffic_receipts"
}

func storeAnalyticsModels() []interface{} {
	return []interface{}{&MerchantStoreProductTrafficDay{}, &MerchantStoreProductTrafficReceipt{}}
}

const MerchantStoreAnalyticsRetentionOption = "MerchantStoreAnalyticsRetention"

type MerchantStoreAnalyticsConfig struct {
	RetentionDays int `json:"retention_days"`
	DedupeDays    int `json:"dedupe_days"`
}

func (config MerchantStoreAnalyticsConfig) valid() bool {
	return config.RetentionDays >= 1 && config.RetentionDays <= 3650 && config.DedupeDays >= 1 && config.DedupeDays <= 30 && config.DedupeDays <= config.RetentionDays
}

func ParseMerchantStoreAnalyticsConfig(value string) (MerchantStoreAnalyticsConfig, error) {
	config := MerchantStoreAnalyticsConfig{RetentionDays: 365, DedupeDays: 7}
	if value == "" {
		return config, nil
	}
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&config) != nil || decoder.Decode(new(any)) != io.EOF || !config.valid() {
		return config, ErrMerchantStoreInput
	}
	return config, nil
}

func storeAnalyticsConfig(tx *gorm.DB) (MerchantStoreAnalyticsConfig, error) {
	var options []Option
	if err := tx.Session(&gorm.Session{NewDB: true}).Where("key = ?", MerchantStoreAnalyticsRetentionOption).Find(&options).Error; err != nil {
		return MerchantStoreAnalyticsConfig{}, err
	}
	if len(options) == 0 {
		return ParseMerchantStoreAnalyticsConfig("")
	}
	return ParseMerchantStoreAnalyticsConfig(options[0].Value)
}

func GetMerchantStoreAnalyticsConfig(actor int) (MerchantStoreAnalyticsConfig, error) {
	if _, err := storeUser(DB, actor, common.RoleAdminUser); err != nil {
		return MerchantStoreAnalyticsConfig{}, err
	}
	return storeAnalyticsConfig(DB)
}

func SaveMerchantStoreAnalyticsConfig(actor int, config MerchantStoreAnalyticsConfig) error {
	if !config.valid() {
		return ErrMerchantStoreInput
	}
	return marketTransaction(DB, func(tx *gorm.DB) error {
		if err := storeRequireWriter(tx); err != nil {
			return err
		}
		if _, err := storeUser(tx, actor, common.RoleRootUser); err != nil {
			return err
		}
		value, _ := json.Marshal(config)
		row := Option{Key: MerchantStoreAnalyticsRetentionOption, Value: string(value)}
		return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "key"}}, DoUpdates: clause.AssignmentColumns([]string{"value"})}).Create(&row).Error
	})
}

type MerchantStoreTrafficInput struct {
	Kind          string `json:"kind"`
	PageKey       string `json:"page_key"`
	PageStartedAt int64  `json:"page_started_at"`
}

func storeAnalyticsTrafficSupported(tx *gorm.DB) bool {
	floor, err := storeWriterGateRow(tx.Session(&gorm.Session{NewDB: true}), "")
	return err == nil && floor >= 7 && floor <= MerchantStoreWriterCapability && tx.Migrator().HasTable(&MerchantStoreProductTrafficDay{}) && tx.Migrator().HasTable(&MerchantStoreProductTrafficReceipt{})
}

func storeRequireAnalyticsWriter(tx *gorm.DB) error {
	if err := storePhaseSixFence(tx, true); err != nil {
		return err
	}
	floor, err := storeWriterGateRow(tx, "SHARE")
	if err != nil || floor < 7 || floor > MerchantStoreWriterCapability {
		return ErrMerchantStoreWriterFrozen
	}
	return nil
}

func storeAnalyticsDay(timestamp int64) int64 { return timestamp - timestamp%86400 }

// Each call deletes bounded batches only from these two disposable traffic
// tables. Lowering retention does not touch financial or business history.
func cleanupStoreAnalyticsTraffic(tx *gorm.DB, config MerchantStoreAnalyticsConfig, now int64) error {
	var receipts []string
	if err := tx.Model(&MerchantStoreProductTrafficReceipt{}).Where("page_started_at < ?", now-int64(config.DedupeDays)*86400).Order("page_started_at,id").Limit(100).Pluck("id", &receipts).Error; err != nil {
		return err
	}
	if len(receipts) > 0 {
		if err := tx.Where("id IN ?", receipts).Delete(&MerchantStoreProductTrafficReceipt{}).Error; err != nil {
			return err
		}
	}
	var days []MerchantStoreProductTrafficDay
	cutoff := storeAnalyticsDay(now) - int64(config.RetentionDays-1)*86400
	if err := tx.Select("product_id,day").Where("day < ?", cutoff).Order("day,product_id").Limit(100).Find(&days).Error; err != nil {
		return err
	}
	if len(days) > 0 {
		query := tx.Where("1=0")
		for _, day := range days {
			query = query.Or("product_id = ? AND day = ?", day.ProductID, day.Day)
		}
		if err := query.Delete(&MerchantStoreProductTrafficDay{}).Error; err != nil {
			return err
		}
	}
	return nil
}

// A real catalogue exposure or successful detail visit is counted once per
// page/product/kind. Retries and concurrent browser tabs with the same page key
// cannot increment twice. Merchant previews and self visits never count.
func RecordMerchantStoreTraffic(actor int, productID string, input MerchantStoreTrafficInput) error {
	page, err := uuid.Parse(input.PageKey)
	if actor < 0 || err != nil || page == uuid.Nil || page.String() != input.PageKey || (input.Kind != "impression" && input.Kind != "click") {
		return ErrMerchantStoreInput
	}
	now := common.GetTimestamp()
	return marketTransaction(DB, func(tx *gorm.DB) error {
		if err := storeRequireAnalyticsWriter(tx); err != nil {
			return err
		}
		config, err := storeAnalyticsConfig(tx)
		if err != nil {
			return err
		}
		if input.PageStartedAt < now-int64(config.DedupeDays)*86400 || input.PageStartedAt > now+300 {
			return ErrMerchantStoreInput
		}
		if actor > 0 {
			if _, err := storeUser(tx, actor, common.RoleCommonUser); err != nil {
				return err
			}
		}
		query := MerchantStoreVisibleProductsForViewer(tx, actor).Where("merchant_store_products.id = ?", productID)
		if tx.Dialector.Name() != "sqlite" {
			query = query.Clauses(clause.Locking{Strength: "SHARE"})
		}
		var product MerchantStoreProduct
		if err := query.First(&product).Error; err != nil {
			return err
		}
		if product.SellerID == actor || product.TestMode || MerchantStoreProductVisibility(&product) == "private" {
			return nil
		}
		if err := cleanupStoreAnalyticsTraffic(tx, config, now); err != nil {
			return err
		}
		digest := sha256.Sum256([]byte(product.ID + "\x00" + input.Kind + "\x00" + page.String()))
		receipt := MerchantStoreProductTrafficReceipt{ID: hex.EncodeToString(digest[:]), ProductID: product.ID, Kind: input.Kind, PageStartedAt: input.PageStartedAt, CreatedAt: now}
		created := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&receipt)
		if created.Error != nil || created.RowsAffected == 0 {
			return created.Error
		}
		row := MerchantStoreProductTrafficDay{ProductID: product.ID, Day: storeAnalyticsDay(now)}
		field := "impressions"
		row.Impressions = 1
		if input.Kind == "click" {
			field, row.Impressions, row.Clicks = "clicks", 0, 1
		}
		return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "product_id"}, {Name: "day"}}, DoUpdates: clause.Assignments(map[string]interface{}{field: gorm.Expr((MerchantStoreProductTrafficDay{}).TableName() + "." + field + " + 1")})}).Create(&row).Error
	})
}

type MerchantStoreAnalyticsCounts struct {
	Impressions            *int64 `json:"impressions"`
	Clicks                 *int64 `json:"clicks"`
	Orders                 int64  `json:"orders"`
	PaidOrders             int64  `json:"paid_orders"`
	RefundedOrders         int64  `json:"refunded_orders"`
	QuantityRefundedOrders int64  `json:"quantity_refunded_orders"`
	AmountRefundedOrders   int64  `json:"amount_refunded_orders"`
	OrderedQuantity        int64  `json:"ordered_quantity"`
	PaidQuantity           int64  `json:"paid_quantity"`
	RefundedQuantity       int64  `json:"refunded_quantity"`
	NetPaidQuantity        int64  `json:"net_paid_quantity"`
}

type MerchantStoreProductAnalytics struct {
	ProductID string `json:"product_id"`
	SellerID  int    `json:"seller_id"`
	Title     string `json:"title"`
	Status    string `json:"status"`
	MerchantStoreAnalyticsCounts
}

type MerchantStoreAnalytics struct {
	Items                []MerchantStoreProductAnalytics `json:"items"`
	Totals               MerchantStoreAnalyticsCounts    `json:"totals"`
	Offset               int                             `json:"offset"`
	Limit                int                             `json:"limit"`
	HasMore              bool                            `json:"has_more"`
	TrafficSupported     bool                            `json:"traffic_supported"`
	TrafficRetentionDays int                             `json:"traffic_retention_days"`
	TrafficSince         *int64                          `json:"traffic_since"`
	StartsAt             int64                           `json:"starts_at"`
	EndsAt               int64                           `json:"ends_at"`
	Timezone             string                          `json:"timezone"`
}

func storeAnalyticsProductScope(tx *gorm.DB, actor int, all bool) *gorm.DB {
	query := tx.Model(&MerchantStoreProduct{})
	if !all {
		query = query.Where("seller_id = ?", actor)
	}
	return query
}

// Orders are a creation-date cohort. Actual attested payment and completed
// refunds for those orders are observed as of the current query, independently
// of UI clicks. Seller self-purchases are excluded; money-only refunds do not
// reduce the delivered quantity. No financial totals are inferred from traffic.
func storeAnalyticsOrderAggregate(tx *gorm.DB, startsAt int64) *gorm.DB {
	paid := "(o.price_quota>0 AND (o.paid_at>0 OR o.status='paid' OR o.verified_payment_issue_at>0))"
	quantityRefund := "COALESCE((SELECT SUM(r.quantity) FROM merchant_store_refunds r WHERE r.order_id=o.id AND r.status='completed' AND r.completed_at>0 AND r.mode='quantity'),0)"
	refund := "EXISTS (SELECT 1 FROM merchant_store_refunds r WHERE r.order_id=o.id AND r.status='completed' AND r.completed_at>0)"
	quantityOrder := "EXISTS (SELECT 1 FROM merchant_store_refunds r WHERE r.order_id=o.id AND r.status='completed' AND r.completed_at>0 AND r.mode='quantity')"
	amountOrder := "EXISTS (SELECT 1 FROM merchant_store_refunds r WHERE r.order_id=o.id AND r.status='completed' AND r.completed_at>0 AND r.mode='amount')"
	clamped := "CASE WHEN o.quantity<" + quantityRefund + " THEN o.quantity ELSE " + quantityRefund + " END"
	query := tx.Table("merchant_store_orders o").Where("o.buyer_id<>o.seller_id AND (o.buyer_id>0 OR (o.buyer_id=0 AND LENGTH(COALESCE(o.guest_id,''))=36))")
	if startsAt > 0 {
		query = query.Where("o.created_at >= ?", startsAt)
	}
	return query.Select("o.product_id, COUNT(*) AS orders, SUM(o.quantity) AS ordered_quantity, SUM(CASE WHEN " + paid + " THEN 1 ELSE 0 END) AS paid_orders, SUM(CASE WHEN " + paid + " AND " + refund + " THEN 1 ELSE 0 END) AS refunded_orders, SUM(CASE WHEN " + paid + " AND " + quantityOrder + " THEN 1 ELSE 0 END) AS quantity_refunded_orders, SUM(CASE WHEN " + paid + " AND " + amountOrder + " THEN 1 ELSE 0 END) AS amount_refunded_orders, SUM(CASE WHEN " + paid + " THEN o.quantity ELSE 0 END) AS paid_quantity, SUM(CASE WHEN " + paid + " THEN " + clamped + " ELSE 0 END) AS refunded_quantity").Group("o.product_id")
}

func GetMerchantStoreAnalytics(actor int, all bool, days, offset, limit int) (*MerchantStoreAnalytics, error) {
	if actor < 1 || days < 0 || days > 3650 || offset < 0 || offset > 100000 || limit < 1 || limit > 100 {
		return nil, ErrMerchantStoreInput
	}
	result := &MerchantStoreAnalytics{Items: []MerchantStoreProductAnalytics{}, Offset: offset, Limit: limit, EndsAt: common.GetTimestamp(), Timezone: "UTC"}
	if days > 0 {
		result.StartsAt = storeAnalyticsDay(result.EndsAt) - int64(days-1)*86400
	}
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := storeRequirePhaseSixReadable(tx); err != nil {
			return err
		}
		role := common.RoleCommonUser
		if all {
			role = common.RoleAdminUser
		}
		if _, err := storeUser(tx, actor, role); err != nil {
			return err
		}
		config, err := storeAnalyticsConfig(tx)
		if err != nil {
			return err
		}
		result.TrafficSupported, result.TrafficRetentionDays = storeAnalyticsTrafficSupported(tx), config.RetentionDays
		orders := storeAnalyticsOrderAggregate(tx, result.StartsAt).Where("o.product_id IN (?)", storeAnalyticsProductScope(tx, actor, all).Select("id"))
		base := storeAnalyticsProductScope(tx, actor, all).Joins("LEFT JOIN (?) stats ON stats.product_id=merchant_store_products.id", orders)
		fields := "COALESCE(stats.orders,0) AS orders,COALESCE(stats.paid_orders,0) AS paid_orders,COALESCE(stats.refunded_orders,0) AS refunded_orders,COALESCE(stats.quantity_refunded_orders,0) AS quantity_refunded_orders,COALESCE(stats.amount_refunded_orders,0) AS amount_refunded_orders,COALESCE(stats.ordered_quantity,0) AS ordered_quantity,COALESCE(stats.paid_quantity,0) AS paid_quantity,COALESCE(stats.refunded_quantity,0) AS refunded_quantity,(COALESCE(stats.paid_quantity,0)-COALESCE(stats.refunded_quantity,0)) AS net_paid_quantity"
		if result.TrafficSupported {
			cutoff := storeAnalyticsDay(result.EndsAt) - int64(config.RetentionDays-1)*86400
			if result.StartsAt > cutoff {
				cutoff = result.StartsAt
			}
			traffic := tx.Model(&MerchantStoreProductTrafficDay{}).Where("day >= ?", cutoff).Select("product_id,SUM(impressions) AS impressions,SUM(clicks) AS clicks").Group("product_id")
			base = base.Joins("LEFT JOIN (?) traffic ON traffic.product_id=merchant_store_products.id", traffic)
			fields += ",COALESCE(traffic.impressions,0) AS impressions,COALESCE(traffic.clicks,0) AS clicks"
			var since sql.NullInt64
			if err := tx.Model(&MerchantStoreProductTrafficDay{}).Where("day >= ? AND product_id IN (?)", cutoff, storeAnalyticsProductScope(tx, actor, all).Select("id")).Select("MIN(day)").Scan(&since).Error; err != nil {
				return err
			}
			if since.Valid {
				result.TrafficSince = &since.Int64
			}
		}
		if err := base.Session(&gorm.Session{}).Select("merchant_store_products.id AS product_id,merchant_store_products.seller_id,merchant_store_products.title,merchant_store_products.status," + fields).Order("merchant_store_products.created_at DESC,merchant_store_products.id ASC").Offset(offset).Limit(limit + 1).Scan(&result.Items).Error; err != nil {
			return err
		}
		result.HasMore = len(result.Items) > limit
		if result.HasMore {
			result.Items = result.Items[:limit]
		}
		// Aggregate the complete authorized scope, not just this visible page.
		var totals MerchantStoreAnalyticsCounts
		summary := "COALESCE(SUM(orders),0) AS orders,COALESCE(SUM(paid_orders),0) AS paid_orders,COALESCE(SUM(refunded_orders),0) AS refunded_orders,COALESCE(SUM(quantity_refunded_orders),0) AS quantity_refunded_orders,COALESCE(SUM(amount_refunded_orders),0) AS amount_refunded_orders,COALESCE(SUM(ordered_quantity),0) AS ordered_quantity,COALESCE(SUM(paid_quantity),0) AS paid_quantity,COALESCE(SUM(refunded_quantity),0) AS refunded_quantity,COALESCE(SUM(net_paid_quantity),0) AS net_paid_quantity"
		if result.TrafficSupported {
			summary += ",COALESCE(SUM(impressions),0) AS impressions,COALESCE(SUM(clicks),0) AS clicks"
		}
		if err := tx.Table("(?) aggregate_products", base.Session(&gorm.Session{}).Select(fields)).Select(summary).Scan(&totals).Error; err != nil {
			return err
		}
		result.Totals = totals
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	return result, err
}
