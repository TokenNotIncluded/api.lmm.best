package controller

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func companyBillingProfileTestRouter(userID int) *gin.Engine {
	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		c.Set("id", userID)
		c.Next()
	})
	engine.GET("/api/user/company-billing-profile", GetCompanyBillingProfile)
	engine.PUT("/api/user/company-billing-profile", PutCompanyBillingProfile)
	return engine
}

func putCompanyBillingProfile(t *testing.T, engine http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPut, "/api/user/company-billing-profile", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	return response
}

func TestCompanyBillingProfileRejectsClientRequiredFieldsRules(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.CompanyBillingProfile{}))
	engine := companyBillingProfileTestRouter(41)

	for _, body := range []string{
		`{"country":"US","isBusiness":true,"useForInvoices":true,"requiredFields":[]}`,
		`{"country":"US","isBusiness":true,"useForInvoices":true,"requiredFields":["state"]}`,
		`{"country":"US","isBusiness":true,"useForInvoices":true,"providerRules":{"requiredFields":[]}}`,
	} {
		response := putCompanyBillingProfile(t, engine, body)
		require.Equal(t, http.StatusBadRequest, response.Code)
		require.NotContains(t, response.Body.String(), "state")
	}
	var count int64
	require.NoError(t, db.Model(&model.CompanyBillingProfile{}).Count(&count).Error)
	require.Zero(t, count)
}

func TestCompanyBillingProfileRequiresCountryAndBooleans(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.CompanyBillingProfile{}))
	response := putCompanyBillingProfile(t, companyBillingProfileTestRouter(41), `{"postcode":"10001"}`)
	require.Equal(t, http.StatusUnprocessableEntity, response.Code)
	var payload struct {
		Errors map[string]string `json:"errors"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
	require.Equal(t, "required", payload.Errors["country"])
	require.Equal(t, "required", payload.Errors["isBusiness"])
	require.Equal(t, "required", payload.Errors["useForInvoices"])
}

func TestCompanyBillingProfileOwnerCanUpsertAndReadSensitiveFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.CompanyBillingProfile{}))
	owner := companyBillingProfileTestRouter(41)

	body := `{"country":" us ","isBusiness":true,"businessName":"Example Company","taxId":"TAX-41","useForInvoices":false}`
	response := putCompanyBillingProfile(t, owner, body)
	require.Equal(t, http.StatusOK, response.Code)
	require.Contains(t, response.Body.String(), `"country":"US"`)

	getRequest := httptest.NewRequest(http.MethodGet, "/api/user/company-billing-profile", nil)
	getResponse := httptest.NewRecorder()
	owner.ServeHTTP(getResponse, getRequest)
	require.Equal(t, http.StatusOK, getResponse.Code)
	require.Contains(t, getResponse.Body.String(), "Example Company")
	require.Contains(t, getResponse.Body.String(), "TAX-41")

	other := companyBillingProfileTestRouter(42)
	otherResponse := httptest.NewRecorder()
	other.ServeHTTP(otherResponse, httptest.NewRequest(http.MethodGet, "/api/user/company-billing-profile", nil))
	require.Equal(t, http.StatusOK, otherResponse.Code)
	require.Equal(t, -1, bytes.Index(otherResponse.Body.Bytes(), []byte("Example Company")))
	require.Equal(t, -1, bytes.Index(otherResponse.Body.Bytes(), []byte("TAX-41")))
	require.Contains(t, otherResponse.Body.String(), `"data":null`)
}

func TestCompanyBillingProfileValidationErrorsDoNotEchoSensitiveValues(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.CompanyBillingProfile{}))
	sensitiveName := strings.Repeat("N", model.CompanyBillingBusinessNameMaxRunes+1)
	body, err := json.Marshal(map[string]any{
		"country": "US", "isBusiness": true, "businessName": sensitiveName,
		"taxId": "sensitive-tax-value", "useForInvoices": true,
	})
	require.NoError(t, err)
	response := putCompanyBillingProfile(t, companyBillingProfileTestRouter(41), string(body))
	require.Equal(t, http.StatusUnprocessableEntity, response.Code)
	require.NotContains(t, response.Body.String(), sensitiveName)
	require.NotContains(t, response.Body.String(), "sensitive-tax-value")
}
