package controller

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func merchantStoreControllerRequest(t *testing.T, actor int, body string, handler gin.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	c.Request = httptest.NewRequest("POST", "/api/store/test", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("id", actor)
	handler(c)
	return response
}

func TestMerchantStoreControllerVerificationNeverReturnsCodeOrAcceptsOtherAddress(t *testing.T) {
	db := setupManageUserTestDB(t)
	t.Setenv("MERCHANT_STORE_ENCRYPTION_KEY", "store-controller-fixture-encryption-key-20261006-123456789")
	require.NoError(t, db.AutoMigrate(model.MerchantStoreModels()...))
	user := model.User{Username: "store-email-owner", AffCode: "store-email-owner", Email: "owner@example.test", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&user).Error)
	previous := merchantStoreVerificationSender
	t.Cleanup(func() { merchantStoreVerificationSender = previous })
	var sentCode string
	sends := 0
	merchantStoreVerificationSender = func(ctx context.Context, actor int, email, code string) error {
		require.Equal(t, user.Id, actor)
		require.Equal(t, user.Email, email)
		require.Len(t, code, 6)
		sentCode = code
		sends++
		return nil
	}
	response := merchantStoreControllerRequest(t, user.Id, `{"email":"attacker@example.test","user_id":999}`, SendMerchantStoreEmailVerification)
	require.Equal(t, 200, response.Code, response.Body.String())
	require.JSONEq(t, `{"success":true,"message":"","data":{"sent":true}}`, response.Body.String())
	require.NotContains(t, response.Body.String(), sentCode)
	require.NotContains(t, response.Body.String(), user.Email)
	response = merchantStoreControllerRequest(t, user.Id, `{}`, SendMerchantStoreEmailVerification)
	require.Equal(t, 429, response.Code)
	require.Contains(t, response.Body.String(), "STORE_EMAIL_VERIFICATION_COOLDOWN")
	require.Equal(t, 1, sends)
	response = merchantStoreControllerRequest(t, user.Id, `{"code":"`+sentCode+`"}`, ConfirmMerchantStoreEmailVerification)
	require.Equal(t, 200, response.Code, response.Body.String())
	address, err := model.GetMerchantStoreVerifiedEmailAddress(user.Id)
	require.NoError(t, err)
	require.Equal(t, user.Email, address)
	response = merchantStoreControllerRequest(t, user.Id, `{"code":"`+sentCode+`"}`, ConfirmMerchantStoreEmailVerification)
	require.Equal(t, 422, response.Code)
	require.Contains(t, response.Body.String(), "STORE_EMAIL_VERIFICATION_INVALID")
	require.NotContains(t, response.Body.String(), sentCode)
	require.NoError(t, db.Model(&user).Update("email", "new-owner@example.test").Error)
	response = merchantStoreControllerRequest(t, user.Id, `{}`, GetMerchantStoreEmailStatus)
	require.Equal(t, 200, response.Code, response.Body.String())
	var status struct {
		Data struct {
			Verified bool   `json:"verified"`
			Email    string `json:"email"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &status))
	require.False(t, status.Data.Verified)
	require.Equal(t, "new-owner@example.test", status.Data.Email)
}

func TestMerchantStoreControllerVerificationSanitizesSenderFailures(t *testing.T) {
	db := setupManageUserTestDB(t)
	t.Setenv("MERCHANT_STORE_ENCRYPTION_KEY", "store-controller-fixture-encryption-key-20261006-123456789")
	require.NoError(t, db.AutoMigrate(model.MerchantStoreModels()...))
	user := model.User{Username: "store-email-failure", AffCode: "store-email-failure", Email: "owner@example.test", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&user).Error)
	previous := merchantStoreVerificationSender
	t.Cleanup(func() { merchantStoreVerificationSender = previous })
	merchantStoreVerificationSender = func(context.Context, int, string, string) error {
		return errors.New("SMTP secret-password owner@example.test private-code")
	}
	response := merchantStoreControllerRequest(t, user.Id, `{}`, SendMerchantStoreEmailVerification)
	require.Equal(t, 500, response.Code)
	for _, secret := range []string{"secret-password", "owner@example.test", "private-code", "SMTP"} {
		require.NotContains(t, response.Body.String(), secret)
	}
}

func TestMerchantStoreControllerConfigPatchPreservesFrozenUnitSettings(t *testing.T) {
	db := setupManageUserTestDB(t)
	require.NoError(t, db.AutoMigrate(model.MerchantStoreModels()...))
	root := model.User{Username: "store-config-root", AffCode: "store-config-root", Role: common.RoleRootUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&root).Error)
	initial := model.MerchantStoreConfig{FeeBPS: 100, RecipientID: root.Id, PromotionQuota: 500000, LinuxDOUnitsPerUSD: "2.5"}
	require.NoError(t, model.SetMerchantStoreConfig(root.Id, initial))
	response := merchantStoreControllerRequest(t, root.Id, `{"promotion_quota":750000}`, SetMerchantStoreConfig)
	require.Equal(t, 200, response.Code, response.Body.String())
	current, err := model.GetMerchantStoreConfig()
	require.NoError(t, err)
	require.Equal(t, 100, current.FeeBPS)
	require.Equal(t, root.Id, current.RecipientID)
	require.Equal(t, 750000, current.PromotionQuota)
	require.Equal(t, "2.5", current.LinuxDOUnitsPerUSD)
	response = merchantStoreControllerRequest(t, root.Id, `{"fee_bps":0,"linuxdo_units_per_usd":""}`, SetMerchantStoreConfig)
	require.Equal(t, 200, response.Code, response.Body.String())
	current, err = model.GetMerchantStoreConfig()
	require.NoError(t, err)
	require.Zero(t, current.FeeBPS)
	require.Empty(t, current.LinuxDOUnitsPerUSD)
	require.Equal(t, 750000, current.PromotionQuota)
}
