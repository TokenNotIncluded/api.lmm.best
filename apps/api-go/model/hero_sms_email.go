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
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/service/herosms"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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
	HeroSMSEmailActivationStatusCompleted       = "completed"
	HeroSMSEmailActivationStatusReconciling     = "reconciling"
	HeroSMSEmailActivationStatusCancelPending   = "cancel_pending"
	HeroSMSEmailActivationStatusCancelled       = "cancelled"
	HeroSMSEmailActivationStatusExpired         = "expired"
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
	EmailEnabled     bool   `json:"email_enabled"`
	SMSEnabled       bool   `json:"sms_enabled"`
	APIKeyConfigured bool   `json:"api_key_configured"`
	PendingWork      bool   `json:"pending_work"`
	Currency         string `json:"currency"`
	CurrencyCode     int    `json:"currency_code"`
	PriceMultiplier  string `json:"price_multiplier"`
}

type HeroSMSSettingsUpdate struct {
	Enabled         *bool  `json:"enabled"`
	EmailEnabled    *bool  `json:"email_enabled"`
	SMSEnabled      *bool  `json:"sms_enabled"`
	APIKey          string `json:"api_key"`
	PriceMultiplier string `json:"price_multiplier"`
}

type HeroSMSEmailProduct struct {
	ID               string `json:"id"`
	Site             string `json:"site"`
	Domain           string `json:"domain"`
	Count            int    `json:"count"`
	Available        bool   `json:"available"`
	CustomerPriceUSD string `json:"customer_price_usd"`
	ChargeQuota      int    `json:"charge_quota"`
}

type HeroSMSEmailProductPage struct {
	Items []HeroSMSEmailProduct `json:"items"`
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
	Site             string                       `json:"site"`
	Domain           string                       `json:"domain"`
	Quantity         int                          `json:"quantity"`
	CustomerPriceUSD string                       `json:"customer_price_usd"`
	ChargeQuota      int                          `json:"charge_quota"`
	RefundedQuota    int                          `json:"refunded_quota"`
	CreatedAt        int64                        `json:"created_at"`
	UpdatedAt        int64                        `json:"updated_at"`
	Activations      []HeroSMSEmailActivationView `json:"activations"`
}

type HeroSMSEmailActivationView struct {
	ID           string `json:"id"`
	OrderID      string `json:"order_id"`
	Status       string `json:"status"`
	DomainID     string `json:"domain_id"`
	Site         string `json:"site"`
	Domain       string `json:"domain"`
	Email        string `json:"email,omitempty"`
	Code         string `json:"code,omitempty"`
	Message      string `json:"message,omitempty"`
	ChargeQuota  int    `json:"charge_quota"`
	CancelReason string `json:"cancel_reason,omitempty"`
	CreatedAt    int64  `json:"created_at"`
	UpdatedAt    int64  `json:"updated_at"`
}

type HeroSMSEmailActivationPage struct {
	Items []HeroSMSEmailActivationView `json:"items"`
	Page  int                          `json:"page"`
	Size  int                          `json:"size"`
	Total int64                        `json:"total"`
}

type HeroSMSEmailOrder struct {
	ID                         string                   `json:"id" gorm:"primaryKey;size:64"`
	UserID                     int                      `json:"user_id" gorm:"index;not null"`
	Operation                  string                   `json:"operation" gorm:"size:32;index;not null"`
	IdempotencyKeyHash         string                   `json:"idempotency_key_hash" gorm:"size:64;index:idx_hero_sms_user_idempotency,unique;not null"`
	RequestPayloadHash         string                   `json:"request_payload_hash" gorm:"size:64;not null"`
	DomainID                   string                   `json:"domain_id" gorm:"size:2048;not null"`
	Site                       string                   `json:"site" gorm:"size:255;not null"`
	Domain                     string                   `json:"domain" gorm:"size:255;not null"`
	Quantity                   int                      `json:"quantity" gorm:"not null"`
	Status                     string                   `json:"status" gorm:"size:32;index;not null"`
	PriceMultiplier            string                   `json:"price_multiplier" gorm:"size:32;not null"`
	ReservedUnitCostMicros     int64                    `json:"reserved_unit_cost_micros" gorm:"not null"`
	ReservedUnitCostDecimal    string                   `json:"reserved_unit_cost_decimal" gorm:"size:64;not null"`
	CustomerUnitPriceMicros    int64                    `json:"customer_unit_price_micros" gorm:"not null"`
	ChargeQuota                int                      `json:"charge_quota" gorm:"not null"`
	RefundedQuota              int                      `json:"refunded_quota" gorm:"not null;default:0"`
	Currency                   string                   `json:"currency" gorm:"size:8;not null"`
	CurrencyCode               int                      `json:"currency_code" gorm:"not null"`
	LastErrorCode              string                   `json:"last_error_code" gorm:"size:64"`
	LastErrorMessage           string                   `json:"last_error_message" gorm:"type:text"`
	ProviderSnapshotCiphertext string                   `json:"provider_snapshot_ciphertext" gorm:"type:text"`
	ProviderRequestStartedAt   int64                    `json:"provider_request_started_at" gorm:"index"`
	LastReconciledAt           int64                    `json:"last_reconciled_at" gorm:"index;not null;default:0"`
	CreatedAt                  int64                    `json:"created_at" gorm:"index"`
	UpdatedAt                  int64                    `json:"updated_at"`
	Activations                []HeroSMSEmailActivation `json:"activations" gorm:"foreignKey:OrderID;references:ID"`
}

type HeroSMSEmailActivation struct {
	ID                        string  `json:"id" gorm:"primaryKey;size:64"`
	OrderID                   string  `json:"order_id" gorm:"size:64;index;not null"`
	UserID                    int     `json:"user_id" gorm:"index;not null"`
	Slot                      int     `json:"slot" gorm:"not null"`
	Status                    string  `json:"status" gorm:"size:32;index;not null"`
	DomainID                  string  `json:"domain_id" gorm:"size:2048;not null"`
	Site                      string  `json:"site" gorm:"size:255;not null"`
	Domain                    string  `json:"domain" gorm:"size:255;not null"`
	ProviderID                *string `json:"provider_id" gorm:"size:128;uniqueIndex"`
	ProviderEmailCiphertext   string  `json:"provider_email_ciphertext" gorm:"type:text"`
	ProviderCodeCiphertext    string  `json:"provider_code_ciphertext" gorm:"type:text"`
	ProviderMessageCiphertext string  `json:"provider_message_ciphertext" gorm:"type:text"`
	ProviderCostMicros        int64   `json:"provider_cost_micros"`
	ChargeQuota               int     `json:"charge_quota" gorm:"not null"`
	Currency                  string  `json:"currency" gorm:"size:8"`
	CurrencyCode              int     `json:"currency_code"`
	CancelReason              string  `json:"cancel_reason" gorm:"size:64"`
	RefundQuota               int     `json:"refund_quota"`
	RefundedAt                int64   `json:"refunded_at"`
	CancelledAt               int64   `json:"cancelled_at"`
	ReorderOfActivationID     *string `json:"reorder_of_activation_id" gorm:"size:64;index"`
	LastReconciledAt          int64   `json:"last_reconciled_at" gorm:"index;not null;default:0"`
	CreatedAt                 int64   `json:"created_at" gorm:"index"`
	UpdatedAt                 int64   `json:"updated_at"`
}

type HeroSMSProviderPurchaseLease struct {
	Name      string `gorm:"primaryKey;size:64"`
	Holder    string `gorm:"size:64;not null"`
	ExpiresAt int64  `gorm:"index;not null"`
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

// pi-lens-ignore: go-bare-error
func heroSMSClient() (herosms.Client, error) {
	if !heroSMSPurchasingEnabled() || !heroSMSEmailPurchasingEnabled() {
		return nil, newHeroSMSError(http.StatusServiceUnavailable, "NOT_CONFIGURED", "HeroSMS email purchasing is disabled")
	}
	return heroSMSOperationsClient()
}

// pi-lens-ignore: go-bare-error
func heroSMSOperationsClient() (herosms.Client, error) {
	apiKey, err := heroSMSConfiguredAPIKey()
	if err != nil {
		return nil, newHeroSMSError(http.StatusServiceUnavailable, "NOT_CONFIGURED", "HeroSMS encryption key is unavailable")
	}
	if apiKey == "" {
		return nil, newHeroSMSError(http.StatusServiceUnavailable, "NOT_CONFIGURED", "HeroSMS API key is not configured")
	}
	baseURL := herosms.DefaultBaseURL
	if heroSMSBaseURL != herosms.DefaultBaseURL && !isProductionEnv() {
		baseURL = heroSMSBaseURL
	}
	return heroSMSClientFactory(baseURL, apiKey), nil
}

const heroSMSProviderPurchaseLeaseName = "hero_sms_email_purchase"

// pi-lens-ignore: go-bare-error
func acquireHeroSMSProviderPurchaseLease(ctx context.Context) (func(), error) {
	if ctx == nil {
		return nil, errors.New("HeroSMS purchase context is required")
	}
	holder := common.GetUUID()
	seed := HeroSMSProviderPurchaseLease{Name: heroSMSProviderPurchaseLeaseName, Holder: "", ExpiresAt: 0}
	if err := DB.Clauses(clause.OnConflict{DoNothing: true}).Create(&seed).Error; err != nil {
		return nil, fmt.Errorf("initialize HeroSMS purchase lease: %w", err)
	}
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		now := time.Now().Unix()
		result := DB.Model(&HeroSMSProviderPurchaseLease{}).
			Where("name = ? AND (expires_at < ? OR holder = ?)", heroSMSProviderPurchaseLeaseName, now, holder).
			Updates(map[string]any{"holder": holder, "expires_at": now + 120})
		if result.Error != nil {
			return nil, fmt.Errorf("acquire HeroSMS purchase lease: %w", result.Error)
		}
		if result.RowsAffected == 1 {
			release := func() {
				result := DB.Model(&HeroSMSProviderPurchaseLease{}).
					Where("name = ? AND holder = ?", heroSMSProviderPurchaseLeaseName, holder).
					Updates(map[string]any{"holder": "", "expires_at": 0})
				if result.Error != nil {
					common.SysLog(fmt.Sprintf("HeroSMS purchase lease release failed: %T", result.Error))
				}
			}
			return release, nil
		}
		retry := time.NewTimer(25 * time.Millisecond)
		select {
		case <-ctx.Done():
			retry.Stop()
			return nil, fmt.Errorf("wait for HeroSMS purchase lease: %w", ctx.Err())
		case <-deadline.C:
			retry.Stop()
			return nil, newHeroSMSError(http.StatusServiceUnavailable, "UPSTREAM_BUSY", "HeroSMS purchase queue is busy")
		case <-retry.C:
		}
	}
}

func isProductionEnv() bool {
	env := strings.ToLower(strings.TrimSpace(os.Getenv("GIN_MODE")))
	return env == "release"
}

func GetHeroSMSSettingsView() (HeroSMSSettingsView, error) {
	pendingWork, err := hasPendingHeroSMSWork()
	if err != nil {
		return HeroSMSSettingsView{}, fmt.Errorf("inspect pending HeroSMS work: %w", err)
	}
	apiKey, err := heroSMSConfiguredAPIKey()
	if err != nil {
		return HeroSMSSettingsView{}, fmt.Errorf("inspect HeroSMS credential state: %w", err)
	}
	return HeroSMSSettingsView{
		Enabled:          heroSMSPurchasingEnabled(),
		EmailEnabled:     heroSMSEmailPurchasingEnabled(),
		SMSEnabled:       heroSMSSMSPurchasingEnabled(),
		APIKeyConfigured: apiKey != "",
		PendingWork:      pendingWork,
		Currency:         setting.HeroSMSCurrency,
		CurrencyCode:     setting.HeroSMSCurrencyCode,
		PriceMultiplier:  heroSMSMultiplierString(),
	}, nil
}

func UpdateHeroSMSSettings(update HeroSMSSettingsUpdate) error {
	multiplier := heroSMSMultiplierString()
	if strings.TrimSpace(update.PriceMultiplier) != "" {
		parsed, err := decimal.NewFromString(strings.TrimSpace(update.PriceMultiplier))
		if err != nil || parsed.LessThanOrEqual(decimal.Zero) || parsed.GreaterThan(decimal.NewFromInt(1000)) || parsed.Exponent() < -6 {
			return newHeroSMSError(http.StatusBadRequest, "INVALID_REQUEST", "invalid HeroSMS price multiplier")
		}
		multiplier = parsed.String()
	}
	enabled := heroSMSPurchasingEnabled()
	if update.Enabled != nil {
		enabled = *update.Enabled
	}
	emailEnabled := heroSMSEmailPurchasingEnabled()
	if update.EmailEnabled != nil {
		emailEnabled = *update.EmailEnabled
	}
	smsEnabled := heroSMSSMSPurchasingEnabled()
	if update.SMSEnabled != nil {
		smsEnabled = *update.SMSEnabled
	}
	encryptedCredential := ""
	storeAPIKey := false
	effectiveAPIKey, keyErr := heroSMSConfiguredAPIKey()
	if keyErr != nil && strings.TrimSpace(update.APIKey) == "" {
		return newHeroSMSError(http.StatusServiceUnavailable, "NOT_CONFIGURED", "HeroSMS encryption key is unavailable")
	}
	if strings.TrimSpace(update.APIKey) != "" {
		candidateAPIKey := strings.TrimSpace(update.APIKey)
		if keyErr != nil || candidateAPIKey != effectiveAPIKey {
			pending, err := hasPendingHeroSMSWork()
			if err != nil {
				return fmt.Errorf("check HeroSMS work before credential rotation: %w", err)
			}
			if pending {
				return newHeroSMSError(http.StatusConflict, "ACTIVE_ORDERS", "finish or reconcile active HeroSMS orders before replacing the API key")
			}
		}
		effectiveAPIKey = candidateAPIKey
		if len(effectiveAPIKey) < 16 || len(effectiveAPIKey) > 1024 {
			return newHeroSMSError(http.StatusBadRequest, "INVALID_REQUEST", "invalid HeroSMS API key")
		}
		ciphertext, err := common.EncryptPersistentString("hero_sms.api_key", "HERO_SMS_ENCRYPTION_KEY", "CRYPTO_SECRET", effectiveAPIKey)
		if err != nil {
			return newHeroSMSError(http.StatusServiceUnavailable, "NOT_CONFIGURED", "HeroSMS encryption key is not configured")
		}
		encryptedCredential = ciphertext
		storeAPIKey = true
	}
	if enabled {
		if effectiveAPIKey == "" {
			return newHeroSMSError(http.StatusBadRequest, "NOT_CONFIGURED", "configure the HeroSMS API key before enabling the service")
		}
		probe, err := common.EncryptPersistentString("hero_sms.runtime_check", "HERO_SMS_ENCRYPTION_KEY", "CRYPTO_SECRET", "configured")
		if err != nil || probe == "" {
			return newHeroSMSError(http.StatusServiceUnavailable, "NOT_CONFIGURED", "HeroSMS encryption key is not configured")
		}
	}
	updates := map[string]string{
		setting.HeroSMSOptionEnabled:    strconv.FormatBool(enabled),
		setting.HeroSMSOptionEmail:      strconv.FormatBool(emailEnabled),
		setting.HeroSMSOptionSMS:        strconv.FormatBool(smsEnabled),
		setting.HeroSMSOptionCurrency:   setting.HeroSMSCurrency,
		setting.HeroSMSOptionCode:       strconv.Itoa(setting.HeroSMSCurrencyCode),
		setting.HeroSMSOptionMultiplier: multiplier,
	}
	if storeAPIKey {
		updates[setting.HeroSMSOptionAPIKey] = encryptedCredential
	}
	options := make([]Option, 0, len(updates))
	for key, value := range updates {
		options = append(options, Option{Key: key, Value: value})
	}
	if err := DB.WithContext(heroSMSOptionWriteContext()).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "key"}},
			DoUpdates: clause.AssignmentColumns([]string{"value"}),
		}).
		Create(&options).Error; err != nil {
		return fmt.Errorf("persist HeroSMS settings: %w", err)
	}
	updateHeroSMSOptionCache(updates)
	return nil
}

func ClearHeroSMSAPIKey() error {
	if heroSMSPurchasingEnabled() {
		return newHeroSMSError(http.StatusConflict, "INVALID_REQUEST", "disable HeroSMS before clearing the API key")
	}
	if pending, err := hasPendingHeroSMSWork(); err != nil {
		return err
	} else if pending {
		return newHeroSMSError(http.StatusConflict, "ACTIVE_ORDERS", "finish or reconcile active HeroSMS orders before clearing the API key")
	}
	if err := DB.WithContext(heroSMSOptionWriteContext()).Where("key = ?", setting.HeroSMSOptionAPIKey).Delete(&Option{}).Error; err != nil {
		return err
	}
	deleteHeroSMSAPIKeyFromCache()
	return nil
}

func CheckHeroSMSConfiguration(ctx context.Context, candidateAPIKey string) error {
	apiKey := strings.TrimSpace(candidateAPIKey)
	if apiKey == "" {
		var err error
		apiKey, err = heroSMSConfiguredAPIKey()
		if err != nil {
			return newHeroSMSError(http.StatusServiceUnavailable, "NOT_CONFIGURED", "HeroSMS encryption key is unavailable")
		}
	}
	if len(apiKey) < 16 || len(apiKey) > 1024 {
		return newHeroSMSError(http.StatusBadRequest, "NOT_CONFIGURED", "configure a valid HeroSMS API key first")
	}
	baseURL := herosms.DefaultBaseURL
	if heroSMSBaseURL != herosms.DefaultBaseURL && !isProductionEnv() {
		baseURL = heroSMSBaseURL
	}
	client := heroSMSClientFactory(baseURL, apiKey)
	tested := false
	if heroSMSEmailPurchasingEnabled() {
		response, err := client.ListEmails(ctx, 1, 1)
		if err != nil {
			return mapHeroSMSProviderError(err)
		}
		if response == nil {
			return newHeroSMSError(http.StatusBadGateway, "BAD_UPSTREAM_RESPONSE", "HeroSMS returned an empty email response")
		}
		tested = true
	}
	if heroSMSSMSPurchasingEnabled() {
		smsClient, ok := client.(herosms.SMSClient)
		if !ok {
			return newHeroSMSError(http.StatusServiceUnavailable, "NOT_CONFIGURED", "HeroSMS SMS client is unavailable")
		}
		countries, err := smsClient.ListSMSCountries(ctx)
		if err != nil {
			return mapHeroSMSProviderError(err)
		}
		if countries == nil {
			return newHeroSMSError(http.StatusBadGateway, "BAD_UPSTREAM_RESPONSE", "HeroSMS returned an empty SMS response")
		}
		tested = true
	}
	if !tested {
		return newHeroSMSError(http.StatusBadRequest, "NOT_CONFIGURED", "enable at least one HeroSMS activation type")
	}
	return nil
}

func ListHeroSMSEmailProducts(ctx context.Context, page int, size int, site string) (*HeroSMSEmailProductPage, error) {
	normalizedSite, err := normalizeHeroSMSName(site)
	if err != nil {
		return nil, newHeroSMSError(http.StatusBadRequest, "INVALID_REQUEST", "a valid target site is required")
	}
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 50
	}
	client, err := heroSMSClient()
	if err != nil {
		return nil, err
	}
	response, err := client.ListDomains(ctx, normalizedSite)
	if err != nil {
		return nil, mapHeroSMSProviderError(err)
	}
	multiplier, err := heroSMSMultiplierDecimal()
	if err != nil {
		return nil, err
	}
	allProducts := make([]HeroSMSEmailProduct, 0, len(response.Data))
	for _, item := range response.Data {
		domain, nameErr := normalizeHeroSMSName(item.Name)
		if nameErr != nil {
			return nil, newHeroSMSError(http.StatusBadGateway, "BAD_UPSTREAM_RESPONSE", "HeroSMS returned an invalid domain")
		}
		customerPrice := item.CostUSD.Mul(multiplier)
		chargeQuota, chargeErr := heroSMSChargeQuota(customerPrice)
		if chargeErr != nil {
			return nil, chargeErr
		}
		productID, tokenErr := encodeHeroSMSQuoteID(normalizedSite, domain, item.CostUSD, multiplier)
		if tokenErr != nil {
			return nil, newHeroSMSError(http.StatusServiceUnavailable, "NOT_CONFIGURED", "HeroSMS encryption is unavailable")
		}
		// pi-lens-ignore: ast-grep:gorm-n-plus-one
		allProducts = append(allProducts, HeroSMSEmailProduct{
			ID:               productID,
			Site:             normalizedSite,
			Domain:           domain,
			Count:            item.Count,
			Available:        item.Count > 0,
			CustomerPriceUSD: customerPrice.String(),
			ChargeQuota:      chargeQuota,
		})
	}
	start := (page - 1) * size
	if start > len(allProducts) {
		start = len(allProducts)
	}
	end := start + size
	if end > len(allProducts) {
		end = len(allProducts)
	}
	return &HeroSMSEmailProductPage{
		Items: allProducts[start:end],
		Page:  page,
		Size:  size,
		Total: len(allProducts),
	}, nil
}

// pi-lens-ignore: go-bare-error
func CreateHeroSMSEmailActivations(ctx context.Context, userID int, idempotencyKey string, request HeroSMSEmailPurchaseRequest) (*HeroSMSEmailOrderView, int, error) {
	return createHeroSMSEmailOrder(ctx, userID, idempotencyKey, request, "purchase", nil, nil)
}

// pi-lens-ignore: go-bare-error
func ReorderHeroSMSEmailActivation(ctx context.Context, userID int, activationID string, idempotencyKey string, domainID string) (*HeroSMSEmailOrderView, int, error) {
	trimmedKey := strings.TrimSpace(idempotencyKey)
	if trimmedKey == "" || len(trimmedKey) > 128 {
		return nil, 0, newHeroSMSError(http.StatusBadRequest, "INVALID_REQUEST", "a valid Idempotency-Key is required")
	}
	request := HeroSMSEmailPurchaseRequest{DomainID: strings.TrimSpace(domainID), Quantity: 1}
	if request.DomainID == "" {
		return nil, 0, newHeroSMSError(http.StatusBadRequest, "INVALID_REQUEST", "a fresh HeroSMS quote is required")
	}
	payloadHash, err := heroSMSPayloadHash("reorder", request, &activationID)
	if err != nil {
		return nil, 0, err
	}
	idempotencyHash := hashString(fmt.Sprintf("%d:%s:%s", userID, "reorder", trimmedKey))
	if existing, lookupErr := getHeroSMSEmailOrderByIdempotency(userID, "reorder", idempotencyHash); lookupErr == nil {
		if existing.RequestPayloadHash != payloadHash {
			return nil, 0, newHeroSMSError(http.StatusConflict, "IDEMPOTENCY_MISMATCH", "idempotent request payload mismatch")
		}
		if existing.Status == HeroSMSEmailOrderStatusFailed {
			code := strings.TrimSpace(existing.LastErrorCode)
			if code == "" {
				code = "PURCHASE_FAILED"
			}
			return nil, 0, newHeroSMSError(http.StatusConflict, code, "previous idempotent HeroSMS purchase failed")
		}
		view, viewErr := heroSMSEmailOrderView(existing)
		if viewErr != nil {
			return nil, 0, viewErr
		}
		if existing.Status == HeroSMSEmailOrderStatusCompleted {
			return view, http.StatusCreated, nil
		}
		return view, http.StatusAccepted, nil
	} else if !errors.Is(lookupErr, gorm.ErrRecordNotFound) {
		return nil, 0, lookupErr
	}
	activation, err := getHeroSMSEmailActivationForUser(userID, activationID)
	if err != nil {
		return nil, 0, err
	}
	switch activation.Status {
	case HeroSMSEmailActivationStatusCompleted, HeroSMSEmailActivationStatusCancelled, HeroSMSEmailActivationStatusExpired, HeroSMSEmailActivationStatusRefunded:
		// Terminal activations may be used as the source of a new paid reorder.
	default:
		return nil, 0, newHeroSMSError(http.StatusConflict, "INVALID_REQUEST", "only terminal HeroSMS activations can be reordered")
	}
	if activation.ProviderID == nil || strings.TrimSpace(*activation.ProviderID) == "" {
		return nil, 0, newHeroSMSError(http.StatusConflict, "INVALID_REQUEST", "activation cannot be reordered before provider reconciliation")
	}
	quoteToken, err := decodeHeroSMSQuoteID(request.DomainID)
	if err != nil || quoteToken.Site != activation.Site || quoteToken.Domain != activation.Domain {
		return nil, 0, newHeroSMSError(http.StatusConflict, "PRICE_CHANGED", "refresh the reorder quote before continuing")
	}
	return createHeroSMSEmailOrder(ctx, userID, idempotencyKey, request, "reorder", &activation.ID, activation.ProviderID)
}

func createHeroSMSEmailOrder(ctx context.Context, userID int, idempotencyKey string, request HeroSMSEmailPurchaseRequest, operation string, reorderOf *string, reorderProviderID *string) (*HeroSMSEmailOrderView, int, error) {
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey == "" || len(idempotencyKey) > 128 {
		return nil, 0, newHeroSMSError(http.StatusBadRequest, "INVALID_REQUEST", "a valid Idempotency-Key is required")
	}
	if strings.TrimSpace(request.DomainID) == "" || request.Quantity < 1 || request.Quantity > 10 {
		return nil, 0, newHeroSMSError(http.StatusBadRequest, "INVALID_REQUEST", "invalid HeroSMS purchase request")
	}
	payloadHash, err := heroSMSPayloadHash(operation, request, reorderOf)
	if err != nil {
		return nil, 0, err
	}
	idempotencyHash := hashString(fmt.Sprintf("%d:%s:%s", userID, operation, idempotencyKey))
	var existing HeroSMSEmailOrder
	lookupErr := DB.Preload("Activations").Where("user_id = ? AND operation = ? AND idempotency_key_hash = ?", userID, operation, idempotencyHash).First(&existing).Error
	if lookupErr == nil {
		if existing.RequestPayloadHash != payloadHash {
			return nil, 0, newHeroSMSError(http.StatusConflict, "IDEMPOTENCY_MISMATCH", "idempotent request payload mismatch")
		}
		if existing.Status == HeroSMSEmailOrderStatusFailed {
			code := strings.TrimSpace(existing.LastErrorCode)
			if code == "" {
				code = "PURCHASE_FAILED"
			}
			return nil, 0, newHeroSMSError(http.StatusConflict, code, "previous idempotent HeroSMS purchase failed")
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
	if !errors.Is(lookupErr, gorm.ErrRecordNotFound) {
		return nil, 0, lookupErr
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
	if quote.Count < request.Quantity {
		return nil, 0, newHeroSMSError(http.StatusConflict, "PRICE_CHANGED", "HeroSMS inventory changed; refresh the quote")
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
	releaseProviderLease, leaseErr := acquireHeroSMSProviderPurchaseLease(ctx)
	if leaseErr != nil {
		return nil, 0, leaseErr
	}
	defer releaseProviderLease()
	providerSnapshot, snapshotErr := captureHeroSMSProviderSnapshot(ctx, client)
	if snapshotErr != nil {
		return nil, 0, mapHeroSMSProviderError(snapshotErr)
	}
	order := HeroSMSEmailOrder{
		UserID:                     userID,
		Operation:                  operation,
		IdempotencyKeyHash:         idempotencyHash,
		RequestPayloadHash:         payloadHash,
		DomainID:                   request.DomainID,
		Site:                       quote.Site,
		Domain:                     quote.Domain,
		Quantity:                   request.Quantity,
		Status:                     HeroSMSEmailOrderStatusPendingProvider,
		PriceMultiplier:            multiplier.String(),
		ReservedUnitCostMicros:     decimalToMicros(quote.CostUSD),
		ReservedUnitCostDecimal:    quote.CostUSD.String(),
		CustomerUnitPriceMicros:    decimalToMicros(customerUnitPrice),
		ChargeQuota:                chargeQuota,
		Currency:                   setting.HeroSMSCurrency,
		CurrencyCode:               HeroSMSCurrencyCode,
		LastErrorCode:              "PROVIDER_INTENT_PENDING",
		LastErrorMessage:           "provider purchase intent is reserved but not started",
		ProviderSnapshotCiphertext: providerSnapshot,
		ProviderRequestStartedAt:   time.Now().Unix(),
	}
	activations := make([]HeroSMSEmailActivation, 0, request.Quantity)
	for slot := 0; slot < request.Quantity; slot++ {
		activation := HeroSMSEmailActivation{
			UserID: userID, Slot: slot + 1, Status: HeroSMSEmailActivationStatusPendingProvider,
			DomainID: request.DomainID, Site: quote.Site, Domain: quote.Domain,
			ChargeQuota: quotaForSlot(chargeQuota, request.Quantity, slot+1),
		}
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
	if cacheErr := updateUserQuotaCache(userID, newQuota); cacheErr != nil {
		common.SysLog(fmt.Sprintf("HeroSMS quota cache update failed: %T", cacheErr))
	}
	if markErr := markHeroSMSEmailOrderStatus(order.ID, HeroSMSEmailOrderStatusPurchaseUnknown, "PROVIDER_ATTEMPT_STARTED", "provider purchase attempt may have started", HeroSMSEmailActivationStatusReconciling); markErr != nil {
		refundErr := failHeroSMSEmailOrder(&order, newHeroSMSError(http.StatusInternalServerError, "INTERNAL_ERROR", "failed to persist provider request intent"))
		if refundErr != nil {
			return nil, 0, fmt.Errorf("persist provider request intent: %v; refund reservation: %w", markErr, refundErr)
		}
		return nil, 0, markErr
	}
	var purchaseResult *HeroSMSEmailOrderView
	statusCode := http.StatusCreated
	if operation == "reorder" {
		if reorderProviderID == nil || strings.TrimSpace(*reorderProviderID) == "" {
			return nil, 0, newHeroSMSError(http.StatusConflict, "INVALID_REQUEST", "activation cannot be reordered")
		}
		record, purchaseErr := client.ReorderEmail(ctx, *reorderProviderID)
		if purchaseErr != nil {
			return handleHeroSMSPurchaseProviderError(&order, purchaseErr)
		}
		return finalizeHeroSMSKnownPurchase(ctx, client, &order, []herosms.EmailRecord{*record}, false)
	}
	if request.Quantity == 1 {
		record, purchaseErr := client.CreateEmail(ctx, quote.Site, quote.Domain)
		if purchaseErr != nil {
			return handleHeroSMSPurchaseProviderError(&order, purchaseErr)
		}
		purchaseResult, statusCode, err = finalizeHeroSMSKnownPurchase(ctx, client, &order, []herosms.EmailRecord{*record}, false)
		if err != nil {
			return nil, 0, err
		}
		return purchaseResult, statusCode, nil
	}
	batch, purchaseErr := client.CreateEmailBatch(ctx, quote.Site, quote.Domain, request.Quantity)
	if purchaseErr != nil {
		if errors.Is(purchaseErr, herosms.ErrBatchCountMismatch) && batch != nil {
			return handleHeroSMSBatchCountMismatch(ctx, client, &order, batch)
		}
		return handleHeroSMSPurchaseProviderError(&order, purchaseErr)
	}
	purchaseResult, statusCode, err = finalizeHeroSMSKnownPurchase(ctx, client, &order, batch.Items, true)
	if err != nil {
		return nil, 0, err
	}
	return purchaseResult, statusCode, nil
}

// pi-lens-ignore: go-bare-error
func handleHeroSMSBatchCountMismatch(ctx context.Context, client herosms.Client, order *HeroSMSEmailOrder, batch *herosms.BatchPurchaseResult) (*HeroSMSEmailOrderView, int, error) {
	if err := markHeroSMSEmailOrderStatus(order.ID, HeroSMSEmailOrderStatusPurchaseUnknown, "BATCH_COUNT_MISMATCH", "provider batch count requires compensation", HeroSMSEmailActivationStatusReconciling); err != nil {
		return nil, 0, fmt.Errorf("persist HeroSMS batch mismatch: %w", err)
	}
	for _, item := range batch.Items {
		providerID := strings.TrimSpace(item.ID)
		if providerID == "" && strings.TrimSpace(item.Email) != "" {
			listing, err := herosms.FindEmailByExactAddress(ctx, client, item.Email)
			if err != nil || listing == nil {
				return GetHeroSMSEmailOrderViewWithStatus(order.UserID, order.ID, http.StatusAccepted)
			}
			providerID = strings.TrimSpace(listing.ID)
		}
		if providerID == "" {
			return GetHeroSMSEmailOrderViewWithStatus(order.UserID, order.ID, http.StatusAccepted)
		}
		if err := client.DeleteEmail(ctx, providerID); err != nil && !errors.Is(err, herosms.ErrNotFound) {
			return GetHeroSMSEmailOrderViewWithStatus(order.UserID, order.ID, http.StatusAccepted)
		}
	}
	failure := newHeroSMSError(http.StatusBadGateway, "BAD_UPSTREAM_RESPONSE", "HeroSMS returned an unexpected batch count")
	if err := failHeroSMSEmailOrder(order, failure); err != nil {
		return nil, 0, fmt.Errorf("refund mismatched HeroSMS batch: %w", err)
	}
	return nil, 0, failure
}

func reserveHeroSMSEmailQuota(order *HeroSMSEmailOrder, activations []HeroSMSEmailActivation) (int, error) {
	var newQuota int
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		// pi-lens-ignore: ast-grep:gorm-n-plus-one
		err = DB.Transaction(func(tx *gorm.DB) error {
			var user User
			// pi-lens-ignore: ast-grep:gorm-n-plus-one
			if err := lockForUpdate(tx).Select("id", "quota").Where("id = ?", order.UserID).First(&user).Error; err != nil {
				return err
			}
			if user.Quota < order.ChargeQuota {
				return newHeroSMSError(http.StatusPaymentRequired, "INSUFFICIENT_BALANCE", "insufficient quota balance")
			}
			// pi-lens-ignore: ast-grep:gorm-n-plus-one
			update := tx.Model(&User{}).Where("id = ? AND quota >= ?", order.UserID, order.ChargeQuota).UpdateColumn("quota", gorm.Expr("quota - ?", order.ChargeQuota))
			if update.Error != nil {
				return update.Error
			}
			if update.RowsAffected != 1 {
				return newHeroSMSError(http.StatusPaymentRequired, "INSUFFICIENT_BALANCE", "insufficient quota balance")
			}
			// pi-lens-ignore: ast-grep:gorm-n-plus-one
			if err := tx.Create(order).Error; err != nil {
				if uniqueConstraintError(err) {
					return newHeroSMSError(http.StatusConflict, "IDEMPOTENCY_MISMATCH", "duplicate HeroSMS idempotency key")
				}
				return err
			}
			for i := range activations {
				activations[i].OrderID = order.ID
			}
			// pi-lens-ignore: ast-grep:gorm-n-plus-one
			if err := tx.Create(&activations).Error; err != nil {
				return err
			}
			// pi-lens-ignore: ast-grep:gorm-n-plus-one
			if err := tx.Create(&HeroSMSEmailQuotaLedger{UserID: order.UserID, OrderID: order.ID, EntryType: HeroSMSEmailLedgerReserve, AmountQuota: -order.ChargeQuota, IdempotencyKey: "hero_sms:reserve:" + order.ID}).Error; err != nil {
				return err
			}
			newQuota = user.Quota - order.ChargeQuota
			return nil
		})
		if err == nil || !retryableHeroSMSDBError(err) {
			break
		}
		waitHeroSMSRetry(attempt)
	}
	return newQuota, err
}

func runHeroSMSTransaction(operation func(tx *gorm.DB) error) error {
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		err = DB.Transaction(operation)
		if err == nil || !retryableHeroSMSDBError(err) {
			break
		}
		waitHeroSMSRetry(attempt)
	}
	return err
}

func waitHeroSMSRetry(attempt int) {
	timer := time.NewTimer(time.Duration(attempt+1) * 10 * time.Millisecond)
	defer timer.Stop()
	<-timer.C
}

func retryableHeroSMSDBError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "database is locked") ||
		strings.Contains(message, "database table is locked") ||
		strings.Contains(message, "deadlock") ||
		strings.Contains(message, "serialization failure") ||
		strings.Contains(message, "sqlstate 40001") ||
		strings.Contains(message, "sqlstate 40p01")
}

func finalizeHeroSMSKnownPurchase(ctx context.Context, client herosms.Client, order *HeroSMSEmailOrder, records []herosms.EmailRecord, _ bool) (*HeroSMSEmailOrderView, int, error) {
	var activations []HeroSMSEmailActivation
	if err := DB.Where("order_id = ?", order.ID).Order("slot asc").Find(&activations).Error; err != nil {
		return nil, 0, err
	}

	resolved := make([]herosms.EmailRecord, 0, len(records))
	for _, item := range records {
		if item.ID == "" && strings.TrimSpace(item.Email) != "" {
			listing, lookupErr := herosms.FindEmailByExactAddress(ctx, client, item.Email)
			if lookupErr != nil {
				if stateErr := markHeroSMSEmailOrderStatus(order.ID, HeroSMSEmailOrderStatusReconciling, "PURCHASE_PENDING_RECONCILIATION", "purchase requires provider reconciliation", HeroSMSEmailActivationStatusReconciling); stateErr != nil {
					return nil, 0, fmt.Errorf("persist reconciliation state: %w", stateErr)
				}
				return GetHeroSMSEmailOrderViewWithStatus(order.UserID, order.ID, http.StatusAccepted)
			}
			if listing == nil || strings.TrimSpace(listing.ID) == "" {
				resolved = append(resolved, item)
				continue
			}
			detail, detailErr := client.GetEmail(ctx, listing.ID)
			if detailErr != nil {
				if stateErr := markHeroSMSEmailOrderStatus(order.ID, HeroSMSEmailOrderStatusReconciling, "PURCHASE_PENDING_RECONCILIATION", "purchase requires provider reconciliation", HeroSMSEmailActivationStatusReconciling); stateErr != nil {
					return nil, 0, fmt.Errorf("persist reconciliation state: %w", stateErr)
				}
				return GetHeroSMSEmailOrderViewWithStatus(order.UserID, order.ID, http.StatusAccepted)
			}
			resolved = append(resolved, *detail)
			continue
		}
		resolved = append(resolved, item)
	}

	type preparedActivation struct {
		providerID        *string
		emailCiphertext   string
		codeCiphertext    string
		messageCiphertext string
		providerCost      int64
		currencyCode      int
		status            string
		cancelReason      string
		cancelledAt       int64
		refundedAt        int64
		refundQuota       int
	}
	prepared := make([]preparedActivation, len(activations))
	for i := range activations {
		item := preparedActivation{status: HeroSMSEmailActivationStatusReconciling}
		if i >= len(resolved) {
			prepared[i] = item
			continue
		}
		record := resolved[i]
		if record.ID != "" {
			providerID := record.ID
			item.providerID = &providerID
		}
		var encryptErr error
		item.emailCiphertext, encryptErr = encryptHeroSMSPayload(record.Email)
		if encryptErr == nil {
			item.codeCiphertext, encryptErr = encryptHeroSMSPayload(record.Code)
		}
		if encryptErr == nil {
			item.messageCiphertext, encryptErr = encryptHeroSMSPayload(record.Message)
		}
		if encryptErr != nil {
			if stateErr := markHeroSMSEmailOrderStatus(order.ID, HeroSMSEmailOrderStatusReconciling, "ENCRYPTION_UNAVAILABLE", "purchase data could not be persisted", HeroSMSEmailActivationStatusReconciling); stateErr != nil {
				return nil, 0, fmt.Errorf("persist encryption failure state: %w", stateErr)
			}
			return nil, 0, newHeroSMSError(http.StatusServiceUnavailable, "NOT_CONFIGURED", "HeroSMS encryption is unavailable")
		}
		item.providerCost = decimalToMicros(record.CostUSD)
		item.currencyCode = record.CurrencyCode
		if activations[i].Status == HeroSMSEmailActivationStatusCancelPending && activations[i].CancelReason == HeroSMSEmailCancelReasonUser {
			item.status = HeroSMSEmailActivationStatusCancelPending
			item.cancelReason = HeroSMSEmailCancelReasonUser
			prepared[i] = item
			continue
		}
		reason := ""
		if strings.TrimSpace(record.Email) == "" || strings.TrimSpace(record.ID) == "" {
			reason = HeroSMSEmailCancelReasonBadUpstream
		} else if record.CurrencyCode != HeroSMSCurrencyCode {
			reason = HeroSMSEmailCancelReasonCurrencyMismatch
		} else if record.CostUSD.GreaterThan(heroSMSReservedUnitCost(order)) {
			reason = HeroSMSEmailCancelReasonPriceChanged
		}
		if reason == "" {
			item.status = HeroSMSEmailActivationStatusActive
			if strings.TrimSpace(record.Code) != "" || strings.TrimSpace(record.Message) != "" {
				item.status = HeroSMSEmailActivationStatusCompleted
			}
			prepared[i] = item
			continue
		}
		item.cancelReason = reason
		item.refundQuota = activations[i].ChargeQuota
		if item.providerID == nil {
			prepared[i] = item
			continue
		}
		if cancelErr := client.DeleteEmail(ctx, *item.providerID); cancelErr != nil {
			item.status = HeroSMSEmailActivationStatusCancelPending
		} else {
			now := time.Now().Unix()
			item.status = HeroSMSEmailActivationStatusRefunded
			item.cancelledAt = now
			item.refundedAt = now
		}
		prepared[i] = item
	}

	refundedQuota := 0
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		attemptRefundedQuota := 0
		// pi-lens-ignore: ast-grep:gorm-n-plus-one
		err = DB.Transaction(func(tx *gorm.DB) error {
			var freshOrder HeroSMSEmailOrder
			// pi-lens-ignore: ast-grep:gorm-n-plus-one
			if err := lockForUpdate(tx).Where("id = ?", order.ID).First(&freshOrder).Error; err != nil {
				return err
			}
			for i := range activations {
				var fresh HeroSMSEmailActivation
				// pi-lens-ignore: ast-grep:gorm-n-plus-one
				if err := lockForUpdate(tx).Where("id = ? AND order_id = ?", activations[i].ID, freshOrder.ID).First(&fresh).Error; err != nil {
					return err
				}
				if fresh.Status == HeroSMSEmailActivationStatusCancelled || fresh.Status == HeroSMSEmailActivationStatusRefunded {
					continue
				}
				item := prepared[i]
				preserveCancellation := fresh.Status == HeroSMSEmailActivationStatusCancelPending
				fresh.ProviderID = item.providerID
				fresh.ProviderEmailCiphertext = item.emailCiphertext
				fresh.ProviderCodeCiphertext = item.codeCiphertext
				fresh.ProviderMessageCiphertext = item.messageCiphertext
				fresh.ProviderCostMicros = item.providerCost
				fresh.Currency = setting.HeroSMSCurrency
				fresh.CurrencyCode = item.currencyCode
				if !preserveCancellation {
					fresh.Status = item.status
					fresh.CancelReason = item.cancelReason
					fresh.CancelledAt = item.cancelledAt
					fresh.RefundedAt = item.refundedAt
					fresh.RefundQuota = item.refundQuota
				}
				// pi-lens-ignore: ast-grep:gorm-n-plus-one
				if err := tx.Save(&fresh).Error; err != nil {
					return err
				}
				if !preserveCancellation && item.status == HeroSMSEmailActivationStatusRefunded && item.refundQuota > 0 {
					if err := heroSMSRefundActivationTx(tx, &freshOrder, &fresh, item.refundQuota, item.cancelReason); err != nil {
						return err
					}
					attemptRefundedQuota += item.refundQuota
				}
			}
			return aggregateHeroSMSEmailOrderStatusTx(tx, freshOrder.ID)
		})
		if err == nil {
			refundedQuota = attemptRefundedQuota
			break
		}
		if !retryableHeroSMSDBError(err) {
			break
		}
		waitHeroSMSRetry(attempt)
	}
	if err != nil {
		if markErr := markHeroSMSEmailOrderStatus(order.ID, HeroSMSEmailOrderStatusPurchaseUnknown, "PURCHASE_PENDING_RECONCILIATION", "purchase outcome requires provider reconciliation", HeroSMSEmailActivationStatusReconciling); markErr == nil {
			return GetHeroSMSEmailOrderViewWithStatus(order.UserID, order.ID, http.StatusAccepted)
		}
		return nil, 0, err
	}
	if refundedQuota > 0 {
		if cacheErr := refreshHeroSMSUserQuotaCache(order.UserID); cacheErr != nil {
			common.SysLog(fmt.Sprintf("HeroSMS refund cache refresh failed: %T", cacheErr))
		}
	}
	fresh, err := getHeroSMSEmailOrder(order.UserID, order.ID)
	if err != nil {
		return nil, 0, err
	}
	orderView, err := heroSMSEmailOrderView(fresh)
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
	if heroErr, ok := mapped.(*HeroSMSError); ok && (heroErr.Code == "UPSTREAM_TIMEOUT" || heroErr.Code == "UPSTREAM_BUSY" || heroErr.Code == "BAD_UPSTREAM_RESPONSE") {
		if err := markHeroSMSEmailOrderStatus(order.ID, HeroSMSEmailOrderStatusPurchaseUnknown, "PURCHASE_PENDING_RECONCILIATION", "purchase outcome requires provider reconciliation", HeroSMSEmailActivationStatusReconciling); err != nil {
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
	refunded := false
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		// pi-lens-ignore: ast-grep:gorm-n-plus-one
		err = DB.Transaction(func(tx *gorm.DB) error {
			var fresh HeroSMSEmailOrder
			// pi-lens-ignore: ast-grep:gorm-n-plus-one
			if err := lockForUpdate(tx).Where("id = ?", order.ID).First(&fresh).Error; err != nil {
				return err
			}
			if fresh.Status != HeroSMSEmailOrderStatusPendingProvider && fresh.Status != HeroSMSEmailOrderStatusPurchaseUnknown {
				return nil
			}
			fresh.Status = HeroSMSEmailOrderStatusFailed
			if heroErr != nil {
				fresh.LastErrorCode = heroErr.Code
				fresh.LastErrorMessage = heroErr.Message
			}
			// pi-lens-ignore: ast-grep:gorm-n-plus-one
			if err := tx.Save(&fresh).Error; err != nil {
				return err
			}
			var activations []HeroSMSEmailActivation
			// pi-lens-ignore: ast-grep:gorm-n-plus-one
			if err := tx.Where("order_id = ?", fresh.ID).Find(&activations).Error; err != nil {
				return err
			}
			for i := range activations {
				if activations[i].Status == HeroSMSEmailActivationStatusPendingProvider || activations[i].Status == HeroSMSEmailActivationStatusReconciling {
					activations[i].Status = HeroSMSEmailActivationStatusCancelled
					activations[i].CancelledAt = time.Now().Unix()
					// pi-lens-ignore: ast-grep:gorm-n-plus-one
					if err := tx.Save(&activations[i]).Error; err != nil {
						return err
					}
				}
			}
			if err := heroSMSRefundOrderTx(tx, &fresh, fresh.ChargeQuota, "order_failure"); err != nil {
				return err
			}
			refunded = true
			return nil
		})
		if err == nil || !retryableHeroSMSDBError(err) {
			break
		}
		waitHeroSMSRetry(attempt)
	}
	if err == nil && refunded {
		if cacheErr := refreshHeroSMSUserQuotaCache(order.UserID); cacheErr != nil {
			common.SysLog(fmt.Sprintf("HeroSMS refund cache refresh failed: %T", cacheErr))
		}
	}
	return err
}

func heroSMSRefundOrderTx(tx *gorm.DB, order *HeroSMSEmailOrder, quota int, refundKey string) error {
	if quota <= 0 {
		return nil
	}
	ledger := HeroSMSEmailQuotaLedger{UserID: order.UserID, OrderID: order.ID, EntryType: HeroSMSEmailLedgerRefund, AmountQuota: quota, IdempotencyKey: "hero_sms:refund:" + order.ID + ":" + refundKey}
	insert := tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "idempotency_key"}},
		DoNothing: true,
	}).Create(&ledger)
	if insert.Error != nil {
		return insert.Error
	}
	if insert.RowsAffected == 0 {
		return nil
	}
	orderUpdate := tx.Model(&HeroSMSEmailOrder{}).
		Where("id = ? AND refunded_quota + ? <= charge_quota", order.ID, quota).
		UpdateColumn("refunded_quota", gorm.Expr("refunded_quota + ?", quota))
	if orderUpdate.Error != nil {
		return orderUpdate.Error
	}
	if orderUpdate.RowsAffected != 1 {
		return errors.New("HeroSMS refund exceeds reserved quota")
	}
	return tx.Model(&User{}).Where("id = ?", order.UserID).UpdateColumn("quota", gorm.Expr("quota + ?", quota)).Error
}

func heroSMSRefundActivationTx(tx *gorm.DB, order *HeroSMSEmailOrder, activation *HeroSMSEmailActivation, quota int, refundKey string) error {
	if quota <= 0 {
		return nil
	}
	ledger := HeroSMSEmailQuotaLedger{UserID: order.UserID, OrderID: order.ID, ActivationID: activation.ID, EntryType: HeroSMSEmailLedgerRefund, AmountQuota: quota, IdempotencyKey: "hero_sms:refund:" + activation.ID + ":" + refundKey}
	insert := tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "idempotency_key"}},
		DoNothing: true,
	}).Create(&ledger)
	if insert.Error != nil {
		return insert.Error
	}
	if insert.RowsAffected == 0 {
		return nil
	}
	orderUpdate := tx.Model(&HeroSMSEmailOrder{}).
		Where("id = ? AND refunded_quota + ? <= charge_quota", order.ID, quota).
		UpdateColumn("refunded_quota", gorm.Expr("refunded_quota + ?", quota))
	if orderUpdate.Error != nil {
		return orderUpdate.Error
	}
	if orderUpdate.RowsAffected != 1 {
		return errors.New("HeroSMS refund exceeds reserved quota")
	}
	return tx.Model(&User{}).Where("id = ?", order.UserID).UpdateColumn("quota", gorm.Expr("quota + ?", quota)).Error
}

func markHeroSMSEmailOrderStatus(orderID string, status string, errorCode string, errorMessage string, activationStatus string) error {
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		// pi-lens-ignore: ast-grep:gorm-n-plus-one
		err = DB.Transaction(func(tx *gorm.DB) error {
			// pi-lens-ignore: ast-grep:gorm-n-plus-one
			if err := tx.Model(&HeroSMSEmailOrder{}).Where("id = ?", orderID).Updates(map[string]any{"status": status, "last_error_code": errorCode, "last_error_message": errorMessage, "updated_at": time.Now().Unix()}).Error; err != nil {
				return err
			}
			// pi-lens-ignore: ast-grep:gorm-n-plus-one
			return tx.Model(&HeroSMSEmailActivation{}).Where("order_id = ? AND status = ?", orderID, HeroSMSEmailActivationStatusPendingProvider).Updates(map[string]any{"status": activationStatus, "updated_at": time.Now().Unix()}).Error
		})
		if err == nil || !retryableHeroSMSDBError(err) {
			break
		}
		waitHeroSMSRetry(attempt)
	}
	return err
}

// pi-lens-ignore: go-bare-error
func GetHeroSMSEmailOrderView(userID int, orderID string) (*HeroSMSEmailOrderView, error) {
	order, err := getHeroSMSEmailOrder(userID, orderID)
	if err != nil {
		return nil, err
	}
	return heroSMSEmailOrderView(order)
}

func GetHeroSMSEmailOrderViewWithStatus(userID int, orderID string, status int) (*HeroSMSEmailOrderView, int, error) {
	view, err := GetHeroSMSEmailOrderView(userID, orderID)
	return view, status, err
}

func ListHeroSMSEmailActivations(userID int, page int, size int, status string) (*HeroSMSEmailActivationPage, error) {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
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
		view.Message = ""
		views = append(views, *view)
	}
	return &HeroSMSEmailActivationPage{Items: views, Page: page, Size: size, Total: total}, nil
}

// pi-lens-ignore: go-bare-error
func GetCurrentHeroSMSEmailActivation(userID int) (*HeroSMSEmailActivationView, error) {
	var activation HeroSMSEmailActivation
	err := DB.Where("user_id = ? AND status IN ?", userID, []string{
		HeroSMSEmailActivationStatusPendingProvider,
		HeroSMSEmailActivationStatusActive,
		HeroSMSEmailActivationStatusReconciling,
		HeroSMSEmailActivationStatusCancelPending,
	}).Order("created_at desc").First(&activation).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load current HeroSMS activation: %w", err)
	}
	return heroSMSEmailActivationView(&activation)
}

// pi-lens-ignore: go-bare-error
func GetHeroSMSEmailActivation(userID int, activationID string) (*HeroSMSEmailActivationView, error) {
	activation, err := getHeroSMSEmailActivationForUser(userID, activationID)
	if err != nil {
		return nil, err
	}
	return heroSMSEmailActivationView(activation)
}

// pi-lens-ignore: go-bare-error
func RefreshHeroSMSEmailActivation(ctx context.Context, userID int, activationID string) (*HeroSMSEmailActivationView, error) {
	activation, err := getHeroSMSEmailActivationForUser(userID, activationID)
	if err != nil {
		return nil, err
	}
	client, err := heroSMSOperationsClient()
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

// pi-lens-ignore: go-bare-error
func CancelHeroSMSEmailActivation(ctx context.Context, userID int, activationID string) (*HeroSMSEmailActivationView, error) {
	owned, err := getHeroSMSEmailActivationForUser(userID, activationID)
	if err != nil {
		return nil, err
	}
	var providerID string
	alreadyTerminal := false
	err = runHeroSMSTransaction(func(tx *gorm.DB) error {
		var order HeroSMSEmailOrder
		if err := lockForUpdate(tx).Select("id").Where("id = ? AND user_id = ?", owned.OrderID, userID).First(&order).Error; err != nil {
			return err
		}
		var activation HeroSMSEmailActivation
		if err := lockForUpdate(tx).Where("id = ? AND user_id = ?", activationID, userID).First(&activation).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return newHeroSMSError(http.StatusNotFound, "NOT_FOUND", "HeroSMS activation not found")
			}
			return err
		}
		switch activation.Status {
		case HeroSMSEmailActivationStatusCancelled, HeroSMSEmailActivationStatusRefunded, HeroSMSEmailActivationStatusCancelPending:
			alreadyTerminal = true
			return nil
		case HeroSMSEmailActivationStatusPendingProvider, HeroSMSEmailActivationStatusActive, HeroSMSEmailActivationStatusReconciling:
			// Non-terminal states may be cancelled.
		case HeroSMSEmailActivationStatusCompleted, HeroSMSEmailActivationStatusExpired:
			return newHeroSMSError(http.StatusConflict, "INVALID_REQUEST", "terminal HeroSMS activation cannot be cancelled")
		default:
			return newHeroSMSError(http.StatusConflict, "INVALID_REQUEST", "HeroSMS activation cannot be cancelled in its current state")
		}
		if activation.ProviderID != nil {
			providerID = strings.TrimSpace(*activation.ProviderID)
		}
		if err := tx.Model(&HeroSMSEmailActivation{}).Where("id = ?", activation.ID).Updates(map[string]any{
			"status": HeroSMSEmailActivationStatusCancelPending, "cancel_reason": HeroSMSEmailCancelReasonUser, "updated_at": time.Now().Unix(),
		}).Error; err != nil {
			return err
		}
		return aggregateHeroSMSEmailOrderStatusTx(tx, activation.OrderID)
	})
	if err != nil {
		return nil, err
	}
	if alreadyTerminal || providerID == "" {
		return GetHeroSMSEmailActivation(userID, activationID)
	}
	client, err := heroSMSOperationsClient()
	if err != nil {
		return nil, err
	}
	if err := client.DeleteEmail(ctx, providerID); err != nil && !errors.Is(err, herosms.ErrNotFound) {
		return GetHeroSMSEmailActivation(userID, activationID)
	}
	err = runHeroSMSTransaction(func(tx *gorm.DB) error {
		var order HeroSMSEmailOrder
		if err := lockForUpdate(tx).Select("id").Where("id = ? AND user_id = ?", owned.OrderID, userID).First(&order).Error; err != nil {
			return err
		}
		var activation HeroSMSEmailActivation
		if err := lockForUpdate(tx).Where("id = ? AND user_id = ?", activationID, userID).First(&activation).Error; err != nil {
			return err
		}
		if activation.Status != HeroSMSEmailActivationStatusCancelPending || activation.CancelReason != HeroSMSEmailCancelReasonUser {
			return nil
		}
		if err := tx.Model(&HeroSMSEmailActivation{}).Where("id = ?", activation.ID).Updates(map[string]any{
			"status": HeroSMSEmailActivationStatusCancelled, "cancelled_at": time.Now().Unix(), "updated_at": time.Now().Unix(),
		}).Error; err != nil {
			return err
		}
		return aggregateHeroSMSEmailOrderStatusTx(tx, activation.OrderID)
	})
	if err != nil {
		return nil, err
	}
	return GetHeroSMSEmailActivation(userID, activationID)
}

// pi-lens-ignore: go-bare-error
func GetHeroSMSEmailActivationOrderView(userID int, activationID string) (*HeroSMSEmailOrderView, error) {
	activation, err := getHeroSMSEmailActivationForUser(userID, activationID)
	if err != nil {
		return nil, err
	}
	return GetHeroSMSEmailOrderView(userID, activation.OrderID)
}

// pi-lens-ignore: go-bare-error
func reconcileHeroSMSEmailActivation(ctx context.Context, client herosms.Client, activation *HeroSMSEmailActivation) error {
	providerID := ""
	if activation.ProviderID != nil {
		providerID = strings.TrimSpace(*activation.ProviderID)
	}
	if providerID == "" {
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
		providerID = listing.ID
	}
	record, err := client.GetEmail(ctx, providerID)
	if err != nil {
		return mapHeroSMSProviderError(err)
	}
	return persistHeroSMSEmailRecord(activation, record)
}

func aggregateHeroSMSEmailOrderStatusTx(tx *gorm.DB, orderID string) error {
	var order HeroSMSEmailOrder
	if err := lockForUpdate(tx).Where("id = ?", orderID).First(&order).Error; err != nil {
		return err
	}
	var activations []HeroSMSEmailActivation
	if err := tx.Select("status", "provider_id", "provider_email_ciphertext").Where("order_id = ?", orderID).Find(&activations).Error; err != nil {
		return err
	}
	if len(activations) == 0 {
		return nil
	}
	hasPending := false
	allRefunded := true
	unknownProvider := false
	for _, activation := range activations {
		switch activation.Status {
		case HeroSMSEmailActivationStatusPendingProvider, HeroSMSEmailActivationStatusReconciling, HeroSMSEmailActivationStatusCancelPending:
			hasPending = true
		}
		if activation.Status != HeroSMSEmailActivationStatusRefunded {
			allRefunded = false
		}
		if activation.ProviderID == nil && strings.TrimSpace(activation.ProviderEmailCiphertext) == "" {
			unknownProvider = true
		}
	}
	if order.Status == HeroSMSEmailOrderStatusPurchaseUnknown && unknownProvider {
		return nil
	}
	updates := map[string]any{"updated_at": time.Now().Unix()}
	switch {
	case hasPending:
		updates["status"] = HeroSMSEmailOrderStatusReconciling
		updates["last_error_code"] = "PURCHASE_PENDING_RECONCILIATION"
		updates["last_error_message"] = "purchase requires provider reconciliation"
	case allRefunded:
		updates["status"] = HeroSMSEmailOrderStatusFailed
		updates["last_error_code"] = "PURCHASE_COMPENSATED"
		updates["last_error_message"] = "provider purchase was cancelled and refunded"
	default:
		updates["status"] = HeroSMSEmailOrderStatusCompleted
		updates["last_error_code"] = ""
		updates["last_error_message"] = ""
	}
	return tx.Model(&HeroSMSEmailOrder{}).Where("id = ?", orderID).Updates(updates).Error
}

// pi-lens-ignore: go-bare-error
func persistHeroSMSEmailRecord(activation *HeroSMSEmailActivation, record *herosms.EmailRecord) error {
	if record == nil || strings.TrimSpace(record.ID) == "" || strings.TrimSpace(record.Email) == "" {
		return newHeroSMSError(http.StatusBadGateway, "BAD_UPSTREAM_RESPONSE", "HeroSMS returned an incomplete activation")
	}
	emailCiphertext, err := encryptHeroSMSPayload(record.Email)
	if err != nil {
		return err
	}
	codeCiphertext, err := encryptHeroSMSPayload(record.Code)
	if err != nil {
		return err
	}
	messageCiphertext, err := encryptHeroSMSPayload(record.Message)
	if err != nil {
		return err
	}
	status := HeroSMSEmailActivationStatusActive
	switch strings.ToUpper(strings.TrimSpace(record.Status)) {
	case "1", "3", "WAIT":
		status = HeroSMSEmailActivationStatusActive
	case "5", "SUCCESS", "COMPLETE", "COMPLETED":
		status = HeroSMSEmailActivationStatusCompleted
	case "4", "CANCEL", "CANCELED", "CANCELLED":
		status = HeroSMSEmailActivationStatusCancelled
	case "6", "7", "EXPIRE", "EXPIRED":
		status = HeroSMSEmailActivationStatusExpired
	}
	if strings.TrimSpace(record.Code) != "" || strings.TrimSpace(record.Message) != "" {
		status = HeroSMSEmailActivationStatusCompleted
	}
	cancelReason := ""
	refundQuota := 0
	if record.CurrencyCode != HeroSMSCurrencyCode {
		status = HeroSMSEmailActivationStatusCancelPending
		cancelReason = HeroSMSEmailCancelReasonCurrencyMismatch
		refundQuota = activation.ChargeQuota
	} else {
		var order HeroSMSEmailOrder
		if err := DB.Select("id", "reserved_unit_cost_micros", "reserved_unit_cost_decimal").Where("id = ?", activation.OrderID).First(&order).Error; err != nil {
			return err
		}
		if record.CostUSD.GreaterThan(heroSMSReservedUnitCost(&order)) {
			status = HeroSMSEmailActivationStatusCancelPending
			cancelReason = HeroSMSEmailCancelReasonPriceChanged
			refundQuota = activation.ChargeQuota
		}
	}
	updates := map[string]any{
		"status":                      status,
		"provider_id":                 record.ID,
		"provider_email_ciphertext":   emailCiphertext,
		"provider_code_ciphertext":    codeCiphertext,
		"provider_message_ciphertext": messageCiphertext,
		"provider_cost_micros":        decimalToMicros(record.CostUSD),
		"currency":                    setting.HeroSMSCurrency,
		"currency_code":               record.CurrencyCode,
		"cancel_reason":               cancelReason,
		"refund_quota":                refundQuota,
		"updated_at":                  time.Now().Unix(),
	}
	return runHeroSMSTransaction(func(tx *gorm.DB) error {
		var order HeroSMSEmailOrder
		if err := lockForUpdate(tx).Select("id").Where("id = ?", activation.OrderID).First(&order).Error; err != nil {
			return err
		}
		result := tx.Model(&HeroSMSEmailActivation{}).
			Where("id = ? AND status NOT IN ?", activation.ID, []string{
				HeroSMSEmailActivationStatusCancelPending,
				HeroSMSEmailActivationStatusCancelled,
				HeroSMSEmailActivationStatusRefunded,
			}).
			Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		return aggregateHeroSMSEmailOrderStatusTx(tx, activation.OrderID)
	})
}

// pi-lens-ignore: go-bare-error
func captureHeroSMSProviderSnapshot(ctx context.Context, client herosms.Client) (string, error) {
	items, err := listHeroSMSEmailsForReconciliation(ctx, client)
	if err != nil {
		return "", err
	}
	ids := make([]string, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.ID) != "" {
			ids = append(ids, item.ID)
		}
	}
	sort.Strings(ids)
	encoded, err := json.Marshal(ids)
	if err != nil {
		return "", err
	}
	return encryptHeroSMSPayload(string(encoded))
}

func listHeroSMSEmailsForReconciliation(ctx context.Context, client herosms.Client) ([]herosms.EmailListItem, error) {
	const pageSize = 100
	seen := make(map[string]struct{})
	items := make([]herosms.EmailListItem, 0)
	for page := 1; page <= 10; page++ {
		response, err := client.ListEmails(ctx, page, pageSize)
		if err != nil {
			return nil, err
		}
		newItems := 0
		for _, item := range response.Data {
			id := strings.TrimSpace(item.ID)
			if id == "" {
				continue
			}
			if _, exists := seen[id]; exists {
				continue
			}
			seen[id] = struct{}{}
			items = append(items, item)
			newItems++
		}
		if len(response.Data) < pageSize || newItems == 0 {
			break
		}
	}
	return items, nil
}

func reconcileHeroSMSUnknownOrder(ctx context.Context, client herosms.Client, order *HeroSMSEmailOrder) error {
	if order != nil && order.Status == HeroSMSEmailOrderStatusPendingProvider && order.LastErrorCode == "PROVIDER_INTENT_PENDING" {
		return failHeroSMSEmailOrder(order, newHeroSMSError(http.StatusServiceUnavailable, "PROVIDER_INTENT_ABORTED", "provider purchase did not start"))
	}
	if order == nil || strings.TrimSpace(order.ProviderSnapshotCiphertext) == "" {
		return nil
	}
	var activations []HeroSMSEmailActivation
	if err := DB.Where("order_id = ?", order.ID).Order("slot asc").Find(&activations).Error; err != nil {
		return err
	}
	for i := range activations {
		if activations[i].ProviderID != nil || strings.TrimSpace(activations[i].ProviderEmailCiphertext) != "" {
			return nil
		}
	}
	snapshotJSON, err := decryptHeroSMSPayload(order.ProviderSnapshotCiphertext)
	if err != nil {
		return err
	}
	var snapshotIDs []string
	if err := json.Unmarshal([]byte(snapshotJSON), &snapshotIDs); err != nil {
		return err
	}
	known := make(map[string]struct{}, len(snapshotIDs))
	for _, id := range snapshotIDs {
		known[id] = struct{}{}
	}
	items, err := listHeroSMSEmailsForReconciliation(ctx, client)
	if err != nil {
		return mapHeroSMSProviderError(err)
	}
	potential := make([]herosms.EmailListItem, 0, order.Quantity)
	potentialIDs := make([]string, 0, order.Quantity)
	for _, item := range items {
		if _, existedBefore := known[item.ID]; existedBefore {
			continue
		}
		if item.Site != "" && !strings.EqualFold(strings.TrimSpace(item.Site), order.Site) {
			continue
		}
		if !strings.HasSuffix(strings.ToLower(strings.TrimSpace(item.Email)), "@"+strings.ToLower(order.Domain)) {
			continue
		}
		potential = append(potential, item)
		potentialIDs = append(potentialIDs, item.ID)
	}
	claimed := make(map[string]struct{})
	if len(potentialIDs) > 0 {
		var claimedIDs []string
		if err := DB.Model(&HeroSMSEmailActivation{}).
			Where("provider_id IN ? AND order_id <> ?", potentialIDs, order.ID).
			Pluck("provider_id", &claimedIDs).Error; err != nil {
			return err
		}
		for _, id := range claimedIDs {
			claimed[id] = struct{}{}
		}
	}
	candidates := make([]herosms.EmailListItem, 0, order.Quantity)
	for _, item := range potential {
		if _, alreadyClaimed := claimed[item.ID]; !alreadyClaimed {
			candidates = append(candidates, item)
		}
	}
	if len(candidates) != order.Quantity {
		if order.LastErrorCode != "BATCH_COUNT_MISMATCH" {
			return nil
		}
		for _, candidate := range candidates {
			if err := client.DeleteEmail(ctx, candidate.ID); err != nil && !errors.Is(err, herosms.ErrNotFound) {
				return mapHeroSMSProviderError(err)
			}
		}
		failure := newHeroSMSError(http.StatusBadGateway, "BAD_UPSTREAM_RESPONSE", "HeroSMS returned an unexpected batch count")
		return failHeroSMSEmailOrder(order, failure)
	}
	sort.Slice(candidates, func(i int, j int) bool { return candidates[i].ID < candidates[j].ID })
	records := make([]herosms.EmailRecord, 0, len(candidates))
	for _, candidate := range candidates {
		record, detailErr := client.GetEmail(ctx, candidate.ID)
		if detailErr != nil {
			return mapHeroSMSProviderError(detailErr)
		}
		records = append(records, *record)
	}
	view, status, err := finalizeHeroSMSKnownPurchase(ctx, client, order, records, order.Quantity > 1)
	if err != nil {
		return err
	}
	if view == nil || (status != http.StatusCreated && status != http.StatusAccepted) {
		return errors.New("HeroSMS reconciliation returned an invalid local result")
	}
	return nil
}

func RunHeroSMSEmailReconciliationOnce(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		limit = 20
	}
	client, err := heroSMSOperationsClient()
	if err != nil {
		return 0, err
	}
	processed := 0
	var firstErr error
	recordErr := func(candidate error) {
		if candidate != nil && firstErr == nil {
			firstErr = candidate
		}
	}
	orderLimit := limit / 2
	if orderLimit < 1 {
		orderLimit = 1
	}
	var orders []HeroSMSEmailOrder
	if err := DB.Where("status IN ?", []string{HeroSMSEmailOrderStatusPendingProvider, HeroSMSEmailOrderStatusPurchaseUnknown, HeroSMSEmailOrderStatusReconciling}).Order("last_reconciled_at asc, updated_at asc, id asc").Limit(orderLimit).Find(&orders).Error; err != nil {
		return 0, err
	}
	for i := range orders {
		if processed >= limit {
			break
		}
		processed++
		releaseProviderLease, leaseErr := acquireHeroSMSProviderPurchaseLease(ctx)
		if leaseErr != nil {
			recordErr(leaseErr)
			continue
		}
		reconciliationErr := reconcileHeroSMSUnknownOrder(ctx, client, &orders[i])
		releaseProviderLease()
		recordErr(reconciliationErr)
		recordErr(DB.Model(&HeroSMSEmailOrder{}).Where("id = ?", orders[i].ID).UpdateColumn("last_reconciled_at", time.Now().UnixNano()).Error)
	}
	var activations []HeroSMSEmailActivation
	remaining := limit - processed
	if remaining <= 0 {
		return processed, firstErr
	}
	if err := DB.Where("status IN ?", []string{HeroSMSEmailActivationStatusActive, HeroSMSEmailActivationStatusReconciling, HeroSMSEmailActivationStatusCancelPending}).Order("last_reconciled_at asc, updated_at asc, id asc").Limit(remaining).Find(&activations).Error; err != nil {
		return processed, err
	}
	for i := range activations {
		processed++
		activation := activations[i]
		switch activation.Status {
		case HeroSMSEmailActivationStatusActive, HeroSMSEmailActivationStatusReconciling:
			recordErr(reconcileHeroSMSEmailActivation(ctx, client, &activation))
		case HeroSMSEmailActivationStatusCancelPending:
			if activation.ProviderID == nil || strings.TrimSpace(*activation.ProviderID) == "" {
				recordErr(reconcileHeroSMSEmailActivation(ctx, client, &activation))
				break
			}
			deleteErr := client.DeleteEmail(ctx, *activation.ProviderID)
			if deleteErr != nil && !errors.Is(deleteErr, herosms.ErrNotFound) {
				recordErr(mapHeroSMSProviderError(deleteErr))
				break
			}
			refunded := false
			// pi-lens-ignore: ast-grep:gorm-n-plus-one
			transactionErr := runHeroSMSTransaction(func(tx *gorm.DB) error {
				var order HeroSMSEmailOrder
				// pi-lens-ignore: ast-grep:gorm-n-plus-one
				if err := lockForUpdate(tx).Where("id = ?", activation.OrderID).First(&order).Error; err != nil {
					return err
				}
				var fresh HeroSMSEmailActivation
				// pi-lens-ignore: ast-grep:gorm-n-plus-one
				if err := lockForUpdate(tx).Where("id = ?", activation.ID).First(&fresh).Error; err != nil {
					return err
				}
				if fresh.Status != HeroSMSEmailActivationStatusCancelPending {
					return nil
				}
				compensate := fresh.CancelReason == HeroSMSEmailCancelReasonPriceChanged || fresh.CancelReason == HeroSMSEmailCancelReasonCurrencyMismatch || fresh.CancelReason == HeroSMSEmailCancelReasonBadUpstream
				updates := map[string]any{"status": HeroSMSEmailActivationStatusCancelled, "cancelled_at": time.Now().Unix(), "updated_at": time.Now().Unix()}
				if compensate {
					refundQuota := fresh.RefundQuota
					if refundQuota <= 0 {
						refundQuota = fresh.ChargeQuota
					}
					updates["status"] = HeroSMSEmailActivationStatusRefunded
					updates["refunded_at"] = time.Now().Unix()
					updates["refund_quota"] = refundQuota
					// pi-lens-ignore: ast-grep:gorm-n-plus-one
					if err := tx.Model(&HeroSMSEmailActivation{}).Where("id = ?", fresh.ID).Updates(updates).Error; err != nil {
						return err
					}
					if err := heroSMSRefundActivationTx(tx, &order, &fresh, refundQuota, fresh.CancelReason); err != nil {
						return err
					}
					refunded = true
					// pi-lens-ignore: ast-grep:gorm-n-plus-one
				} else if err := tx.Model(&HeroSMSEmailActivation{}).Where("id = ?", fresh.ID).Updates(updates).Error; err != nil {
					return err
				}
				return aggregateHeroSMSEmailOrderStatusTx(tx, fresh.OrderID)
			})
			recordErr(transactionErr)
			if transactionErr == nil && refunded {
				recordErr(refreshHeroSMSUserQuotaCache(activation.UserID))
			}
		}
		recordErr(DB.Model(&HeroSMSEmailActivation{}).Where("id = ?", activation.ID).UpdateColumn("last_reconciled_at", time.Now().UnixNano()).Error)
	}
	return processed, firstErr
}

func hasPendingHeroSMSWork() (bool, error) {
	emailPending, err := HasPendingHeroSMSEmailReconciliationWork()
	if err != nil {
		return false, err
	}
	smsPending, err := HasPendingHeroSMSSMSWork()
	if err != nil {
		return false, err
	}
	return emailPending || smsPending, nil
}

func HasPendingHeroSMSSMSReconciliationWork() (bool, error) {
	var count int64
	err := DB.Model(&HeroSMSSMSOrder{}).Where("status = ?", HeroSMSSMSOrderStatusPurchaseUnknown).Count(&count).Error
	return count > 0, err
}

func HasPendingHeroSMSEmailReconciliationWork() (bool, error) {
	var count int64
	err := DB.Model(&HeroSMSEmailActivation{}).Where("status IN ?", []string{HeroSMSEmailActivationStatusPendingProvider, HeroSMSEmailActivationStatusActive, HeroSMSEmailActivationStatusReconciling, HeroSMSEmailActivationStatusCancelPending}).Count(&count).Error
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
		Site:             order.Site,
		Domain:           order.Domain,
		Quantity:         order.Quantity,
		CustomerPriceUSD: microsToDecimal(order.CustomerUnitPriceMicros).StringFixed(6),
		ChargeQuota:      order.ChargeQuota,
		RefundedQuota:    order.RefundedQuota,
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
	return &HeroSMSEmailActivationView{
		ID:           activation.ID,
		OrderID:      activation.OrderID,
		Status:       activation.Status,
		DomainID:     activation.DomainID,
		Site:         activation.Site,
		Domain:       activation.Domain,
		Email:        email,
		Code:         code,
		Message:      message,
		ChargeQuota:  activation.ChargeQuota,
		CancelReason: activation.CancelReason,
		CreatedAt:    activation.CreatedAt,
		UpdatedAt:    activation.UpdatedAt,
	}, nil
}

type heroSMSDomainQuote struct {
	ID           string
	Site         string
	Domain       string
	Count        int
	CostUSD      decimal.Decimal
	Currency     string
	CurrencyCode int
}

type heroSMSQuoteToken struct {
	Site       string `json:"s"`
	Domain     string `json:"d"`
	CostUSD    string `json:"c"`
	Multiplier string `json:"m"`
	IssuedAt   int64  `json:"iat"`
}

// pi-lens-ignore: go-bare-error
func lookupHeroSMSDomainQuote(ctx context.Context, client herosms.Client, domainID string) (*heroSMSDomainQuote, error) {
	token, err := decodeHeroSMSQuoteID(domainID)
	if err != nil {
		return nil, newHeroSMSError(http.StatusBadRequest, "INVALID_REQUEST", "invalid HeroSMS product id")
	}
	now := time.Now().Unix()
	if token.IssuedAt > now+60 || now-token.IssuedAt > 5*60 {
		return nil, newHeroSMSError(http.StatusConflict, "PRICE_CHANGED", "HeroSMS quote expired; refresh the quote")
	}
	quotedCost, err := decimal.NewFromString(token.CostUSD)
	if err != nil || quotedCost.IsNegative() {
		return nil, newHeroSMSError(http.StatusBadRequest, "INVALID_REQUEST", "invalid HeroSMS quote")
	}
	quotedMultiplier, err := decimal.NewFromString(token.Multiplier)
	if err != nil || quotedMultiplier.LessThanOrEqual(decimal.Zero) {
		return nil, newHeroSMSError(http.StatusBadRequest, "INVALID_REQUEST", "invalid HeroSMS quote")
	}
	currentMultiplier, err := heroSMSMultiplierDecimal()
	if err != nil {
		return nil, err
	}
	if !currentMultiplier.Equal(quotedMultiplier) {
		return nil, newHeroSMSError(http.StatusConflict, "PRICE_CHANGED", "HeroSMS price changed; refresh the quote")
	}
	products, err := client.ListDomains(ctx, token.Site)
	if err != nil {
		return nil, mapHeroSMSProviderError(err)
	}
	for _, item := range products.Data {
		candidate, nameErr := normalizeHeroSMSName(item.Name)
		if nameErr == nil && candidate == token.Domain && item.Count > 0 {
			if !item.CostUSD.Equal(quotedCost) {
				return nil, newHeroSMSError(http.StatusConflict, "PRICE_CHANGED", "HeroSMS price changed; refresh the quote")
			}
			return &heroSMSDomainQuote{
				ID:           domainID,
				Site:         token.Site,
				Domain:       token.Domain,
				Count:        item.Count,
				CostUSD:      item.CostUSD,
				Currency:     setting.HeroSMSCurrency,
				CurrencyCode: HeroSMSCurrencyCode,
			}, nil
		}
	}
	return nil, newHeroSMSError(http.StatusNotFound, "NOT_FOUND", "HeroSMS domain is unavailable")
}

func encodeHeroSMSQuoteID(site string, domain string, cost decimal.Decimal, multiplier decimal.Decimal) (string, error) {
	payload, err := json.Marshal(heroSMSQuoteToken{Site: site, Domain: domain, CostUSD: cost.String(), Multiplier: multiplier.String(), IssuedAt: time.Now().Unix()})
	if err != nil {
		return "", err
	}
	ciphertext, err := common.EncryptPersistentString("hero_sms.quote", "HERO_SMS_ENCRYPTION_KEY", "CRYPTO_SECRET", string(payload))
	if err != nil {
		return "", err
	}
	return "hsq_" + base64.RawURLEncoding.EncodeToString([]byte(ciphertext)), nil
}

func decodeHeroSMSQuoteID(value string) (*heroSMSQuoteToken, error) {
	if !strings.HasPrefix(value, "hsq_") || len(value) > 2048 {
		return nil, errors.New("invalid product id")
	}
	encoded, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, "hsq_"))
	if err != nil {
		return nil, err
	}
	plaintext, err := common.DecryptPersistentString("hero_sms.quote", "HERO_SMS_ENCRYPTION_KEY", "CRYPTO_SECRET", string(encoded))
	if err != nil {
		return nil, err
	}
	var token heroSMSQuoteToken
	if err := json.Unmarshal([]byte(plaintext), &token); err != nil {
		return nil, err
	}
	token.Site, err = normalizeHeroSMSName(token.Site)
	if err != nil {
		return nil, err
	}
	token.Domain, err = normalizeHeroSMSName(token.Domain)
	if err != nil {
		return nil, err
	}
	return &token, nil
}

func normalizeHeroSMSName(value string) (string, error) {
	normalized := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(value), "."))
	if normalized == "" || len(normalized) > 253 || strings.HasPrefix(normalized, ".") || strings.HasSuffix(normalized, ".") || strings.Contains(normalized, "..") {
		return "", errors.New("invalid name")
	}
	for _, character := range normalized {
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '.' || character == '-' {
			continue
		}
		return "", errors.New("invalid name")
	}
	return normalized, nil
}

// pi-lens-ignore: go-bare-error
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
	return heroSMSOptionValue(setting.HeroSMSOptionMultiplier, setting.HeroSMSPriceMultiplier)
}

func heroSMSMultiplierDecimal() (decimal.Decimal, error) {
	value, err := decimal.NewFromString(heroSMSMultiplierString())
	if err != nil || value.LessThanOrEqual(decimal.Zero) {
		return decimal.Zero, newHeroSMSError(http.StatusBadRequest, "INVALID_REQUEST", "invalid HeroSMS price multiplier")
	}
	return value, nil
}

// pi-lens-ignore: go-bare-error
func heroSMSChargeQuota(priceUSD decimal.Decimal) (int, error) {
	quotaUnit, err := decimal.NewFromString(strconv.FormatFloat(common.QuotaPerUnit, 'f', -1, 64))
	if err != nil {
		return 0, err
	}
	return common.QuotaFromDecimalStrict(priceUSD.Mul(quotaUnit).Ceil())
}

func heroSMSReservedUnitCost(order *HeroSMSEmailOrder) decimal.Decimal {
	if order != nil && strings.TrimSpace(order.ReservedUnitCostDecimal) != "" {
		if value, err := decimal.NewFromString(order.ReservedUnitCostDecimal); err == nil {
			return value
		}
	}
	if order == nil {
		return decimal.Zero
	}
	return microsToDecimal(order.ReservedUnitCostMicros)
}

func decimalToMicros(value decimal.Decimal) int64 {
	return value.Shift(6).RoundCeil(0).IntPart()
}

func microsToDecimal(value int64) decimal.Decimal {
	return decimal.NewFromInt(value).Shift(-6)
}

// pi-lens-ignore: go-bare-error
func encryptHeroSMSPayload(value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", nil
	}
	return common.EncryptPersistentString("hero_sms.payload", "HERO_SMS_ENCRYPTION_KEY", "CRYPTO_SECRET", value)
}

// pi-lens-ignore: go-bare-error
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
	case errors.Is(err, herosms.ErrNoSMSNumbersAvailable):
		return newHeroSMSError(http.StatusConflict, "PRICE_CHANGED", "HeroSMS has no matching phone numbers")
	case errors.Is(err, herosms.ErrProviderBalanceInsufficient):
		return newHeroSMSError(http.StatusServiceUnavailable, "UPSTREAM_BUSY", "HeroSMS provider balance is insufficient")
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

func quotaForSlot(total int, quantity int, slot int) int {
	if quantity <= 1 {
		return total
	}
	base := total / quantity
	remainder := total % quantity
	if slot >= 1 && slot <= remainder {
		return base + 1
	}
	return base
}

// pi-lens-ignore: go-bare-error
func refreshHeroSMSUserQuotaCache(userID int) error {
	var user User
	if err := DB.Select("id", "quota").Where("id = ?", userID).First(&user).Error; err != nil {
		return err
	}
	return updateUserQuotaCache(userID, user.Quota)
}
