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
	var anchor decimal.Decimal
	var err error
	for attempt := 0; attempt < 6; attempt++ {
		err = DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var options []Option
			keys := []string{CreditsPerUSDOptionKey, "QuotaPerUnit", "USDExchangeRate", "TopUpPlatformUnitsPerCNY"}
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
				return err
			}
			if !allowCreate {
				return common.ErrCreditUnitsUnavailable
			}
			candidate := decimal.NewFromInt(1)
			for _, key := range keys[1:] {
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
			return err
		})
		if err == nil {
			return common.SetCreditsPerUSD(anchor)
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
