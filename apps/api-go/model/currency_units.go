package model

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const CreditsPerUSDOptionKey = "CreditsPerUSD"
const LegacyPricingQuotaPerUnitOptionKey = "LegacyPricingQuotaPerUnit"
const PublicCreditsPerUSDOptionKey = common.PublicCreditsPerUSDOptionKey
const DefaultPublicCreditsPerUSD = "100000"

// InitializeCreditUnits creates the immutable anchor without rewriting any
// balance, price, paid order, pending order or refund snapshot. The unique
// option key chooses one winner if multiple nodes start simultaneously.
func InitializeCreditUnits(ctx context.Context) error {
	return initializeCreditUnits(ctx, true)
}

// VerifyCreditUnits preserves read-only startup/migration verification. A
// missing anchor must be prepared by an apply-mode node before serving money.
func VerifyCreditUnits(ctx context.Context) error {
	err := initializeCreditUnits(ctx, false)
	if errors.Is(err, common.ErrCreditUnitsUnavailable) {
		return fmt.Errorf("credit currency initialization required; run migrate --apply: %w", err)
	}
	return err
}

func initializeCreditUnits(ctx context.Context, allowCreate bool) error {
	if DB == nil {
		common.ClearCreditsPerUSD()
		return common.ErrCreditUnitsUnavailable
	}
	var anchor, legacy, public decimal.Decimal
	var publicConfigured bool
	var err error
	for attempt := 0; attempt < 6; attempt++ {
		err = DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if allowCreate {
				// The legacy calibration row is also the cross-node fence for
				// administrators racing the first currency initialization.
				if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&Option{Key: "QuotaPerUnit", Value: "500000"}).Error; err != nil {
					return err
				}
				var calibration Option
				if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("key = ?", "QuotaPerUnit").First(&calibration).Error; err != nil {
					return err
				}
			}
			var options []Option
			keys := []string{CreditsPerUSDOptionKey, LegacyPricingQuotaPerUnitOptionKey, "QuotaPerUnit", "USDExchangeRate", "TopUpPlatformUnitsPerCNY", PublicCreditsPerUSDOptionKey}
			// A single authoritative snapshot avoids mixing different nodes' caches.
			query := tx
			if allowCreate {
				query = query.Clauses(clause.Locking{Strength: "SHARE"})
			}
			if err := query.Where("key IN ?", keys).Find(&options).Error; err != nil {
				return err
			}
			values := map[string]string{"QuotaPerUnit": "500000", "USDExchangeRate": "7.3", "TopUpPlatformUnitsPerCNY": "1"}
			for _, option := range options {
				values[option.Key] = option.Value
			}
			// Current fiat FX and the retained price scale must still be usable
			// when the immutable anchor already exists. Malformed durable values
			// cannot silently inherit a cache's unrelated default on restart.
			for _, key := range []string{"QuotaPerUnit", "USDExchangeRate"} {
				if _, err := parsePositiveCreditRate(values[key]); err != nil {
					return fmt.Errorf("invalid %s for credit initialization: %w", key, err)
				}
			}
			if value, exists := values[CreditsPerUSDOptionKey]; exists {
				var err error
				anchor, err = parseCreditAnchor(value)
				if err != nil {
					return err
				}
			} else {
				if !allowCreate {
					return common.ErrCreditUnitsUnavailable
				}
				candidate := decimal.NewFromInt(1)
				for _, key := range []string{"QuotaPerUnit", "USDExchangeRate", "TopUpPlatformUnitsPerCNY"} {
					value, err := parsePositiveCreditRate(values[key])
					if err != nil {
						return fmt.Errorf("invalid %s for credit initialization: %w", key, err)
					}
					candidate = candidate.Mul(value)
				}
				if _, err := parseCreditAnchor(candidate.String()); err != nil {
					return err
				}
				if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&Option{Key: CreditsPerUSDOptionKey, Value: candidate.String()}).Error; err != nil {
					return err
				}
				var winner Option
				if err := tx.Where("key = ?", CreditsPerUSDOptionKey).First(&winner).Error; err != nil {
					return err
				}
				var err error
				anchor, err = parseCreditAnchor(winner.Value)
				if err != nil {
					return err
				}
			}
			if _, exists := values[LegacyPricingQuotaPerUnitOptionKey]; !exists {
				if !allowCreate {
					return common.ErrCreditUnitsUnavailable
				}
				if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&Option{Key: LegacyPricingQuotaPerUnitOptionKey, Value: values["QuotaPerUnit"]}).Error; err != nil {
					return err
				}
				var baseline Option
				if err := tx.Where("key = ?", LegacyPricingQuotaPerUnitOptionKey).First(&baseline).Error; err != nil {
					return err
				}
				values[LegacyPricingQuotaPerUnitOptionKey] = baseline.Value
			}
			if value, exists := values[PublicCreditsPerUSDOptionKey]; exists {
				publicConfigured = true
				public, err = parsePublicCreditRate(value)
				if err != nil {
					return err
				}
			} else if allowCreate {
				publicConfigured = true
				if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&Option{Key: PublicCreditsPerUSDOptionKey, Value: DefaultPublicCreditsPerUSD}).Error; err != nil {
					return err
				}
				var denomination Option
				if err := tx.Where("key = ?", PublicCreditsPerUSDOptionKey).First(&denomination).Error; err != nil {
					return err
				}
				public, err = parsePublicCreditRate(denomination.Value)
				if err != nil {
					return err
				}
			} else {
				// Verification of a pre-public-denomination database stays read-only.
				public = anchor
				publicConfigured = false
			}
			var err error
			legacy, err = parsePositiveCreditRate(values[LegacyPricingQuotaPerUnitOptionKey])
			if err != nil {
				return err
			}
			current, err := parsePositiveCreditRate(values["QuotaPerUnit"])
			if err != nil || !current.Equal(legacy) {
				return errors.New("legacy pricing calibration differs from immutable credit currency basis")
			}
			return nil
		})
		if err == nil {
			if err := common.SetCreditCurrencyBasis(anchor, legacy); err != nil {
				common.ClearCreditsPerUSD()
				return err
			}
			if publicConfigured {
				if err := common.SetPublicCreditsPerUSD(public); err != nil {
					common.ClearCreditsPerUSD()
					return err
				}
			} else {
				common.ClearPublicCreditsPerUSD()
			}
			common.OptionMapRWMutex.Lock()
			common.QuotaPerUnit = legacy.InexactFloat64()
			if common.OptionMap != nil {
				common.OptionMap["QuotaPerUnit"] = legacy.String()
				common.OptionMap[PublicCreditsPerUSDOptionKey] = public.String()
			}
			common.OptionMapRWMutex.Unlock()
			return nil
		}
		if !isCreditInitBusy(err) {
			break
		}
		timer := time.NewTimer(time.Duration(attempt+1) * 10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			err = ctx.Err()
			attempt = 6
		case <-timer.C:
		}
	}
	common.ClearCreditsPerUSD()
	return fmt.Errorf("initialize credit currency units: %w", err)
}

// lockCreditUnitOptionChanges shares the initialization calibration fence.
// A bulk write cannot slip a new pricing scale in after the anchor is created.
func lockCreditUnitOptionChanges(tx *gorm.DB, values map[string]string) error {
	value, changes := values["QuotaPerUnit"]
	if !changes {
		return nil
	}
	candidate, err := parsePositiveCreditRate(value)
	if err != nil {
		return err
	}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&Option{Key: "QuotaPerUnit", Value: "500000"}).Error; err != nil {
		return err
	}
	var calibration Option
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("key = ?", "QuotaPerUnit").First(&calibration).Error; err != nil {
		return err
	}
	var options []Option
	if err := tx.Where("key IN ?", []string{CreditsPerUSDOptionKey, LegacyPricingQuotaPerUnitOptionKey}).Find(&options).Error; err != nil {
		return err
	}
	var hasAnchor bool
	var baseline string
	for _, option := range options {
		if option.Key == CreditsPerUSDOptionKey {
			hasAnchor = true
		}
		if option.Key == LegacyPricingQuotaPerUnitOptionKey {
			baseline = option.Value
		}
	}
	if !hasAnchor {
		return nil
	}
	fixed, err := parsePositiveCreditRate(baseline)
	if err != nil {
		return errors.New("immutable legacy pricing calibration is unavailable; run migrate --apply")
	}
	if !candidate.Equal(fixed) {
		return errors.New("legacy QuotaPerUnit is immutable after credit currency initialization")
	}
	return nil
}

func parsePositiveCreditRate(value string) (decimal.Decimal, error) {
	// Reject pathological exponents/coefficients before decimal arithmetic.
	if len(value) == 0 || len(value) > 80 {
		return decimal.Zero, errors.New("invalid positive currency rate")
	}
	result, err := decimal.NewFromString(value)
	if err != nil || !result.IsPositive() || result.Exponent() < -18 || result.Exponent() > 18 {
		return decimal.Zero, errors.New("invalid positive currency rate")
	}
	return result, nil
}

func parseCreditAnchor(value string) (decimal.Decimal, error) {
	anchor, err := parsePositiveCreditRate(value)
	if err != nil {
		return decimal.Zero, err
	}
	return anchor, common.ValidateCreditsPerUSD(anchor)
}

func isCreditInitBusy(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "database is locked") || strings.Contains(message, "database table is locked") || strings.Contains(message, "sqlite_busy") ||
		strings.Contains(message, "deadlock") || strings.Contains(message, "serialization failure") ||
		strings.Contains(message, "sqlstate 40001") || strings.Contains(message, "sqlstate 40p01") || strings.Contains(message, "lock wait timeout")
}

func parsePublicCreditRate(value string) (decimal.Decimal, error) {
	result, err := parsePositiveCreditRate(value)
	if err != nil {
		return decimal.Zero, err
	}
	return result, common.ValidatePublicCreditsPerUSD(result)
}
