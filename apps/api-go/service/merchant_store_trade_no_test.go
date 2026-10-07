package service

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMerchantStoreTradeNoPatternAcceptsRandomAndLegacyNumbers(t *testing.T) {
	for _, trade := range []string{"MS" + strings.Repeat("a", 30), "MS" + strings.Repeat("b9", 15), "MS" + strings.Repeat("Az0", 10)} {
		require.True(t, merchantStoreTradeNoPattern.MatchString(trade), trade)
	}
	for _, trade := range []string{"MS" + strings.Repeat("a", 29), "MS" + strings.Repeat("a", 31), "MS" + strings.Repeat("a", 29) + "-", "MS" + strings.Repeat("a", 29) + "_", "MS" + strings.Repeat("a", 29) + "\n", "ms" + strings.Repeat("a", 30)} {
		require.False(t, merchantStoreTradeNoPattern.MatchString(trade), trade)
	}
}
