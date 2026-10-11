package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func newIPPolicyLookupTestRequest(path, peer, clientIP, country, authorization string) *gin.Context {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, path, nil)
	c.Request.RemoteAddr = peer
	c.Request.Header.Set("X-Original-Client-IP", clientIP)
	c.Request.Header.Set("X-LMM-Edge-Country", country)
	c.Request.Header.Set("Authorization", authorization)
	return c
}

func setIPPolicyLookupTestRules(t *testing.T, rules string) {
	t.Helper()
	previous := setting.GetIPAccessRoutingRules()
	require.NoError(t, setting.UpdateIPAccessRoutingRules(rules))
	t.Cleanup(func() { require.NoError(t, setting.UpdateIPAccessRoutingRules(previous)) })
}

func TestIPPolicySkipsUnnecessaryKeyLookups(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupKeyBypassIPPolicyTest(t)
	setIPPolicyLookupTestRules(t, "dip(203.0.113.8) -> direct\ndip(geoip:cn) -> reject\nfallback: direct")

	queries := 0
	require.NoError(t, model.DB.Callback().Query().Before("gorm:query").Register("count_policy_queries", func(*gorm.DB) { queries++ }))
	for _, path := range []string{"/internal/access-ip-policy", "/api/internal/access-ip-policy"} {
		for _, test := range []struct {
			name, peer, ip, country string
			status                  int
		}{
			{"ordered direct", "127.0.0.1:42000", "203.0.113.8", "CN", http.StatusNoContent},
			{"fallback direct", "[::1]:42000", "198.51.100.8", "US", http.StatusNoContent},
			{"untrusted peer", "198.51.100.8:42000", "203.0.113.8", "CN", http.StatusForbidden},
			{"invalid client IP", "127.0.0.1:42000", "invalid", "CN", http.StatusForbidden},
			{"invalid country", "127.0.0.1:42000", "198.51.100.8", "invalid", http.StatusForbidden},
		} {
			t.Run(path+"/"+test.name, func(t *testing.T) {
				before := queries
				c := newIPPolicyLookupTestRequest(path, test.peer, test.ip, test.country, "Bearer sk-unknownfixturekey-group")
				CheckIPAccessRoutingPolicy(c)
				assert.Equal(t, test.status, c.Writer.Status())
				assert.Equal(t, before, queries, "direct, invalid-metadata and non-loopback requests must not query token storage")
			})
		}
	}
}

func countIPPolicyTokenQueries(t *testing.T, beforeQuery func()) *atomic.Int64 {
	t.Helper()
	queries := &atomic.Int64{}
	require.NoError(t, model.DB.Callback().Query().Before("gorm:query").Register("count_token_policy_queries", func(db *gorm.DB) {
		if db.Statement.Table == "tokens" {
			queries.Add(1)
			if beforeQuery != nil {
				beforeQuery()
			}
		}
	}))
	return queries
}

func TestIPPolicyBoundsRotatingInvalidKeysAcrossAliases(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupKeyBypassIPPolicyTest(t)
	setIPPolicyLookupTestRules(t, "fallback: reject")
	queries := countIPPolicyTokenQueries(t, nil)
	paths := []string{"/internal/access-ip-policy", "/api/internal/access-ip-policy"}
	clientIPs := []string{"203.0.113.8", "::ffff:203.0.113.8"}
	prefixes := []string{"", "Bearer ", "bearer sk-"}
	for i := range ipPolicyBypassSourceFailures + 12 {
		c := newIPPolicyLookupTestRequest(paths[i%len(paths)], "127.0.0.1:42000", clientIPs[i%len(clientIPs)], "CN", prefixes[i%len(prefixes)]+fmt.Sprintf("unknown%d-group", i))
		// These client-supplied forwarding headers must not partition the budget.
		c.Request.Header.Set("X-Forwarded-For", fmt.Sprintf("198.51.100.%d", i+1))
		c.Request.Header.Set("X-Real-IP", fmt.Sprintf("192.0.2.%d", i+1))
		CheckIPAccessRoutingPolicy(c)
		assert.Equal(t, http.StatusForbidden, c.Writer.Status())
		assert.Equal(t, accessPolicyDenied, c.Writer.Header().Get(accessPolicyResultHeader))
	}
	assert.EqualValues(t, ipPolicyBypassSourceFailures, queries.Load())

	c := newIPPolicyLookupTestRequest(paths[0], "127.0.0.1:42000", "203.0.113.9", "CN", "Bearer sk-anotherfixture")
	CheckIPAccessRoutingPolicy(c)
	assert.EqualValues(t, ipPolicyBypassSourceFailures+1, queries.Load(), "another source retains its own budget")

	setIPPolicyLookupTestRules(t, "fallback: direct")
	c = newIPPolicyLookupTestRequest(paths[0], "127.0.0.1:42000", "203.0.113.8", "CN", "Bearer sk-anotherfixture")
	CheckIPAccessRoutingPolicy(c)
	assert.Equal(t, http.StatusNoContent, c.Writer.Status(), "exhaustion must not reject policy-allowed traffic")
	assert.EqualValues(t, ipPolicyBypassSourceFailures+1, queries.Load())
}

func TestIPPolicyBoundsConcurrentInvalidKeyLookups(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupKeyBypassIPPolicyTest(t)
	setIPPolicyLookupTestRules(t, "fallback: reject")
	sqlDB, err := model.DB.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	release := make(chan struct{})
	queries := countIPPolicyTokenQueries(t, func() { <-release })
	const requests = 48
	finished := make(chan int, requests)
	for i := range requests {
		go func() {
			c := newIPPolicyLookupTestRequest("/internal/access-ip-policy", "127.0.0.1:42000", "203.0.113.8", "CN", fmt.Sprintf("Bearer sk-concurrent%d", i))
			CheckIPAccessRoutingPolicy(c)
			finished <- c.Writer.Status()
		}()
	}

	// The admitted lookups remain blocked in the query callback; all other
	// requests must finish without waiting for DB work or consuming new slots.
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	completed := 0
waitForDenied:
	for completed < requests-ipPolicyBypassSourceFailures {
		select {
		case status := <-finished:
			assert.Equal(t, http.StatusForbidden, status)
			completed++
		case <-timer.C:
			t.Error("concurrent misses exceeded the lookup budget")
			break waitForDenied
		}
	}
	assert.LessOrEqual(t, queries.Load(), int64(ipPolicyBypassSourceFailures))
	close(release)
	for completed < requests {
		assert.Equal(t, http.StatusForbidden, <-finished)
		completed++
	}
	assert.EqualValues(t, ipPolicyBypassSourceFailures, queries.Load())
}

func TestIPPolicyBoundsDistinctSourceLookups(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupKeyBypassIPPolicyTest(t)
	setIPPolicyLookupTestRules(t, "fallback: reject")
	queries := countIPPolicyTokenQueries(t, nil)
	for i := range ipPolicyBypassGlobalFailures + 32 {
		c := newIPPolicyLookupTestRequest("/internal/access-ip-policy", "127.0.0.1:42000", fmt.Sprintf("2001:db8::%x", i+1), "CN", fmt.Sprintf("Bearer sk-distinct%d", i))
		assert.False(t, keyBypassesIPPolicy(c))
	}
	assert.EqualValues(t, ipPolicyBypassGlobalFailures, queries.Load())
}

func TestIPPolicySuccessfulBypassIsFreshAndDoesNotConsumeFailureBudget(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupKeyBypassIPPolicyTest(t)
	setIPPolicyLookupTestRules(t, "fallback: reject")
	user := createBypassTestUser(t, 1, true)
	createBypassTestToken(t, user.Id, "freshbypassfixture")
	queries := countIPPolicyTokenQueries(t, nil)
	for i := range ipPolicyBypassGlobalFailures + 4 {
		path := "/internal/access-ip-policy"
		if i%2 == 0 {
			path = "/api/internal/access-ip-policy"
		}
		c := newIPPolicyLookupTestRequest(path, "127.0.0.1:42000", "203.0.113.8", "CN", "bearer sk-freshbypassfixture-mygroup")
		CheckIPAccessRoutingPolicy(c)
		assert.Equal(t, http.StatusNoContent, c.Writer.Status())
		assert.Empty(t, c.Writer.Header().Get(accessPolicyResultHeader))
	}
	assert.EqualValues(t, ipPolicyBypassGlobalFailures+4, queries.Load(), "a successful bypass must not cache the credential or decision")

	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", user.Id).Update("setting", `{"allow_key_bypass_ip_policy":false}`).Error)
	c := newIPPolicyLookupTestRequest("/internal/access-ip-policy", "127.0.0.1:42000", "203.0.113.8", "CN", "Bearer sk-freshbypassfixture")
	CheckIPAccessRoutingPolicy(c)
	assert.Equal(t, http.StatusForbidden, c.Writer.Status(), "revoking opt-in takes effect on the next lookup")
}

func TestIPPolicyBypassPreservesTokenValidityAndQuota(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupKeyBypassIPPolicyTest(t)
	setIPPolicyLookupTestRules(t, "fallback: reject")
	user := createBypassTestUser(t, 1, true)
	for i, test := range []struct {
		name   string
		update map[string]any
		status int
	}{
		{"valid finite quota", map[string]any{"unlimited_quota": false, "remain_quota": 10}, http.StatusNoContent},
		{"disabled token", map[string]any{"status": common.TokenStatusDisabled}, http.StatusForbidden},
		{"expired token", map[string]any{"expired_time": common.GetTimestamp() - 1}, http.StatusForbidden},
		{"exhausted quota", map[string]any{"unlimited_quota": false, "remain_quota": 0}, http.StatusForbidden},
		{"oauth managed token", map[string]any{"oauth_managed": true}, http.StatusForbidden},
		{"assistant runtime token", map[string]any{"creation_source": model.TokenCreationSourceAssistantRuntime}, http.StatusForbidden},
	} {
		t.Run(test.name, func(t *testing.T) {
			key := fmt.Sprintf("validityfixture%d", i)
			createBypassTestToken(t, user.Id, key)
			require.NoError(t, model.DB.Model(&model.Token{}).Where("key = ?", key).Updates(test.update).Error)
			c := newIPPolicyLookupTestRequest("/internal/access-ip-policy", "127.0.0.1:42000", "203.0.113.8", "CN", "Bearer sk-"+key+"-mygroup")
			CheckIPAccessRoutingPolicy(c)
			assert.Equal(t, test.status, c.Writer.Status())
		})
	}
}

func TestIPPolicyExhaustedSourceDoesNotSpendGlobalBudget(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupKeyBypassIPPolicyTest(t)
	queries := countIPPolicyTokenQueries(t, nil)
	for i := range ipPolicyBypassGlobalFailures + 4 {
		c := newIPPolicyLookupTestRequest("/internal/access-ip-policy", "127.0.0.1:42000", "203.0.113.8", "CN", fmt.Sprintf("Bearer sk-spendfixture%d", i))
		assert.False(t, keyBypassesIPPolicy(c))
	}
	assert.EqualValues(t, ipPolicyBypassSourceFailures, queries.Load())
	c := newIPPolicyLookupTestRequest("/internal/access-ip-policy", "127.0.0.1:42000", "203.0.113.9", "CN", "Bearer sk-freshsourcefixture")
	assert.False(t, keyBypassesIPPolicy(c))
	assert.EqualValues(t, ipPolicyBypassSourceFailures+1, queries.Load(), "denied admission must release its global reservation")
}

func TestIPPolicyLookupErrorsConsumeFailureBudget(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupKeyBypassIPPolicyTest(t)
	queries := countIPPolicyTokenQueries(t, nil)
	require.NoError(t, model.DB.Callback().Query().Before("gorm:query").Register("policy_database_error", func(db *gorm.DB) {
		db.AddError(fmt.Errorf("local fixture database unavailable"))
	}))
	for i := range ipPolicyBypassSourceFailures + 4 {
		c := newIPPolicyLookupTestRequest("/internal/access-ip-policy", "127.0.0.1:42000", "203.0.113.8", "CN", fmt.Sprintf("Bearer sk-errorfixture%d", i))
		assert.False(t, keyBypassesIPPolicy(c))
	}
	assert.EqualValues(t, ipPolicyBypassSourceFailures, queries.Load())
}

func TestIPPolicyBoundsRedisMissesBeforeDatabaseFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupKeyBypassIPPolicyTest(t)
	cache := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: cache.Addr()})
	previousClient := common.RDB
	common.RDB = client
	common.RedisEnabled = true
	t.Cleanup(func() {
		common.RDB = previousClient
		_ = client.Close()
	})
	queries := countIPPolicyTokenQueries(t, nil)
	for i := range ipPolicyBypassSourceFailures + 4 {
		c := newIPPolicyLookupTestRequest("/internal/access-ip-policy", "127.0.0.1:42000", "203.0.113.8", "CN", fmt.Sprintf("Bearer sk-redisfixture%d", i))
		assert.False(t, keyBypassesIPPolicy(c))
	}
	assert.EqualValues(t, ipPolicyBypassSourceFailures, queries.Load())
	assert.Empty(t, cache.Keys(), "failed credentials are not retained in Redis")
}

func TestIPPolicyUnavailableGeoIPPreservesBoundedKeyBypass(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupKeyBypassIPPolicyTest(t)
	setIPPolicyLookupTestRules(t, setting.DefaultIPAccessRoutingRules)
	user := createBypassTestUser(t, 1, true)
	createBypassTestToken(t, user.Id, "unavailablegeoipfixture")
	queries := countIPPolicyTokenQueries(t, nil)
	for _, path := range []string{"/internal/access-ip-policy", "/api/internal/access-ip-policy"} {
		c := newIPPolicyLookupTestRequest(path, "127.0.0.1:42000", "203.0.113.8", "", "Bearer sk-unavailablegeoipfixture-group")
		CheckIPAccessRoutingPolicy(c)
		assert.Equal(t, http.StatusNoContent, c.Writer.Status())
		assert.Empty(t, c.Writer.Header().Get(accessPolicyResultHeader))
	}
	for i := range ipPolicyBypassSourceFailures + 4 {
		c := newIPPolicyLookupTestRequest("/internal/access-ip-policy", "127.0.0.1:42000", "203.0.113.8", "", fmt.Sprintf("Bearer sk-unavailablegeoipunknown%d", i))
		CheckIPAccessRoutingPolicy(c)
		assert.Equal(t, http.StatusForbidden, c.Writer.Status())
		assert.Equal(t, accessPolicyDenied, c.Writer.Header().Get(accessPolicyResultHeader))
	}
	assert.EqualValues(t, 2+ipPolicyBypassSourceFailures, queries.Load(), "unavailable GeoIP must not restore unbounded lookups")

	for _, metadata := range []struct{ ip, country string }{
		{"", ""}, {"invalid", ""}, {"203.0.113.9", "invalid"},
	} {
		c := newIPPolicyLookupTestRequest("/internal/access-ip-policy", "127.0.0.1:42000", metadata.ip, metadata.country, "Bearer sk-unavailablegeoipfixture")
		CheckIPAccessRoutingPolicy(c)
		assert.Equal(t, http.StatusForbidden, c.Writer.Status())
	}
	assert.EqualValues(t, 2+ipPolicyBypassSourceFailures, queries.Load(), "invalid trusted metadata still fails closed before lookup")
}
