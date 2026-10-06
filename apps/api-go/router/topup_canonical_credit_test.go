package router

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCanonicalTopUpCurrencyRoutesAreDistinctFromLegacyRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiRouter(engine)
	routes := map[string]bool{}
	for _, route := range engine.Routes() {
		routes[route.Method+" "+route.Path] = true
	}
	for _, suffix := range []string{"amount", "pay", "stripe/amount", "stripe/pay", "waffo/amount", "waffo/pay", "waffo-pancake/amount", "waffo-pancake/pay", "discount-code/validate"} {
		require.True(t, routes[http.MethodPost+" /api/user/topup/currency/"+suffix], suffix)
		require.True(t, routes[http.MethodPost+" /api/user/topup/currency/v2/"+suffix], "public denomination route is distinct: "+suffix)
		require.True(t, routes[http.MethodPost+" /api/user/"+suffix], "legacy route remains: "+suffix)
		require.False(t, routes[http.MethodGet+" /api/user/topup/currency/"+suffix])
		require.False(t, routes[http.MethodGet+" /api/user/topup/currency/v2/"+suffix])
	}
}
