//go:build rt12

package router

// These tests use the real main router, middleware, controller, models and an
// isolated SQLite database. They do not provide PostgreSQL, Redis, WebAuthn or
// OAuth-provider acceptance evidence. Do not run them in parallel: the existing
// router fixtures replace package-level database and configuration variables.

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/i18n"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type rt12Reply struct {
	Success bool `json:"success"`
	Data    struct {
		ID          int    `json:"id"`
		Require2FA  bool   `json:"require_2fa"`
		FlowToken   string `json:"flow_token"`
		AccessToken string `json:"access_token"`
		Session     struct {
			SID string `json:"sid"`
		} `json:"session"`
		User struct {
			ID int `json:"id"`
		} `json:"user"`
	} `json:"data"`
}

type rt12Actor struct {
	user     model.User
	password string
	codes    []string
}

type rt12Harness struct {
	db     *gorm.DB
	engine *gin.Engine
	a, b   rt12Actor
}

func rt12Setup(t *testing.T) *rt12Harness {
	t.Helper()
	installRouterCurrencyFixture(t)
	db := setupOpenSourceBountyAccessRouterTest(t)
	require.NoError(t, db.AutoMigrate(
		&model.AuthFlow{}, &model.UserSession{}, &model.TwoFA{}, &model.TwoFABackupCode{},
		&model.PasskeyCredential{}, &model.UserOAuthBinding{}, &model.ExternalIdentityClaim{},
		&model.Token{}, &model.Log{}, &model.TopUp{}, &model.SubscriptionOrder{}, &model.UserSubscription{},
	))
	sqlDB, err := db.DB()
	require.NoError(t, err)
	// SQLite's single writer is explicit. Concurrent HTTP submissions below do
	// not stand in for a multi-connection PostgreSQL/Redis qualification run.
	sqlDB.SetMaxOpenConns(1)
	oldLogDB, oldSecret := model.LOG_DB, common.SessionSecret
	oldPassword := common.PasswordLoginEnabled
	oldGlobal, oldCritical, oldTurnstile := common.GlobalApiRateLimitEnable, common.CriticalRateLimitEnable, common.TurnstileCheckEnabled
	model.LOG_DB, common.SessionSecret = db, "rt12-isolated-session-test-only"
	common.PasswordLoginEnabled = true
	common.GlobalApiRateLimitEnable, common.CriticalRateLimitEnable, common.TurnstileCheckEnabled = false, false, false
	t.Cleanup(func() {
		model.LOG_DB, common.SessionSecret = oldLogDB, oldSecret
		common.PasswordLoginEnabled = oldPassword
		common.GlobalApiRateLimitEnable, common.CriticalRateLimitEnable, common.TurnstileCheckEnabled = oldGlobal, oldCritical, oldTurnstile
	})
	require.NoError(t, i18n.Init())
	seed := func(name string) rt12Actor {
		password := "rt12-" + name + "-password"
		hash, err := common.Password2Hash(password)
		require.NoError(t, err)
		level := model.TrustLevelMinUser + 1
		user := model.User{Username: name, Password: hash, AffCode: name, DisplayName: name,
			Group: "default", Role: common.RoleCommonUser, Status: common.UserStatusEnabled,
			AuthVersion: 1, TrustLevelOverride: &level}
		require.NoError(t, db.Create(&user).Error)
		key, err := common.GenerateTOTPSecret(name)
		require.NoError(t, err)
		require.NoError(t, db.Create(&model.TwoFA{UserId: user.Id, Secret: key.Secret(), IsEnabled: true}).Error)
		codes, err := common.GenerateBackupCodes()
		require.NoError(t, err)
		for _, code := range codes {
			hash, err := common.HashBackupCode(code)
			require.NoError(t, err)
			require.NoError(t, db.Create(&model.TwoFABackupCode{UserId: user.Id, CodeHash: hash}).Error)
		}
		return rt12Actor{user: user, password: password, codes: codes}
	}
	h := &rt12Harness{db: db, a: seed("rt12-a"), b: seed("rt12-b")}
	gin.SetMode(gin.TestMode)
	h.engine = gin.New()
	SetApiRouter(h.engine)
	return h
}

func (h *rt12Harness) request(method, path, body, access string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://rt12.invalid")
	if access != "" {
		req.Header.Set("Authorization", "Bearer "+access)
	}
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	res := httptest.NewRecorder()
	h.engine.ServeHTTP(res, req)
	return res
}

func rt12Decode(t *testing.T, res *httptest.ResponseRecorder) rt12Reply {
	t.Helper()
	var reply rt12Reply
	// Do not include response bodies in failure messages: a successful response
	// can contain a test access token or a flow token.
	require.NoError(t, json.Unmarshal(res.Body.Bytes(), &reply))
	return reply
}

func rt12JSON(t *testing.T, value interface{}) string {
	t.Helper()
	body, err := json.Marshal(value)
	require.NoError(t, err)
	return string(body)
}

func (h *rt12Harness) count(t *testing.T, entity interface{}, where string, args ...interface{}) int64 {
	t.Helper()
	query := h.db.Model(entity)
	if where != "" {
		query = query.Where(where, args...)
	}
	var count int64
	require.NoError(t, query.Count(&count).Error)
	return count
}

func rt12NoBundle(t *testing.T, res *httptest.ResponseRecorder) {
	t.Helper()
	reply := rt12Decode(t, res)
	require.True(t, reply.Data.AccessToken == "", "unexpected access token")
	require.Empty(t, reply.Data.Session.SID)
	for _, cookie := range res.Result().Cookies() {
		if cookie.Name == service.RefreshCookieName {
			require.True(t, cookie.Value == "" || cookie.MaxAge < 0, "unexpected live refresh cookie")
		}
	}
}

func (h *rt12Harness) begin(t *testing.T, actor rt12Actor) string {
	t.Helper()
	res := h.request("POST", "/api/user/login", rt12JSON(t, map[string]string{
		"username": actor.user.Username, "password": actor.password,
	}), "")
	require.Equal(t, http.StatusOK, res.Code)
	reply := rt12Decode(t, res)
	require.True(t, reply.Success)
	require.True(t, reply.Data.Require2FA, "password step did not require the enrolled factor")
	require.True(t, reply.Data.FlowToken != "", "missing second-step flow")
	rt12NoBundle(t, res)
	flow, err := model.GetAuthFlow(reply.Data.FlowToken, model.AuthFlowMatch{Purpose: model.AuthFlowPurposeTwoFALogin, UserId: actor.user.Id})
	require.NoError(t, err)
	require.Nil(t, flow.ConsumedAt)
	require.True(t, flow.ExpiresAt.After(time.Now()))
	return reply.Data.FlowToken
}

func (h *rt12Harness) finish(t *testing.T, flow, code string) *httptest.ResponseRecorder {
	t.Helper()
	return h.request("POST", "/api/user/login/2fa", rt12JSON(t, map[string]string{"flow_token": flow, "code": code}), "")
}

func (h *rt12Harness) assertAccountUnchanged(t *testing.T, actor rt12Actor, version int64) {
	t.Helper()
	var current model.User
	require.NoError(t, h.db.First(&current, actor.user.Id).Error)
	require.Equal(t, version, current.AuthVersion)
	require.Equal(t, actor.user.Email, current.Email)
	require.Equal(t, actor.user.DisplayName, current.DisplayName)
	require.True(t, current.Password == actor.user.Password, "password hash changed unexpectedly")
	require.Zero(t, h.count(t, &model.PasskeyCredential{}, "user_id = ?", actor.user.Id))
	require.Zero(t, h.count(t, &model.UserOAuthBinding{}, "user_id = ?", actor.user.Id))
}

func (h *rt12Harness) assertSessionOwner(t *testing.T, res *httptest.ResponseRecorder, actor rt12Actor, version int64) rt12Reply {
	t.Helper()
	reply := rt12Decode(t, res)
	require.True(t, reply.Success)
	require.True(t, reply.Data.AccessToken != "", "no full access token after a valid second step")
	require.Equal(t, actor.user.Id, reply.Data.User.ID)
	require.NotEmpty(t, reply.Data.Session.SID)
	require.EqualValues(t, 1, h.count(t, &model.UserSession{}, "sid = ? AND user_id = ? AND user_auth_version = ? AND status = ?",
		reply.Data.Session.SID, actor.user.Id, version, model.UserSessionStatusActive))
	self := h.request("GET", "/api/user/self", "", reply.Data.AccessToken)
	require.Equal(t, http.StatusOK, self.Code)
	selfReply := rt12Decode(t, self)
	require.True(t, selfReply.Success)
	require.Equal(t, actor.user.Id, selfReply.Data.ID)
	var hasRefresh bool
	for _, cookie := range res.Result().Cookies() {
		if cookie.Name == service.RefreshCookieName && cookie.Value != "" && cookie.MaxAge >= 0 {
			hasRefresh = true
		}
	}
	require.True(t, hasRefresh, "completed login has no refresh cookie")
	return reply
}

func TestRT12PasswordStepDoesNotAuthorizeAccountOrKeyMutations(t *testing.T) {
	h := rt12Setup(t)
	flow := h.begin(t, h.a)
	routes := []struct{ method, path, body string }{
		{"GET", "/api/user/self", ""},
		{"GET", "/api/user/token", ""},
		{"POST", "/api/token/", `{"name":"rt12-denied","unlimited_quota":true,"expired_time":-1,"group":"default"}`},
		{"PUT", "/api/user/self", `{"display_name":"rt12-not-authorized"}`},
		{"POST", "/api/user/2fa/disable", `{"code":"AAAA-BBBB"}`},
		{"POST", "/api/user/passkey/register/begin", `{}`},
		{"POST", "/api/verify", `{"method":"2fa","scope":"passkey.register","code":"123456"}`},
		{"POST", "/api/oauth/email/bind", `{"email":"candidate@rt12.invalid","code":"123456"}`},
	}
	for _, route := range routes {
		registered := false
		for _, actual := range h.engine.Routes() {
			if actual.Method == route.method && actual.Path == route.path {
				registered = true
				break
			}
		}
		require.True(t, registered, "route missing: %s %s", route.method, route.path)
		for _, mode := range []string{"none", "flow-as-bearer", "flow-as-refresh-cookie"} {
			t.Run(route.path+"/"+mode, func(t *testing.T) {
				access := ""
				var cookies []*http.Cookie
				if mode == "flow-as-bearer" {
					access = flow
				}
				if mode == "flow-as-refresh-cookie" {
					cookies = append(cookies, &http.Cookie{Name: service.RefreshCookieName, Value: flow})
				}
				res := h.request(route.method, route.path, route.body, access, cookies...)
				require.Contains(t, []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound}, res.Code)
				rt12NoBundle(t, res)
			})
		}
	}
	require.Zero(t, h.count(t, &model.UserSession{}, ""))
	require.Zero(t, h.count(t, &model.Token{}, ""))
	require.Zero(t, h.count(t, &model.TwoFABackupCode{}, "is_used = ?", true))
	require.Zero(t, h.count(t, &model.Log{}, "content LIKE ?", "Logged in successfully via %"))
	h.assertAccountUnchanged(t, h.a, 1)
	h.assertAccountUnchanged(t, h.b, 1)
	// The same real routes must have a working positive control; a broken app
	// returning errors for everything is not an authentication test pass.
	h.assertSessionOwner(t, h.finish(t, flow, h.a.codes[0]), h.a, 1)
	require.EqualValues(t, 1, h.count(t, &model.TwoFABackupCode{}, "user_id = ? AND is_used = ?", h.a.user.Id, true))
	require.Eventually(t, func() bool {
		return h.count(t, &model.Log{}, "user_id = ? AND content LIKE ?", h.a.user.Id, "Logged in successfully via 2fa%") == 1
	}, 2*time.Second, 10*time.Millisecond)
}

func TestRT12SwappedUserFlowAndBackupCodeCannotChangePrincipal(t *testing.T) {
	h := rt12Setup(t)
	flowA, flowB := h.begin(t, h.a), h.begin(t, h.b)
	for _, pair := range []struct{ flow, code string }{{flowA, h.b.codes[0]}, {flowB, h.a.codes[0]}} {
		res := h.finish(t, pair.flow, pair.code)
		require.False(t, rt12Decode(t, res).Success)
		rt12NoBundle(t, res)
	}
	require.Zero(t, h.count(t, &model.UserSession{}, ""))
	require.Zero(t, h.count(t, &model.TwoFABackupCode{}, "is_used = ?", true))
	h.assertAccountUnchanged(t, h.a, 1)
	h.assertAccountUnchanged(t, h.b, 1)
	h.assertSessionOwner(t, h.finish(t, flowA, h.a.codes[0]), h.a, 1)
	h.assertSessionOwner(t, h.finish(t, flowB, h.b.codes[0]), h.b, 1)
}

func TestRT12ExpiredOrWrongPurposeFlowDoesNotConsumeRecoveryCredential(t *testing.T) {
	for _, invalidation := range []string{"expired", "wrong-purpose"} {
		t.Run(invalidation, func(t *testing.T) {
			h := rt12Setup(t)
			token := h.begin(t, h.a)
			flow, err := model.GetAuthFlow(token, model.AuthFlowMatch{Purpose: model.AuthFlowPurposeTwoFALogin})
			require.NoError(t, err)
			if invalidation == "expired" {
				require.NoError(t, h.db.Model(flow).Update("expires_at", time.Now().Add(-time.Second)).Error)
			} else {
				// This is a purpose-mismatch fixture, not a completed WebAuthn proof.
				require.NoError(t, h.db.Model(flow).Update("purpose", model.AuthFlowPurposePasskeyStepUp).Error)
			}
			res := h.finish(t, token, h.a.codes[0])
			require.False(t, rt12Decode(t, res).Success)
			rt12NoBundle(t, res)
			require.Zero(t, h.count(t, &model.UserSession{}, ""))
			require.Zero(t, h.count(t, &model.TwoFABackupCode{}, "is_used = ?", true))
			var saved model.AuthFlow
			require.NoError(t, h.db.First(&saved, flow.Id).Error)
			require.Nil(t, saved.ConsumedAt)
			h.assertSessionOwner(t, h.finish(t, h.begin(t, h.a), h.a.codes[0]), h.a, 1)
		})
	}
}

func TestRT12ConcurrentBackupRedemptionIssuesAtMostOneSession(t *testing.T) {
	for _, separateFlows := range []bool{false, true} {
		name := "same-flow"
		if separateFlows {
			name = "separate-flows"
		}
		t.Run(name, func(t *testing.T) {
			h := rt12Setup(t)
			const workers = 3 // Below the account lockout threshold; storage races only.
			tokens := make([]string, workers)
			tokens[0] = h.begin(t, h.a)
			for i := 1; i < workers; i++ {
				tokens[i] = tokens[0]
				if separateFlows {
					tokens[i] = h.begin(t, h.a)
				}
			}
			bodies := make([]string, workers)
			for i := range bodies {
				bodies[i] = rt12JSON(t, map[string]string{"flow_token": tokens[i], "code": h.a.codes[0]})
			}
			responses := make([]*httptest.ResponseRecorder, workers)
			start := make(chan struct{})
			var wg sync.WaitGroup
			for i := range responses {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					<-start
					responses[i] = h.request("POST", "/api/user/login/2fa", bodies[i], "")
				}(i)
			}
			close(start)
			wg.Wait()
			winners := 0
			for _, res := range responses {
				if rt12Decode(t, res).Data.AccessToken != "" {
					h.assertSessionOwner(t, res, h.a, 1)
					winners++
				} else {
					rt12NoBundle(t, res)
				}
			}
			require.Equal(t, 1, winners)
			require.EqualValues(t, 1, h.count(t, &model.UserSession{}, "user_id = ?", h.a.user.Id))
			require.EqualValues(t, 1, h.count(t, &model.TwoFABackupCode{}, "user_id = ? AND is_used = ?", h.a.user.Id, true))
			require.Zero(t, h.count(t, &model.Token{}, ""))
			replay := h.finish(t, tokens[0], h.a.codes[0])
			require.False(t, rt12Decode(t, replay).Success)
			rt12NoBundle(t, replay)
			require.EqualValues(t, 1, h.count(t, &model.UserSession{}, "user_id = ?", h.a.user.Id))
		})
	}
}

func TestRT12SessionInsertFailureGrantsNoPermissionsAndRecordsConsumption(t *testing.T) {
	h := rt12Setup(t)
	token := h.begin(t, h.a)
	flow, err := model.GetAuthFlow(token, model.AuthFlowMatch{Purpose: model.AuthFlowPurposeTwoFALogin})
	require.NoError(t, err)
	var injected atomic.Int32
	const callback = "rt12:reject-session-insert"
	require.NoError(t, h.db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Table == "user_sessions" {
			injected.Add(1)
			tx.AddError(errors.New("rt12 isolated session insert failure"))
		}
	}))
	defer h.db.Callback().Create().Remove(callback)
	res := h.finish(t, token, h.a.codes[0])
	require.Positive(t, injected.Load(), "the intended failure boundary was never reached")
	require.False(t, rt12Decode(t, res).Success)
	rt12NoBundle(t, res)
	require.Zero(t, h.count(t, &model.UserSession{}, ""))
	require.Zero(t, h.count(t, &model.Token{}, ""))
	require.Zero(t, h.count(t, &model.Log{}, "content LIKE ?", "Logged in successfully via %"))
	var saved model.AuthFlow
	require.NoError(t, h.db.First(&saved, flow.Id).Error)
	used := h.count(t, &model.TwoFABackupCode{}, "user_id = ? AND is_used = ?", h.a.user.Id, true)
	// Record facts without assuming every credential type must be restored on
	// failure. This observation alone is not a rollback-consistency verdict.
	t.Logf("RT12 failure state: flow_consumed=%t used_backup_count=%d sessions=0 keys=0", saved.ConsumedAt != nil, used)
	require.NoError(t, h.db.Callback().Create().Remove(callback))
	h.assertSessionOwner(t, h.finish(t, h.begin(t, h.a), h.a.codes[1]), h.a, 1)
}

func TestRT12DisablingFactorInvalidatesAnEarlierPasswordStep(t *testing.T) {
	h := rt12Setup(t)
	oldFlow := h.begin(t, h.a)
	login := h.assertSessionOwner(t, h.finish(t, h.begin(t, h.a), h.a.codes[0]), h.a, 1)
	disabled := h.request("POST", "/api/user/2fa/disable", rt12JSON(t, map[string]string{"code": h.a.codes[1]}), login.Data.AccessToken)
	require.True(t, rt12Decode(t, disabled).Success)
	var current model.User
	require.NoError(t, h.db.First(&current, h.a.user.Id).Error)
	require.EqualValues(t, 2, current.AuthVersion)
	require.Zero(t, h.count(t, &model.TwoFA{}, "user_id = ? AND is_enabled = ?", h.a.user.Id, true))
	staleSession := h.request("GET", "/api/user/self", "", login.Data.AccessToken)
	require.Contains(t, []int{http.StatusUnauthorized, http.StatusForbidden}, staleSession.Code)
	replay := h.finish(t, oldFlow, h.a.codes[2])
	require.False(t, rt12Decode(t, replay).Success)
	rt12NoBundle(t, replay)
	require.EqualValues(t, 1, h.count(t, &model.UserSession{}, "user_id = ?", h.a.user.Id))
	require.Zero(t, h.count(t, &model.Token{}, ""))
	h.assertAccountUnchanged(t, h.b, 1)
}
