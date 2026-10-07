package controller

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/i18n"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/oauth"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupInviteRegistrationTest(t *testing.T) *gorm.DB {
	t.Helper()
	db := setupManageUserTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Token{}, &model.UserOAuthBinding{}, &model.AuthFlow{}))
	persistCreditDenominationFixture(t, db)
	require.NoError(t, i18n.Init())
	setRegistrationGateTestState(t, "terms", "privacy")
	settings := operation_setting.GetDeveloperAccessSetting()
	oldSettings := *settings
	settings.PaidActivationEnabled = false
	oldDefaultToken, oldNewUserQuota := constant.GenerateDefaultToken, common.QuotaForNewUser
	oldLocalAcceptance := model.LocalAcceptanceDeveloperAccessEnabled()
	constant.GenerateDefaultToken = false
	common.QuotaForNewUser = 0
	model.SetLocalAcceptanceDeveloperAccess(false)
	common.OptionMapRWMutex.Lock()
	wasNil := common.OptionMap == nil
	if wasNil {
		common.OptionMap = make(map[string]string)
	}
	oldInvite, hadInvite := common.OptionMap[operation_setting.InviteRegistrationEnabledOptionKey]
	oldDisabled, hadDisabled := common.OptionMap[common.RegistrationDisabledMethodsOptionKey]
	common.OptionMap[common.RegistrationDisabledMethodsOptionKey] = ""
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		*settings = oldSettings
		constant.GenerateDefaultToken, common.QuotaForNewUser = oldDefaultToken, oldNewUserQuota
		model.SetLocalAcceptanceDeveloperAccess(oldLocalAcceptance)
		common.OptionMapRWMutex.Lock()
		defer common.OptionMapRWMutex.Unlock()
		if wasNil {
			common.OptionMap = nil
			return
		}
		if hadInvite {
			common.OptionMap[operation_setting.InviteRegistrationEnabledOptionKey] = oldInvite
		} else {
			delete(common.OptionMap, operation_setting.InviteRegistrationEnabledOptionKey)
		}
		if hadDisabled {
			common.OptionMap[common.RegistrationDisabledMethodsOptionKey] = oldDisabled
		} else {
			delete(common.OptionMap, common.RegistrationDisabledMethodsOptionKey)
		}
	})
	return db
}

func setInviteRegistrationEnabledForTest(enabled bool) {
	operation_setting.GetDeveloperAccessSetting().InviteRegistrationEnabled = enabled
	common.OptionMapRWMutex.Lock()
	common.OptionMap[operation_setting.InviteRegistrationEnabledOptionKey] = fmt.Sprint(enabled)
	common.OptionMapRWMutex.Unlock()
}

type inviteRegistrationTestCase struct {
	name    string
	enabled bool
	inviter string
	wantL1  bool
}

var inviteRegistrationTestCases = []inviteRegistrationTestCase{
	{name: "enabled valid inviter", enabled: true, inviter: "valid", wantL1: true},
	{name: "disabled valid inviter", inviter: "valid"},
	{name: "enabled no affiliate", enabled: true},
	{name: "enabled unknown affiliate", enabled: true, inviter: "unknown"},
	{name: "enabled disabled inviter", enabled: true, inviter: "disabled"},
	{name: "enabled deleted inviter", enabled: true, inviter: "deleted"},
}

func seedInviteRegistrationInviter(t *testing.T, db *gorm.DB, kind string) (string, int) {
	t.Helper()
	if kind == "" {
		return "", 0
	}
	if kind == "unknown" {
		return "missing-affiliate", 0
	}
	inviter := model.User{Username: "inviter", AffCode: "invite-affiliate", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	if kind == "disabled" {
		inviter.Status = common.UserStatusDisabled
	}
	require.NoError(t, db.Create(&inviter).Error)
	if kind == "deleted" {
		require.NoError(t, db.Delete(&inviter).Error)
	}
	if kind != "valid" {
		return inviter.AffCode, 0
	}
	return inviter.AffCode, inviter.Id
}

func assertInviteRegistrationAccess(t *testing.T, db *gorm.DB, user *model.User, inviterID int, wantL1 bool) {
	t.Helper()
	var stored model.User
	require.NoError(t, db.First(&stored, user.Id).Error)
	assert.Equal(t, inviterID, stored.InviterId)
	assert.Nil(t, stored.TrustLevelOverride)
	assert.Equal(t, common.RoleCommonUser, stored.Role)
	assert.Equal(t, common.UserStatusEnabled, stored.Status)
	if wantL1 {
		assert.Positive(t, stored.ConsoleActivatedAt)
		assert.LessOrEqual(t, stored.ConsoleActivatedAt, common.GetTimestamp())
	} else {
		assert.Zero(t, stored.ConsoleActivatedAt)
	}
	snapshot, err := model.GetFreshUserAccessSnapshot(&stored)
	require.NoError(t, err)
	wantLevel := model.TrustLevelMinUser
	if wantL1 {
		wantLevel++
	}
	assert.Equal(t, wantLevel, snapshot.TrustLevel.Level)
	assert.Equal(t, wantL1, snapshot.DeveloperAccess.Granted)
	assert.False(t, snapshot.PaidActivationComplete)
	data := buildSelfUserData(&stored)
	onboarding := data["onboarding"].(gin.H)
	assert.Equal(t, true, onboarding["details_available"])
	assert.Equal(t, wantL1, onboarding["activation_complete"])
	assert.Equal(t, false, onboarding["paid_activation_complete"])
	assert.Equal(t, wantL1, data["developer_access_granted"])
	assert.Equal(t, wantL1, data["permissions"].(map[string]interface{})["docs_access"])
	if constant.GenerateDefaultToken && wantL1 {
		assert.Equal(t, "first_request", onboarding["stage"])
	} else if wantL1 {
		assert.Equal(t, "credential", onboarding["stage"])
	} else {
		assert.Equal(t, "activate", onboarding["stage"])
	}
	var topUpCount int64
	require.NoError(t, db.Model(&model.TopUp{}).Where("user_id = ?", user.Id).Count(&topUpCount).Error)
	assert.Zero(t, topUpCount, "invitation activation must not fabricate a payment")
}

func TestInviteRegistrationPasswordUsesValidatedInviter(t *testing.T) {
	for _, test := range inviteRegistrationTestCases {
		t.Run(test.name, func(t *testing.T) {
			db := setupInviteRegistrationTest(t)
			setInviteRegistrationEnabledForTest(test.enabled)
			aff, inviterID := seedInviteRegistrationInviter(t, db, test.inviter)
			body, err := common.Marshal(map[string]interface{}{
				"username": "password-invitee", "password": "password1", "aff_code": aff, "accepted_legal": true,
				"console_activated_at": 123, "trust_level_override": 4, "role": common.RoleRootUser, "inviter_id": 999,
			})
			require.NoError(t, err)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/api/user/register", strings.NewReader(string(body)))
			Register(c)
			require.Equal(t, http.StatusOK, recorder.Code)
			require.True(t, decodeRegistrationGateResponse(t, recorder).Success, recorder.Body.String())
			var user model.User
			require.NoError(t, db.Where("username = ?", "password-invitee").First(&user).Error)
			assertInviteRegistrationAccess(t, db, &user, inviterID, test.wantL1)
			if inviterID != 0 {
				var inviter model.User
				require.NoError(t, db.First(&inviter, inviterID).Error)
				assert.Equal(t, 1, inviter.AffCount)
			}
		})
	}
}

func TestInviteRegistrationOAuthFirstCreationAndExistingLogin(t *testing.T) {
	for _, providerName := range []string{"builtin OIDC", "generic OAuth"} {
		t.Run(providerName, func(t *testing.T) {
			for _, test := range inviteRegistrationTestCases {
				t.Run(test.name, func(t *testing.T) {
					db := setupInviteRegistrationTest(t)
					setInviteRegistrationEnabledForTest(test.enabled)
					aff, inviterID := seedInviteRegistrationInviter(t, db, test.inviter)
					var provider oauth.Provider = &oauth.OIDCProvider{}
					if providerName == "generic OAuth" {
						provider = oauth.NewGenericOAuthProvider(&model.CustomOAuthProvider{Id: 17, Name: "Invite test", Slug: "invite-test", Enabled: true})
					}
					identity := &oauth.OAuthUser{ProviderUserID: "invite-subject", Username: "oauth-invitee"}
					user, err := findOrCreateOAuthUser(nil, provider, identity, aff, true)
					require.NoError(t, err)
					require.NotNil(t, user)
					assertInviteRegistrationAccess(t, db, user, inviterID, test.wantL1)
					if providerName == "generic OAuth" {
						binding, err := model.GetUserOAuthBinding(user.Id, 17)
						require.NoError(t, err)
						assert.Equal(t, identity.ProviderUserID, binding.ProviderUserId)
					} else {
						assert.Equal(t, identity.ProviderUserID, user.OidcId)
					}
					beforeActivation := user.ConsoleActivatedAt
					setInviteRegistrationEnabledForTest(!test.enabled)
					common.RegisterEnabled, common.OAuthRegisterEnabled = false, false
					relogged, err := findOrCreateOAuthUser(nil, provider, identity, aff, false)
					require.NoError(t, err)
					assert.Equal(t, user.Id, relogged.Id)
					assert.Equal(t, beforeActivation, relogged.ConsoleActivatedAt)
					assertInviteRegistrationAccess(t, db, relogged, inviterID, test.wantL1)
					var count int64
					require.NoError(t, db.Model(&model.User{}).Where("username = ?", "oauth-invitee").Count(&count).Error)
					assert.EqualValues(t, 1, count)
				})
			}
		})
	}
}

func TestInviteRegistrationWeChatFirstCreationAndExistingLogin(t *testing.T) {
	for _, test := range inviteRegistrationTestCases {
		t.Run(test.name, func(t *testing.T) {
			db := setupInviteRegistrationTest(t)
			setInviteRegistrationEnabledForTest(test.enabled)
			aff, inviterID := seedInviteRegistrationInviter(t, db, test.inviter)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			user, ok := findOrCreateWeChatUser(c, "invite-wechat-subject", aff, true)
			require.True(t, ok, recorder.Body.String())
			require.NotNil(t, user)
			assertInviteRegistrationAccess(t, db, user, inviterID, test.wantL1)
			beforeActivation := user.ConsoleActivatedAt
			setInviteRegistrationEnabledForTest(!test.enabled)
			common.RegisterEnabled, common.OAuthRegisterEnabled = false, false
			relogged, ok := findOrCreateWeChatUser(c, "invite-wechat-subject", aff, false)
			require.True(t, ok, recorder.Body.String())
			assert.Equal(t, user.Id, relogged.Id)
			assert.Equal(t, beforeActivation, relogged.ConsoleActivatedAt)
			assertInviteRegistrationAccess(t, db, relogged, inviterID, test.wantL1)
		})
	}
}

func TestInviteRegistrationWeChatStartBindsAffiliateAndConsentToState(t *testing.T) {
	db := setupInviteRegistrationTest(t)
	oldWeChatEnabled := common.WeChatAuthEnabled
	common.WeChatAuthEnabled = true
	t.Cleanup(func() { common.WeChatAuthEnabled = oldWeChatEnabled })
	setInviteRegistrationEnabledForTest(true)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/oauth/wechat/start", strings.NewReader(`{"aff":" state-affiliate ","accepted_legal":true}`))
	WeChatAuthStart(c)
	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Success bool `json:"success"`
		Data    struct {
			FlowToken string `json:"flow_token"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success)
	require.NotEmpty(t, response.Data.FlowToken)
	assert.Contains(t, recorder.Header().Get("Set-Cookie"), oauthStateCookieName("wechat")+"="+response.Data.FlowToken)
	flow, err := model.GetAuthFlow(response.Data.FlowToken, model.AuthFlowMatch{
		Purpose: model.AuthFlowPurposeWeChatLogin, Provider: "wechat", Intent: model.AuthFlowIntentLogin,
	})
	require.NoError(t, err)
	var payload oauthFlowPayload
	require.NoError(t, common.UnmarshalJsonStr(flow.Payload, &payload))
	assert.Equal(t, "state-affiliate", payload.AffiliateCode)
	assert.True(t, payload.AcceptedLegal)
	assert.Zero(t, flow.UserId)
	var count int64
	require.NoError(t, db.Model(&model.User{}).Count(&count).Error)
	assert.Zero(t, count, "starting login must not grant access or create an account")
}

func TestInviteRegistrationWeChatStartRejectsOversizedAffiliate(t *testing.T) {
	db := setupInviteRegistrationTest(t)
	oldWeChatEnabled := common.WeChatAuthEnabled
	common.WeChatAuthEnabled = true
	t.Cleanup(func() { common.WeChatAuthEnabled = oldWeChatEnabled })
	setInviteRegistrationEnabledForTest(true)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/oauth/wechat/start", strings.NewReader(
		fmt.Sprintf(`{"aff":%q,"accepted_legal":true}`, strings.Repeat("a", 33)),
	))
	WeChatAuthStart(c)
	assert.False(t, decodeRegistrationGateResponse(t, recorder).Success)
	var flowCount, userCount int64
	require.NoError(t, db.Model(&model.AuthFlow{}).Count(&flowCount).Error)
	require.NoError(t, db.Model(&model.User{}).Count(&userCount).Error)
	assert.Zero(t, flowCount)
	assert.Zero(t, userCount)
	assert.Empty(t, recorder.Header().Get("Set-Cookie"))
}

func TestInviteRegistrationPasswordPreservesDefaultTokenAndGate(t *testing.T) {
	t.Run("default token remains atomic with activation", func(t *testing.T) {
		db := setupInviteRegistrationTest(t)
		setInviteRegistrationEnabledForTest(true)
		constant.GenerateDefaultToken = true
		aff, inviterID := seedInviteRegistrationInviter(t, db, "valid")
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/api/user/register", strings.NewReader(
			fmt.Sprintf(`{"username":"token-invitee","password":"password1","aff_code":%q,"accepted_legal":true}`, aff),
		))
		Register(c)
		require.True(t, decodeRegistrationGateResponse(t, recorder).Success, recorder.Body.String())
		var user model.User
		require.NoError(t, db.Where("username = ?", "token-invitee").First(&user).Error)
		assertInviteRegistrationAccess(t, db, &user, inviterID, true)
		var tokens []model.Token
		require.NoError(t, db.Where("user_id = ?", user.Id).Find(&tokens).Error)
		require.Len(t, tokens, 1)
		assert.Equal(t, model.TokenCreationSourceSystem, tokens[0].CreationSource)
		assert.Equal(t, common.TokenStatusEnabled, tokens[0].Status)
	})
	t.Run("default token failure rolls back activation", func(t *testing.T) {
		db := setupInviteRegistrationTest(t)
		setInviteRegistrationEnabledForTest(true)
		constant.GenerateDefaultToken = true
		aff, inviterID := seedInviteRegistrationInviter(t, db, "valid")
		require.NoError(t, db.Callback().Create().Before("gorm:create").Register("invite-test-token-failure", func(tx *gorm.DB) {
			if tx.Statement.Schema != nil && tx.Statement.Schema.Table == "tokens" {
				tx.AddError(errors.New("test token creation failed"))
			}
		}))
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/api/user/register", strings.NewReader(
			fmt.Sprintf(`{"username":"token-failure","password":"password1","aff_code":%q,"accepted_legal":true}`, aff),
		))
		Register(c)
		assert.False(t, decodeRegistrationGateResponse(t, recorder).Success)
		var userCount, tokenCount int64
		require.NoError(t, db.Model(&model.User{}).Count(&userCount).Error)
		require.NoError(t, db.Model(&model.Token{}).Count(&tokenCount).Error)
		assert.EqualValues(t, 1, userCount)
		assert.Zero(t, tokenCount)
		var inviter model.User
		require.NoError(t, db.First(&inviter, inviterID).Error)
		assert.Zero(t, inviter.AffCount)
	})
	t.Run("valid invite cannot bypass missing consent", func(t *testing.T) {
		db := setupInviteRegistrationTest(t)
		setInviteRegistrationEnabledForTest(true)
		aff, inviterID := seedInviteRegistrationInviter(t, db, "valid")
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/api/user/register", strings.NewReader(
			fmt.Sprintf(`{"username":"blocked-invitee","password":"password1","aff_code":%q,"accepted_legal":false}`, aff),
		))
		Register(c)
		require.Equal(t, http.StatusUnprocessableEntity, recorder.Code)
		assert.Equal(t, legalConsentRequiredCode, decodeRegistrationGateResponse(t, recorder).Code)
		var count int64
		require.NoError(t, db.Model(&model.User{}).Count(&count).Error)
		assert.EqualValues(t, 1, count)
		var inviter model.User
		require.NoError(t, db.First(&inviter, inviterID).Error)
		assert.Zero(t, inviter.AffCount)
	})
}

func TestInviteRegistrationStillEnforcesNewAccountGates(t *testing.T) {
	for _, route := range []string{"password", "OIDC", "generic", "wechat"} {
		t.Run(route, func(t *testing.T) {
			for _, gate := range []string{"registration disabled", "method disabled", "consent missing", "email unverified"} {
				t.Run(gate, func(t *testing.T) {
					db := setupInviteRegistrationTest(t)
					setInviteRegistrationEnabledForTest(true)
					aff, inviterID := seedInviteRegistrationInviter(t, db, "valid")
					accepted := true
					switch gate {
					case "registration disabled":
						common.RegisterEnabled = false
					case "method disabled":
						common.PasswordRegisterEnabled, common.OAuthRegisterEnabled = false, false
					case "consent missing":
						accepted = false
					case "email unverified":
						common.EmailVerificationEnabled = true
					}
					recorder := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(recorder)
					c.Request = httptest.NewRequest(http.MethodPost, "/api/user/register", strings.NewReader(
						fmt.Sprintf(`{"username":"gated-invitee","password":"password1","aff_code":%q,"accepted_legal":%t}`, aff, accepted),
					))
					switch route {
					case "password":
						Register(c)
						assert.False(t, decodeRegistrationGateResponse(t, recorder).Success)
					case "wechat":
						user, ok := findOrCreateWeChatUser(c, "gated-wechat", aff, accepted)
						assert.Nil(t, user)
						assert.False(t, ok)
					default:
						var provider oauth.Provider = &oauth.OIDCProvider{}
						if route == "generic" {
							provider = oauth.NewGenericOAuthProvider(&model.CustomOAuthProvider{Id: 17, Name: "Invite test", Slug: "invite-test", Enabled: true})
						}
						user, err := findOrCreateOAuthUser(nil, provider, &oauth.OAuthUser{ProviderUserID: "gated-subject"}, aff, accepted)
						assert.Error(t, err)
						assert.Nil(t, user)
					}
					var count int64
					require.NoError(t, db.Model(&model.User{}).Count(&count).Error)
					assert.EqualValues(t, 1, count)
					var inviter model.User
					require.NoError(t, db.First(&inviter, inviterID).Error)
					assert.Zero(t, inviter.AffCount)
				})
			}
		})
	}
}
