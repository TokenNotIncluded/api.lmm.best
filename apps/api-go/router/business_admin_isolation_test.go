package router

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type bi10Actor struct {
	user model.User
	pat  string
	key  model.Token
}

type bi10HTTPFixture struct {
	db     *gorm.DB
	engine *gin.Engine
	actors map[string]*bi10Actor
}

// This fixture registers main's real API routes. It does not replace UserAuth,
// AdminAuth, RootAuth, credential classification, or ownership checks. Ordinary
// active users have a stored activation fact, not an always-allow test flag.
func bi10HTTP(t *testing.T) *bi10HTTPFixture {
	t.Helper()
	require.False(t, model.LocalAcceptanceDeveloperAccessEnabled(), "disable the local acceptance bypass before authorization tests")
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousRedis, previousRDB := common.RedisEnabled, common.RDB
	previousMainType, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
	previousRate, previousSecret, previousMode := common.GlobalApiRateLimitEnable, common.SessionSecret, gin.Mode()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "bi10-http.sqlite")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	model.DB, model.LOG_DB = db, db
	common.RedisEnabled, common.RDB = false, nil
	common.GlobalApiRateLimitEnable = false // rate-limit behavior is outside this suite
	common.SessionSecret = "bi10-local-only-session-secret"
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.RedisEnabled, common.RDB = previousRedis, previousRDB
		common.GlobalApiRateLimitEnable, common.SessionSecret = previousRate, previousSecret
		common.SetDatabaseTypes(previousMainType, previousLogType)
		gin.SetMode(previousMode)
		require.NoError(t, sqlDB.Close())
	})
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.UserSession{}, &model.Token{}, &model.TopUp{}, &model.SubscriptionOrder{}, &model.Log{}))
	fixture := &bi10HTTPFixture{db: db, actors: map[string]*bi10Actor{}}
	for index, spec := range []struct {
		name   string
		role   int
		active bool
		banned bool
	}{
		{"a", common.RoleCommonUser, true, false},
		{"b", common.RoleCommonUser, true, false},
		{"restricted", common.RoleCommonUser, false, false},
		{"admin", common.RoleAdminUser, true, false},
		{"root", common.RoleRootUser, true, false},
		{"banned", common.RoleCommonUser, true, true},
	} {
		actor := &bi10Actor{pat: "bi10-local-pat-" + spec.name}
		actor.user = model.User{
			Id: 11001 + index, Username: "bi10-" + spec.name, AffCode: "bi10-" + spec.name,
			Password: "not-used-for-pat-auth", Role: spec.role,
			Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1,
			AccessToken: &actor.pat,
		}
		if spec.active {
			actor.user.ConsoleActivatedAt = time.Now().Unix()
		}
		if spec.banned {
			actor.user.Status = common.UserStatusDisabled
		}
		require.NoError(t, db.Create(&actor.user).Error)
		actor.key = model.Token{
			UserId: actor.user.Id, Key: "bi10-local-key-" + spec.name,
			Name: "bi10-" + spec.name, Status: common.TokenStatusEnabled,
			ExpiredTime: -1, RemainQuota: 100, CreationSource: model.TokenCreationSourceManual,
		}
		require.NoError(t, db.Create(&actor.key).Error)
		fixture.actors[spec.name] = actor
	}
	fixture.engine = gin.New()
	SetApiRouter(fixture.engine)
	return fixture
}

func (fixture *bi10HTTPFixture) request(method, path, credential, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if credential != "" {
		request.Header.Set("Authorization", "Bearer "+credential)
	}
	// These untrusted headers must not replace the authenticated identity.
	request.Header.Set("X-Role", "100")
	request.Header.Set("X-User-Id", "11002")
	request.Header.Set("New-Api-User", "11002")
	response := httptest.NewRecorder()
	fixture.engine.ServeHTTP(response, request)
	return response
}

func bi10Denied(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	switch response.Code {
	case http.StatusOK:
		var body struct {
			Success *bool `json:"success"`
		}
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
		require.NotNil(t, body.Success, "200 must contain an explicit business denial")
		require.False(t, *body.Success)
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound:
		// Console discovery intentionally returns 404 for some denied callers.
	default:
		t.Fatalf("not an authorization denial: HTTP %d: %s", response.Code, response.Body.String())
	}
}

func bi10Row(t *testing.T, db *gorm.DB, id int) model.Token {
	t.Helper()
	var row model.Token
	require.NoError(t, db.Unscoped().First(&row, id).Error)
	return row
}

func bi10OwnExport(t *testing.T, fixture *bi10HTTPFixture, actor *bi10Actor, credential string) {
	t.Helper()
	response := fixture.request(http.MethodPost, "/api/token/batch/keys", credential,
		fmt.Sprintf(`{"ids":[%d]}`, actor.key.Id))
	require.Equal(t, http.StatusOK, response.Code)
	var body struct {
		Success bool `json:"success"`
		Data    struct {
			Keys map[string]string `json:"keys"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.True(t, body.Success)
	require.Equal(t, map[string]string{fmt.Sprint(actor.key.Id): actor.key.Key}, body.Data.Keys)
}

func TestBI10RealRoutesRejectForeignKeyObjects(t *testing.T) {
	fixture := bi10HTTP(t)
	for _, name := range []string{"anonymous", "a", "b", "restricted", "admin", "root", "banned"} {
		t.Run(name, func(t *testing.T) {
			foreign := fixture.actors["b"].key
			if name == "b" {
				foreign = fixture.actors["a"].key
			}
			before := bi10Row(t, fixture.db, foreign.Id)
			credential := ""
			if actor := fixture.actors[name]; actor != nil {
				credential = actor.pat
			}
			for _, operation := range []struct{ method, path, body string }{
				{http.MethodGet, fmt.Sprintf("/api/token/%d", foreign.Id), ""},
				{http.MethodPut, "/api/token/?status_only=true", fmt.Sprintf(`{"id":%d,"user_id":%d,"role":100,"status":%d}`, foreign.Id, foreign.UserId, common.TokenStatusDisabled)},
				{http.MethodDelete, fmt.Sprintf("/api/token/%d", foreign.Id), ""},
			} {
				response := fixture.request(operation.method, operation.path, credential, operation.body)
				bi10Denied(t, response)
				require.NotContains(t, response.Body.String(), foreign.Key)
				require.Equal(t, before, bi10Row(t, fixture.db, foreign.Id))
			}
		})
	}
}

func TestBI10RealRoutesExportOnlyAuthenticatedOwnerKeys(t *testing.T) {
	fixture := bi10HTTP(t)
	for _, name := range []string{"a", "b", "admin", "root"} {
		t.Run(name, func(t *testing.T) {
			foreign := fixture.actors["b"].key
			if name == "b" {
				foreign = fixture.actors["a"].key
			}
			actor := fixture.actors[name]
			payload := fmt.Sprintf(`{"ids":[%d,%d],"user_id":%d,"role":100}`, actor.key.Id, foreign.Id, foreign.UserId)
			response := fixture.request(http.MethodPost, "/api/token/batch/keys", actor.pat, payload)
			require.Equal(t, http.StatusOK, response.Code)
			var body struct {
				Success bool `json:"success"`
				Data    struct {
					Keys map[string]string `json:"keys"`
				} `json:"data"`
			}
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
			require.True(t, body.Success)
			require.Equal(t, map[string]string{fmt.Sprint(actor.key.Id): actor.key.Key}, body.Data.Keys)
			require.NotContains(t, response.Body.String(), foreign.Key)
			require.False(t, bi10Row(t, fixture.db, foreign.Id).DeletedAt.Valid)
		})
	}
}

func TestBI10RealAdminGatesIgnoreForgedRoleHeaders(t *testing.T) {
	fixture := bi10HTTP(t)
	ordinary := fixture.actors["a"]
	response := fixture.request(http.MethodGet, fmt.Sprintf("/api/user/%d", fixture.actors["b"].user.Id), ordinary.pat, "")
	require.Equal(t, http.StatusForbidden, response.Code)
	require.Contains(t, response.Body.String(), "AUTH_INSUFFICIENT_PRIVILEGE")
	response = fixture.request(http.MethodGet, "/api/option/", fixture.actors["admin"].pat, "")
	require.Equal(t, http.StatusForbidden, response.Code)
	require.Contains(t, response.Body.String(), "AUTH_INSUFFICIENT_PRIVILEGE")
}

func TestBI10RealRoutesRejectRotatedPAT(t *testing.T) {
	fixture := bi10HTTP(t)
	actor := fixture.actors["a"]
	bi10OwnExport(t, fixture, actor, actor.pat)
	before := bi10Row(t, fixture.db, actor.key.Id)
	// This is a stored revocation fixture, not a claim that the account's PAT
	// rotation controller has been tested. That controller needs separate tests.
	require.NoError(t, fixture.db.Model(&actor.user).Update("access_token", "bi10-replacement-pat").Error)
	response := fixture.request(http.MethodDelete, fmt.Sprintf("/api/token/%d", actor.key.Id), actor.pat, "")
	bi10Denied(t, response)
	require.Equal(t, before, bi10Row(t, fixture.db, actor.key.Id))
}

func TestBI10RealRoutesRejectExpiredStoredSession(t *testing.T) {
	fixture := bi10HTTP(t)
	actor := fixture.actors["a"]
	now := time.Now().Unix()
	session := model.UserSession{
		SID: "bi10-expired-session", UserID: actor.user.Id, Version: 1,
		UserAuthVersion: actor.user.AuthVersion, Status: model.UserSessionStatusActive,
		RefreshHash: "bi10-expired-refresh-hash", LoginMethod: "password",
		LastActiveAt: now, ExpiresAt: now + 3600,
	}
	require.NoError(t, model.CreateUserSession(&session))
	credential, _, err := service.IssueAccessToken(service.AuthIdentity{
		UserID: actor.user.Id, SessionID: session.SID,
		UserAuthVersion: session.UserAuthVersion, SessionVersion: session.Version,
	})
	require.NoError(t, err)
	bi10OwnExport(t, fixture, actor, credential)
	// Expire a session that has just succeeded on a real protected route.
	updated := fixture.db.Model(&model.UserSession{}).Where("sid = ?", session.SID).Update("expires_at", now-60)
	require.NoError(t, updated.Error)
	require.EqualValues(t, 1, updated.RowsAffected)
	before := bi10Row(t, fixture.db, actor.key.Id)
	response := fixture.request(http.MethodDelete, fmt.Sprintf("/api/token/%d", actor.key.Id), credential, "")
	bi10Denied(t, response)
	require.Equal(t, before, bi10Row(t, fixture.db, actor.key.Id))
}
