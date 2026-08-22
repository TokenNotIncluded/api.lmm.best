package model

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/service/herosms"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

const (
	HeroSMSCurrencyCode = 840

	HeroSMSEmailOrderStatusPendingProvider = "pending_provider"
	HeroSMSEmailOrderStatusCompleted       = "completed"
	HeroSMSEmailOrderStatusReconciling     = "reconciling"
	HeroSMSEmailOrderStatusPurchaseUnknown = "purchase_unknown"
	HeroSMSEmailOrderStatusFailed          = "failed"

	HeroSMSEmailActivationStatusPendingProvider = "pending_provider"
	HeroSMSEmailActivationStatusActive          = "active"
	HeroSMSEmailActivationStatusReconciling     = "reconciling"
	HeroSMSEmailActivationStatusCancelPending   = "cancel_pending"
	HeroSMSEmailActivationStatusCancelled       = "cancelled"
	HeroSMSEmailActivationStatusRefunded        = "refunded"

	HeroSMSEmailCancelReasonUser             = "user_cancel"
	HeroSMSEmailCancelReasonPriceChanged     = "price_changed"
	HeroSMSEmailCancelReasonCurrencyMismatch = "currency_mismatch"
	HeroSMSEmailCancelReasonBadUpstream      = "bad_upstream"

	HeroSMSEmailLedgerReserve = "reserve"
	HeroSMSEmailLedgerRefund  = "refund"

	HeroSMSEmailTaskType = "hero_sms_email_reconciliation"
)

type HeroSMSError struct {
	Status  int    `json:"-"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *HeroSMSError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	return e.Code
}

func NewHeroSMSError(status int, code string, message string) *HeroSMSError {
	return &HeroSMSError{Status: status, Code: code, Message: message}
}

func newHeroSMSError(status int, code string, message string) *HeroSMSError {
	return NewHeroSMSError(status, code, message)
}

type HeroSMSSettingsView struct {
	Enabled          bool   `json:"enabled"`
	APIKeyConfigured bool   `json:"api_key_configured"`
	Currency         string `json:"currency"`
	CurrencyCode     int    `json:"currency_code"`
	PriceMultiplier  string `json:"price_multiplier"`
}

type HeroSMSSettingsUpdate struct {
	Enabled         *bool  `json:"enabled"`
	APIKey          string `json:"api_key"`
	PriceMultiplier string `json:"price_multiplier"`
}

type HeroSMSEmailProduct struct {
	DomainID         string `json:"domain_id"`
	Site             string `json:"site"`
	Domain           string `json:"domain"`
	Stock            int    `json:"stock"`
	UpstreamCostUSD  string `json:"upstream_cost_usd"`
	PriceMultiplier  string `json:"price_multiplier"`
	CustomerPriceUSD string `json:"customer_price_usd"`
	ChargeQuota      int    `json:"charge_quota"`
	Currency         string `json:"currency"`
	CurrencyCode     int    `json:"currency_code"`
}

type HeroSMSEmailProductPage struct {
	Data  []HeroSMSEmailProduct `json:"data"`
	Page  int                   `json:"page"`
	Size  int                   `json:"size"`
	Total int                   `json:"total"`
}

type HeroSMSEmailPurchaseRequest struct {
	DomainID string `json:"domain_id"`
	Quantity int    `json:"quantity"`
}

type HeroSMSEmailOrderView struct {
	ID               string                       `json:"id"`
	Operation        string                       `json:"operation"`
	Status           string                       `json:"status"`
	DomainID         string                       `json:"domain_id"`
	Quantity         int                          `json:"quantity"`
	PriceMultiplier  string                       `json:"price_multiplier"`
	ReservedCostUSD  string                       `json:"reserved_cost_usd"`
	CustomerPriceUSD string                       `json:"customer_price_usd"`
	ChargeQuota      int                          `json:"charge_quota"`
	CreatedAt        int64                        `json:"created_at"`
	UpdatedAt        int64                        `json:"updated_at"`
	Activations      []HeroSMSEmailActivationView `json:"activations"`
}

type HeroSMSEmailActivationView struct {
	ID           string `json:"id"`
	OrderID      string `json:"order_id"`
	Status       string `json:"status"`
	DomainID     string `json:"domain_id"`
	ProviderID   string `json:"provider_id,omitempty"`
	Email        string `json:"email,omitempty"`
	Code         string `json:"code,omitempty"`
	Message      string `json:"message,omitempty"`
	UpstreamCost string `json:"upstream_cost_usd,omitempty"`
	Currency     string `json:"currency,omitempty"`
	CurrencyCode int    `json:"currency_code,omitempty"`
	CancelReason string `json:"cancel_reason,omitempty"`
	CreatedAt    int64  `json:"created_at"`
	UpdatedAt    int64  `json:"updated_at"`
}

type HeroSMSEmailActivationPage struct {
	Data  []HeroSMSEmailActivationView `json:"data"`
	Page  int                          `json:"page"`
	Size  int                          `json:"size"`
	Total int64                        `json:"total"`
}

type HeroSMSEmailOrder struct {
	ID                      string                   `json:"id" gorm:"primaryKey;size:64"`
	UserID                  int                      `json:"user_id" gorm:"index;not null"`
	Operation               string                   `json:"operation" gorm:"size:32;index;not null"`
	IdempotencyKeyHash      string                   `json:"idempotency_key_hash" gorm:"size:64;index:idx_hero_sms_user_idempotency,unique;not null"`
	RequestPayloadHash      string                   `json:"request_payload_hash" gorm:"size:64;not null"`
	DomainID                string                   `json:"domain_id" gorm:"size:128;not null"`
	Quantity                int                      `json:"quantity" gorm:"not null"`
	Status                  string                   `json:"status" gorm:"size:32;index;not null"`
	PriceMultiplier         string                   `json:"price_multiplier" gorm:"size:32;not null"`
	ReservedUnitCostMicros  int64                    `json:"reserved_unit_cost_micros" gorm:"not null"`
	CustomerUnitPriceMicros int64                    `json:"customer_unit_price_micros" gorm:"not null"`
	ChargeQuota             int                      `json:"charge_quota" gorm:"not null"`
	Currency                string                   `json:"currency" gorm:"size:8;not null"`
	CurrencyCode            int                      `json:"currency_code" gorm:"not null"`
	LastErrorCode           string                   `json:"last_error_code" gorm:"size:64"`
	LastErrorMessage        string                   `json:"last_error_message" gorm:"type:text"`
	CreatedAt               int64                    `json:"created_at" gorm:"index"`
	UpdatedAt               int64                    `json:"updated_at"`
	Activations             []HeroSMSEmailActivation `json:"activations" gorm:"foreignKey:OrderID;references:ID"`
}

type HeroSMSEmailActivation struct {
	ID                        string  `json:"id" gorm:"primaryKey;size:64"`
	OrderID                   string  `json:"order_id" gorm:"size:64;index;not null"`
	UserID                    int     `json:"user_id" gorm:"index;not null"`
	Slot                      int     `json:"slot" gorm:"not null"`
	Status                    string  `json:"status" gorm:"size:32;index;not null"`
	DomainID                  string  `json:"domain_id" gorm:"size:128;not null"`
	ProviderID                *string `json:"provider_id" gorm:"size:128;uniqueIndex"`
	ProviderEmailCiphertext   string  `json:"provider_email_ciphertext" gorm:"type:text"`
	ProviderCodeCiphertext    string  `json:"provider_code_ciphertext" gorm:"type:text"`
	ProviderMessageCiphertext string  `json:"provider_message_ciphertext" gorm:"type:text"`
	ProviderCostMicros        int64   `json:"provider_cost_micros"`
	Currency                  string  `json:"currency" gorm:"size:8"`
	CurrencyCode              int     `json:"currency_code"`
	CancelReason              string  `json:"cancel_reason" gorm:"size:64"`
	RefundQuota               int     `json:"refund_quota"`
	RefundedAt                int64   `json:"refunded_at"`
	CancelledAt               int64   `json:"cancelled_at"`
	ReorderOfActivationID     *string `json:"reorder_of_activation_id" gorm:"size:64;index"`
	CreatedAt                 int64   `json:"created_at" gorm:"index"`
	UpdatedAt                 int64   `json:"updated_at"`
}

type HeroSMSEmailQuotaLedger struct {
	ID             int64  `json:"id" gorm:"primaryKey;autoIncrement"`
	UserID         int    `json:"user_id" gorm:"index;not null"`
	OrderID        string `json:"order_id" gorm:"size:64;index;not null"`
	ActivationID   string `json:"activation_id" gorm:"size:64;index"`
	EntryType      string `json:"entry_type" gorm:"size:32;index;not null"`
	AmountQuota    int    `json:"amount_quota" gorm:"not null"`
	IdempotencyKey string `json:"idempotency_key" gorm:"size:128;uniqueIndex;not null"`
	CreatedAt      int64  `json:"created_at" gorm:"index"`
}

func (o *HeroSMSEmailOrder) BeforeCreate(_ *gorm.DB) error {
	if strings.TrimSpace(o.ID) == "" {
		o.ID = "hseord_" + common.GetUUID()
	}
	now := time.Now().Unix()
	if o.CreatedAt == 0 {
		o.CreatedAt = now
	}
	o.UpdatedAt = now
	return nil
}

func (o *HeroSMSEmailOrder) BeforeUpdate(_ *gorm.DB) error {
	o.UpdatedAt = time.Now().Unix()
	return nil
}

func (a *HeroSMSEmailActivation) BeforeCreate(_ *gorm.DB) error {
	if strings.TrimSpace(a.ID) == "" {
		a.ID = "hseact_" + common.GetUUID()
	}
	now := time.Now().Unix()
	if a.CreatedAt == 0 {
		a.CreatedAt = now
	}
	a.UpdatedAt = now
	return nil
}

func (a *HeroSMSEmailActivation) BeforeUpdate(_ *gorm.DB) error {
	a.UpdatedAt = time.Now().Unix()
	return nil
}

func (l *HeroSMSEmailQuotaLedger) BeforeCreate(_ *gorm.DB) error {
	if l.CreatedAt == 0 {
		l.CreatedAt = time.Now().Unix()
	}
	return nil
}

type heroSMSClientFactoryFunc func(baseURL string, apiKey string) herosms.Client

var heroSMSClientFactory heroSMSClientFactoryFunc = func(baseURL string, apiKey string) herosms.Client {
	return herosms.NewClient(baseURL, apiKey)
}

var heroSMSBaseURL = herosms.DefaultBaseURL

func SetHeroSMSClientFactoryForTest(factory func(baseURL string, apiKey string) herosms.Client, baseURL string) func() {
	previousFactory := heroSMSClientFactory
	previousBaseURL := heroSMSBaseURL
	if factory != nil {
		heroSMSClientFactory = factory
	}
	if strings.TrimSpace(baseURL) != "" {
		heroSMSBaseURL = strings.TrimRight(baseURL, "/")
	}
	return func() {
		heroSMSClientFactory = previousFactory
		heroSMSBaseURL = previousBaseURL
	}
}

func heroSMSClient() (herosms.Client, error) {
	if !setting.HeroSMSEnabled || strings.TrimSpace(setting.HeroSMSAPIKey) == "" {
		return nil, newHeroSMSError(http.StatusServiceUnavailable, "NOT_CONFIGURED", "HeroSMS 未配置")
	}
	baseURL := herosms.DefaultBaseURL
	if heroSMSBaseURL != herosms.DefaultBaseURL && !isProductionEnv() {
		baseURL = heroSMSBaseURL
	}
	return heroSMSClientFactory(baseURL, setting.HeroSMSAPIKey), nil
}

func isProductionEnv() bool {
	env := strings.ToLower(strings.TrimSpace(os.Getenv("GIN_MODE")))
	return env == "release"
}

func GetHeroSMSSettingsView() HeroSMSSettingsView {
	return HeroSMSSettingsView{
		Enabled:          setting.HeroSMSEnabled,
		APIKeyConfigured: strings.TrimSpace(setting.HeroSMSAPIKey) != "",
		Currency:         setting.HeroSMSCurrency,
		CurrencyCode:     setting.HeroSMSCurrencyCode,
		PriceMultiplier:  heroSMSMultiplierString(),
	}
}

func UpdateHeroSMSSettings(update HeroSMSSettingsUpdate) error {
	multiplier := heroSMSMultiplierString()
	if strings.TrimSpace(update.PriceMultiplier) != "" {
		parsed, err := decimal.NewFromString(strings.TrimSpace(update.PriceMultiplier))
		if err != nil || parsed.LessThanOrEqual(decimal.Zero) {
			return newHeroSMSError(http.StatusBadRequest, "INVALID_REQUEST", "invalid HeroSMS price multiplier")
		}
		multiplier = parsed.String()
	}
	enabled := setting.HeroSMSEnabled
	if update.Enabled != nil {
		enabled = *update.Enabled
	}
	apiKeyCiphertext := ""
	storeAPIKey := false
	if strings.TrimSpace(update.APIKey) != "" {
		ciphertext, err := common.EncryptPersistentString("hero_sms.api_key", "HERO_SMS_ENCRYPTION_KEY", "CRYPTO_SECRET", strings.TrimSpace(update.APIKey))
		if err != nil {
			return newHeroSMSError(http.StatusServiceUnavailable, "NOT_CONFIGURED", "HeroSMS encryption key is not configured")
		}
		apiKeyCiphertext = ciphertext
		storeAPIKey = true
	}
	updates := map[string]string{
		setting.HeroSMSOptionEnabled:    strconv.FormatBool(enabled),
		setting.HeroSMSOptionCurrency:   setting.HeroSMSCurrency,
		setting.HeroSMSOptionCode:       strconv.Itoa(setting.HeroSMSCurrencyCode),
		setting.HeroSMSOptionMultiplier: multiplier,
	}
	if storeAPIKey {
		updates[setting.HeroSMSOptionAPIKey] = apiKeyCiphertext
	}
	if err := DB.Transaction(func(tx *gorm.DB) error {
		for key, value := range updates {
			option := &Option{}
			if err := tx.FirstOrCreate(option, Option{Key: key}).Error; err != nil {
				return err
			}
			option.Value = value
			if err := tx.Save(option).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	for key, value := range updates {
		if err := updateOptionMap(key, value); err != nil {
			return err
		}
	}
	return nil
}

func ClearHeroSMSAPIKey() error {
	if setting.HeroSMSEnabled {
		return newHeroSMSError(http.StatusConflict, "INVALID_REQUEST", "disable HeroSMS before clearing the API key")
	}
	if err := DB.Where("key = ?", setting.HeroSMSOptionAPIKey).Delete(&Option{}).Error; err != nil {
		return err
	}
	setting.HeroSMSAPIKey = ""
	return updateOptionMap(setting.HeroSMSOptionAPIKey, "")
}

func TestHeroSMSConfiguration(ctx context.Context) error {
	client, err := heroSMSClient()
	if err != nil {
		return err
	}
	_, err = client.ListDomains(ctx, 1, 1, "")
	return mapHeroSMSProviderError(err)
}

func ListHeroSMSEmailProducts(ctx context.Context, page int, size int, site string) (*HeroSMSEmailProductPage, error) {
	client, err := heroSMSClient()
	if err != nil {
		return nil, err
	}
	response, err := client.ListDomains(ctx, page, size, site)
	if err != nil {
		return nil, mapHeroSMSProviderError(err)
	}
	multiplier, err := heroSMSMultiplierDecimal()
	if err != nil {
		return nil, err
	}
	products := make([]HeroSMSEmailProduct, 0, len(response.Data))
	for _, item := range response.Data {
		if item.CurrencyCode != HeroSMSCurrencyCode || !strings.EqualFold(item.Currency, setting.HeroSMSCurrency) {
			return nil, newHeroSMSError(http.StatusBadGateway, "CURRENCY_MISMATCH", "HeroSMS product currency mismatch")
		}
		customerPrice := item.CostUSD.Mul(multiplier)
		chargeQuota, err := heroSMSChargeQuota(customerPrice)
		if err != nil {
			return nil, err
		}
		products = append(products, HeroSMSEmailProduct{
			DomainID:         item.ID,
			Site:             item.Site,
			Domain:           item.Domain,
			Stock:            item.Stock,
			UpstreamCostUSD:  item.CostUSD.StringFixed(6),
			PriceMultiplier:  multiplier.String(),
			CustomerPriceUSD: customerPrice.StringFixed(6),
			ChargeQuota:      chargeQuota,
			Currency:         item.Currency,
			CurrencyCode:     item.CurrencyCode,
		})
	}
	return &HeroSMSEmailProductPage{Data: products, Page: response.Page, Size: response.Size, Total: response.Total}, nil
}

func CreateHeroSMSEmailActivations(ctx context.Context, userID int, idempotencyKey string, request HeroSMSEmailPurchaseRequest) (*HeroSMSEmailOrderView, int, error) {
	return createHeroSMSEmailOrder(ctx, userID, idempotencyKey, request, "purchase", nil)
}

func ReorderHeroSMSEmailActivation(ctx context.Context, userID int, activationID string, idempotencyKey string) (*HeroSMSEmailOrderView, int, error) {
	activation, err := getHeroSMSEmailActivationForUser(userID, activationID)
	if err != nil {
		return nil, 0, err
	}
	request := HeroSMSEmailPurchaseRequest{DomainID: activation.DomainID, Quantity: 1}
	return createHeroSMSEmailOrder(ctx, userID, idempotencyKey, request, "reorder", &activation.ID)
}

func createHeroSMSEmailOrder(ctx context.Context, userID int, idempotencyKey string, request HeroSMSEmailPurchaseRequest, operation string, reorderOf *string) (*HeroSMSEmailOrderView, int, error) {
	if strings.TrimSpace(idempotencyKey) == "" {
		return nil, 0, newHeroSMSError(http.StatusBadRequest, "INVALID_REQUEST", "Idempotency-Key is required")
	}
	if strings.TrimSpace(request.DomainID) == "" || request.Quantity < 1 || request.Quantity > 10 {
		return nil, 0, newHeroSMSError(http.StatusBadRequest, "INVALID_REQUEST", "invalid HeroSMS purchase request")
	}
	client, err := heroSMSClient()
	if err != nil {
		return nil, 0, err
	}
	quote, err := lookupHeroSMSDomainQuote(ctx, client, request.DomainID)
	if err != nil {
		return nil, 0, err
	}
	if quote.CurrencyCode != HeroSMSCurrencyCode || !strings.EqualFold(quote.Currency, setting.HeroSMSCurrency) {
		return nil, 0, newHeroSMSError(http.StatusBadGateway, "CURRENCY_MISMATCH", "HeroSMS quote currency mismatch")
	}
	multiplier, err := heroSMSMultiplierDecimal()
	if err != nil {
		return nil, 0, err
	}
	customerUnitPrice := quote.CostUSD.Mul(multiplier)
	chargeQuota, err := heroSMSChargeQuota(customerUnitPrice.Mul(decimal.NewFromInt(int64(request.Quantity))))
	if err != nil {
		return nil, 0, err
	}
	payloadHash, err := heroSMSPayloadHash(operation, request, reorderOf)
	if err != nil {
		return nil, 0, err
	}
	idempotencyHash := hashString(fmt.Sprintf("%d:%s:%s", userID, operation, strings.TrimSpace(idempotencyKey)))
	var existing HeroSMSEmailOrder
	if err := DB.Preload("Activations").Where("user_id = ? AND operation = ? AND idempotency_key_hash = ?", userID, operation, idempotencyHash).First(&existing).Error; err == nil {
		if existing.RequestPayloadHash != payloadHash {
			return nil, 0, newHeroSMSError(http.StatusConflict, "IDEMPOTENCY_MISMATCH", "idempotent request payload mismatch")
		}
		view, err := heroSMSEmailOrderView(&existing)
		if err != nil {
			return nil, 0, err
		}
		if existing.Status == HeroSMSEmailOrderStatusCompleted {
			return view, http.StatusCreated, nil
		}
		return view, http.StatusAccepted, nil
	}
	order := HeroSMSEmailOrder{
		UserID:                  userID,
		Operation:               operation,
		IdempotencyKeyHash:      idempotencyHash,
		RequestPayloadHash:      payloadHash,
		DomainID:                request.DomainID,
		Quantity:                request.Quantity,
		Status:                  HeroSMSEmailOrderStatusPendingProvider,
		PriceMultiplier:         multiplier.String(),
		ReservedUnitCostMicros:  decimalToMicros(quote.CostUSD),
		CustomerUnitPriceMicros: decimalToMicros(customerUnitPrice),
		ChargeQuota:             chargeQuota,
		Currency:                setting.HeroSMSCurrency,
		CurrencyCode:            HeroSMSCurrencyCode,
	}
	activations := make([]HeroSMSEmailActivation, 0, request.Quantity)
	for slot := 0; slot < request.Quantity; slot++ {
		activation := HeroSMSEmailActivation{UserID: userID, Slot: slot + 1, Status: HeroSMSEmailActivationStatusPendingProvider, DomainID: request.DomainID}
		if reorderOf != nil {
			activation.ReorderOfActivationID = reorderOf
		}
		activations = append(activations, activation)
	}
	newQuota, err := reserveHeroSMSEmailQuota(&order, activations)
	if err != nil {
		if heroErr, ok := err.(*HeroSMSError); ok && heroErr.Code == "IDEMPOTENCY_MISMATCH" {
			existingOrder, existingErr := getHeroSMSEmailOrderByIdempotency(userID, operation, idempotencyHash)
			if existingErr == nil && existingOrder.RequestPayloadHash == payloadHash {
				view, viewErr := heroSMSEmailOrderView(existingOrder)
				if viewErr != nil {
					return nil, 0, viewErr
				}
				if existingOrder.Status == HeroSMSEmailOrderStatusCompleted {
					return view, http.StatusCreated, nil
				}
				return view, http.StatusAccepted, nil
			}
		}
		return nil, 0, err
	}
	_ = updateUserQuotaCache(userID, newQuota)
	var purchaseResult *HeroSMSEmailOrderView
	statusCode := http.StatusCreated
	if request.Quantity == 1 {
		record, purchaseErr := client.CreateEmail(ctx, request.DomainID)
		if purchaseErr != nil {
			return handleHeroSMSPurchaseProviderError(&order, purchaseErr)
		}
		purchaseResult, statusCode, err = finalizeHeroSMSKnownPurchase(ctx, client, &order, []herosms.EmailRecord{*record}, false)
		if err != nil {
			return nil, 0, err
		}
		return purchaseResult, statusCode, nil
	}
	batch, purchaseErr := client.CreateEmailBatch(ctx, request.DomainID, request.Quantity)
	if purchaseErr != nil {
		return handleHeroSMSPurchaseProviderError(&order, purchaseErr)
	}
	purchaseResult, statusCode, err = finalizeHeroSMSKnownPurchase(ctx, client, &order, batch.Items, true)
	if err != nil {
		return nil, 0, err
	}
	return purchaseResult, statusCode, nil
}

func reserveHeroSMSEmailQuota(order *HeroSMSEmailOrder, activations []HeroSMSEmailActivation) (int, error) {
	var newQuota int
	err := DB.Transaction(func(tx *gorm.DB) error {
		var user User
		if err := lockForUpdate(tx).Select("id", "quota").Where("id = ?", order.UserID).First(&user).Error; err != nil {
			return err
		}
		if user.Quota < order.ChargeQuota {
			return newHeroSMSError(http.StatusPaymentRequired, "INSUFFICIENT_BALANCE", "insufficient quota balance")
		}
		update := tx.Model(&User{}).Where("id = ? AND quota >= ?", order.UserID, order.ChargeQuota).UpdateColumn("quota", gorm.Expr("quota - ?", order.ChargeQuota))
		if update.Error != nil {
			return update.Error
		}
		if update.RowsAffected != 1 {
			return newHeroSMSError(http.StatusPaymentRequired, "INSUFFICIENT_BALANCE", "insufficient quota balance")
		}
		if err := tx.Create(order).Error; err != nil {
			if uniqueConstraintError(err) {
				return newHeroSMSError(http.StatusConflict, "IDEMPOTENCY_MISMATCH", "duplicate HeroSMS idempotency key")
			}
			return err
		}
		for i := range activations {
			activations[i].OrderID = order.ID
		}
		if err := tx.Create(&activations).Error; err != nil {
			return err
		}
		if err := tx.Create(&HeroSMSEmailQuotaLedger{UserID: order.UserID, OrderID: order.ID, EntryType: HeroSMSEmailLedgerReserve, AmountQuota: -order.ChargeQuota, IdempotencyKey: "hero_sms:reserve:" + order.ID}).Error; err != nil {
			return err
		}
		newQuota = user.Quota - order.ChargeQuota
		return nil
	})
	return newQuota, err
}

func finalizeHeroSMSKnownPurchase(ctx context.Context, client herosms.Client, order *HeroSMSEmailOrder, records []herosms.EmailRecord, batch bool) (*HeroSMSEmailOrderView, int, error) {
	resolved := make([]herosms.EmailRecord, 0, len(records))
	for _, item := range records {
		if item.ID == "" && strings.TrimSpace(item.Email) != "" {
			listing, err := herosms.FindEmailByExactAddress(ctx, client, item.Email)
			if err != nil {
				return nil, 0, mapHeroSMSProviderError(err)
			}
			if listing == nil || strings.TrimSpace(listing.ID) == "" {
				resolved = append(resolved, item)
				continue
			}
			detail, err := client.GetEmail(ctx, listing.ID)
			if err != nil {
				return nil, 0, mapHeroSMSProviderError(err)
			}
			resolved = append(resolved, *detail)
			continue
		}
		resolved = append(resolved, item)
	}
	status := HeroSMSEmailOrderStatusCompleted
	if len(resolved) != order.Quantity {
		status = HeroSMSEmailOrderStatusReconciling
	}
	var orderView *HeroSMSEmailOrderView
	err := DB.Transaction(func(tx *gorm.DB) error {
		var activations []HeroSMSEmailActivation
		if err := tx.Where("order_id = ?", order.ID).Order("slot asc").Find(&activations).Error; err != nil {
			return err
		}
		for i := range activations {
			if i >= len(resolved) {
				activations[i].Status = HeroSMSEmailActivationStatusReconciling
				if err := tx.Save(&activations[i]).Error; err != nil {
					return err
				}
				continue
			}
			record := resolved[i]
			if strings.TrimSpace(record.Email) == "" {
				activations[i].Status = HeroSMSEmailActivationStatusReconciling
				status = HeroSMSEmailOrderStatusReconciling
				if err := tx.Save(&activations[i]).Error; err != nil {
					return err
				}
				continue
			}
			if record.ID != "" {
				activations[i].ProviderID = &record.ID
			}
			activations[i].ProviderEmailCiphertext, _ = encryptHeroSMSPayload(record.Email)
			activations[i].ProviderCodeCiphertext, _ = encryptHeroSMSPayload(record.Code)
			activations[i].ProviderMessageCiphertext, _ = encryptHeroSMSPayload(record.Message)
			activations[i].ProviderCostMicros = decimalToMicros(record.CostUSD)
			activations[i].Currency = strings.TrimSpace(record.Currency)
			activations[i].CurrencyCode = record.CurrencyCode
			if record.ID == "" || record.CurrencyCode != HeroSMSCurrencyCode || !strings.EqualFold(record.Currency, setting.HeroSMSCurrency) {
				activations[i].Status = HeroSMSEmailActivationStatusReconciling
				activations[i].CancelReason = HeroSMSEmailCancelReasonCurrencyMismatch
				status = HeroSMSEmailOrderStatusReconciling
			} else {
				reservedUnitCost := microsToDecimal(order.ReservedUnitCostMicros)
				if record.CostUSD.GreaterThan(reservedUnitCost) {
					cancelErr := client.DeleteEmail(ctx, record.ID)
					if cancelErr == nil {
						activations[i].Status = HeroSMSEmailActivationStatusRefunded
						activations[i].CancelReason = HeroSMSEmailCancelReasonPriceChanged
						activations[i].CancelledAt = time.Now().Unix()
						activations[i].RefundedAt = activations[i].CancelledAt
						activations[i].RefundQuota = quotaPerActivation(order.ChargeQuota, order.Quantity)
						if err := heroSMSRefundActivationTx(tx, order, &activations[i], activations[i].RefundQuota, "price_changed"); err != nil {
							return err
						}
						status = HeroSMSEmailOrderStatusReconciling
					} else {
						activations[i].Status = HeroSMSEmailActivationStatusCancelPending
						activations[i].CancelReason = HeroSMSEmailCancelReasonPriceChanged
						activations[i].RefundQuota = quotaPerActivation(order.ChargeQuota, order.Quantity)
						status = HeroSMSEmailOrderStatusReconciling
					}
				} else {
					activations[i].Status = HeroSMSEmailActivationStatusActive
				}
			}
			if err := tx.Save(&activations[i]).Error; err != nil {
				return err
			}
		}
		order.Status = status
		if status != HeroSMSEmailOrderStatusCompleted && batch {
			order.LastErrorCode = "PURCHASE_PENDING_RECONCILIATION"
		}
		if err := tx.Save(order).Error; err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	fresh, err := getHeroSMSEmailOrder(order.UserID, order.ID)
	if err != nil {
		return nil, 0, err
	}
	orderView, err = heroSMSEmailOrderView(fresh)
	if err != nil {
		return nil, 0, err
	}
	if fresh.Status == HeroSMSEmailOrderStatusCompleted {
		return orderView, http.StatusCreated, nil
	}
	return orderView, http.StatusAccepted, nil
}

func handleHeroSMSPurchaseProviderError(order *HeroSMSEmailOrder, purchaseErr error) (*HeroSMSEmailOrderView, int, error) {
	mapped := mapHeroSMSProviderError(purchaseErr)
	if heroErr, ok := mapped.(*HeroSMSError); ok && heroErr.Code == "UPSTREAM_TIMEOUT" {
		if err := markHeroSMSEmailOrderStatus(order.ID, HeroSMSEmailOrderStatusPurchaseUnknown, heroErr.Code, heroErr.Message, HeroSMSEmailActivationStatusReconciling); err != nil {
			return nil, 0, err
		}
		orderView, err := GetHeroSMSEmailOrderView(order.UserID, order.ID)
		if err != nil {
			return nil, 0, err
		}
		return orderView, http.StatusAccepted, nil
	}
	if err := failHeroSMSEmailOrder(order, mapped); err != nil {
		return nil, 0, err
	}
	return nil, 0, mapped
}

func failHeroSMSEmailOrder(order *HeroSMSEmailOrder, cause error) error {
	heroErr, _ := cause.(*HeroSMSError)
	return DB.Transaction(func(tx *gorm.DB) error {
		var fresh HeroSMSEmailOrder
		if err := lockForUpdate(tx).Where("id = ?", order.ID).First(&fresh).Error; err != nil {
			return err
		}
		if fresh.Status != HeroSMSEmailOrderStatusPendingProvider {
			return nil
		}
		fresh.Status = HeroSMSEmailOrderStatusFailed
		if heroErr != nil {
			fresh.LastErrorCode = heroErr.Code
			fresh.LastErrorMessage = heroErr.Message
		}
		if err := tx.Save(&fresh).Error; err != nil {
			return err
		}
		var activations []HeroSMSEmailActivation
		if err := tx.Where("order_id = ?", fresh.ID).Find(&activations).Error; err != nil {
			return err
		}
		for i := range activations {
			if activations[i].Status == HeroSMSEmailActivationStatusPendingProvider {
				activations[i].Status = HeroSMSEmailActivationStatusCancelled
				activations[i].CancelledAt = time.Now().Unix()
				if err := tx.Save(&activations[i]).Error; err != nil {
					return err
				}
			}
		}
		return heroSMSRefundOrderTx(tx, &fresh, fresh.ChargeQuota, "order_failure")
	})
}

func heroSMSRefundOrderTx(tx *gorm.DB, order *HeroSMSEmailOrder, quota int, refundKey string) error {
	ledger := HeroSMSEmailQuotaLedger{UserID: order.UserID, OrderID: order.ID, EntryType: HeroSMSEmailLedgerRefund, AmountQuota: quota, IdempotencyKey: "hero_sms:refund:" + order.ID + ":" + refundKey}
	if err := tx.Where("id = ?", order.UserID).UpdateColumn("quota", gorm.Expr("quota + ?", quota)).Error; err != nil {
		return err
	}
	if err := tx.Create(&ledger).Error; err != nil {
		if uniqueConstraintError(err) {
			return nil
		}
		return err
	}
	return nil
}

func heroSMSRefundActivationTx(tx *gorm.DB, order *HeroSMSEmailOrder, activation *HeroSMSEmailActivation, quota int, refundKey string) error {
	if quota <= 0 {
		return nil
	}
	ledger := HeroSMSEmailQuotaLedger{UserID: order.UserID, OrderID: order.ID, ActivationID: activation.ID, EntryType: HeroSMSEmailLedgerRefund, AmountQuota: quota, IdempotencyKey: "hero_sms:refund:" + activation.ID + ":" + refundKey}
	if err := tx.Create(&ledger).Error; err != nil {
		if uniqueConstraintError(err) {
			return nil
		}
		return err
	}
	return tx.Model(&User{}).Where("id = ?", order.UserID).UpdateColumn("quota", gorm.Expr("quota + ?", quota)).Error
}

func markHeroSMSEmailOrderStatus(orderID string, status string, errorCode string, errorMessage string, activationStatus string) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&HeroSMSEmailOrder{}).Where("id = ?", orderID).Updates(map[string]any{"status": status, "last_error_code": errorCode, "last_error_message": errorMessage, "updated_at": time.Now().Unix()}).Error; err != nil {
			return err
		}
		return tx.Model(&HeroSMSEmailActivation{}).Where("order_id = ? AND status = ?", orderID, HeroSMSEmailActivationStatusPendingProvider).Updates(map[string]any{"status": activationStatus, "updated_at": time.Now().Unix()}).Error
	})
}

func GetHeroSMSEmailOrderView(userID int, orderID string) (*HeroSMSEmailOrderView, error) {
	order, err := getHeroSMSEmailOrder(userID, orderID)
	if err != nil {
		return nil, err
	}
	return heroSMSEmailOrderView(order)
}

func ListHeroSMSEmailActivations(userID int, page int, size int, status string) (*HeroSMSEmailActivationPage, error) {
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 20
	}
	query := DB.Model(&HeroSMSEmailActivation{}).Where("user_id = ?", userID)
	if strings.TrimSpace(status) != "" {
		query = query.Where("status = ?", strings.TrimSpace(status))
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, err
	}
	var activations []HeroSMSEmailActivation
	if err := query.Order("created_at desc").Limit(size).Offset((page - 1) * size).Find(&activations).Error; err != nil {
		return nil, err
	}
	views := make([]HeroSMSEmailActivationView, 0, len(activations))
	for i := range activations {
		view, err := heroSMSEmailActivationView(&activations[i])
		if err != nil {
			return nil, err
		}
		views = append(views, *view)
	}
	return &HeroSMSEmailActivationPage{Data: views, Page: page, Size: size, Total: total}, nil
}

func GetHeroSMSEmailActivation(userID int, activationID string) (*HeroSMSEmailActivationView, error) {
	activation, err := getHeroSMSEmailActivationForUser(userID, activationID)
	if err != nil {
		return nil, err
	}
	return heroSMSEmailActivationView(activation)
}

func RefreshHeroSMSEmailActivation(ctx context.Context, userID int, activationID string) (*HeroSMSEmailActivationView, error) {
	activation, err := getHeroSMSEmailActivationForUser(userID, activationID)
	if err != nil {
		return nil, err
	}
	client, err := heroSMSClient()
	if err != nil {
		return nil, err
	}
	if activation.ProviderID == nil || strings.TrimSpace(*activation.ProviderID) == "" {
		if err := reconcileHeroSMSEmailActivation(ctx, client, activation); err != nil {
			return nil, err
		}
	} else {
		record, err := client.GetEmail(ctx, *activation.ProviderID)
		if err != nil {
			return nil, mapHeroSMSProviderError(err)
		}
		if err := persistHeroSMSEmailRecord(activation, record); err != nil {
			return nil, err
		}
	}
	return GetHeroSMSEmailActivation(userID, activationID)
}

func CancelHeroSMSEmailActivation(ctx context.Context, userID int, activationID string) (*HeroSMSEmailActivationView, error) {
	activation, err := getHeroSMSEmailActivationForUser(userID, activationID)
	if err != nil {
		return nil, err
	}
	client, err := heroSMSClient()
	if err != nil {
		return nil, err
	}
	if activation.ProviderID == nil || strings.TrimSpace(*activation.ProviderID) == "" {
		if err := DB.Model(&HeroSMSEmailActivation{}).Where("id = ? AND user_id = ?", activation.ID, userID).Updates(map[string]any{"status": HeroSMSEmailActivationStatusCancelPending, "cancel_reason": HeroSMSEmailCancelReasonUser, "updated_at": time.Now().Unix()}).Error; err != nil {
			return nil, err
		}
		return GetHeroSMSEmailActivation(userID, activationID)
	}
	if err := client.DeleteEmail(ctx, *activation.ProviderID); err != nil {
		if err := DB.Model(&HeroSMSEmailActivation{}).Where("id = ? AND user_id = ?", activation.ID, userID).Updates(map[string]any{"status": HeroSMSEmailActivationStatusCancelPending, "cancel_reason": HeroSMSEmailCancelReasonUser, "updated_at": time.Now().Unix()}).Error; err != nil {
			return nil, err
		}
		return GetHeroSMSEmailActivation(userID, activationID)
	}
	if err := DB.Model(&HeroSMSEmailActivation{}).Where("id = ? AND user_id = ?", activation.ID, userID).Updates(map[string]any{"status": HeroSMSEmailActivationStatusCancelled, "cancel_reason": HeroSMSEmailCancelReasonUser, "cancelled_at": time.Now().Unix(), "updated_at": time.Now().Unix()}).Error; err != nil {
		return nil, err
	}
	return GetHeroSMSEmailActivation(userID, activationID)
}

func GetHeroSMSEmailActivationOrderView(userID int, activationID string) (*HeroSMSEmailOrderView, error) {
	activation, err := getHeroSMSEmailActivationForUser(userID, activationID)
	if err != nil {
		return nil, err
	}
	return GetHeroSMSEmailOrderView(userID, activation.OrderID)
}

func reconcileHeroSMSEmailActivation(ctx context.Context, client herosms.Client, activation *HeroSMSEmailActivation) error {
	email, err := decryptHeroSMSPayload(activation.ProviderEmailCiphertext)
	if err != nil {
		return err
	}
	listing, err := herosms.FindEmailByExactAddress(ctx, client, email)
	if err != nil {
		return mapHeroSMSProviderError(err)
	}
	if listing == nil || strings.TrimSpace(listing.ID) == "" {
		return nil
	}
	record, err := client.GetEmail(ctx, listing.ID)
	if err != nil {
		return mapHeroSMSProviderError(err)
	}
	return persistHeroSMSEmailRecord(activation, record)
}

func persistHeroSMSEmailRecord(activation *HeroSMSEmailActivation, record *herosms.EmailRecord) error {
	updates := map[string]any{
		"status":               HeroSMSEmailActivationStatusActive,
		"provider_cost_micros": decimalToMicros(record.CostUSD),
		"currency":             strings.TrimSpace(record.Currency),
		"currency_code":        record.CurrencyCode,
		"updated_at":           time.Now().Unix(),
	}
	if strings.TrimSpace(record.ID) != "" {
		updates["provider_id"] = record.ID
	}
	if encrypted, err := encryptHeroSMSPayload(record.Email); err == nil {
		updates["provider_email_ciphertext"] = encrypted
	}
	if encrypted, err := encryptHeroSMSPayload(record.Code); err == nil {
		updates["provider_code_ciphertext"] = encrypted
	}
	if encrypted, err := encryptHeroSMSPayload(record.Message); err == nil {
		updates["provider_message_ciphertext"] = encrypted
	}
	if record.CurrencyCode != HeroSMSCurrencyCode || !strings.EqualFold(record.Currency, setting.HeroSMSCurrency) {
		updates["status"] = HeroSMSEmailActivationStatusCancelPending
		updates["cancel_reason"] = HeroSMSEmailCancelReasonCurrencyMismatch
	}
	return DB.Model(&HeroSMSEmailActivation{}).Where("id = ?", activation.ID).Updates(updates).Error
}

func RunHeroSMSEmailReconciliationOnce(ctx context.Context, limit int) (int, error) {
	if !setting.HeroSMSEnabled || strings.TrimSpace(setting.HeroSMSAPIKey) == "" {
		return 0, nil
	}
	if limit <= 0 {
		limit = 20
	}
	client, err := heroSMSClient()
	if err != nil {
		return 0, nil
	}
	var activations []HeroSMSEmailActivation
	if err := DB.Where("status IN ?", []string{HeroSMSEmailActivationStatusReconciling, HeroSMSEmailActivationStatusCancelPending}).Order("updated_at asc").Limit(limit).Find(&activations).Error; err != nil {
		return 0, err
	}
	processed := 0
	for i := range activations {
		processed++
		activation := activations[i]
		switch activation.Status {
		case HeroSMSEmailActivationStatusReconciling:
			_ = reconcileHeroSMSEmailActivation(ctx, client, &activation)
		case HeroSMSEmailActivationStatusCancelPending:
			if activation.ProviderID == nil || strings.TrimSpace(*activation.ProviderID) == "" {
				_ = reconcileHeroSMSEmailActivation(ctx, client, &activation)
				break
			}
			if err := client.DeleteEmail(ctx, *activation.ProviderID); err == nil {
				updates := map[string]any{"status": HeroSMSEmailActivationStatusCancelled, "cancelled_at": time.Now().Unix(), "updated_at": time.Now().Unix()}
				if activation.CancelReason == HeroSMSEmailCancelReasonPriceChanged || activation.CancelReason == HeroSMSEmailCancelReasonCurrencyMismatch || activation.CancelReason == HeroSMSEmailCancelReasonBadUpstream {
					updates["status"] = HeroSMSEmailActivationStatusRefunded
					updates["refunded_at"] = time.Now().Unix()
					var order HeroSMSEmailOrder
					if err := DB.Where("id = ?", activation.OrderID).First(&order).Error; err == nil {
						_ = DB.Transaction(func(tx *gorm.DB) error {
							if err := tx.Model(&HeroSMSEmailActivation{}).Where("id = ?", activation.ID).Updates(updates).Error; err != nil {
								return err
							}
							return heroSMSRefundActivationTx(tx, &order, &activation, activation.RefundQuota, activation.CancelReason)
						})
					}
					continue
				}
				_ = DB.Model(&HeroSMSEmailActivation{}).Where("id = ?", activation.ID).Updates(updates).Error
			}
		}
	}
	return processed, nil
}

func HasPendingHeroSMSEmailReconciliationWork() (bool, error) {
	var count int64
	err := DB.Model(&HeroSMSEmailActivation{}).Where("status IN ?", []string{HeroSMSEmailActivationStatusReconciling, HeroSMSEmailActivationStatusCancelPending}).Count(&count).Error
	return count > 0, err
}

func getHeroSMSEmailActivationForUser(userID int, activationID string) (*HeroSMSEmailActivation, error) {
	var activation HeroSMSEmailActivation
	if err := DB.Where("id = ? AND user_id = ?", activationID, userID).First(&activation).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, newHeroSMSError(http.StatusNotFound, "NOT_FOUND", "HeroSMS activation not found")
		}
		return nil, err
	}
	return &activation, nil
}

func getHeroSMSEmailOrderByIdempotency(userID int, operation string, idempotencyHash string) (*HeroSMSEmailOrder, error) {
	var order HeroSMSEmailOrder
	if err := DB.Preload("Activations").Where("user_id = ? AND operation = ? AND idempotency_key_hash = ?", userID, operation, idempotencyHash).First(&order).Error; err != nil {
		return nil, err
	}
	return &order, nil
}

func getHeroSMSEmailOrder(userID int, orderID string) (*HeroSMSEmailOrder, error) {
	var order HeroSMSEmailOrder
	if err := DB.Preload("Activations").Where("id = ? AND user_id = ?", orderID, userID).First(&order).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, newHeroSMSError(http.StatusNotFound, "NOT_FOUND", "HeroSMS order not found")
		}
		return nil, err
	}
	return &order, nil
}

func heroSMSEmailOrderView(order *HeroSMSEmailOrder) (*HeroSMSEmailOrderView, error) {
	views := make([]HeroSMSEmailActivationView, 0, len(order.Activations))
	for i := range order.Activations {
		view, err := heroSMSEmailActivationView(&order.Activations[i])
		if err != nil {
			return nil, err
		}
		views = append(views, *view)
	}
	return &HeroSMSEmailOrderView{
		ID:               order.ID,
		Operation:        order.Operation,
		Status:           order.Status,
		DomainID:         order.DomainID,
		Quantity:         order.Quantity,
		PriceMultiplier:  order.PriceMultiplier,
		ReservedCostUSD:  microsToDecimal(order.ReservedUnitCostMicros).StringFixed(6),
		CustomerPriceUSD: microsToDecimal(order.CustomerUnitPriceMicros).StringFixed(6),
		ChargeQuota:      order.ChargeQuota,
		CreatedAt:        order.CreatedAt,
		UpdatedAt:        order.UpdatedAt,
		Activations:      views,
	}, nil
}

func heroSMSEmailActivationView(activation *HeroSMSEmailActivation) (*HeroSMSEmailActivationView, error) {
	email, err := decryptHeroSMSPayload(activation.ProviderEmailCiphertext)
	if err != nil {
		return nil, err
	}
	code, err := decryptHeroSMSPayload(activation.ProviderCodeCiphertext)
	if err != nil {
		return nil, err
	}
	message, err := decryptHeroSMSPayload(activation.ProviderMessageCiphertext)
	if err != nil {
		return nil, err
	}
	providerID := ""
	if activation.ProviderID != nil {
		providerID = *activation.ProviderID
	}
	return &HeroSMSEmailActivationView{
		ID:           activation.ID,
		OrderID:      activation.OrderID,
		Status:       activation.Status,
		DomainID:     activation.DomainID,
		ProviderID:   providerID,
		Email:        email,
		Code:         code,
		Message:      message,
		UpstreamCost: microsToDecimal(activation.ProviderCostMicros).StringFixed(6),
		Currency:     activation.Currency,
		CurrencyCode: activation.CurrencyCode,
		CancelReason: activation.CancelReason,
		CreatedAt:    activation.CreatedAt,
		UpdatedAt:    activation.UpdatedAt,
	}, nil
}

func lookupHeroSMSDomainQuote(ctx context.Context, client herosms.Client, domainID string) (*herosms.Domain, error) {
	products, err := client.ListDomains(ctx, 1, 100, "")
	if err != nil {
		return nil, mapHeroSMSProviderError(err)
	}
	for _, item := range products.Data {
		if item.ID == domainID {
			copied := item
			return &copied, nil
		}
	}
	return nil, newHeroSMSError(http.StatusNotFound, "NOT_FOUND", "HeroSMS domain not found")
}

func heroSMSPayloadHash(operation string, request HeroSMSEmailPurchaseRequest, reorderOf *string) (string, error) {
	body := map[string]any{"operation": operation, "domain_id": request.DomainID, "quantity": request.Quantity}
	if reorderOf != nil {
		body["reorder_of_activation_id"] = *reorderOf
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	return hashString(string(encoded)), nil
}

func hashString(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func heroSMSMultiplierString() string {
	value := strings.TrimSpace(setting.HeroSMSPriceMultiplierValue)
	if value == "" {
		return setting.HeroSMSPriceMultiplier
	}
	return value
}

func heroSMSMultiplierDecimal() (decimal.Decimal, error) {
	value, err := decimal.NewFromString(heroSMSMultiplierString())
	if err != nil || value.LessThanOrEqual(decimal.Zero) {
		return decimal.Zero, newHeroSMSError(http.StatusBadRequest, "INVALID_REQUEST", "invalid HeroSMS price multiplier")
	}
	return value, nil
}

func heroSMSChargeQuota(priceUSD decimal.Decimal) (int, error) {
	quotaUnit, err := decimal.NewFromString(strconv.FormatFloat(common.QuotaPerUnit, 'f', -1, 64))
	if err != nil {
		return 0, err
	}
	return common.QuotaFromDecimalStrict(priceUSD.Mul(quotaUnit).Ceil())
}

func decimalToMicros(value decimal.Decimal) int64 {
	return value.Shift(6).RoundCeil(0).IntPart()
}

func microsToDecimal(value int64) decimal.Decimal {
	return decimal.NewFromInt(value).Shift(-6)
}

func encryptHeroSMSPayload(value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", nil
	}
	return common.EncryptPersistentString("hero_sms.payload", "HERO_SMS_ENCRYPTION_KEY", "CRYPTO_SECRET", value)
}

func decryptHeroSMSPayload(value string) (string, error) {
	return common.DecryptPersistentString("hero_sms.payload", "HERO_SMS_ENCRYPTION_KEY", "CRYPTO_SECRET", value)
}

func mapHeroSMSProviderError(err error) error {
	if err == nil {
		return nil
	}
	if heroErr, ok := err.(*HeroSMSError); ok {
		return heroErr
	}
	switch {
	case errors.Is(err, herosms.ErrUnauthorized):
		return newHeroSMSError(http.StatusServiceUnavailable, "NOT_CONFIGURED", "HeroSMS credentials are invalid")
	case errors.Is(err, herosms.ErrNotFound):
		return newHeroSMSError(http.StatusNotFound, "NOT_FOUND", "HeroSMS resource not found")
	case errors.Is(err, herosms.ErrInvalidRequest):
		return newHeroSMSError(http.StatusBadRequest, "INVALID_REQUEST", "HeroSMS request was rejected")
	case errors.Is(err, herosms.ErrRateLimited):
		return newHeroSMSError(http.StatusTooManyRequests, "RATE_LIMITED", "HeroSMS rate limited the request")
	case errors.Is(err, herosms.ErrUpstreamBusy):
		return newHeroSMSError(http.StatusBadGateway, "UPSTREAM_BUSY", "HeroSMS upstream is busy")
	case errors.Is(err, herosms.ErrUpstreamTimeout):
		return newHeroSMSError(http.StatusAccepted, "UPSTREAM_TIMEOUT", "HeroSMS purchase timed out and is pending reconciliation")
	case errors.Is(err, herosms.ErrBadResponse):
		return newHeroSMSError(http.StatusBadGateway, "BAD_UPSTREAM_RESPONSE", "HeroSMS returned an invalid response")
	default:
		return newHeroSMSError(http.StatusBadGateway, "UPSTREAM_BUSY", "HeroSMS request failed")
	}
}

func quotaPerActivation(total int, quantity int) int {
	if quantity <= 1 {
		return total
	}
	base := total / quantity
	if base <= 0 {
		return total
	}
	return base
}
