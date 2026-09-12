package controller

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type cacheRuntimeResponse struct {
	Success bool           `json:"success"`
	Ready   bool           `json:"ready"`
	Live    bool           `json:"live"`
	Message string         `json:"message"`
	Data    map[string]any `json:"data"`
}

func preserveCacheRuntimeHooks(t *testing.T) {
	t.Helper()
	previousReadiness := cacheReadinessError
	previousEnsure := ensureCachesWarmAsync
	previousGetPricing := getPricingCache
	previousRefreshPricing := refreshPricingCache
	t.Cleanup(func() {
		cacheReadinessError = previousReadiness
		ensureCachesWarmAsync = previousEnsure
		getPricingCache = previousGetPricing
		refreshPricingCache = previousRefreshPricing
	})
}

func decodeCacheRuntimeResponse(t *testing.T, recorder *httptest.ResponseRecorder) cacheRuntimeResponse {
	t.Helper()
	var response cacheRuntimeResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	return response
}

func TestGetLivenessDoesNotDependOnCacheReadiness(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/api/livez", nil)

	GetLiveness(context)

	require.Equal(t, http.StatusOK, recorder.Code)
	response := decodeCacheRuntimeResponse(t, recorder)
	require.True(t, response.Success)
	require.True(t, response.Live)
	require.Empty(t, response.Message)
}

func TestGetStatusReturnsNotReadyPayloadAndTriggersOneRetry(t *testing.T) {
	preserveCacheRuntimeHooks(t)
	cacheReadinessError = func() error { return errors.New("database detail must not leak") }
	var retries atomic.Int32
	ensureCachesWarmAsync = func() { retries.Add(1) }

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/api/status", nil)
	GetStatus(context)

	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	response := decodeCacheRuntimeResponse(t, recorder)
	require.False(t, response.Success)
	require.False(t, response.Ready)
	require.Equal(t, "service caches are not ready", response.Message)
	require.NotContains(t, recorder.Body.String(), "database detail")
	require.Equal(t, int32(1), retries.Load())
}

func TestGetStatusRecoversWithoutDroppingExistingData(t *testing.T) {
	preserveCacheRuntimeHooks(t)
	var calls atomic.Int32
	cacheReadinessError = func() error {
		if calls.Add(1) == 1 {
			return errors.New("cold")
		}
		return nil
	}
	ensureCachesWarmAsync = func() {}

	previousSystemName := common.SystemName
	common.SystemName = "readiness-preserved-system"
	t.Cleanup(func() { common.SystemName = previousSystemName })

	coldRecorder := httptest.NewRecorder()
	coldContext, _ := gin.CreateTestContext(coldRecorder)
	coldContext.Request = httptest.NewRequest(http.MethodGet, "/api/status", nil)
	GetStatus(coldContext)
	require.Equal(t, http.StatusServiceUnavailable, coldRecorder.Code)

	readyRecorder := httptest.NewRecorder()
	readyContext, _ := gin.CreateTestContext(readyRecorder)
	readyContext.Request = httptest.NewRequest(http.MethodGet, "/api/status", nil)
	GetStatus(readyContext)

	require.Equal(t, http.StatusOK, readyRecorder.Code)
	response := decodeCacheRuntimeResponse(t, readyRecorder)
	require.True(t, response.Success)
	require.True(t, response.Ready)
	require.Empty(t, response.Message)
	require.Equal(t, "readiness-preserved-system", response.Data["system_name"])
}

func TestGetPricingReturnsServiceUnavailableWithoutSnapshot(t *testing.T) {
	preserveCacheRuntimeHooks(t)
	getPricingCache = func() []model.Pricing { return nil }

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/api/pricing", nil)
	GetPricing(context)

	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	response := decodeCacheRuntimeResponse(t, recorder)
	require.False(t, response.Success)
	require.Equal(t, "pricing cache is not ready", response.Message)
}

func TestPricingAdminMutationsPropagateRefreshFailure(t *testing.T) {
	preserveCacheRuntimeHooks(t)
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Option{}, &model.RatioNotification{}, &model.RatioDelivery{}))
	common.OptionMapRWMutex.Lock()
	previousOptionMap := common.OptionMap
	common.OptionMap = make(map[string]string, len(previousOptionMap))
	for key, value := range previousOptionMap {
		common.OptionMap[key] = value
	}
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previousOptionMap
		common.OptionMapRWMutex.Unlock()
	})
	refreshErr := errors.New("forced pricing refresh failure")
	refreshPricingCache = func() error { return refreshErr }

	previousRatio := ratio_setting.ModelRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(previousRatio))
	})

	tests := []struct {
		name    string
		method  string
		path    string
		body    string
		handler gin.HandlerFunc
	}{
		{
			name:    "model meta create",
			method:  http.MethodPost,
			path:    "/api/models",
			body:    `{"model_name":"zz-refresh-error-model"}`,
			handler: CreateModelMeta,
		},
		{
			name:    "pricing option",
			method:  http.MethodPut,
			path:    "/api/option",
			body:    `{"key":"ModelRatio","value":"{}"}`,
			handler: UpdateOption,
		},
		{
			name:    "pricing reset",
			method:  http.MethodPost,
			path:    "/api/option/rest_model_ratio",
			handler: ResetModelRatio,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(recorder)
			context.Request = httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			context.Request.Header.Set("Content-Type", "application/json")

			test.handler(context)

			require.Equal(t, http.StatusOK, recorder.Code)
			response := decodeCacheRuntimeResponse(t, recorder)
			require.False(t, response.Success)
			require.Contains(t, response.Message, refreshErr.Error())
		})
	}
}
