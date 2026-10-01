package controller

import (
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
	"testing"
)

func TestUserListOptionsRejectInvalidRangesAndFilters(t *testing.T) {
	for _, query := range []string{"risk_min=NaN", "risk_max=Inf", "risk_min=-0.1", "risk_max=1.1", "risk_min=0.9&risk_max=0.8", "transfers=unknown", "usage=unknown", "funding=unknown", "checkin=unknown"} {
		t.Run(query, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("GET", "/api/user/?"+query, nil)
			_, err := parseUserListOptions(c)
			require.Error(t, err)
		})
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/api/user/?risk_min=0&risk_max=1&transfers=sent&usage=zero&funding=unpaid&checkin=yes&sort_by=risk_score&sort_order=asc", nil)
	options, err := parseUserListOptions(c)
	require.NoError(t, err)
	require.Equal(t, 0.0, *options.Filters.RiskMin)
	require.Equal(t, 1.0, *options.Filters.RiskMax)
	require.Equal(t, "risk_score", options.SortBy)
	require.Equal(t, "sent", options.Filters.Transfers)
}
