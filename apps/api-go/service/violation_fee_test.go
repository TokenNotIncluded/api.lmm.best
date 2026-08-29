package service

import (
	"errors"
	"math"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/stretchr/testify/require"
)

func TestCalcViolationFeeQuotaSaturatesExtremeAndNonFiniteValues(t *testing.T) {
	originalQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 500_000
	t.Cleanup(func() { common.QuotaPerUnit = originalQuotaPerUnit })

	require.Equal(t, 750_000, calcViolationFeeQuota(1, 1.5))
	require.Equal(t, common.MaxQuota, calcViolationFeeQuota(math.MaxFloat64, 1))
	require.Equal(t, common.MaxQuota, calcViolationFeeQuota(1, math.Inf(1)))
	require.Zero(t, calcViolationFeeQuota(math.NaN(), 1))
	require.Zero(t, calcViolationFeeQuota(1, math.NaN()))
	require.Zero(t, calcViolationFeeQuota(math.Inf(-1), 1))
}

func TestNormalizeViolationFeeErrorIsProviderAgnostic(t *testing.T) {
	err := types.NewErrorWithStatusCode(errors.New("Content violates usage guidelines"), types.ErrorCodeBadResponse, 400)
	normalized := NormalizeViolationFeeError(err)
	require.Equal(t, types.ErrorCodeViolationFeeUsagePolicy, normalized.GetErrorCode())
	require.True(t, IsViolationFeeCode(normalized.GetErrorCode()))
}
