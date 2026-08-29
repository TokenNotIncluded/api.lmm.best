package service

import (
	"math"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
)

func TestImageTokenQuotaSaturatesNonFiniteAndExtremeProducts(t *testing.T) {
	require.Equal(t, 3, imageTokenQuota(2, 1.62))
	require.Equal(t, common.MaxQuota, imageTokenQuota(math.MaxInt, 2))
	require.Equal(t, common.MaxQuota, imageTokenQuota(1536, math.Inf(1)))
	require.Equal(t, 0, imageTokenQuota(1536, math.NaN()))
}
