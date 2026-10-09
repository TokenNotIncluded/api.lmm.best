package router

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// This fixture uses the real PostgreSQL schema gate, real session issuance,
// and the production router/middleware. No context identity is manufactured.
func nativeAccountHTTPFixture(t *testing.T) (*gorm.DB, *gin.Engine, []*service.AuthBundle) {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("TEST_POSTGRES_DSN"))
	if dsn == "" || os.Getenv("TEST_POSTGRES_ISOLATED_SCHEMA") != "1" {
		t.Skip("requires a dedicated TEST_POSTGRES_DSN and TEST_POSTGRES_ISOLATED_SCHEMA=1")
	}
	base, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	baseSQL, err := base.DB()
	require.NoError(t, err)
	schema := fmt.Sprintf("lmm_native_http_%d_%d", os.Getpid(), time.Now().UnixNano())
	quoted := pgx.Identifier{schema}.Sanitize()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	require.NoError(t, base.WithContext(ctx).Exec("CREATE SCHEMA "+quoted).Error)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		require.NoError(t, base.WithContext(ctx).Exec("DROP SCHEMA "+quoted+" CASCADE").Error)
		_ = baseSQL.Close()
	})
	parsed, err := url.Parse(dsn)
	if err == nil && (parsed.Scheme == "postgres" || parsed.Scheme == "postgresql") {
		query := parsed.Query()
		query.Set("search_path", schema)
		parsed.RawQuery = query.Encode()
		dsn = parsed.String()
	} else {
		dsn += " search_path=" + schema
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.UserSession{}, &model.NativeAccountRecord{}, &model.NativeAccountMember{}, &model.NativeAccountInvitation{}, &model.NativeAccountEvent{}))
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousType, previousRedis := common.MainDatabaseType(), common.RedisEnabled
	previousSecret, previousSecure := common.SessionSecret, common.SessionCookieSecure
	previousAPI, previousCritical := common.GlobalApiRateLimitEnable, common.CriticalRateLimitEnable
	previousOrigins := common.SessionCookieTrustedURLs
	model.DB, model.LOG_DB = db, db
	common.SetMainDatabaseType(common.DatabaseTypePostgreSQL)
	common.RedisEnabled, common.GlobalApiRateLimitEnable, common.CriticalRateLimitEnable = false, false, false
	common.SessionSecret, common.SessionCookieSecure = "isolated-native-http-test-secret", true
	common.SessionCookieTrustedURLs = nil
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.SetMainDatabaseType(previousType)
		common.RedisEnabled, common.GlobalApiRateLimitEnable, common.CriticalRateLimitEnable = previousRedis, previousAPI, previousCritical
		common.SessionSecret, common.SessionCookieSecure = previousSecret, previousSecure
		common.SessionCookieTrustedURLs = previousOrigins
	})
	logins := make([]*service.AuthBundle, 4)
	for i := range logins {
		id := i + 1
		role := common.RoleCommonUser
		if id == 4 {
			role = common.RoleRootUser
		}
		pat := fmt.Sprintf("native-http-pat-%d", id)
		u := model.User{Id: id, Username: fmt.Sprintf("native-http-%d", id), AffCode: fmt.Sprintf("native-http-%d", id), Status: common.UserStatusEnabled, Role: role, AuthVersion: 1, Group: "default", Quota: 12345, AccessToken: &pat}
		require.NoError(t, db.Create(&u).Error)
		logins[i], err = service.CreateLoginSession(id, "password", "192.0.2.1", "native-http-test")
		require.NoError(t, err)
	}
	t.Setenv("NATIVE_ACCOUNTS_ENABLED", "true")
	engine := gin.New()
	require.NoError(t, SetNativeAccountRouter(engine))
	require.Len(t, engine.Routes(), 12)
	return db, engine, logins
}

func nativeAccountHTTPRequest(engine *gin.Engine, token, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "https://accounts.example.test"+path, strings.NewReader(body))
	req.Header.Set("Origin", "https://accounts.example.test")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-LMM-Credit-Unit", strconv.FormatInt(common.FixedCreditsPerUSD, 10))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	return w
}

func nativeAccountHTTPData[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Contains(t, w.Header().Get("Cache-Control"), "no-store")
	var response struct {
		Success bool `json:"success"`
		Data    T    `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	require.True(t, response.Success)
	return response.Data
}

func TestNativeAccountHTTPRealSessionLifecyclePostgres(t *testing.T) {
	db, engine, logins := nativeAccountHTTPFixture(t)
	request := func(user int, method, path, body string) *httptest.ResponseRecorder {
		return nativeAccountHTTPRequest(engine, logins[user-1].AccessToken, method, path, body)
	}
	team := nativeAccountHTTPData[model.NativeAccountSummary](t, request(1, "POST", "/api/accounts/teams", `{"display_name":"研发团队","request_key":"native-http-create-001"}`))
	require.False(t, team.TeamBillingAvailable)
	path := fmt.Sprintf("/api/accounts/teams/%d", team.Account.ID)
	admin := nativeAccountHTTPData[model.NativeAccountInvitation](t, request(1, "POST", path+"/invitations", `{"user_id":2,"role":"admin","request_key":"native-http-admin-001"}`))
	nativeAccountHTTPData[model.NativeAccountSummary](t, request(2, "POST", "/api/accounts/invitations/"+admin.ID+"/accept", ""))
	invite := nativeAccountHTTPData[model.NativeAccountInvitation](t, request(2, "POST", path+"/invitations", `{"user_id":3,"role":"member","request_key":"native-http-member-001"}`))
	inbox := nativeAccountHTTPData[[]model.NativeAccountInvitationView](t, request(3, "GET", "/api/accounts/invitations", ""))
	require.Len(t, inbox, 1)
	require.Equal(t, "研发团队", inbox[0].TeamDisplayName)
	sent := nativeAccountHTTPData[[]model.NativeAccountInvitationView](t, request(2, "GET", path+"/invitations", ""))
	require.Len(t, sent, 1)
	require.Equal(t, invite.ID, sent[0].ID)
	require.Equal(t, 403, request(3, "GET", path+"/invitations", "").Code)
	require.Equal(t, 403, request(4, "GET", path, "").Code, "platform root is not a member")
	nativeAccountHTTPData[any](t, request(3, "POST", "/api/accounts/invitations/"+invite.ID+"/decline", ""))
	nativeAccountHTTPData[any](t, request(3, "POST", "/api/accounts/invitations/"+invite.ID+"/decline", ""))
	require.Equal(t, 409, request(3, "POST", "/api/accounts/invitations/"+invite.ID+"/accept", "").Code)
	invite = nativeAccountHTTPData[model.NativeAccountInvitation](t, request(2, "POST", path+"/invitations", `{"user_id":3,"role":"member","request_key":"native-http-member-002"}`))
	nativeAccountHTTPData[model.NativeAccountSummary](t, request(3, "POST", "/api/accounts/invitations/"+invite.ID+"/accept", ""))
	require.Equal(t, 403, request(3, "PATCH", path+"/members/3", `{"role":"admin"}`).Code)
	nativeAccountHTTPData[any](t, request(3, "DELETE", path+"/members/3", ""))
	require.Equal(t, 409, request(3, "POST", "/api/accounts/invitations/"+invite.ID+"/accept", "").Code)
	var users []model.User
	require.NoError(t, db.Order("id").Find(&users).Error)
	for _, user := range users {
		require.Zero(t, user.ConsoleActivatedAt, "team identity never activates the personal account")
		require.Equal(t, 12345, user.Quota)
	}
}

func TestNativeAccountHTTPAuthenticationAndRevocationPostgres(t *testing.T) {
	db, engine, logins := nativeAccountHTTPFixture(t)
	token := logins[0].AccessToken
	require.Equal(t, 401, nativeAccountHTTPRequest(engine, "", "GET", "/api/accounts", "").Code)
	require.Equal(t, 401, nativeAccountHTTPRequest(engine, "native-http-pat-1", "GET", "/api/accounts", "").Code)
	require.Equal(t, 400, nativeAccountHTTPRequest(engine, token, "POST", "/api/accounts/teams", `{"display_name":"Team","owner_user_id":4}`).Code)
	req := httptest.NewRequest("GET", "https://accounts.example.test/api/accounts", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Origin", "https://other.example.test")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	require.Equal(t, 403, w.Code)
	require.Contains(t, w.Body.String(), "AUTH_ORIGIN_FORBIDDEN")
	req = httptest.NewRequest("GET", "https://accounts.example.test/api/accounts", nil)
	req.Header.Set("Origin", "https://accounts.example.test")
	req.AddCookie(&http.Cookie{Name: service.RefreshCookieName, Value: logins[0].RefreshToken})
	w = httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	require.Equal(t, 401, w.Code, "a refresh cookie alone cannot authorize the account API")
	nativeAccountHTTPData[model.NativeAccountList](t, nativeAccountHTTPRequest(engine, token, "GET", "/api/accounts", ""))
	require.NoError(t, db.Model(&model.UserSession{}).Where("sid = ?", logins[0].Session.SID).Update("status", model.UserSessionStatusRevoked).Error)
	w = nativeAccountHTTPRequest(engine, token, "GET", "/api/accounts", "")
	require.True(t, w.Code == 401 || w.Code == 403, w.Body.String())
	require.NotContains(t, w.Body.String(), token)
}
