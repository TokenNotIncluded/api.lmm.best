package model

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type MerchantStoreOrder struct {
	EmailDeliveryStatus        string `json:"email_delivery_status,omitempty" gorm:"-"`
	PaymentIssued              bool   `json:"payment_issued" gorm:"-"`
	ID                         string `json:"id" gorm:"primaryKey;size:64"`
	TradeNo                    string `json:"trade_no" gorm:"size:32;uniqueIndex;not null"`
	BuyerID                    int    `json:"buyer_id" gorm:"not null;index"`
	SellerID                   int    `json:"seller_id" gorm:"not null;index"`
	ProductID                  string `json:"product_id" gorm:"size:36;not null;index"`
	ProductTitle               string `json:"product_title" gorm:"size:200"`
	DeliveryTemplate           string `json:"delivery_template" gorm:"type:varchar(32);not null;default:''"`
	Quantity                   int    `json:"quantity"`
	UnitPriceQuota             int    `json:"unit_price_quota" gorm:"type:bigint"`
	PriceQuota                 int    `json:"price_quota" gorm:"type:bigint"`
	FeeQuota                   int    `json:"fee_quota" gorm:"type:bigint"`
	FeeBPS                     int    `json:"fee_bps"`
	RecipientID                int    `json:"-"`
	FeeHeld                    bool   `json:"-"`
	PaymentMethod              string `json:"payment_method" gorm:"size:40"`
	InputDigest                string `json:"-" gorm:"size:64"`
	Status                     string `json:"status" gorm:"size:32;not null;index"`
	AmountMinor                int64  `json:"amount_minor" gorm:"type:bigint"`
	Currency                   string `json:"currency" gorm:"size:16"`
	FrozenUSDFX                string `json:"frozen_usd_fx" gorm:"size:64;column:frozen_usd_fx"`
	ProviderTradeID            string `json:"-" gorm:"size:128"`
	GatewaySnapshot            string `json:"-" gorm:"type:text"`
	PaymentScopeHash           string `json:"-" gorm:"size:64"`
	PaymentIssueCode           string `json:"payment_issue_code,omitempty" gorm:"size:64"`
	PaymentIssueOriginalStatus string `json:"payment_issue_original_status,omitempty" gorm:"size:32"`
	VerifiedPaymentIssueAt     int64  `json:"verified_payment_issue_at,omitempty"`
	CheckoutURL                string `json:"checkout_url,omitempty" gorm:"type:text"`
	ProviderSessionID          string `json:"-" gorm:"size:128"`
	ProviderCheckoutExpiresAt  int64  `json:"-"`
	ProviderClosureReference   string `json:"-" gorm:"size:128"`
	PaymentCheckedAt           int64  `json:"-"`
	PaymentCheckError          string `json:"-" gorm:"size:64"`
	PickupTokenHash            string `json:"-" gorm:"size:64;uniqueIndex"`
	PickupTokenCiphertext      string `json:"-" gorm:"type:text"`
	PickupCodeHash             string `json:"-" gorm:"size:128"`
	PickupLoginRequired        bool   `json:"pickup_login_required"`
	PickupCodeRequired         bool   `json:"pickup_code_required"`
	EmailPickupLink            bool   `json:"email_pickup_link"`
	PickupEmailHash            string `json:"-" gorm:"size:64;index"`
	PickupEmailCiphertext      string `json:"-" gorm:"type:text"`
	OfficialAtPurchase         bool   `json:"official_at_purchase"`
	CreatedAt                  int64  `json:"created_at"`
	PaidAt                     int64  `json:"paid_at"`
	ExpiresAt                  int64  `json:"expires_at" gorm:"index"`
	ClaimedAt                  int64  `json:"claimed_at"`
}

// Provider transactions settle at most one order within a gateway scope.
// Platform receipts have a global scope across all merchants.
type MerchantStorePaymentReceipt struct {
	ID        string `json:"-" gorm:"primaryKey;size:64"`
	OrderID   string `json:"-" gorm:"size:64;not null;uniqueIndex"`
	CreatedAt int64  `json:"-"`
}
type MerchantStoreCheckoutInput struct {
	BuyerID           int    `json:"-"`
	ProductID         string `json:"product_id"`
	Quantity          int    `json:"quantity"`
	RequestKey        string `json:"request_key"`
	PaymentMethod     string `json:"payment_method"`
	PickupCode        string `json:"pickup_code"`
	PickupEmail       string `json:"pickup_email"`
	DisclaimerVersion string `json:"disclaimer_version"`
}
type MerchantStoreClaimMetadata struct {
	PickupLoginSatisfied bool   `json:"pickup_login_satisfied"`
	Status               string `json:"status"`
	OrderID              string `json:"order_id"`
	ProductTitle         string `json:"product_title"`
	DeliveryTemplate     string `json:"delivery_template"`
	Quantity             int    `json:"quantity"`
	PickupLoginRequired  bool   `json:"pickup_login_required"`
	PickupCodeRequired   bool   `json:"pickup_code_required"`
}
type MerchantStoreClaim struct {
	OrderID          string   `json:"order_id"`
	ProductTitle     string   `json:"product_title"`
	DeliveryTemplate string   `json:"delivery_template"`
	Items            []string `json:"items"`
}

func fmtStoreActor(id int) string { return strconv.Itoa(id) }
func storeCheckoutDigest(in MerchantStoreCheckoutInput) string {
	values := []any{in.ProductID, in.Quantity, in.PaymentMethod, storeHash(in.PickupCode)}
	// Preserve replay digests for orders placed before optional pickup email.
	if in.PickupEmail != "" {
		values = append(values, storeHash(in.PickupEmail))
	}
	return marketDigest(values)
}
func storeAcceptDisclaimer(tx *gorm.DB, in MerchantStoreCheckoutInput, official bool) error {
	if official {
		return nil
	}
	var a MerchantStoreDisclaimerAcceptance
	e := tx.First(&a, "user_id = ? AND version = ?", in.BuyerID, MerchantStoreDisclaimerVersion).Error
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return ErrMerchantStoreDisclaimer
	}
	return e
}
func AcceptMerchantStoreDisclaimer(buyerID int, version string) error {
	if version != MerchantStoreDisclaimerVersion {
		return ErrMerchantStoreDisclaimer
	}
	return marketTransaction(DB, func(tx *gorm.DB) error {
		if e := marketLockUsers(tx, buyerID); e != nil {
			return e
		}
		if _, e := storeUser(tx, buyerID, common.RoleCommonUser); e != nil {
			return e
		}
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&MerchantStoreDisclaimerAcceptance{UserID: buyerID, Version: version, AcceptedAt: common.GetTimestamp()}).Error
	})
}
func HasMerchantStoreDisclaimerAcceptance(buyerID int) (bool, error) {
	var count int64
	e := DB.Model(&MerchantStoreDisclaimerAcceptance{}).Where("user_id = ? AND version = ?", buyerID, MerchantStoreDisclaimerVersion).Count(&count).Error
	return count > 0, e
}
func CreateMerchantStoreOrder(in MerchantStoreCheckoutInput) (*MerchantStoreOrder, bool, error) {
	var e error
	in.PickupEmail, e = NormalizeMerchantStorePickupEmail(in.PickupEmail)
	if e != nil {
		return nil, false, e
	}
	if in.BuyerID <= 0 || in.Quantity < 1 || in.Quantity > 1000 || in.RequestKey == "" || len(in.RequestKey) > 128 || !storePaymentMethod(in.PaymentMethod) || len(in.PickupCode) > 72 {
		return nil, false, ErrMerchantStoreInput
	}
	var o MerchantStoreOrder
	created := false
	var invalidations []int
	e = storeWithProduct(in.ProductID, func(tx *gorm.DB, p *MerchantStoreProduct) error {
		c, e := storeConfig(tx)
		if e != nil {
			return e
		}
		if e = marketLockUsers(tx, in.BuyerID, p.SellerID, c.RecipientID); e != nil {
			return e
		}
		if _, e = storeUser(tx, c.RecipientID, common.RoleRootUser); e != nil {
			return e
		}
		buyer, e := storeUser(tx, in.BuyerID, common.RoleCommonUser)
		if e != nil {
			return e
		}
		seller, e := storeUser(tx, p.SellerID, common.RoleCommonUser)
		if e != nil {
			return e
		}
		id := storeHash("order:" + fmtStoreActor(in.BuyerID) + ":" + in.RequestKey)
		digest := storeCheckoutDigest(in)
		if e = tx.First(&o, "id = ?", id).Error; e == nil {
			if o.InputDigest != digest {
				return ErrMerchantStoreConflict
			}
			return storeOrderEmailViews(tx, in.BuyerID, []*MerchantStoreOrder{&o})
		} else if !errors.Is(e, gorm.ErrRecordNotFound) {
			return e
		}
		// A valid existing order retains its frozen obligations when the listing
		// changes mode. Only new orders use the current visibility/buyer policy.
		if e = storeProductNewBuyer(p, buyer.Id); e != nil {
			return e
		}
		// Existing orders above returned before current listing policy. Check
		// the unit price, never quantity times price, under the current config.
		if e = storeRequireMinimumUnitPrice(tx, p.PriceQuota); e != nil {
			return e
		}
		if e = storeRequirePaymentCategory(tx, seller.Id, in.PaymentMethod); e != nil {
			return e
		}
		if in.PaymentMethod != "balance" {
			if in.Quantity > 100 {
				return ErrMerchantStoreInput
			}
			var pending, sameProduct int64
			q := tx.Model(&MerchantStoreOrder{}).Where("buyer_id = ? AND status IN ?", buyer.Id, []string{"pending", "reconciliation_pending"})
			if e = q.Count(&pending).Error; e != nil {
				return e
			}
			if e = tx.Model(&MerchantStoreOrder{}).Where("buyer_id = ? AND product_id = ? AND status IN ?", buyer.Id, p.ID, []string{"pending", "reconciliation_pending"}).Count(&sameProduct).Error; e != nil {
				return e
			}
			if pending >= 3 || sameProduct >= 1 {
				return ErrMerchantStorePendingLimit
			}
		}
		if e = storeCheckSaleLimit(tx, p, in.Quantity); e != nil {
			return e
		}
		allowed := false
		for _, method := range p.PaymentMethods {
			if method == in.PaymentMethod {
				allowed = true
			}
		}
		if !allowed {
			return ErrMerchantStoreDenied
		}
		if (p.PickupCodeRequired || in.PickupCode != "") && len(in.PickupCode) < 8 {
			return ErrMerchantStoreInput
		}
		if p.EmailPickupLink && in.PickupEmail == "" {
			return ErrMerchantStoreInput
		}
		if p.PriceQuota <= 0 || p.PriceQuota > common.MaxWalletQuota/in.Quantity {
			return ErrMerchantStoreInput
		}
		price := p.PriceQuota * in.Quantity
		fee := storeFee(price, c.FeeBPS)
		if seller.Role == common.RoleRootUser {
			fee = 0
		}
		if seller.Quota < fee {
			return ErrMerchantStoreBalance
		}
		var gateway MerchantStoreGateway
		if e = tx.Where("seller_id = ? AND provider = ? AND enabled = ?", seller.Id, in.PaymentMethod, true).First(&gateway).Error; e != nil {
			return ErrMerchantStoreUnavailable
		}
		if strings.HasPrefix(in.PaymentMethod, "external:") {
			if seller.Quota <= MerchantStoreExternalMinimumQuota {
				return ErrMerchantStoreDenied
			}
			provider := in.PaymentMethod
			var g MerchantStoreGateway
			if e = tx.Where("seller_id = ? AND provider = ? AND enabled = ?", seller.Id, provider, true).First(&g).Error; e != nil {
				return ErrMerchantStoreUnavailable
			}
		}
		if e = storeAcceptDisclaimer(tx, in, seller.Role >= common.RoleAdminUser); e != nil {
			return e
		}
		token, e := storeToken()
		if e != nil {
			return e
		}
		cipher, e := storeEncrypt("pickup", id, token)
		if e != nil {
			return e
		}
		now := common.GetTimestamp()
		o = MerchantStoreOrder{ID: id, BuyerID: buyer.Id, SellerID: seller.Id, ProductID: p.ID, ProductTitle: p.Title, DeliveryTemplate: p.Template, Quantity: in.Quantity, UnitPriceQuota: p.PriceQuota, PriceQuota: price, FeeQuota: fee, FeeBPS: c.FeeBPS, RecipientID: c.RecipientID, InputDigest: digest, PaymentMethod: in.PaymentMethod, Status: "pending", PickupTokenHash: storeHash(token), PickupTokenCiphertext: cipher, PickupLoginRequired: p.PickupLoginRequired, PickupCodeRequired: in.PickupCode != "", EmailPickupLink: in.PickupEmail != "", OfficialAtPurchase: seller.Role >= common.RoleAdminUser, CreatedAt: now, ExpiresAt: now + 1800}
		if in.PickupEmail != "" {
			o.PickupEmailHash = storeHash(in.PickupEmail)
			o.PickupEmailCiphertext, e = storeEncrypt("order-pickup-email", id, in.PickupEmail)
			if e != nil {
				return e
			}
		}
		if in.PickupCode != "" {
			hash, e := bcrypt.GenerateFromPassword([]byte(in.PickupCode), bcrypt.DefaultCost)
			if e != nil {
				return e
			}
			o.PickupCodeHash = string(hash)
		}
		var stock []MerchantStoreStock
		q := tx.Select("id").Where("product_id = ? AND state = ?", p.ID, "available")
		if p.DeliveryStrategy == "random" {
			if tx.Dialector.Name() == "mysql" {
				q = q.Order("RAND()")
			} else {
				q = q.Order("RANDOM()")
			}
		} else {
			q = q.Order("position ASC,id ASC")
		}
		if e = q.Limit(in.Quantity).Find(&stock).Error; e != nil {
			return e
		}

		if len(stock) < in.Quantity {
			return ErrMerchantStoreStock
		}
		ids := make([]string, in.Quantity)
		for i := 0; i < in.Quantity; i++ {
			ids[i] = stock[i].ID
		}
		r := tx.Model(&MerchantStoreStock{}).Where("id IN ? AND state = ?", ids, "available").Updates(map[string]any{"state": "reserved", "order_id": id})
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != int64(in.Quantity) {
			return ErrMerchantStoreStock
		}
		if in.PaymentMethod == "balance" {
			if e = storeDebit(tx, buyer.Id, price); e != nil {
				return e
			}
			if e = storeCredit(tx, seller.Id, price-fee); e != nil {
				return e
			}
			if e = storeCredit(tx, c.RecipientID, fee); e != nil {
				return e
			}
			if e = storeTransfer(tx, id, "sale", buyer.Id, seller.Id, price); e != nil {
				return e
			}
			if e = storeTransfer(tx, id, "fee", seller.Id, c.RecipientID, fee); e != nil {
				return e
			}
			o.Status = "paid"
			o.PaidAt = now
			if e = tx.Model(&MerchantStoreStock{}).Where("order_id = ? AND state = ?", id, "reserved").Update("state", "delivered").Error; e != nil {
				return e
			}
		} else if fee > 0 {
			if e = storeDebit(tx, seller.Id, fee); e != nil {
				return e
			}
			o.FeeHeld = true
			if e = storeTransfer(tx, id, "fee_hold", seller.Id, 0, fee); e != nil {
				return e
			}
		}
		if e = storeCreateOrderWithRandomTradeNo(tx, &o); e != nil {
			return e
		}
		if o.Status == "paid" {
			if e := enqueueMerchantStoreEmail(tx, &o); e != nil {
				return e
			}
		}
		created = true
		invalidations = []int{buyer.Id, seller.Id, c.RecipientID}
		return storeEvent(tx, buyer.Id, id, "checkout")
	})
	if e == nil && created {
		marketInvalidate(invalidations...)
	}
	o.PaymentIssued = o.GatewaySnapshot != "" || o.ProviderSessionID != ""
	if o.EmailDeliveryStatus == "" {
		o.EmailDeliveryStatus = "none"
	}
	return &o, created && e == nil, e
}
func storeOrderTx(id string, fn func(*gorm.DB, *MerchantStoreOrder) error) error {
	var o MerchantStoreOrder
	if e := DB.First(&o, "id = ?", id).Error; e != nil {
		return e
	}
	return storeWithProduct(o.ProductID, func(tx *gorm.DB, _ *MerchantStoreProduct) error {
		if e := lockForUpdate(tx).First(&o, "id = ?", id).Error; e != nil {
			return e
		}
		return fn(tx, &o)
	})
}
func GetMerchantStorePaymentOrder(id string) (*MerchantStoreOrder, error) {
	var o MerchantStoreOrder
	e := DB.First(&o, "id = ?", id).Error
	o.PaymentIssued = o.GatewaySnapshot != "" || o.ProviderSessionID != ""
	return &o, e
}
func GetMerchantStorePaymentOrderByTradeNo(trade string) (*MerchantStoreOrder, error) {
	if !storeValidTradeNo(trade) {
		return nil, ErrMerchantStoreInput
	}
	var o MerchantStoreOrder
	e := DB.Scopes(marketExactTextScope("trade_no", trade)).First(&o).Error
	o.PaymentIssued = o.GatewaySnapshot != "" || o.ProviderSessionID != ""
	return &o, e
}

// LDC fx is a legacy integrity field containing an original platform rate
// declaration, not a derived USD exchange rate. amountMinor freezes the invoice.
func BindMerchantStorePaymentQuote(id string, amountMinor int64, currency, fx string) error {
	if amountMinor <= 0 || amountMinor > int64(common.MaxWalletQuota) || len(currency) < 3 || len(currency) > 16 || currency != strings.ToUpper(currency) || len(fx) == 0 || len(fx) > 64 {
		return ErrMerchantStoreInput
	}
	return storeOrderTx(id, func(tx *gorm.DB, o *MerchantStoreOrder) error {
		if o.PaymentMethod == "balance" || o.Status != "pending" || o.ExpiresAt <= common.GetTimestamp() {
			return ErrMerchantStoreConflict
		}
		if o.AmountMinor != 0 {
			if o.AmountMinor == amountMinor && o.Currency == currency && o.FrozenUSDFX == fx {
				return nil
			}
			return ErrMerchantStoreConflict
		}
		return tx.Model(o).Updates(map[string]any{"amount_minor": amountMinor, "currency": currency, "frozen_usd_fx": fx}).Error
	})
}
func BindMerchantStorePaymentContext(id, snapshot string, scopeHashes ...string) error {
	if len(scopeHashes) > 1 {
		return ErrMerchantStoreInput
	}
	scopeHash := ""
	if len(scopeHashes) == 1 {
		scopeHash = scopeHashes[0]
		if !storePaymentScopeValid(scopeHash) {
			return ErrMerchantStoreInput
		}
	}
	if snapshot == "" || len(snapshot) > 128<<10 {
		return ErrMerchantStoreInput
	}
	return storeOrderTx(id, func(tx *gorm.DB, o *MerchantStoreOrder) error {
		if o.Status != "pending" {
			return ErrMerchantStoreConflict
		}
		if o.GatewaySnapshot != "" {
			if o.GatewaySnapshot == snapshot && o.PaymentScopeHash == scopeHash {
				return nil
			}
			return ErrMerchantStoreConflict
		}
		// Serialize first issuance with category/channel changes. Existing
		// encrypted invoices remain usable regardless of current enablement.
		if err := marketLockUsers(tx, o.SellerID); err != nil {
			return err
		}
		seller, err := storeUser(tx, o.SellerID, common.RoleCommonUser)
		if err != nil {
			return err
		}
		if err = storeRequirePaymentCategory(tx, o.SellerID, o.PaymentMethod); err != nil {
			return err
		}
		if err = storeValidatePaymentSelection(tx, seller, []string{o.PaymentMethod}); err != nil {
			return err
		}
		return tx.Model(o).Updates(map[string]any{"gateway_snapshot": snapshot, "payment_scope_hash": scopeHash}).Error
	})
}
func BindMerchantStoreCheckoutSession(id, checkoutURL, sessionID string, expiresAt ...int64) error {
	if len(expiresAt) > 1 {
		return ErrMerchantStoreInput
	}
	var expiry int64
	if len(expiresAt) == 1 {
		expiry = expiresAt[0]
		if expiry < 0 || expiry > int64(common.MaxWalletQuota) {
			return ErrMerchantStoreInput
		}
	}
	if !storeURL(checkoutURL) || sessionID == "" || len(sessionID) > 128 {
		return ErrMerchantStoreInput
	}
	return storeOrderTx(id, func(tx *gorm.DB, o *MerchantStoreOrder) error {
		if o.Status != "pending" && o.Status != "reconciliation_pending" {
			return ErrMerchantStoreConflict
		}
		if (o.ExpiresAt <= common.GetTimestamp() || o.Status == "reconciliation_pending") && o.GatewaySnapshot == "" {
			return ErrMerchantStoreConflict
		}
		if o.ProviderSessionID != "" {
			if o.ProviderSessionID == sessionID && o.CheckoutURL == checkoutURL && o.ProviderCheckoutExpiresAt == expiry {
				return nil
			}
			return ErrMerchantStoreConflict
		}
		return tx.Model(o).Updates(map[string]any{"checkout_url": checkoutURL, "provider_session_id": sessionID, "provider_checkout_expires_at": expiry}).Error
	})
}

// Only an adapter that verified signature, exact amount/currency, merchant,
// frozen gateway and provider transaction may call this settlement primitive.
func CompleteMerchantStorePayment(id, providerTradeID string) error {
	if providerTradeID == "" || len(providerTradeID) > 128 {
		return ErrMerchantStoreInput
	}
	var invalidations []int
	e := storeOrderTx(id, func(tx *gorm.DB, o *MerchantStoreOrder) error {
		if o.Status == "paid" {
			if o.ProviderTradeID == providerTradeID {
				return nil
			}
			return ErrMerchantStoreConflict
		}
		if (o.Status != "pending" && o.Status != "reconciliation_pending") || o.PaymentMethod == "balance" || o.AmountMinor <= 0 {
			return ErrMerchantStoreConflict
		}
		if o.ProviderTradeID != "" && o.ProviderTradeID != providerTradeID {
			return ErrMerchantStoreConflict
		}
		if e := storeReservePaymentReceipt(tx, o, providerTradeID); e != nil {
			return e
		}
		if e := marketLockUsers(tx, o.SellerID, o.RecipientID); e != nil {
			if errors.Is(e, gorm.ErrRecordNotFound) {
				return ErrMerchantStoreDenied
			}
			return e
		}
		// New checkout rejects disabled merchants. A verified payment for an
		// already reserved order still fulfills the frozen obligation; the wallet
		// lock above proves this account exists and delta guards preserve its bounds.
		if _, e := storeUser(tx, o.RecipientID, common.RoleRootUser); e != nil {
			return e
		}
		if o.FeeQuota > 0 && !o.FeeHeld {
			return ErrMerchantStoreConflict
		}
		var reserved int64
		if e := tx.Model(&MerchantStoreStock{}).Where("order_id = ? AND state = ?", id, "reserved").Count(&reserved).Error; e != nil {
			return e
		}
		if reserved != int64(o.Quantity) {
			return ErrMerchantStoreStock
		}
		if strings.HasPrefix(o.PaymentMethod, "platform:") {
			if e := ApplyWalletQuotaDelta(tx, o.SellerID, o.PriceQuota); e != nil {
				return e
			}
			if e := storeTransfer(tx, id, "sale", 0, o.SellerID, o.PriceQuota); e != nil {
				return e
			}
		}
		if e := storeCredit(tx, o.RecipientID, o.FeeQuota); e != nil {
			return e
		}
		if e := storeTransfer(tx, id, "fee", 0, o.RecipientID, o.FeeQuota); e != nil {
			return e
		}
		o.Status = "paid"
		o.PaidAt = common.GetTimestamp()
		o.ProviderTradeID = providerTradeID
		o.PaymentIssueCode = ""
		o.VerifiedPaymentIssueAt = 0
		o.PaymentIssueOriginalStatus = ""
		o.FeeHeld = false
		if e := tx.Save(o).Error; e != nil {
			return e
		}
		if e := tx.Model(&MerchantStoreStock{}).Where("order_id = ? AND state = ?", id, "reserved").Update("state", "delivered").Error; e != nil {
			return e
		}
		if e := enqueueMerchantStoreEmail(tx, o); e != nil {
			return e
		}
		invalidations = []int{o.SellerID, o.RecipientID}
		return storeEvent(tx, 0, id, "payment_verified")
	})
	if e == nil {
		marketInvalidate(invalidations...)
	}
	return e
}
func closeMerchantStoreOrder(id string, actor int, expired, providerClosed bool, closureReference string) error {
	var sellerID int
	e := storeOrderTx(id, func(tx *gorm.DB, o *MerchantStoreOrder) error {
		if expired {
			if o.ExpiresAt > common.GetTimestamp() {
				return ErrMerchantStoreConflict
			}
		} else if !providerClosed {
			u, e := storeUser(tx, actor, common.RoleCommonUser)
			if e != nil {
				return e
			}
			if actor != o.BuyerID && actor != o.SellerID && u.Role < common.RoleAdminUser {
				return ErrMerchantStoreDenied
			}
		}
		if o.Status == "cancelled" || o.Status == "expired" {
			return nil
		}
		if o.ProviderTradeID != "" || o.VerifiedPaymentIssueAt > 0 {
			return ErrMerchantStoreConflict
		}
		if o.Status != "pending" && !(providerClosed && o.Status == "reconciliation_pending") {
			return ErrMerchantStoreConflict
		}
		if !providerClosed && (o.GatewaySnapshot != "" || o.ProviderSessionID != "") {
			return ErrMerchantStoreConflict
		}
		if e := marketLockUsers(tx, o.SellerID); e != nil {
			return e
		}
		if o.FeeHeld {
			if e := ApplyWalletQuotaDelta(tx, o.SellerID, o.FeeQuota); e != nil {
				return e
			}
			if e := storeTransfer(tx, id, "fee_release", 0, o.SellerID, o.FeeQuota); e != nil {
				return e
			}
			o.FeeHeld = false
		}
		o.Status = "cancelled"
		if providerClosed {
			o.ProviderClosureReference = closureReference
		}
		if expired {
			o.Status = "expired"
		}
		if e := tx.Save(o).Error; e != nil {
			return e
		}
		if e := tx.Model(&MerchantStoreStock{}).Where("order_id = ? AND state = ?", id, "reserved").Updates(map[string]any{"state": "available", "order_id": ""}).Error; e != nil {
			return e
		}
		sellerID = o.SellerID
		return storeEvent(tx, actor, id, o.Status)
	})
	if e == nil {
		marketInvalidate(sellerID)
	}
	return e
}
func CancelMerchantStoreOrder(actor int, id string) error {
	return closeMerchantStoreOrder(id, actor, false, false, "")
}
func ExpireMerchantStoreOrders(limit int) (int, error) {
	_, limit = storePage(0, limit)
	var rows []MerchantStoreOrder
	if e := DB.Where("status = ? AND expires_at <= ?", "pending", common.GetTimestamp()).Order("expires_at ASC").Limit(limit).Find(&rows).Error; e != nil {
		return 0, e
	}
	n := 0
	for _, o := range rows {
		release := false
		e := storeOrderTx(o.ID, func(tx *gorm.DB, current *MerchantStoreOrder) error {
			if current.ProviderTradeID != "" || current.VerifiedPaymentIssueAt > 0 {
				return ErrMerchantStoreConflict
			}
			if current.Status != "pending" || current.ExpiresAt > common.GetTimestamp() {
				return ErrMerchantStoreConflict
			}
			if current.GatewaySnapshot != "" || current.ProviderSessionID != "" {
				return tx.Model(current).Update("status", "reconciliation_pending").Error
			}
			release = true
			return nil
		})
		if e != nil {
			if errors.Is(e, ErrMerchantStoreConflict) {
				continue
			}
			return n, e
		}
		if release {
			if e = closeMerchantStoreOrder(o.ID, 0, true, false, ""); e != nil {
				if errors.Is(e, ErrMerchantStoreConflict) {
					continue
				}
				return n, e
			}
		}
		n++
	}
	return n, nil
}
func GetMerchantStoreOrder(actor int, id string) (*MerchantStoreOrder, error) {
	if _, e := storeUser(DB, actor, common.RoleCommonUser); e != nil {
		return nil, e
	}
	var o MerchantStoreOrder
	if e := DB.Where("id = ? AND (buyer_id = ? OR seller_id = ?)", id, actor, actor).First(&o).Error; e != nil {
		return nil, e
	}
	if actor != o.BuyerID {
		o.CheckoutURL = ""
	}
	o.PaymentIssued = o.GatewaySnapshot != "" || o.ProviderSessionID != ""
	if e := storeOrderEmailViews(DB, actor, []*MerchantStoreOrder{&o}); e != nil {
		return nil, e
	}
	return &o, nil
}
func ListMerchantStoreOrders(actor int, seller bool, offset, limit int) ([]MerchantStoreOrder, error) {
	if _, e := storeUser(DB, actor, common.RoleCommonUser); e != nil {
		return nil, e
	}
	offset, limit = storePage(offset, limit)
	field := "buyer_id"
	if seller {
		field = "seller_id"
	}
	var rows []MerchantStoreOrder
	e := DB.Where(field+" = ?", actor).Order("created_at DESC,id ASC").Offset(offset).Limit(limit).Find(&rows).Error
	for i := range rows {
		rows[i].PaymentIssued = rows[i].GatewaySnapshot != "" || rows[i].ProviderSessionID != ""
	}
	if seller {
		for i := range rows {
			rows[i].CheckoutURL = ""
		}
	}
	if e == nil && !seller {
		views := make([]*MerchantStoreOrder, len(rows))
		for i := range rows {
			views[i] = &rows[i]
		}
		e = storeOrderEmailViews(DB, actor, views)
	}
	return rows, e
}
func GetMerchantStoreOrderPickupToken(buyerID int, id string) (string, error) {
	o, e := GetMerchantStoreOrder(buyerID, id)
	if e != nil {
		return "", e
	}
	if o.BuyerID != buyerID || o.Status != "paid" {
		return "", ErrMerchantStoreDenied
	}
	return storeDecrypt("pickup", o.ID, o.PickupTokenCiphertext)
}
func storeTokenValid(token string) bool {
	if len(token) != 43 {
		return false
	}
	for _, r := range token {
		if !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}
func InspectMerchantStoreClaim(token string) (*MerchantStoreClaimMetadata, error) {
	if !storeTokenValid(token) {
		return nil, ErrMerchantStoreDenied
	}
	var o MerchantStoreOrder
	if e := DB.Where("pickup_token_hash = ? AND status = ?", storeHash(token), "paid").First(&o).Error; e != nil {
		return nil, ErrMerchantStoreDenied
	}
	return &MerchantStoreClaimMetadata{Status: o.Status, OrderID: o.ID, ProductTitle: o.ProductTitle, DeliveryTemplate: o.DeliveryTemplate, Quantity: o.Quantity, PickupLoginRequired: o.PickupLoginRequired, PickupCodeRequired: o.PickupCodeRequired || o.PickupCodeHash != ""}, nil
}
func ClaimMerchantStoreOrder(token, code string, buyerID int) (*MerchantStoreClaim, error) {
	return ClaimMerchantStoreOrderWithAuthorization(token, code, buyerID, nil)
}

// ClaimMerchantStoreOrderWithAuthorization lets a server-side, order-scoped
// credential be rechecked inside the delivery transaction before any stock is
// decrypted. The callback must never be supplied or selected by a client.
func ClaimMerchantStoreOrderWithAuthorization(token, code string, buyerID int, authorize func(*gorm.DB, *MerchantStoreOrder) error) (*MerchantStoreClaim, error) {
	if !storeTokenValid(token) || len(code) > 72 {
		return nil, ErrMerchantStoreDenied
	}
	var lookup MerchantStoreOrder
	if e := DB.Where("pickup_token_hash = ?", storeHash(token)).First(&lookup).Error; e != nil {
		return nil, ErrMerchantStoreDenied
	}
	var result MerchantStoreClaim
	e := storeOrderTx(lookup.ID, func(tx *gorm.DB, o *MerchantStoreOrder) error {
		if o.Status != "paid" || o.PickupTokenHash != storeHash(token) {
			return ErrMerchantStoreDenied
		}
		if authorize != nil {
			if e := authorize(tx, o); e != nil {
				return e
			}
		}
		if o.PickupLoginRequired {
			if buyerID != o.BuyerID {
				return ErrMerchantStoreDenied
			}
			if _, e := storeUser(tx, buyerID, common.RoleCommonUser); e != nil {
				return e
			}
		}
		if (o.PickupCodeRequired || o.PickupCodeHash != "") && bcrypt.CompareHashAndPassword([]byte(o.PickupCodeHash), []byte(code)) != nil {
			return ErrMerchantStoreDenied
		}
		var rows []MerchantStoreStock
		if e := tx.Where("order_id = ? AND state = ?", o.ID, "delivered").Order("position ASC,id ASC").Find(&rows).Error; e != nil {
			return e
		}
		if len(rows) != o.Quantity {
			return ErrMerchantStoreConflict
		}
		result = MerchantStoreClaim{OrderID: o.ID, ProductTitle: o.ProductTitle, DeliveryTemplate: o.DeliveryTemplate, Items: make([]string, 0, len(rows))}
		for _, row := range rows {
			value, e := storeDecrypt("stock", row.ProductID+":"+row.ID, row.Ciphertext)
			if e != nil {
				return e
			}
			result.Items = append(result.Items, value)
		}
		if o.ClaimedAt == 0 {
			o.ClaimedAt = common.GetTimestamp()
			return tx.Model(o).Update("claimed_at", o.ClaimedAt).Error
		}
		return nil
	})
	return &result, e
}

// Payment adapters may release issued orders only after proving the provider
// closed the session without payment. A browser request cannot call this.
func ConfirmMerchantStoreOrderPaymentClosed(id, closureReference string) error {
	if closureReference == "" || len(closureReference) > 128 {
		return ErrMerchantStoreInput
	}
	return closeMerchantStoreOrder(id, 0, false, true, closureReference)
}

// This scanner returns only issued provider sessions with authoritative expiry.
// Epay has no trustworthy cancellation evidence and is never reclaimed by time.
func DueMerchantStorePaymentOrders(ctx context.Context, now int64, limit int) ([]MerchantStoreOrder, error) {
	_, limit = storePage(0, limit)
	var rows []MerchantStoreOrder
	e := DB.WithContext(ctx).Where("status IN ? AND gateway_snapshot <> ? AND payment_method IN ? AND provider_checkout_expires_at > ? AND provider_checkout_expires_at <= ? AND (payment_checked_at = ? OR payment_checked_at <= ?)", []string{"pending", "reconciliation_pending"}, "", []string{"platform:waffo_pancake", "external:waffo_pancake"}, 0, now-900, 0, now-900).Order("provider_checkout_expires_at ASC,id ASC").Limit(limit).Find(&rows).Error
	return rows, e
}
func MarkMerchantStorePaymentChecked(id string, now int64, code string) error {
	if len(code) > 64 {
		return ErrMerchantStoreInput
	}
	for _, r := range code {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_') {
			return ErrMerchantStoreInput
		}
	}
	return DB.Model(&MerchantStoreOrder{}).Where("id = ? AND status IN ?", id, []string{"pending", "reconciliation_pending"}).Updates(map[string]any{"payment_checked_at": now, "payment_check_error": code}).Error
}

func storePaymentScopeValid(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, r := range value {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}
func storePaymentReceiptID(o *MerchantStoreOrder, receipt string) string {
	scopeSeller := o.SellerID
	if strings.HasPrefix(o.PaymentMethod, "platform:") {
		scopeSeller = 0
	}
	scope := fmtStoreActor(scopeSeller)
	if o.PaymentScopeHash != "" {
		scope += ":" + o.PaymentScopeHash
	}
	return storeHash(o.PaymentMethod + ":" + scope + ":" + receipt)
}
func storeReservePaymentReceipt(tx *gorm.DB, o *MerchantStoreOrder, receipt string) error {
	id := storePaymentReceiptID(o, receipt)
	var row MerchantStorePaymentReceipt
	e := tx.First(&row, "id = ?", id).Error
	if e == nil {
		if row.OrderID != o.ID {
			return ErrMerchantStoreConflict
		}
		return nil
	}
	if !errors.Is(e, gorm.ErrRecordNotFound) {
		return e
	}
	// The unique key arbitrates concurrent transactions on different products.
	// Waiting for a competing insertion cannot abort this transaction with a raw
	// constraint error; read committed then checks its authoritative ownership.
	result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&MerchantStorePaymentReceipt{ID: id, OrderID: o.ID, CreatedAt: common.GetTimestamp()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 1 {
		return nil
	}
	if e = tx.First(&row, "id = ?", id).Error; e != nil {
		return e
	}
	if row.OrderID != o.ID {
		return ErrMerchantStoreConflict
	}
	return nil
}
func storePaymentIssueCodeValid(code string) bool {
	switch code {
	case "settlement_unavailable", "recipient_unavailable", "seller_unavailable", "wallet_bounds", "stock_unavailable", "verified_payment_after_closed", "settlement_conflict":
		return true
	}
	return false
}

// Only a trusted adapter after signature and exact account/order/currency/amount
// verification may preserve this evidence. It makes no financial or inventory
// changes and can record failures even when the original merchant disappeared.
func RecordMerchantStoreVerifiedPaymentIssue(id, receipt, code string) error {
	if receipt == "" || len(receipt) > 128 || !storePaymentIssueCodeValid(code) {
		return ErrMerchantStoreInput
	}
	var lookup MerchantStoreOrder
	if e := DB.First(&lookup, "id = ?", id).Error; e != nil {
		return e
	}
	return marketTransaction(DB, func(tx *gorm.DB) error {
		// This evidence changes paid sales usage. Match checkout's product->order
		// lock order, while preserving real payment evidence if a product vanished.
		var product MerchantStoreProduct
		if e := lockForUpdate(tx).First(&product, "id = ?", lookup.ProductID).Error; e != nil && !errors.Is(e, gorm.ErrRecordNotFound) {
			return e
		}
		var o MerchantStoreOrder
		if e := lockForUpdate(tx).First(&o, "id = ?", id).Error; e != nil {
			return e
		}
		if o.PaymentMethod == "balance" || o.AmountMinor <= 0 {
			return ErrMerchantStoreConflict
		}
		if o.ProviderTradeID != "" && o.ProviderTradeID != receipt {
			return ErrMerchantStoreConflict
		}
		if o.Status == "paid" {
			if o.ProviderTradeID == receipt {
				return nil
			}
			return ErrMerchantStoreConflict
		}
		if e := storeReservePaymentReceipt(tx, &o, receipt); e != nil {
			return e
		}
		if o.Status == "cancelled" || o.Status == "expired" {
			code = "verified_payment_after_closed"
		}
		if o.VerifiedPaymentIssueAt == 0 {
			o.PaymentIssueOriginalStatus = o.Status
			o.VerifiedPaymentIssueAt = common.GetTimestamp()
		}
		o.ProviderTradeID = receipt
		o.PaymentIssueCode = code
		o.Status = "reconciliation_pending"
		return tx.Save(&o).Error
	})
}
