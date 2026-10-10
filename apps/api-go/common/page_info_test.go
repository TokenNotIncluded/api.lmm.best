package common

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func pageInfoForQuery(t *testing.T, query string) *PageInfo {
	t.Helper()
	gin.SetMode(gin.TestMode)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	request := httptest.NewRequest("GET", "/users?"+query, nil)
	context.Request = request
	return GetPageQuery(context)
}

func TestGetPageQueryRejectsNonPositivePageSizeAliases(t *testing.T) {
	tests := []struct {
		name  string
		query string
	}{
		{name: "page_size negative", query: "page_size=-1"},
		{name: "ps negative", query: "page_size=0&ps=-1"},
		{name: "size negative", query: "page_size=0&ps=0&size=-1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			page := pageInfoForQuery(t, test.query)
			require.Equal(t, ItemsPerPage, page.PageSize)
			require.Greater(t, page.PageSize, 0)
		})
	}
}

func TestGetPageQueryCapsPositivePageSize(t *testing.T) {
	page := pageInfoForQuery(t, "page_size=1000")
	require.Equal(t, 100, page.PageSize)
}

func TestGetPageQueryEndpointCapDoesNotChangeDefault(t *testing.T) {
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest("GET", "/api/log/token?page_size=1001", nil)
	require.Equal(t, 1000, GetPageQuery(context, 1000).PageSize)
	require.Equal(t, 100, GetPageQuery(context).PageSize)
	require.Equal(t, 1, GetPageQuery(context, 0).PageSize)
	require.Equal(t, 1, GetPageQuery(context, -1).PageSize)
}

func TestGetPageQueryDoesNotAcceptAtoiRangeErrorValues(t *testing.T) {
	for _, query := range []string{
		"p=92233720368547758080",
		"ps=92233720368547758080",
		"size=92233720368547758080",
	} {
		page := pageInfoForQuery(t, query)
		require.Equal(t, 1, page.Page)
		require.Equal(t, ItemsPerPage, page.PageSize)
	}
}
