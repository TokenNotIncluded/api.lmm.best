package model

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/service/herosms"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

const (
	HeroSMSSMSOrderStatusPendingProvider = "pending_provider"
	HeroSMSSMSOrderStatusPurchaseUnknown = "purchase_unknown"
	HeroSMSSMSOrderStatusActive          = "active"
	HeroSMSSMSOrderStatusCompleted       = "completed"
	HeroSMSSMSOrderStatusCancelled       = "cancelled"
	HeroSMSSMSOrderStatusFailed          = "failed"

	HeroSMSSMSLedgerReserve = "reserve"
	HeroSMSSMSLedgerRefund  = "refund"
	HeroSMSSMSTaskType      = "hero_sms_sms_reconciliation"

	heroSMSSMSQuoteTTL      = 2 * time.Minute
	heroSMSSMSUnknownWindow = 15 * time.Minute
)

type HeroSMSSMSOrder struct {
	ID                         string  `json:"id" gorm:"primaryKey;size:64"`
	UserID                     int     `json:"user_id" gorm:"index;not null"`
	IdempotencyKeyHash         string  `json:"-" gorm:"size:64;not null;uniqueIndex:idx_hero_sms_sms_idempotency"`
	RequestPayloadHash         string  `json:"-" gorm:"size:64;not null"`
	CountryID                  int     `json:"country_id" gorm:"index;not null"`
	Service                    string  `json:"service" gorm:"size:64;index;not null"`
	Operator                   string  `json:"operator" gorm:"size:64"`
	Status                     string  `json:"status" gorm:"size:32;index;not null"`
	PriceMultiplier            string  `json:"price_multiplier" gorm:"size:32;not null"`
	ProviderPriceCNY           string  `json:"provider_price_cny" gorm:"size:64;not null"`
	CustomerPriceUSD           string  `json:"customer_price_usd" gorm:"size:64;not null"`
	ReservedQuota              int     `json:"-" gorm:"not null"`
	ChargeQuota                int     `json:"charge_quota" gorm:"not null"`
	RefundedQuota              int     `json:"refunded_quota" gorm:"not null;default:0"`
	ProviderID                 *string `json:"provider_id" gorm:"size:128;uniqueIndex"`
	ProviderCurrencyCode       int     `json:"provider_currency_code"`
	PhoneCiphertext            string  `json:"-" gorm:"type:text"`
	CodeCiphertext             string  `json:"-" gorm:"type:text"`
	MessageCiphertext          string  `json:"-" gorm:"type:text"`
	ProviderSnapshotCiphertext string  `json:"-" gorm:"type:text"`
	LastErrorCode              string  `json:"last_error_code" gorm:"size:64"`
	LastErrorMessage           string  `json:"last_error_message" gorm:"type:text"`
	ProviderRequestStartedAt   int64   `json:"provider_request_started_at" gorm:"index"`
	CompletedAt                *int64  `json:"completed_at"`
	CreatedAt                  int64   `json:"created_at" gorm:"index"`
	UpdatedAt                  int64   `json:"updated_at"`
}

type HeroSMSSMSQuotaLedger struct {
	ID             uint   `json:"id" gorm:"primaryKey"`
	UserID         int    `json:"user_id" gorm:"index;not null"`
	OrderID        string `json:"order_id" gorm:"size:64;index;not null"`
	EntryType      string `json:"entry_type" gorm:"size:32;index;not null"`
	AmountQuota    int    `json:"amount_quota" gorm:"not null"`
	IdempotencyKey string `json:"idempotency_key" gorm:"size:191;uniqueIndex;not null"`
	CreatedAt      int64  `json:"created_at" gorm:"index"`
}

func (HeroSMSSMSOrder) TableName() string { return "hero_sms_sms_orders" }

func (HeroSMSSMSQuotaLedger) TableName() string { return "hero_sms_sms_quota_ledgers" }

func (order *HeroSMSSMSOrder) BeforeCreate(_ *gorm.DB) error {
	if strings.TrimSpace(order.ID) == "" {
		order.ID = "hssms_" + common.GetUUID()
	}
	now := time.Now().Unix()
	if order.CreatedAt == 0 {
		order.CreatedAt = now
	}
	if order.UpdatedAt == 0 {
		order.UpdatedAt = now
	}
	return nil
}

func (ledger *HeroSMSSMSQuotaLedger) BeforeCreate(_ *gorm.DB) error {
	if ledger.CreatedAt == 0 {
		ledger.CreatedAt = time.Now().Unix()
	}
	return nil
}

type HeroSMSSMSCountryView struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type HeroSMSSMSServiceView struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

type HeroSMSSMSOfferView struct {
	ID               string `json:"id"`
	CountryID        int    `json:"country_id"`
	Service          string `json:"service"`
	Operator         string `json:"operator"`
	Inventory        int    `json:"inventory"`
	ProviderPriceCNY string `json:"provider_price_cny"`
	CustomerPriceUSD string `json:"customer_price_usd"`
	ChargeQuota      int    `json:"charge_quota"`
	PriceMultiplier  string `json:"price_multiplier"`
}

type HeroSMSSMSPurchaseRequest struct {
	OfferID string `json:"offer_id"`
}

type HeroSMSSMSOrderView struct {
	ID               string  `json:"id"`
	CountryID        int     `json:"country_id"`
	Service          string  `json:"service"`
	Operator         string  `json:"operator"`
	Status           string  `json:"status"`
	ProviderPriceCNY string  `json:"provider_price_cny"`
	CustomerPriceUSD string  `json:"customer_price_usd"`
	ChargeQuota      int     `json:"charge_quota"`
	RefundedQuota    int     `json:"refunded_quota"`
	ProviderID       *string `json:"provider_id"`
	PhoneNumber      string  `json:"phone_number"`
	Code             string  `json:"code"`
	Message          string  `json:"message"`
	LastErrorCode    string  `json:"last_error_code"`
	LastErrorMessage string  `json:"last_error_message"`
	CreatedAt        int64   `json:"created_at"`
	UpdatedAt        int64   `json:"updated_at"`
}

type HeroSMSSMSOrderPage struct {
	Items []HeroSMSSMSOrderView `json:"items"`
	Page  int                   `json:"page"`
	Size  int                   `json:"size"`
	Total int64                 `json:"total"`
}

type heroSMSSMSQuoteToken struct {
	CountryID  int    `json:"country_id"`
	Service    string `json:"service"`
	Operator   string `json:"operator"`
	CostCNY    string `json:"cost_cny"`
	Multiplier string `json:"multiplier"`
	IssuedAt   int64  `json:"issued_at"`
}

// pi-lens-ignore: go-bare-error
func heroSMSSMSClient() (herosms.SMSClient, error) {
	if !heroSMSPurchasingEnabled() || !heroSMSSMSPurchasingEnabled() {
		return nil, newHeroSMSError(http.StatusServiceUnavailable, "NOT_CONFIGURED", "HeroSMS SMS purchasing is disabled")
	}
	return heroSMSSMSOperationsClient()
}

func heroSMSSMSOperationsClient() (herosms.SMSClient, error) {
	client, err := heroSMSOperationsClient()
	if err != nil {
		return nil, err
	}
	smsClient, ok := client.(herosms.SMSClient)
	if !ok {
		return nil, newHeroSMSError(http.StatusServiceUnavailable, "NOT_CONFIGURED", "HeroSMS SMS client is unavailable")
	}
	return smsClient, nil
}

func GetHeroSMSSMSCountries(ctx context.Context) ([]HeroSMSSMSCountryView, error) {
	client, err := heroSMSSMSClient()
	if err != nil {
		return nil, err
	}
	countries, err := client.ListSMSCountries(ctx)
	if err != nil {
		return nil, mapHeroSMSProviderError(err)
	}
	views := make([]HeroSMSSMSCountryView, 0, len(countries))
	for _, country := range countries {
		if !country.Visible || strings.TrimSpace(country.Name) == "" {
			continue
		}
		views = append(views, HeroSMSSMSCountryView{ID: country.ID, Name: country.Name})
	}
	sort.Slice(views, func(i, j int) bool { return views[i].Name < views[j].Name })
	return views, nil
}

func GetHeroSMSSMSServices(ctx context.Context) ([]HeroSMSSMSServiceView, error) {
	client, err := heroSMSSMSClient()
	if err != nil {
		return nil, err
	}
	services, err := client.ListSMSServices(ctx)
	if err != nil {
		return nil, mapHeroSMSProviderError(err)
	}
	views := make([]HeroSMSSMSServiceView, 0, len(services))
	for _, service := range services {
		if strings.TrimSpace(service.Code) == "" || strings.TrimSpace(service.Name) == "" {
			continue
		}
		views = append(views, HeroSMSSMSServiceView{Code: service.Code, Name: service.Name})
	}
	sort.Slice(views, func(i, j int) bool { return views[i].Name < views[j].Name })
	return views, nil
}

func GetHeroSMSSMSOffer(ctx context.Context, countryID int, service string, operator string) (*HeroSMSSMSOfferView, error) {
	client, err := heroSMSSMSClient()
	if err != nil {
		return nil, err
	}
	service = strings.TrimSpace(service)
	operator = strings.TrimSpace(operator)
	if countryID < 0 || service == "" || len(service) > 64 || len(operator) > 64 {
		return nil, newHeroSMSError(http.StatusBadRequest, "INVALID_REQUEST", "invalid HeroSMS SMS offer request")
	}
	offer, err := client.GetSMSOffer(ctx, countryID, service)
	if err != nil {
		return nil, mapHeroSMSProviderError(err)
	}
	multiplier, err := heroSMSMultiplierDecimal()
	if err != nil {
		return nil, err
	}
	customerPrice := offer.Price.Mul(multiplier)
	chargeQuota, err := heroSMSChargeQuota(customerPrice)
	if err != nil {
		return nil, err
	}
	quoteID, err := encodeHeroSMSSMSQuote(heroSMSSMSQuoteToken{
		CountryID:  countryID,
		Service:    service,
		Operator:   operator,
		CostCNY:    offer.Price.String(),
		Multiplier: multiplier.String(),
		IssuedAt:   time.Now().Unix(),
	})
	if err != nil {
		return nil, newHeroSMSError(http.StatusServiceUnavailable, "NOT_CONFIGURED", "HeroSMS encryption is unavailable")
	}
	return &HeroSMSSMSOfferView{
		ID:               quoteID,
		CountryID:        countryID,
		Service:          service,
		Operator:         operator,
		Inventory:        offer.Count,
		ProviderPriceCNY: offer.Price.String(),
		CustomerPriceUSD: customerPrice.String(),
		ChargeQuota:      chargeQuota,
		PriceMultiplier:  multiplier.String(),
	}, nil
}

func CreateHeroSMSSMSOrder(ctx context.Context, userID int, request HeroSMSSMSPurchaseRequest, idempotencyKey string) (*HeroSMSSMSOrderView, int, int, error) {
	trimmedKey := strings.TrimSpace(idempotencyKey)
	if userID <= 0 || trimmedKey == "" || len(trimmedKey) > 128 || strings.TrimSpace(request.OfferID) == "" {
		return nil, 0, 0, newHeroSMSError(http.StatusBadRequest, "INVALID_REQUEST", "invalid HeroSMS SMS purchase request")
	}
	idempotencyHash := sha256Hex(trimmedKey)
	payloadBytes, _ := json.Marshal(request)
	payloadHash := sha256Hex(string(payloadBytes))
	var existing HeroSMSSMSOrder
	if err := DB.Where("user_id = ? AND idempotency_key_hash = ?", userID, idempotencyHash).First(&existing).Error; err == nil {
		if existing.RequestPayloadHash != payloadHash {
			return nil, 0, 0, newHeroSMSError(http.StatusConflict, "IDEMPOTENCY_MISMATCH", "idempotent request payload mismatch")
		}
		view, viewErr := heroSMSSMSOrderView(&existing)
		return view, getUserQuotaValue(userID), statusForHeroSMSSMSOrder(existing.Status), viewErr
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, 0, 0, err
	}

	client, err := heroSMSSMSClient()
	if err != nil {
		return nil, 0, 0, err
	}
	quote, err := decodeHeroSMSSMSQuote(request.OfferID)
	if err != nil || time.Since(time.Unix(quote.IssuedAt, 0)) > heroSMSSMSQuoteTTL {
		return nil, 0, 0, newHeroSMSError(http.StatusConflict, "PRICE_CHANGED", "refresh the HeroSMS SMS quote")
	}
	currentMultiplier, err := heroSMSMultiplierDecimal()
	if err != nil {
		return nil, 0, 0, err
	}
	if currentMultiplier.String() != quote.Multiplier {
		return nil, 0, 0, newHeroSMSError(http.StatusConflict, "PRICE_CHANGED", "HeroSMS price multiplier changed")
	}
	providerOffer, err := client.GetSMSOffer(ctx, quote.CountryID, quote.Service)
	if err != nil {
		return nil, 0, 0, mapHeroSMSProviderError(err)
	}
	reservedCost, err := decimal.NewFromString(quote.CostCNY)
	if err != nil || !providerOffer.Price.Equal(reservedCost) || providerOffer.Count <= 0 {
		return nil, 0, 0, newHeroSMSError(http.StatusConflict, "PRICE_CHANGED", "HeroSMS SMS price or inventory changed")
	}
	customerPrice := reservedCost.Mul(currentMultiplier)
	chargeQuota, err := heroSMSChargeQuota(customerPrice)
	if err != nil {
		return nil, 0, 0, err
	}

	releaseLease, err := acquireHeroSMSProviderPurchaseLease(ctx)
	if err != nil {
		return nil, 0, 0, err
	}
	defer releaseLease()
	activeBefore, err := client.ListActiveSMSActivations(ctx)
	if err != nil {
		return nil, 0, 0, mapHeroSMSProviderError(err)
	}
	snapshot, err := encryptHeroSMSSMSSnapshot(activeBefore)
	if err != nil {
		return nil, 0, 0, newHeroSMSError(http.StatusServiceUnavailable, "NOT_CONFIGURED", "HeroSMS encryption is unavailable")
	}
	order := HeroSMSSMSOrder{
		UserID:                     userID,
		IdempotencyKeyHash:         idempotencyHash,
		RequestPayloadHash:         payloadHash,
		CountryID:                  quote.CountryID,
		Service:                    quote.Service,
		Operator:                   quote.Operator,
		Status:                     HeroSMSSMSOrderStatusPendingProvider,
		PriceMultiplier:            currentMultiplier.String(),
		ProviderPriceCNY:           reservedCost.String(),
		CustomerPriceUSD:           customerPrice.String(),
		ReservedQuota:              chargeQuota,
		ChargeQuota:                chargeQuota,
		ProviderSnapshotCiphertext: snapshot,
		LastErrorCode:              "PROVIDER_INTENT_PENDING",
		LastErrorMessage:           "provider purchase intent is reserved but not started",
		ProviderRequestStartedAt:   time.Now().Unix(),
	}
	newQuota, err := reserveHeroSMSSMSQuota(&order)
	if err != nil {
		return nil, 0, 0, err
	}
	if cacheErr := updateUserQuotaCache(userID, newQuota); cacheErr != nil {
		common.SysLog(fmt.Sprintf("HeroSMS SMS quota cache update failed: %T", cacheErr))
	}
	if err := DB.Model(&HeroSMSSMSOrder{}).Where("id = ?", order.ID).Updates(map[string]any{
		"status":             HeroSMSSMSOrderStatusPurchaseUnknown,
		"last_error_code":    "PROVIDER_ATTEMPT_STARTED",
		"last_error_message": "provider purchase attempt may have started",
		"updated_at":         time.Now().Unix(),
	}).Error; err != nil {
		_ = failHeroSMSSMSOrder(order.ID, "INTERNAL_ERROR", "failed to persist provider request intent")
		return nil, 0, 0, err
	}
	activation, purchaseErr := client.PurchaseSMSActivation(ctx, herosms.SMSPurchaseRequest{
		CountryID: quote.CountryID,
		Service:   quote.Service,
		Operator:  quote.Operator,
		MaxPrice:  reservedCost,
	})
	if purchaseErr != nil {
		if errors.Is(purchaseErr, herosms.ErrUpstreamTimeout) || errors.Is(purchaseErr, herosms.ErrUpstreamBusy) {
			view, reconcileErr := reconcileHeroSMSSMSOrder(ctx, client, order.ID)
			if reconcileErr != nil {
				return nil, newQuota, 0, reconcileErr
			}
			return view, newQuota, statusForHeroSMSSMSOrder(view.Status), nil
		}
		mapped := mapHeroSMSProviderError(purchaseErr)
		if heroErr, ok := mapped.(*HeroSMSError); ok {
			_ = failHeroSMSSMSOrder(order.ID, heroErr.Code, heroErr.Message)
		} else {
			_ = failHeroSMSSMSOrder(order.ID, "UPSTREAM_BUSY", "HeroSMS SMS purchase failed")
		}
		return nil, getUserQuotaValue(userID), 0, mapped
	}
	view, newQuota, err := completeHeroSMSSMSOrder(ctx, client, order.ID, activation)
	if err != nil {
		return nil, getUserQuotaValue(userID), 0, err
	}
	return view, newQuota, http.StatusCreated, nil
}

func reserveHeroSMSSMSQuota(order *HeroSMSSMSOrder) (int, error) {
	newQuota := 0
	err := DB.Transaction(func(tx *gorm.DB) error {
		var user User
		if err := lockForUpdate(tx).Select("id", "quota").Where("id = ?", order.UserID).First(&user).Error; err != nil {
			return err
		}
		if user.Quota < order.ChargeQuota {
			return newHeroSMSError(http.StatusPaymentRequired, "INSUFFICIENT_BALANCE", "insufficient quota balance")
		}
		update := tx.Model(&User{}).Where("id = ? AND quota >= ?", order.UserID, order.ChargeQuota).UpdateColumn("quota", gorm.Expr("quota - ?", order.ChargeQuota))
		if update.Error != nil || update.RowsAffected != 1 {
			return newHeroSMSError(http.StatusPaymentRequired, "INSUFFICIENT_BALANCE", "insufficient quota balance")
		}
		if err := tx.Create(order).Error; err != nil {
			return err
		}
		if err := tx.Create(&HeroSMSSMSQuotaLedger{
			UserID:         order.UserID,
			OrderID:        order.ID,
			EntryType:      HeroSMSSMSLedgerReserve,
			AmountQuota:    -order.ChargeQuota,
			IdempotencyKey: "hero_sms:sms:reserve:" + order.ID,
		}).Error; err != nil {
			return err
		}
		newQuota = user.Quota - order.ChargeQuota
		return nil
	})
	return newQuota, err
}

func completeHeroSMSSMSOrder(ctx context.Context, client herosms.SMSClient, orderID string, activation *herosms.SMSActivation) (*HeroSMSSMSOrderView, int, error) {
	if activation == nil || strings.TrimSpace(activation.ID) == "" || strings.TrimSpace(activation.PhoneNumber) == "" {
		_ = failHeroSMSSMSOrder(orderID, "BAD_UPSTREAM_RESPONSE", "HeroSMS returned an invalid SMS activation")
		return nil, 0, newHeroSMSError(http.StatusBadGateway, "BAD_UPSTREAM_RESPONSE", "HeroSMS returned an invalid SMS activation")
	}
	phoneCiphertext, err := encryptHeroSMSPayload(activation.PhoneNumber)
	if err != nil {
		_ = client.SetSMSActivationStatus(ctx, activation.ID, 8)
		_ = failHeroSMSSMSOrder(orderID, "NOT_CONFIGURED", "HeroSMS encryption is unavailable")
		return nil, 0, newHeroSMSError(http.StatusServiceUnavailable, "NOT_CONFIGURED", "HeroSMS encryption is unavailable")
	}

	var newQuota int
	userID := 0
	err = DB.Transaction(func(tx *gorm.DB) error {
		var order HeroSMSSMSOrder
		if err := lockForUpdate(tx).Where("id = ?", orderID).First(&order).Error; err != nil {
			return err
		}
		userID = order.UserID
		if order.Status == HeroSMSSMSOrderStatusActive || order.Status == HeroSMSSMSOrderStatusCompleted {
			newQuota = getUserQuotaValue(order.UserID)
			return nil
		}
		multiplier, err := decimal.NewFromString(order.PriceMultiplier)
		if err != nil {
			return err
		}
		actualCharge, err := heroSMSChargeQuota(activation.ActivationCost.Mul(multiplier))
		if err != nil {
			return err
		}
		if actualCharge > order.ChargeQuota {
			return newHeroSMSError(http.StatusConflict, "PRICE_CHANGED", "HeroSMS SMS price exceeded the confirmed quote")
		}
		refund := order.ChargeQuota - actualCharge
		if refund > 0 {
			if err := tx.Model(&User{}).Where("id = ?", order.UserID).UpdateColumn("quota", gorm.Expr("quota + ?", refund)).Error; err != nil {
				return err
			}
			if err := tx.Create(&HeroSMSSMSQuotaLedger{
				UserID:         order.UserID,
				OrderID:        order.ID,
				EntryType:      HeroSMSSMSLedgerRefund,
				AmountQuota:    refund,
				IdempotencyKey: "hero_sms:sms:price_refund:" + order.ID,
			}).Error; err != nil {
				return err
			}
		}
		providerID := activation.ID
		now := time.Now().Unix()
		if err := tx.Model(&HeroSMSSMSOrder{}).Where("id = ?", order.ID).Updates(map[string]any{
			"status":                 HeroSMSSMSOrderStatusActive,
			"provider_id":            providerID,
			"provider_currency_code": activation.CurrencyCode,
			"phone_ciphertext":       phoneCiphertext,
			"provider_price_cny":     activation.ActivationCost.String(),
			"customer_price_usd":     activation.ActivationCost.Mul(multiplier).String(),
			"charge_quota":           actualCharge,
			"refunded_quota":         order.RefundedQuota + refund,
			"last_error_code":        "",
			"last_error_message":     "",
			"updated_at":             now,
		}).Error; err != nil {
			return err
		}
		var user User
		if err := tx.Select("quota").Where("id = ?", order.UserID).First(&user).Error; err != nil {
			return err
		}
		newQuota = user.Quota
		return nil
	})
	if err != nil {
		if heroErr, ok := err.(*HeroSMSError); ok && heroErr.Code == "PRICE_CHANGED" {
			_ = client.SetSMSActivationStatus(ctx, activation.ID, 8)
			_ = failHeroSMSSMSOrder(orderID, heroErr.Code, heroErr.Message)
		}
		return nil, 0, err
	}
	if cacheErr := updateUserQuotaCache(userID, newQuota); cacheErr != nil {
		common.SysLog(fmt.Sprintf("HeroSMS SMS quota cache update failed: %T", cacheErr))
	}
	view, err := GetHeroSMSSMSOrder(orderID, 0)
	return view, newQuota, err
}

// pi-lens-ignore: go-bare-error
func failHeroSMSSMSOrder(orderID string, code string, message string) error {
	return refundHeroSMSSMSOrder(orderID, HeroSMSSMSOrderStatusFailed, code, message)
}

func refundHeroSMSSMSOrder(orderID string, status string, code string, message string) error {
	userID := 0
	newQuota := 0
	err := DB.Transaction(func(tx *gorm.DB) error {
		var order HeroSMSSMSOrder
		if err := lockForUpdate(tx).Where("id = ?", orderID).First(&order).Error; err != nil {
			return err
		}
		userID = order.UserID
		refund := order.ReservedQuota - order.RefundedQuota
		if refund > 0 {
			if err := tx.Model(&User{}).Where("id = ?", order.UserID).UpdateColumn("quota", gorm.Expr("quota + ?", refund)).Error; err != nil {
				return err
			}
			if err := tx.Create(&HeroSMSSMSQuotaLedger{
				UserID:         order.UserID,
				OrderID:        order.ID,
				EntryType:      HeroSMSSMSLedgerRefund,
				AmountQuota:    refund,
				IdempotencyKey: "hero_sms:sms:refund:" + order.ID,
			}).Error; err != nil && !uniqueConstraintError(err) {
				return err
			}
		}
		now := time.Now().Unix()
		if err := tx.Model(&HeroSMSSMSOrder{}).Where("id = ?", order.ID).Updates(map[string]any{
			"status":             status,
			"refunded_quota":     order.RefundedQuota + refund,
			"last_error_code":    code,
			"last_error_message": message,
			"updated_at":         now,
		}).Error; err != nil {
			return err
		}
		var user User
		if err := tx.Select("quota").Where("id = ?", order.UserID).First(&user).Error; err != nil {
			return err
		}
		newQuota = user.Quota
		return nil
	})
	if err == nil && userID > 0 {
		if cacheErr := updateUserQuotaCache(userID, newQuota); cacheErr != nil {
			common.SysLog(fmt.Sprintf("HeroSMS SMS quota cache update failed: %T", cacheErr))
		}
	}
	return err
}

// pi-lens-ignore: go-bare-error
func RefreshHeroSMSSMSOrder(ctx context.Context, userID int, orderID string) (*HeroSMSSMSOrderView, error) {
	client, err := heroSMSSMSOperationsClient()
	if err != nil {
		return nil, err
	}
	order, err := getHeroSMSSMSOrder(userID, orderID)
	if err != nil {
		return nil, err
	}
	if order.Status == HeroSMSSMSOrderStatusPurchaseUnknown {
		return reconcileHeroSMSSMSOrder(ctx, client, order.ID)
	}
	if order.Status != HeroSMSSMSOrderStatusActive || order.ProviderID == nil {
		return heroSMSSMSOrderView(order)
	}
	status, err := client.GetSMSActivationStatus(ctx, *order.ProviderID)
	if err != nil {
		return nil, mapHeroSMSProviderError(err)
	}
	if status.Code == "" {
		return heroSMSSMSOrderView(order)
	}
	codeCiphertext, err := encryptHeroSMSPayload(status.Code)
	if err != nil {
		return nil, err
	}
	messageCiphertext, err := encryptHeroSMSPayload(status.Text)
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	if err := DB.Model(&HeroSMSSMSOrder{}).Where("id = ? AND status = ?", order.ID, HeroSMSSMSOrderStatusActive).Updates(map[string]any{
		"status":             HeroSMSSMSOrderStatusCompleted,
		"code_ciphertext":    codeCiphertext,
		"message_ciphertext": messageCiphertext,
		"completed_at":       now,
		"updated_at":         now,
	}).Error; err != nil {
		return nil, err
	}
	_ = client.SetSMSActivationStatus(ctx, *order.ProviderID, 6)
	return GetHeroSMSSMSOrder(order.ID, userID)
}

// pi-lens-ignore: go-bare-error
func CancelHeroSMSSMSOrder(ctx context.Context, userID int, orderID string) (*HeroSMSSMSOrderView, int, error) {
	client, err := heroSMSSMSOperationsClient()
	if err != nil {
		return nil, 0, err
	}
	order, err := getHeroSMSSMSOrder(userID, orderID)
	if err != nil {
		return nil, 0, err
	}
	if order.Status == HeroSMSSMSOrderStatusCompleted || order.Status == HeroSMSSMSOrderStatusCancelled || order.Status == HeroSMSSMSOrderStatusFailed {
		view, viewErr := heroSMSSMSOrderView(order)
		return view, getUserQuotaValue(userID), viewErr
	}
	if order.ProviderID == nil || strings.TrimSpace(*order.ProviderID) == "" {
		return nil, 0, newHeroSMSError(http.StatusConflict, "RECONCILING", "wait for provider purchase reconciliation before cancelling")
	}
	if err := client.SetSMSActivationStatus(ctx, *order.ProviderID, 8); err != nil {
		return nil, 0, mapHeroSMSProviderError(err)
	}
	if err := refundHeroSMSSMSOrder(order.ID, HeroSMSSMSOrderStatusCancelled, "USER_CANCELLED", "activation cancelled before receiving a code"); err != nil {
		return nil, 0, err
	}
	view, err := GetHeroSMSSMSOrder(order.ID, userID)
	return view, getUserQuotaValue(userID), err
}

// pi-lens-ignore: go-bare-error
func GetHeroSMSSMSOrder(orderID string, userID int) (*HeroSMSSMSOrderView, error) {
	var order HeroSMSSMSOrder
	query := DB.Where("id = ?", strings.TrimSpace(orderID))
	if userID > 0 {
		query = query.Where("user_id = ?", userID)
	}
	if err := query.First(&order).Error; err != nil {
		return nil, err
	}
	return heroSMSSMSOrderView(&order)
}

// pi-lens-ignore: go-bare-error
func GetCurrentHeroSMSSMSOrder(ctx context.Context, userID int) (*HeroSMSSMSOrderView, error) {
	var order HeroSMSSMSOrder
	err := DB.Where("user_id = ? AND status IN ?", userID, []string{
		HeroSMSSMSOrderStatusPendingProvider,
		HeroSMSSMSOrderStatusPurchaseUnknown,
		HeroSMSSMSOrderStatusActive,
	}).Order("created_at DESC").First(&order).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return RefreshHeroSMSSMSOrder(ctx, userID, order.ID)
}

func ListHeroSMSSMSOrders(userID int, page int, size int) (*HeroSMSSMSOrderPage, error) {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}
	query := DB.Model(&HeroSMSSMSOrder{}).Where("user_id = ?", userID)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, err
	}
	var orders []HeroSMSSMSOrder
	if err := query.Order("created_at DESC").Offset((page - 1) * size).Limit(size).Find(&orders).Error; err != nil {
		return nil, err
	}
	views := make([]HeroSMSSMSOrderView, 0, len(orders))
	for index := range orders {
		view, err := heroSMSSMSOrderView(&orders[index])
		if err != nil {
			return nil, err
		}
		views = append(views, *view)
	}
	return &HeroSMSSMSOrderPage{Items: views, Page: page, Size: size, Total: total}, nil
}

func heroSMSSMSOrderView(order *HeroSMSSMSOrder) (*HeroSMSSMSOrderView, error) {
	view := &HeroSMSSMSOrderView{
		ID:               order.ID,
		CountryID:        order.CountryID,
		Service:          order.Service,
		Operator:         order.Operator,
		Status:           order.Status,
		ProviderPriceCNY: order.ProviderPriceCNY,
		CustomerPriceUSD: order.CustomerPriceUSD,
		ChargeQuota:      order.ChargeQuota,
		RefundedQuota:    order.RefundedQuota,
		ProviderID:       order.ProviderID,
		LastErrorCode:    order.LastErrorCode,
		LastErrorMessage: order.LastErrorMessage,
		CreatedAt:        order.CreatedAt,
		UpdatedAt:        order.UpdatedAt,
	}
	var err error
	if order.PhoneCiphertext != "" {
		view.PhoneNumber, err = decryptHeroSMSPayload(order.PhoneCiphertext)
		if err != nil {
			return nil, err
		}
	}
	if order.CodeCiphertext != "" {
		view.Code, err = decryptHeroSMSPayload(order.CodeCiphertext)
		if err != nil {
			return nil, err
		}
	}
	if order.MessageCiphertext != "" {
		view.Message, err = decryptHeroSMSPayload(order.MessageCiphertext)
		if err != nil {
			return nil, err
		}
	}
	return view, nil
}

func getHeroSMSSMSOrder(userID int, orderID string) (*HeroSMSSMSOrder, error) {
	var order HeroSMSSMSOrder
	if err := DB.Where("id = ? AND user_id = ?", strings.TrimSpace(orderID), userID).First(&order).Error; err != nil {
		return nil, err
	}
	return &order, nil
}

// pi-lens-ignore: go-bare-error
func reconcileHeroSMSSMSOrder(ctx context.Context, client herosms.SMSClient, orderID string) (*HeroSMSSMSOrderView, error) {
	var order HeroSMSSMSOrder
	if err := DB.Where("id = ?", orderID).First(&order).Error; err != nil {
		return nil, err
	}
	if order.Status != HeroSMSSMSOrderStatusPurchaseUnknown {
		return heroSMSSMSOrderView(&order)
	}
	activations, err := client.ListActiveSMSActivations(ctx)
	if err != nil {
		return nil, mapHeroSMSProviderError(err)
	}
	knownIDs, err := decryptHeroSMSSMSSnapshot(order.ProviderSnapshotCiphertext)
	if err != nil {
		return nil, err
	}
	candidates := make([]herosms.SMSActiveActivation, 0, 1)
	for _, activation := range activations {
		if _, known := knownIDs[activation.ID]; known {
			continue
		}
		if activation.Service == order.Service && activation.CountryCode == order.CountryID {
			candidates = append(candidates, activation)
		}
	}
	if len(candidates) == 1 {
		activation := &herosms.SMSActivation{
			ID:             candidates[0].ID,
			PhoneNumber:    candidates[0].PhoneNumber,
			ActivationCost: candidates[0].ActivationCost,
			CostValue:      candidates[0].CostValue,
			CurrencyCode:   candidates[0].CurrencyCode,
			CountryCode:    candidates[0].CountryCode,
			ActivationTime: candidates[0].ActivationTime,
		}
		view, _, completeErr := completeHeroSMSSMSOrder(ctx, client, order.ID, activation)
		return view, completeErr
	}
	if len(candidates) == 0 && time.Since(time.Unix(order.ProviderRequestStartedAt, 0)) >= heroSMSSMSUnknownWindow {
		if err := failHeroSMSSMSOrder(order.ID, "PROVIDER_NOT_FOUND", "provider purchase did not create an activation"); err != nil {
			return nil, err
		}
		return GetHeroSMSSMSOrder(order.ID, order.UserID)
	}
	if len(candidates) > 1 {
		_ = DB.Model(&HeroSMSSMSOrder{}).Where("id = ?", order.ID).Updates(map[string]any{
			"last_error_code":    "RECONCILIATION_AMBIGUOUS",
			"last_error_message": "multiple provider activations require manual reconciliation",
			"updated_at":         time.Now().Unix(),
		}).Error
	}
	return GetHeroSMSSMSOrder(order.ID, order.UserID)
}

func HasPendingHeroSMSSMSWork() (bool, error) {
	var count int64
	err := DB.Model(&HeroSMSSMSOrder{}).Where("status IN ?", []string{
		HeroSMSSMSOrderStatusPendingProvider,
		HeroSMSSMSOrderStatusPurchaseUnknown,
		HeroSMSSMSOrderStatusActive,
	}).Count(&count).Error
	return count > 0, err
}

func RunHeroSMSSMSReconciliationOnce(ctx context.Context, limit int) (int, error) {
	if limit < 1 || limit > 100 {
		limit = 20
	}
	client, err := heroSMSSMSOperationsClient()
	if err != nil {
		return 0, err
	}
	var orders []HeroSMSSMSOrder
	if err := DB.Where("status = ?", HeroSMSSMSOrderStatusPurchaseUnknown).Order("created_at ASC").Limit(limit).Find(&orders).Error; err != nil {
		return 0, err
	}
	processed := 0
	for index := range orders {
		if _, err := reconcileHeroSMSSMSOrder(ctx, client, orders[index].ID); err != nil {
			continue
		}
		processed++
	}
	return processed, nil
}

func encodeHeroSMSSMSQuote(token heroSMSSMSQuoteToken) (string, error) {
	payload, err := json.Marshal(token)
	if err != nil {
		return "", err
	}
	ciphertext, err := common.EncryptPersistentString("hero_sms.sms_quote", "HERO_SMS_ENCRYPTION_KEY", "CRYPTO_SECRET", string(payload))
	if err != nil {
		return "", err
	}
	return "hssq_" + base64.RawURLEncoding.EncodeToString([]byte(ciphertext)), nil
}

func decodeHeroSMSSMSQuote(value string) (*heroSMSSMSQuoteToken, error) {
	if !strings.HasPrefix(value, "hssq_") || len(value) > 2048 {
		return nil, errors.New("invalid SMS quote")
	}
	ciphertext, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, "hssq_"))
	if err != nil {
		return nil, err
	}
	plaintext, err := common.DecryptPersistentString("hero_sms.sms_quote", "HERO_SMS_ENCRYPTION_KEY", "CRYPTO_SECRET", string(ciphertext))
	if err != nil {
		return nil, err
	}
	var token heroSMSSMSQuoteToken
	if err := json.Unmarshal([]byte(plaintext), &token); err != nil {
		return nil, err
	}
	if token.CountryID < 0 || strings.TrimSpace(token.Service) == "" || token.IssuedAt <= 0 {
		return nil, errors.New("invalid SMS quote")
	}
	return &token, nil
}

// pi-lens-ignore: go-bare-error
func encryptHeroSMSSMSSnapshot(activations []herosms.SMSActiveActivation) (string, error) {
	ids := make([]string, 0, len(activations))
	for _, activation := range activations {
		if activation.ID != "" {
			ids = append(ids, activation.ID)
		}
	}
	sort.Strings(ids)
	payload, err := json.Marshal(ids)
	if err != nil {
		return "", err
	}
	return encryptHeroSMSPayload(string(payload))
}

func decryptHeroSMSSMSSnapshot(ciphertext string) (map[string]struct{}, error) {
	plaintext, err := decryptHeroSMSPayload(ciphertext)
	if err != nil {
		return nil, err
	}
	var ids []string
	if err := json.Unmarshal([]byte(plaintext), &ids); err != nil {
		return nil, err
	}
	result := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		result[id] = struct{}{}
	}
	return result, nil
}

func statusForHeroSMSSMSOrder(status string) int {
	if status == HeroSMSSMSOrderStatusActive || status == HeroSMSSMSOrderStatusCompleted {
		return http.StatusCreated
	}
	return http.StatusAccepted
}

func getUserQuotaValue(userID int) int {
	var quota int
	_ = DB.Model(&User{}).Select("quota").Where("id = ?", userID).Scan(&quota).Error
	return quota
}

func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func HeroSMSSMSPricingExplanation() string {
	return fmt.Sprintf("HeroSMS 1 CNY × %s = platform USD balance charge; quota conversion uses the platform 1:1 simplified balance model", heroSMSOptionValue(setting.HeroSMSOptionMultiplier, setting.HeroSMSPriceMultiplier))
}
