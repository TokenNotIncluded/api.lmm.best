package model

import (
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/logger"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

type TopUp struct {
	Id                    int     `json:"id"`
	UserId                int     `json:"user_id" gorm:"index"`
	Amount                int64   `json:"amount"` // deprecated integer projection
	PlatformAmountMicros  int64   `json:"platform_amount_micros" gorm:"not null;default:0"`
	CreditedQuota         int64   `json:"credited_quota" gorm:"not null;default:0"`
	ExpectedAmountMicros  int64   `json:"expected_amount_micros" gorm:"not null;default:0"`
	SettledAmountMicros   int64   `json:"settled_amount_micros" gorm:"not null;default:0"`
	SettlementCurrency    string  `json:"settlement_currency" gorm:"type:varchar(16);not null;default:''"`
	Money                 float64 `json:"money"`
	RefundedAmountMicros  int64   `json:"refunded_amount_micros" gorm:"not null;default:0"`
	RefundedQuota         int64   `json:"refunded_quota" gorm:"not null;default:0"`
	TradeNo               string  `json:"trade_no" gorm:"unique;type:varchar(255);index"`
	PaymentMethod         string  `json:"payment_method" gorm:"type:varchar(50)"`
	PaymentProvider       string  `json:"payment_provider" gorm:"type:varchar(50);default:'';uniqueIndex:idx_topup_provider_event,priority:1;uniqueIndex:idx_topup_provider_transaction,priority:1"`
	DiscountCodeId        int     `json:"discount_code_id,omitempty" gorm:"index"`
	DiscountPercent       int     `json:"discount_percent,omitempty"`
	ProviderProductId     string  `json:"provider_product_id" gorm:"type:varchar(255);not null;default:''"`
	ProviderStoreId       string  `json:"provider_store_id" gorm:"type:varchar(255);not null;default:''"`
	ProviderEventId       *string `json:"provider_event_id,omitempty" gorm:"type:varchar(255);uniqueIndex:idx_topup_provider_event,priority:2"`
	ProviderTransactionId *string `json:"provider_transaction_id,omitempty" gorm:"type:varchar(255);uniqueIndex:idx_topup_provider_transaction,priority:2"`
	CreateTime            int64   `json:"create_time"`
	CompleteTime          int64   `json:"complete_time"`
	Status                string  `json:"status"`
}

const (
	PaymentMethodStripe       = "stripe"
	PaymentMethodCreem        = "creem"
	PaymentMethodWaffo        = "waffo"
	PaymentMethodWaffoPancake = "waffo_pancake"
	PaymentMethodBalance      = "balance"
)

var (
	ErrPaymentEvidenceConflict = errors.New("payment evidence conflict")
	ErrInvalidTopUpQuota       = errors.New("invalid top-up quota")
	ErrTopUpQuotaLimitExceeded = errors.New("top-up quota limit exceeded")
	settlementLockShards       [64]sync.Mutex
)

type ExternalTopUpSettlement struct {
	TradeNo                    string
	PaymentProvider            string
	PaymentMethod              string
	SettlementCurrency         string
	SettledAmountMicros        int64
	ProviderQuotedAmountMicros int64
	ProviderProductId          string
	ProviderStoreId            string
	ProviderEventId            string
	ProviderTransactionId      string
	StripeCustomer             string
	CustomerEmail              string
}

func optionalEvidence(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

func evidenceValue(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func normalizeSettlement(settlement ExternalTopUpSettlement) ExternalTopUpSettlement {
	settlement.TradeNo = strings.TrimSpace(settlement.TradeNo)
	settlement.PaymentProvider = strings.TrimSpace(settlement.PaymentProvider)
	settlement.PaymentMethod = strings.TrimSpace(settlement.PaymentMethod)
	settlement.SettlementCurrency = strings.ToUpper(strings.TrimSpace(settlement.SettlementCurrency))
	settlement.ProviderProductId = strings.TrimSpace(settlement.ProviderProductId)
	settlement.ProviderStoreId = strings.TrimSpace(settlement.ProviderStoreId)
	settlement.ProviderEventId = strings.TrimSpace(settlement.ProviderEventId)
	settlement.ProviderTransactionId = strings.TrimSpace(settlement.ProviderTransactionId)
	settlement.StripeCustomer = strings.TrimSpace(settlement.StripeCustomer)
	settlement.CustomerEmail = strings.TrimSpace(settlement.CustomerEmail)
	return settlement
}

func expectedTopUpAmountMicros(topUp *TopUp) int64 {
	if topUp == nil {
		return 0
	}
	if topUp.ExpectedAmountMicros > 0 {
		return topUp.ExpectedAmountMicros
	}
	if topUp.Money <= 0 {
		return 0
	}
	return decimal.NewFromFloat(topUp.Money).
		Mul(decimal.NewFromInt(1_000_000)).
		Round(0).
		IntPart()
}

func settlementEvidenceMatches(topUp *TopUp, settlement ExternalTopUpSettlement) bool {
	if topUp == nil ||
		topUp.SettledAmountMicros != settlement.SettledAmountMicros ||
		!strings.EqualFold(strings.TrimSpace(topUp.SettlementCurrency), settlement.SettlementCurrency) ||
		topUp.ProviderStoreId != settlement.ProviderStoreId ||
		evidenceValue(topUp.ProviderEventId) != settlement.ProviderEventId ||
		evidenceValue(topUp.ProviderTransactionId) != settlement.ProviderTransactionId {
		return false
	}
	return topUp.ProviderProductId == settlement.ProviderProductId ||
		(topUp.PaymentProvider == PaymentProviderWaffoPancake && settlement.ProviderProductId == "")
}

func callbackProductEvidenceMatches(topUp *TopUp, settlement ExternalTopUpSettlement) bool {
	return topUp.ProviderProductId == settlement.ProviderProductId ||
		(topUp.PaymentProvider == PaymentProviderWaffoPancake && settlement.ProviderProductId == "")
}

func completedSettlementEvidenceCompatible(topUp *TopUp, settlement ExternalTopUpSettlement) bool {
	if topUp == nil || (topUp.SettledAmountMicros > 0 && topUp.SettledAmountMicros != settlement.SettledAmountMicros) {
		return false
	}
	if persisted := strings.TrimSpace(topUp.SettlementCurrency); persisted != "" &&
		(settlement.SettlementCurrency == "" || !strings.EqualFold(persisted, settlement.SettlementCurrency)) {
		return false
	}
	if topUp.ProviderProductId != "" && !callbackProductEvidenceMatches(topUp, settlement) {
		return false
	}
	return (topUp.ProviderStoreId == "" || topUp.ProviderStoreId == settlement.ProviderStoreId) &&
		(evidenceValue(topUp.ProviderEventId) == "" || evidenceValue(topUp.ProviderEventId) == settlement.ProviderEventId) &&
		(evidenceValue(topUp.ProviderTransactionId) == "" || evidenceValue(topUp.ProviderTransactionId) == settlement.ProviderTransactionId)
}

func completedSettlementEvidenceUpdates(topUp *TopUp, settlement ExternalTopUpSettlement) map[string]interface{} {
	updates := make(map[string]interface{}, 6)
	if topUp.SettledAmountMicros == 0 {
		updates["settled_amount_micros"] = settlement.SettledAmountMicros
	}
	if strings.TrimSpace(topUp.SettlementCurrency) == "" && settlement.SettlementCurrency != "" {
		updates["settlement_currency"] = settlement.SettlementCurrency
	}
	if topUp.ProviderProductId == "" && settlement.ProviderProductId != "" {
		updates["provider_product_id"] = settlement.ProviderProductId
	}
	if topUp.ProviderStoreId == "" && settlement.ProviderStoreId != "" {
		updates["provider_store_id"] = settlement.ProviderStoreId
	}
	if evidenceValue(topUp.ProviderEventId) == "" && settlement.ProviderEventId != "" {
		updates["provider_event_id"] = optionalEvidence(settlement.ProviderEventId)
	}
	if evidenceValue(topUp.ProviderTransactionId) == "" && settlement.ProviderTransactionId != "" {
		updates["provider_transaction_id"] = optionalEvidence(settlement.ProviderTransactionId)
	}
	return updates
}

func completedSettlementEvidenceCAS(query *gorm.DB, topUp *TopUp) *gorm.DB {
	query = query.
		Where("id = ? AND status = ?", topUp.Id, common.TopUpStatusSuccess).
		Where("settled_amount_micros = ?", topUp.SettledAmountMicros).
		Where("settlement_currency = ?", topUp.SettlementCurrency).
		Where("provider_product_id = ?", topUp.ProviderProductId).
		Where("provider_store_id = ?", topUp.ProviderStoreId)
	if eventID := evidenceValue(topUp.ProviderEventId); eventID == "" {
		query = query.Where("(provider_event_id IS NULL OR provider_event_id = '')")
	} else {
		query = query.Where("provider_event_id = ?", eventID)
	}
	if transactionID := evidenceValue(topUp.ProviderTransactionId); transactionID == "" {
		query = query.Where("(provider_transaction_id IS NULL OR provider_transaction_id = '')")
	} else {
		query = query.Where("provider_transaction_id = ?", transactionID)
	}
	return query
}

func reloadMatchingCompletedSettlement(db *gorm.DB, settlement ExternalTopUpSettlement) (*TopUp, bool, error) {
	var completed TopUp
	if err := db.Where("trade_no = ?", settlement.TradeNo).First(&completed).Error; err != nil {
		return nil, false, err
	}
	if completed.Status != common.TopUpStatusSuccess || completed.PaymentProvider != settlement.PaymentProvider {
		return &completed, false, nil
	}
	return &completed, settlementEvidenceMatches(&completed, settlement), nil
}

func settlementLock(tradeNo string) func() {
	hasher := fnv.New32a()
	_, _ = hasher.Write([]byte(tradeNo))
	lock := &settlementLockShards[hasher.Sum32()%uint32(len(settlementLockShards))]
	lock.Lock()
	return lock.Unlock
}

func evidenceAlreadyBound(tx *gorm.DB, settlement ExternalTopUpSettlement) (bool, error) {
	query := tx.Model(&TopUp{}).
		Where("payment_provider = ? AND trade_no <> ?", settlement.PaymentProvider, settlement.TradeNo)
	if settlement.ProviderEventId != "" {
		var count int64
		if err := query.Where("provider_event_id = ?", settlement.ProviderEventId).Count(&count).Error; err != nil {
			return false, err
		}
		if count > 0 {
			return true, nil
		}
	}
	if settlement.ProviderTransactionId != "" {
		var count int64
		if err := query.Where("provider_transaction_id = ?", settlement.ProviderTransactionId).Count(&count).Error; err != nil {
			return false, err
		}
		if count > 0 {
			return true, nil
		}
	}
	return false, nil
}

func uniqueConstraintError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique constraint") ||
		strings.Contains(message, "duplicate entry") ||
		strings.Contains(message, "duplicated key")
}

// consumeDiscountCodeUsage consumes the slot reserved when the order was
// persisted. It deliberately never re-checks MaxUses after provider payment:
// a later capacity change must not turn a successful charge into missing
// wallet credit.
func consumeDiscountCodeUsage(tx *gorm.DB, topUp *TopUp) error {
	// Reservation helpers live in discount_code_reservation.go and share this transaction.
	return consumeReservedDiscountCodeUsageTx(tx, topUp)
}

// CompleteExternalTopUp is the only external top-up settlement path. It binds
// signed provider evidence, atomically transitions the order with a CAS, and
// credits the user in the same database transaction.
func CompleteExternalTopUp(settlement ExternalTopUpSettlement) (*TopUp, error) {
	settlement = normalizeSettlement(settlement)
	if DB == nil || settlement.TradeNo == "" || settlement.PaymentProvider == "" ||
		settlement.SettledAmountMicros <= 0 ||
		(settlement.ProviderEventId == "" && settlement.ProviderTransactionId == "") {
		return nil, gorm.ErrInvalidData
	}

	unlock := settlementLock(settlement.TradeNo)
	defer unlock()

	completed, err := completeExternalTopUpOnDB(DB, settlement)
	if err != nil {
		if uniqueConstraintError(err) {
			return nil, ErrPaymentEvidenceConflict
		}
		return nil, err
	}
	InvalidatePaidTopUpAggregate(completed.UserId)
	_ = invalidateUserCache(completed.UserId)
	return completed, nil
}

// completeExternalTopUpOnDB is the database transaction and CAS boundary.
// Tests call it with independent handles so correctness does not depend on the
// process-local compatibility lock in CompleteExternalTopUp.
func completeExternalTopUpOnDB(db *gorm.DB, settlement ExternalTopUpSettlement) (*TopUp, error) {
	settlement = normalizeSettlement(settlement)
	var completed TopUp
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		err = db.Transaction(func(tx *gorm.DB) error {
			if err := lockForUpdate(tx).Where("trade_no = ?", settlement.TradeNo).First(&completed).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return ErrTopUpNotFound
				}
				return err
			}
			if completed.PaymentProvider != settlement.PaymentProvider {
				return ErrPaymentMethodMismatch
			}
			if completed.CreditedQuota > int64(common.MaxWalletQuota) || completed.CreditedQuota < int64(common.MinWalletQuota) {
				return ErrInvalidTopUpQuota
			}

			expectedAmountMicros := expectedTopUpAmountMicros(&completed)
			if expectedAmountMicros <= 0 {
				return ErrPaymentEvidenceConflict
			}
			if settlement.ProviderQuotedAmountMicros > 0 {
				if settlement.PaymentProvider != PaymentProviderStripe ||
					settlement.ProviderQuotedAmountMicros != expectedAmountMicros ||
					settlement.SettledAmountMicros > settlement.ProviderQuotedAmountMicros {
					return ErrPaymentEvidenceConflict
				}
			} else if expectedAmountMicros != settlement.SettledAmountMicros {
				return ErrPaymentEvidenceConflict
			}
			if completed.SettlementCurrency != "" &&
				(settlement.SettlementCurrency == "" || !strings.EqualFold(completed.SettlementCurrency, settlement.SettlementCurrency)) {
				return ErrPaymentEvidenceConflict
			}
			if completed.ProviderProductId != "" && completed.ProviderProductId != settlement.ProviderProductId {
				if completed.PaymentProvider != PaymentProviderWaffoPancake || settlement.ProviderProductId != "" {
					return ErrPaymentEvidenceConflict
				}
			}
			if completed.ProviderStoreId != "" && completed.ProviderStoreId != settlement.ProviderStoreId {
				return ErrPaymentEvidenceConflict
			}
			if completed.Status == common.TopUpStatusSuccess {
				if !completedSettlementEvidenceCompatible(&completed, settlement) {
					return ErrPaymentEvidenceConflict
				}
				updates := completedSettlementEvidenceUpdates(&completed, settlement)
				if len(updates) == 0 {
					if !settlementEvidenceMatches(&completed, settlement) {
						return ErrPaymentEvidenceConflict
					}
					return nil
				}
				bound, err := evidenceAlreadyBound(tx, settlement)
				if err != nil {
					return err
				}
				if bound {
					return ErrPaymentEvidenceConflict
				}
				result := completedSettlementEvidenceCAS(tx.Model(&TopUp{}), &completed).Updates(updates)
				if result.Error != nil {
					return result.Error
				}
				if result.RowsAffected != 1 {
					return ErrTopUpStatusInvalid
				}
				if value, ok := updates["settled_amount_micros"]; ok {
					completed.SettledAmountMicros = value.(int64)
				}
				if value, ok := updates["settlement_currency"]; ok {
					completed.SettlementCurrency = value.(string)
				}
				if value, ok := updates["provider_product_id"]; ok {
					completed.ProviderProductId = value.(string)
				}
				if value, ok := updates["provider_store_id"]; ok {
					completed.ProviderStoreId = value.(string)
				}
				if value, ok := updates["provider_event_id"]; ok {
					completed.ProviderEventId = value.(*string)
				}
				if value, ok := updates["provider_transaction_id"]; ok {
					completed.ProviderTransactionId = value.(*string)
				}
				return nil
			}
			if completed.Status != common.TopUpStatusPending {
				return ErrTopUpStatusInvalid
			}
			if completed.PaymentProvider == PaymentProviderCreem && strings.TrimSpace(completed.SettlementCurrency) == "" {
				return ErrPaymentEvidenceConflict
			}
			if completed.PaymentProvider == PaymentProviderEpay && !epayHasImmutableSettlementSnapshot(&completed) {
				// Historical Epay rows stored only an ambiguous float Money value.
				// They may represent either already-converted USD or CNY and cannot
				// be settled safely. Require manual reconciliation instead of
				// guessing and crediting at today's exchange configuration.
				// Virtual units such as LDC are valid when the order snapshotted
				// amount, quota, and currency before the user paid.
				return ErrPaymentEvidenceConflict
			}

			bound, err := evidenceAlreadyBound(tx, settlement)
			if err != nil {
				return err
			}
			if bound {
				return ErrPaymentEvidenceConflict
			}

			quota := normalizedTopUpCreditedQuota(&completed)
			if quota <= 0 {
				return errors.New("无效的充值额度")
			}
			completeTime := common.GetTimestamp()
			providerProductID := settlement.ProviderProductId
			if completed.PaymentProvider == PaymentProviderWaffoPancake && providerProductID == "" {
				providerProductID = completed.ProviderProductId
			}
			updates := map[string]interface{}{
				"credited_quota":          quota,
				"expected_amount_micros":  expectedAmountMicros,
				"settled_amount_micros":   settlement.SettledAmountMicros,
				"settlement_currency":     settlement.SettlementCurrency,
				"provider_product_id":     providerProductID,
				"provider_store_id":       settlement.ProviderStoreId,
				"provider_event_id":       optionalEvidence(settlement.ProviderEventId),
				"provider_transaction_id": optionalEvidence(settlement.ProviderTransactionId),
				"complete_time":           completeTime,
				"status":                  common.TopUpStatusSuccess,
			}
			if settlement.PaymentMethod != "" {
				updates["payment_method"] = settlement.PaymentMethod
			}
			result := tx.Model(&TopUp{}).
				Where("id = ? AND status = ?", completed.Id, common.TopUpStatusPending).
				Updates(updates)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return ErrTopUpStatusInvalid
			}

			completed.CreditedQuota = quota
			completed.ExpectedAmountMicros = expectedAmountMicros
			completed.SettledAmountMicros = settlement.SettledAmountMicros
			completed.SettlementCurrency = settlement.SettlementCurrency
			completed.ProviderProductId = providerProductID
			completed.ProviderStoreId = settlement.ProviderStoreId
			completed.ProviderEventId = optionalEvidence(settlement.ProviderEventId)
			completed.ProviderTransactionId = optionalEvidence(settlement.ProviderTransactionId)
			completed.CompleteTime = completeTime
			completed.Status = common.TopUpStatusSuccess
			if settlement.PaymentMethod != "" {
				completed.PaymentMethod = settlement.PaymentMethod
			}

			userUpdates := map[string]interface{}{}
			if settlement.StripeCustomer != "" {
				userUpdates["stripe_customer"] = settlement.StripeCustomer
			}
			if settlement.CustomerEmail != "" {
				var user User
				if err := tx.Select("email").First(&user, completed.UserId).Error; err != nil {
					return err
				}
				if user.Email == "" {
					userUpdates["email"] = settlement.CustomerEmail
				}
			}
			if err := creditTopUpQuota(tx, completed.UserId, quota, userUpdates); err != nil {
				return err
			}
			if err := consumeDiscountCodeUsage(tx, &completed); err != nil {
				return err
			}
			return nil
		})
		if err == nil {
			return &completed, nil
		}
		raceError := errors.Is(err, ErrTopUpStatusInvalid) || uniqueConstraintError(err) || strings.Contains(strings.ToLower(err.Error()), "locked")
		if raceError {
			reloaded, matches, reloadErr := reloadMatchingCompletedSettlement(db, settlement)
			if reloadErr == nil && matches {
				return reloaded, nil
			}
			if uniqueConstraintError(err) && reloadErr == nil {
				return nil, ErrPaymentEvidenceConflict
			}
		}
		if !raceError || attempt == 4 {
			break
		}
		time.Sleep(time.Duration(attempt+1) * 10 * time.Millisecond)
	}
	if err != nil {
		if uniqueConstraintError(err) {
			return nil, ErrPaymentEvidenceConflict
		}
		return nil, err
	}
	return nil, gorm.ErrInvalidData
}

const (
	PaymentProviderEpay         = "epay"
	PaymentProviderStripe       = "stripe"
	PaymentProviderCreem        = "creem"
	PaymentProviderWaffo        = "waffo"
	PaymentProviderWaffoPancake = "waffo_pancake"
	PaymentProviderBalance      = "balance"
)

// LinuxDO Credit is not fiat. New records should use an explicit LDC alias.
// Legacy rows also used method=epay; those are internal unless immutable CNY
// settlement evidence proves they came from the real Epay gateway.
var linuxDOCreditPaymentMethods = []string{
	"ldc",
	"linuxdo",
	"linux_do",
	"linuxdo_credit",
}

// IsFinancialPaymentSource distinguishes cash/card gateways from internal
// credits. LinuxDO points are persisted through the ePay adapter for
// compatibility, but they are not revenue and must not enter the finance
// dashboard.
func IsFinancialPaymentSource(method, provider string) bool {
	method = strings.ToLower(strings.TrimSpace(method))
	provider = strings.ToLower(strings.TrimSpace(provider))
	if method == PaymentMethodBalance || provider == PaymentProviderBalance {
		return false
	}
	if provider == PaymentProviderEpay {
		for _, internalMethod := range linuxDOCreditPaymentMethods {
			if method == internalMethod {
				return false
			}
		}
	}
	switch method {
	case "gift", "bonus", "checkin", "invite", "bounty", "linuxdo", "linux_do", "linuxdo_credit", "internal", "admin":
		return false
	}
	switch provider {
	case "gift", "bonus", "checkin", "invite", "bounty", "linuxdo", "linux_do", "linuxdo_credit", "internal", "admin":
		return false
	}
	return true
}

var (
	ErrPaymentMethodMismatch = errors.New("payment method mismatch")
	ErrTopUpNotFound         = errors.New("topup not found")
	ErrTopUpStatusInvalid    = errors.New("topup status invalid")
	ErrRefundAmountInvalid   = errors.New("refund amount conflicts with settled topup")
)

func (topUp *TopUp) Insert() error {
	if topUp == nil {
		return gorm.ErrInvalidData
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := reserveDiscountCodeUsageTx(tx, topUp); err != nil {
			return err
		}
		return tx.Create(topUp).Error
	})
}

func topUpQuotaMaxCurrent(creditedQuota int64) (int64, error) {
	if creditedQuota <= 0 || creditedQuota > int64(common.MaxWalletQuota) {
		return 0, ErrInvalidTopUpQuota
	}
	return int64(common.MaxWalletQuota) - creditedQuota, nil
}

// ValidateTopUpQuotaCapacity performs the cheap pre-payment check. Settlement
// repeats the same predicate atomically because the wallet may change while a
// checkout is open.
func ValidateTopUpQuotaCapacity(userId int, creditedQuota int64) error {
	maxCurrentQuota, err := topUpQuotaMaxCurrent(creditedQuota)
	if err != nil {
		return err
	}
	var user User
	if err := DB.Select("quota").Where("id = ?", userId).First(&user).Error; err != nil {
		return err
	}
	if int64(user.Quota) < int64(common.MinWalletQuota) || int64(user.Quota) > maxCurrentQuota {
		return ErrTopUpQuotaLimitExceeded
	}
	return nil
}

// creditTopUpQuota applies the wallet ceiling and the increment in one SQL
// update. This closes the race where two payment callbacks both pass a
// separate balance read before crediting the same account.
func creditTopUpQuota(tx *gorm.DB, userId int, creditedQuota int64, updates map[string]interface{}) error {
	if _, err := topUpQuotaMaxCurrent(creditedQuota); err != nil {
		return err
	}
	quotaDelta := int(creditedQuota)
	if int64(quotaDelta) != creditedQuota {
		return ErrInvalidTopUpQuota
	}
	// Keep this guarded multi-column UPDATE: using ApplyWalletQuotaDelta here
	// would split the wallet credit from provider metadata stored in updates.
	query, err := GuardWalletQuotaDelta(tx.Model(&User{}).Where("id = ?", userId), quotaDelta)
	if err != nil {
		return ErrInvalidTopUpQuota
	}
	updateFields := make(map[string]interface{}, len(updates)+1)
	for key, value := range updates {
		updateFields[key] = value
	}
	updateFields["quota"] = gorm.Expr("quota + ?", quotaDelta)
	result := query.Updates(updateFields)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 1 {
		return nil
	}
	var count int64
	if err := tx.Model(&User{}).Where("id = ?", userId).Count(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		return gorm.ErrRecordNotFound
	}
	return ErrTopUpQuotaLimitExceeded
}

func (topUp *TopUp) Update() error {
	var err error
	err = DB.Save(topUp).Error
	return err
}

func GetTopUpById(id int) *TopUp {
	var topUp *TopUp
	var err error
	err = DB.Where("id = ?", id).First(&topUp).Error
	if err != nil {
		return nil
	}
	return topUp
}

func GetTopUpByTradeNo(tradeNo string) *TopUp {
	var topUp *TopUp
	var err error
	err = DB.Where("trade_no = ?", tradeNo).First(&topUp).Error
	if err != nil {
		return nil
	}
	return topUp
}

// GetTopUpByProviderTransaction resolves a provider's durable payment
// transaction identifier back to the original local top-up. Webhook handlers
// must still validate the resulting order status before changing balances.
func GetTopUpByProviderTransaction(paymentProvider, providerTransactionID string) (*TopUp, error) {
	paymentProvider = strings.TrimSpace(paymentProvider)
	providerTransactionID = strings.TrimSpace(providerTransactionID)
	if DB == nil || paymentProvider == "" || providerTransactionID == "" {
		return nil, ErrTopUpNotFound
	}
	var topUp TopUp
	err := DB.Where("payment_provider = ? AND provider_transaction_id = ?", paymentProvider, providerTransactionID).First(&topUp).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrTopUpNotFound
	}
	if err != nil {
		return nil, err
	}
	return &topUp, nil
}

// HasSuccessfulPaidTopUp reports whether the account has completed at least
// one real-money recharge. Quota grants, redemption codes, and balance-funded
// subscription purchases do not unlock paid-only product surfaces.
func HasSuccessfulPaidTopUp(userId int) (bool, error) {
	return HasSuccessfulPaidTopUpWithTx(DB, userId, false)
}

// HasSuccessfulPaidTopUpWithTx applies the same activation predicate as the
// ordinary access path. The transactional authorization path locks the
// matching fact rows so a concurrent update or deletion is serialized before
// credential commit.
func HasSuccessfulPaidTopUpWithTx(tx *gorm.DB, userId int, lockFacts bool) (bool, error) {
	if userId <= 0 {
		return false, nil
	}
	if tx == nil {
		return false, gorm.ErrInvalidDB
	}

	creditedQuotaExpression, creditedQuotaArgs := positiveNormalizedCreditedQuotaSQL()
	query := successfulExternalPaidTopUpQuery(tx.Model(&TopUp{})).
		Where("user_id = ?", userId).
		Where("("+creditedQuotaExpression+") > 0", creditedQuotaArgs...)
	if !lockFacts {
		var matchingRows int64
		if err := query.Count(&matchingRows).Error; err != nil {
			return false, err
		}
		return matchingRows > 0, nil
	}
	var matchingIDs []int
	if err := lockForUpdate(query).Order("id").Pluck("id", &matchingIDs).Error; err != nil {
		return false, err
	}
	return len(matchingIDs) > 0, nil
}

func positiveNormalizedCreditedQuotaSQL() (string, []interface{}) {
	expression := "CASE WHEN NOT (COALESCE(payment_provider, '') IN ? OR (COALESCE(payment_provider, '') = '' AND COALESCE(payment_method, '') IN ?)) THEN 0 " +
		"WHEN credited_quota > 0 THEN credited_quota " +
		"WHEN payment_provider = ? OR payment_method = ? THEN amount " +
		"WHEN payment_provider IN ? OR payment_method IN ? OR (COALESCE(payment_provider, '') = '' AND payment_method IN ?) THEN amount * ? " +
		"ELSE 0 END"
	return expression, []interface{}{
		[]string{PaymentProviderEpay, PaymentProviderStripe, PaymentProviderCreem, PaymentProviderWaffo, PaymentProviderWaffoPancake},
		[]string{PaymentMethodStripe, PaymentMethodCreem, PaymentMethodWaffo, PaymentMethodWaffoPancake, "alipay", "wxpay"},
		PaymentProviderCreem,
		PaymentMethodCreem,
		[]string{PaymentProviderEpay, PaymentProviderStripe, PaymentProviderWaffo, PaymentProviderWaffoPancake},
		[]string{PaymentMethodStripe, PaymentMethodWaffo, PaymentMethodWaffoPancake},
		[]string{"alipay", "wxpay"},
		common.QuotaPerUnit,
	}
}

// successfulExternalPaidTopUpQuery applies the single activation predicate
// shared by paid-only access checks and trust-level aggregates. The positive
// credited-quota requirement is evaluated through normalizedTopUpCreditedQuota
// so legacy provider-specific amount units remain safe and portable.
func successfulExternalPaidTopUpQuery(query *gorm.DB) *gorm.DB {
	return query.
		Where("status = ?", common.TopUpStatusSuccess).
		Where("(settled_amount_micros > 0 OR (settled_amount_micros = 0 AND money > 0))").
		Where("(payment_method IS NULL OR payment_method <> ?)", PaymentMethodBalance).
		Where("(payment_provider IS NULL OR payment_provider <> ?)", PaymentProviderBalance).
		Where(
			"NOT (LOWER(COALESCE(payment_provider, '')) = ? AND LOWER(COALESCE(payment_method, '')) IN ?)",
			PaymentProviderEpay,
			linuxDOCreditPaymentMethods,
		).
		Where(
			"NOT (LOWER(COALESCE(payment_provider, '')) = ? AND LOWER(COALESCE(payment_method, '')) = ? AND (UPPER(COALESCE(settlement_currency, '')) <> ? OR (expected_amount_micros <= 0 AND settled_amount_micros <= 0)))",
			PaymentProviderEpay,
			PaymentProviderEpay,
			"CNY",
		)
}

func topUpPaidAmountMicros(topUp *TopUp) int64 {
	if topUp == nil {
		return 0
	}
	if topUp.SettledAmountMicros > 0 {
		return topUp.SettledAmountMicros
	}
	return expectedTopUpAmountMicros(topUp)
}

// StandardTopUpCreditedQuota converts an ePay/Stripe/FAST/Waffo display
// amount into the exact quota that their completion paths grant.
func standardTopUpCreditedQuotaChecked(amount int64) (int64, error) {
	if amount <= 0 {
		return 0, nil
	}
	if common.QuotaPerUnit <= 0 || math.IsNaN(common.QuotaPerUnit) || math.IsInf(common.QuotaPerUnit, 0) {
		return 0, ErrInvalidTopUpQuota
	}

	// Decimal.IntPart delegates to big.Int.Int64, which keeps only the low
	// 64 bits when the integer does not fit. Check the arbitrary-precision
	// integer first so an extreme admin multiplier can never wrap into a
	// seemingly valid, small credit.
	credited := decimal.NewFromInt(amount).Mul(decimal.NewFromFloat(common.QuotaPerUnit))
	creditedInteger := credited.BigInt()
	if !creditedInteger.IsInt64() {
		return 0, ErrInvalidTopUpQuota
	}
	quota := creditedInteger.Int64()
	if quota <= 0 || quota > int64(common.MaxWalletQuota) {
		return 0, ErrInvalidTopUpQuota
	}
	if err := common.ValidateWalletQuota(int(quota)); err != nil {
		return 0, ErrInvalidTopUpQuota
	}
	return quota, nil
}

// StandardTopUpCreditedQuota preserves the historical value-only API while
// failing closed on invalid or unrepresentable conversions. Payment entry
// points validate the returned value before creating an order, and settlement
// paths reject zero as an invalid quota.
func StandardTopUpCreditedQuota(amount int64) int64 {
	quota, err := standardTopUpCreditedQuotaChecked(amount)
	if err != nil {
		return 0
	}
	return quota
}

func normalizedTopUpCreditedQuota(topUp *TopUp) int64 {
	if topUp == nil {
		return 0
	}
	if topUp.CreditedQuota > 0 && (knownExternalTopUpSource(topUp) || epayHasImmutableSettlementSnapshot(topUp)) {
		return topUp.CreditedQuota
	}
	if !knownExternalTopUpSource(topUp) {
		return 0
	}
	switch {
	case topUp.PaymentProvider == PaymentProviderCreem || topUp.PaymentMethod == PaymentMethodCreem:
		return topUp.Amount
	case topUp.PaymentProvider == PaymentProviderEpay,
		topUp.PaymentProvider == PaymentProviderStripe,
		topUp.PaymentProvider == PaymentProviderWaffo,
		topUp.PaymentProvider == PaymentProviderWaffoPancake,
		topUp.PaymentMethod == PaymentMethodStripe,
		topUp.PaymentMethod == PaymentMethodWaffo,
		topUp.PaymentMethod == PaymentMethodWaffoPancake,
		(strings.TrimSpace(topUp.PaymentProvider) == "" && (topUp.PaymentMethod == "alipay" || topUp.PaymentMethod == "wxpay")):
		return StandardTopUpCreditedQuota(topUp.Amount)
	default:
		return 0
	}
}

func normalizedTopUpCreditedQuotaInt(topUp *TopUp) (int, error) {
	quota := normalizedTopUpCreditedQuota(topUp)
	if quota <= 0 || quota > int64(common.MaxWalletQuota) {
		return 0, ErrInvalidTopUpQuota
	}
	value := int(quota)
	if int64(value) != quota {
		return 0, ErrInvalidTopUpQuota
	}
	if err := common.ValidateWalletQuota(value); err != nil {
		return 0, ErrInvalidTopUpQuota
	}
	return value, nil
}

func isLegacyLinuxDOCreditTopUp(topUp *TopUp) bool {
	if topUp == nil || !strings.EqualFold(strings.TrimSpace(topUp.PaymentProvider), PaymentProviderEpay) {
		return false
	}
	method := strings.ToLower(strings.TrimSpace(topUp.PaymentMethod))
	for _, internalMethod := range linuxDOCreditPaymentMethods {
		if method == internalMethod {
			return true
		}
	}
	if method != PaymentProviderEpay {
		return false
	}
	return !strings.EqualFold(strings.TrimSpace(topUp.SettlementCurrency), "CNY") ||
		(topUp.ExpectedAmountMicros <= 0 && topUp.SettledAmountMicros <= 0)
}

// EpayHasImmutableSettlementSnapshot reports whether an Epay order recorded
// amount, credited quota, and settlement unit before redirecting to the
// gateway. Historical rows stored only an ambiguous float Money value and
// cannot be settled safely. Virtual units such as LDC are valid snapshots;
// requiring CNY here would take payment and then refuse to credit.
func EpayHasImmutableSettlementSnapshot(topUp *TopUp) bool {
	return epayHasImmutableSettlementSnapshot(topUp)
}

func epayHasImmutableSettlementSnapshot(topUp *TopUp) bool {
	if topUp == nil {
		return false
	}
	return topUp.ExpectedAmountMicros > 0 &&
		topUp.CreditedQuota > 0 &&
		strings.TrimSpace(topUp.SettlementCurrency) != ""
}

func knownExternalTopUpSource(topUp *TopUp) bool {
	if topUp == nil || isLegacyLinuxDOCreditTopUp(topUp) {
		return false
	}
	switch topUp.PaymentProvider {
	case PaymentProviderEpay, PaymentProviderStripe, PaymentProviderCreem, PaymentProviderWaffo, PaymentProviderWaffoPancake:
		return true
	}
	if strings.TrimSpace(topUp.PaymentProvider) != "" {
		return false
	}
	switch topUp.PaymentMethod {
	case PaymentMethodStripe, PaymentMethodCreem, PaymentMethodWaffo, PaymentMethodWaffoPancake, "alipay", "wxpay":
		return true
	default:
		return false
	}
}

func topUpActivityAnchor(topUp *TopUp) int64 {
	if topUp == nil {
		return 0
	}
	if topUp.CompleteTime > 0 {
		return topUp.CompleteTime
	}
	return topUp.CreateTime
}

func UpdatePendingTopUpStatus(tradeNo string, expectedPaymentProvider string, targetStatus string) error {
	if tradeNo == "" {
		return errors.New("未提供支付单号")
	}

	refCol := "`trade_no`"
	if common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		refCol = `"trade_no"`
	}

	return DB.Transaction(func(tx *gorm.DB) error {
		topUp := &TopUp{}
		if err := lockForUpdate(tx).Where(refCol+" = ?", tradeNo).First(topUp).Error; err != nil {
			return ErrTopUpNotFound
		}
		if expectedPaymentProvider != "" && topUp.PaymentProvider != expectedPaymentProvider {
			return ErrPaymentMethodMismatch
		}
		if topUp.Status != common.TopUpStatusPending {
			return ErrTopUpStatusInvalid
		}

		topUp.Status = targetStatus
		if err := tx.Save(topUp).Error; err != nil {
			return err
		}
		if targetStatus != common.TopUpStatusPending && targetStatus != common.TopUpStatusSuccess {
			return releaseDiscountCodeReservationTx(tx, topUp.TradeNo)
		}
		return nil
	})
}

// RechargeEpay 原子完成易支付订单：订单行锁、状态校验、成功更新与用户额度增加
// 在同一个事务内完成，因此同一订单的并发/重复回调（包括多实例部署下）最多充值一次。
// alreadyDone=true 表示订单此前已完成，本次为幂等重复回调。
// 进程内的 LockOrder 只是优化，正确性由本函数的数据库行锁保证。
func RechargeEpay(tradeNo string, actualPaymentMethod string, callerIp string) (alreadyDone bool, err error) {
	if tradeNo == "" {
		return false, errors.New("未提供支付单号")
	}

	refCol := "`trade_no`"
	if common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		refCol = `"trade_no"`
	}

	var quotaToAdd int
	topUp := &TopUp{}
	err = DB.Transaction(func(tx *gorm.DB) error {
		if err := lockForUpdate(tx).Where(refCol+" = ?", tradeNo).First(topUp).Error; err != nil {
			return ErrTopUpNotFound
		}
		if topUp.PaymentProvider != PaymentProviderEpay {
			return ErrPaymentMethodMismatch
		}
		if topUp.Status == common.TopUpStatusSuccess {
			alreadyDone = true
			return nil
		}
		if topUp.Status != common.TopUpStatusPending {
			return ErrTopUpStatusInvalid
		}
		if actualPaymentMethod != "" && topUp.PaymentMethod != actualPaymentMethod {
			topUp.PaymentMethod = actualPaymentMethod
		}
		if !epayHasImmutableSettlementSnapshot(topUp) {
			return ErrPaymentEvidenceConflict
		}
		quotaToAdd = int(topUp.CreditedQuota)
		if quotaToAdd <= 0 || int64(quotaToAdd) != topUp.CreditedQuota || common.ValidateWalletQuota(quotaToAdd) != nil {
			return ErrInvalidTopUpQuota
		}
		topUp.SettledAmountMicros = topUp.ExpectedAmountMicros
		topUp.CompleteTime = common.GetTimestamp()
		topUp.Status = common.TopUpStatusSuccess
		if err := tx.Save(topUp).Error; err != nil {
			return err
		}
		return creditTopUpQuota(tx, topUp.UserId, int64(quotaToAdd), nil)
	})
	if err != nil {
		if !errors.Is(err, ErrTopUpNotFound) && !errors.Is(err, ErrPaymentMethodMismatch) && !errors.Is(err, ErrTopUpStatusInvalid) {
			common.SysError("epay topup failed: " + err.Error())
		}
		return false, err
	}
	if alreadyDone {
		return true, nil
	}
	syncCreditUserQuotaCache(topUp.UserId, quotaToAdd, "epay topup")

	common.SysLog(fmt.Sprintf("易支付充值成功 trade_no=%s user_id=%d quota_to_add=%d money=%.2f", topUp.TradeNo, topUp.UserId, quotaToAdd, topUp.Money))
	RecordTopupLog(topUp.UserId, fmt.Sprintf("使用在线充值成功，充值金额: %v，支付金额：%f", logger.LogQuota(quotaToAdd), topUp.Money), callerIp, topUp.PaymentMethod, PaymentProviderEpay)
	return false, nil
}

func Recharge(referenceId string, customerId string, callerIp string) (err error) {
	if referenceId == "" {
		return errors.New("未提供支付单号")
	}

	var quota int64
	topUp := &TopUp{}

	refCol := "`trade_no`"
	if common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		refCol = `"trade_no"`
	}

	err = DB.Transaction(func(tx *gorm.DB) error {
		err := lockForUpdate(tx).Where(refCol+" = ?", referenceId).First(topUp).Error
		if err != nil {
			return errors.New("充值订单不存在")
		}

		if topUp.PaymentProvider != PaymentProviderStripe {
			return ErrPaymentMethodMismatch
		}

		if topUp.Status != common.TopUpStatusPending {
			return errors.New("充值订单状态错误")
		}

		quota = normalizedTopUpCreditedQuota(topUp)
		if quota <= 0 {
			return errors.New("无效的充值额度")
		}
		topUp.CreditedQuota = quota
		topUp.CompleteTime = common.GetTimestamp()
		topUp.Status = common.TopUpStatusSuccess
		err = tx.Save(topUp).Error
		if err != nil {
			return err
		}

		return creditTopUpQuota(tx, topUp.UserId, quota, map[string]interface{}{
			"stripe_customer": customerId,
		})
	})

	if err != nil {
		common.SysError("topup failed: " + err.Error())
		return errors.New("充值失败，请稍后重试")
	}
	InvalidatePaidTopUpAggregate(topUp.UserId)
	syncCreditUserQuotaCache(topUp.UserId, int(quota), "stripe topup")

	RecordTopupLog(topUp.UserId, fmt.Sprintf("使用在线充值成功，充值金额: %v，支付金额：%d", logger.FormatQuota(int(quota)), topUp.Amount), callerIp, topUp.PaymentMethod, PaymentMethodStripe)

	return nil
}

// topUpQueryWindowSeconds 限制充值记录查询的时间窗口（秒）。
const topUpQueryWindowSeconds int64 = 30 * 24 * 60 * 60

// topUpQueryCutoff 返回允许查询的最早 create_time（秒级 Unix 时间戳）。
func topUpQueryCutoff() int64 {
	return common.GetTimestamp() - topUpQueryWindowSeconds
}

func GetUserTopUps(userId int, pageInfo *common.PageInfo) (topups []*TopUp, total int64, err error) {
	// Start transaction
	tx := DB.Begin()
	if tx.Error != nil {
		return nil, 0, tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	cutoff := topUpQueryCutoff()

	// Get total count within transaction
	err = tx.Model(&TopUp{}).Where("user_id = ? AND create_time >= ?", userId, cutoff).Count(&total).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	// Get paginated topups within same transaction
	err = tx.Where("user_id = ? AND create_time >= ?", userId, cutoff).Order("id desc").Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&topups).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	// Commit transaction
	if err = tx.Commit().Error; err != nil {
		return nil, 0, err
	}

	return topups, total, nil
}

// GetAllTopUps 获取全平台的充值记录（管理员使用，不限制时间窗口）
func GetAllTopUps(pageInfo *common.PageInfo) (topups []*TopUp, total int64, err error) {
	tx := DB.Begin()
	if tx.Error != nil {
		return nil, 0, tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	if err = tx.Model(&TopUp{}).Count(&total).Error; err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	if err = tx.Order("id desc").Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&topups).Error; err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	if err = tx.Commit().Error; err != nil {
		return nil, 0, err
	}

	return topups, total, nil
}

// searchTopUpCountHardLimit 搜索充值记录时 COUNT 的安全上限，
// 防止对超大表执行无界 COUNT 触发 DoS。
const searchTopUpCountHardLimit = 10000

// SearchUserTopUps 按订单号搜索某用户的充值记录
func SearchUserTopUps(userId int, keyword string, pageInfo *common.PageInfo) (topups []*TopUp, total int64, err error) {
	tx := DB.Begin()
	if tx.Error != nil {
		return nil, 0, tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	query := tx.Model(&TopUp{}).Where("user_id = ? AND create_time >= ?", userId, topUpQueryCutoff())
	if keyword != "" {
		pattern, perr := sanitizeLikePattern(keyword)
		if perr != nil {
			tx.Rollback()
			return nil, 0, perr
		}
		query = query.Where("trade_no LIKE ? ESCAPE '!'", pattern)
	}

	if err = query.Limit(searchTopUpCountHardLimit).Count(&total).Error; err != nil {
		tx.Rollback()
		common.SysError("failed to count search topups: " + err.Error())
		return nil, 0, errors.New("搜索充值记录失败")
	}

	if err = query.Order("id desc").Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&topups).Error; err != nil {
		tx.Rollback()
		common.SysError("failed to search topups: " + err.Error())
		return nil, 0, errors.New("搜索充值记录失败")
	}

	if err = tx.Commit().Error; err != nil {
		return nil, 0, err
	}
	return topups, total, nil
}

// SearchAllTopUps 按订单号搜索全平台充值记录（管理员使用，不限制时间窗口）
func SearchAllTopUps(keyword string, pageInfo *common.PageInfo) (topups []*TopUp, total int64, err error) {
	tx := DB.Begin()
	if tx.Error != nil {
		return nil, 0, tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	query := tx.Model(&TopUp{})
	if keyword != "" {
		pattern, perr := sanitizeLikePattern(keyword)
		if perr != nil {
			tx.Rollback()
			return nil, 0, perr
		}
		query = query.Where("trade_no LIKE ? ESCAPE '!'", pattern)
	}

	if err = query.Limit(searchTopUpCountHardLimit).Count(&total).Error; err != nil {
		tx.Rollback()
		common.SysError("failed to count search topups: " + err.Error())
		return nil, 0, errors.New("搜索充值记录失败")
	}

	if err = query.Order("id desc").Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&topups).Error; err != nil {
		tx.Rollback()
		common.SysError("failed to search topups: " + err.Error())
		return nil, 0, errors.New("搜索充值记录失败")
	}

	if err = tx.Commit().Error; err != nil {
		return nil, 0, err
	}
	return topups, total, nil
}

// ManualCompleteTopUp 管理员手动完成订单并给用户充值
func ManualCompleteTopUp(tradeNo string, callerIp string) error {
	if tradeNo == "" {
		return errors.New("未提供订单号")
	}

	refCol := "`trade_no`"
	if common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		refCol = `"trade_no"`
	}

	var userId int
	var quotaToAdd int
	var payMoney float64
	var paymentMethod string
	var completed bool

	err := DB.Transaction(func(tx *gorm.DB) error {
		topUp := &TopUp{}
		// 行级锁，避免并发补单
		if err := lockForUpdate(tx).Where(refCol+" = ?", tradeNo).First(topUp).Error; err != nil {
			return errors.New("充值订单不存在")
		}

		// 幂等处理：已成功直接返回
		if topUp.Status == common.TopUpStatusSuccess {
			return nil
		}

		if topUp.Status != common.TopUpStatusPending {
			return errors.New("订单状态不是待支付，无法补单")
		}

		creditedQuota, quotaErr := normalizedTopUpCreditedQuotaInt(topUp)
		if quotaErr != nil {
			return errors.New("无效的充值额度")
		}
		quotaToAdd = creditedQuota

		// 标记完成
		topUp.CreditedQuota = int64(quotaToAdd)
		topUp.CompleteTime = common.GetTimestamp()
		topUp.Status = common.TopUpStatusSuccess
		if err := tx.Save(topUp).Error; err != nil {
			return err
		}

		// 增加用户额度（立即写库，保持一致性）
		if err := creditTopUpQuota(tx, topUp.UserId, int64(quotaToAdd), nil); err != nil {
			return err
		}
		if err := consumeDiscountCodeUsage(tx, topUp); err != nil {
			return err
		}

		userId = topUp.UserId
		payMoney = topUp.Money
		paymentMethod = topUp.PaymentMethod
		completed = true
		return nil
	})

	if err != nil {
		return err
	}
	if !completed {
		return nil
	}
	InvalidatePaidTopUpAggregate(userId)

	// 事务外记录日志，避免阻塞
	syncCreditUserQuotaCache(userId, quotaToAdd, "manual topup")
	RecordTopupLog(userId, fmt.Sprintf("管理员补单成功，充值金额: %v，支付金额：%f", logger.FormatQuota(quotaToAdd), payMoney), callerIp, paymentMethod, "admin")
	return nil
}
func RechargeCreem(referenceId string, customerEmail string, customerName string, callerIp string) (err error) {
	if referenceId == "" {
		return errors.New("未提供支付单号")
	}

	var quota int64
	topUp := &TopUp{}

	refCol := "`trade_no`"
	if common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		refCol = `"trade_no"`
	}

	err = DB.Transaction(func(tx *gorm.DB) error {
		err := lockForUpdate(tx).Where(refCol+" = ?", referenceId).First(topUp).Error
		if err != nil {
			return errors.New("充值订单不存在")
		}

		if topUp.PaymentProvider != PaymentProviderCreem {
			return ErrPaymentMethodMismatch
		}

		if topUp.Status != common.TopUpStatusPending {
			return errors.New("充值订单状态错误")
		}

		quota = normalizedTopUpCreditedQuota(topUp)
		if quota <= 0 {
			return errors.New("无效的充值额度")
		}
		topUp.CreditedQuota = quota
		topUp.CompleteTime = common.GetTimestamp()
		topUp.Status = common.TopUpStatusSuccess
		err = tx.Save(topUp).Error
		if err != nil {
			return err
		}

		// 构建更新字段，优先使用邮箱，如果邮箱为空则使用用户名
		updateFields := map[string]interface{}{}

		// 如果有客户邮箱，尝试更新用户邮箱（仅当用户邮箱为空时）
		if customerEmail != "" {
			// 先检查用户当前邮箱是否为空
			var user User
			err = tx.Where("id = ?", topUp.UserId).First(&user).Error
			if err != nil {
				return err
			}

			// 如果用户邮箱为空，则更新为支付时使用的邮箱
			if user.Email == "" {
				updateFields["email"] = customerEmail
			}
		}

		err = creditTopUpQuota(tx, topUp.UserId, quota, updateFields)
		if err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		common.SysError("creem topup failed: " + err.Error())
		return errors.New("充值失败，请稍后重试")
	}
	InvalidatePaidTopUpAggregate(topUp.UserId)
	syncCreditUserQuotaCache(topUp.UserId, int(quota), "creem topup")

	RecordTopupLog(topUp.UserId, fmt.Sprintf("使用Creem充值成功，充值额度: %v，支付金额：%.2f", quota, topUp.Money), callerIp, topUp.PaymentMethod, PaymentMethodCreem)

	return nil
}

func RechargeWaffo(tradeNo string, callerIp string) (err error) {
	if tradeNo == "" {
		return errors.New("未提供支付单号")
	}

	var quotaToAdd int
	topUp := &TopUp{}

	refCol := "`trade_no`"
	if common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		refCol = `"trade_no"`
	}

	err = DB.Transaction(func(tx *gorm.DB) error {
		err := lockForUpdate(tx).Where(refCol+" = ?", tradeNo).First(topUp).Error
		if err != nil {
			return errors.New("充值订单不存在")
		}

		if topUp.PaymentProvider != PaymentProviderWaffo {
			return ErrPaymentMethodMismatch
		}

		if topUp.Status == common.TopUpStatusSuccess {
			return nil // 幂等：已成功直接返回
		}

		if topUp.Status != common.TopUpStatusPending {
			return errors.New("充值订单状态错误")
		}

		quotaToAdd, err = normalizedTopUpCreditedQuotaInt(topUp)
		if err != nil {
			return errors.New("无效的充值额度")
		}

		topUp.CreditedQuota = int64(quotaToAdd)
		topUp.CompleteTime = common.GetTimestamp()
		topUp.Status = common.TopUpStatusSuccess
		if err := tx.Save(topUp).Error; err != nil {
			return err
		}

		if err := creditTopUpQuota(tx, topUp.UserId, int64(quotaToAdd), nil); err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		common.SysError("waffo topup failed: " + err.Error())
		return errors.New("充值失败，请稍后重试")
	}
	InvalidatePaidTopUpAggregate(topUp.UserId)
	syncCreditUserQuotaCache(topUp.UserId, quotaToAdd, "waffo topup")

	if quotaToAdd > 0 {
		RecordTopupLog(topUp.UserId, fmt.Sprintf("Waffo充值成功，充值额度: %v，支付金额: %.2f", logger.FormatQuota(quotaToAdd), topUp.Money), callerIp, topUp.PaymentMethod, PaymentMethodWaffo)
	}

	return nil
}

func RechargeWaffoPancake(tradeNo string) (err error) {
	if tradeNo == "" {
		return errors.New("未提供支付单号")
	}

	var quotaToAdd int
	topUp := &TopUp{}

	refCol := "`trade_no`"
	if common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		refCol = `"trade_no"`
	}

	err = DB.Transaction(func(tx *gorm.DB) error {
		err := lockForUpdate(tx).Where(refCol+" = ?", tradeNo).First(topUp).Error
		if err != nil {
			return errors.New("充值订单不存在")
		}

		if topUp.PaymentProvider != PaymentProviderWaffoPancake {
			return ErrPaymentMethodMismatch
		}

		if topUp.Status == common.TopUpStatusSuccess {
			return nil
		}

		if topUp.Status != common.TopUpStatusPending {
			return errors.New("充值订单状态错误")
		}

		quotaToAdd, err = normalizedTopUpCreditedQuotaInt(topUp)
		if err != nil {
			return errors.New("无效的充值额度")
		}

		topUp.CreditedQuota = int64(quotaToAdd)
		topUp.CompleteTime = common.GetTimestamp()
		topUp.Status = common.TopUpStatusSuccess
		if err := tx.Save(topUp).Error; err != nil {
			return err
		}

		if err := creditTopUpQuota(tx, topUp.UserId, int64(quotaToAdd), nil); err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		common.SysError("waffo pancake topup failed: " + err.Error())
		return errors.New("充值失败，请稍后重试")
	}
	InvalidatePaidTopUpAggregate(topUp.UserId)
	syncCreditUserQuotaCache(topUp.UserId, quotaToAdd, "waffo pancake topup")

	if quotaToAdd > 0 {
		RecordLog(topUp.UserId, LogTypeTopup, fmt.Sprintf("Waffo Pancake充值成功，充值额度: %v，支付金额: %.2f", logger.FormatQuota(quotaToAdd), topUp.Money))
	}

	return nil
}
