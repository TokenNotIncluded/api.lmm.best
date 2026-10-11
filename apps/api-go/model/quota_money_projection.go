package model

import (
	"database/sql"
	"encoding/json"
	"math/big"
	"strconv"
	"strings"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// QuotaMoneyProjection describes a read-only display conversion. Quota remains
// the original stored integer; these fields are never persisted or settled.
// USD is a decimal string rounded once to 18 places, independently of live FX
// and pricing options. Absence is unknown, not zero.
type QuotaMoneyProjection struct {
	QuotaUSD            *string `json:"quota_usd,omitempty" gorm:"-"`
	QuotaMoneyAvailable bool    `json:"quota_money_available" gorm:"-"`
	QuotaMoneyBasis     string  `json:"quota_money_basis" gorm:"-"`
}

const (
	quotaMoneyMaxRecords       = 10_000
	quotaMoneyMaxBases         = 64
	quotaMoneyMaxMetadataBytes = 64 * 1024
	maxSafeCreditBasis         = int64(9_007_199_254_740_991)
)

// capturedCreditsPerUSD deliberately does not consult current configuration or
// infer a basis from log timestamps. Bound numeric parsing before allocating a
// decimal: opaque/malformed metadata must not make a history read expensive.
func capturedCreditsPerUSD(other string) (int64, bool) {
	if len(other) > quotaMoneyMaxMetadataBytes {
		return 0, false
	}
	var metadata map[string]json.RawMessage
	if json.Unmarshal([]byte(other), &metadata) != nil {
		return 0, false
	}
	raw := strings.TrimSpace(string(metadata["pricing_credits_per_usd"]))
	if len(raw) == 0 || len(raw) > 64 || raw[0] < '0' || raw[0] > '9' {
		return 0, false
	}
	if index := strings.IndexAny(raw, "eE"); index >= 0 {
		exponent, err := strconv.Atoi(raw[index+1:])
		if err != nil || exponent < -18 || exponent > 18 {
			return 0, false
		}
	}
	value, err := decimal.NewFromString(raw)
	if err != nil || !value.IsPositive() || value.GreaterThan(decimal.NewFromInt(maxSafeCreditBasis)) || !value.Equal(value.Truncate(0)) {
		return 0, false
	}
	return value.IntPart(), true
}

func unknownQuotaMoney(basis string) QuotaMoneyProjection {
	return QuotaMoneyProjection{QuotaMoneyBasis: basis}
}

func quotaMoneyFromRat(value *big.Rat, basis string) QuotaMoneyProjection {
	usd := strings.TrimRight(strings.TrimRight(value.FloatString(18), "0"), ".")
	return QuotaMoneyProjection{QuotaUSD: &usd, QuotaMoneyAvailable: true, QuotaMoneyBasis: basis}
}

// ProjectLogQuotaMoney accepts only consumption logs with a captured numeric
// credits-per-USD anchor. Payment/refund log quotas have different semantics.
func ProjectLogQuotaMoney(quota int, logType int, other string) QuotaMoneyProjection {
	if logType != LogTypeConsume {
		return unknownQuotaMoney("not_consumption")
	}
	basis, ok := capturedCreditsPerUSD(other)
	if !ok || quota < 0 {
		return unknownQuotaMoney("missing_credit_basis")
	}
	return quotaMoneyFromRat(new(big.Rat).SetFrac(big.NewInt(int64(quota)), big.NewInt(basis)), "captured_credits_per_usd")
}

func projectReadLogQuota(log *Log) {
	if !log.QuotaStoredValid {
		log.QuotaMoneyProjection = unknownQuotaMoney("invalid_stored_quota")
		return
	}
	log.QuotaMoneyProjection = ProjectLogQuotaMoney(log.Quota, log.Type, log.Other)
}

// Read projection inputs using exactly the raw statistic's ownership and
// request filters. A hard row/anchor bound prevents full-history JSON scans.
// A partial subtotal is never presented as the complete monetary statistic.
func projectQuotaStatistic(query *gorm.DB) (QuotaMoneyProjection, int, bool, error) {
	rows, err := query.Session(&gorm.Session{}).Select("quota", "other").Limit(quotaMoneyMaxRecords + 1).Rows()
	if err != nil {
		return unknownQuotaMoney("aggregate_missing_credit_basis"), 0, false, err
	}
	defer rows.Close()
	sums := make(map[int64]*big.Int)
	count, unknown := 0, 0
	for rows.Next() {
		count++
		if count > quotaMoneyMaxRecords {
			return unknownQuotaMoney("projection_limit"), unknown, true, nil
		}
		var quota sql.NullInt64
		var other sql.NullString
		if err := rows.Scan(&quota, &other); err != nil {
			return unknownQuotaMoney("aggregate_missing_credit_basis"), unknown, false, err
		}
		basis, ok := capturedCreditsPerUSD(other.String)
		if !ok || !quota.Valid || quota.Int64 < 0 {
			unknown++
			continue
		}
		if sums[basis] == nil {
			if len(sums) >= quotaMoneyMaxBases {
				return unknownQuotaMoney("projection_limit"), unknown, true, nil
			}
			sums[basis] = new(big.Int)
		}
		sums[basis].Add(sums[basis], big.NewInt(quota.Int64))
	}
	if err := rows.Err(); err != nil {
		return unknownQuotaMoney("aggregate_missing_credit_basis"), unknown, false, err
	}
	if unknown != 0 {
		return unknownQuotaMoney("aggregate_missing_credit_basis"), unknown, false, nil
	}
	total := new(big.Rat)
	for basis, quota := range sums {
		total.Add(total, new(big.Rat).SetFrac(quota, big.NewInt(basis)))
	}
	label := "captured_credits_per_usd"
	if count == 0 {
		label = "empty_result"
	}
	return quotaMoneyFromRat(total, label), 0, false, nil
}
