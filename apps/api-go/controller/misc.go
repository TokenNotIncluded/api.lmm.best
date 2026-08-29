package controller

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/i18n"
	"github.com/LIghtJUNction/api.lmm.best/logger"
	"github.com/LIghtJUNction/api.lmm.best/middleware"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/oauth"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/console_setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/system_setting"

	"github.com/gin-gonic/gin"
)

func TestStatus(c *gin.Context) {
	err := model.PingDB()
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success": false,
			"message": "数据库连接失败",
		})
		return
	}
	// 获取HTTP统计信息
	httpStats := middleware.GetStats()
	c.JSON(http.StatusOK, gin.H{
		"success":    true,
		"message":    "Server is running",
		"http_stats": httpStats,
	})
	return
}

func GetLiveness(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"live":    true,
		"message": "",
	})
}

func getPublicPreviewModelIDs() []string {
	// The public status payload and the assistant must share the same live
	// catalog. Keeping a separate ability-table preview would silently omit
	// models that are enabled in other groups or newly published in pricing.
	return getPublicCatalogModelIDs()
}

func getPublicCatalogModelIDsForUser(userID int) []string {
	return getPublicCatalogModelIDsWithBillingPolicy(modelListAcceptsUnsetRatioModel(userID))
}

// getPublicCatalogModelIDs mirrors the live public pricing catalog. The
// assistant's L0 pricing tool intentionally exposes only the default-group
// reference price, so models that are enabled exclusively in a private group
// must not be advertised here: otherwise the assistant would list a model and
// then be unable to quote its reference price. The all group is public too.
// An empty result is intentional: callers must report that the live catalog is
// not ready instead of silently substituting a potentially incomplete ability
// snapshot.
func getPublicCatalogModelIDs() []string {
	return getPublicCatalogModelIDsWithBillingPolicy(false)
}

func getPublicCatalogModelIDsWithBillingPolicy(acceptUnsetRatioModel bool) []string {
	modelIDs := make(map[string]struct{})
	for _, pricing := range getPricingCache() {
		if !common.StringsContains(pricing.EnableGroup, "default") &&
			!common.StringsContains(pricing.EnableGroup, "all") {
			continue
		}
		if name := strings.TrimSpace(pricing.ModelName); name != "" && modelListIncludesModel(name, acceptUnsetRatioModel) {
			modelIDs[name] = struct{}{}
		}
	}
	result := make([]string, 0, len(modelIDs))
	for name := range modelIDs {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

func GetStatus(c *gin.Context) {
	if err := cacheReadinessError(); err != nil {
		ensureCachesWarmAsync()
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success": false,
			"ready":   false,
			"message": "service caches are not ready",
		})
		return
	}

	cs := console_setting.GetConsoleSetting()
	registrationDisabledMethods := common.GetRegistrationDisabledMethods()
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()

	passkeySetting := system_setting.GetPasskeySettings()
	assistantSettings := setting.GetAssistantSettings()
	assistantGroup, assistantModel, routeErr := assistantConfiguredRouteResolver(assistantSettings)
	if routeErr != nil {
		assistantModel = ""
	}
	data := gin.H{
		"version":                     common.Version,
		"start_time":                  common.StartTime,
		"email_verification":          common.EmailVerificationEnabled,
		"github_oauth":                common.GitHubOAuthEnabled,
		"github_client_id":            common.GitHubClientId,
		"discord_oauth":               system_setting.GetDiscordSettings().Enabled,
		"discord_client_id":           system_setting.GetDiscordSettings().ClientId,
		"linuxdo_oauth":               common.LinuxDOOAuthEnabled,
		"linuxdo_client_id":           common.LinuxDOClientId,
		"linuxdo_minimum_trust_level": common.LinuxDOMinimumTrustLevel,
		"telegram_oauth":              common.TelegramOAuthEnabled,
		"telegram_bot_name":           common.TelegramBotName,
		"theme":                       "default",
		"system_name":                 common.SystemName,
		"logo":                        common.Logo,
		"footer_html":                 common.Footer,
		"wechat_qrcode":               common.WeChatAccountQRCodeImageURL,
		"wechat_login":                common.WeChatAuthEnabled,
		"server_address":              system_setting.ServerAddress,
		"turnstile_check":             common.TurnstileCheckEnabled,
		"turnstile_site_key":          common.TurnstileSiteKey,
		"docs_link":                   operation_setting.GetGeneralSetting().DocsLink,
		"quota_per_unit":              common.QuotaPerUnit,
		// 兼容旧前端：保留 display_in_currency，同时提供新的 quota_display_type
		"display_in_currency":                 operation_setting.IsCurrencyDisplay(),
		"quota_display_type":                  operation_setting.GetQuotaDisplayType(),
		"custom_currency_symbol":              operation_setting.GetGeneralSetting().CustomCurrencySymbol,
		"custom_currency_code":                operation_setting.GetGeneralSetting().CustomCurrencyCode,
		"custom_currency_exchange_rate":       operation_setting.GetGeneralSetting().CustomCurrencyExchangeRate,
		"enable_batch_update":                 common.BatchUpdateEnabled,
		"enable_drawing":                      common.DrawingEnabled,
		"enable_task":                         common.TaskEnabled,
		"enable_data_export":                  common.DataExportEnabled,
		"data_export_default_time":            common.DataExportDefaultTime,
		"default_collapse_sidebar":            common.DefaultCollapseSidebar,
		"mj_notify_enabled":                   setting.MjNotifyEnabled,
		"chats":                               setting.Chats,
		"demo_site_enabled":                   operation_setting.DemoSiteEnabled,
		"self_use_mode_enabled":               operation_setting.SelfUseModeEnabled,
		"register_enabled":                    common.RegisterEnabled,
		"password_login_enabled":              common.PasswordLoginEnabled,
		"password_register_enabled":           common.PasswordRegisterEnabled,
		"oauth_register_enabled":              common.OAuthRegisterEnabled,
		"oauth_registration_disabled_methods": registrationDisabledMethods,
		"default_use_auto_group":              setting.DefaultUseAutoGroup,
		"preview_model_ids":                   getPublicPreviewModelIDs(),
		"backend_capabilities": gin.H{
			"bounty_notifications":    true,
			"bounty_challenge_cancel": true,
			"bounty_public_read":      true,
			"self_oauth_unbind":       true,
			"responses_websocket":     true,
		},
		"assistant": gin.H{
			"enabled":      assistantSettings.Enabled,
			"group":        assistantGroup,
			"model":        assistantModel,
			"funding_mode": "super_administrator",
		},

		"usd_exchange_rate": operation_setting.USDExchangeRate,
		// Legacy clients read price as platform units per real USD and
		// stripe_unit_price as real USD per platform unit.
		"price":             operation_setting.USDExchangeRate * operation_setting.TopUpPlatformUnitsPerCNY,
		"stripe_unit_price": standardUSDPerPlatformUnit(),

		// 面板启用开关
		"api_info_enabled":      cs.ApiInfoEnabled,
		"uptime_kuma_enabled":   cs.UptimeKumaEnabled,
		"announcements_enabled": cs.AnnouncementsEnabled,
		"faq_enabled":           cs.FAQEnabled,

		// 模块管理配置
		"HeaderNavModules":    common.OptionMap["HeaderNavModules"],
		"SidebarModulesAdmin": common.OptionMap["SidebarModulesAdmin"],

		"oidc_enabled":                system_setting.GetOIDCSettings().Enabled,
		"oidc_client_id":              system_setting.GetOIDCSettings().ClientId,
		"oidc_authorization_endpoint": system_setting.GetOIDCSettings().AuthorizationEndpoint,
		"oidc_display_name":           system_setting.GetOIDCSettings().GetEffectiveDisplayName(),
		"passkey_login":               passkeySetting.Enabled,
		"passkey_display_name":        passkeySetting.RPDisplayName,
		"passkey_rp_id":               passkeySetting.RPID,
		"passkey_origins":             passkeySetting.Origins,
		"passkey_allow_insecure":      passkeySetting.AllowInsecureOrigin,
		"passkey_user_verification":   passkeySetting.UserVerification,
		"passkey_attachment":          passkeySetting.AttachmentPreference,
		"setup":                       constant.IsSetup(),
		"user_agreement_enabled":      system_setting.UserAgreementPublished(),
		"privacy_policy_enabled":      system_setting.PrivacyPolicyPublished(),
		"checkin_enabled":             operation_setting.GetCheckinSetting().Enabled,
	}

	// 根据启用状态注入可选内容
	if cs.ApiInfoEnabled {
		data["api_info"] = console_setting.GetApiInfo()
	}
	if cs.AnnouncementsEnabled {
		data["announcements"] = console_setting.GetAnnouncements()
	}
	if cs.FAQEnabled {
		data["faq"] = console_setting.GetFAQ()
	}
	docsAccess := false
	if dashboardUser, ok := middleware.AuthenticatedDashboardUser(c); ok {
		trustLevel, err := model.GetTrustLevelInfoForUserBase(dashboardUser)
		if err != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf("failed to calculate trust level for user %d: %s", dashboardUser.Id, err.Error()))
		} else {
			docsAccess = trustLevel.Level >= 1
		}
	}
	data["docs_access"] = docsAccess
	if !docsAccess {
		data["docs_link"] = ""
	}
	// Keep the public status response useful for branding, authentication, and
	// billing while withholding legacy relay details until the account has
	// permanently activated the developer console.
	if !middleware.ConsoleActivationGranted(c) {
		data["api_info_enabled"] = false
		delete(data, "api_info")
	}

	// Add enabled custom OAuth providers
	customProviders := oauth.GetEnabledCustomProviders()
	if len(customProviders) > 0 {
		type CustomOAuthInfo struct {
			Id                    int    `json:"id"`
			Name                  string `json:"name"`
			Slug                  string `json:"slug"`
			Icon                  string `json:"icon"`
			ClientId              string `json:"client_id"`
			AuthorizationEndpoint string `json:"authorization_endpoint"`
			Scopes                string `json:"scopes"`
		}
		providersInfo := make([]CustomOAuthInfo, 0, len(customProviders))
		for _, p := range customProviders {
			config := p.GetConfig()
			providersInfo = append(providersInfo, CustomOAuthInfo{
				Id:                    config.Id,
				Name:                  config.Name,
				Slug:                  config.Slug,
				Icon:                  config.Icon,
				ClientId:              config.ClientId,
				AuthorizationEndpoint: config.AuthorizationEndpoint,
				Scopes:                config.Scopes,
			})
		}
		data["custom_oauth_providers"] = providersInfo
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"ready":   true,
		"message": "",
		"data":    data,
	})
	return
}

func GetNotice(c *gin.Context) {
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    common.OptionMap["Notice"],
	})
	return
}

func GetAbout(c *gin.Context) {
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    common.OptionMap["About"],
	})
	return
}

func GetUserAgreement(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    system_setting.GetLegalSettings().UserAgreement,
	})
	return
}

func GetPrivacyPolicy(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    system_setting.GetLegalSettings().PrivacyPolicy,
	})
	return
}

func GetMidjourney(c *gin.Context) {
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    common.OptionMap["Midjourney"],
	})
	return
}

func GetHomePageContent(c *gin.Context) {
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    common.OptionMap["HomePageContent"],
	})
	return
}

func SendEmailVerification(c *gin.Context) {
	email := model.NormalizeEmail(c.Query("email"))
	if err := common.Validate.Var(email, "required,email"); err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	parts := strings.Split(email, "@")
	if len(parts) != 2 {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "无效的邮箱地址",
		})
		return
	}
	localPart := parts[0]
	domainPart := parts[1]
	if common.EmailDomainRestrictionEnabled {
		allowed := false
		for _, domain := range common.EmailDomainWhitelist {
			if domainPart == domain {
				allowed = true
				break
			}
		}
		if !allowed {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "The administrator has enabled the email domain name whitelist, and your email address is not allowed due to special symbols or it's not in the whitelist.",
			})
			return
		}
	}
	if common.EmailAliasRestrictionEnabled {
		containsSpecialSymbols := strings.Contains(localPart, "+") || strings.Contains(localPart, ".")
		if containsSpecialSymbols {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "管理员已启用邮箱地址别名限制，您的邮箱地址由于包含特殊符号而被拒绝。",
			})
			return
		}
	}

	if model.IsEmailAlreadyTaken(email) {
		common.ApiErrorI18n(c, i18n.MsgUserEmailAlreadyTaken)
		return
	}
	code := common.GenerateVerificationCode(6)
	common.RegisterVerificationCodeWithKey(email, code, common.EmailVerificationPurpose)
	subject := fmt.Sprintf("%s邮箱验证邮件", common.SystemName)
	content := fmt.Sprintf("<p>您好，你正在进行%s邮箱验证。</p>"+
		"<p>您的验证码为: <strong>%s</strong></p>"+
		"<p>验证码 %d 分钟内有效，如果不是本人操作，请忽略。</p>", common.SystemName, code, common.VerificationValidMinutes)
	err := common.SendEmail(subject, email, content)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
	return
}

func SendPasswordResetEmail(c *gin.Context) {
	email := model.NormalizeEmail(c.Query("email"))
	if err := common.Validate.Var(email, "required,email"); err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	if _, err := model.GetUniqueUserByEmail(email); err == nil {
		code := common.GenerateVerificationCode(0)
		common.RegisterVerificationCodeWithKey(email, code, common.PasswordResetPurpose)
		subject := fmt.Sprintf("%s密码重置", common.SystemName)
		content, buildErr := buildPasswordResetEmailContent(
			system_setting.ServerAddress,
			common.SystemName,
			email,
			code,
			common.VerificationValidMinutes,
		)
		if buildErr != nil {
			logger.LogError(c.Request.Context(), "failed to build password reset email: "+buildErr.Error())
			c.JSON(http.StatusOK, gin.H{"success": true, "message": ""})
			return
		}
		err := common.SendEmail(subject, email, content)
		if err != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf("failed to send password reset email to %s: %s", email, err.Error()))
		}
	} else if err != nil && !errors.Is(err, model.ErrEmailNotFound) {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("skip password reset email for %s: %s", email, err.Error()))
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
}

func buildPasswordResetEmailContent(serverAddress, systemName, email, token string, validMinutes int) (string, error) {
	resetURL, err := url.Parse(strings.TrimSpace(serverAddress))
	if err != nil {
		return "", fmt.Errorf("invalid server address: %w", err)
	}
	if resetURL.Host == "" || (resetURL.Scheme != "http" && resetURL.Scheme != "https") {
		return "", fmt.Errorf("server address must be an absolute http/https URL")
	}
	resetURL.Path = strings.TrimRight(resetURL.Path, "/") + "/user/reset"
	query := resetURL.Query()
	query.Set("email", email)
	query.Set("token", token)
	resetURL.RawQuery = query.Encode()

	var content bytes.Buffer
	err = passwordResetEmailTemplate.Execute(&content, struct {
		SystemName   string
		ResetURL     string
		ValidMinutes int
	}{
		SystemName:   systemName,
		ResetURL:     resetURL.String(),
		ValidMinutes: validMinutes,
	})
	if err != nil {
		return "", fmt.Errorf("render password reset email: %w", err)
	}
	return content.String(), nil
}

var passwordResetEmailTemplate = template.Must(template.New("password-reset-email").Parse(
	`<p>您好，你正在进行{{.SystemName}}密码重置。</p>` +
		`<p>点击 <a href="{{.ResetURL}}">此处</a> 进行密码重置。</p>` +
		`<p>如果链接无法点击，请尝试点击下面的链接或将其复制到浏览器中打开：<br> {{.ResetURL}} </p>` +
		`<p>重置链接 {{.ValidMinutes}} 分钟内有效，如果不是本人操作，请忽略。</p>`,
))

type PasswordResetRequest struct {
	Email string `json:"email"`
	Token string `json:"token"`
}

func ResetPassword(c *gin.Context) {
	var req PasswordResetRequest
	err := json.NewDecoder(c.Request.Body).Decode(&req)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	req.Email = model.NormalizeEmail(req.Email)
	if req.Email == "" || req.Token == "" {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	if !common.VerifyCodeWithKey(req.Email, req.Token, common.PasswordResetPurpose) {
		common.ApiErrorI18n(c, i18n.MsgUserPasswordResetLinkInvalid)
		return
	}
	password := common.GenerateVerificationCode(12)
	err = model.ResetUserPasswordByEmail(req.Email, password)
	if err != nil {
		if errors.Is(err, model.ErrEmailNotFound) || errors.Is(err, model.ErrEmailAmbiguous) {
			common.ApiErrorI18n(c, i18n.MsgUserPasswordResetLinkInvalid)
			return
		}
		common.ApiError(c, err)
		return
	}
	common.DeleteKey(req.Email, common.PasswordResetPurpose)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    password,
	})
	return
}
