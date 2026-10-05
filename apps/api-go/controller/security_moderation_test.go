package controller

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/dto"
	"github.com/LIghtJUNction/api.lmm.best/middleware"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupSecurityModerationDB(t *testing.T) *gorm.DB {
	t.Helper()
	if _, err := common.CreditsPerUSD(); err != nil {
		require.NoError(t, common.SetCreditsPerUSD(decimal.NewFromInt(500000)))
		t.Cleanup(common.ClearCreditsPerUSD)
	}
	oldGinMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldGinMode) })
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Channel{}, &model.Ability{}, &model.Option{}, &model.ModerationJob{}, &model.AdvancedSecurityEvent{}))
	oldDB, oldRedis := model.DB, common.RedisEnabled
	model.DB, common.RedisEnabled = db, false
	oldSettings := setting.GetModerationSettings()
	require.NoError(t, setting.UpdateModerationSettings(setting.DefaultModerationSettings().OptionValues()))
	t.Cleanup(func() {
		model.DB, common.RedisEnabled = oldDB, oldRedis
		require.NoError(t, setting.UpdateModerationSettings(oldSettings.OptionValues()))
		sqlDB, err := db.DB()
		require.NoError(t, err)
		require.NoError(t, sqlDB.Close())
	})
	return db
}

func TestSecurityModerationPolicyPublishesTrueUSDWithoutRewritingLegacy(t *testing.T) {
	previousAnchor, previousError := common.CreditsPerUSD()
	previousUnit := common.QuotaPerUnit
	t.Cleanup(func() {
		common.QuotaPerUnit = previousUnit
		if previousError != nil {
			common.ClearCreditsPerUSD()
		} else {
			require.NoError(t, common.SetCreditsPerUSD(previousAnchor))
		}
	})
	require.NoError(t, common.SetCreditsPerUSD(decimal.NewFromInt(500000)))
	common.QuotaPerUnit = 3000000
	settings := setting.DefaultModerationSettings()
	settings.GroupPolicies = map[string]setting.ModerationGroupPolicy{
		"historical": {Mode: "strict", CategoryFinesUSD: map[string]float64{"hate": .5}},
		"legacy":     {Mode: "strict", AmountCurrency: "legacy_pricing_unit", CategoryFinesUSD: map[string]float64{"hate": .5}},
		"fiat":       {Mode: "strict", AmountCurrency: "USD", CategoryFinesUSD: map[string]float64{"hate": .5}},
	}
	storedBefore := settings.OptionValues()
	policy := publicModerationPolicy(settings)
	for _, group := range []string{"historical", "legacy", "fiat"} {
		require.Equal(t, "USD", policy.GroupPolicies[group].AmountCurrency)
	}
	require.Equal(t, 3.0, policy.GroupPolicies["historical"].CategoryFinesUSD["hate"])
	require.Equal(t, 3.0, policy.GroupPolicies["legacy"].CategoryFinesUSD["hate"])
	require.Equal(t, .5, policy.GroupPolicies["fiat"].CategoryFinesUSD["hate"])
	require.Equal(t, storedBefore, settings.OptionValues())
	common.QuotaPerUnit = 0
	policy = publicModerationPolicy(settings)
	require.Nil(t, policy.GroupPolicies["historical"].CategoryFinesUSD, "invalid basis must never relabel legacy amounts as USD")
	require.Equal(t, .5, policy.GroupPolicies["fiat"].CategoryFinesUSD["hate"], "explicit USD is independent of the legacy scale")
	common.ClearCreditsPerUSD()
	policy = publicModerationPolicy(settings)
	require.Nil(t, policy.GroupPolicies["fiat"].CategoryFinesUSD, "uninitialized monetary state must not quote a charge")
}

func securityModerationRequest(t *testing.T, handler gin.HandlerFunc, query string, role, id int) *httptest.ResponseRecorder {
	t.Helper()
	response := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(response)
	ctx.Set("role", role)
	ctx.Set("id", id)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/security?"+query, nil)
	handler(ctx)
	require.Equal(t, http.StatusOK, response.Code)
	return response
}

func TestSecurityModerationCatalogUsesRealOfficialAbilitiesWithoutLoadingKeys(t *testing.T) {
	db := setupSecurityModerationDB(t)
	latest, fixed := setting.DefaultModerationModel, "omni-moderation-2024-09-26"
	group := "review"
	proxy := `{"proxy":"https://proxy.example"}`
	override := `{"model":"gpt-5"}`
	channels := []model.Channel{
		{Id: 1, Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, Models: latest + "," + fixed, Key: "fixture-only-never-read-key"},
		{Id: 2, Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, BaseURL: common.GetPointer("https://relay.example")},
		{Id: 3, Type: constant.ChannelTypeAzure, Status: common.ChannelStatusEnabled},
		{Id: 4, Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusManuallyDisabled},
		{Id: 5, Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled},
		{Id: 6, Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, Setting: &proxy},
		{Id: 7, Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, ParamOverride: &override},
		{Id: 8, Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, BaseURL: common.GetPointer("https://api.openai.com/v1")},
	}
	require.NoError(t, db.Create(&channels).Error)
	abilities := []model.Ability{
		{Group: group, Model: latest, ChannelId: 1, Enabled: true},
		{Group: group, Model: "gpt-5", ChannelId: 1, Enabled: true},
		{Group: group, Model: fixed, ChannelId: 2, Enabled: true},
		{Group: group, Model: fixed, ChannelId: 3, Enabled: true},
		{Group: group, Model: fixed, ChannelId: 4, Enabled: true},
		{Group: group, Model: fixed, ChannelId: 5, Enabled: false},
		{Group: group, Model: fixed, ChannelId: 6, Enabled: true},
		{Group: group, Model: fixed, ChannelId: 7, Enabled: true},
		{Group: "other", Model: fixed, ChannelId: 8, Enabled: true},
	}
	require.NoError(t, db.Create(&abilities).Error)
	queries := 0
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("test:moderation-catalog-no-key", func(tx *gorm.DB) {
		if tx.Statement.Table == "channels" {
			queries++
			selects := strings.ToLower(strings.Join(tx.Statement.Selects, " "))
			assert.NotEmpty(t, selects)
			assert.NotContains(t, selects, "channels.key")
			assert.NotContains(t, selects, "channel_info")
			assert.NotContains(t, selects, "*")
		}
	}))
	t.Cleanup(func() { require.NoError(t, db.Callback().Query().Remove("test:moderation-catalog-no-key")) })

	var response struct {
		Success bool `json:"success"`
		Data    struct {
			Group  string   `json:"group"`
			Models []string `json:"models"`
		} `json:"data"`
	}
	first := securityModerationRequest(t, GetAdminModerationModels, "group=review", common.RoleRootUser, 1)
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &response))
	require.True(t, response.Success)
	require.Equal(t, group, response.Data.Group)
	require.Equal(t, []string{latest}, response.Data.Models)
	require.Equal(t, 2, queries)
	assert.NotContains(t, first.Body.String(), "fixture-only-never-read-key")
	assert.NotContains(t, first.Body.String(), "channel")

	require.NoError(t, db.Create(&model.Ability{Group: group, Model: fixed, ChannelId: 8, Enabled: true}).Error)
	second := securityModerationRequest(t, GetAdminModerationModels, "group=review", common.RoleRootUser, 1)
	require.NoError(t, json.Unmarshal(second.Body.Bytes(), &response))
	require.True(t, response.Success)
	require.Equal(t, []string{latest, fixed}, response.Data.Models)

	empty := securityModerationRequest(t, GetAdminModerationModels, "group=missing", common.RoleRootUser, 1)
	require.NoError(t, json.Unmarshal(empty.Body.Bytes(), &response))
	require.True(t, response.Success)
	require.NotNil(t, response.Data.Models)
	require.Empty(t, response.Data.Models)
	for _, query := range []string{"", "group=%2A", "group=" + strings.Repeat("a", 65)} {
		invalid := securityModerationRequest(t, GetAdminModerationModels, query, common.RoleRootUser, 1)
		assert.Contains(t, invalid.Body.String(), `"success":false`)
	}
}

func seedSecurityModerationJob(t *testing.T, db *gorm.DB, id int64, userID int, group, source, status string, created int64) {
	t.Helper()
	job := model.ModerationJob{
		ID: id, EventKey: fmt.Sprintf("fixture-job-%d", id), UserID: userID,
		RequestID: fmt.Sprintf("request-%d", id), Group: group, Source: source,
		ReviewGroup: "private-review-route", ReviewModel: setting.DefaultModerationModel,
		Payload: "private submitted content that must never be returned", LeaseOwner: "private-lease-owner",
		CapturedMode: setting.ModerationModeStrict, CapturedCategoryFinesJSON: `{"hate":0.5}`,
		Status: status, CategoriesJSON: `["hate"]`, CategoryScoresJSON: `{"hate":0.99}`,
		ErrorMessage: "raw-provider-error-private", CreatedAt: created, UpdatedAt: created,
	}
	require.NoError(t, db.Create(&job).Error)
}

func TestSecurityModerationReviewsFilterBeforePaginationAndNeverReturnContent(t *testing.T) {
	db := setupSecurityModerationDB(t)
	seedSecurityModerationJob(t, db, 1, 7, "alpha", model.ModerationSourceRelayInput, model.ModerationJobCompleted, 100)
	seedSecurityModerationJob(t, db, 2, 7, "alpha", model.ModerationSourceRelayInput, model.ModerationJobCompleted, 110)
	seedSecurityModerationJob(t, db, 3, 7, "alpha", model.ModerationSourceAssistantOutput, model.ModerationJobCompleted, 110)
	seedSecurityModerationJob(t, db, 4, 7, "alpha", model.ModerationSourceRelayInput, model.ModerationJobFailed, 110)
	seedSecurityModerationJob(t, db, 5, 8, "beta", model.ModerationSourceRelayInput, model.ModerationJobCompleted, 110)
	var response struct {
		Success bool `json:"success"`
		Data    struct {
			Rows     []dto.SecurityModerationReview `json:"rows"`
			Total    int64                          `json:"total"`
			Page     int                            `json:"page"`
			PageSize int                            `json:"page_size"`
		} `json:"data"`
	}
	query := "group=alpha&status=completed&source=relay_input&start_timestamp=105&end_timestamp=115"
	result := securityModerationRequest(t, ListAdminModerationReviews, query, common.RoleRootUser, 9)
	require.NoError(t, json.Unmarshal(result.Body.Bytes(), &response))
	require.True(t, response.Success)
	require.EqualValues(t, 1, response.Data.Total)
	require.Len(t, response.Data.Rows, 1)
	assert.EqualValues(t, 2, response.Data.Rows[0].ID)
	assert.Equal(t, moderationSourceKind, response.Data.Rows[0].SourceKind)
	assert.Equal(t, []string{"hate"}, response.Data.Rows[0].Categories)
	for _, forbidden := range []string{"payload", "private submitted", "captured_category", "category_scores", "lease", "raw-provider", "private-review-route", "review_group", "input_digest"} {
		assert.NotContains(t, result.Body.String(), forbidden)
	}

	page := securityModerationRequest(t, ListAdminModerationReviews, "group=alpha&status=completed&source=relay_input&p=2&page_size=1", common.RoleRootUser, 9)
	require.NoError(t, json.Unmarshal(page.Body.Bytes(), &response))
	require.True(t, response.Success)
	assert.EqualValues(t, 2, response.Data.Total)
	assert.Equal(t, 2, response.Data.Page)
	assert.Equal(t, 1, response.Data.PageSize)
	require.Len(t, response.Data.Rows, 1)
	assert.EqualValues(t, 1, response.Data.Rows[0].ID)
}

func TestSecurityModerationReviewsRespectAdministratorHierarchy(t *testing.T) {
	db := setupSecurityModerationDB(t)
	users := []model.User{
		{Id: 101, Username: "common-fixture", AffCode: "moderation-common", Role: common.RoleCommonUser},
		{Id: 102, Username: "peer-fixture", AffCode: "moderation-peer", Role: common.RoleAdminUser},
		{Id: 103, Username: "root-fixture", AffCode: "moderation-root", Role: common.RoleRootUser},
	}
	require.NoError(t, db.Create(&users).Error)
	for _, user := range users {
		seedSecurityModerationJob(t, db, int64(user.Id), user.Id, "private-group", model.ModerationSourceRelayInput, model.ModerationJobCompleted, 100)
	}
	var response struct {
		Success bool `json:"success"`
		Data    struct {
			Rows []dto.SecurityModerationReview `json:"rows"`
		} `json:"data"`
	}
	result := securityModerationRequest(t, ListAdminModerationReviews, "", common.RoleAdminUser, 99)
	require.NoError(t, json.Unmarshal(result.Body.Bytes(), &response))
	require.True(t, response.Success)
	require.Len(t, response.Data.Rows, 3)
	for _, row := range response.Data.Rows {
		if row.ID == 101 {
			assert.Equal(t, 101, row.UserID)
			assert.Equal(t, "private-group", row.Group)
		} else {
			assert.Zero(t, row.UserID)
			assert.Empty(t, row.RequestID)
			assert.Empty(t, row.Group)
			assert.Zero(t, row.FeeRecordID)
		}
	}
}

func TestSecurityModerationReviewFilterRejectsInvalidBoundsAndSources(t *testing.T) {
	setupSecurityModerationDB(t)
	queries := []string{
		"p=-1", "p=x", "p=9223372036854775807&page_size=100", "page_size=-1",
		"status=unknown", "source=chatgpt", "start_timestamp=-1", "end_timestamp=x",
		"start_timestamp=20&end_timestamp=10", "group=%2A", "user_id=0",
	}
	for _, query := range queries {
		t.Run(query, func(t *testing.T) {
			result := securityModerationRequest(t, ListAdminModerationReviews, query, common.RoleRootUser, 1)
			assert.Contains(t, result.Body.String(), `"success":false`)
		})
	}
}

func TestSecurityModerationPublicPolicyUsesAuthoritativeOptionsAndRetiresLegacyEnforcement(t *testing.T) {
	db := setupSecurityModerationDB(t)
	values := setting.DefaultModerationSettings().OptionValues()
	values[setting.ModerationEnabledOptionKey] = "true"
	values[setting.AssistantModerationEnabledOptionKey] = "true"
	values[setting.ModerationGroupOptionKey] = "private-api-review-route"
	values[setting.AssistantModerationGroupOptionKey] = "private-assistant-review-route"
	values[setting.ModerationGroupPoliciesOptionKey] = `{"premium":{"mode":"strict","category_fines_usd":{"hate":0.5}},"default":{"mode":"tolerant"},"excluded":{"mode":"off"}}`
	for key, value := range values {
		require.NoError(t, db.Create(&model.Option{Key: key, Value: value}).Error)
	}
	assert.False(t, setting.GetModerationSettings().Enabled, "local cache deliberately differs from authoritative options")
	var response struct {
		Success bool                     `json:"success"`
		Data    dto.PublicSecurityPolicy `json:"data"`
	}
	result := securityModerationRequest(t, GetPublicSecurityPolicy, "", 0, 0)
	require.NoError(t, json.Unmarshal(result.Body.Bytes(), &response))
	require.True(t, response.Success)
	assert.True(t, response.Data.Moderation.Enabled)
	assert.True(t, response.Data.Moderation.AssistantEnabled)
	assert.True(t, response.Data.Moderation.Async)
	assert.True(t, response.Data.Moderation.NoticeOnly)
	assert.Equal(t, moderationSourceKind, response.Data.Moderation.Engine)
	assert.Equal(t, []string{"text"}, response.Data.Moderation.SupportedInputs)
	assert.Equal(t, 0.5, response.Data.Moderation.GroupPolicies["premium"].CategoryFinesUSD["hate"])
	assert.Equal(t, []string{"default", "premium"}, response.Data.ProtectedGroups)
	assert.False(t, response.Data.Enforcement.Enabled)
	assert.False(t, response.Data.Enforcement.OnPrompt)
	assert.True(t, response.Data.Enforcement.Retired)
	for _, fee := range response.Data.ViolationFees {
		assert.False(t, fee.Enabled)
		assert.True(t, fee.Historical)
	}
	for _, private := range []string{"private-api-review-route", "private-assistant-review-route", `"review_group":`, `"review_model":`, `"channel":`, `"key":`} {
		assert.NotContains(t, result.Body.String(), private)
	}
}

func TestSecurityModerationPublicDefaultsAreDisabledAndStatisticsAreAggregateOnly(t *testing.T) {
	db := setupSecurityModerationDB(t)
	policyResult := securityModerationRequest(t, GetPublicSecurityPolicy, "", 0, 0)
	var policy struct {
		Success bool                     `json:"success"`
		Data    dto.PublicSecurityPolicy `json:"data"`
	}
	require.NoError(t, json.Unmarshal(policyResult.Body.Bytes(), &policy))
	require.True(t, policy.Success)
	assert.False(t, policy.Data.Moderation.Enabled)
	assert.False(t, policy.Data.Moderation.AssistantEnabled)
	assert.NotNil(t, policy.Data.Moderation.GroupPolicies)
	assert.Empty(t, policy.Data.Moderation.GroupPolicies)
	assert.Empty(t, policy.Data.ProtectedGroups)
	seedSecurityModerationJob(t, db, 1, 7, "private-group", model.ModerationSourceRelayInput, model.ModerationJobCompleted, 1)
	seedSecurityModerationJob(t, db, 2, 8, "other-private-group", model.ModerationSourceAssistantOutput, model.ModerationJobFailed, 2)
	require.NoError(t, db.Model(&model.ModerationJob{}).Where("id = ?", 1).Updates(map[string]any{"flagged": true, "charged_quota": 100}).Error)
	result := securityModerationRequest(t, GetPublicSecurityStats, "", 0, 0)
	var stats struct {
		Success bool              `json:"success"`
		Data    dto.SecurityStats `json:"data"`
	}
	require.NoError(t, json.Unmarshal(result.Body.Bytes(), &stats))
	require.True(t, stats.Success)
	require.NotNil(t, stats.Data.Moderation)
	assert.EqualValues(t, 1, stats.Data.Moderation.Completed)
	assert.EqualValues(t, 1, stats.Data.Moderation.Failed)
	assert.EqualValues(t, 1, stats.Data.Moderation.Flagged)
	assert.EqualValues(t, 1, stats.Data.Moderation.Fined)
	assert.EqualValues(t, 100, stats.Data.Moderation.ChargedQuota)
	for _, private := range []string{"private-group", "user_id", "request_id", "payload", "lease", "review_group"} {
		assert.NotContains(t, result.Body.String(), private)
	}
}

func TestSecurityModerationEndpointsUseNormalAdminAuthentication(t *testing.T) {
	db := setupSecurityModerationDB(t)
	adminToken, userToken := "fixture-only-moderation-admin-pat", "fixture-only-moderation-user-pat"
	users := []model.User{
		{Id: 90101, Username: "moderation-admin-fixture", AffCode: "moderation-admin", AccessToken: &adminToken, Role: common.RoleAdminUser, Status: common.UserStatusEnabled, AuthVersion: 1},
		{Id: 90102, Username: "moderation-user-fixture", AffCode: "moderation-user", AccessToken: &userToken, Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AuthVersion: 1},
	}
	require.NoError(t, db.Create(&users).Error)
	router := gin.New()
	admin := router.Group("/api/security/admin", middleware.AdminAuth())
	admin.GET("/moderation/models", middleware.DisableCache(), GetAdminModerationModels)
	admin.GET("/moderation-reviews", middleware.DisableCache(), ListAdminModerationReviews)
	admin.GET("/moderation-stats", middleware.DisableCache(), GetAdminModerationStats)
	for _, path := range []string{"moderation/models?group=" + url.QueryEscape("default"), "moderation-reviews", "moderation-stats"} {
		for _, test := range []struct {
			name  string
			token string
			code  int
		}{{"anonymous", "", http.StatusUnauthorized}, {"ordinary", userToken, http.StatusForbidden}, {"administrator", adminToken, http.StatusOK}} {
			t.Run(path+"/"+test.name, func(t *testing.T) {
				request := httptest.NewRequest(http.MethodGet, "/api/security/admin/"+path, nil)
				if test.token != "" {
					request.Header.Set("Authorization", "Bearer "+test.token)
				}
				result := httptest.NewRecorder()
				router.ServeHTTP(result, request)
				assert.Equal(t, test.code, result.Code)
				if test.code == http.StatusOK {
					assert.Contains(t, result.Body.String(), `"success":true`)
					assert.Contains(t, result.Header().Get("Cache-Control"), "no-store")
				}
			})
		}
	}
}
