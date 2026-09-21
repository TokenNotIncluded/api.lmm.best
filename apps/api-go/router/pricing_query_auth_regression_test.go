package router

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupPricingQueryAuthRegression(t *testing.T) (*gorm.DB, model.User, *gin.Engine) {
	t.Helper()
	previousDB, previousLogDB := model.DB, model.LOG_DB
	t.Cleanup(func() { model.DB, model.LOG_DB = previousDB, previousLogDB })
	setupRelayRouterTestDB(t)
	levelOne := model.TrustLevelMinUser + 1
	user := model.User{
		Username: "pricing-auth-regression", Status: common.UserStatusEnabled,
		Group: "default", Quota: 100, TrustLevelOverride: &levelOne, ConsoleActivatedAt: 1,
	}
	require.NoError(t, model.DB.Create(&user).Error)
	engine := gin.New()
	SetRelayRouter(engine)
	return model.DB, user, engine
}

func pricingQueryAuthRequest(engine *gin.Engine, key string) *httptest.ResponseRecorder {
	// After successful authentication the real controller rejects this query
	// with 400, avoiding dependence on the process-wide model pricing cache.
	request := httptest.NewRequest(http.MethodGet, "/v1/pricing?model="+strings.Repeat("x", 513), nil)
	request.Header.Set("Authorization", "Bearer sk-"+key)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	return response
}

func TestPricingQueryAuthPreservesTokenState(t *testing.T) {
	for _, tc := range []struct {
		name              string
		status, remaining int
		expired           int64
		wantStatus        int
	}{
		{"enabled without quota", common.TokenStatusEnabled, 0, -1, http.StatusUnauthorized},
		{"enabled but expired", common.TokenStatusEnabled, 100, time.Now().Unix() - 60, http.StatusUnauthorized},
		{"already exhausted", common.TokenStatusExhausted, 0, -1, http.StatusUnauthorized},
		{"enabled with quota", common.TokenStatusEnabled, 100, -1, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, user, engine := setupPricingQueryAuthRegression(t)
			token := model.Token{
				UserId: user.Id, Key: "syntheticpricingstatecredential", Name: "test key",
				Status: tc.status, ExpiredTime: tc.expired, RemainQuota: tc.remaining,
				UsedQuota: 17, AccessedTime: 123,
			}
			require.NoError(t, db.Create(&token).Error)
			var before model.Token
			require.NoError(t, db.First(&before, token.Id).Error)
			response := pricingQueryAuthRequest(engine, token.Key)
			var after model.Token
			require.NoError(t, db.First(&after, token.Id).Error)
			require.Equal(t, before, after, "GET pricing must not change status, quota, accessed time, or any other key field")
			require.Equal(t, tc.wantStatus, response.Code, response.Body.String())
			if tc.wantStatus == http.StatusBadRequest {
				require.Contains(t, response.Body.String(), "model query is too long")
			}
		})
	}
}

func TestPricingQueryAuthNeverLogsCredentialSQL(t *testing.T) {
	for _, tc := range []struct {
		name       string
		wantStatus int
	}{
		{"valid credential", http.StatusBadRequest},
		{"unknown credential", http.StatusUnauthorized},
		{"database failure", http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, user, engine := setupPricingQueryAuthRegression(t)
			const key = "syntheticpricinglogcredential"
			if tc.name != "unknown credential" {
				require.NoError(t, db.Create(&model.Token{
					UserId: user.Id, Key: key, Status: common.TokenStatusEnabled,
					ExpiredTime: -1, RemainQuota: 100,
				}).Error)
			}
			if tc.name == "database failure" {
				require.NoError(t, db.Migrator().DropTable(&model.Token{}))
			}
			var logs bytes.Buffer
			// Model the application's DEBUG=true logger: SQL parameters are
			// interpolated and all queries (including failures) are recorded.
			model.DB = db.Session(&gorm.Session{Logger: logger.New(log.New(&logs, "", 0), logger.Config{
				LogLevel: logger.Info, ParameterizedQueries: false,
			})})
			require.NoError(t, model.DB.Exec("SELECT 1").Error)
			require.Contains(t, logs.String(), "SELECT 1", "the test logger must actually capture SQL")
			logs.Reset()
			response := pricingQueryAuthRequest(engine, key)
			require.Equal(t, tc.wantStatus, response.Code, response.Body.String())
			require.NotContains(t, logs.String(), key, "credential SQL must remain silent even with debug logging and database failures")
			require.NotContains(t, response.Body.String(), key)
		})
	}
}
