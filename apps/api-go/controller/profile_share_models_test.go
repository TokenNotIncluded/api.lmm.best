package controller

import (
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestProfileShareModelsOptions(t *testing.T) {
	for _, query := range []url.Values{
		{"layout": {"models"}, "period": {"all"}},
		{"layout": {"models"}, "top": {"13"}},
		{"layout": {"models"}, "top": {"0"}},
		{"layout": {"models"}, "top": {"3.5"}},
		{"layout": {"models"}, "period": {"invalid"}},
		{"layout": {"models"}, "accent": {"url(javascript:alert(1))"}},
		{"layout": {"models"}, "anything": {"unknown"}},
	} {
		_, _, err := parseProfileShareModelsSVGOptions(query)
		require.Error(t, err, "%v", query)
	}
	for _, period := range []string{"7d", "30d", "365d"} {
		query := url.Values{"layout": {"models"}, "period": {period}, "top": {"3"}, "theme": {"transparent"}, "font": {"mono"}, "requests": {"0"}}
		options, top, err := parseProfileShareModelsSVGOptions(query)
		require.NoError(t, err)
		require.Equal(t, 3, top)
		require.Equal(t, "models", options.Layout)
		require.Equal(t, "models", query.Get("layout"), "parser must not mutate caller options")
		require.Equal(t, "none", options.Background)
		require.False(t, options.ShowRequests)
	}
	options, top, err := parseProfileShareModelsSVGOptions(url.Values{"layout": {"models"}})
	require.NoError(t, err)
	require.Equal(t, "30d", options.Period)
	require.Equal(t, 6, top)
}

func TestProfileShareModelsRender(t *testing.T) {
	usage := model.ProfileShareModelUsage{Tokens: 1000, Requests: 25, Quota: 100, ModelCount: 4,
		Models: []model.ProfileShareModelRow{
			{ModelName: "a<script>\x00", Tokens: 400, Requests: 10, Quota: 40},
			{ModelName: "b", Tokens: 300, Requests: 8, Quota: 30},
			{ModelName: "unknown", Tokens: 200, Requests: 5, Quota: 20},
		},
	}
	for _, language := range []string{"en", "zh", "zh-TW", "fr", "ru", "ja", "vi"} {
		options, _, err := parseProfileShareModelsSVGOptions(url.Values{"layout": {"models"}, "lang": {language}, "title": {"<svg onload=alert(1)>"}, "format": {"full"}})
		require.NoError(t, err)
		svg := renderProfileShareModelsSVG(options, usage, 1, 86400)
		require.Contains(t, svg, "&lt;svg")
		require.NotContains(t, svg, "<script>")
		require.NotContains(t, svg, "\x00")
		require.Contains(t, svg, "40.0%")
		require.Contains(t, svg, "10.0%")
		require.Contains(t, svg, ">"+profileShareModelText(profileShareFormatNumber(usage.Tokens, options.Format, language))+"</text>")
		require.Contains(t, svg, profileShareModelLanguages[language].Other)
		require.Contains(t, svg, "prefers-reduced-motion")
		var parsed struct{}
		require.NoError(t, xml.Unmarshal([]byte(svg), &parsed))
	}
	options, _, err := parseProfileShareModelsSVGOptions(url.Values{"layout": {"models"}, "animation": {"none"}, "requests": {"0"}})
	require.NoError(t, err)
	svg := renderProfileShareModelsSVG(options, usage, 1, 86400)
	require.NotContains(t, svg, "API requests")
	require.NotContains(t, svg, "<style>")
	for index := range usage.Models {
		usage.Models[index].Quota = 0
	}
	usage.Quota = 0
	require.Contains(t, renderProfileShareModelsSVG(options, usage, 1, 86400), "Tokens by model")
	for index := range usage.Models {
		usage.Models[index].Tokens = 0
	}
	usage.Tokens = 0
	require.Contains(t, renderProfileShareModelsSVG(options, usage, 1, 86400), "Requests by model")
	empty := renderProfileShareModelsSVG(options, model.ProfileShareModelUsage{}, 1, 86400)
	require.Contains(t, empty, "No usage in this range yet")
	require.NotContains(t, empty, "NaN")
}

func TestProfileShareModelsConsentAndIsolation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.ProfileShare{}, &model.QuotaData{}))
	user := model.User{Username: "models-owner", Password: "password", Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&user).Error)
	now := time.Now().Unix()
	for _, row := range []model.QuotaData{
		{UserID: user.Id, ModelName: "owned-model", CreatedAt: now - 10, TokenUsed: 42, Count: 2, Quota: 20},
		{UserID: user.Id + 1, ModelName: "private-other-user", CreatedAt: now - 10, TokenUsed: 999, Count: 2},
		{UserID: user.Id, ModelName: "too-old", CreatedAt: now - 366*86400, TokenUsed: 999, Count: 2},
		{UserID: user.Id, ModelName: "future-record", CreatedAt: now + 86400, TokenUsed: 999, Count: 2},
	} {
		require.NoError(t, db.Create(&row).Error)
	}
	engine := gin.New()
	engine.Use(func(c *gin.Context) { c.Set("id", user.Id); c.Next() })
	engine.POST("/api/user/self/profile-share", EnableSelfProfileShare)
	engine.DELETE("/api/user/self/profile-share", DisableSelfProfileShare)
	engine.GET("/api/share/profile/:token", GetPublicProfileShareSVG)
	request := func(method, path, body string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(response, req)
		return response
	}
	self := "/api/user/self/profile-share"
	// Legacy body-less enable does not silently reveal model names or spend.
	require.Equal(t, http.StatusOK, request(http.MethodPost, self, "").Code)
	share, err := model.GetProfileShare(user.Id)
	require.NoError(t, err)
	require.False(t, share.ModelUsageEnabled)
	path := "/api/share/profile/" + share.Token + ".svg?layout=models&period=365d"
	require.Equal(t, http.StatusNotFound, request(http.MethodGet, path, "").Code)
	for _, body := range []string{`{"model_usage_enabled":"yes"}`, `{"model_usage_enabled":true} {}`, `{"unexpected":true}`} {
		require.Equal(t, http.StatusBadRequest, request(http.MethodPost, self, body).Code)
	}
	enabled := request(http.MethodPost, self, `{"model_usage_enabled":true}`)
	require.Equal(t, http.StatusOK, enabled.Code)
	require.Contains(t, enabled.Body.String(), `"model_usage_enabled":true`)
	response := request(http.MethodGet, path, "")
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	require.Contains(t, response.Header().Get("Content-Type"), "image/svg+xml")
	require.Contains(t, response.Body.String(), "owned-model")
	for _, secret := range []string{"private-other-user", "too-old", "future-record", "password"} {
		require.NotContains(t, response.Body.String(), secret)
	}
	require.Equal(t, http.StatusOK, request(http.MethodPost, self, `{"model_usage_enabled":false}`).Code)
	require.Equal(t, http.StatusNotFound, request(http.MethodGet, path, "").Code)
	require.Equal(t, http.StatusOK, request(http.MethodGet, strings.Replace(path, "layout=models", "layout=badge", 1), "").Code)
	require.Equal(t, http.StatusOK, request(http.MethodDelete, self, "").Code)
	require.Equal(t, http.StatusNotFound, request(http.MethodGet, path, "").Code)
}

// Optional artifacts are rendered by the production renderer, not hand-drawn
// screenshots. CI can inspect them directly in browsers without user data.
func TestProfileShareModelsVisualFixtures(t *testing.T) {
	dir := os.Getenv("PROFILE_SHARE_REVIEW_OUTPUT")
	if dir == "" {
		t.Skip("visual artifact output not requested")
	}
	require.NoError(t, os.MkdirAll(dir, 0755))
	usage := model.ProfileShareModelUsage{Tokens: 907626820, Requests: 14654, Quota: 290180750, ModelCount: 30,
		Models: []model.ProfileShareModelRow{
			{ModelName: "model-a", Tokens: 4572755, Requests: 3102, Quota: 94276950},
			{ModelName: "model-b", Tokens: 714051481, Requests: 8337, Quota: 91794400},
			{ModelName: "model-c", Tokens: 100000000, Requests: 1900, Quota: 36500000},
			{ModelName: "model-d", Tokens: 45000000, Requests: 500, Quota: 28000000},
			{ModelName: "model-e", Tokens: 24000000, Requests: 300, Quota: 15000000},
			{ModelName: "model-f", Tokens: 15000000, Requests: 200, Quota: 10000000},
		},
	}
	for _, theme := range []string{"dark", "paper", "transparent"} {
		options, _, err := parseProfileShareModelsSVGOptions(url.Values{"layout": {"models"}, "theme": {theme}, "lang": {"zh"}, "animation": {"none"}})
		require.NoError(t, err)
		svg := renderProfileShareModelsSVG(options, usage, 1787932800, 1790524800)
		require.NoError(t, os.WriteFile(filepath.Join(dir, "models-"+theme+".svg"), []byte(svg), 0644))
	}
}
