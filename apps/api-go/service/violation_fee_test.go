package service

import (
	"errors"
	"math"
	"net/http/httptest"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/gin-gonic/gin"
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

func TestUpstreamViolationMarkersNeverChargeTheLegacyWalletPath(t *testing.T) {
	db, userID := setupAssistantFundingTestDB(t, 1_000_000)
	require.NoError(t, db.AutoMigrate(&model.ViolationFeeRecord{}, &model.ViolationFeeState{}))
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set(common.RequestIdKey, "legacy-upstream-violation")
	info := &relaycommon.RelayInfo{UserId: userID, UserGroup: "default", UsingGroup: "default"}
	err := NormalizeViolationFeeError(types.NewErrorWithStatusCode(errors.New("Content violates usage guidelines"), types.ErrorCodeBadResponse, 400))
	require.False(t, ChargeViolationFeeIfNeeded(c, info, err))
	var user model.User
	require.NoError(t, db.First(&user, userID).Error)
	require.Equal(t, 1_000_000, user.Quota)
	var records int64
	require.NoError(t, db.Model(&model.ViolationFeeRecord{}).Count(&records).Error)
	require.Zero(t, records)
}
