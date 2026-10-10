package model

// Frozen signed phase-five product schema. New phase-six fields must not alter
// the historical access preparation model. Read-only DTO fields are ignored.
type merchantStoreProductSchemaFive struct {
	Catalogue             *MerchantStoreCatalogueMetadata `json:"catalogue,omitempty" gorm:"-"`
	DisplayTags           []string                        `json:"display_tags" gorm:"-"`
	NetPaidQuantity       *int64                          `json:"net_paid_quantity" gorm:"-"`
	ID                    string                          `json:"id" gorm:"primaryKey;size:36"`
	SellerID              int                             `json:"seller_id" gorm:"not null;index"`
	Seller                *MerchantStorePublicSeller      `json:"seller,omitempty" gorm:"-:all"`
	Title                 string                          `json:"title" gorm:"size:200;not null"`
	Description           string                          `json:"description" gorm:"type:text"`
	ImageURLs             []string                        `json:"image_urls" gorm:"serializer:json;type:text"`
	Contact               string                          `json:"contact" gorm:"type:text"`
	Links                 []MerchantStoreLink             `json:"links" gorm:"serializer:json;type:text"`
	PriceQuota            int                             `json:"price_quota" gorm:"type:bigint;not null"`
	TestMode              bool                            `json:"test_mode" gorm:"not null;default:false"`
	Visibility            string                          `json:"visibility" gorm:"size:16;not null;default:'';index"`
	PurchaseLoginRequired bool                            `json:"purchase_login_required" gorm:"not null;default:true"`
	SaleLimit             *int64                          `json:"sale_limit" gorm:"type:bigint"`
	MaxQuantityPerOrder   *int64                          `json:"max_quantity_per_order" gorm:"type:bigint"`
	MaxQuantityPerBuyer   *int64                          `json:"max_quantity_per_buyer" gorm:"type:bigint"`
	Template              string                          `json:"template" gorm:"size:32"`
	DeliveryStrategy      string                          `json:"delivery_strategy" gorm:"size:16"`
	PaymentMethods        []string                        `json:"payment_methods" gorm:"serializer:json;type:text"`
	PickupLoginRequired   bool                            `json:"pickup_login_required"`
	PickupCodeRequired    bool                            `json:"pickup_code_required"`
	EmailPickupLink       bool                            `json:"email_pickup_link"`
	Status                string                          `json:"status" gorm:"size:16;not null;index"`
	ReviewNote            string                          `json:"review_note" gorm:"type:text"`
	ReviewedBy            int                             `json:"reviewed_by"`
	ReviewedAt            int64                           `json:"reviewed_at"`
	AIReviewToken         string                          `json:"-" gorm:"type:varchar(36);not null;default:''"`
	PromotionExpiresAt    int64                           `json:"promotion_expires_at" gorm:"index"`
	CreatedAt             int64                           `json:"created_at"`
	UpdatedAt             int64                           `json:"updated_at"`
	Official              bool                            `json:"official" gorm:"-"`
	AvailableStock        int64                           `json:"available_stock" gorm:"-"`
	PaidQuantity          int64                           `json:"paid_quantity" gorm:"-"`
	ReservedQuantity      int64                           `json:"reserved_quantity" gorm:"-"`
	SaleAvailable         int64                           `json:"sale_available" gorm:"-"`
	TradingPaused         bool                            `json:"trading_paused" gorm:"-"`
	DefaultVariantID      string                          `json:"default_variant_id" gorm:"-"`
	Variants              []MerchantStoreVariant          `json:"variants" gorm:"-"`
	InventoryTotal        int64                           `json:"inventory_total" gorm:"-"`
	InventoryAvailable    int64                           `json:"inventory_available" gorm:"-"`
	PriceMinQuota         int                             `json:"price_min_quota" gorm:"-"`
	PriceMaxQuota         int                             `json:"price_max_quota" gorm:"-"`

	BuyerPurchaseRemaining *int64 `json:"buyer_purchase_remaining,omitempty" gorm:"-"`
}

func (*merchantStoreProductSchemaFive) TableName() string { return "merchant_store_products" }
