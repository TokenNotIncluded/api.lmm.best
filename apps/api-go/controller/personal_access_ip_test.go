package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoopbackPeerAcceptsOnlyLoopbackAddresses(t *testing.T) {
	assert.True(t, loopbackPeer("127.0.0.1:3000"))
	assert.True(t, loopbackPeer("[::1]:3000"))
	assert.False(t, loopbackPeer("10.0.0.2:3000"))
	assert.False(t, loopbackPeer("198.51.100.2:3000"))
	assert.False(t, loopbackPeer("not-an-address"))
}

func TestAccessPolicyAPIPathMatchesNginxBackendFamilies(t *testing.T) {
	for _, path := range []string{
		"/api/status",
		"/mcp",
		"/v1/models",
		"/v1beta/models/gemini:generateContent",
		"/pg/chat/completions",
		"/mj/submit",
		"/suno/generate",
		"/kling/v1/videos",
		"/jimeng/generations",
		"/dashboard/billing/subscription",
		"/dashboard/billing/usage",
		"/openai/mj/image/task",
	} {
		t.Run(path, func(t *testing.T) {
			assert.True(t, accessPolicyAPIPath(path))
		})
	}
	for _, path := range []string{
		"/",
		"/pricing",
		"/v1beta-docs",
		"/apiary",
		"/foo/mjpeg",
		"/oauth/mj",
		"/oauth/mj/callback",
		"/static/mj/asset.js",
	} {
		t.Run("browser "+path, func(t *testing.T) {
			assert.False(t, accessPolicyAPIPath(path))
		})
	}
}

func TestPersonalAccessIPCompatibilityEndpointIsRetired(t *testing.T) {
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/api/user/access-ip", nil)

	PersonalAccessIPRetired(context)

	assert.Equal(t, http.StatusGone, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "PERSONAL_IP_ACCESS_RETIRED")
}

func TestIPAccessRoutingPolicyMarksOnlyDeniedRequests(t *testing.T) {
	gin.SetMode(gin.TestMode)
	originalRules := setting.GetIPAccessRoutingRules()
	require.NoError(t, setting.UpdateIPAccessRoutingRules(setting.DefaultIPAccessRoutingRules))
	t.Cleanup(func() {
		require.NoError(t, setting.UpdateIPAccessRoutingRules(originalRules))
	})

	t.Run("China request is marked denied", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		context, _ := gin.CreateTestContext(recorder)
		context.Request = httptest.NewRequest(http.MethodGet, "/internal/access-ip-policy", nil)
		context.Request.RemoteAddr = "127.0.0.1:42000"
		context.Request.Header.Set("X-LMM-CN-Source", "1")
		context.Request.Header.Set("X-Original-Client-IP", "203.0.113.8")

		CheckIPAccessRoutingPolicy(context)

		assert.Equal(t, http.StatusForbidden, context.Writer.Status())
		assert.Equal(t, accessPolicyDenied, recorder.Header().Get(accessPolicyResultHeader))
	})

	t.Run("unknown edge country fails closed", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		context, _ := gin.CreateTestContext(recorder)
		context.Request = httptest.NewRequest(http.MethodGet, "/internal/access-ip-policy", nil)
		context.Request.RemoteAddr = "127.0.0.1:42000"
		context.Request.Header.Set("X-Original-Client-IP", "203.0.113.8")

		CheckIPAccessRoutingPolicy(context)

		assert.Equal(t, http.StatusForbidden, context.Writer.Status())
		assert.Equal(t, accessPolicyDenied, recorder.Header().Get(accessPolicyResultHeader))
	})

	t.Run("public peer cannot spoof edge headers", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		context, _ := gin.CreateTestContext(recorder)
		context.Request = httptest.NewRequest(http.MethodGet, "/internal/access-ip-policy", nil)
		context.Request.RemoteAddr = "198.51.100.2:42000"
		context.Request.Header.Set("X-LMM-Edge-Country", "US")
		context.Request.Header.Set("X-LMM-CN-Source", "0")

		CheckIPAccessRoutingPolicy(context)

		assert.Equal(t, http.StatusForbidden, context.Writer.Status())
		assert.Equal(t, accessPolicyDenied, recorder.Header().Get(accessPolicyResultHeader))
	})

	t.Run("specific direct rule overrides broader country rejection", func(t *testing.T) {
		require.NoError(t, setting.UpdateIPAccessRoutingRules("dip(203.0.113.8) -> direct\ndip(geoip:cn) -> reject"))
		t.Cleanup(func() {
			require.NoError(t, setting.UpdateIPAccessRoutingRules(setting.DefaultIPAccessRoutingRules))
		})

		recorder := httptest.NewRecorder()
		context, _ := gin.CreateTestContext(recorder)
		context.Request = httptest.NewRequest(http.MethodGet, "/internal/access-ip-policy", nil)
		context.Request.RemoteAddr = "127.0.0.1:42000"
		context.Request.Header.Set("X-LMM-CN-Source", "1")
		context.Request.Header.Set("X-Original-Client-IP", "203.0.113.8")

		CheckIPAccessRoutingPolicy(context)

		assert.Equal(t, http.StatusNoContent, context.Writer.Status())
		assert.Empty(t, recorder.Header().Get(accessPolicyResultHeader))
	})

	t.Run("client supplied destination IP metadata cannot bypass a client rule", func(t *testing.T) {
		require.NoError(t, setting.UpdateIPAccessRoutingRules("dip(203.0.113.8) -> reject\nfallback: direct"))
		t.Cleanup(func() {
			require.NoError(t, setting.UpdateIPAccessRoutingRules(setting.DefaultIPAccessRoutingRules))
		})

		recorder := httptest.NewRecorder()
		context, _ := gin.CreateTestContext(recorder)
		context.Request = httptest.NewRequest(http.MethodGet, "/internal/access-ip-policy", nil)
		context.Request.RemoteAddr = "127.0.0.1:42000"
		context.Request.Header.Set("X-Original-Client-IP", "203.0.113.8")
		context.Request.Header.Set("X-LMM-Edge-Destination-IP", "198.51.100.9")

		CheckIPAccessRoutingPolicy(context)

		assert.Equal(t, http.StatusForbidden, context.Writer.Status())
		assert.Equal(t, accessPolicyDenied, recorder.Header().Get(accessPolicyResultHeader))
	})
}

func TestAccessPolicyErrorPageRequiresCapturedDenial(t *testing.T) {
	gin.SetMode(gin.TestMode)

	request := func(remoteAddr string, headers map[string]string) (int, *httptest.ResponseRecorder) {
		recorder := httptest.NewRecorder()
		context, _ := gin.CreateTestContext(recorder)
		context.Request = httptest.NewRequest(http.MethodGet, "/internal/errors/access-policy", nil)
		context.Request.RemoteAddr = remoteAddr
		for name, value := range headers {
			context.Request.Header.Set(name, value)
		}
		GetAccessPolicyErrorPage(context)
		return context.Writer.Status(), recorder
	}

	validHeaders := map[string]string{
		"X-LMM-Internal-Error":   accessPolicyErrorHeader,
		accessPolicyResultHeader: accessPolicyDenied,
		"X-Original-Client-IP":   "203.0.113.42",
		"X-LMM-CN-Source":        "1",
		"X-LMM-Edge-Country":     "CN",
		"User-Agent":             "Mozilla/5.0 TestBrowser",
	}
	status, response := request("127.0.0.1:42000", validHeaders)
	require.Equal(t, http.StatusUnavailableForLegalReasons, status)
	assert.Contains(t, response.Header().Get("Content-Type"), "text/html")
	body := response.Body.String()
	assert.Contains(t, body, "当前网络请求已被拒绝")
	assert.Contains(t, body, "疑难解答")
	assert.Contains(t, body, "203.0.113.42")
	assert.Contains(t, body, "IPv4")
	assert.Contains(t, body, "route_reject")
	assert.NotContains(t, body, "符合条件的账号")

	jsonHeaders := make(map[string]string, len(validHeaders)+4)
	for name, value := range validHeaders {
		jsonHeaders[name] = value
	}
	jsonHeaders[accessPolicyOriginalURIHeader] = "/v1/models?debug=1"
	jsonHeaders[accessPolicyOriginalAcceptHeader] = "*/*"
	jsonHeaders["Origin"] = "https://sdk.example"
	jsonHeaders["Authorization"] = "Bearer must-not-leak"
	jsonHeaders["Cookie"] = "session=must-not-leak"
	status, response = request("127.0.0.1:42000", jsonHeaders)
	require.Equal(t, http.StatusUnavailableForLegalReasons, status)
	assert.Contains(t, response.Header().Get("Content-Type"), "application/json")
	assert.Equal(t, "*", response.Header().Get("Access-Control-Allow-Origin"))
	assert.Empty(t, response.Header().Get("Access-Control-Allow-Credentials"))
	assert.Equal(t, common.RequestIdKey, response.Header().Get("Access-Control-Expose-Headers"))
	assert.Equal(t, "Accept, Origin", response.Header().Get("Vary"))
	assert.Less(t, response.Body.Len(), 512)
	assert.NotContains(t, response.Body.String(), "must-not-leak")
	assert.NotContains(t, response.Body.String(), "203.0.113.42")
	var payload struct {
		Error struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			RequestID string `json:"request_id"`
			Type      string `json:"type"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
	assert.Equal(t, accessPolicyRejectedErrorCode, payload.Error.Code)
	assert.Equal(t, accessPolicyRejectedMessage, payload.Error.Message)
	assert.Equal(t, accessPolicyRejectedErrorType, payload.Error.Type)
	assert.NotEmpty(t, payload.Error.RequestID)

	jsonHeaders[accessPolicyOriginalURIHeader] = "/api/status"
	jsonHeaders[accessPolicyOriginalAcceptHeader] = "text/plain, application/problem+json; q=0.9"
	status, response = request("127.0.0.1:42000", jsonHeaders)
	require.Equal(t, http.StatusUnavailableForLegalReasons, status)
	assert.Contains(t, response.Header().Get("Content-Type"), "application/json")

	for _, frontendURI := range []string{"/", "/oauth/mj", "/static/mj/asset.js"} {
		jsonHeaders[accessPolicyOriginalURIHeader] = frontendURI
		jsonHeaders[accessPolicyOriginalAcceptHeader] = "application/json; q=0, text/html"
		status, response = request("127.0.0.1:42000", jsonHeaders)
		require.Equal(t, http.StatusUnavailableForLegalReasons, status)
		assert.Contains(t, response.Header().Get("Content-Type"), "text/html")
		assert.Empty(t, response.Header().Get("Access-Control-Allow-Origin"))
		assert.Empty(t, response.Header().Get("Access-Control-Allow-Credentials"))
		assert.Equal(t, "Accept, Origin", response.Header().Get("Vary"))
		assert.NotContains(t, response.Body.String(), "must-not-leak")
	}

	status, _ = request("127.0.0.1:42000", map[string]string{
		"X-LMM-Internal-Error": accessPolicyErrorHeader,
	})
	assert.Equal(t, http.StatusNotFound, status)

	status, _ = request("198.51.100.2:42000", validHeaders)
	assert.Equal(t, http.StatusNotFound, status)
}
