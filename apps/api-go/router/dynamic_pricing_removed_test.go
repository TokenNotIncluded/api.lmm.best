package router

import (
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func TestDynamicPricingRoutesAreRemoved(t *testing.T) {
	engine := gin.New()
	SetApiRouter(engine)
	for _, route := range engine.Routes() {
		require.False(t, strings.HasPrefix(route.Path, "/api/dynamic_pricing"), route.Path)
	}
}
