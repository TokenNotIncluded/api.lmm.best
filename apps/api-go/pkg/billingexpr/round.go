package billingexpr

import "github.com/LIghtJUNction/api.lmm.best/common"

// QuotaRound converts a float64 quota value to int using half-away-from-zero
// rounding with int32 saturation for legacy projections. Consumption and
// reservations use common.ChargeQuotaFrom* so positive fractions are retained.
//
// It delegates to common.QuotaRound so all quota rounding/conversion shares
// one saturation + logging policy (see common/quota_math.go).
func QuotaRound(f float64) int {
	return common.QuotaRound(f)
}

// QuotaRoundStrict rejects an unrepresentable pre-consume estimate.
func QuotaRoundStrict(f float64) (int, error) {
	return common.QuotaRoundStrict(f)
}
