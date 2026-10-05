package controller

import (
	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"testing"
)

func pricingUSDTestAnchor(t *testing.T) {
	t.Helper()
	old, e := common.CreditsPerUSD()
	require.NoError(t, common.SetCreditsPerUSD(decimal.NewFromInt(500000)))
	t.Cleanup(func() {
		if e != nil {
			common.ClearCreditsPerUSD()
		} else {
			require.NoError(t, common.SetCreditsPerUSD(old))
		}
	})
}
