package model

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/url"
	"regexp"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const MerchantStoreCreditsPerUSD = 500000
const MerchantStoreDisclaimerVersion = "merchant-store-v1"
const MerchantStoreExternalMinimumQuota = 10 * MerchantStoreCreditsPerUSD

var (
	ErrMerchantStoreInput        = errors.New("invalid store input")
	ErrMerchantStoreDenied       = errors.New("store access denied")
	ErrMerchantStoreConflict     = errors.New("store request conflict")
	ErrMerchantStoreBalance      = errors.New("insufficient store wallet balance")
	ErrMerchantStoreStock        = errors.New("insufficient store inventory")
	ErrMerchantStoreDisclaimer   = errors.New("current merchant disclaimer must be accepted")
	ErrMerchantStorePendingLimit = errors.New("too many unpaid store orders")
	ErrMerchantStoreUnavailable  = errors.New("store product unavailable")
)

type MerchantStoreLink struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Description string `json:"description"`
}
type MerchantStoreProduct struct {
	ID                  string              `json:"id" gorm:"primaryKey;size:36"`
	SellerID            int                 `json:"seller_id" gorm:"not null;index"`
	Title               string              `json:"title" gorm:"size:200;not null"`
	Description         string              `json:"description" gorm:"type:text"`
	ImageURLs           []string            `json:"image_urls" gorm:"serializer:json;type:text"`
	Contact             string              `json:"contact" gorm:"type:text"`
	Links               []MerchantStoreLink `json:"links" gorm:"serializer:json;type:text"`
	PriceQuota          int                 `json:"price_quota" gorm:"type:bigint;not null"`
	SaleLimit           *int64              `json:"sale_limit" gorm:"type:bigint"`
	Template            string              `json:"template" gorm:"size:32"`
	DeliveryStrategy    string              `json:"delivery_strategy" gorm:"size:16"`
	PaymentMethods      []string            `json:"payment_methods" gorm:"serializer:json;type:text"`
	PickupLoginRequired bool                `json:"pickup_login_required"`
	PickupCodeRequired  bool                `json:"pickup_code_required"`
	EmailPickupLink     bool                `json:"email_pickup_link"`
	Status              string              `json:"status" gorm:"size:16;not null;index"`
	ReviewNote          string              `json:"review_note" gorm:"type:text"`
	ReviewedBy          int                 `json:"reviewed_by"`
	ReviewedAt          int64               `json:"reviewed_at"`
	AIReviewToken       string              `json:"-" gorm:"type:varchar(36);not null;default:''"`
	PromotionExpiresAt  int64               `json:"promotion_expires_at" gorm:"index"`
	CreatedAt           int64               `json:"created_at"`
	UpdatedAt           int64               `json:"updated_at"`
	Official            bool                `json:"official" gorm:"-"`
	AvailableStock      int64               `json:"available_stock" gorm:"-"`
	PaidQuantity        int64               `json:"paid_quantity" gorm:"-"`
	ReservedQuantity    int64               `json:"reserved_quantity" gorm:"-"`
	SaleAvailable       int64               `json:"sale_available" gorm:"-"`
	TradingPaused       bool                `json:"trading_paused" gorm:"-"`
}
type MerchantStoreProductInput struct {
	Title               string              `json:"title"`
	Description         string              `json:"description"`
	ImageURLs           []string            `json:"image_urls"`
	Contact             string              `json:"contact"`
	Links               []MerchantStoreLink `json:"links"`
	PriceQuota          int                 `json:"price_quota"`
	Template            string              `json:"template"`
	DeliveryStrategy    string              `json:"delivery_strategy"`
	PaymentMethods      []string            `json:"payment_methods"`
	PickupLoginRequired bool                `json:"pickup_login_required"`
	PickupCodeRequired  bool                `json:"pickup_code_required"`
	EmailPickupLink     bool                `json:"email_pickup_link"`
}
type MerchantStoreStock struct {
	ID         string `json:"id" gorm:"primaryKey;size:36"`
	ProductID  string `json:"product_id" gorm:"size:36;not null;index:store_stock_available,priority:1"`
	Ciphertext string `json:"-" gorm:"type:text;not null"`
	State      string `json:"state" gorm:"size:16;not null;index:store_stock_available,priority:2"`
	OrderID    string `json:"-" gorm:"size:64;index"`
	Position   int64  `json:"position" gorm:"index:store_stock_available,priority:3"`
	CreatedAt  int64  `json:"created_at"`
}
type MerchantStoreConfig struct {
	ID                    int    `json:"-" gorm:"primaryKey"`
	FeeBPS                int    `json:"fee_bps"`
	RecipientID           int    `json:"recipient_id"`
	PromotionQuota        int    `json:"promotion_quota" gorm:"type:bigint"`
	MinimumUnitPriceQuota int    `json:"minimum_unit_price_quota" gorm:"type:bigint;not null;default:500000"`
	LinuxDOUnitsPerUSD    string `json:"linuxdo_units_per_usd" gorm:"size:64"`
}
type MerchantStoreDisclaimerAcceptance struct {
	UserID     int    `json:"-" gorm:"primaryKey"`
	Version    string `json:"version" gorm:"primaryKey;size:64"`
	AcceptedAt int64  `json:"accepted_at"`
}
type MerchantStoreTransfer struct {
	ID         string `json:"id" gorm:"primaryKey;size:100"`
	OrderID    string `json:"order_id" gorm:"size:64;not null;index"`
	FromUserID int    `json:"from_user_id"`
	ToUserID   int    `json:"to_user_id"`
	Kind       string `json:"kind" gorm:"size:24"`
	Quota      int    `json:"quota" gorm:"type:bigint"`
	CreatedAt  int64  `json:"created_at"`
}
type MerchantStorePromotion struct {
	ID        string `json:"id" gorm:"primaryKey;size:64"`
	ProductID string `json:"product_id" gorm:"size:36;index"`
	SellerID  int    `json:"seller_id"`
	Months    int    `json:"months"`
	Quota     int    `json:"quota" gorm:"type:bigint"`
	ExpiresAt int64  `json:"expires_at"`
	CreatedAt int64  `json:"created_at"`
}
type MerchantStoreEvent struct {
	ID        string `json:"id" gorm:"primaryKey;size:36"`
	ActorID   int    `json:"actor_id"`
	ObjectID  string `json:"object_id" gorm:"size:64;index"`
	Action    string `json:"action" gorm:"size:32"`
	CreatedAt int64  `json:"created_at"`
}

func MerchantStoreModels() []interface{} {
	return []interface{}{&MerchantStoreProduct{}, &MerchantStoreStock{}, &MerchantStoreConfig{}, &MerchantStoreOrder{}, &MerchantStoreTransfer{}, &MerchantStoreDisclaimerAcceptance{}, &MerchantStoreGateway{}, &MerchantStorePromotion{}, &MerchantStoreEvent{}, &MerchantStoreEmailDelivery{}, &MerchantStorePaymentReceipt{}, &MerchantStoreVerifiedEmail{}, &MerchantStoreEmailVerificationChallenge{}, &MerchantStoreOrderSearchChallenge{}, &MerchantStoreOrderSearchAuthorization{}}
}
func storeHash(s string) string { sum := sha256.Sum256([]byte(s)); return hex.EncodeToString(sum[:]) }
func storeToken() (string, error) {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		return "", e
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func storeEvent(tx *gorm.DB, actor int, object, action string) error {
	return tx.Create(&MerchantStoreEvent{ID: uuid.NewString(), ActorID: actor, ObjectID: object, Action: action, CreatedAt: common.GetTimestamp()}).Error
}
func storeSecretPurpose(kind, id string) string { return "merchant-store:" + kind + ":" + id }
func storeEncrypt(kind, id, value string) (string, error) {
	return common.EncryptPersistentString(storeSecretPurpose(kind, id), "MERCHANT_STORE_ENCRYPTION_KEY", "CRYPTO_SECRET", value)
}
func storeDecrypt(kind, id, value string) (string, error) {
	return common.DecryptPersistentString(storeSecretPurpose(kind, id), "MERCHANT_STORE_ENCRYPTION_KEY", "CRYPTO_SECRET", value)
}
func storeUser(tx *gorm.DB, id, minRole int) (*User, error) {
	var u User
	if e := tx.Where("id = ? AND status = ? AND role >= ?", id, common.UserStatusEnabled, minRole).First(&u).Error; e != nil {
		return nil, ErrMerchantStoreDenied
	}
	return &u, nil
}
func storeURL(s string) bool {
	u, e := url.Parse(s)
	return e == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Hostname() != "" && u.User == nil && len(s) <= 4096
}
func storeConfig(tx *gorm.DB) (MerchantStoreConfig, error) {
	var c MerchantStoreConfig
	e := tx.First(&c, 1).Error
	if errors.Is(e, gorm.ErrRecordNotFound) {
		var u User
		if e = tx.Where("role = ? AND status = ?", common.RoleRootUser, common.UserStatusEnabled).Order("id ASC").First(&u).Error; e != nil {
			return c, ErrMerchantStoreUnavailable
		}
		c = MerchantStoreConfig{ID: 1, FeeBPS: 100, RecipientID: u.Id, PromotionQuota: MerchantStoreCreditsPerUSD, MinimumUnitPriceQuota: MerchantStoreCreditsPerUSD}
	} else if e != nil {
		return c, e
	}
	if c.FeeBPS < 0 || c.FeeBPS > 10000 || !marketQuotaValid(c.PromotionQuota) || !marketQuotaValid(c.MinimumUnitPriceQuota) || !storeLinuxDORateValid(c.LinuxDOUnitsPerUSD) {
		return c, ErrMerchantStoreInput
	}
	_, e = storeUser(tx, c.RecipientID, common.RoleRootUser)
	return c, e
}
func GetMerchantStoreConfig() (MerchantStoreConfig, error) { return storeConfig(DB) }
func SetMerchantStoreConfig(actor int, c MerchantStoreConfig) error {
	if c.FeeBPS < 0 || c.FeeBPS > 10000 || !marketQuotaValid(c.PromotionQuota) || !marketQuotaValid(c.MinimumUnitPriceQuota) || !storeLinuxDORateValid(c.LinuxDOUnitsPerUSD) || c.RecipientID <= 0 {
		return ErrMerchantStoreInput
	}
	return marketTransaction(DB, func(tx *gorm.DB) error {
		if e := marketLockUsers(tx, actor, c.RecipientID); e != nil {
			return e
		}
		if _, e := storeUser(tx, actor, common.RoleRootUser); e != nil {
			return e
		}
		if _, e := storeUser(tx, c.RecipientID, common.RoleRootUser); e != nil {
			return e
		}
		return storeWriteConfig(tx, c)
	})
}
func storeFee(price, bps int) int { return marketFee(price, bps) }
func storeDebit(tx *gorm.DB, id, amount int) error {
	r := UpdateWalletQuotaByDelta(tx.Model(&User{}).Where("id = ? AND status = ? AND quota >= ?", id, common.UserStatusEnabled, amount), -amount)
	if r.Error != nil {
		return r.Error
	}
	if r.RowsAffected != 1 {
		return ErrMerchantStoreBalance
	}
	return nil
}
func storeCredit(tx *gorm.DB, id, amount int) error {
	if _, e := storeUser(tx, id, common.RoleCommonUser); e != nil {
		return e
	}
	return ApplyWalletQuotaDelta(tx, id, amount)
}
func storeTransfer(tx *gorm.DB, ref, kind string, from, to, quota int) error {
	if quota == 0 {
		return nil
	}
	return tx.Create(&MerchantStoreTransfer{ID: ref + ":" + kind, OrderID: ref, FromUserID: from, ToUserID: to, Kind: kind, Quota: quota, CreatedAt: common.GetTimestamp()}).Error
}
func validateStoreProduct(in *MerchantStoreProductInput) error {
	in.Title = strings.TrimSpace(in.Title)
	if in.Title == "" || len(in.Title) > 200 || len(in.Description) > 128<<10 || len(in.Contact) > 4096 || len(in.ImageURLs) > 32 || len(in.Links) > 2000 || in.PriceQuota <= 0 || !marketQuotaValid(in.PriceQuota) {
		return ErrMerchantStoreInput
	}
	if in.Template == "" {
		in.Template = "card-key"
	}
	if in.Template != "card-key" && in.Template != "text" && in.Template != "custom-text" {
		return ErrMerchantStoreInput
	}
	if in.DeliveryStrategy == "" {
		in.DeliveryStrategy = "sequential"
	}
	if in.DeliveryStrategy != "sequential" && in.DeliveryStrategy != "random" {
		return ErrMerchantStoreInput
	}
	for _, v := range in.ImageURLs {
		if !storeURL(v) {
			return ErrMerchantStoreInput
		}
	}
	for _, v := range in.Links {
		if len(v.Title) == 0 || len(v.Title) > 200 || len(v.Description) > 4096 || !storeURL(v.URL) {
			return ErrMerchantStoreInput
		}
	}
	seen := map[string]bool{}
	for _, m := range in.PaymentMethods {
		if !storePaymentMethod(m) || seen[m] {
			return ErrMerchantStoreInput
		}
		seen[m] = true
	}
	return nil
}
func storePaymentMethod(m string) bool {
	_, known := MerchantStorePaymentCategory(m)
	return known
}
func storeProductOwner(tx *gorm.DB, actor int, id string) (*MerchantStoreProduct, error) {
	var p MerchantStoreProduct
	if e := lockForUpdate(tx).First(&p, "id = ?", id).Error; e != nil {
		return nil, e
	}
	if p.SellerID != actor {
		return nil, ErrMerchantStoreDenied
	}
	if _, e := storeUser(tx, actor, common.RoleCommonUser); e != nil {
		return nil, e
	}
	return &p, nil
}
func storeWithProduct(id string, fn func(*gorm.DB, *MerchantStoreProduct) error) error {
	return marketTransaction(DB, func(tx *gorm.DB) error {
		var p MerchantStoreProduct
		if e := lockForUpdate(tx).First(&p, "id = ?", id).Error; e != nil {
			return e
		}
		return fn(tx, &p)
	})
}

// Global store economics are editable only by a superadministrator.
func SetMerchantStorePromotionPrice(actor, quota int) error {
	if !marketQuotaValid(quota) {
		return ErrMerchantStoreInput
	}
	return marketTransaction(DB, func(tx *gorm.DB) error {
		if _, e := storeUser(tx, actor, common.RoleRootUser); e != nil {
			return e
		}
		c, e := storeConfig(tx)
		if e != nil {
			return e
		}
		if e = tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&c).Error; e != nil {
			return e
		}
		return tx.Model(&MerchantStoreConfig{}).Where("id = ?", 1).Update("promotion_quota", quota).Error
	})
}

var storeLinuxDORatePattern = regexp.MustCompile(`^[0-9]{1,12}(\.[0-9]{1,12})?$`)

func storeLinuxDORateValid(rate string) bool {
	if rate == "" {
		return true
	}
	if len(rate) > 64 || !storeLinuxDORatePattern.MatchString(rate) {
		return false
	}
	value, e := decimal.NewFromString(rate)
	return e == nil && value.IsPositive()
}
