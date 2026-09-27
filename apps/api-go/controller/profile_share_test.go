package controller

import (
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestProfileShareSVGOptionsRejectInjectionAndRenderActualUsage(t *testing.T) {
	_, err := parseProfileShareSVGOptions(url.Values{"accent": {"red;url(https://evil.test)"}})
	require.Error(t, err)
	_, err = parseProfileShareSVGOptions(url.Values{"unknown": {"value"}})
	require.Error(t, err)
	_, err = parseProfileShareSVGOptions(url.Values{"title": {"hello\nworld"}})
	require.Error(t, err)

	options, err := parseProfileShareSVGOptions(url.Values{
		"title":     {`<script>alert("x")</script>`},
		"accent":    {"#fa7722"},
		"animation": {"wave"},
		"lang":      {"zh"},
	})
	require.NoError(t, err)
	svg := renderProfileShareSVG(options, model.ProfileShareUsage{Tokens: 83_400_000, Requests: 42})
	require.NotContains(t, svg, "<script>")
	require.Contains(t, svg, "&lt;script&gt;")
	require.Contains(t, svg, "8340万")
	require.Contains(t, svg, "api.lmm.best")
	require.Contains(t, svg, "prefers-reduced-motion")
	var parsed struct{}
	require.NoError(t, xml.Unmarshal([]byte(svg), &parsed))
}

func TestProfileSharePublicSVGRequiresOptInAndCanBeRevoked(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.ProfileShare{}, &model.QuotaData{}))
	user := model.User{Username: "svg-owner", Password: "password", Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&user).Error)
	require.NoError(t, db.Create(&model.QuotaData{UserID: user.Id, CreatedAt: 100, TokenUsed: 42, Count: 2}).Error)

	engine := gin.New()
	engine.Use(func(c *gin.Context) { c.Set("id", user.Id); c.Next() })
	engine.GET("/api/user/self/profile-share", GetSelfProfileShare)
	engine.POST("/api/user/self/profile-share", EnableSelfProfileShare)
	engine.DELETE("/api/user/self/profile-share", DisableSelfProfileShare)
	engine.GET("/api/share/profile/:token", GetPublicProfileShareSVG)

	request := func(method, path string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, httptest.NewRequest(method, path, nil))
		return response
	}
	require.Contains(t, request(http.MethodGet, "/api/user/self/profile-share").Body.String(), `"enabled":false`)
	share, err := model.EnableProfileShare(user.Id)
	require.NoError(t, err)
	path := "/api/share/profile/" + share.Token + ".svg?layout=badge&period=all&accent=%23fa7722"
	response := request(http.MethodGet, path)
	require.Equal(t, http.StatusOK, response.Code)
	require.Contains(t, response.Header().Get("Content-Type"), "image/svg+xml")
	require.Contains(t, response.Body.String(), ">42<")
	require.NotContains(t, response.Body.String(), "password")
	profile := request(http.MethodGet, "/api/share/profile/"+share.Token+".svg?lang=zh")
	require.Equal(t, http.StatusOK, profile.Code)
	require.Contains(t, profile.Body.String(), "Token 活动")
	require.Contains(t, profile.Body.String(), "https://api.lmm.best")
	require.Equal(t, http.StatusBadRequest, request(http.MethodGet, "/api/share/profile/"+share.Token+".svg?accent=red").Code)
	require.Equal(t, http.StatusNotFound, request(http.MethodGet, "/api/share/profile/"+strings.Repeat("0", 48)+".svg").Code)
	require.Equal(t, http.StatusOK, request(http.MethodDelete, "/api/user/self/profile-share").Code)
	require.Equal(t, http.StatusNotFound, request(http.MethodGet, path).Code)
}

func TestProfileShareOverviewSVGUsesRealYearDataAndEscapesCustomText(t *testing.T) {
	start := int64(100 * 86400)
	rows := []model.ProfileShareDay{
		{Day: start + 368*86400, Tokens: 7_700_000_000},
		{Day: start + 369*86400, Tokens: 640_000_000},
	}
	summary := buildProfileShareYearSummary(rows, start)
	require.EqualValues(t, 8_340_000_000, summary.Tokens)
	require.Equal(t, 2, summary.Current)
	require.Equal(t, 2, summary.Longest)
	require.Equal(t, 5, summary.Levels[368])

	options, err := parseProfileShareSVGOptions(url.Values{
		"lang": {"zh"}, "title": {`<script>alert(1)</script>`},
	})
	require.NoError(t, err)
	owner := &model.User{Username: "light", DisplayName: "lightjunction", RequestCount: 87_000}
	svg := renderProfileShareProfileSVG(options, owner, rows, start)
	require.Contains(t, svg, "83.4亿")
	require.Contains(t, svg, "8.7万")
	require.Contains(t, svg, "https://api.lmm.best")
	require.Contains(t, svg, "&lt;script&gt;")
	require.NotContains(t, svg, "<script>")
	var parsed struct{}
	require.NoError(t, xml.Unmarshal([]byte(svg), &parsed))
}

func TestProfileShareOverviewSkipsOnlyThePartialFirstMonth(t *testing.T) {
	start := time.Date(2025, time.September, 18, 0, 0, 0, 0, time.UTC).Unix()
	options, err := parseProfileShareSVGOptions(url.Values{"lang": {"zh"}})
	require.NoError(t, err)
	svg := renderProfileShareProfileSVG(options, &model.User{Username: "reader"}, nil, start)
	require.Equal(t, 1, strings.Count(svg, ">9月</text>"))
	for _, month := range []string{"10月", "11月", "12月", "1月", "2月", "3月", "4月", "5月", "6月", "7月", "8月"} {
		require.Contains(t, svg, ">"+month+"</text>")
	}
}
