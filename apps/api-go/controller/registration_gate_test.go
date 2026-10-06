package controller

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/oauth"
	"github.com/LIghtJUNction/api.lmm.best/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type registrationGateTestOAuthProvider struct {
	existing *model.User
}

func (*registrationGateTestOAuthProvider) GetName() string { return "GitHub" }
func (*registrationGateTestOAuthProvider) IsEnabled() bool { return true }
func (*registrationGateTestOAuthProvider) ExchangeToken(context.Context, string, *gin.Context) (*oauth.OAuthToken, error) {
	return &oauth.OAuthToken{}, nil
}
func (*registrationGateTestOAuthProvider) GetUserInfo(context.Context, *oauth.OAuthToken) (*oauth.OAuthUser, error) {
	return &oauth.OAuthUser{ProviderUserID: "registration-gate-subject"}, nil
}
func (provider *registrationGateTestOAuthProvider) IsUserIDTaken(string) bool {
	return provider.existing != nil
}
func (provider *registrationGateTestOAuthProvider) FillUserByProviderID(user *model.User, _ string) error {
	if provider.existing == nil {
		return errors.New("missing test identity")
	}
	*user = *provider.existing
	return nil
}
func (*registrationGateTestOAuthProvider) SetProviderUserID(*model.User, string) {}
func (*registrationGateTestOAuthProvider) GetProviderPrefix() string             { return "gate_" }
func (*registrationGateTestOAuthProvider) ProviderUserIDColumn() string          { return "" }

func setRegistrationGateTestState(t *testing.T, agreement, privacy string) {
	t.Helper()
	settings := system_setting.GetLegalSettings()
	originalSettings := *settings
	originalRegisterEnabled := common.RegisterEnabled
	originalPasswordRegisterEnabled := common.PasswordRegisterEnabled
	originalOAuthRegisterEnabled := common.OAuthRegisterEnabled
	originalEmailVerificationEnabled := common.EmailVerificationEnabled
	settings.UserAgreement = agreement
	settings.PrivacyPolicy = privacy
	common.RegisterEnabled = true
	common.PasswordRegisterEnabled = true
	common.OAuthRegisterEnabled = true
	common.EmailVerificationEnabled = false
	t.Cleanup(func() {
		*settings = originalSettings
		common.RegisterEnabled = originalRegisterEnabled
		common.PasswordRegisterEnabled = originalPasswordRegisterEnabled
		common.OAuthRegisterEnabled = originalOAuthRegisterEnabled
		common.EmailVerificationEnabled = originalEmailVerificationEnabled
	})
}

func decodeRegistrationGateResponse(t *testing.T, recorder *httptest.ResponseRecorder) struct {
	Success bool   `json:"success"`
	Code    string `json:"code"`
	Message string `json:"message"`
} {
	t.Helper()
	var response struct {
		Success bool   `json:"success"`
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	return response
}

func TestRegistrationGateDecision(t *testing.T) {
	tests := []struct {
		name              string
		policiesPublished bool
		acceptedLegal     bool
		wantStatus        int
		wantCode          string
	}{
		{name: "policies missing without consent", wantStatus: http.StatusServiceUnavailable, wantCode: registrationLegalUnavailableCode},
		{name: "policies missing despite consent", acceptedLegal: true, wantStatus: http.StatusServiceUnavailable, wantCode: registrationLegalUnavailableCode},
		{name: "consent missing", policiesPublished: true, wantStatus: http.StatusUnprocessableEntity, wantCode: legalConsentRequiredCode},
		{name: "published and accepted", policiesPublished: true, acceptedLegal: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := registrationGateFailure(test.policiesPublished, test.acceptedLegal)
			if test.wantStatus == 0 {
				// The helper returns a typed nil pointer on success; passing it to
				// require.NoError would box it into a non-nil error interface.
				require.Nil(t, err)
				return
			}
			require.Error(t, err)
			assert.Equal(t, test.wantStatus, err.Status)
			assert.Equal(t, test.wantCode, err.Code)
			assert.NotEmpty(t, err.Message)
		})
	}
}

func TestPasswordRegistrationFailsClosedBeforeUserCreation(t *testing.T) {
	tests := []struct {
		name       string
		agreement  string
		privacy    string
		accepted   bool
		wantStatus int
		wantCode   string
	}{
		{name: "operator policies unavailable", accepted: true, wantStatus: http.StatusServiceUnavailable, wantCode: registrationLegalUnavailableCode},
		{name: "consent missing", agreement: "terms", privacy: "privacy", wantStatus: http.StatusUnprocessableEntity, wantCode: legalConsentRequiredCode},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := setupManageUserTestDB(t)
			setRegistrationGateTestState(t, test.agreement, test.privacy)
			body := `{"username":"new-user","password":"password1","accepted_legal":` +
				map[bool]string{true: "true", false: "false"}[test.accepted] + `}`
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/api/user/register", strings.NewReader(body))

			Register(c)

			assert.Equal(t, test.wantStatus, recorder.Code)
			response := decodeRegistrationGateResponse(t, recorder)
			assert.False(t, response.Success)
			assert.Equal(t, test.wantCode, response.Code)
			assert.Empty(t, recorder.Header().Get("Retry-After"))
			var count int64
			require.NoError(t, db.Model(&model.User{}).Count(&count).Error)
			assert.Zero(t, count)
		})
	}
}

func TestPasswordRegistrationMarksLinuxDOEmailAccount(t *testing.T) {
	db := setupManageUserTestDB(t)
	setRegistrationGateTestState(t, "terms", "privacy")
	common.EmailVerificationEnabled = true
	email := "member@linux.do"
	code := "123456"
	common.RegisterVerificationCodeWithKey(email, code, common.EmailVerificationPurpose)
	t.Cleanup(func() { common.DeleteKey(email, common.EmailVerificationPurpose) })

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/user/register", strings.NewReader(
		`{"username":"linuxdo-email-user","password":"password1","email":"member@linux.do","verification_code":"123456","accepted_legal":true}`,
	))
	Register(c)

	assert.Equal(t, http.StatusOK, recorder.Code)
	var user model.User
	require.NoError(t, db.Where("username = ?", "linuxdo-email-user").First(&user).Error)
	assert.Equal(t, model.PaymentRestrictionLinuxDOEmail, user.PaymentRestrictionFlags)
}

func TestOAuthFirstCreateUsesStateBoundConsentAndExistingLoginBypassesGate(t *testing.T) {
	setupAuthFlowControllerTest(t)
	setRegistrationGateTestState(t, "terms", "privacy")
	provider := &registrationGateTestOAuthProvider{}
	oauthUser := &oauth.OAuthUser{ProviderUserID: "new-subject"}

	user, err := findOrCreateOAuthUser(nil, provider, oauthUser, "", false)
	require.Nil(t, user)
	var gateErr *registrationGateError
	require.ErrorAs(t, err, &gateErr)
	assert.Equal(t, legalConsentRequiredCode, gateErr.Code)
	var count int64
	require.NoError(t, model.DB.Model(&model.User{}).Count(&count).Error)
	assert.Zero(t, count)

	settings := system_setting.GetLegalSettings()
	settings.UserAgreement = ""
	settings.PrivacyPolicy = ""
	provider.existing = &model.User{Id: 41, Username: "existing-oauth", Status: common.UserStatusEnabled}
	user, err = findOrCreateOAuthUser(nil, provider, oauthUser, "", false)
	require.NoError(t, err)
	require.NotNil(t, user)
	assert.Equal(t, 41, user.Id)
}

func TestOAuthRegistrationRestrictionBlocksNewAccountsButAllowsExistingLogin(t *testing.T) {
	setupAuthFlowControllerTest(t)
	setRegistrationGateTestState(t, "", "")

	common.OptionMapRWMutex.Lock()
	previous := ""
	wasInitialized := common.OptionMap != nil
	if common.OptionMap != nil {
		previous = common.OptionMap[common.RegistrationDisabledMethodsOptionKey]
	} else {
		common.OptionMap = make(map[string]string)
	}
	common.OptionMap[common.RegistrationDisabledMethodsOptionKey] = "github"
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		if wasInitialized {
			common.OptionMap[common.RegistrationDisabledMethodsOptionKey] = previous
		} else {
			common.OptionMap = nil
		}
		common.OptionMapRWMutex.Unlock()
	})

	provider := &registrationGateTestOAuthProvider{}
	oauthUser := &oauth.OAuthUser{ProviderUserID: "restricted-subject"}
	user, err := findOrCreateOAuthUser(nil, provider, oauthUser, "", true)
	var disabledErr *OAuthRegistrationDisabledError
	require.ErrorAs(t, err, &disabledErr)
	assert.Nil(t, user)

	provider.existing = &model.User{Id: 42, Username: "existing-github", Status: common.UserStatusEnabled}
	user, err = findOrCreateOAuthUser(nil, provider, oauthUser, "", false)
	require.NoError(t, err)
	require.NotNil(t, user)
	assert.Equal(t, 42, user.Id)

	common.OptionMapRWMutex.Lock()
	common.OptionMap[common.RegistrationDisabledMethodsOptionKey] = ""
	common.OptionMapRWMutex.Unlock()
	common.OAuthRegisterEnabled = false
	provider.existing = nil
	user, err = findOrCreateOAuthUser(nil, provider, &oauth.OAuthUser{ProviderUserID: "global-switch-subject"}, "", true)
	require.ErrorAs(t, err, &disabledErr)
	assert.Nil(t, user)

	provider.existing = &model.User{Id: 45, Username: "existing-global-switch", Status: common.UserStatusEnabled}
	user, err = findOrCreateOAuthUser(nil, provider, &oauth.OAuthUser{ProviderUserID: "global-switch-subject"}, "", false)
	require.NoError(t, err)
	require.NotNil(t, user)
	assert.Equal(t, 45, user.Id)
}

func TestOAuthEmailVerificationBlocksUnattestedNewAccountsButNotExistingLogin(t *testing.T) {
	setupAuthFlowControllerTest(t)
	setRegistrationGateTestState(t, "terms", "privacy")
	common.EmailVerificationEnabled = true
	provider := &registrationGateTestOAuthProvider{}
	oauthUser := &oauth.OAuthUser{
		ProviderUserID: "email-gate-new",
		Email:          "new@example.com",
	}

	user, err := findOrCreateOAuthUser(nil, provider, oauthUser, "", true)
	var verificationErr *OAuthEmailVerificationRequiredError
	require.ErrorAs(t, err, &verificationErr)
	assert.Nil(t, user)
	var count int64
	require.NoError(t, model.DB.Model(&model.User{}).Count(&count).Error)
	assert.Zero(t, count)

	oauthUser.EmailVerified = true
	user, err = findOrCreateOAuthUser(nil, provider, oauthUser, "", true)
	require.NoError(t, err)
	require.NotNil(t, user)
	assert.Equal(t, "new@example.com", user.Email)

	provider.existing = &model.User{Id: 43, Username: "existing-oauth", Status: common.UserStatusEnabled}
	existing, err := findOrCreateOAuthUser(nil, provider, &oauth.OAuthUser{ProviderUserID: "existing-email-gate"}, "", false)
	require.NoError(t, err)
	assert.Equal(t, 43, existing.Id)
}

func TestOAuthCallbackCannotSubstituteConsentForState(t *testing.T) {
	provider := setupAuthFlowControllerTest(t)
	setRegistrationGateTestState(t, "terms", "privacy")
	common.RegisterEnabled = true
	token, _, err := model.CreateAuthFlow(model.AuthFlowCreate{
		Purpose: model.AuthFlowPurposeOAuth, Provider: "auth-flow-test", Intent: model.AuthFlowIntentLogin,
		Payload: `{"accepted_legal":false}`, ExpiresAt: time.Now().Add(time.Minute),
	})
	require.NoError(t, err)
	router := gin.New()
	router.GET("/api/oauth/:provider", HandleOAuth)
	request := newAuthFlowOAuthCallbackRequest(t, token, "&code=test&accepted_legal=true")
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	assert.Equal(t, http.StatusUnprocessableEntity, response.Code)
	assert.Equal(t, legalConsentRequiredCode, decodeRegistrationGateResponse(t, response).Code)
	assert.Equal(t, 1, provider.exchangeCalls)
	assert.Equal(t, 1, provider.userInfoCalls)
	var count int64
	require.NoError(t, model.DB.Model(&model.User{}).Count(&count).Error)
	assert.Zero(t, count)
}

func TestWeChatFirstCreateGatesWhileExistingIdentityLoginBypasses(t *testing.T) {
	db := setupManageUserTestDB(t)
	setRegistrationGateTestState(t, "", "")
	common.OAuthRegisterEnabled = false
	existing := model.User{
		Username: "existing-wechat", Password: "password", WeChatId: "wechat-existing",
		Role: common.RoleCommonUser, Status: common.UserStatusEnabled,
	}
	require.NoError(t, db.Create(&existing).Error)

	existingRecorder := httptest.NewRecorder()
	existingContext, _ := gin.CreateTestContext(existingRecorder)
	user, ok := findOrCreateWeChatUser(existingContext, existing.WeChatId, "", false)
	require.True(t, ok)
	require.NotNil(t, user)
	assert.Equal(t, existing.Id, user.Id)
	assert.Equal(t, http.StatusOK, existingRecorder.Code)
	assert.Empty(t, existingRecorder.Body.String())

	blockedRecorder := httptest.NewRecorder()
	blockedContext, _ := gin.CreateTestContext(blockedRecorder)
	user, ok = findOrCreateWeChatUser(blockedContext, "wechat-oauth-disabled", "", true)
	assert.False(t, ok)
	assert.Nil(t, user)
	assert.Equal(t, http.StatusOK, blockedRecorder.Code)
	assert.Contains(t, blockedRecorder.Body.String(), "OAuth")

	common.OAuthRegisterEnabled = true

	newRecorder := httptest.NewRecorder()
	newContext, _ := gin.CreateTestContext(newRecorder)
	user, ok = findOrCreateWeChatUser(newContext, "wechat-new", "", true)
	assert.False(t, ok)
	assert.Nil(t, user)
	assert.Equal(t, http.StatusServiceUnavailable, newRecorder.Code)
	assert.Equal(t, registrationLegalUnavailableCode, decodeRegistrationGateResponse(t, newRecorder).Code)
	var count int64
	require.NoError(t, db.Model(&model.User{}).Count(&count).Error)
	assert.EqualValues(t, 1, count)
}

func TestWeChatEmailVerificationBlocksOnlyNewAccounts(t *testing.T) {
	db := setupManageUserTestDB(t)
	setRegistrationGateTestState(t, "terms", "privacy")
	common.EmailVerificationEnabled = true
	existing := model.User{
		Username: "existing-wechat-email", Password: "password", WeChatId: "wechat-email-existing",
		Role: common.RoleCommonUser, Status: common.UserStatusEnabled,
	}
	require.NoError(t, db.Create(&existing).Error)

	existingRecorder := httptest.NewRecorder()
	existingContext, _ := gin.CreateTestContext(existingRecorder)
	user, ok := findOrCreateWeChatUser(existingContext, existing.WeChatId, "", false)
	require.True(t, ok)
	require.NotNil(t, user)
	assert.Equal(t, existing.Id, user.Id)

	newRecorder := httptest.NewRecorder()
	newContext, _ := gin.CreateTestContext(newRecorder)
	newContext.Request = httptest.NewRequest(http.MethodGet, "/api/wechat", nil)
	user, ok = findOrCreateWeChatUser(newContext, "wechat-email-new", "", true)
	assert.False(t, ok)
	assert.Nil(t, user)
	assert.Equal(t, http.StatusOK, newRecorder.Code)
	assert.Contains(t, newRecorder.Body.String(), "email")
	var count int64
	require.NoError(t, db.Model(&model.User{}).Count(&count).Error)
	assert.EqualValues(t, 1, count)
}
