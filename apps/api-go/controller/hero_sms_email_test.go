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
	t.Cleanup(func() { model.DB = previousDB })
	require.NoError(t, db.AutoMigrate(&model.Option{}))
	oldMap := common.OptionMap
	common.OptionMap = map[string]string{}
	model.InitOptionMap()
	t.Cleanup(func() { common.OptionMap = oldMap })
	oldEnabled := setting.HeroSMSEnabled
	oldKey := setting.HeroSMSAPIKey
	oldMultiplier := setting.HeroSMSPriceMultiplierValue
	t.Cleanup(func() {
		setting.HeroSMSEnabled = oldEnabled
		setting.HeroSMSAPIKey = oldKey
		setting.HeroSMSPriceMultiplierValue = oldMultiplier
	})
	oldEnv, hadEnv := os.LookupEnv("HERO_SMS_ENCRYPTION_KEY")
	require.NoError(t, os.Setenv("HERO_SMS_ENCRYPTION_KEY", "controller-hero-sms-encryption-key"))
	t.Cleanup(func() {
		if hadEnv {
			_ = os.Setenv("HERO_SMS_ENCRYPTION_KEY", oldEnv)
		} else {
			_ = os.Unsetenv("HERO_SMS_ENCRYPTION_KEY")
		}
	})
	return db
}

func TestHeroSMSOptionEndpointsHideSecretAndRetainAPIKey(t *testing.T) {
	db := setupHeroSMSControllerTestDB(t)
	engine := gin.New()
	engine.PUT("/option/hero-sms", PutHeroSMSOptions)
	engine.GET("/option/hero-sms", GetHeroSMSOptions)
	engine.GET("/option", GetOptions)
	engine.DELETE("/option/hero-sms/key", DeleteHeroSMSOptionKey)

	request := httptest.NewRequest(http.MethodPut, "/option/hero-sms", strings.NewReader(`{"enabled":true,"api_key":"secret-key","price_multiplier":"11"}`))
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
	require.Equal(t, "secret-key", setting.HeroSMSAPIKey)

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
	require.Empty(t, setting.HeroSMSAPIKey)
}

func TestHeroSMSGenericOptionWriteRejectsAPIKey(t *testing.T) {
	setupHeroSMSControllerTestDB(t)
	engine := gin.New()
	engine.PUT("/option", UpdateOption)

	request := httptest.NewRequest(http.MethodPut, "/option", strings.NewReader(`{"key":"hero_sms.api_key","value":"nope"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code)
	var envelope map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
	require.Equal(t, false, envelope["success"])
	require.Contains(t, envelope["message"], "hero_sms.api_key")
}
