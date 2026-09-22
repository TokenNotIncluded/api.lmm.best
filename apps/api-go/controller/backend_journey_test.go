package controller

import (
	"errors"
	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBackendJourneyRegistrationRollsBackInitialTokenFailureAndCanRetry(t *testing.T) {
	db := setupManageUserTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Token{}))
	setRegistrationGateTestState(t, "terms", "privacy")
	oldDefault, oldAuto, oldQuota := constant.GenerateDefaultToken, setting.DefaultUseAutoGroup, common.QuotaForNewUser
	payment := operation_setting.GetPaymentSetting()
	oldPayment := *payment
	constant.GenerateDefaultToken, setting.DefaultUseAutoGroup, common.QuotaForNewUser = true, true, 123
	payment.ComplianceConfirmed, payment.ComplianceTermsVersion = true, operation_setting.CurrentComplianceTermsVersion
	t.Cleanup(func() {
		constant.GenerateDefaultToken = oldDefault
		setting.DefaultUseAutoGroup = oldAuto
		common.QuotaForNewUser = oldQuota
		*payment = oldPayment
	})
	inviter := model.User{Username: "journey-inviter", Password: "unused", AffCode: "journey-aff", Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&inviter).Error)
	failToken := true
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register("journey:token-failure", func(tx *gorm.DB) {
		if failToken && tx.Statement.Table == "tokens" {
			tx.AddError(errors.New("injected initial token failure"))
		}
	}))
	register := func() *httptest.ResponseRecorder {
		r := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(r)
		c.Request = httptest.NewRequest(http.MethodPost, "/api/user/register", strings.NewReader(`{"username":"journey-new","password":"password123","accepted_legal":true,"aff_code":"journey-aff","role":100,"quota":999999}`))
		Register(c)
		return r
	}
	first := register()
	assert.False(t, decodeRegistrationGateResponse(t, first).Success)
	var count int64
	require.NoError(t, db.Model(&model.User{}).Where("username = ?", "journey-new").Count(&count).Error)
	assert.Zero(t, count, "an unsuccessful registration must not leave a committed account")
	var storedInviter model.User
	require.NoError(t, db.First(&storedInviter, inviter.Id).Error)
	assert.Zero(t, storedInviter.AffCount, "failed registration must not increment inviter counters")
	failToken = false
	second := register()
	require.True(t, decodeRegistrationGateResponse(t, second).Success, second.Body.String())
	var user model.User
	require.NoError(t, db.Where("username = ?", "journey-new").First(&user).Error)
	assert.Equal(t, common.RoleCommonUser, user.Role)
	assert.Equal(t, 123, user.Quota)
	assert.NotEqual(t, "password123", user.Password)
	var tokens []model.Token
	require.NoError(t, db.Where("user_id = ?", user.Id).Find(&tokens).Error)
	require.Len(t, tokens, 1)
	assert.Equal(t, model.TokenCreationSourceSystem, tokens[0].CreationSource)
	assert.Equal(t, "auto", tokens[0].Group)
	assert.False(t, decodeRegistrationGateResponse(t, register()).Success)
	require.NoError(t, db.First(&storedInviter, inviter.Id).Error)
	assert.Equal(t, 1, storedInviter.AffCount)
	require.NoError(t, db.Model(&model.Token{}).Where("user_id = ?", user.Id).Count(&count).Error)
	assert.EqualValues(t, 1, count)
}

func TestBackendJourneyRegistrationWithoutDefaultToken(t *testing.T) {
	db := setupManageUserTestDB(t)
	setRegistrationGateTestState(t, "terms", "privacy")
	old := constant.GenerateDefaultToken
	constant.GenerateDefaultToken = false
	t.Cleanup(func() { constant.GenerateDefaultToken = old })
	r := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(r)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/user/register", strings.NewReader(`{"username":"journey-no-token","password":"password123","accepted_legal":true}`))
	Register(c)
	require.True(t, decodeRegistrationGateResponse(t, r).Success, r.Body.String())
	var user model.User
	require.NoError(t, db.Where("username = ?", "journey-no-token").First(&user).Error)
	require.Positive(t, user.Id)
}

func TestBackendJourneyTokenEditRejectsUnavailableGroupsWithoutBreakingRevocation(t *testing.T) {
	for _, tc := range []struct {
		name, group                           string
		noRatio, revoked, statusOnly, success bool
	}{
		{name: "unlisted group", group: "private"},
		{name: "missing pricing group", group: "vip", noRatio: true},
		{name: "revoked group", group: "vip", revoked: true},
		{name: "available group", group: "vip", success: true},
		{name: "inherit account group", group: "", success: true},
		{name: "disable stale group", group: "private", statusOnly: true, success: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := setupManageUserTestDB(t)
			require.NoError(t, db.AutoMigrate(&model.Token{}))
			configureTokenAutoGroupsTest(t, "5", `["default","vip"]`)
			if tc.noRatio {
				require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1}`))
			}
			if tc.revoked {
				require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default"}`))
			}
			user := model.User{Username: "journey-key-user", Password: "unused", AffCode: "journey-key", Group: "default", Status: common.UserStatusEnabled}
			require.NoError(t, db.Create(&user).Error)
			token := model.Token{UserId: user.Id, Key: "synthetic-journey-key", Name: "before", Group: "default", Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true}
			if tc.statusOnly {
				token.Group = tc.group
			}
			require.NoError(t, db.Create(&token).Error)
			request := baseAutoTokenRequest("after")
			request["id"], request["group"], request["status"] = token.Id, tc.group, common.TokenStatusEnabled
			target := "/api/token/"
			if tc.statusOnly {
				target += "?status_only=true"
				request["status"] = common.TokenStatusDisabled
			}
			c, r := newTokenAutoGroupsAuthenticatedContext(t, http.MethodPut, target, request, user.Id)
			UpdateToken(c)
			assert.Equal(t, tc.success, decodeAPIResponse(t, r).Success, r.Body.String())
			var stored model.Token
			require.NoError(t, db.First(&stored, token.Id).Error)
			if !tc.success {
				assert.Equal(t, token, stored, "failed validation must not persist any partial edit")
			}
			if tc.statusOnly {
				assert.Equal(t, common.TokenStatusDisabled, stored.Status)
			}
		})
	}
}
