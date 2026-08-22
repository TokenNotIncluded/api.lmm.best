package model

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/config"
	"github.com/LIghtJUNction/api.lmm.best/setting/dynamic_pricing_setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/performance_setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/system_setting"
	"gorm.io/gorm"
)

type Option struct {
	Key   string `json:"key" gorm:"primaryKey"`
	Value string `json:"value"`
}

var retiredIPAccessOptionKeys = map[string]struct{}{
	"GlobalIPWhitelistEnabled":  {},
	"GlobalIPWhitelistCIDRs":    {},
	"RegionAccessPolicyEnabled": {},
	"RegionBlockedCountryCodes": {},
}

func isRetiredIPAccessOptionKey(key string) bool {
	_, retired := retiredIPAccessOptionKeys[key]
	return retired
}

func AllOption() ([]*Option, error) {
	var options []*Option
	var err error
	err = DB.Find(&options).Error
	return options, err
}

func InitOptionMap() {
	common.OptionMapRWMutex.Lock()
	common.OptionMap = make(map[string]string)

	// 添加原有的系统配置
	common.OptionMap["FileUploadPermission"] = strconv.Itoa(common.FileUploadPermission)
	common.OptionMap["FileDownloadPermission"] = strconv.Itoa(common.FileDownloadPermission)
	common.OptionMap["ImageUploadPermission"] = strconv.Itoa(common.ImageUploadPermission)
	common.OptionMap["ImageDownloadPermission"] = strconv.Itoa(common.ImageDownloadPermission)
	common.OptionMap["PasswordLoginEnabled"] = strconv.FormatBool(common.PasswordLoginEnabled)
	common.OptionMap["PasswordRegisterEnabled"] = strconv.FormatBool(common.PasswordRegisterEnabled)
	common.OptionMap["OAuthRegisterEnabled"] = strconv.FormatBool(common.OAuthRegisterEnabled)
	common.OptionMap["EmailVerificationEnabled"] = strconv.FormatBool(common.EmailVerificationEnabled)
	common.OptionMap["GitHubOAuthEnabled"] = strconv.FormatBool(common.GitHubOAuthEnabled)
	common.OptionMap["LinuxDOOAuthEnabled"] = strconv.FormatBool(common.LinuxDOOAuthEnabled)
	common.OptionMap["TelegramOAuthEnabled"] = strconv.FormatBool(common.TelegramOAuthEnabled)
	common.OptionMap["WeChatAuthEnabled"] = strconv.FormatBool(common.WeChatAuthEnabled)
	common.OptionMap["TurnstileCheckEnabled"] = strconv.FormatBool(common.TurnstileCheckEnabled)
	common.OptionMap["RegisterEnabled"] = strconv.FormatBool(common.RegisterEnabled)
	common.OptionMap[common.RegistrationDisabledMethodsOptionKey] = ""
	common.OptionMap["AutomaticDisableChannelEnabled"] = strconv.FormatBool(common.AutomaticDisableChannelEnabled)
	common.OptionMap["AutomaticEnableChannelEnabled"] = strconv.FormatBool(common.AutomaticEnableChannelEnabled)
	common.OptionMap["LogConsumeEnabled"] = strconv.FormatBool(common.LogConsumeEnabled)
	common.OptionMap["DisplayInCurrencyEnabled"] = strconv.FormatBool(common.DisplayInCurrencyEnabled)
	common.OptionMap["DisplayTokenStatEnabled"] = strconv.FormatBool(common.DisplayTokenStatEnabled)
	common.OptionMap["DrawingEnabled"] = strconv.FormatBool(common.DrawingEnabled)
	common.OptionMap["TaskEnabled"] = strconv.FormatBool(common.TaskEnabled)
	common.OptionMap["DataExportEnabled"] = strconv.FormatBool(common.DataExportEnabled)
	common.OptionMap["ChannelDisableThreshold"] = strconv.FormatFloat(common.ChannelDisableThreshold, 'f', -1, 64)
	common.OptionMap["EmailDomainRestrictionEnabled"] = strconv.FormatBool(common.EmailDomainRestrictionEnabled)
	common.OptionMap["EmailAliasRestrictionEnabled"] = strconv.FormatBool(common.EmailAliasRestrictionEnabled)
	common.OptionMap["EmailDomainWhitelist"] = strings.Join(common.EmailDomainWhitelist, ",")
	common.OptionMap["SMTPServer"] = ""
	common.OptionMap["SMTPFrom"] = ""
	common.OptionMap["SMTPPort"] = strconv.Itoa(common.SMTPPort)
	common.OptionMap["SMTPAccount"] = ""
	common.OptionMap["SMTPToken"] = ""
	common.OptionMap["SMTPSSLEnabled"] = strconv.FormatBool(common.SMTPSSLEnabled)
	common.OptionMap["SMTPStartTLSEnabled"] = strconv.FormatBool(common.SMTPStartTLSEnabled)
	common.OptionMap["SMTPInsecureSkipVerify"] = strconv.FormatBool(common.SMTPInsecureSkipVerify)
	common.OptionMap["SMTPForceAuthLogin"] = strconv.FormatBool(common.SMTPForceAuthLogin)
	common.OptionMap["Notice"] = ""
	common.OptionMap["About"] = ""
	common.OptionMap["HomePageContent"] = ""
	common.OptionMap["Footer"] = common.Footer
	common.OptionMap["SystemName"] = common.SystemName
	common.OptionMap["Logo"] = common.Logo
	common.OptionMap["ServerAddress"] = system_setting.ServerAddress
	common.OptionMap["WorkerUrl"] = system_setting.WorkerUrl
	common.OptionMap["WorkerValidKey"] = system_setting.WorkerValidKey
	common.OptionMap["WorkerAllowHttpImageRequestEnabled"] = strconv.FormatBool(system_setting.WorkerAllowHttpImageRequestEnabled)
	common.OptionMap["PayAddress"] = ""
	common.OptionMap["CustomCallbackAddress"] = ""
	common.OptionMap["EpayId"] = ""
	common.OptionMap["EpayKey"] = ""
	common.OptionMap["Price"] = strconv.FormatFloat(operation_setting.Price, 'f', -1, 64)
	common.OptionMap["USDExchangeRate"] = strconv.FormatFloat(operation_setting.USDExchangeRate, 'f', -1, 64)
	common.OptionMap["MinTopUp"] = strconv.Itoa(operation_setting.MinTopUp)
	common.OptionMap["StripeMinTopUp"] = strconv.Itoa(setting.StripeMinTopUp)
	common.OptionMap["StripeApiSecret"] = setting.StripeApiSecret
	common.OptionMap["StripeWebhookSecret"] = setting.StripeWebhookSecret
	common.OptionMap["StripePriceId"] = setting.StripePriceId
	common.OptionMap["StripeUnitPrice"] = strconv.FormatFloat(setting.StripeUnitPrice, 'f', -1, 64)
	common.OptionMap["StripePromotionCodesEnabled"] = strconv.FormatBool(setting.StripePromotionCodesEnabled)
	common.OptionMap["CreemApiKey"] = setting.CreemApiKey
	common.OptionMap["CreemProducts"] = setting.CreemProducts
	common.OptionMap["CreemTestMode"] = strconv.FormatBool(setting.CreemTestMode)
	common.OptionMap["CreemWebhookSecret"] = setting.CreemWebhookSecret
	common.OptionMap["WaffoEnabled"] = strconv.FormatBool(setting.WaffoEnabled)
	common.OptionMap["WaffoApiKey"] = setting.WaffoApiKey
	common.OptionMap["WaffoPrivateKey"] = setting.WaffoPrivateKey
	common.OptionMap["WaffoPublicCert"] = setting.WaffoPublicCert
	common.OptionMap["WaffoSandboxPublicCert"] = setting.WaffoSandboxPublicCert
	common.OptionMap["WaffoSandboxApiKey"] = setting.WaffoSandboxApiKey
	common.OptionMap["WaffoSandboxPrivateKey"] = setting.WaffoSandboxPrivateKey
	common.OptionMap["WaffoSandbox"] = strconv.FormatBool(setting.WaffoSandbox)
	common.OptionMap["WaffoMerchantId"] = setting.WaffoMerchantId
	common.OptionMap["WaffoNotifyUrl"] = setting.WaffoNotifyUrl
	common.OptionMap["WaffoReturnUrl"] = setting.WaffoReturnUrl
	common.OptionMap["WaffoSubscriptionReturnUrl"] = setting.WaffoSubscriptionReturnUrl
	common.OptionMap["WaffoCurrency"] = setting.WaffoCurrency
	common.OptionMap["WaffoUnitPrice"] = strconv.FormatFloat(setting.WaffoUnitPrice, 'f', -1, 64)
	common.OptionMap["WaffoMinTopUp"] = strconv.Itoa(setting.WaffoMinTopUp)
	common.OptionMap["WaffoPayMethods"] = setting.WaffoPayMethods2JsonString()
	common.OptionMap["WaffoPancakeMerchantID"] = setting.WaffoPancakeMerchantID
	common.OptionMap["WaffoPancakePrivateKey"] = setting.WaffoPancakePrivateKey
	common.OptionMap["WaffoPancakeReturnURL"] = setting.WaffoPancakeReturnURL
	common.OptionMap["WaffoPancakeUnitPrice"] = strconv.FormatFloat(setting.WaffoPancakeUnitPrice, 'f', -1, 64)
	common.OptionMap["WaffoPancakeMinTopUp"] = strconv.Itoa(setting.WaffoPancakeMinTopUp)
	common.OptionMap["WaffoPancakeStoreID"] = setting.WaffoPancakeStoreID
	common.OptionMap["WaffoPancakeProductID"] = setting.WaffoPancakeProductID
	common.OptionMap["TopupGroupRatio"] = common.TopupGroupRatio2JSONString()
	common.OptionMap["Chats"] = setting.Chats2JsonString()
	assistantSettings := setting.GetAssistantSettings()
	common.OptionMap[setting.AssistantEnabledOptionKey] = strconv.FormatBool(assistantSettings.Enabled)
	common.OptionMap[setting.AssistantModelOptionKey] = assistantSettings.Model
	common.OptionMap[setting.AssistantGroupOptionKey] = assistantSettings.Group
	common.OptionMap[setting.AssistantL1AutoApprovalUserIDsOptionKey] = assistantSettings.L1AutoApprovalUserIDs
	common.OptionMap[setting.AssistantReasoningEffortOptionKey] = assistantSettings.ReasoningEffort
	common.OptionMap[setting.AssistantStreamEnabledOptionKey] = strconv.FormatBool(assistantSettings.StreamEnabled)
	common.OptionMap[setting.AssistantTemperatureOptionKey] = strconv.FormatFloat(assistantSettings.Temperature, 'f', -1, 64)
	common.OptionMap[setting.AssistantMaxTokensOptionKey] = strconv.Itoa(assistantSettings.MaxTokens)
	common.OptionMap[setting.AssistantWeeklyCreditUSDOptionKey] = "0"
	common.OptionMap[setting.AssistantAgentLoopEnabledOptionKey] = strconv.FormatBool(assistantSettings.AgentLoopEnabled)
	common.OptionMap[setting.AssistantMaxStepsOptionKey] = strconv.Itoa(assistantSettings.MaxSteps)
	common.OptionMap[setting.AssistantTimeoutSecondsOptionKey] = strconv.Itoa(assistantSettings.TimeoutSeconds)
	common.OptionMap[setting.AssistantCacheEnabledOptionKey] = strconv.FormatBool(assistantSettings.CacheEnabled)
	common.OptionMap[setting.AssistantCacheTTLMinutesOptionKey] = strconv.Itoa(assistantSettings.CacheTTLMinutes)
	common.OptionMap[setting.AssistantPersonaOptionKey] = assistantSettings.Persona
	common.OptionMap[setting.AssistantSystemPromptOptionKey] = assistantSettings.SystemPrompt
	common.OptionMap[setting.AssistantSearchProviderOptionKey] = string(assistantSettings.SearchProvider)
	common.OptionMap[setting.AssistantSearchURLOptionKey] = assistantSettings.SearchURL
	common.OptionMap[setting.AssistantSearchAPIKeyOptionKey] = assistantSettings.SearchAPIKey
	common.OptionMap[setting.AssistantSearchMCPToolOptionKey] = assistantSettings.SearchMCPTool
	common.OptionMap[setting.AssistantSkillsOptionKey] = assistantSettings.Skills
	common.OptionMap[setting.AssistantSkillFilesOptionKey] = setting.AssistantSkillFilesJSON(assistantSettings.SkillFiles)
	common.OptionMap[setting.AssistantReviewEnabledOptionKey] = strconv.FormatBool(assistantSettings.ReviewEnabled)
	common.OptionMap[setting.AssistantReviewWindowDaysOptionKey] = strconv.Itoa(assistantSettings.ReviewWindowDays)
	common.OptionMap[setting.AssistantReviewIntervalHoursOptionKey] = strconv.Itoa(assistantSettings.ReviewIntervalHours)
	common.OptionMap[setting.AssistantReviewProbabilityOptionKey] = strconv.FormatFloat(assistantSettings.ReviewProbability, 'f', -1, 64)
	common.OptionMap[setting.AssistantReviewModelOptionKey] = assistantSettings.ReviewModel
	common.OptionMap[setting.AssistantReviewGroupPoliciesOptionKey] = setting.AssistantReviewGroupPoliciesJSON(assistantSettings.ReviewGroupPolicies)
	common.OptionMap[setting.AssistantRetentionEnabledOptionKey] = strconv.FormatBool(assistantSettings.RetentionEnabled)
	common.OptionMap[setting.AssistantActiveRetentionDaysOptionKey] = strconv.Itoa(assistantSettings.ActiveRetentionDays)
	common.OptionMap[setting.AssistantArchivedRetentionDaysOptionKey] = strconv.Itoa(assistantSettings.ArchivedRetentionDays)
	common.OptionMap[setting.AssistantSecurityRetentionDaysOptionKey] = strconv.Itoa(assistantSettings.SecurityRetentionDays)
	common.OptionMap[setting.AssistantRetentionIntervalHoursOptionKey] = strconv.Itoa(assistantSettings.RetentionIntervalHours)
	common.OptionMap["AutoGroups"] = setting.AutoGroups2JsonString()
	common.OptionMap["DefaultUseAutoGroup"] = strconv.FormatBool(setting.DefaultUseAutoGroup)
	common.OptionMap["MaxTokenAutoGroups"] = strconv.Itoa(setting.GetMaxTokenAutoGroups())
	common.OptionMap["PayMethods"] = operation_setting.PayMethods2JsonString()
	common.OptionMap["GitHubClientId"] = ""
	common.OptionMap["GitHubClientSecret"] = ""
	common.OptionMap["TelegramBotToken"] = ""
	common.OptionMap["TelegramBotName"] = ""
	common.OptionMap["WeChatServerAddress"] = ""
	common.OptionMap["WeChatServerToken"] = ""
	common.OptionMap["WeChatAccountQRCodeImageURL"] = ""
	common.OptionMap["TurnstileSiteKey"] = ""
	common.OptionMap["TurnstileSecretKey"] = ""
	common.OptionMap["QuotaForNewUser"] = strconv.Itoa(common.QuotaForNewUser)
	common.OptionMap[OpenSourceBountyFeeRateOptionKey] = "1"
	common.OptionMap["QuotaForInviter"] = strconv.Itoa(common.QuotaForInviter)
	common.OptionMap["QuotaForInvitee"] = strconv.Itoa(common.QuotaForInvitee)
	common.OptionMap["QuotaRemindThreshold"] = strconv.Itoa(common.QuotaRemindThreshold)
	common.OptionMap["PreConsumedQuota"] = strconv.Itoa(common.PreConsumedQuota)
	common.OptionMap["ModelRequestRateLimitCount"] = strconv.Itoa(setting.ModelRequestRateLimitCount)
	common.OptionMap["ModelRequestRateLimitDurationMinutes"] = strconv.Itoa(setting.ModelRequestRateLimitDurationMinutes)
	common.OptionMap["ModelRequestRateLimitSuccessCount"] = strconv.Itoa(setting.ModelRequestRateLimitSuccessCount)
	common.OptionMap["ModelRequestRateLimitGroup"] = setting.ModelRequestRateLimitGroup2JSONString()
	common.OptionMap["ModelRatio"] = ratio_setting.ModelRatio2JSONString()
	common.OptionMap["ModelPrice"] = ratio_setting.ModelPrice2JSONString()
	common.OptionMap["CacheRatio"] = ratio_setting.CacheRatio2JSONString()
	common.OptionMap["CreateCacheRatio"] = ratio_setting.CreateCacheRatio2JSONString()
	common.OptionMap["GroupRatio"] = ratio_setting.GroupRatio2JSONString()
	common.OptionMap["GroupGroupRatio"] = ratio_setting.GroupGroupRatio2JSONString()
	common.OptionMap["UserUsableGroups"] = setting.UserUsableGroups2JSONString()
	common.OptionMap["CompletionRatio"] = ratio_setting.CompletionRatio2JSONString()
	common.OptionMap["ImageRatio"] = ratio_setting.ImageRatio2JSONString()
	common.OptionMap["AudioRatio"] = ratio_setting.AudioRatio2JSONString()
	common.OptionMap["AudioCompletionRatio"] = ratio_setting.AudioCompletionRatio2JSONString()
	common.OptionMap["TopUpLink"] = common.TopUpLink
	//common.OptionMap["ChatLink"] = common.ChatLink
	//common.OptionMap["ChatLink2"] = common.ChatLink2
	common.OptionMap["QuotaPerUnit"] = strconv.FormatFloat(common.QuotaPerUnit, 'f', -1, 64)
	common.OptionMap["RetryTimes"] = strconv.Itoa(common.RetryTimes)
	common.OptionMap["DataExportInterval"] = strconv.Itoa(common.DataExportInterval)
	common.OptionMap["DataExportDefaultTime"] = common.DataExportDefaultTime
	common.OptionMap["DefaultCollapseSidebar"] = strconv.FormatBool(common.DefaultCollapseSidebar)
	common.OptionMap["MjNotifyEnabled"] = strconv.FormatBool(setting.MjNotifyEnabled)
	common.OptionMap["MjAccountFilterEnabled"] = strconv.FormatBool(setting.MjAccountFilterEnabled)
	common.OptionMap["MjModeClearEnabled"] = strconv.FormatBool(setting.MjModeClearEnabled)
	common.OptionMap["MjForwardUrlEnabled"] = strconv.FormatBool(setting.MjForwardUrlEnabled)
	common.OptionMap["MjActionCheckSuccessEnabled"] = strconv.FormatBool(setting.MjActionCheckSuccessEnabled)
	common.OptionMap["CheckSensitiveEnabled"] = strconv.FormatBool(setting.CheckSensitiveEnabled)
	common.OptionMap["DemoSiteEnabled"] = strconv.FormatBool(operation_setting.DemoSiteEnabled)
	common.OptionMap["SelfUseModeEnabled"] = strconv.FormatBool(operation_setting.SelfUseModeEnabled)
	common.OptionMap["ModelRequestRateLimitEnabled"] = strconv.FormatBool(setting.ModelRequestRateLimitEnabled)
	common.OptionMap["CheckSensitiveOnPromptEnabled"] = strconv.FormatBool(setting.CheckSensitiveOnPromptEnabled)
	common.OptionMap["StopOnSensitiveEnabled"] = strconv.FormatBool(setting.StopOnSensitiveEnabled)
	common.OptionMap["SensitiveWords"] = setting.SensitiveWordsToString()
	advancedSecuritySettings := setting.GetAdvancedSecuritySettings()
	common.OptionMap[setting.AdvancedSecurityEnabledOptionKey] = strconv.FormatBool(advancedSecuritySettings.Enabled)
	common.OptionMap[setting.AdvancedSecurityOnPromptOptionKey] = strconv.FormatBool(advancedSecuritySettings.OnPrompt)
	common.OptionMap[setting.AdvancedSecurityActionOptionKey] = advancedSecuritySettings.Action
	common.OptionMap[setting.AdvancedSecurityRulesOptionKey] = setting.AdvancedSecurityRulesToJSONString()
	antiRelaySettings := setting.GetAntiRelaySettings()
	common.OptionMap[setting.AntiRelayEnabledOptionKey] = strconv.FormatBool(antiRelaySettings.Enabled)
	common.OptionMap[setting.AntiRelayRejectProxyHeadersOptionKey] = strconv.FormatBool(antiRelaySettings.RejectProxyHeaders)
	common.OptionMap[setting.AntiRelayHTTPSOnlyOptionKey] = strconv.FormatBool(antiRelaySettings.HTTPSOnly)
	common.OptionMap[setting.AntiRelayBlockedCIDRsOptionKey] = setting.AntiRelayBlockedCIDRsToJSONString()
	common.OptionMap[setting.AntiRelayTrustedProxyCIDRsOptionKey] = setting.AntiRelayTrustedProxyCIDRsToJSONString()
	common.OptionMap[setting.IPAccessRoutingRulesOptionKey] = setting.GetIPAccessRoutingRules()
	common.OptionMap["StreamCacheQueueLength"] = strconv.Itoa(setting.StreamCacheQueueLength)
	common.OptionMap["AutomaticDisableKeywords"] = operation_setting.AutomaticDisableKeywordsToString()
	common.OptionMap["AutomaticDisableStatusCodes"] = operation_setting.AutomaticDisableStatusCodesToString()
	common.OptionMap["AutomaticRetryStatusCodes"] = operation_setting.AutomaticRetryStatusCodesToString()
	common.OptionMap["ExposeRatioEnabled"] = strconv.FormatBool(ratio_setting.IsExposeRatioEnabled())

	// 自动添加所有注册的模型配置
	modelConfigs := config.GlobalConfig.ExportAllConfigs()
	for k, v := range modelConfigs {
		common.OptionMap[k] = v
	}

	common.OptionMapRWMutex.Unlock()
	loadOptionsFromDatabase()
}

func loadOptionsFromDatabase() {
	options, _ := AllOption()
	for _, option := range options {
		err := updateOptionMap(option.Key, option.Value)
		if err != nil {
			common.SysLog("failed to update option map: " + err.Error())
		}
	}
}

func SyncOptions(frequency int) {
	for {
		time.Sleep(time.Duration(frequency) * time.Second)
		common.SysLog("syncing options from database")
		loadOptionsFromDatabase()
	}
}

func validateOptionValue(key string, value string) error {
	if isRetiredIPAccessOptionKey(key) {
		return errors.New("legacy IP access option is retired; use IPAccessRoutingRules")
	}
	if key == common.RegistrationDisabledMethodsOptionKey {
		_, err := common.ParseRegistrationDisabledMethods(value)
		return err
	}
	if dynamic_pricing_setting.IsOptionKey(key) {
		return dynamic_pricing_setting.ValidateOptionValues(map[string]string{key: value})
	}
	if err := setting.ValidateAssistantOption(key, value); err != nil {
		return err
	}
	if key == setting.AssistantModelOptionKey {
		group := strings.TrimSpace(setting.GetAssistantSettings().Group)
		if group == "" {
			group = setting.DefaultAssistantGroup
		}
		if !IsModelEnabledForGroup(group, strings.TrimSpace(value)) {
			return fmt.Errorf("assistant model is not enabled in the %s group; choose a live model from the model list", group)
		}
	}
	if key == setting.AssistantGroupOptionKey && !ratio_setting.ContainsGroupRatio(strings.TrimSpace(value)) {
		return errors.New("assistant routing group must be an existing group")
	}
	if key == setting.AssistantReviewModelOptionKey && !IsModelEnabledForGroup("default", strings.TrimSpace(value)) {
		return errors.New("assistant review model is not enabled in the default group; choose a live model from the model list")
	}
	if err := setting.ValidateAdvancedSecurityOption(key, value); err != nil {
		return err
	}
	if err := setting.ValidateAntiRelayOption(key, value); err != nil {
		return err
	}
	if err := setting.ValidateIPAccessRoutingOption(key, value); err != nil {
		return err
	}
	if key == operation_setting.ToolPriceOptionKey {
		return operation_setting.ValidateToolPricesJSON(value)
	}
	if key == OpenSourceBountyFeeRateOptionKey {
		_, err := parseOpenSourceBountyFeeRateBasisPoints(value)
		return err
	}
	if key == "MaxTokenAutoGroups" {
		return setting.ValidateMaxTokenAutoGroups(value)
	}
	if key == "public_relay_setting.group" {
		group := strings.TrimSpace(value)
		if group == "" || !ratio_setting.ContainsGroupRatio(group) {
			return errors.New("public relay group must be an existing group")
		}
		return nil
	}
	if key == "group_ratio_setting.group_warnings" {
		return ratio_setting.CheckGroupWarnings(value)
	}
	if key == "GroupGroupRatio" {
		return ratio_setting.CheckGroupGroupRatio(value)
	}
	if key == operation_setting.ViolationFeeOptionKey+".policies" {
		return operation_setting.ValidateViolationFeeSettingsJSON(`{"enabled":true,"policies":` + value + `}`)
	}
	return nil
}

func UpdateOption(key string, value string) error {
	if err := validateOptionValue(key, value); err != nil {
		return err
	}
	// Save to database first
	option := Option{
		Key: key,
	}
	// https://gorm.io/docs/update.html#Save-All-Fields
	if err := DB.FirstOrCreate(&option, Option{Key: key}).Error; err != nil {
		return err
	}
	option.Value = value
	// Save is a combination function.
	// If save value does not contain primary key, it will execute Create,
	// otherwise it will execute Update (with all fields).
	if err := DB.Save(&option).Error; err != nil {
		return err
	}
	// Update OptionMap
	return updateOptionMap(key, value)
}

// ValidateOptionValue exposes the same validation used by UpdateOption without
// persisting or mutating the in-memory option map.  Assistant admin previews
// use this to reject an invalid change before issuing a one-time confirmation
// flow.
func ValidateOptionValue(key, value string) error {
	return validateOptionValue(key, value)
}

// ValidateOptionValues checks a related set of option writes without
// persisting them. Dynamic-pricing fields are validated as one candidate
// configuration so an import cannot pass each field in isolation while the
// resulting configuration is unsafe.
func ValidateOptionValues(values map[string]string) error {
	if len(values) == 0 {
		return errors.New("at least one option is required")
	}
	dynamicValues := make(map[string]string)
	assistantRouteChanged := false
	for key, value := range values {
		if dynamic_pricing_setting.IsOptionKey(key) {
			dynamicValues[key] = value
			continue
		}
		if key == setting.AssistantGroupOptionKey || key == setting.AssistantModelOptionKey {
			assistantRouteChanged = true
			continue
		}
		if err := validateOptionValue(key, value); err != nil {
			return err
		}
	}
	if assistantRouteChanged {
		if value, ok := values[setting.AssistantGroupOptionKey]; ok {
			if err := setting.ValidateAssistantOption(setting.AssistantGroupOptionKey, value); err != nil {
				return err
			}
			if !ratio_setting.ContainsGroupRatio(strings.TrimSpace(value)) {
				return errors.New("assistant routing group must be an existing group")
			}
		}
		if value, ok := values[setting.AssistantModelOptionKey]; ok {
			if err := setting.ValidateAssistantOption(setting.AssistantModelOptionKey, value); err != nil {
				return err
			}
		}
		if err := validateAssistantRouteValues(values); err != nil {
			return err
		}
	}
	if len(dynamicValues) > 0 {
		if err := dynamic_pricing_setting.ValidateOptionValues(dynamicValues); err != nil {
			return err
		}
	}
	return nil
}

// validateAssistantRouteValues validates the candidate group/model pair as a
// unit. This matters when the settings page changes both fields at once: the
// model must be checked against the proposed group, not the old in-memory one.
func validateAssistantRouteValues(values map[string]string) error {
	settings := setting.GetAssistantSettings()
	group := strings.TrimSpace(settings.Group)
	if value, ok := values[setting.AssistantGroupOptionKey]; ok {
		group = strings.TrimSpace(value)
	}
	if group == "" {
		group = setting.DefaultAssistantGroup
	}
	if !ratio_setting.ContainsGroupRatio(group) {
		return errors.New("assistant routing group must be an existing group")
	}

	modelID := strings.TrimSpace(settings.Model)
	if value, ok := values[setting.AssistantModelOptionKey]; ok {
		modelID = strings.TrimSpace(value)
	}
	if modelID == "" || !IsModelEnabledForGroup(group, modelID) {
		return fmt.Errorf("assistant model is not enabled in the %s group; choose a live model from the model list", group)
	}
	return nil
}

// UpdateOptionsBulk persists multiple key/value pairs in a single database
// transaction, then dispatches them through updateOptionMap in one pass. If
// any DB write fails the whole transaction rolls back and no in-memory state
// is touched — safe for callers that must commit a set of related options
// atomically (e.g. payment gateway binding).
func UpdateOptionsBulk(values map[string]string) error {
	if len(values) == 0 {
		return nil
	}
	if err := ValidateOptionValues(values); err != nil {
		return err
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	// Apply the master switch last when enabling (all safety inputs are live
	// first), and first when disabling (request-path pricing stops before any
	// other setting changes).
	enabledKey := "dynamic_pricing_setting.enabled"
	if enabledValue, ok := values[enabledKey]; ok {
		withoutEnabled := make([]string, 0, len(keys)-1)
		for _, key := range keys {
			if key != enabledKey {
				withoutEnabled = append(withoutEnabled, key)
			}
		}
		if enabledValue == "false" {
			keys = append([]string{enabledKey}, withoutEnabled...)
		} else {
			keys = append(withoutEnabled, enabledKey)
		}
	}
	err := DB.Transaction(func(tx *gorm.DB) error {
		for _, k := range keys {
			v := values[k]
			option := Option{Key: k}
			if err := tx.FirstOrCreate(&option, Option{Key: k}).Error; err != nil {
				return err
			}
			option.Value = v
			if err := tx.Save(&option).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, k := range keys {
		v := values[k]
		if err := updateOptionMap(k, v); err != nil {
			return err
		}
	}
	return nil
}

// UpdateAdvancedSecurityOptions persists and applies the four guardrail
// settings as a unit. Database readers see one transaction, while request
// handlers see one runtime settings swap instead of four intermediate states.
func UpdateAdvancedSecurityOptions(enabled, onPrompt bool, action, rules string) error {
	values := map[string]string{
		setting.AdvancedSecurityEnabledOptionKey:  strconv.FormatBool(enabled),
		setting.AdvancedSecurityOnPromptOptionKey: strconv.FormatBool(onPrompt),
		setting.AdvancedSecurityActionOptionKey:   action,
		setting.AdvancedSecurityRulesOptionKey:    rules,
	}
	keys := []string{
		setting.AdvancedSecurityRulesOptionKey,
		setting.AdvancedSecurityActionOptionKey,
		setting.AdvancedSecurityOnPromptOptionKey,
		setting.AdvancedSecurityEnabledOptionKey,
	}
	for _, key := range keys {
		if err := validateOptionValue(key, values[key]); err != nil {
			return err
		}
	}

	if err := DB.Transaction(func(tx *gorm.DB) error {
		for _, key := range keys {
			option := Option{Key: key}
			if err := tx.FirstOrCreate(&option, Option{Key: key}).Error; err != nil {
				return err
			}
			option.Value = values[key]
			if err := tx.Save(&option).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}

	// Validation above makes this deterministic after the transaction. Keeping
	// the setter defensive prevents future validation/runtime drift.
	if err := setting.ApplyAdvancedSecuritySettings(enabled, onPrompt, action, rules); err != nil {
		return err
	}
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	for _, key := range keys {
		common.OptionMap[key] = values[key]
	}
	common.OptionMapRWMutex.Unlock()
	return nil
}

func updateOptionMap(key string, value string) (err error) {
	if isRetiredIPAccessOptionKey(key) {
		common.OptionMapRWMutex.Lock()
		delete(common.OptionMap, key)
		common.OptionMapRWMutex.Unlock()
		return nil
	}
	// Reject malformed persisted route rules before touching OptionMap or the
	// active request-path policy.
	if err := setting.ValidateIPAccessRoutingOption(key, value); err != nil {
		return err
	}
	// Legacy model-specific Grok violation options are intentionally ignored.
	// The active policy is now operation_setting's provider-agnostic group
	// policy; deleting these keys from the runtime map keeps old database rows
	// from reappearing in the admin option API without requiring destructive
	// migration of the historical rows.
	if strings.HasPrefix(key, "grok.violation_") {
		common.OptionMapRWMutex.Lock()
		delete(common.OptionMap, key)
		common.OptionMapRWMutex.Unlock()
		return nil
	}
	if key == retiredThemeOptionKey {
		common.OptionMapRWMutex.Lock()
		delete(common.OptionMap, key)
		common.OptionMapRWMutex.Unlock()
		return nil
	}
	if key == setting.AssistantWeeklyCreditUSDOptionKey {
		common.OptionMapRWMutex.Lock()
		defer common.OptionMapRWMutex.Unlock()
		if common.OptionMap == nil {
			common.OptionMap = make(map[string]string)
		}
		common.OptionMap[key] = "0"
		return nil
	}
	common.OptionMapRWMutex.Lock()
	defer common.OptionMapRWMutex.Unlock()
	common.OptionMap[key] = value

	// 检查是否是模型配置 - 使用更规范的方式处理
	if handleConfigUpdate(key, value) {
		return nil // 已由配置系统处理
	}

	// 处理传统配置项...
	if strings.HasSuffix(key, "Permission") {
		intValue, _ := strconv.Atoi(value)
		switch key {
		case "FileUploadPermission":
			common.FileUploadPermission = intValue
		case "FileDownloadPermission":
			common.FileDownloadPermission = intValue
		case "ImageUploadPermission":
			common.ImageUploadPermission = intValue
		case "ImageDownloadPermission":
			common.ImageDownloadPermission = intValue
		}
	}
	if strings.HasSuffix(key, "Enabled") || key == "DefaultCollapseSidebar" || key == "DefaultUseAutoGroup" || key == "SMTPForceAuthLogin" || key == "SMTPInsecureSkipVerify" {
		boolValue := value == "true"
		switch key {
		case "PasswordRegisterEnabled":
			common.PasswordRegisterEnabled = boolValue
		case "OAuthRegisterEnabled":
			common.OAuthRegisterEnabled = boolValue
		case "PasswordLoginEnabled":
			common.PasswordLoginEnabled = boolValue
		case "EmailVerificationEnabled":
			common.EmailVerificationEnabled = boolValue
		case "GitHubOAuthEnabled":
			common.GitHubOAuthEnabled = boolValue
		case "LinuxDOOAuthEnabled":
			common.LinuxDOOAuthEnabled = boolValue
		case "WeChatAuthEnabled":
			common.WeChatAuthEnabled = boolValue
		case "TelegramOAuthEnabled":
			common.TelegramOAuthEnabled = boolValue
		case "TurnstileCheckEnabled":
			common.TurnstileCheckEnabled = boolValue
		case "RegisterEnabled":
			common.RegisterEnabled = boolValue
		case "EmailDomainRestrictionEnabled":
			common.EmailDomainRestrictionEnabled = boolValue
		case "EmailAliasRestrictionEnabled":
			common.EmailAliasRestrictionEnabled = boolValue
		case "AutomaticDisableChannelEnabled":
			common.AutomaticDisableChannelEnabled = boolValue
		case "AutomaticEnableChannelEnabled":
			common.AutomaticEnableChannelEnabled = boolValue
		case "LogConsumeEnabled":
			common.LogConsumeEnabled = boolValue
		case "DisplayInCurrencyEnabled":
			// 兼容旧字段：同步到新配置 general_setting.quota_display_type（运行时生效）
			// true -> USD, false -> TOKENS
			newVal := "USD"
			if !boolValue {
				newVal = "TOKENS"
			}
			if cfg := config.GlobalConfig.Get("general_setting"); cfg != nil {
				_ = config.UpdateConfigFromMap(cfg, map[string]string{"quota_display_type": newVal})
			}
		case "DisplayTokenStatEnabled":
			common.DisplayTokenStatEnabled = boolValue
		case "DrawingEnabled":
			common.DrawingEnabled = boolValue
		case "TaskEnabled":
			common.TaskEnabled = boolValue
		case "DataExportEnabled":
			common.DataExportEnabled = boolValue
		case "DefaultCollapseSidebar":
			common.DefaultCollapseSidebar = boolValue
		case "MjNotifyEnabled":
			setting.MjNotifyEnabled = boolValue
		case "MjAccountFilterEnabled":
			setting.MjAccountFilterEnabled = boolValue
		case "MjModeClearEnabled":
			setting.MjModeClearEnabled = boolValue
		case "MjForwardUrlEnabled":
			setting.MjForwardUrlEnabled = boolValue
		case "MjActionCheckSuccessEnabled":
			setting.MjActionCheckSuccessEnabled = boolValue
		case "CheckSensitiveEnabled":
			setting.CheckSensitiveEnabled = boolValue
		case "DemoSiteEnabled":
			operation_setting.DemoSiteEnabled = boolValue
		case "SelfUseModeEnabled":
			operation_setting.SelfUseModeEnabled = boolValue
		case "CheckSensitiveOnPromptEnabled":
			setting.CheckSensitiveOnPromptEnabled = boolValue
		case "ModelRequestRateLimitEnabled":
			setting.ModelRequestRateLimitEnabled = boolValue
		case "StopOnSensitiveEnabled":
			setting.StopOnSensitiveEnabled = boolValue
		case setting.AdvancedSecurityEnabledOptionKey:
			setting.SetAdvancedSecurityEnabled(boolValue)
		case setting.AdvancedSecurityOnPromptOptionKey:
			setting.SetAdvancedSecurityOnPrompt(boolValue)
		case setting.AntiRelayEnabledOptionKey:
			setting.SetAntiRelayEnabled(boolValue)
		case setting.AntiRelayRejectProxyHeadersOptionKey:
			setting.SetAntiRelayRejectProxyHeaders(boolValue)
		case setting.AntiRelayHTTPSOnlyOptionKey:
			setting.SetAntiRelayHTTPSOnly(boolValue)
		case "SMTPSSLEnabled":
			common.SMTPSSLEnabled = boolValue
		case "SMTPStartTLSEnabled":
			common.SMTPStartTLSEnabled = boolValue
		case "SMTPInsecureSkipVerify":
			common.SMTPInsecureSkipVerify = boolValue
		case "SMTPForceAuthLogin":
			common.SMTPForceAuthLogin = boolValue
		case "WorkerAllowHttpImageRequestEnabled":
			system_setting.WorkerAllowHttpImageRequestEnabled = boolValue
		case "DefaultUseAutoGroup":
			setting.DefaultUseAutoGroup = boolValue
		case "ExposeRatioEnabled":
			ratio_setting.SetExposeRatioEnabled(boolValue)
		case setting.AssistantEnabledOptionKey:
			setting.SetAssistantEnabled(boolValue)
		case setting.AssistantAgentLoopEnabledOptionKey:
			setting.SetAssistantAgentLoopEnabled(boolValue)
		case setting.AssistantStreamEnabledOptionKey:
			setting.SetAssistantStreamEnabled(boolValue)
		case setting.AssistantCacheEnabledOptionKey:
			setting.SetAssistantCacheEnabled(boolValue)
		case setting.AssistantReviewEnabledOptionKey:
			setting.SetAssistantReviewEnabled(boolValue)
		case setting.AssistantRetentionEnabledOptionKey:
			setting.SetAssistantRetentionEnabled(boolValue)
		}
	}
	switch key {
	case "EmailDomainWhitelist":
		common.EmailDomainWhitelist = strings.Split(value, ",")
	case "SMTPServer":
		common.SMTPServer = value
	case "SMTPPort":
		intValue, _ := strconv.Atoi(value)
		common.SMTPPort = intValue
	case "SMTPAccount":
		common.SMTPAccount = value
	case "SMTPFrom":
		common.SMTPFrom = value
	case "SMTPToken":
		common.SMTPToken = value
	case "ServerAddress":
		system_setting.ServerAddress = value
	case "WorkerUrl":
		system_setting.WorkerUrl = value
	case "WorkerValidKey":
		system_setting.WorkerValidKey = value
	case "PayAddress":
		operation_setting.PayAddress = value
	case "Chats":
		err = setting.UpdateChatsByJsonString(value)
	case setting.AssistantModelOptionKey:
		err = setting.UpdateAssistantModel(value)
	case setting.AssistantGroupOptionKey:
		err = setting.UpdateAssistantGroup(value)
	case setting.AssistantL1AutoApprovalUserIDsOptionKey:
		err = setting.UpdateAssistantL1AutoApprovalUserIDs(value)
	case setting.AssistantReasoningEffortOptionKey:
		err = setting.UpdateAssistantReasoningEffort(value)
	case setting.AssistantTemperatureOptionKey:
		err = setting.UpdateAssistantTemperature(value)
	case setting.AssistantMaxTokensOptionKey:
		err = setting.UpdateAssistantMaxTokens(value)
	case setting.AssistantMaxStepsOptionKey:
		err = setting.UpdateAssistantMaxSteps(value)
	case setting.AssistantTimeoutSecondsOptionKey:
		err = setting.UpdateAssistantTimeoutSeconds(value)
	case setting.AssistantCacheTTLMinutesOptionKey:
		err = setting.UpdateAssistantCacheTTLMinutes(value)
	case setting.AssistantPersonaOptionKey:
		err = setting.UpdateAssistantPersona(value)
	case setting.AssistantSystemPromptOptionKey:
		err = setting.UpdateAssistantSystemPrompt(value)
	case setting.AssistantSearchProviderOptionKey:
		err = setting.UpdateAssistantSearchProvider(value)
	case setting.AssistantSearchURLOptionKey:
		err = setting.UpdateAssistantSearchURL(value)
	case setting.AssistantSearchAPIKeyOptionKey:
		err = setting.UpdateAssistantSearchAPIKey(value)
	case setting.AssistantSearchMCPToolOptionKey:
		err = setting.UpdateAssistantSearchMCPTool(value)
	case setting.AssistantSkillsOptionKey:
		err = setting.UpdateAssistantSkills(value)
	case setting.AssistantSkillFilesOptionKey:
		err = setting.UpdateAssistantSkillFiles(value)
	case setting.AssistantReviewWindowDaysOptionKey:
		err = setting.UpdateAssistantReviewWindowDays(value)
	case setting.AssistantReviewIntervalHoursOptionKey:
		err = setting.UpdateAssistantReviewIntervalHours(value)
	case setting.AssistantReviewProbabilityOptionKey:
		err = setting.UpdateAssistantReviewProbability(value)
	case setting.AssistantReviewModelOptionKey:
		err = setting.UpdateAssistantReviewModel(value)
	case setting.AssistantReviewGroupPoliciesOptionKey:
		err = setting.UpdateAssistantReviewGroupPolicies(value)
	case setting.AssistantActiveRetentionDaysOptionKey:
		err = setting.UpdateAssistantActiveRetentionDays(value)
	case setting.AssistantArchivedRetentionDaysOptionKey:
		err = setting.UpdateAssistantArchivedRetentionDays(value)
	case setting.AssistantSecurityRetentionDaysOptionKey:
		err = setting.UpdateAssistantSecurityRetentionDays(value)
	case setting.AssistantRetentionIntervalHoursOptionKey:
		err = setting.UpdateAssistantRetentionIntervalHours(value)
	case "AutoGroups":
		err = setting.UpdateAutoGroupsByJsonString(value)
	case "MaxTokenAutoGroups":
		err = setting.UpdateMaxTokenAutoGroups(value)
	case "CustomCallbackAddress":
		operation_setting.CustomCallbackAddress = value
	case "EpayId":
		operation_setting.EpayId = value
	case "EpayKey":
		operation_setting.EpayKey = value
	case "Price":
		operation_setting.Price, _ = strconv.ParseFloat(value, 64)
	case "USDExchangeRate":
		operation_setting.USDExchangeRate, _ = strconv.ParseFloat(value, 64)
	case "MinTopUp":
		operation_setting.MinTopUp, _ = strconv.Atoi(value)
	case "StripeApiSecret":
		setting.StripeApiSecret = value
	case "StripeWebhookSecret":
		setting.StripeWebhookSecret = value
	case "StripePriceId":
		setting.StripePriceId = value
	case "StripeUnitPrice":
		setting.StripeUnitPrice, _ = strconv.ParseFloat(value, 64)
	case "StripeMinTopUp":
		setting.StripeMinTopUp, _ = strconv.Atoi(value)
	case "StripePromotionCodesEnabled":
		setting.StripePromotionCodesEnabled = value == "true"
	case "CreemApiKey":
		setting.CreemApiKey = value
	case "CreemProducts":
		setting.CreemProducts = value
	case "CreemTestMode":
		setting.CreemTestMode = value == "true"
	case "CreemWebhookSecret":
		setting.CreemWebhookSecret = value
	case "WaffoEnabled":
		setting.WaffoEnabled = value == "true"
	case "WaffoApiKey":
		setting.WaffoApiKey = value
	case "WaffoPrivateKey":
		setting.WaffoPrivateKey = value
	case "WaffoPublicCert":
		setting.WaffoPublicCert = value
	case "WaffoSandboxPublicCert":
		setting.WaffoSandboxPublicCert = value
	case "WaffoSandboxApiKey":
		setting.WaffoSandboxApiKey = value
	case "WaffoSandboxPrivateKey":
		setting.WaffoSandboxPrivateKey = value
	case "WaffoSandbox":
		setting.WaffoSandbox = value == "true"
	case "WaffoMerchantId":
		setting.WaffoMerchantId = value
	case "WaffoNotifyUrl":
		setting.WaffoNotifyUrl = value
	case "WaffoReturnUrl":
		setting.WaffoReturnUrl = value
	case "WaffoSubscriptionReturnUrl":
		setting.WaffoSubscriptionReturnUrl = value
	case "WaffoCurrency":
		setting.WaffoCurrency = value
	case "WaffoUnitPrice":
		setting.WaffoUnitPrice, _ = strconv.ParseFloat(value, 64)
	case "WaffoMinTopUp":
		setting.WaffoMinTopUp, _ = strconv.Atoi(value)
	case "WaffoPancakeMerchantID":
		setting.WaffoPancakeMerchantID = value
	case "WaffoPancakePrivateKey":
		setting.WaffoPancakePrivateKey = value
	case "WaffoPancakeReturnURL":
		setting.WaffoPancakeReturnURL = value
	case "WaffoPancakeStoreID":
		setting.WaffoPancakeStoreID = value
	case "WaffoPancakeProductID":
		setting.WaffoPancakeProductID = value
	case "WaffoPancakeUnitPrice":
		setting.WaffoPancakeUnitPrice, _ = strconv.ParseFloat(value, 64)
	case "WaffoPancakeMinTopUp":
		setting.WaffoPancakeMinTopUp, _ = strconv.Atoi(value)
	case "TopupGroupRatio":
		err = common.UpdateTopupGroupRatioByJSONString(value)
	case "GitHubClientId":
		common.GitHubClientId = value
	case "GitHubClientSecret":
		common.GitHubClientSecret = value
	case "LinuxDOClientId":
		common.LinuxDOClientId = value
	case "LinuxDOClientSecret":
		common.LinuxDOClientSecret = value
	case "LinuxDOMinimumTrustLevel":
		common.LinuxDOMinimumTrustLevel, _ = strconv.Atoi(value)
	case "Footer":
		common.Footer = value
	case "SystemName":
		common.SystemName = value
	case "Logo":
		common.Logo = value
	case "WeChatServerAddress":
		common.WeChatServerAddress = value
	case "WeChatServerToken":
		common.WeChatServerToken = value
	case "WeChatAccountQRCodeImageURL":
		common.WeChatAccountQRCodeImageURL = value
	case "TelegramBotToken":
		common.TelegramBotToken = value
	case "TelegramBotName":
		common.TelegramBotName = value
	case "TurnstileSiteKey":
		common.TurnstileSiteKey = value
	case "TurnstileSecretKey":
		common.TurnstileSecretKey = value
	case "QuotaForNewUser":
		common.QuotaForNewUser, _ = strconv.Atoi(value)
	case "QuotaForInviter":
		common.QuotaForInviter, _ = strconv.Atoi(value)
	case "QuotaForInvitee":
		common.QuotaForInvitee, _ = strconv.Atoi(value)
	case "QuotaRemindThreshold":
		common.QuotaRemindThreshold, _ = strconv.Atoi(value)
	case "PreConsumedQuota":
		common.PreConsumedQuota, _ = strconv.Atoi(value)
	case "ModelRequestRateLimitCount":
		setting.ModelRequestRateLimitCount, _ = strconv.Atoi(value)
	case "ModelRequestRateLimitDurationMinutes":
		setting.ModelRequestRateLimitDurationMinutes, _ = strconv.Atoi(value)
	case "ModelRequestRateLimitSuccessCount":
		setting.ModelRequestRateLimitSuccessCount, _ = strconv.Atoi(value)
	case "ModelRequestRateLimitGroup":
		err = setting.UpdateModelRequestRateLimitGroupByJSONString(value)
	case "RetryTimes":
		common.RetryTimes, _ = strconv.Atoi(value)
	case "DataExportInterval":
		common.DataExportInterval, _ = strconv.Atoi(value)
	case "DataExportDefaultTime":
		common.DataExportDefaultTime = value
	case "ModelRatio":
		err = ratio_setting.UpdateModelRatioByJSONString(value)
	case "GroupRatio":
		err = ratio_setting.UpdateGroupRatioByJSONString(value)
	case "GroupGroupRatio":
		err = ratio_setting.UpdateGroupGroupRatioByJSONString(value)
	case "group_ratio_setting.group_warnings":
		err = ratio_setting.UpdateGroupWarningsByJSONString(value)
	case "UserUsableGroups":
		err = setting.UpdateUserUsableGroupsByJSONString(value)
	case "CompletionRatio":
		err = ratio_setting.UpdateCompletionRatioByJSONString(value)
	case "ModelPrice":
		err = ratio_setting.UpdateModelPriceByJSONString(value)
	case "CacheRatio":
		err = ratio_setting.UpdateCacheRatioByJSONString(value)
	case "CreateCacheRatio":
		err = ratio_setting.UpdateCreateCacheRatioByJSONString(value)
	case "ImageRatio":
		err = ratio_setting.UpdateImageRatioByJSONString(value)
	case "AudioRatio":
		err = ratio_setting.UpdateAudioRatioByJSONString(value)
	case "AudioCompletionRatio":
		err = ratio_setting.UpdateAudioCompletionRatioByJSONString(value)
	case "TopUpLink":
		common.TopUpLink = value
	//case "ChatLink":
	//	common.ChatLink = value
	//case "ChatLink2":
	//	common.ChatLink2 = value
	case "ChannelDisableThreshold":
		common.ChannelDisableThreshold, _ = strconv.ParseFloat(value, 64)
	case "QuotaPerUnit":
		common.QuotaPerUnit, _ = strconv.ParseFloat(value, 64)
	case "SensitiveWords":
		setting.SensitiveWordsFromString(value)
	case setting.AdvancedSecurityActionOptionKey:
		err = setting.UpdateAdvancedSecurityAction(value)
	case setting.AdvancedSecurityRulesOptionKey:
		err = setting.UpdateAdvancedSecurityRules(value)
	case setting.AntiRelayBlockedCIDRsOptionKey:
		err = setting.UpdateAntiRelayBlockedCIDRs(value)
	case setting.AntiRelayTrustedProxyCIDRsOptionKey:
		err = setting.UpdateAntiRelayTrustedProxyCIDRs(value)
	case setting.IPAccessRoutingRulesOptionKey:
		err = setting.UpdateIPAccessRoutingRules(value)
	case "AutomaticDisableKeywords":
		operation_setting.AutomaticDisableKeywordsFromString(value)
	case "AutomaticDisableStatusCodes":
		err = operation_setting.AutomaticDisableStatusCodesFromString(value)
	case "AutomaticRetryStatusCodes":
		err = operation_setting.AutomaticRetryStatusCodesFromString(value)
	case "StreamCacheQueueLength":
		setting.StreamCacheQueueLength, _ = strconv.Atoi(value)
	case "PayMethods":
		err = operation_setting.UpdatePayMethodsByJsonString(value)
	case "WaffoPayMethods":
		// WaffoPayMethods is read directly from OptionMap via setting.GetWaffoPayMethods().
		// The value is already stored in OptionMap at the top of this function (line: common.OptionMap[key] = value).
		// No additional in-memory variable to update.
	}
	if err == nil && IsPricingOptionKey(key) {
		InvalidatePricingCache()
	}
	return err
}

func IsPricingOptionKey(key string) bool {
	if strings.HasPrefix(key, "billing_setting.") {
		return true
	}
	switch key {
	case "ModelRatio", "CompletionRatio", "ModelPrice", "CacheRatio", "CreateCacheRatio", "ImageRatio", "AudioRatio", "AudioCompletionRatio":
		return true
	default:
		return false
	}
}

// handleConfigUpdate 处理分层配置更新，返回是否已处理
func handleConfigUpdate(key, value string) bool {
	if key == operation_setting.ToolPriceOptionKey {
		operation_setting.LoadToolPricesFromJSONString(value)
		return true
	}

	parts := strings.SplitN(key, ".", 2)
	if len(parts) != 2 {
		return false // 不是分层配置
	}

	configName := parts[0]
	configKey := parts[1]

	// 获取配置对象
	cfg := config.GlobalConfig.Get(configName)
	if cfg == nil {
		return false // 未注册的配置
	}

	// 更新配置
	configMap := map[string]string{
		configKey: value,
	}
	config.UpdateConfigFromMap(cfg, configMap)

	// 特定配置的后处理
	if configName == "performance_setting" {
		performance_setting.UpdateAndSync()
	} else if configName == "billing_setting" {
		InvalidatePricingCache()
		ratio_setting.InvalidateExposedDataCache()
	}

	return true // 已处理
}
