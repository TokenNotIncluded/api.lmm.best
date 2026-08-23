package controller

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service/herosms"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupHeroSMSControllerTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	previousDB := model.DB
	model.DB = db
	oldRedisEnabled := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() {
		model.DB = previousDB
		common.RedisEnabled = oldRedisEnabled
	})
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Option{}, &model.HeroSMSEmailOrder{}, &model.HeroSMSEmailActivation{}, &model.HeroSMSEmailQuotaLedger{}, &model.HeroSMSSMSOrder{}, &model.HeroSMSSMSQuotaLedger{}, &model.HeroSMSProviderPurchaseLease{}))
	oldMap := common.OptionMap
	common.OptionMap = map[string]string{}
	model.InitOptionMap()
	t.Cleanup(func() { common.OptionMap = oldMap })
	oldEnv, hadEnv := os.LookupEnv("HERO_SMS_ENCRYPTION_KEY")
	require.NoError(t, os.Setenv("HERO_SMS_ENCRYPTION_KEY", "controller-hero-sms-encryption-key"))
	t.Cleanup(func() {
		if hadEnv {
			require.NoError(t, os.Setenv("HERO_SMS_ENCRYPTION_KEY", oldEnv))
		} else {
			require.NoError(t, os.Unsetenv("HERO_SMS_ENCRYPTION_KEY"))
		}
	})
	return db
}

func testHeroSMSOptionEndpointsHideSecretAndRetainAPIKey(t *testing.T) {
	db := setupHeroSMSControllerTestDB(t)
	engine := gin.New()
	engine.PUT("/option/hero-sms", PutHeroSMSOptions)
	engine.GET("/option/hero-sms", GetHeroSMSOptions)
	engine.GET("/option", GetOptions)
	engine.DELETE("/option/hero-sms/key", DeleteHeroSMSOptionKey)

	request := httptest.NewRequest(http.MethodPut, "/option/hero-sms", strings.NewReader(`{"enabled":true,"email_enabled":true,"sms_enabled":true,"api_key":"test-secret-key-12345","price_multiplier":"11"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code)
	require.NotContains(t, response.Body.String(), "secret-key")

	request = httptest.NewRequest(http.MethodGet, "/option/hero-sms", nil)
	response = httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code)
	require.NotContains(t, response.Body.String(), "secret-key")
	require.Contains(t, response.Body.String(), `"api_key_configured":true`)
	require.Contains(t, response.Body.String(), `"email_enabled":true`)
	require.Contains(t, response.Body.String(), `"sms_enabled":true`)

	request = httptest.NewRequest(http.MethodGet, "/option", nil)
	response = httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code)
	require.NotContains(t, response.Body.String(), setting.HeroSMSOptionAPIKey)
	require.NotContains(t, response.Body.String(), "secret-key")

	var option model.Option
	require.NoError(t, db.Where("key = ?", setting.HeroSMSOptionAPIKey).First(&option).Error)
	require.NotEqual(t, "secret-key", option.Value)
	require.True(t, strings.HasPrefix(option.Value, "v1:"))

	request = httptest.NewRequest(http.MethodPut, "/option/hero-sms", strings.NewReader(`{"enabled":true}`))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code)
	require.Contains(t, response.Body.String(), `"api_key_configured":true`)

	request = httptest.NewRequest(http.MethodDelete, "/option/hero-sms/key", nil)
	response = httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	require.Equal(t, http.StatusConflict, response.Code)

	request = httptest.NewRequest(http.MethodPut, "/option/hero-sms", strings.NewReader(`{"enabled":false}`))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code)

	request = httptest.NewRequest(http.MethodDelete, "/option/hero-sms/key", nil)
	response = httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code)
	request = httptest.NewRequest(http.MethodGet, "/option/hero-sms", nil)
	response = httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code)
	require.Contains(t, response.Body.String(), `"api_key_configured":false`)
}

func testHeroSMSOptionsAcceptsUnpersistedCandidateKey(t *testing.T) {
	setupHeroSMSControllerTestDB(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		require.Equal(t, "candidate-secret-key-12345", request.Header.Get("ApiKey"))
		require.NoError(t, json.NewEncoder(writer).Encode(map[string]any{"data": []any{}}))
	}))
	defer server.Close()
	restore := model.SetHeroSMSClientFactoryForTest(func(_ string, apiKey string) herosms.Client {
		return herosms.NewClient(server.URL, apiKey)
	}, server.URL)
	defer restore()

	engine := gin.New()
	engine.POST("/option/hero-sms/test", CheckHeroSMSOptions)
	request := httptest.NewRequest(http.MethodPost, "/option/hero-sms/test", strings.NewReader(`{"api_key":"candidate-secret-key-12345"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code)
	require.NotContains(t, response.Body.String(), "candidate-secret-key-12345")
	require.Contains(t, response.Body.String(), `"success":true`)
}

func testHeroSMSUserEndpointsUseStableSafeEnvelope(t *testing.T) {
	db := setupHeroSMSControllerTestDB(t)
	user := model.User{Id: 401, Username: "hero-controller-user", Password: "password123", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Quota: 1_000_000, Group: "default", AffCode: "hero-controller-aff"}
	require.NoError(t, db.Create(&user).Error)
	require.NoError(t, model.UpdateHeroSMSSettings(model.HeroSMSSettingsUpdate{Enabled: ptrBoolForController(true), APIKey: "controller-secret-key-12345"}))
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.Method + " " + request.URL.Path {
		case http.MethodGet + " /emails/domains":
			require.NoError(t, json.NewEncoder(writer).Encode(map[string]any{"data": []map[string]any{{"name": "mail.test", "cost": 0.1, "count": 2}}}))
		case http.MethodGet + " /emails":
			require.NoError(t, json.NewEncoder(writer).Encode(map[string]any{"data": []any{}}))
		case http.MethodPost + " /emails":
			writer.WriteHeader(http.StatusCreated)
			require.NoError(t, json.NewEncoder(writer).Encode(map[string]any{"status": true, "data": map[string]any{"id": 901, "site": "demo.com", "email": "user@mail.test", "status": 3, "cost": 0.1, "currency": 840}}))
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	restore := model.SetHeroSMSClientFactoryForTest(func(_ string, apiKey string) herosms.Client { return herosms.NewClient(server.URL, apiKey) }, server.URL)
	defer restore()

	engine := gin.New()
	engine.Use(func(c *gin.Context) { c.Set("id", user.Id); c.Next() })
	engine.GET("/hero-sms/email/products", ListHeroSMSEmailProducts)
	engine.POST("/hero-sms/email/activations", CreateHeroSMSEmailActivations)

	productsResponse := httptest.NewRecorder()
	engine.ServeHTTP(productsResponse, httptest.NewRequest(http.MethodGet, "/hero-sms/email/products?site=demo.com&page=1&size=10", nil))
	require.Equal(t, http.StatusOK, productsResponse.Code)
	var productsEnvelope struct {
		Success bool `json:"success"`
		Data    struct {
			Items []struct {
				ID string `json:"id"`
			} `json:"items"`
			CurrencyCode int `json:"currency_code"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(productsResponse.Body.Bytes(), &productsEnvelope))
	require.True(t, productsEnvelope.Success)
	require.Equal(t, 840, productsEnvelope.Data.CurrencyCode)
	require.Len(t, productsEnvelope.Data.Items, 1)
	require.NotEmpty(t, productsEnvelope.Data.Items[0].ID)
	require.NotContains(t, productsResponse.Body.String(), "controller-secret-key-12345")

	purchaseBody := fmt.Sprintf(`{"domain_id":%q,"quantity":1}`, productsEnvelope.Data.Items[0].ID)
	purchaseRequest := httptest.NewRequest(http.MethodPost, "/hero-sms/email/activations", strings.NewReader(purchaseBody))
	purchaseRequest.Header.Set("Content-Type", "application/json")
	purchaseRequest.Header.Set("Idempotency-Key", "controller-idem")
	purchaseResponse := httptest.NewRecorder()
	engine.ServeHTTP(purchaseResponse, purchaseRequest)
	require.Equal(t, http.StatusCreated, purchaseResponse.Code)
	var purchaseEnvelope struct {
		Success bool `json:"success"`
		Data    struct {
			Order struct {
				ID string `json:"id"`
			} `json:"order"`
			Activations []struct {
				ID    string `json:"id"`
				Email string `json:"email"`
			} `json:"activations"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(purchaseResponse.Body.Bytes(), &purchaseEnvelope))
	require.True(t, purchaseEnvelope.Success)
	require.NotEmpty(t, purchaseEnvelope.Data.Order.ID)
	require.Len(t, purchaseEnvelope.Data.Activations, 1)
	require.Equal(t, "user@mail.test", purchaseEnvelope.Data.Activations[0].Email)
	require.NotContains(t, purchaseResponse.Body.String(), "provider_id")
}

func ptrBoolForController(value bool) *bool { return &value }

func testHeroSMSGenericOptionWriteRejectsAllProtectedKeys(t *testing.T) {
	setupHeroSMSControllerTestDB(t)
	engine := gin.New()
	engine.PUT("/option", UpdateOption)
	engine.POST("/options", UpdateOptionsBulk)

	for _, key := range []string{"hero_sms.api_key", "hero_sms.enabled", "hero_sms.email_enabled", "hero_sms.sms_enabled", "hero_sms.price_multiplier"} {
		request := httptest.NewRequest(http.MethodPut, "/option", strings.NewReader(fmt.Sprintf(`{"key":%q,"value":"nope"}`, key)))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		require.Equal(t, http.StatusOK, response.Code)
		envelope := make(map[string]any)
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
		require.Equal(t, false, envelope["success"])
		require.Contains(t, envelope["message"], "HeroSMS settings")
	}

	bulkRequest := httptest.NewRequest(http.MethodPost, "/options", strings.NewReader(`{"values":{"hero_sms.enabled":"true"}}`))
	bulkRequest.Header.Set("Content-Type", "application/json")
	bulkResponse := httptest.NewRecorder()
	engine.ServeHTTP(bulkResponse, bulkRequest)
	require.Equal(t, http.StatusOK, bulkResponse.Code)
	require.Contains(t, bulkResponse.Body.String(), "HeroSMS settings")
}

// pi-lens-ignore: ast-grep:go-test-functions
func TestHeroSMSHTTPContract(t *testing.T) {
	tests := []struct {
		name string
		run  func(*testing.T)
	}{
		{name: "HeroSMSOptionEndpointsHideSecretAndRetainAPIKey", run: testHeroSMSOptionEndpointsHideSecretAndRetainAPIKey},
		{name: "HeroSMSOptionsAcceptsUnpersistedCandidateKey", run: testHeroSMSOptionsAcceptsUnpersistedCandidateKey},
		{name: "HeroSMSUserEndpointsUseStableSafeEnvelope", run: testHeroSMSUserEndpointsUseStableSafeEnvelope},
		{name: "HeroSMSGenericOptionWriteRejectsAllProtectedKeys", run: testHeroSMSGenericOptionWriteRejectsAllProtectedKeys},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, testCase.run)
	}
}
