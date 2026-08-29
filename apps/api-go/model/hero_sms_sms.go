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
	HeroSMSSMSOrderStatusCancelPending   = "cancel_pending"
	HeroSMSSMSOrderStatusCompleted       = "completed"
	HeroSMSSMSOrderStatusCancelled       = "cancelled"
	HeroSMSSMSOrderStatusFailed          = "failed"

	HeroSMSSMSLedgerReserve = "reserve"
	HeroSMSSMSLedgerRefund  = "refund"
	HeroSMSSMSTaskType      = "hero_sms_sms_reconciliation"

	HeroSMSSMSComplaintStatusSubmitting    = "submitting"
	HeroSMSSMSComplaintStatusSubmitted     = "submitted"
	HeroSMSSMSComplaintStatusSubmitUnknown = "submit_unknown"
	HeroSMSSMSComplaintStatusFailed        = "failed"
	HeroSMSSMSComplaintStatusClosedCode    = "closed_code"
	HeroSMSSMSComplaintStatusClosedRefund  = "closed_refund"

	// typos:ignore DISMATCH -- HeroSMS's official complaint enum uses this spelling.
	HeroSMSSMSComplaintNumberBlocked      = "NUMBER_BLOCKED"
	HeroSMSSMSComplaintNumberInUse        = "NUMBER_ALREADY_IN_USE"
	HeroSMSSMSComplaintCodeMismatch       = "SMS_CODE_DISMATCH"
	HeroSMSSMSComplaintNotReceived        = "SMS_NOT_RECEIVED"
	HeroSMSSMSComplaintCodeSentToApp      = "CODE_SENT_TO_APP"
	HeroSMSSMSComplaintIncomingCallNumber = "INCOMING_CALL_NUMBER"
	HeroSMSSMSComplaintIncomingCallVoice  = "INCOMING_CALL_VOICE"

	heroSMSSMSQuoteVersion         = 2
	heroSMSSMSQuoteTTL             = 2 * time.Minute
	heroSMSSMSUnknownWindow        = 15 * time.Minute
	heroSMSSMSRecentCodeWindow     = 15 * time.Minute
	heroSMSSMSComplaintWait        = 2 * time.Minute
	heroSMSSMSComplaintRetryDelay  = 30 * time.Second
	heroSMSSMSComplaintMaxAttempts = 3
	heroSMSSMSCurrentOrdersLimit   = 20
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
	ComplaintType              string  `json:"complaint_type" gorm:"size:64"`
	ComplaintStatus            string  `json:"complaint_status" gorm:"size:32;index"`
	ComplaintSubmittedAt       int64   `json:"complaint_submitted_at" gorm:"index"`
	ComplaintSubmitAttempts    int     `json:"complaint_submit_attempts"`
	ComplaintNextRetryAt       int64   `json:"complaint_next_retry_at" gorm:"index"`
	ComplaintLastCheckedAt     int64   `json:"complaint_last_checked_at" gorm:"index"`
	ProviderCancelAcceptedAt   int64   `json:"provider_cancel_accepted_at"`
	CancelFinalStatus          string  `json:"cancel_final_status" gorm:"size:32"`
	CancelErrorCode            string  `json:"cancel_error_code" gorm:"size:64"`
	CancelErrorMessage         string  `json:"cancel_error_message" gorm:"type:text"`
	LastErrorCode              string  `json:"last_error_code" gorm:"size:64"`
	LastErrorMessage           string  `json:"last_error_message" gorm:"type:text"`
	ProviderRequestStartedAt   int64   `json:"provider_request_started_at" gorm:"index"`
	ProviderExpiresAt          int64   `json:"provider_expires_at" gorm:"index;not null;default:0"`
	CompletedAt                *int64  `json:"completed_at"`
	HistoryHiddenAt            int64   `json:"history_hidden_at" gorm:"index;not null;default:0"`
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
	ID          int    `json:"id"`
	Name        string `json:"name"`
	EnglishName string `json:"english_name"`
	ChineseName string `json:"chinese_name"`
	Popularity  int64  `json:"popularity"`
}

type HeroSMSSMSServiceView struct {
	Code       string `json:"code"`
	Name       string `json:"name"`
	Popularity int64  `json:"popularity"`
}

type heroSMSSMSCountryPopularity struct {
	CountryID  int
	Popularity int64
}

type heroSMSSMSServicePopularity struct {
	Service    string
	Popularity int64
}

var heroSMSSMSIdempotencyMissHook func()

type HeroSMSSMSPriceTierView struct {
	ID               string `json:"id"`
	Inventory        int    `json:"inventory"`
	CustomerPriceUSD string `json:"customer_price_usd"`
	ChargeQuota      int    `json:"charge_quota"`
}

type HeroSMSSMSOfferView struct {
	ID               string                    `json:"id"`
	CountryID        int                       `json:"country_id"`
	Service          string                    `json:"service"`
	Operator         string                    `json:"operator"`
	Inventory        int                       `json:"inventory"`
	CustomerPriceUSD string                    `json:"customer_price_usd"`
	ChargeQuota      int                       `json:"charge_quota"`
	Bid              bool                      `json:"bid"`
	Tiers            []HeroSMSSMSPriceTierView `json:"tiers"`
}

type HeroSMSSMSPurchaseRequest struct {
	OfferID string `json:"offer_id"`
}

type HeroSMSSMSOrderView struct {
	ID                   string  `json:"id"`
	CountryID            int     `json:"country_id"`
	Service              string  `json:"service"`
	Operator             string  `json:"operator"`
	Status               string  `json:"status"`
	CustomerPriceUSD     string  `json:"customer_price_usd"`
	ChargeQuota          int     `json:"charge_quota"`
	RefundedQuota        int     `json:"refunded_quota"`
	ProviderID           *string `json:"provider_id"`
	CanCancel            bool    `json:"can_cancel"`
	CanComplain          bool    `json:"can_complain"`
	ComplaintType        string  `json:"complaint_type"`
	ComplaintStatus      string  `json:"complaint_status"`
	ComplaintSubmittedAt int64   `json:"complaint_submitted_at"`
	PhoneNumber          string  `json:"phone_number"`
	Code                 string  `json:"code"`
	Message              string  `json:"message"`
	LastErrorCode        string  `json:"last_error_code"`
	LastErrorMessage     string  `json:"last_error_message"`
	CreatedAt            int64   `json:"created_at"`
	UpdatedAt            int64   `json:"updated_at"`
	ExpiresAt            int64   `json:"expires_at,omitempty"`
}

type HeroSMSSMSOrderPage struct {
	Items []HeroSMSSMSOrderView `json:"items"`
	Page  int                   `json:"page"`
	Size  int                   `json:"size"`
	Total int64                 `json:"total"`
}

type heroSMSSMSQuoteToken struct {
	Version      int    `json:"version"`
	UserID       int    `json:"user_id"`
	CountryID    int    `json:"country_id"`
	Service      string `json:"service"`
	Operator     string `json:"operator"`
	CostCNY      string `json:"cost_cny"`
	Multiplier   string `json:"multiplier"`
	CurrencyCode int    `json:"currency_code"`
	IssuedAt     int64  `json:"issued_at"`
	Bid          bool   `json:"bid,omitempty"`
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

func GetHeroSMSSMSCountries(ctx context.Context, service string) ([]HeroSMSSMSCountryView, error) {
	service = strings.TrimSpace(service)
	if len(service) > 64 {
		return nil, newHeroSMSError(http.StatusBadRequest, "INVALID_REQUEST", "invalid HeroSMS service")
	}
	client, err := heroSMSSMSClient()
	if err != nil {
		return nil, err
	}
	countries, err := client.ListSMSCountries(ctx)
	if err != nil {
		return nil, mapHeroSMSProviderError(err)
	}
	popularity, err := heroSMSSMSCountryPopularityCounts(service)
	if err != nil {
		return nil, err
	}
	views := make([]HeroSMSSMSCountryView, 0, len(countries))
	for _, country := range countries {
		if !country.Visible || strings.TrimSpace(country.Name) == "" {
			continue
		}
		views = append(views, HeroSMSSMSCountryView{
			ID:          country.ID,
			Name:        strings.TrimSpace(country.Name),
			EnglishName: strings.TrimSpace(country.EnglishName),
			ChineseName: strings.TrimSpace(country.ChineseName),
			Popularity:  popularity[country.ID],
		})
	}
	sort.SliceStable(views, func(i, j int) bool {
		if views[i].Popularity != views[j].Popularity {
			return views[i].Popularity > views[j].Popularity
		}
		return views[i].ID < views[j].ID
	})
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
	popularity, err := heroSMSSMSServicePopularityCounts()
	if err != nil {
		return nil, err
	}
	views := make([]HeroSMSSMSServiceView, 0, len(services))
	for _, service := range services {
		if strings.TrimSpace(service.Code) == "" || strings.TrimSpace(service.Name) == "" {
			continue
		}
		code := strings.TrimSpace(service.Code)
		views = append(views, HeroSMSSMSServiceView{
			Code:       code,
			Name:       strings.TrimSpace(service.Name),
			Popularity: popularity[code],
		})
	}
	sort.SliceStable(views, func(i, j int) bool {
		if views[i].Popularity != views[j].Popularity {
			return views[i].Popularity > views[j].Popularity
		}
		return views[i].Code < views[j].Code
	})
	return views, nil
}

func heroSMSSMSCountryPopularityCounts(service string) (map[int]int64, error) {
	var rows []heroSMSSMSCountryPopularity
	query := DB.Model(&HeroSMSSMSOrder{}).
		Select("country_id, COUNT(*) AS popularity").
		Where("status IN ?", []string{HeroSMSSMSOrderStatusActive, HeroSMSSMSOrderStatusCompleted})
	if normalizedService := strings.TrimSpace(service); normalizedService != "" {
		query = query.Where("service = ?", normalizedService)
	}
	err := query.Group("country_id").Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	counts := make(map[int]int64, len(rows))
	for _, row := range rows {
		counts[row.CountryID] = row.Popularity
	}
	return counts, nil
}

func heroSMSSMSServicePopularityCounts() (map[string]int64, error) {
	var rows []heroSMSSMSServicePopularity
	err := DB.Model(&HeroSMSSMSOrder{}).
		Select("service, COUNT(*) AS popularity").
		Where("status IN ?", []string{HeroSMSSMSOrderStatusActive, HeroSMSSMSOrderStatusCompleted}).
		Group("service").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	counts := make(map[string]int64, len(rows))
	for _, row := range rows {
		counts[row.Service] = row.Popularity
	}
	return counts, nil
}

func ListHeroSMSSMSOperators(ctx context.Context, countryID int) ([]string, error) {
	client, err := heroSMSSMSClient()
	if err != nil {
		return nil, err
	}
	if countryID < 0 {
		return nil, newHeroSMSError(http.StatusBadRequest, "INVALID_REQUEST", "invalid HeroSMS country")
	}
	operators, err := client.ListSMSOperators(ctx, countryID)
	if err != nil {
		return nil, mapHeroSMSProviderError(err)
	}
	return operators, nil
}

// pi-lens-ignore: go-bare-error
func GetHeroSMSSMSOffer(ctx context.Context, userID int, countryID int, service string, operator string) (*HeroSMSSMSOfferView, error) {
	return getHeroSMSSMSOffer(ctx, userID, countryID, service, operator, "")
}

// pi-lens-ignore: go-bare-error
func GetHeroSMSSMSBidOffer(ctx context.Context, userID int, countryID int, service string, operator string, maxCustomerPriceUSD string) (*HeroSMSSMSOfferView, error) {
	if strings.TrimSpace(maxCustomerPriceUSD) == "" {
		return nil, newHeroSMSError(http.StatusBadRequest, "INVALID_REQUEST", "invalid HeroSMS SMS maximum bid")
	}
	return getHeroSMSSMSOffer(ctx, userID, countryID, service, operator, maxCustomerPriceUSD)
}

func parseHeroSMSSMSBidPrice(value string) (decimal.Decimal, bool) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 32 || strings.ContainsAny(value, "eE+-") {
		return decimal.Zero, false
	}
	digits := 0
	fractionDigits := 0
	seenDecimalPoint := false
	for _, character := range value {
		if character == '.' && !seenDecimalPoint {
			seenDecimalPoint = true
			continue
		}
		if character < '0' || character > '9' {
			return decimal.Zero, false
		}
		digits++
		if seenDecimalPoint {
			fractionDigits++
		}
	}
	if digits == 0 || fractionDigits > 6 {
		return decimal.Zero, false
	}
	price, err := decimal.NewFromString(value)
	if err != nil || price.LessThanOrEqual(decimal.Zero) || price.GreaterThan(decimal.NewFromInt(1_000_000)) {
		return decimal.Zero, false
	}
	return price, true
}

func getHeroSMSSMSOffer(ctx context.Context, userID int, countryID int, service string, operator string, maxCustomerPriceUSD string) (*HeroSMSSMSOfferView, error) {
	client, err := heroSMSSMSClient()
	if err != nil {
		return nil, err
	}
	service = strings.TrimSpace(service)
	operator = strings.TrimSpace(operator)
	if strings.EqualFold(operator, "any") {
		operator = ""
	}
	maxCustomerPriceUSD = strings.TrimSpace(maxCustomerPriceUSD)
	if userID <= 0 || countryID < 0 || service == "" || len(service) > 64 || len(operator) > 64 || len(maxCustomerPriceUSD) > 64 {
		return nil, newHeroSMSError(http.StatusBadRequest, "INVALID_REQUEST", "invalid HeroSMS SMS offer request")
	}
	if operator != "" {
		operators, operatorErr := client.ListSMSOperators(ctx, countryID)
		if operatorErr != nil {
			return nil, mapHeroSMSProviderError(operatorErr)
		}
		canonical := ""
		for _, candidate := range operators {
			if strings.EqualFold(strings.TrimSpace(candidate), operator) {
				canonical = strings.TrimSpace(candidate)
				break
			}
		}
		if canonical == "" {
			return nil, newHeroSMSError(http.StatusBadRequest, "INVALID_REQUEST", "invalid HeroSMS operator")
		}
		operator = canonical
	}
	offer, err := client.GetSMSOffer(ctx, countryID, service)
	if err != nil {
		return nil, mapHeroSMSProviderError(err)
	}
	if len(offer.Tiers) == 0 {
		return nil, newHeroSMSError(http.StatusConflict, "PRICE_CHANGED", "HeroSMS has no available SMS price tiers")
	}
	multiplier, err := heroSMSMultiplierDecimal()
	if err != nil {
		return nil, err
	}
	issuedAt := time.Now().Unix()
	tiers := make([]HeroSMSSMSPriceTierView, 0, len(offer.Tiers))
	// pi-lens-ignore: gorm-n-plus-one -- this loop encrypts quote tokens and performs no database calls.
	for _, tier := range offer.Tiers {
		if tier.Count <= 0 || tier.Price.LessThanOrEqual(decimal.Zero) {
			continue
		}
		customerPrice := tier.Price.Mul(multiplier)
		chargeQuota, chargeErr := heroSMSChargeQuota(customerPrice)
		if chargeErr != nil {
			return nil, chargeErr
		}
		quoteID, quoteErr := encodeHeroSMSSMSQuote(heroSMSSMSQuoteToken{
			Version:      heroSMSSMSQuoteVersion,
			UserID:       userID,
			CountryID:    countryID,
			Service:      service,
			Operator:     operator,
			CostCNY:      tier.Price.String(),
			Multiplier:   multiplier.String(),
			CurrencyCode: setting.HeroSMSCurrencyCode,
			IssuedAt:     issuedAt,
		})
		if quoteErr != nil {
			return nil, newHeroSMSError(http.StatusServiceUnavailable, "NOT_CONFIGURED", "HeroSMS encryption is unavailable")
		}
		tiers = append(tiers, HeroSMSSMSPriceTierView{
			ID:               quoteID,
			Inventory:        tier.Count,
			CustomerPriceUSD: customerPrice.String(),
			ChargeQuota:      chargeQuota,
		})
	}
	if len(tiers) == 0 {
		return nil, newHeroSMSError(http.StatusConflict, "PRICE_CHANGED", "HeroSMS has no available SMS price tiers")
	}

	selected := tiers[0]
	bid := maxCustomerPriceUSD != ""
	if bid {
		maxCustomerPrice, valid := parseHeroSMSSMSBidPrice(maxCustomerPriceUSD)
		if !valid {
			return nil, newHeroSMSError(http.StatusBadRequest, "INVALID_REQUEST", "invalid HeroSMS SMS maximum bid")
		}
		providerMaxPrice := maxCustomerPrice.Div(multiplier)
		inventory := 0
		for _, tier := range offer.Tiers {
			if tier.Count > 0 && tier.Price.LessThanOrEqual(providerMaxPrice) {
				inventory = tier.Count
			}
		}
		if inventory <= 0 {
			return nil, newHeroSMSError(http.StatusConflict, "PRICE_CHANGED", "HeroSMS has no inventory within that bid")
		}
		chargeQuota, chargeErr := heroSMSChargeQuota(maxCustomerPrice)
		if chargeErr != nil {
			return nil, chargeErr
		}
		quoteID, quoteErr := encodeHeroSMSSMSQuote(heroSMSSMSQuoteToken{
			Version:      heroSMSSMSQuoteVersion,
			UserID:       userID,
			CountryID:    countryID,
			Service:      service,
			Operator:     operator,
			CostCNY:      providerMaxPrice.String(),
			Multiplier:   multiplier.String(),
			CurrencyCode: setting.HeroSMSCurrencyCode,
			IssuedAt:     issuedAt,
			Bid:          true,
		})
		if quoteErr != nil {
			return nil, newHeroSMSError(http.StatusServiceUnavailable, "NOT_CONFIGURED", "HeroSMS encryption is unavailable")
		}
		selected = HeroSMSSMSPriceTierView{
			ID:               quoteID,
			Inventory:        inventory,
			CustomerPriceUSD: maxCustomerPrice.String(),
			ChargeQuota:      chargeQuota,
		}
	}

	return &HeroSMSSMSOfferView{
		ID:               selected.ID,
		CountryID:        countryID,
		Service:          service,
		Operator:         operator,
		Inventory:        selected.Inventory,
		CustomerPriceUSD: selected.CustomerPriceUSD,
		ChargeQuota:      selected.ChargeQuota,
		Bid:              bid,
		Tiers:            tiers,
	}, nil
}

func heroSMSSMSHasInventoryWithinPrice(offer *herosms.SMSOffer, maxPrice decimal.Decimal) bool {
	if offer == nil || maxPrice.LessThanOrEqual(decimal.Zero) {
		return false
	}
	for _, tier := range offer.Tiers {
		if tier.Count > 0 && tier.Price.LessThanOrEqual(maxPrice) {
			return true
		}
	}
	return false
}

// pi-lens-ignore: go-bare-error
func replayHeroSMSSMSIdempotentOrder(userID int, idempotencyHash string, payloadHash string) (*HeroSMSSMSOrderView, int, int, bool, error) {
	var existing HeroSMSSMSOrder
	err := DB.Where("user_id = ? AND idempotency_key_hash = ?", userID, idempotencyHash).First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, 0, 0, false, nil
	}
	if err != nil {
		return nil, 0, 0, false, err
	}
	if existing.RequestPayloadHash != payloadHash {
		return nil, 0, 0, true, newHeroSMSError(http.StatusConflict, "IDEMPOTENCY_MISMATCH", "idempotent request payload mismatch")
	}
	view, err := heroSMSSMSOrderView(&existing)
	return view, getUserQuotaValue(userID), statusForHeroSMSSMSOrder(existing.Status), true, err
}

func heroSMSSMSPurchaseMayHaveSucceeded(err error) bool {
	return errors.Is(err, herosms.ErrUpstreamTimeout) ||
		errors.Is(err, herosms.ErrUpstreamBusy) ||
		errors.Is(err, herosms.ErrBadResponse)
}

// pi-lens-ignore: go-bare-error
func CreateHeroSMSSMSOrder(ctx context.Context, userID int, request HeroSMSSMSPurchaseRequest, idempotencyKey string) (*HeroSMSSMSOrderView, int, int, error) {
	trimmedKey := strings.TrimSpace(idempotencyKey)
	if userID <= 0 || trimmedKey == "" || len(trimmedKey) > 128 || strings.TrimSpace(request.OfferID) == "" {
		return nil, 0, 0, newHeroSMSError(http.StatusBadRequest, "INVALID_REQUEST", "invalid HeroSMS SMS purchase request")
	}
	idempotencyHash := sha256Hex(trimmedKey)
	payloadBytes, _ := json.Marshal(request)
	payloadHash := sha256Hex(string(payloadBytes))
	if view, quota, status, found, err := replayHeroSMSSMSIdempotentOrder(userID, idempotencyHash, payloadHash); found || err != nil {
		return view, quota, status, err
	}
	if heroSMSSMSIdempotencyMissHook != nil {
		heroSMSSMSIdempotencyMissHook()
	}

	client, err := heroSMSSMSClient()
	if err != nil {
		return nil, 0, 0, err
	}
	quote, err := decodeHeroSMSSMSQuote(request.OfferID)
	if err != nil || quote.Version != heroSMSSMSQuoteVersion || quote.UserID != userID || quote.CurrencyCode != setting.HeroSMSCurrencyCode || time.Since(time.Unix(quote.IssuedAt, 0)) > heroSMSSMSQuoteTTL {
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
	if err != nil || reservedCost.LessThanOrEqual(decimal.Zero) {
		return nil, 0, 0, newHeroSMSError(http.StatusConflict, "PRICE_CHANGED", "HeroSMS SMS price or inventory changed")
	}
	if !heroSMSSMSHasInventoryWithinPrice(providerOffer, reservedCost) {
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
	// A transport retry can arrive while the first request is waiting for or
	// holding the provider lease. Recheck under that lease so the retry replays
	// the exact order instead of racing the unique idempotency constraint.
	if view, quota, status, found, replayErr := replayHeroSMSSMSIdempotentOrder(userID, idempotencyHash, payloadHash); found || replayErr != nil {
		return view, quota, status, replayErr
	}
	if time.Since(time.Unix(quote.IssuedAt, 0)) > heroSMSSMSQuoteTTL {
		return nil, 0, 0, newHeroSMSError(http.StatusConflict, "PRICE_CHANGED", "refresh the HeroSMS SMS quote")
	}
	lockedMultiplier, err := heroSMSMultiplierDecimal()
	if err != nil {
		return nil, 0, 0, err
	}
	if lockedMultiplier.String() != quote.Multiplier {
		return nil, 0, 0, newHeroSMSError(http.StatusConflict, "PRICE_CHANGED", "HeroSMS price multiplier changed")
	}
	if quote.Operator != "" {
		operators, operatorErr := client.ListSMSOperators(ctx, quote.CountryID)
		if operatorErr != nil {
			return nil, 0, 0, mapHeroSMSProviderError(operatorErr)
		}
		operatorAvailable := false
		for _, candidate := range operators {
			if strings.EqualFold(strings.TrimSpace(candidate), quote.Operator) {
				operatorAvailable = true
				break
			}
		}
		if !operatorAvailable {
			return nil, 0, 0, newHeroSMSError(http.StatusConflict, "PRICE_CHANGED", "HeroSMS operator availability changed")
		}
	}
	providerOffer, err = client.GetSMSOffer(ctx, quote.CountryID, quote.Service)
	if err != nil {
		return nil, 0, 0, mapHeroSMSProviderError(err)
	}
	if !heroSMSSMSHasInventoryWithinPrice(providerOffer, reservedCost) {
		return nil, 0, 0, newHeroSMSError(http.StatusConflict, "PRICE_CHANGED", "HeroSMS SMS price or inventory changed")
	}
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
		_ = refundHeroSMSSMSOrder(
			order.ID,
			HeroSMSSMSOrderStatusFailed,
			"INTERNAL_ERROR",
			"failed to persist provider request intent",
			HeroSMSSMSOrderStatusPendingProvider,
		)
		return nil, 0, 0, err
	}
	activation, purchaseErr := client.PurchaseSMSActivation(ctx, herosms.SMSPurchaseRequest{
		CountryID:    quote.CountryID,
		Service:      quote.Service,
		Operator:     quote.Operator,
		MaxPrice:     reservedCost,
		CurrencyCode: setting.HeroSMSCurrencyCode,
	})
	if purchaseErr != nil {
		if heroSMSSMSPurchaseMayHaveSucceeded(purchaseErr) {
			view, reconcileErr := reconcileHeroSMSSMSOrderWithProviderLeaseHeld(ctx, client, order.ID)
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
		update := UpdateWalletQuotaByDelta(
			tx.Model(&User{}).Where("id = ? AND quota >= ?", order.UserID, order.ChargeQuota),
			-order.ChargeQuota,
		)
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

// pi-lens-ignore: go-bare-error
func rejectHeroSMSSMSActivation(ctx context.Context, client herosms.SMSClient, orderID string, activationID string, status int, code string, message string) error {
	now := time.Now().Unix()
	providerID := strings.TrimSpace(activationID)
	claim := DB.Model(&HeroSMSSMSOrder{}).
		Where("id = ? AND status = ?", orderID, HeroSMSSMSOrderStatusPurchaseUnknown).
		Updates(map[string]any{
			"status":               HeroSMSSMSOrderStatusCancelPending,
			"provider_id":          providerID,
			"cancel_final_status":  HeroSMSSMSOrderStatusFailed,
			"cancel_error_code":    code,
			"cancel_error_message": message,
			"last_error_code":      "CANCEL_PENDING",
			"last_error_message":   message,
			"updated_at":           now,
		})
	if claim.Error != nil {
		return claim.Error
	}
	if claim.RowsAffected == 0 {
		return newHeroSMSError(http.StatusConflict, "ORDER_STATE_CHANGED", "HeroSMS SMS order state changed")
	}
	if cancelErr := client.SetSMSActivationStatus(ctx, providerID, 8); cancelErr != nil {
		return newHeroSMSError(http.StatusAccepted, "RECONCILING", "HeroSMS activation cancellation is pending")
	}
	if err := DB.Model(&HeroSMSSMSOrder{}).
		Where("id = ? AND status = ?", orderID, HeroSMSSMSOrderStatusCancelPending).
		Updates(map[string]any{
			"provider_cancel_accepted_at": time.Now().Unix(),
			"updated_at":                  time.Now().Unix(),
		}).Error; err != nil {
		return err
	}
	return newHeroSMSError(status, code, message)
}

func completeHeroSMSSMSOrder(ctx context.Context, client herosms.SMSClient, orderID string, activation *herosms.SMSActivation) (*HeroSMSSMSOrderView, int, error) {
	if activation == nil || strings.TrimSpace(activation.ID) == "" || strings.TrimSpace(activation.PhoneNumber) == "" {
		_ = failHeroSMSSMSOrder(orderID, "BAD_UPSTREAM_RESPONSE", "HeroSMS returned an invalid SMS activation")
		return nil, 0, newHeroSMSError(http.StatusBadGateway, "BAD_UPSTREAM_RESPONSE", "HeroSMS returned an invalid SMS activation")
	}
	phoneCiphertext, err := encryptHeroSMSPayload(activation.PhoneNumber)
	if err != nil {
		return nil, 0, rejectHeroSMSSMSActivation(
			ctx,
			client,
			orderID,
			activation.ID,
			http.StatusServiceUnavailable,
			"NOT_CONFIGURED",
			"HeroSMS encryption is unavailable",
		)
	}

	providerExpiresAt := int64(0)
	if parsed, parseErr := time.Parse(time.RFC3339, strings.TrimSpace(activation.ActivationEndTime)); parseErr == nil {
		providerExpiresAt = parsed.Unix()
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
		if order.Status != HeroSMSSMSOrderStatusPurchaseUnknown {
			return newHeroSMSError(http.StatusConflict, "ORDER_STATE_CHANGED", "HeroSMS SMS order state changed")
		}
		multiplier, err := decimal.NewFromString(order.PriceMultiplier)
		if err != nil {
			return err
		}
		if activation.CurrencyCode != setting.HeroSMSCurrencyCode {
			return newHeroSMSError(http.StatusBadGateway, "CURRENCY_MISMATCH", "HeroSMS SMS currency did not match the confirmed quote")
		}
		reservedProviderCost, err := decimal.NewFromString(order.ProviderPriceCNY)
		if err != nil || reservedProviderCost.LessThanOrEqual(decimal.Zero) {
			return newHeroSMSError(http.StatusConflict, "PRICE_CHANGED", "HeroSMS SMS quote is invalid")
		}
		if activation.ActivationCost.GreaterThan(reservedProviderCost) {
			return newHeroSMSError(http.StatusConflict, "PRICE_CHANGED", "HeroSMS SMS price exceeded the confirmed quote")
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
			if err := ApplyWalletQuotaDelta(tx, order.UserID, refund); err != nil {
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
		if err := tx.Model(&HeroSMSSMSOrder{}).
			Where("id = ? AND status = ?", order.ID, HeroSMSSMSOrderStatusPurchaseUnknown).
			Updates(map[string]any{
				"status":                 HeroSMSSMSOrderStatusActive,
				"provider_id":            providerID,
				"provider_currency_code": activation.CurrencyCode,
				"provider_expires_at":    providerExpiresAt,
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
		if heroErr, ok := err.(*HeroSMSError); ok && (heroErr.Code == "PRICE_CHANGED" || heroErr.Code == "CURRENCY_MISMATCH") {
			return nil, 0, rejectHeroSMSSMSActivation(
				ctx,
				client,
				orderID,
				activation.ID,
				heroErr.Status,
				heroErr.Code,
				heroErr.Message,
			)
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
	return refundHeroSMSSMSOrder(
		orderID,
		HeroSMSSMSOrderStatusFailed,
		code,
		message,
		HeroSMSSMSOrderStatusPurchaseUnknown,
	)
}

func normalizeHeroSMSSMSComplaintType(value string) (string, bool) {
	value = strings.TrimSpace(value)
	switch value {
	case HeroSMSSMSComplaintNumberBlocked,
		HeroSMSSMSComplaintNumberInUse,
		HeroSMSSMSComplaintCodeMismatch,
		HeroSMSSMSComplaintNotReceived,
		HeroSMSSMSComplaintCodeSentToApp,
		HeroSMSSMSComplaintIncomingCallNumber,
		HeroSMSSMSComplaintIncomingCallVoice:
		return value, true
	default:
		return "", false
	}
}

func heroSMSSMSComplaintOutcomeUnknown(err error) bool {
	return errors.Is(err, herosms.ErrUpstreamTimeout) ||
		errors.Is(err, herosms.ErrUpstreamBusy) ||
		errors.Is(err, herosms.ErrBadResponse)
}

func heroSMSSMSComplaintNeedsReconciliation(status string) bool {
	switch status {
	case HeroSMSSMSComplaintStatusSubmitting,
		HeroSMSSMSComplaintStatusSubmitted,
		HeroSMSSMSComplaintStatusSubmitUnknown:
		return true
	default:
		return false
	}
}

func heroSMSSMSStatusAllowed(status string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, candidate := range allowed {
		if status == candidate {
			return true
		}
	}
	return false
}

func refundHeroSMSSMSOrder(orderID string, status string, code string, message string, expectedStatuses ...string) error {
	userID := 0
	newQuota := 0
	err := DB.Transaction(func(tx *gorm.DB) error {
		var order HeroSMSSMSOrder
		if err := lockForUpdate(tx).Where("id = ?", orderID).First(&order).Error; err != nil {
			return err
		}
		if !heroSMSSMSStatusAllowed(order.Status, expectedStatuses) {
			return newHeroSMSError(http.StatusConflict, "ORDER_STATE_CHANGED", "HeroSMS SMS order state changed")
		}
		userID = order.UserID
		refund := order.ReservedQuota - order.RefundedQuota
		if refund > 0 {
			if err := ApplyWalletQuotaDelta(tx, order.UserID, refund); err != nil {
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
		updates := map[string]any{
			"status":             status,
			"refunded_quota":     order.RefundedQuota + refund,
			"last_error_code":    code,
			"last_error_message": message,
			"updated_at":         now,
		}
		if heroSMSSMSComplaintNeedsReconciliation(order.ComplaintStatus) {
			updates["complaint_status"] = HeroSMSSMSComplaintStatusClosedRefund
		}
		if err := tx.Model(&HeroSMSSMSOrder{}).Where("id = ?", order.ID).Updates(updates).Error; err != nil {
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

func completeHeroSMSSMSCode(ctx context.Context, client herosms.SMSClient, order *HeroSMSSMSOrder, expectedStatus string, code string, message string) (bool, error) {
	codeCiphertext, err := encryptHeroSMSPayload(code)
	if err != nil {
		return false, err
	}
	messageCiphertext, err := encryptHeroSMSPayload(message)
	if err != nil {
		return false, err
	}
	now := time.Now().Unix()
	updates := map[string]any{
		"status":             HeroSMSSMSOrderStatusCompleted,
		"code_ciphertext":    codeCiphertext,
		"message_ciphertext": messageCiphertext,
		"completed_at":       now,
		"updated_at":         now,
	}
	if heroSMSSMSComplaintNeedsReconciliation(order.ComplaintStatus) {
		updates["complaint_status"] = HeroSMSSMSComplaintStatusClosedCode
	}
	result := DB.Model(&HeroSMSSMSOrder{}).
		Where("id = ? AND status = ?", order.ID, expectedStatus).
		Updates(updates)
	if result.Error != nil {
		return false, result.Error
	}
	if result.RowsAffected == 0 {
		return false, nil
	}
	if order.ProviderID != nil {
		_ = client.SetSMSActivationStatus(ctx, *order.ProviderID, 6)
	}
	return true, nil
}

func heroSMSSMSCancellationResult(order *HeroSMSSMSOrder) (string, string, string) {
	status := strings.TrimSpace(order.CancelFinalStatus)
	if status != HeroSMSSMSOrderStatusFailed {
		status = HeroSMSSMSOrderStatusCancelled
	}
	code := strings.TrimSpace(order.CancelErrorCode)
	message := strings.TrimSpace(order.CancelErrorMessage)
	if code == "" {
		code = "USER_CANCELLED"
	}
	if message == "" {
		message = "activation cancelled before receiving a code"
	}
	return status, code, message
}

// pi-lens-ignore: go-bare-error
func finalizeHeroSMSSMSCancellation(ctx context.Context, client herosms.SMSClient, order *HeroSMSSMSOrder) (*HeroSMSSMSOrderView, int, error) {
	if order.ProviderID == nil || strings.TrimSpace(*order.ProviderID) == "" {
		return nil, 0, newHeroSMSError(http.StatusConflict, "RECONCILING", "wait for provider purchase reconciliation before cancelling")
	}
	providerID := strings.TrimSpace(*order.ProviderID)
	providerState, err := client.GetSMSActivationState(ctx, providerID)
	if err != nil {
		return nil, 0, mapHeroSMSProviderError(err)
	}
	if providerState == herosms.SMSActivationStateCancel {
		status, code, message := heroSMSSMSCancellationResult(order)
		if err := refundHeroSMSSMSOrder(order.ID, status, code, message, HeroSMSSMSOrderStatusCancelPending); err != nil {
			return nil, 0, err
		}
		view, viewErr := GetHeroSMSSMSOrder(order.ID, order.UserID)
		return view, getUserQuotaValue(order.UserID), viewErr
	}

	providerStatus, err := client.GetSMSActivationStatus(ctx, providerID)
	if err != nil {
		return nil, 0, mapHeroSMSProviderError(err)
	}
	if providerStatus.Code != "" {
		completed, completeErr := completeHeroSMSSMSCode(ctx, client, order, HeroSMSSMSOrderStatusCancelPending, providerStatus.Code, providerStatus.Text)
		if completeErr != nil {
			return nil, 0, completeErr
		}
		if completed {
			view, viewErr := GetHeroSMSSMSOrder(order.ID, order.UserID)
			return view, getUserQuotaValue(order.UserID), viewErr
		}
	}
	if providerState == herosms.SMSActivationStateOK {
		return nil, 0, newHeroSMSError(http.StatusAccepted, "RECONCILING", "HeroSMS activation completion is pending")
	}

	if order.ProviderCancelAcceptedAt == 0 {
		if err := client.SetSMSActivationStatus(ctx, providerID, 8); err != nil {
			return nil, 0, mapHeroSMSProviderError(err)
		}
		now := time.Now().Unix()
		if err := DB.Model(&HeroSMSSMSOrder{}).
			Where("id = ? AND status = ?", order.ID, HeroSMSSMSOrderStatusCancelPending).
			Updates(map[string]any{
				"provider_cancel_accepted_at": now,
				"updated_at":                  now,
			}).Error; err != nil {
			return nil, 0, err
		}
		order.ProviderCancelAcceptedAt = now
	}

	view, viewErr := GetHeroSMSSMSOrder(order.ID, order.UserID)
	return view, getUserQuotaValue(order.UserID), viewErr
}

// pi-lens-ignore: go-bare-error
func reconcileHeroSMSSMSComplaintWithProviderLeaseHeld(ctx context.Context, client herosms.SMSClient, order *HeroSMSSMSOrder) (*HeroSMSSMSOrderView, error) {
	defer func() {
		checkedAt := time.Now().Unix()
		if err := DB.Model(&HeroSMSSMSOrder{}).
			Where("id = ? AND status = ? AND complaint_status IN ?", order.ID, HeroSMSSMSOrderStatusActive, []string{HeroSMSSMSComplaintStatusSubmitting, HeroSMSSMSComplaintStatusSubmitted, HeroSMSSMSComplaintStatusSubmitUnknown}).
			Updates(map[string]any{
				"complaint_last_checked_at": checkedAt,
				"updated_at":                checkedAt,
			}).Error; err != nil {
			common.SysLog(fmt.Sprintf("HeroSMS SMS complaint poll timestamp update failed: %T", err))
		}
	}()
	if order.ProviderID == nil || strings.TrimSpace(*order.ProviderID) == "" {
		return nil, newHeroSMSError(http.StatusConflict, "RECONCILING", "wait for provider purchase reconciliation before submitting a complaint")
	}
	providerID := strings.TrimSpace(*order.ProviderID)
	now := time.Now().Unix()
	shouldRetrySubmission :=
		(order.ComplaintStatus == HeroSMSSMSComplaintStatusSubmitting || order.ComplaintStatus == HeroSMSSMSComplaintStatusSubmitUnknown) &&
			order.ComplaintSubmitAttempts < heroSMSSMSComplaintMaxAttempts &&
			(order.ComplaintNextRetryAt == 0 || order.ComplaintNextRetryAt <= now)
	if shouldRetrySubmission {
		providerErr := client.SubmitSMSActivationComplaint(ctx, providerID, order.ComplaintType)
		attempts := order.ComplaintSubmitAttempts + 1
		complaintStatus := HeroSMSSMSComplaintStatusSubmitted
		nextRetryAt := int64(0)
		if providerErr != nil {
			if heroSMSSMSComplaintOutcomeUnknown(providerErr) {
				complaintStatus = HeroSMSSMSComplaintStatusSubmitUnknown
				nextRetryAt = time.Now().Add(heroSMSSMSComplaintRetryDelay).Unix()
			} else {
				complaintStatus = HeroSMSSMSComplaintStatusFailed
			}
		}
		if err := DB.Model(&HeroSMSSMSOrder{}).
			Where("id = ? AND status = ? AND complaint_status IN ?", order.ID, HeroSMSSMSOrderStatusActive, []string{HeroSMSSMSComplaintStatusSubmitting, HeroSMSSMSComplaintStatusSubmitUnknown}).
			Updates(map[string]any{
				"complaint_status":          complaintStatus,
				"complaint_submit_attempts": attempts,
				"complaint_next_retry_at":   nextRetryAt,
				"updated_at":                now,
			}).Error; err != nil {
			return nil, err
		}
		order.ComplaintStatus = complaintStatus
		order.ComplaintSubmitAttempts = attempts
		order.ComplaintNextRetryAt = nextRetryAt
		if providerErr != nil && !heroSMSSMSComplaintOutcomeUnknown(providerErr) {
			return nil, mapHeroSMSProviderError(providerErr)
		}
	}

	providerState, err := client.GetSMSActivationState(ctx, providerID)
	if err != nil {
		return nil, mapHeroSMSProviderError(err)
	}
	if providerState == herosms.SMSActivationStateCancel {
		if err := refundHeroSMSSMSOrder(
			order.ID,
			HeroSMSSMSOrderStatusCancelled,
			"UPSTREAM_REFUND_CONFIRMED",
			"HeroSMS confirmed cancellation after the complaint",
			HeroSMSSMSOrderStatusActive,
		); err != nil {
			return nil, err
		}
		return GetHeroSMSSMSOrder(order.ID, order.UserID)
	}
	providerStatus, err := client.GetSMSActivationStatus(ctx, providerID)
	if err != nil {
		return nil, mapHeroSMSProviderError(err)
	}
	if providerStatus.Code != "" {
		if _, err := completeHeroSMSSMSCode(ctx, client, order, HeroSMSSMSOrderStatusActive, providerStatus.Code, providerStatus.Text); err != nil {
			return nil, err
		}
		return GetHeroSMSSMSOrder(order.ID, order.UserID)
	}
	return GetHeroSMSSMSOrder(order.ID, order.UserID)
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
	if order.Status != HeroSMSSMSOrderStatusActive && order.Status != HeroSMSSMSOrderStatusCancelPending {
		return heroSMSSMSOrderView(order)
	}
	releaseLease, err := acquireHeroSMSProviderPurchaseLease(ctx)
	if err != nil {
		return nil, err
	}
	defer releaseLease()
	order, err = getHeroSMSSMSOrder(userID, order.ID)
	if err != nil {
		return nil, err
	}
	if order.Status == HeroSMSSMSOrderStatusCancelPending {
		view, _, cancelErr := finalizeHeroSMSSMSCancellation(ctx, client, order)
		return view, cancelErr
	}
	if order.Status != HeroSMSSMSOrderStatusActive || order.ProviderID == nil {
		return heroSMSSMSOrderView(order)
	}
	if order.ProviderExpiresAt > 0 && time.Now().Unix() >= order.ProviderExpiresAt {
		now := time.Now().Unix()
		claim := DB.Model(&HeroSMSSMSOrder{}).
			Where("id = ? AND user_id = ? AND status = ?", order.ID, userID, HeroSMSSMSOrderStatusActive).
			Updates(map[string]any{
				"status":                      HeroSMSSMSOrderStatusCancelPending,
				"provider_cancel_accepted_at": 0,
				"cancel_final_status":         HeroSMSSMSOrderStatusCancelled,
				"cancel_error_code":           "ACTIVATION_EXPIRED",
				"cancel_error_message":        "activation expired before receiving a code",
				"last_error_code":             "CANCEL_PENDING",
				"last_error_message":          "activation expired; awaiting HeroSMS cancellation confirmation",
				"updated_at":                  now,
			})
		if claim.Error != nil {
			return nil, claim.Error
		}
		order, err = getHeroSMSSMSOrder(userID, order.ID)
		if err != nil {
			return nil, err
		}
		if order.Status == HeroSMSSMSOrderStatusCancelPending {
			view, _, cancelErr := finalizeHeroSMSSMSCancellation(ctx, client, order)
			return view, cancelErr
		}
		return heroSMSSMSOrderView(order)
	}
	if heroSMSSMSComplaintNeedsReconciliation(order.ComplaintStatus) {
		return reconcileHeroSMSSMSComplaintWithProviderLeaseHeld(ctx, client, order)
	}
	status, err := client.GetSMSActivationStatus(ctx, *order.ProviderID)
	if err != nil {
		return nil, mapHeroSMSProviderError(err)
	}
	if status.Code == "" {
		if order.ProviderExpiresAt == 0 {
			providerState, stateErr := client.GetSMSActivationState(ctx, *order.ProviderID)
			if stateErr == nil && providerState == herosms.SMSActivationStateCancel {
				if err := refundHeroSMSSMSOrder(
					order.ID,
					HeroSMSSMSOrderStatusCancelled,
					"PROVIDER_CANCELLED",
					"HeroSMS cancelled the activation before a code arrived",
					HeroSMSSMSOrderStatusActive,
				); err != nil {
					return nil, err
				}
				return GetHeroSMSSMSOrder(order.ID, userID)
			}
		}
		return heroSMSSMSOrderView(order)
	}
	if _, err := completeHeroSMSSMSCode(ctx, client, order, HeroSMSSMSOrderStatusActive, status.Code, status.Text); err != nil {
		return nil, err
	}
	return GetHeroSMSSMSOrder(order.ID, userID)
}

// pi-lens-ignore: go-bare-error
func CancelHeroSMSSMSOrder(ctx context.Context, userID int, orderID string) (*HeroSMSSMSOrderView, int, error) {
	client, err := heroSMSSMSOperationsClient()
	if err != nil {
		return nil, 0, err
	}
	releaseLease, err := acquireHeroSMSProviderPurchaseLease(ctx)
	if err != nil {
		return nil, 0, err
	}
	defer releaseLease()
	order, err := getHeroSMSSMSOrder(userID, orderID)
	if err != nil {
		return nil, 0, err
	}
	if order.Status == HeroSMSSMSOrderStatusCompleted || order.Status == HeroSMSSMSOrderStatusCancelled || order.Status == HeroSMSSMSOrderStatusFailed || order.Status == HeroSMSSMSOrderStatusCancelPending {
		view, viewErr := heroSMSSMSOrderView(order)
		return view, getUserQuotaValue(userID), viewErr
	}
	if order.Status != HeroSMSSMSOrderStatusActive || order.ProviderID == nil || strings.TrimSpace(*order.ProviderID) == "" {
		return nil, 0, newHeroSMSError(http.StatusConflict, "RECONCILING", "wait for provider purchase reconciliation before cancelling")
	}
	now := time.Now().Unix()
	claim := DB.Model(&HeroSMSSMSOrder{}).
		Where("id = ? AND user_id = ? AND status = ?", order.ID, userID, HeroSMSSMSOrderStatusActive).
		Updates(map[string]any{
			"status":                      HeroSMSSMSOrderStatusCancelPending,
			"provider_cancel_accepted_at": 0,
			"cancel_final_status":         HeroSMSSMSOrderStatusCancelled,
			"cancel_error_code":           "USER_CANCELLED",
			"cancel_error_message":        "activation cancelled before receiving a code",
			"last_error_code":             "CANCEL_PENDING",
			"last_error_message":          "",
			"updated_at":                  now,
		})
	if claim.Error != nil {
		return nil, 0, claim.Error
	}
	if claim.RowsAffected == 0 {
		view, viewErr := GetHeroSMSSMSOrder(order.ID, userID)
		return view, getUserQuotaValue(userID), viewErr
	}
	order, err = getHeroSMSSMSOrder(userID, order.ID)
	if err != nil {
		return nil, 0, err
	}
	if order.Status != HeroSMSSMSOrderStatusCancelPending {
		view, viewErr := heroSMSSMSOrderView(order)
		return view, getUserQuotaValue(userID), viewErr
	}
	return finalizeHeroSMSSMSCancellation(ctx, client, order)
}

// pi-lens-ignore: go-bare-error
// pi-lens-ignore: go-bare-error
func SubmitHeroSMSSMSComplaint(ctx context.Context, userID int, orderID string, complaintType string) (*HeroSMSSMSOrderView, error) {
	complaintType, valid := normalizeHeroSMSSMSComplaintType(complaintType)
	if !valid {
		return nil, newHeroSMSError(http.StatusBadRequest, "INVALID_COMPLAINT_REASON", "select a supported complaint reason")
	}
	client, err := heroSMSSMSOperationsClient()
	if err != nil {
		return nil, err
	}
	releaseLease, err := acquireHeroSMSProviderPurchaseLease(ctx)
	if err != nil {
		return nil, err
	}
	defer releaseLease()

	order, err := getHeroSMSSMSOrder(userID, orderID)
	if err != nil {
		return nil, err
	}
	if order.Status != HeroSMSSMSOrderStatusActive || order.ProviderID == nil || strings.TrimSpace(*order.ProviderID) == "" {
		return nil, newHeroSMSError(http.StatusConflict, "ORDER_STATE_CHANGED", "only an active activation can receive a complaint")
	}
	if time.Now().Before(time.Unix(order.CreatedAt, 0).Add(heroSMSSMSComplaintWait)) {
		return nil, newHeroSMSError(http.StatusConflict, "COMPLAINT_TOO_EARLY", "wait two minutes before submitting a complaint")
	}
	if heroSMSSMSComplaintNeedsReconciliation(order.ComplaintStatus) {
		if order.ComplaintType != complaintType {
			return nil, newHeroSMSError(http.StatusConflict, "COMPLAINT_ALREADY_SUBMITTED", "a complaint is already pending for this activation")
		}
		return heroSMSSMSOrderView(order)
	}
	if order.ComplaintStatus != "" && order.ComplaintStatus != HeroSMSSMSComplaintStatusFailed {
		return nil, newHeroSMSError(http.StatusConflict, "COMPLAINT_ALREADY_CLOSED", "the complaint workflow is already closed")
	}

	now := time.Now().Unix()
	claim := DB.Model(&HeroSMSSMSOrder{}).
		Where("id = ? AND user_id = ? AND status = ? AND (complaint_status IS NULL OR complaint_status = '' OR complaint_status = ?)", order.ID, userID, HeroSMSSMSOrderStatusActive, HeroSMSSMSComplaintStatusFailed).
		Updates(map[string]any{
			"complaint_type":            complaintType,
			"complaint_status":          HeroSMSSMSComplaintStatusSubmitting,
			"complaint_submitted_at":    now,
			"complaint_submit_attempts": 1,
			"complaint_next_retry_at":   now + int64(heroSMSSMSComplaintRetryDelay/time.Second),
			"updated_at":                now,
		})
	if claim.Error != nil {
		return nil, claim.Error
	}
	if claim.RowsAffected == 0 {
		return nil, newHeroSMSError(http.StatusConflict, "ORDER_STATE_CHANGED", "HeroSMS SMS order state changed")
	}

	providerErr := client.SubmitSMSActivationComplaint(ctx, *order.ProviderID, complaintType)
	if providerErr != nil {
		complaintStatus := HeroSMSSMSComplaintStatusFailed
		nextRetryAt := int64(0)
		if heroSMSSMSComplaintOutcomeUnknown(providerErr) {
			complaintStatus = HeroSMSSMSComplaintStatusSubmitUnknown
			nextRetryAt = time.Now().Add(heroSMSSMSComplaintRetryDelay).Unix()
		}
		updateErr := DB.Model(&HeroSMSSMSOrder{}).
			Where("id = ? AND status = ? AND complaint_status = ?", order.ID, HeroSMSSMSOrderStatusActive, HeroSMSSMSComplaintStatusSubmitting).
			Updates(map[string]any{
				"complaint_status":        complaintStatus,
				"complaint_next_retry_at": nextRetryAt,
				"updated_at":              time.Now().Unix(),
			}).Error
		if updateErr != nil {
			common.SysLog(fmt.Sprintf("HeroSMS SMS complaint state update failed: %T", updateErr))
		}
		return nil, mapHeroSMSProviderError(providerErr)
	}
	if err := DB.Model(&HeroSMSSMSOrder{}).
		Where("id = ? AND status = ? AND complaint_status = ?", order.ID, HeroSMSSMSOrderStatusActive, HeroSMSSMSComplaintStatusSubmitting).
		Updates(map[string]any{
			"complaint_status":        HeroSMSSMSComplaintStatusSubmitted,
			"complaint_next_retry_at": 0,
			"updated_at":              time.Now().Unix(),
		}).Error; err != nil {
		return nil, err
	}
	return GetHeroSMSSMSOrder(order.ID, userID)
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
		HeroSMSSMSOrderStatusCancelPending,
	}).Order("created_at DESC").First(&order).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return RefreshHeroSMSSMSOrder(ctx, userID, order.ID)
}

func heroSMSSMSCurrentStatus(status string) bool {
	return status == HeroSMSSMSOrderStatusPendingProvider ||
		status == HeroSMSSMSOrderStatusPurchaseUnknown ||
		status == HeroSMSSMSOrderStatusActive ||
		status == HeroSMSSMSOrderStatusCancelPending ||
		status == HeroSMSSMSOrderStatusCompleted
}

func ListCurrentHeroSMSSMSOrders(ctx context.Context, userID int) ([]HeroSMSSMSOrderView, error) {
	cutoff := time.Now().Add(-heroSMSSMSRecentCodeWindow).Unix()
	var orders []HeroSMSSMSOrder
	err := DB.Where(
		"user_id = ? AND (status IN ? OR (status = ? AND completed_at >= ?))",
		userID,
		[]string{
			HeroSMSSMSOrderStatusPendingProvider,
			HeroSMSSMSOrderStatusPurchaseUnknown,
			HeroSMSSMSOrderStatusActive,
			HeroSMSSMSOrderStatusCancelPending,
		},
		HeroSMSSMSOrderStatusCompleted,
		cutoff,
	).Order("created_at DESC").Limit(heroSMSSMSCurrentOrdersLimit).Find(&orders).Error
	if err != nil {
		return nil, err
	}
	views := make([]HeroSMSSMSOrderView, 0, len(orders))
	for index := range orders {
		view, refreshErr := RefreshHeroSMSSMSOrder(ctx, userID, orders[index].ID)
		if refreshErr != nil {
			common.SysLog(fmt.Sprintf("HeroSMS SMS current-order refresh failed: order=%s error=%T", orders[index].ID, refreshErr))
			view, refreshErr = heroSMSSMSOrderView(&orders[index])
		}
		if refreshErr != nil {
			return nil, refreshErr
		}
		if view != nil && heroSMSSMSCurrentStatus(view.Status) {
			view.ProviderID = nil
			views = append(views, *view)
		}
	}
	return views, nil
}

func ListHeroSMSSMSOrders(userID int, page int, size int) (*HeroSMSSMSOrderPage, error) {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}
	query := DB.Model(&HeroSMSSMSOrder{}).
		Where("user_id = ? AND history_hidden_at = 0", userID)
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

func heroSMSSMSOrderSummaryView(order *HeroSMSSMSOrder) (*HeroSMSSMSOrderView, error) {
	view := &HeroSMSSMSOrderView{
		ID:               order.ID,
		CountryID:        order.CountryID,
		Service:          order.Service,
		Operator:         order.Operator,
		Status:           order.Status,
		CustomerPriceUSD: order.CustomerPriceUSD,
		ChargeQuota:      order.ChargeQuota,
		RefundedQuota:    order.RefundedQuota,
		CreatedAt:        order.CreatedAt,
		UpdatedAt:        order.UpdatedAt,
	}
	if order.PhoneCiphertext == "" {
		return view, nil
	}
	phone, err := decryptHeroSMSPayload(order.PhoneCiphertext)
	if err != nil {
		return nil, err
	}
	view.PhoneNumber = phone
	return view, nil
}

func ListHeroSMSSMSOrderSummaries(userID int, page int, size int) (*HeroSMSSMSOrderPage, error) {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}
	query := DB.Model(&HeroSMSSMSOrder{}).
		Where("user_id = ? AND history_hidden_at = 0", userID).
		Where("status NOT IN ?", []string{
			HeroSMSSMSOrderStatusPendingProvider,
			HeroSMSSMSOrderStatusPurchaseUnknown,
			HeroSMSSMSOrderStatusActive,
			HeroSMSSMSOrderStatusCancelPending,
		})
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
		view, err := heroSMSSMSOrderSummaryView(&orders[index])
		if err != nil {
			return nil, err
		}
		views = append(views, *view)
	}
	return &HeroSMSSMSOrderPage{Items: views, Page: page, Size: size, Total: total}, nil
}

func heroSMSSMSTerminalStatuses() []string {
	return []string{
		HeroSMSSMSOrderStatusCompleted,
		HeroSMSSMSOrderStatusCancelled,
		HeroSMSSMSOrderStatusFailed,
	}
}

// HideHeroSMSSMSOrderFromHistory removes a terminal order from the owner's
// history view without deleting financial, provider, or refund audit data.
// pi-lens-ignore: go-bare-error
func HideHeroSMSSMSOrderFromHistory(userID int, orderID string) error {
	trimmedOrderID := strings.TrimSpace(orderID)
	result := DB.Model(&HeroSMSSMSOrder{}).
		Where("id = ? AND user_id = ? AND history_hidden_at = 0 AND status IN ?", trimmedOrderID, userID, heroSMSSMSTerminalStatuses()).
		UpdateColumn("history_hidden_at", time.Now().Unix())
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected > 0 {
		return nil
	}
	order, err := getHeroSMSSMSOrder(userID, trimmedOrderID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return newHeroSMSError(http.StatusNotFound, "ORDER_NOT_FOUND", "HeroSMS SMS order not found")
	}
	if err != nil {
		return err
	}
	if order.HistoryHiddenAt > 0 {
		return nil
	}
	return newHeroSMSError(http.StatusConflict, "ORDER_ACTIVE", "active HeroSMS SMS orders cannot be removed from history")
}

// ClearHeroSMSSMSOrderHistory hides every terminal order owned by the caller.
// Rows remain intact for quota, refund, complaint, and provider reconciliation.
func ClearHeroSMSSMSOrderHistory(userID int) (int64, error) {
	result := DB.Model(&HeroSMSSMSOrder{}).
		Where("user_id = ? AND history_hidden_at = 0 AND status IN ?", userID, heroSMSSMSTerminalStatuses()).
		UpdateColumn("history_hidden_at", time.Now().Unix())
	return result.RowsAffected, result.Error
}

func heroSMSSMSOrderView(order *HeroSMSSMSOrder) (*HeroSMSSMSOrderView, error) {
	complaintRetryable := order.ComplaintStatus == "" || order.ComplaintStatus == HeroSMSSMSComplaintStatusFailed
	view := &HeroSMSSMSOrderView{
		ID:                   order.ID,
		CountryID:            order.CountryID,
		Service:              order.Service,
		Operator:             order.Operator,
		Status:               order.Status,
		CustomerPriceUSD:     order.CustomerPriceUSD,
		ChargeQuota:          order.ChargeQuota,
		RefundedQuota:        order.RefundedQuota,
		ProviderID:           order.ProviderID,
		CanCancel:            order.Status == HeroSMSSMSOrderStatusActive && order.ProviderID != nil,
		CanComplain:          order.Status == HeroSMSSMSOrderStatusActive && order.ProviderID != nil && order.CodeCiphertext == "" && complaintRetryable && !time.Now().Before(time.Unix(order.CreatedAt, 0).Add(heroSMSSMSComplaintWait)),
		ComplaintType:        order.ComplaintType,
		ComplaintStatus:      order.ComplaintStatus,
		ComplaintSubmittedAt: order.ComplaintSubmittedAt,
		LastErrorCode:        order.LastErrorCode,
		LastErrorMessage:     order.LastErrorMessage,
		CreatedAt:            order.CreatedAt,
		UpdatedAt:            order.UpdatedAt,
		ExpiresAt:            order.ProviderExpiresAt,
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
	releaseLease, err := acquireHeroSMSProviderPurchaseLease(ctx)
	if err != nil {
		return nil, err
	}
	defer releaseLease()
	return reconcileHeroSMSSMSOrderWithProviderLeaseHeld(ctx, client, orderID)
}

func heroSMSSMSReconciliationCandidate(order *HeroSMSSMSOrder, activation *herosms.SMSActiveActivation) bool {
	if order == nil || activation == nil || strings.TrimSpace(activation.PhoneNumber) == "" {
		return false
	}
	if activation.Service != order.Service || activation.CountryCode != order.CountryID || activation.CurrencyCode != setting.HeroSMSCurrencyCode {
		return false
	}
	reservedCost, err := decimal.NewFromString(order.ProviderPriceCNY)
	if err != nil || activation.ActivationCost.LessThanOrEqual(decimal.Zero) || activation.ActivationCost.GreaterThan(reservedCost) {
		return false
	}
	requestedOperator := strings.TrimSpace(order.Operator)
	actualOperator := strings.TrimSpace(activation.ActivationOperator)
	if requestedOperator != "" && !strings.EqualFold(requestedOperator, "any") && actualOperator != "" && !strings.EqualFold(requestedOperator, actualOperator) {
		return false
	}
	activationTime := strings.TrimSpace(activation.ActivationTime)
	if activationTime == "" {
		return true
	}
	parsed, parseErr := time.Parse(time.RFC3339, activationTime)
	if parseErr != nil {
		parsed, parseErr = time.ParseInLocation("2006-01-02 15:04:05", activationTime, time.UTC)
	}
	return parseErr != nil || !parsed.Before(time.Unix(order.ProviderRequestStartedAt, 0).Add(-30*time.Second))
}

// pi-lens-ignore: go-bare-error
func reconcileHeroSMSSMSOrderWithProviderLeaseHeld(ctx context.Context, client herosms.SMSClient, orderID string) (*HeroSMSSMSOrderView, error) {
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
		if heroSMSSMSReconciliationCandidate(&order, &activation) {
			candidates = append(candidates, activation)
		}
	}
	if len(candidates) == 1 {
		activation := &herosms.SMSActivation{
			ID:                 candidates[0].ID,
			PhoneNumber:        candidates[0].PhoneNumber,
			ActivationCost:     candidates[0].ActivationCost,
			CostValue:          candidates[0].CostValue,
			CurrencyCode:       candidates[0].CurrencyCode,
			CountryCode:        candidates[0].CountryCode,
			ActivationTime:     candidates[0].ActivationTime,
			ActivationEndTime:  candidates[0].ActivationEndTime,
			ActivationOperator: candidates[0].ActivationOperator,
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
		_ = DB.Model(&HeroSMSSMSOrder{}).
			Where("id = ? AND status = ?", order.ID, HeroSMSSMSOrderStatusPurchaseUnknown).
			Updates(map[string]any{
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
		HeroSMSSMSOrderStatusCancelPending,
	}).Count(&count).Error
	return count > 0, err
}

func reconcileHeroSMSSMSCancellation(ctx context.Context, client herosms.SMSClient, orderID string, userID int) error {
	releaseLease, err := acquireHeroSMSProviderPurchaseLease(ctx)
	if err != nil {
		return err
	}
	defer releaseLease()
	order, err := getHeroSMSSMSOrder(userID, orderID)
	if err != nil {
		return err
	}
	if order.Status != HeroSMSSMSOrderStatusCancelPending {
		return nil
	}
	_, _, err = finalizeHeroSMSSMSCancellation(ctx, client, order)
	return err
}

func reconcileHeroSMSSMSComplaint(ctx context.Context, client herosms.SMSClient, orderID string, userID int) error {
	releaseLease, err := acquireHeroSMSProviderPurchaseLease(ctx)
	if err != nil {
		return err
	}
	defer releaseLease()
	order, err := getHeroSMSSMSOrder(userID, orderID)
	if err != nil {
		return err
	}
	if order.Status != HeroSMSSMSOrderStatusActive || !heroSMSSMSComplaintNeedsReconciliation(order.ComplaintStatus) {
		return nil
	}
	_, err = reconcileHeroSMSSMSComplaintWithProviderLeaseHeld(ctx, client, order)
	return err
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
	if err := DB.Where(
		"status IN ? OR (status = ? AND complaint_status IN ?)",
		[]string{HeroSMSSMSOrderStatusPurchaseUnknown, HeroSMSSMSOrderStatusCancelPending},
		HeroSMSSMSOrderStatusActive,
		[]string{HeroSMSSMSComplaintStatusSubmitting, HeroSMSSMSComplaintStatusSubmitted, HeroSMSSMSComplaintStatusSubmitUnknown},
	).
		Order("CASE WHEN status = 'cancel_pending' THEN 0 WHEN status = 'purchase_unknown' THEN 1 ELSE 2 END ASC").
		Order("updated_at ASC").
		Limit(limit).
		Find(&orders).Error; err != nil {
		return 0, err
	}
	processed := 0
	for index := range orders {
		var processErr error
		switch {
		case orders[index].Status == HeroSMSSMSOrderStatusCancelPending:
			processErr = reconcileHeroSMSSMSCancellation(ctx, client, orders[index].ID, orders[index].UserID)
		case orders[index].Status == HeroSMSSMSOrderStatusActive && heroSMSSMSComplaintNeedsReconciliation(orders[index].ComplaintStatus):
			processErr = reconcileHeroSMSSMSComplaint(ctx, client, orders[index].ID, orders[index].UserID)
		default:
			_, processErr = reconcileHeroSMSSMSOrder(ctx, client, orders[index].ID)
		}
		if processErr == nil {
			processed++
		}
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
