package controller

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/billing_setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/config"
	"github.com/LIghtJUNction/api.lmm.best/setting/console_setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/system_setting"
	"github.com/gin-gonic/gin"
)

const (
	assistantAdminConfigChangeKind    = "config"
	assistantAdminPricingChangeKind   = "pricing"
	assistantAdminChannelChangeKind   = "channel"
	assistantAdminUserSkillChangeKind = "user_skill"
	assistantAdminModelSyncChangeKind = "model_sync"
	assistantAdminChangeLifetime      = 10 * time.Minute
	assistantAdminMaxConfigChanges    = 16
	assistantAdminMaxChannelChanges   = 12
	assistantAdminMaxChannelRows      = 1000
	assistantAdminMaxValueRunes       = 12_000
	assistantAdminMaxUserSkillChanges = 8
	assistantAdminMaxSkillMemoryViews = 32
)

// These are deliberately non-secret configuration surfaces.  Credentials,
// session keys, provider API keys, payment secrets, and arbitrary database or
// shell settings never enter the assistant write path.
var assistantAdminConfigAllowlist = map[string]string{
	"FileUploadPermission":                          "File upload permission level",
	"FileDownloadPermission":                        "File download permission level",
	"ImageUploadPermission":                         "Image upload permission level",
	"ImageDownloadPermission":                       "Image download permission level",
	"PasswordLoginEnabled":                          "Enable password login",
	"PasswordRegisterEnabled":                       "Enable password registration",
	"OAuthRegisterEnabled":                          "Enable OAuth registration",
	"EmailVerificationEnabled":                      "Require email verification",
	"RegisterEnabled":                               "Enable new registrations",
	"GitHubOAuthEnabled":                            "Enable GitHub OAuth",
	"LinuxDOOAuthEnabled":                           "Enable LinuxDO OAuth",
	"WeChatAuthEnabled":                             "Enable WeChat login",
	"TelegramOAuthEnabled":                          "Enable Telegram OAuth",
	"TurnstileCheckEnabled":                         "Enable Turnstile checks",
	"EmailDomainRestrictionEnabled":                 "Enable email-domain restrictions",
	"EmailAliasRestrictionEnabled":                  "Restrict email aliases",
	"EmailDomainWhitelist":                          "Allowed email domains",
	"AutomaticDisableChannelEnabled":                "Automatically disable unhealthy channels",
	"AutomaticEnableChannelEnabled":                 "Automatically re-enable recovered channels",
	"LogConsumeEnabled":                             "Record consumption logs",
	"DrawingEnabled":                                "Enable image generation",
	"TaskEnabled":                                   "Enable asynchronous tasks",
	"DataExportEnabled":                             "Enable data exports",
	"CheckSensitiveEnabled":                         "Enable sensitive-content checks",
	"CheckSensitiveOnPromptEnabled":                 "Check sensitive content in prompts",
	"StopOnSensitiveEnabled":                        "Stop requests on sensitive content",
	"SelfUseModeEnabled":                            "Enable self-use mode",
	"DemoSiteEnabled":                               "Enable demo-site mode",
	"MjNotifyEnabled":                               "Enable Midjourney notifications",
	"MjAccountFilterEnabled":                        "Filter Midjourney accounts",
	"MjModeClearEnabled":                            "Clear Midjourney mode state",
	"MjForwardUrlEnabled":                           "Enable Midjourney forwarding URLs",
	"MjActionCheckSuccessEnabled":                   "Check Midjourney action success",
	"WorkerAllowHttpImageRequestEnabled":            "Allow worker HTTP image requests",
	"AssistantEnabled":                              "Enable or disable the AI assistant",
	"AssistantModel":                                "Default assistant model ID",
	"AssistantReasoningEffort":                      "Default assistant reasoning effort",
	"AssistantStreamEnabled":                        "Stream assistant responses to the console",
	"AssistantTemperature":                          "Assistant response temperature",
	"AssistantMaxTokens":                            "Maximum assistant response tokens",
	"AssistantAgentLoopEnabled":                     "Enable safe assistant tool calls",
	"AssistantMaxSteps":                             "Maximum assistant tool-loop steps",
	"AssistantTimeoutSeconds":                       "Assistant tool-loop timeout",
	"AssistantCacheEnabled":                         "Enable identical-question caching",
	"AssistantCacheTTLMinutes":                      "Assistant cache lifetime",
	"AssistantPersona":                              "Assistant personality",
	"AssistantSystemPrompt":                         "Administrator operating instructions",
	"AssistantSearchProvider":                       "Assistant web-search provider",
	"AssistantSearchURL":                            "Assistant search endpoint",
	"AssistantSearchMCPTool":                        "Assistant MCP search tool name",
	"AssistantSkills":                               "Assistant skills and playbooks",
	"AssistantSkillFiles":                           "Administrator-managed platform skill files",
	"AssistantReviewEnabled":                        "Enable automatic assistant review",
	"AssistantReviewWindowDays":                     "Assistant review window in days",
	"AssistantReviewIntervalHours":                  "Assistant review interval in hours",
	"AssistantReviewProbability":                    "Per-request assistant review sampling probability",
	"AssistantReviewGroup":                          "Background assistant review routing group",
	"AssistantReviewModel":                          "Background assistant review model ID",
	"AssistantReviewReasoningEffort":                "Background assistant review reasoning effort",
	"AssistantReviewGroupPolicies":                  "Per-routing-group assistant review policies",
	"ServerAddress":                                 "Public service address",
	"WorkerUrl":                                     "Worker service URL",
	"CustomCallbackAddress":                         "Payment callback address",
	"SMTPSSLEnabled":                                "Enable SMTP over SSL",
	"SMTPStartTLSEnabled":                           "Enable SMTP STARTTLS",
	"SMTPInsecureSkipVerify":                        "Allow insecure SMTP certificate verification",
	"SMTPForceAuthLogin":                            "Require SMTP authentication",
	"SMTPServer":                                    "SMTP server host",
	"SMTPPort":                                      "SMTP server port",
	"SMTPFrom":                                      "SMTP sender address",
	"USDExchangeRate":                               "Real CNY per USD exchange rate",
	"TopUpPlatformUnitsPerCNY":                      "Wallet platform units purchased per CNY",
	"MinTopUp":                                      "Minimum top-up amount",
	"PayAddress":                                    "Public payment address",
	"StripeMinTopUp":                                "Stripe minimum top-up amount",
	"StripePriceId":                                 "Stripe public price ID",
	"StripePromotionCodesEnabled":                   "Enable Stripe promotion codes",
	"CreemTestMode":                                 "Enable Creem test mode",
	"CreemProducts":                                 "Creem public product mapping",
	"WaffoEnabled":                                  "Enable Waffo payments",
	"WaffoSandbox":                                  "Enable Waffo sandbox mode",
	"WaffoNotifyUrl":                                "Waffo payment callback URL",
	"WaffoReturnUrl":                                "Waffo payment return URL",
	"WaffoSubscriptionReturnUrl":                    "Waffo subscription return URL",
	"WaffoMinTopUp":                                 "Waffo minimum top-up amount",
	"WaffoPancakeReturnURL":                         "Waffo Pancake return URL",
	"WaffoPancakeMerchantID":                        "Waffo Pancake merchant ID",
	"WaffoPancakeStoreID":                           "Waffo Pancake public store ID",
	"WaffoPancakeProductID":                         "Waffo Pancake public product ID",
	"WaffoPancakeMinTopUp":                          "Waffo Pancake minimum top-up amount",
	"PayMethods":                                    "Legacy payment methods",
	"QuotaPerUnit":                                  "Quota-to-currency conversion unit",
	"ModelRequestRateLimitEnabled":                  "Enable model request rate limits",
	"ModelRequestRateLimitCount":                    "Model request rate-limit count",
	"ModelRequestRateLimitDurationMinutes":          "Model request rate-limit window",
	"ModelRequestRateLimitSuccessCount":             "Model request success threshold",
	"ModelRequestRateLimitGroup":                    "Per-group model request limits",
	"UserUsableGroups":                              "User-visible routing groups",
	"AutoGroups":                                    "Automatic routing groups",
	"DefaultUseAutoGroup":                           "Use automatic routing by default",
	"MaxTokenAutoGroups":                            "Maximum automatic-routing groups",
	"GroupRatio":                                    "Global user-group price multipliers",
	"GroupGroupRatio":                               "Per-user-group routing multipliers",
	"TopupGroupRatio":                               "Top-up group multipliers",
	"DisplayInCurrencyEnabled":                      "Display quota in currency",
	"DisplayTokenStatEnabled":                       "Display token statistics",
	"ExposeRatioEnabled":                            "Expose pricing ratios to clients",
	"Notice":                                        "System notice",
	"About":                                         "About page content",
	"HomePageContent":                               "Home page content",
	"Footer":                                        "Footer content",
	"SystemName":                                    "Displayed system name",
	"Logo":                                          "Displayed logo URL or path",
	"TopUpLink":                                     "Top-up link",
	"DefaultCollapseSidebar":                        "Collapse the main sidebar by default",
	"HeaderNavModules":                              "Header navigation modules",
	"SidebarModulesAdmin":                           "Administrator sidebar modules",
	"QuotaForNewUser":                               "New-user quota",
	"QuotaForInviter":                               "Invitation reward for inviter",
	"QuotaForInvitee":                               "Invitation reward for invitee",
	"OpenSourceBountyFeeRate":                       "Open-source bounty platform fee",
	"AdvancedSecurityEnabled":                       "Enable advanced security rules",
	"AdvancedSecurityOnPromptEnabled":               "Apply advanced security rules to prompts",
	"AdvancedSecurityAction":                        "Advanced security action",
	"AdvancedSecurityRules":                         "Advanced security rule set",
	"LinuxDOMinimumTrustLevel":                      "Minimum LinuxDO trust level",
	"QuotaRemindThreshold":                          "Quota reminder threshold",
	"PreConsumedQuota":                              "Pre-consumed request quota",
	"RetryTimes":                                    "Upstream retry count",
	"ChannelDisableThreshold":                       "Automatic channel-disable threshold",
	"DataExportInterval":                            "Data-export interval",
	"DataExportDefaultTime":                         "Default data-export time",
	"StreamCacheQueueLength":                        "Stream cache queue length",
	"SensitiveWords":                                "Sensitive-word list",
	"AutomaticDisableKeywords":                      "Automatic channel-disable keywords",
	"AutomaticDisableStatusCodes":                   "Automatic channel-disable status codes",
	"AutomaticRetryStatusCodes":                     "Automatic retry status codes",
	"general_setting.quota_display_type":            "Quota display type",
	"general_setting.custom_currency_symbol":        "Custom currency symbol",
	"general_setting.custom_currency_code":          "Custom currency ISO code",
	"general_setting.custom_currency_exchange_rate": "Custom currency exchange rate",
}

// Registered configuration modules are included only through this explicit
// module list. Field names are filtered below so a newly registered module
// cannot accidentally expose credentials to the assistant write path.
var assistantAdminConfigModuleAllowlist = map[string]string{
	"billing_setting":          "Billing",
	"channel_affinity_setting": "Channel affinity",
	"checkin_setting":          "Daily check-in",
	"claude":                   "Claude adapter",
	"console_setting":          "Console",
	"discord":                  "Discord login",
	"fetch_setting":            "Outbound-fetch security",
	"gemini":                   "Gemini adapter",
	"general_setting":          "General behavior",
	"global":                   "Global model behavior",
	"grok":                     "Grok adapter",
	"legal":                    "Legal content",
	"monitor_setting":          "Channel monitoring",
	"oidc":                     "OIDC login",
	"passkey":                  "Passkey login",
	"payment_setting":          "Payment offers",
	"perf_metrics_setting":     "Performance metrics",
	"performance_setting":      "Performance",
	"qwen":                     "Qwen adapter",
	"quota_setting":            "Quota behavior",
	"group_ratio_setting":      "Group routing ratios",
	"dynamic_pricing_setting":  "Dynamic pricing",
	"token_setting":            "Token behavior",
	"tool_price_setting":       "Tool pricing",
}

var assistantAdminBlockedConfigFieldFragments = []string{
	"api_key",
	"apikey",
	"authorization",
	"certificate",
	"client_id",
	"clientid",
	"client_secret",
	"cookie",
	"credential",
	"header",
	"private",
	"password",
	"secret",
	"session",
	"signing",
	"token",
	"webhook",
}

// Channel credentials, provider-specific settings, headers, proxies, and
// upstream endpoints deliberately stay outside the assistant surface. These
// fields are limited to routing metadata and the manual enable/disable
// operation that an administrator can already perform in the channel panel.
var assistantAdminChannelFieldLabels = map[string]string{
	"status":              "Enabled or manually disabled",
	"name":                "Channel display name",
	"test_model":          "Channel test model",
	"weight":              "Channel routing weight",
	"models":              "Enabled model list",
	"group":               "Routing groups",
	"model_mapping":       "Model name mapping",
	"status_code_mapping": "Status-code mapping",
	"priority":            "Channel routing priority",
	"auto_ban":            "Automatically ban unhealthy channel",
	"tag":                 "Channel tag",
	"remark":              "Channel remark",
}

type assistantAdminPricingChange struct {
	ModelID              string                      `json:"model_id"`
	Mode                 string                      `json:"mode"`
	Value                float64                     `json:"value"`
	CompletionRatio      *float64                    `json:"completion_ratio,omitempty"`
	CacheRatio           *float64                    `json:"cache_ratio,omitempty"`
	CreateCacheRatio     *float64                    `json:"create_cache_ratio,omitempty"`
	ImageRatio           *float64                    `json:"image_ratio,omitempty"`
	AudioRatio           *float64                    `json:"audio_ratio,omitempty"`
	AudioCompletionRatio *float64                    `json:"audio_completion_ratio,omitempty"`
	Expected             *assistantAdminPricingState `json:"expected,omitempty"`
}

type assistantAdminPricingState struct {
	Mode                 string  `json:"mode"`
	Value                float64 `json:"value"`
	CompletionRatio      float64 `json:"completion_ratio"`
	CacheRatio           float64 `json:"cache_ratio"`
	CreateCacheRatio     float64 `json:"create_cache_ratio"`
	ImageRatio           float64 `json:"image_ratio"`
	AudioRatio           float64 `json:"audio_ratio"`
	AudioCompletionRatio float64 `json:"audio_completion_ratio"`
}

type assistantAdminChangePayload struct {
	Kind           string                         `json:"kind"`
	ConfigChanges  map[string]string              `json:"config_changes,omitempty"`
	ConfigExpected map[string]string              `json:"config_expected,omitempty"`
	Channel        *assistantAdminChannelChange   `json:"channel,omitempty"`
	Pricing        *assistantAdminPricingChange   `json:"pricing,omitempty"`
	UserSkill      *assistantAdminUserSkillChange `json:"user_skill,omitempty"`
	ModelSync      *assistantAdminModelSyncChange `json:"model_sync,omitempty"`
}

type assistantAdminChannelChange struct {
	ChannelID int               `json:"channel_id"`
	Changes   map[string]string `json:"changes"`
	Expected  map[string]string `json:"expected"`
}

// assistantAdminUserSkillChange is stored only inside a short-lived,
// session-bound auth flow. It never comes from the browser during apply; the
// server rehydrates and validates this exact preview again before writing.
type assistantAdminUserSkillChange struct {
	TargetUserID int                          `json:"target_user_id"`
	Operation    string                       `json:"operation"`
	Memory       *assistantAdminMemoryChange  `json:"memory,omitempty"`
	Profile      *assistantAdminProfileChange `json:"profile,omitempty"`
}

type assistantAdminMemoryChange struct {
	ID                int64    `json:"id,omitempty"`
	ExpectedUpdatedAt int64    `json:"expected_updated_at,omitempty"`
	Title             string   `json:"title"`
	Content           string   `json:"content"`
	Tags              []string `json:"tags"`
	Enabled           bool     `json:"enabled"`
}

type assistantAdminProfileChange struct {
	ExpectedUpdatedAt int64    `json:"expected_updated_at,omitempty"`
	Exists            bool     `json:"exists"`
	ProfileKey        string   `json:"profile_key"`
	Tags              []string `json:"tags"`
	Strategy          string   `json:"strategy"`
	Enabled           bool     `json:"enabled"`
}

type assistantAdminConfigPreview struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	OldValue string `json:"old_value"`
	NewValue string `json:"new_value"`
}

type assistantAdminApplyInput struct {
	ConfirmationToken string `json:"confirmation_token"`
	Confirmed         bool   `json:"confirmed"`
}

func assistantAdminSafeConfigField(key string) (string, bool) {
	parts := strings.SplitN(key, ".", 2)
	if len(parts) != 2 {
		return "", false
	}
	moduleLabel, moduleAllowed := assistantAdminConfigModuleAllowlist[parts[0]]
	if !moduleAllowed {
		return "", false
	}
	field := strings.ToLower(strings.TrimSpace(parts[1]))
	if field == "" {
		return "", false
	}
	if parts[0] == "channel_affinity_setting" {
		switch field {
		case "enabled", "switch_on_success", "keep_on_channel_disabled", "max_entries", "default_ttl_seconds":
		default:
			return "", false
		}
	}
	if parts[0] == "payment_setting" {
		switch field {
		case "amount_options", "amount_discount":
		default:
			return "", false
		}
	}
	if parts[0] == "group_ratio_setting" {
		switch field {
		case "group_ratio", "group_group_ratio", "group_special_usable_group", "group_warnings":
		default:
			return "", false
		}
	}
	if parts[0] == "dynamic_pricing_setting" {
		switch field {
		case "enabled", "min_factor", "base_price_usd_per_million", "cost_floor_factor", "max_factor", "channel_costs":
		default:
			return "", false
		}
	}
	if parts[0] == "performance_setting" && field == "disk_cache_path" {
		return "", false
	}
	// These fields contain the word "token" but are operational numeric or
	// model-behavior settings, not credentials. Keep the broad deny fragment
	// check below for all other token-bearing names.
	safeTokenFields := map[string]struct{}{
		"claude.default_max_tokens":                        {},
		"claude.thinking_adapter_budget_tokens_percentage": {},
		"gemini.thinking_adapter_budget_tokens_percentage": {},
		"token_setting.max_user_tokens":                    {},
	}
	if _, allowed := safeTokenFields[strings.ToLower(key)]; allowed {
		return fmt.Sprintf("%s setting: %s", moduleLabel, parts[1]), true
	}
	for _, fragment := range assistantAdminBlockedConfigFieldFragments {
		if strings.Contains(field, fragment) {
			return "", false
		}
	}
	return fmt.Sprintf("%s setting: %s", moduleLabel, parts[1]), true
}

func assistantAdminAvailableConfigLabels() map[string]string {
	labels := make(map[string]string, len(assistantAdminConfigAllowlist)+16)
	for key, label := range assistantAdminConfigAllowlist {
		labels[key] = label
	}
	for key := range config.GlobalConfig.ExportAllConfigs() {
		if label, ok := assistantAdminSafeConfigField(key); ok {
			labels[key] = label
		}
	}
	return labels
}

func assistantAdminConfigLabel(key string) (string, bool) {
	if label, ok := assistantAdminConfigAllowlist[key]; ok {
		return label, true
	}
	label, ok := assistantAdminSafeConfigField(key)
	if !ok {
		return "", false
	}
	_, exported := config.GlobalConfig.ExportAllConfigs()[key]
	return label, exported
}

func assistantAdminUser(userID int) (*model.UserBase, error) {
	if userID <= 0 {
		return nil, errors.New("administrator account is unavailable")
	}
	user, err := model.GetUserCache(userID)
	if err != nil || user == nil || user.Role < common.RoleAdminUser {
		return nil, errors.New("administrator access is required")
	}
	return user, nil
}

func assistantRootUser(userID int) (*model.UserBase, error) {
	user, err := assistantAdminUser(userID)
	if err != nil || user.Role < common.RoleRootUser {
		return nil, errors.New("root administrator access is required")
	}
	return user, nil
}

func assistantAdminConfiguredGroups() map[string]string {
	groups := setting.GetUserUsableGroupsCopy()
	for group := range ratio_setting.GetGroupRatioCopy() {
		if group == "" || group == "auto" {
			continue
		}
		if _, exists := groups[group]; !exists {
			groups[group] = setting.GetUsableGroupDescription(group)
		}
	}
	return groups
}

func assistantAdminChannelFieldLabel(field string) (string, bool) {
	label, ok := assistantAdminChannelFieldLabels[strings.TrimSpace(field)]
	return label, ok
}

func assistantAdminChannelCurrentState(channel *model.Channel) map[string]string {
	state := map[string]string{
		"status":              strconv.Itoa(channel.Status),
		"name":                channel.Name,
		"test_model":          "",
		"weight":              strconv.FormatUint(uint64(channel.GetWeight()), 10),
		"models":              channel.Models,
		"group":               channel.Group,
		"model_mapping":       channel.GetModelMapping(),
		"status_code_mapping": channel.GetStatusCodeMapping(),
		"priority":            strconv.FormatInt(channel.GetPriority(), 10),
		"auto_ban":            strconv.FormatBool(channel.GetAutoBan()),
		"tag":                 channel.GetTag(),
		"remark":              "",
	}
	if channel.TestModel != nil {
		state["test_model"] = *channel.TestModel
	}
	if channel.Remark != nil {
		state["remark"] = *channel.Remark
	}
	return state
}

func assistantAdminChannelState(channelID int) (map[string]string, *model.Channel, error) {
	if channelID <= 0 {
		return nil, nil, errors.New("channel_id must be a positive integer")
	}
	if model.DB == nil {
		return nil, nil, errors.New("channel database is unavailable")
	}
	channel, err := model.GetChannelById(channelID, false)
	if err != nil || channel == nil {
		if err != nil {
			return nil, nil, fmt.Errorf("channel %d could not be loaded: %w", channelID, err)
		}
		return nil, nil, fmt.Errorf("channel %d could not be loaded", channelID)
	}
	return assistantAdminChannelCurrentState(channel), channel, nil
}

func assistantAdminChannelViews() ([]map[string]any, int64, bool, error) {
	if model.DB == nil {
		return nil, 0, false, errors.New("channel database is unavailable")
	}
	var total int64
	if err := model.DB.Model(&model.Channel{}).Count(&total).Error; err != nil {
		return nil, 0, false, err
	}
	channels := make([]model.Channel, 0, assistantAdminMaxChannelRows)
	if err := model.DB.Model(&model.Channel{}).
		Select("id", "type", "status", "name", "test_model", "weight", "models", "group", "model_mapping", "status_code_mapping", "priority", "auto_ban", "tag", "remark").
		Order("id ASC").
		Limit(assistantAdminMaxChannelRows).
		Find(&channels).Error; err != nil {
		return nil, 0, false, err
	}
	views := make([]map[string]any, 0, len(channels))
	for index := range channels {
		channel := &channels[index]
		state := assistantAdminChannelCurrentState(channel)
		view := map[string]any{
			"id":                  channel.Id,
			"type":                channel.Type,
			"status":              channel.Status,
			"name":                channel.Name,
			"test_model":          state["test_model"],
			"weight":              channel.GetWeight(),
			"models":              channel.Models,
			"group":               channel.Group,
			"model_mapping":       state["model_mapping"],
			"status_code_mapping": state["status_code_mapping"],
			"priority":            channel.GetPriority(),
			"auto_ban":            channel.GetAutoBan(),
			"tag":                 state["tag"],
			"remark":              state["remark"],
		}
		views = append(views, view)
	}
	return views, total, total > int64(len(views)), nil
}

func assistantAdminChannelChanges(input map[string]any) (int, map[string]string, error) {
	channelNumber, ok := inputNumber(input, "channel_id")
	if !ok || channelNumber < 1 || math.Trunc(channelNumber) != channelNumber || channelNumber > 2_147_483_647 {
		return 0, nil, errors.New("channel_id must be a positive integer")
	}
	raw, exists := input["changes"]
	if !exists {
		return 0, nil, errors.New("changes are required")
	}
	changes, ok := raw.(map[string]any)
	if !ok {
		if encoded, encodedOK := raw.(string); encodedOK {
			if err := json.Unmarshal([]byte(encoded), &changes); err != nil {
				return 0, nil, errors.New("changes must be a JSON object")
			}
		} else {
			return 0, nil, errors.New("changes must be a JSON object")
		}
	}
	if len(changes) == 0 || len(changes) > assistantAdminMaxChannelChanges {
		return 0, nil, fmt.Errorf("choose between 1 and %d channel changes", assistantAdminMaxChannelChanges)
	}
	result := make(map[string]string, len(changes))
	for field, rawValue := range changes {
		field = strings.TrimSpace(field)
		if _, ok := assistantAdminChannelFieldLabel(field); !ok {
			return 0, nil, fmt.Errorf("channel field %q is not available to the assistant", field)
		}
		value, err := assistantAdminValueString(rawValue)
		if err != nil {
			return 0, nil, fmt.Errorf("%s: %w", field, err)
		}
		if field == "auto_ban" {
			parsed, parseErr := strconv.ParseBool(value)
			if parseErr != nil {
				return 0, nil, fmt.Errorf("%s: must be a boolean value", field)
			}
			value = strconv.FormatBool(parsed)
		}
		if err := validateAssistantAdminChannelValue(field, value); err != nil {
			return 0, nil, fmt.Errorf("%s: %w", field, err)
		}
		result[field] = value
	}
	return int(channelNumber), result, nil
}

func validateAssistantAdminChannelValue(field, value string) error {
	if len([]rune(value)) > assistantAdminMaxValueRunes {
		return fmt.Errorf("value must be at most %d characters", assistantAdminMaxValueRunes)
	}
	switch field {
	case "status":
		status, err := strconv.Atoi(value)
		if err != nil || (status != common.ChannelStatusEnabled && status != common.ChannelStatusManuallyDisabled) {
			return errors.New("status must be enabled (1) or manually disabled (2)")
		}
	case "name", "test_model":
		if len([]rune(value)) > 255 {
			return errors.New("channel name and test model must be at most 255 characters")
		}
	case "weight":
		weight, err := strconv.ParseUint(value, 10, strconv.IntSize)
		if err != nil || weight > 1_000_000_000 {
			return errors.New("weight must be a non-negative integer no greater than 1000000000")
		}
	case "models":
		if strings.IndexByte(value, '\x00') >= 0 {
			return errors.New("models cannot contain NUL characters")
		}
	case "group":
		if strings.TrimSpace(value) == "" || len([]rune(value)) > 64 {
			return errors.New("group must be non-empty and at most 64 characters")
		}
		for _, group := range strings.Split(value, ",") {
			if strings.TrimSpace(group) == "" {
				return errors.New("group list cannot contain empty names")
			}
		}
	case "model_mapping":
		if strings.TrimSpace(value) == "" {
			return nil
		}
		var mapping map[string]string
		if err := json.Unmarshal([]byte(value), &mapping); err != nil {
			return errors.New("model_mapping must be a JSON object of string-to-string mappings")
		}
		for source, target := range mapping {
			if strings.TrimSpace(source) == "" || strings.TrimSpace(target) == "" {
				return errors.New("model_mapping cannot contain empty names")
			}
		}
	case "status_code_mapping":
		if strings.TrimSpace(value) == "" {
			return nil
		}
		var mapping map[string]any
		if err := json.Unmarshal([]byte(value), &mapping); err != nil {
			return errors.New("status_code_mapping must be a JSON object")
		}
		for source, target := range mapping {
			from, err := strconv.Atoi(source)
			if err != nil || from < 100 || from > 599 {
				return errors.New("status_code_mapping source codes must be HTTP status codes")
			}
			var targetNumber float64
			switch typed := target.(type) {
			case float64:
				targetNumber = typed
			case string:
				targetNumber, err = strconv.ParseFloat(strings.TrimSpace(typed), 64)
			default:
				return errors.New("status_code_mapping target codes must be integers")
			}
			if err != nil || math.IsNaN(targetNumber) || math.IsInf(targetNumber, 0) || math.Trunc(targetNumber) != targetNumber || targetNumber < 100 || targetNumber > 599 {
				return errors.New("status_code_mapping target codes must be HTTP status codes")
			}
		}
	case "priority":
		priority, err := strconv.ParseInt(value, 10, 64)
		if err != nil || priority < -1_000_000_000 || priority > 1_000_000_000 {
			return errors.New("priority must be between -1000000000 and 1000000000")
		}
	case "auto_ban":
		if _, err := strconv.ParseBool(value); err != nil {
			return errors.New("auto_ban must be a boolean value")
		}
	case "tag", "remark":
		if len([]rune(value)) > 255 {
			return errors.New("tag and remark must be at most 255 characters")
		}
	}
	return nil
}

func assistantAdminSession(c *gin.Context) (string, bool) {
	if c == nil || !requireAssistantBrowserSession(c) {
		return "", false
	}
	sessionID := strings.TrimSpace(c.GetString("session_id"))
	return sessionID, sessionID != ""
}

func sortedAssistantAdminConfigKeys() []string {
	labels := assistantAdminAvailableConfigLabels()
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedAssistantAdminChangeKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func assistantAdminCurrentOptions(keys []string) map[string]string {
	current := make(map[string]string, len(keys))
	exported := config.GlobalConfig.ExportAllConfigs()
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	for _, key := range keys {
		value, exists := common.OptionMap[key]
		if exists {
			current[key] = value
			continue
		}
		current[key] = exported[key]
	}
	return current
}

func assistantAdminValueString(value any) (string, error) {
	var result string
	switch typed := value.(type) {
	case string:
		result = strings.TrimSpace(typed)
	case bool:
		result = strconv.FormatBool(typed)
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) {
			return "", errors.New("configuration value must be finite")
		}
		result = strconv.FormatFloat(typed, 'f', -1, 64)
	case json.Number:
		result = strings.TrimSpace(typed.String())
	case map[string]any, []any:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return "", errors.New("configuration value must be valid JSON")
		}
		result = string(encoded)
	default:
		return "", errors.New("configuration values must be strings, numbers, booleans, or JSON objects")
	}
	if len([]rune(result)) > assistantAdminMaxValueRunes {
		return "", fmt.Errorf("configuration values must be at most %d characters", assistantAdminMaxValueRunes)
	}
	return result, nil
}

func assistantAdminConfigFieldKind(key string) (reflect.Kind, bool) {
	parts := strings.SplitN(key, ".", 2)
	if len(parts) != 2 {
		return reflect.Invalid, false
	}
	cfg := config.GlobalConfig.Get(parts[0])
	if cfg == nil {
		return reflect.Invalid, false
	}
	value := reflect.ValueOf(cfg)
	if value.Kind() == reflect.Ptr {
		if value.IsNil() {
			return reflect.Invalid, false
		}
		value = value.Elem()
	}
	if value.Kind() != reflect.Struct {
		return reflect.Invalid, false
	}
	typ := value.Type()
	for index := 0; index < value.NumField(); index++ {
		field := typ.Field(index)
		if !field.IsExported() {
			continue
		}
		jsonName := strings.Split(field.Tag.Get("json"), ",")[0]
		if jsonName == "" || jsonName == "-" {
			jsonName = field.Name
		}
		if jsonName == parts[1] || field.Name == parts[1] {
			return field.Type.Kind(), true
		}
	}
	return reflect.Invalid, false
}

func validateAssistantAdminConfigFieldType(key, value string) error {
	kind, ok := assistantAdminConfigFieldKind(key)
	if !ok {
		return nil
	}
	switch kind {
	case reflect.Bool:
		if _, err := strconv.ParseBool(value); err != nil {
			return errors.New("must be a boolean value")
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if _, err := strconv.ParseInt(value, 10, 64); err != nil {
			return errors.New("must be an integer value")
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if _, err := strconv.ParseUint(value, 10, 64); err != nil {
			return errors.New("must be a non-negative integer value")
		}
	case reflect.Float32, reflect.Float64:
		number, err := strconv.ParseFloat(value, 64)
		if err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
			return errors.New("must be a finite number")
		}
	case reflect.Map, reflect.Slice, reflect.Array, reflect.Struct, reflect.Ptr:
		if !json.Valid([]byte(value)) {
			return errors.New("must be valid JSON")
		}
	}
	return nil
}

func assistantAdminConfigChanges(input map[string]any) (map[string]string, error) {
	raw, ok := input["changes"]
	if !ok {
		return nil, errors.New("changes are required")
	}
	changes, ok := raw.(map[string]any)
	if !ok {
		if encoded, encodedOK := raw.(string); encodedOK {
			if err := json.Unmarshal([]byte(encoded), &changes); err != nil {
				return nil, errors.New("changes must be a JSON object")
			}
		} else {
			return nil, errors.New("changes must be a JSON object")
		}
	}
	if len(changes) == 0 || len(changes) > assistantAdminMaxConfigChanges {
		return nil, fmt.Errorf("choose between 1 and %d configuration changes", assistantAdminMaxConfigChanges)
	}
	result := make(map[string]string, len(changes))
	labels := assistantAdminAvailableConfigLabels()
	for key, value := range changes {
		key = strings.TrimSpace(key)
		if _, allowed := labels[key]; !allowed {
			return nil, fmt.Errorf("configuration key %q is not available to the assistant", key)
		}
		normalized, err := assistantAdminValueString(value)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
		if !strings.HasPrefix(key, "dynamic_pricing_setting.") {
			if err := validateAssistantAdminConfigValue(key, normalized); err != nil {
				return nil, fmt.Errorf("%s: %w", key, err)
			}
		}
		result[key] = normalized
	}
	if hasAssistantAdminDynamicPricingChange(result) {
		if err := model.ValidateOptionValues(result); err != nil {
			return nil, fmt.Errorf("dynamic pricing: %w", err)
		}
	}
	return result, nil
}

func hasAssistantAdminDynamicPricingChange(values map[string]string) bool {
	for key := range values {
		if strings.HasPrefix(key, "dynamic_pricing_setting.") {
			return true
		}
	}
	return false
}

func validateAssistantAdminDynamicPricingChange(values map[string]string) error {
	if !hasAssistantAdminDynamicPricingChange(values) {
		return nil
	}
	return model.ValidateOptionValues(values)
}

func validateAssistantAdminConfigDependencies(key, value string) error {
	if value != "true" {
		return nil
	}
	switch key {
	case "GitHubOAuthEnabled":
		if strings.TrimSpace(common.GitHubClientId) == "" || strings.TrimSpace(common.GitHubClientSecret) == "" {
			return errors.New("GitHub OAuth credentials must be configured before enabling it")
		}
	case "LinuxDOOAuthEnabled":
		if strings.TrimSpace(common.LinuxDOClientId) == "" || strings.TrimSpace(common.LinuxDOClientSecret) == "" {
			return errors.New("LinuxDO OAuth credentials must be configured before enabling it")
		}
	case "WeChatAuthEnabled":
		if strings.TrimSpace(common.WeChatServerAddress) == "" || strings.TrimSpace(common.WeChatServerToken) == "" {
			return errors.New("WeChat login settings must be configured before enabling it")
		}
	case "TelegramOAuthEnabled":
		if strings.TrimSpace(common.TelegramBotToken) == "" {
			return errors.New("Telegram bot token must be configured before enabling it")
		}
	case "TurnstileCheckEnabled":
		if strings.TrimSpace(common.TurnstileSiteKey) == "" || strings.TrimSpace(common.TurnstileSecretKey) == "" {
			return errors.New("Turnstile keys must be configured before enabling it")
		}
	case "EmailDomainRestrictionEnabled":
		if len(common.EmailDomainWhitelist) == 0 {
			return errors.New("at least one email domain must be configured before enabling restrictions")
		}
	case "discord.enabled":
		settings := system_setting.GetDiscordSettings()
		if settings == nil || strings.TrimSpace(settings.ClientId) == "" || strings.TrimSpace(settings.ClientSecret) == "" {
			return errors.New("Discord OAuth credentials must be configured before enabling it")
		}
	case "oidc.enabled":
		settings := system_setting.GetOIDCSettings()
		if settings == nil || strings.TrimSpace(settings.ClientId) == "" || strings.TrimSpace(settings.ClientSecret) == "" {
			return errors.New("OIDC credentials must be configured before enabling it")
		}
	}
	return nil
}

func validateAssistantAdminConfigValue(key, value string) error {
	if err := model.ValidateOptionValue(key, value); err != nil {
		return err
	}
	if err := validateAssistantAdminConfigFieldType(key, value); err != nil {
		return err
	}
	if err := validateAssistantAdminConfigDependencies(key, value); err != nil {
		return err
	}
	switch key {
	case "AssistantEnabled", "AssistantAgentLoopEnabled", "AssistantCacheEnabled", "ModelRequestRateLimitEnabled", "DefaultUseAutoGroup", "DisplayInCurrencyEnabled", "DisplayTokenStatEnabled", "ExposeRatioEnabled", "DefaultCollapseSidebar", "PasswordLoginEnabled", "PasswordRegisterEnabled", "OAuthRegisterEnabled", "EmailVerificationEnabled", "RegisterEnabled", "GitHubOAuthEnabled", "LinuxDOOAuthEnabled", "WeChatAuthEnabled", "TelegramOAuthEnabled", "TurnstileCheckEnabled", "EmailDomainRestrictionEnabled", "EmailAliasRestrictionEnabled", "AutomaticDisableChannelEnabled", "AutomaticEnableChannelEnabled", "LogConsumeEnabled", "DrawingEnabled", "TaskEnabled", "DataExportEnabled", "CheckSensitiveEnabled", "CheckSensitiveOnPromptEnabled", "StopOnSensitiveEnabled", "SelfUseModeEnabled", "DemoSiteEnabled", "MjNotifyEnabled", "MjAccountFilterEnabled", "MjModeClearEnabled", "MjForwardUrlEnabled", "MjActionCheckSuccessEnabled", "WorkerAllowHttpImageRequestEnabled", "SMTPSSLEnabled", "SMTPStartTLSEnabled", "SMTPInsecureSkipVerify", "SMTPForceAuthLogin", "StripePromotionCodesEnabled", "CreemTestMode", "WaffoEnabled", "WaffoSandbox", "AdvancedSecurityEnabled", "AdvancedSecurityOnPromptEnabled":
		if _, err := strconv.ParseBool(value); err != nil {
			return errors.New("must be a boolean value")
		}
	case "FileUploadPermission", "FileDownloadPermission", "ImageUploadPermission", "ImageDownloadPermission":
		permission, err := strconv.Atoi(value)
		if err != nil || permission < common.RoleGuestUser || permission > common.RoleRootUser {
			return errors.New("permission level must be between guest and root")
		}
	case "QuotaForNewUser", "QuotaForInviter", "QuotaForInvitee", "QuotaRemindThreshold", "PreConsumedQuota", "RetryTimes", "DataExportInterval", "StreamCacheQueueLength", "LinuxDOMinimumTrustLevel", "MinTopUp", "StripeMinTopUp", "WaffoMinTopUp", "WaffoPancakeMinTopUp":
		integer, err := strconv.Atoi(value)
		if err != nil || integer < 0 || integer > 1_000_000_000 {
			return errors.New("must be an integer between 0 and 1000000000")
		}
		if key == "LinuxDOMinimumTrustLevel" && integer > 4 {
			return errors.New("LinuxDO minimum trust level must be between 0 and 4")
		}
	case "SMTPPort":
		port, err := strconv.Atoi(value)
		if err != nil || port < 1 || port > 65535 {
			return errors.New("SMTP port must be between 1 and 65535")
		}
	case "USDExchangeRate", "TopUpPlatformUnitsPerCNY":
		amount, err := strconv.ParseFloat(value, 64)
		if err != nil || amount <= 0 || amount > 1_000_000_000 || math.IsNaN(amount) || math.IsInf(amount, 0) {
			return errors.New("payment price must be a positive finite number no greater than 1000000000")
		}
	case "SMTPServer", "SMTPFrom", "StripePriceId", "WaffoPancakeMerchantID", "WaffoPancakeStoreID", "WaffoPancakeProductID":
		if len([]rune(value)) > 512 {
			return errors.New("payment and SMTP settings must be at most 512 characters")
		}
	case "CreemProducts":
		var products []map[string]any
		if err := json.Unmarshal([]byte(value), &products); err != nil || len(products) > 100 {
			return errors.New("Creem products must be a JSON array with at most 100 entries")
		}
		for _, product := range products {
			productID, ok := product["productId"].(string)
			if !ok || strings.TrimSpace(productID) == "" || len([]rune(productID)) > 128 {
				return errors.New("each Creem product must have a non-empty productId")
			}
			price, ok := product["price"].(float64)
			if !ok || price < 0 || math.IsNaN(price) || math.IsInf(price, 0) {
				return errors.New("each Creem product must have a finite non-negative price")
			}
			currency, ok := product["currency"].(string)
			if !ok || len([]rune(strings.TrimSpace(currency))) < 3 || len([]rune(currency)) > 8 {
				return errors.New("each Creem product must have a currency")
			}
			quota, ok := product["quota"].(float64)
			if !ok || quota < 0 || quota > 1e12 || math.IsNaN(quota) || math.IsInf(quota, 0) || math.Trunc(quota) != quota {
				return errors.New("each Creem product must have a non-negative integer quota")
			}
		}
	case "ChannelDisableThreshold":
		threshold, err := strconv.ParseFloat(value, 64)
		if err != nil || threshold < 0 || threshold > 1_000_000 || math.IsNaN(threshold) || math.IsInf(threshold, 0) {
			return errors.New("must be a finite threshold between 0 and 1000000")
		}
	case "AutomaticDisableStatusCodes", "AutomaticRetryStatusCodes":
		if _, err := operation_setting.ParseHTTPStatusCodeRanges(value); err != nil {
			return err
		}
	case "ModelRequestRateLimitCount":
		count, err := strconv.Atoi(value)
		if err != nil || count < 0 || count > 1_000_000_000 {
			return errors.New("request rate-limit count must be between 0 and 1000000000")
		}
	case "ModelRequestRateLimitDurationMinutes":
		minutes, err := strconv.Atoi(value)
		if err != nil || minutes < 1 || minutes > 10080 {
			return errors.New("request rate-limit duration must be between 1 and 10080 minutes")
		}
	case "ModelRequestRateLimitSuccessCount":
		count, err := strconv.Atoi(value)
		if err != nil || count < 1 || count > 1_000_000_000 {
			return errors.New("request success threshold must be between 1 and 1000000000")
		}
	case "AutoGroups":
		var groups []string
		if err := json.Unmarshal([]byte(value), &groups); err != nil || len(groups) == 0 {
			return errors.New("must be a non-empty JSON array of routing groups")
		}
		for _, group := range groups {
			if strings.TrimSpace(group) == "" {
				return errors.New("routing groups cannot contain empty names")
			}
		}
	case "GroupRatio":
		if err := ratio_setting.CheckGroupRatio(value); err != nil {
			return err
		}
	case "group_ratio_setting.group_warnings":
		if err := ratio_setting.CheckGroupWarnings(value); err != nil {
			return err
		}
	case "dynamic_pricing_setting.enabled", "dynamic_pricing_setting.min_factor", "dynamic_pricing_setting.base_price_usd_per_million", "dynamic_pricing_setting.cost_floor_factor", "dynamic_pricing_setting.max_factor", "dynamic_pricing_setting.channel_costs":
		// The model-level validator above checks the field shape. Related
		// dynamic-pricing fields are validated together before a preview is
		// issued, so enabling the feature can include its channel costs in the
		// same atomic change.
		return nil
	case "group_ratio_setting.group_ratio":
		var ratios map[string]float64
		if err := json.Unmarshal([]byte(value), &ratios); err != nil || len(ratios) > assistantAdminMaxChannelRows {
			return errors.New("group_ratio must be a JSON object with at most 1000 groups")
		}
		for group, ratio := range ratios {
			if strings.TrimSpace(group) == "" || ratio < 0 || math.IsNaN(ratio) || math.IsInf(ratio, 0) {
				return errors.New("group ratios must have non-empty names and non-negative finite values")
			}
		}
	case "GroupGroupRatio":
		var groups map[string]map[string]float64
		if err := json.Unmarshal([]byte(value), &groups); err != nil {
			return errors.New("must be a JSON object of non-negative group ratios")
		}
		for userGroup, ratios := range groups {
			for routeGroup, ratio := range ratios {
				if ratio < 0 || math.IsNaN(ratio) || math.IsInf(ratio, 0) {
					return fmt.Errorf("ratio %s.%s must be non-negative and finite", userGroup, routeGroup)
				}
			}
		}
	case "group_ratio_setting.group_group_ratio":
		var ratios map[string]map[string]float64
		if err := json.Unmarshal([]byte(value), &ratios); err != nil || len(ratios) > assistantAdminMaxChannelRows {
			return errors.New("group_group_ratio must be a JSON object with at most 1000 groups")
		}
		for userGroup, routeRatios := range ratios {
			if strings.TrimSpace(userGroup) == "" {
				return errors.New("user group names cannot be empty")
			}
			for routeGroup, ratio := range routeRatios {
				if strings.TrimSpace(routeGroup) == "" || ratio < 0 || math.IsNaN(ratio) || math.IsInf(ratio, 0) {
					return errors.New("group_group_ratio values must be non-negative finite numbers")
				}
			}
		}
	case "group_ratio_setting.group_special_usable_group":
		var groups map[string]map[string]string
		if err := json.Unmarshal([]byte(value), &groups); err != nil || len(groups) > assistantAdminMaxChannelRows {
			return errors.New("group_special_usable_group must be a JSON object with at most 1000 groups")
		}
		for userGroup, routeGroups := range groups {
			if strings.TrimSpace(userGroup) == "" {
				return errors.New("user group names cannot be empty")
			}
			for routeGroup, description := range routeGroups {
				if strings.TrimSpace(routeGroup) == "" || len([]rune(description)) > 255 {
					return errors.New("special usable groups must have valid names and descriptions")
				}
			}
		}
	case "UserUsableGroups":
		var groups map[string]string
		if err := json.Unmarshal([]byte(value), &groups); err != nil || len(groups) == 0 {
			return errors.New("must be a non-empty JSON object of user groups")
		}
	case "QuotaPerUnit":
		amount, err := strconv.ParseFloat(value, 64)
		if err != nil || amount <= 0 || math.IsNaN(amount) || math.IsInf(amount, 0) {
			return errors.New("must be a positive finite number")
		}
	case "TopupGroupRatio":
		var ratios map[string]float64
		if err := json.Unmarshal([]byte(value), &ratios); err != nil {
			return errors.New("must be a JSON object of non-negative top-up ratios")
		}
		for group, ratio := range ratios {
			if ratio < 0 || math.IsNaN(ratio) || math.IsInf(ratio, 0) {
				return fmt.Errorf("top-up ratio %s must be non-negative and finite", group)
			}
		}
	case "payment_setting.amount_options":
		var amounts []int
		if err := json.Unmarshal([]byte(value), &amounts); err != nil || len(amounts) > 100 {
			return errors.New("amount_options must be a JSON array with at most 100 entries")
		}
		seen := make(map[int]struct{}, len(amounts))
		for _, amount := range amounts {
			if amount <= 0 || amount > 1_000_000_000 {
				return errors.New("top-up amounts must be positive integers no greater than 1000000000")
			}
			if _, exists := seen[amount]; exists {
				return errors.New("top-up amounts cannot contain duplicates")
			}
			seen[amount] = struct{}{}
		}
	case "payment_setting.amount_discount":
		var discounts map[int]float64
		if err := json.Unmarshal([]byte(value), &discounts); err != nil || len(discounts) > 100 {
			return errors.New("amount_discount must be a JSON object with at most 100 entries")
		}
		for amount, discount := range discounts {
			if amount <= 0 || discount <= 0 || discount > 1 || math.IsNaN(discount) || math.IsInf(discount, 0) {
				return errors.New("top-up discounts must map positive amounts to finite values between 0 and 1")
			}
		}
	case "PayMethods":
		var methods []map[string]string
		if err := json.Unmarshal([]byte(value), &methods); err != nil || len(methods) > 32 {
			return errors.New("PayMethods must be a JSON array with at most 32 entries")
		}
		for _, method := range methods {
			methodType := strings.TrimSpace(method["type"])
			if methodType == "" || len([]rune(methodType)) > 64 {
				return errors.New("each payment method must have a non-empty type")
			}
			for key, methodValue := range method {
				switch key {
				case "name", "icon", "type", "min_topup":
				default:
					return fmt.Errorf("payment method field %q is not available to the assistant", key)
				}
				if len([]rune(methodValue)) > 255 {
					return errors.New("payment method fields must be at most 255 characters")
				}
			}
			if minTopUp := strings.TrimSpace(method["min_topup"]); minTopUp != "" {
				amount, err := strconv.Atoi(minTopUp)
				if err != nil || amount < 0 || amount > 1_000_000_000 {
					return errors.New("payment method min_topup must be a non-negative integer")
				}
			}
		}
	case "ModelRequestRateLimitGroup":
		if err := setting.CheckModelRequestRateLimitGroup(value); err != nil {
			return err
		}
	case "billing_setting.billing_mode":
		var modes map[string]string
		if err := json.Unmarshal([]byte(value), &modes); err != nil {
			return errors.New("billing_mode must be a JSON object")
		}
		for modelID, mode := range modes {
			if strings.TrimSpace(modelID) == "" {
				return errors.New("billing_mode cannot contain an empty model ID")
			}
			if mode != "" && mode != billing_setting.BillingModeRatio && mode != billing_setting.BillingModeTieredExpr {
				return fmt.Errorf("billing mode for %s must be ratio or tiered_expr", modelID)
			}
		}
	case "billing_setting.billing_expr":
		var expressions map[string]string
		if err := json.Unmarshal([]byte(value), &expressions); err != nil {
			return errors.New("billing_expr must be a JSON object")
		}
		if len(expressions) > assistantAdminMaxChannelRows {
			return fmt.Errorf("billing_expr may contain at most %d model expressions", assistantAdminMaxChannelRows)
		}
		for modelID, expression := range expressions {
			if strings.TrimSpace(modelID) == "" {
				return errors.New("billing_expr cannot contain an empty model ID")
			}
			if strings.TrimSpace(expression) == "" {
				continue
			}
			if err := billing_setting.SmokeTestExpr(expression); err != nil {
				return fmt.Errorf("billing expression for %s is invalid: %w", modelID, err)
			}
		}
	case "general_setting.quota_display_type":
		switch value {
		case "USD", "CNY", "TOKENS", "CUSTOM":
		default:
			return errors.New("quota display type must be USD, CNY, TOKENS, or CUSTOM")
		}
	case "general_setting.custom_currency_exchange_rate":
		rate, err := strconv.ParseFloat(value, 64)
		if err != nil || rate <= 0 || math.IsNaN(rate) || math.IsInf(rate, 0) {
			return errors.New("custom currency exchange rate must be positive and finite")
		}
	case "general_setting.custom_currency_symbol":
		if len([]rune(value)) > 8 {
			return errors.New("custom currency symbol must be at most 8 characters")
		}
	case "general_setting.custom_currency_code":
		return operation_setting.ValidateCustomCurrencyCode(value)
	case "HeaderNavModules", "SidebarModulesAdmin":
		if strings.TrimSpace(value) != "" {
			var decoded map[string]any
			if err := json.Unmarshal([]byte(value), &decoded); err != nil {
				return errors.New("navigation settings must be a JSON object")
			}
		}
	case "ServerAddress", "WorkerUrl", "CustomCallbackAddress", "PayAddress", "WaffoNotifyUrl", "WaffoReturnUrl", "WaffoSubscriptionReturnUrl", "WaffoPancakeReturnURL":
		if strings.TrimSpace(value) != "" {
			parsed, err := url.ParseRequestURI(value)
			if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
				return errors.New("must be an absolute HTTP or HTTPS URL without embedded credentials")
			}
		}
	case "console_setting.api_info", "console_setting.announcements", "console_setting.faq", "console_setting.uptime_kuma_groups":
		fieldName := map[string]string{
			"console_setting.api_info":           "ApiInfo",
			"console_setting.announcements":      "Announcements",
			"console_setting.faq":                "FAQ",
			"console_setting.uptime_kuma_groups": "UptimeKumaGroups",
		}[key]
		if err := console_setting.ValidateConsoleSettings(value, fieldName); err != nil {
			return err
		}
	}
	if key == "AssistantSearchURL" {
		parsed, err := url.ParseRequestURI(value)
		if err == nil {
			for queryKey := range parsed.Query() {
				lowerKey := strings.ToLower(queryKey)
				if strings.Contains(lowerKey, "key") || strings.Contains(lowerKey, "token") || strings.Contains(lowerKey, "secret") || strings.Contains(lowerKey, "auth") {
					return errors.New("search URLs must not place credentials in query parameters")
				}
			}
		}
	}
	return nil
}

func createAssistantAdminFlow(c *gin.Context, userID int, payload assistantAdminChangePayload) (string, error) {
	sessionID, ok := assistantAdminSession(c)
	if !ok {
		return "", errors.New("administrator browser session is required")
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", errors.New("administrator change could not be prepared")
	}
	token, _, err := model.CreateAuthFlow(model.AuthFlowCreate{
		Purpose:   model.AuthFlowPurposeAssistantAdmin,
		UserId:    userID,
		SessionId: sessionID,
		Payload:   string(encoded),
		ExpiresAt: time.Now().Add(assistantAdminChangeLifetime),
	})
	if err != nil {
		return "", err
	}
	return token, nil
}

func executeAssistantAdminConfigTool(c *gin.Context, userID int) map[string]any {
	user, err := assistantRootUser(userID)
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error()}
	}
	keys := sortedAssistantAdminConfigKeys()
	current := assistantAdminCurrentOptions(keys)
	labels := assistantAdminAvailableConfigLabels()
	settings := make([]map[string]string, 0, len(keys))
	for _, key := range keys {
		settings = append(settings, map[string]string{
			"key":           key,
			"label":         labels[key],
			"current_value": current[key],
		})
	}
	return map[string]any{
		"ok":                         true,
		"administrator_role":         user.Role,
		"configurable_settings":      settings,
		"sensitive_settings_omitted": true,
		"write_rule":                 "Use prepare_admin_config_change, then wait for explicit UI confirmation.",
	}
}

func executeAssistantReviewTool(userID int) map[string]any {
	if _, err := assistantAdminUser(userID); err != nil {
		return map[string]any{"ok": false, "error": err.Error()}
	}
	task, err := model.GetLatestSystemTask(model.SystemTaskTypeAssistantReview)
	if err != nil {
		return map[string]any{"ok": false, "error": "automatic assistant review is unavailable"}
	}
	if task == nil {
		return map[string]any{"ok": true, "status": "not_run", "review": nil}
	}
	if task.Status != model.SystemTaskStatusSucceeded {
		return map[string]any{"ok": true, "status": task.Status, "review": nil, "updated_at": task.UpdatedAt}
	}
	review := model.AssistantReview{}
	if err := json.Unmarshal([]byte(task.Result), &review); err != nil {
		return map[string]any{"ok": false, "error": "automatic assistant review result is invalid"}
	}
	return map[string]any{
		"ok": true, "status": task.Status, "updated_at": task.UpdatedAt,
		"privacy_scope": "aggregate_only", "review": review,
	}
}

func executeAssistantAdminConfigChangeTool(c *gin.Context, userID int, input map[string]any) map[string]any {
	if _, err := assistantRootUser(userID); err != nil {
		return map[string]any{"ok": false, "error": err.Error()}
	}
	changes, err := assistantAdminConfigChanges(input)
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error()}
	}
	keys := sortedAssistantAdminChangeKeys(changes)
	current := assistantAdminCurrentOptions(keys)
	labels := assistantAdminAvailableConfigLabels()
	preview := make([]assistantAdminConfigPreview, 0, len(keys))
	for _, key := range keys {
		preview = append(preview, assistantAdminConfigPreview{
			Key:      key,
			Label:    labels[key],
			OldValue: current[key],
			NewValue: changes[key],
		})
	}
	payload := assistantAdminChangePayload{
		Kind:           assistantAdminConfigChangeKind,
		ConfigChanges:  changes,
		ConfigExpected: current,
	}
	token, err := createAssistantAdminFlow(c, userID, payload)
	if err != nil {
		return map[string]any{"ok": false, "error": "administrator browser session is required to prepare a change"}
	}
	action := map[string]any{
		"type":                  "admin_config_change",
		"confirmation_token":    token,
		"requires_confirmation": true,
		"expires_in_seconds":    int(assistantAdminChangeLifetime / time.Second),
		"changes":               preview,
	}
	c.Set(assistantClientActionKey, action)
	return map[string]any{
		"ok":        true,
		"status":    "confirmation_required",
		"action":    "admin_config_change",
		"changes":   preview,
		"next_step": "Show the exact preview and ask the administrator to confirm in the UI.",
	}
}

func executeAssistantAdminChannelsTool(userID int) map[string]any {
	user, err := assistantAdminUser(userID)
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error()}
	}
	channels, total, truncated, err := assistantAdminChannelViews()
	if err != nil {
		return map[string]any{"ok": false, "error": "administrator channel inventory is unavailable"}
	}
	fields := make([]string, 0, len(assistantAdminChannelFieldLabels))
	for field := range assistantAdminChannelFieldLabels {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	return map[string]any{
		"ok":                         true,
		"administrator_role":         user.Role,
		"channels":                   channels,
		"total_channels":             total,
		"truncated":                  truncated,
		"editable_fields":            fields,
		"sensitive_settings_omitted": true,
		"write_rule":                 "Use prepare_admin_channel_change, then wait for explicit UI confirmation.",
	}
}

func assistantAdminTargetUserID(input map[string]any) (int, error) {
	value, ok := inputNumber(input, "target_user_id")
	if !ok || value < 1 || value > 2_147_483_647 || math.Trunc(value) != value {
		return 0, errors.New("target_user_id must be a positive integer")
	}
	return int(value), nil
}

func assistantAdminSkillEnabled(input map[string]any, defaultValue bool) (bool, error) {
	raw, exists := input["enabled"]
	if !exists {
		return defaultValue, nil
	}
	enabled, ok := raw.(bool)
	if !ok {
		return false, errors.New("enabled must be a boolean")
	}
	return enabled, nil
}

func assistantAdminMemoryPreview(memory *model.AssistantMemory) []assistantAdminConfigPreview {
	if memory == nil {
		return nil
	}
	return []assistantAdminConfigPreview{
		{Key: "memory.title", Label: "Memory title", OldValue: memory.Title},
		{Key: "memory.content", Label: "Memory content", OldValue: memory.Content},
		{Key: "memory.tags", Label: "Memory tags", OldValue: memory.TagsJSON},
		{Key: "memory.enabled", Label: "Memory enabled", OldValue: strconv.FormatBool(memory.Enabled)},
	}
}

func assistantAdminProfilePreview(profile *model.AssistantUserProfile) []assistantAdminConfigPreview {
	if profile == nil {
		return nil
	}
	view := model.AssistantUserProfileViewOf(profile)
	tags, _ := json.Marshal(view.Tags)
	return []assistantAdminConfigPreview{
		{Key: "profile.profile_key", Label: "Profile key", OldValue: view.ProfileKey},
		{Key: "profile.tags", Label: "Profile tags", OldValue: string(tags)},
		{Key: "profile.strategy", Label: "Profile strategy", OldValue: view.Strategy},
		{Key: "profile.enabled", Label: "Profile enabled", OldValue: strconv.FormatBool(view.Enabled)},
	}
}

// executeAssistantAdminUserSkillsTool exposes only the selected user's
// bounded, redacted skill state. OpenSkills applies the same strict role
// lattice used by conversation history: an admin may inspect a lower-role
// user, but a peer or higher-role admin is denied.
func executeAssistantAdminUserSkillsTool(userID int, input map[string]any) map[string]any {
	if _, err := assistantAdminUser(userID); err != nil {
		return map[string]any{"ok": false, "error": err.Error()}
	}
	targetID, err := assistantAdminTargetUserID(input)
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error()}
	}
	scope, err := service.OpenSkills(userID, targetID)
	if err != nil {
		return map[string]any{"ok": false, "status": "target_forbidden", "error": "the target user's skills are outside the administrator's permitted scope"}
	}
	memories, err := scope.Memories(true)
	if err != nil {
		return map[string]any{"ok": false, "error": "user memory skills could not be loaded"}
	}
	truncated := len(memories) > assistantAdminMaxSkillMemoryViews
	if truncated {
		memories = memories[:assistantAdminMaxSkillMemoryViews]
	}
	profile, err := scope.Profile()
	if err != nil {
		return map[string]any{"ok": false, "error": "user profile skill could not be loaded"}
	}
	return map[string]any{
		"ok":                        true,
		"target_user_id":            targetID,
		"owner_scope":               "higher_role_administrator_only",
		"profile":                   model.AssistantUserProfileViewOf(profile),
		"memories":                  memories,
		"memories_truncated":        truncated,
		"sensitive_values_redacted": true,
		"write_rule":                "Use prepare_admin_user_skill_change, then wait for explicit UI confirmation.",
	}
}

func assistantAdminMemoryChangeInput(input map[string]any, scope service.UserSkills, ownerID int) (*assistantAdminMemoryChange, []assistantAdminConfigPreview, error) {
	operation := strings.ToLower(inputString(input, "operation"))
	if operation == "" {
		operation = "upsert"
	}
	if operation != "upsert" && operation != "delete" {
		return nil, nil, errors.New("memory operation must be upsert or delete")
	}
	var memoryID int64
	if value, supplied := inputNumber(input, "memory_id"); supplied {
		if value < 1 || value > math.MaxInt64 || math.Trunc(value) != value {
			return nil, nil, errors.New("memory_id must be a positive integer")
		}
		memoryID = int64(value)
	}
	if operation == "delete" && memoryID <= 0 {
		return nil, nil, errors.New("memory_id is required to delete a memory")
	}
	if operation == "delete" {
		memory, err := model.GetMemory(ownerID, memoryID)
		if err != nil {
			return nil, nil, err
		}
		change := &assistantAdminMemoryChange{ID: memory.Id, ExpectedUpdatedAt: memory.UpdatedAt, Enabled: memory.Enabled}
		preview := assistantAdminMemoryPreview(memory)
		for index := range preview {
			preview[index].NewValue = "[deleted]"
		}
		return change, preview, nil
	}
	tags, ok := stringList(input, "tags", model.AssistantMemoryMaxTags)
	if !ok {
		return nil, nil, errors.New("memory tags are invalid")
	}
	enabled, err := assistantAdminSkillEnabled(input, true)
	if err != nil {
		return nil, nil, err
	}
	normalized, err := model.NormalizeMemoryInput(model.MemoryInput{
		ID: memoryID, Title: inputString(input, "title"), Content: inputString(input, "content"),
		Tags: tags, Source: model.AssistantMemorySourceAdmin, Enabled: enabled,
	})
	if err != nil {
		return nil, nil, err
	}
	var current *model.AssistantMemory
	if memoryID > 0 {
		current, err = model.GetMemory(ownerID, memoryID)
		if err != nil {
			return nil, nil, err
		}
	} else {
		memories, listErr := scope.Memories(true)
		if listErr != nil {
			return nil, nil, listErr
		}
		for _, candidate := range memories {
			if candidate.Title == normalized.Title {
				return nil, nil, errors.New("a memory with this title already exists; provide memory_id to update it")
			}
		}
	}
	change := &assistantAdminMemoryChange{
		ID: memoryID, Title: normalized.Title, Content: normalized.Content,
		Tags: normalized.Tags, Enabled: normalized.Enabled,
	}
	if current != nil {
		change.ExpectedUpdatedAt = current.UpdatedAt
	}
	tagsJSON, _ := json.Marshal(normalized.Tags)
	preview := assistantAdminMemoryPreview(current)
	preview = append(preview,
		assistantAdminConfigPreview{Key: "memory.title", Label: "Memory title", NewValue: normalized.Title},
		assistantAdminConfigPreview{Key: "memory.content", Label: "Memory content", NewValue: normalized.Content},
		assistantAdminConfigPreview{Key: "memory.tags", Label: "Memory tags", NewValue: string(tagsJSON)},
		assistantAdminConfigPreview{Key: "memory.enabled", Label: "Memory enabled", NewValue: strconv.FormatBool(normalized.Enabled)},
	)
	if current == nil {
		for index := range preview[:len(preview)-4] {
			preview[index].OldValue = "—"
		}
	}
	return change, preview, nil
}

func executeAssistantAdminUserSkillChangeTool(c *gin.Context, userID int, input map[string]any) map[string]any {
	if _, err := assistantAdminUser(userID); err != nil {
		return map[string]any{"ok": false, "error": err.Error()}
	}
	targetID, err := assistantAdminTargetUserID(input)
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error()}
	}
	scope, err := service.OpenSkills(userID, targetID)
	if err != nil {
		return map[string]any{"ok": false, "status": "target_forbidden", "error": "the target user's skills are outside the administrator's permitted scope"}
	}
	kind := strings.ToLower(inputString(input, "kind"))
	if kind != "memory" && kind != "profile" {
		return map[string]any{"ok": false, "error": "skill kind must be memory or profile"}
	}
	change := assistantAdminUserSkillChange{TargetUserID: targetID, Operation: strings.ToLower(inputString(input, "operation"))}
	var preview []assistantAdminConfigPreview
	if kind == "memory" {
		memoryChange, memoryPreview, memoryErr := assistantAdminMemoryChangeInput(input, scope, targetID)
		if memoryErr != nil {
			return map[string]any{"ok": false, "error": memoryErr.Error()}
		}
		if change.Operation == "" {
			change.Operation = "upsert"
		}
		change.Memory = memoryChange
		preview = memoryPreview
	} else {
		if change.Operation == "" {
			change.Operation = "upsert"
		}
		if change.Operation != "upsert" {
			return map[string]any{"ok": false, "error": "profile operation must be upsert"}
		}
		tags, tagsOK := stringList(input, "tags", model.AssistantUserProfileMaxTags)
		if !tagsOK {
			return map[string]any{"ok": false, "error": "profile tags are invalid"}
		}
		enabled, enabledErr := assistantAdminSkillEnabled(input, true)
		if enabledErr != nil {
			return map[string]any{"ok": false, "error": enabledErr.Error()}
		}
		normalized, normalizeErr := model.NormalizeProfileInput(model.ProfileInput{
			Key: inputString(input, "profile_key"), Tags: tags, Strategy: inputString(input, "strategy"),
			Source: model.AssistantProfileSourceAdmin, Enabled: enabled,
		})
		if normalizeErr != nil {
			return map[string]any{"ok": false, "error": normalizeErr.Error()}
		}
		current, profileErr := scope.Profile()
		if profileErr != nil {
			return map[string]any{"ok": false, "error": "user profile skill could not be loaded"}
		}
		change.Profile = &assistantAdminProfileChange{
			ProfileKey: normalized.Key, Tags: normalized.Tags, Strategy: normalized.Strategy, Enabled: normalized.Enabled,
		}
		if current != nil {
			change.Profile.Exists = true
			change.Profile.ExpectedUpdatedAt = current.UpdatedAt
		}
		profilePreview := assistantAdminProfilePreview(current)
		encodedTags, _ := json.Marshal(normalized.Tags)
		profilePreview = append(profilePreview,
			assistantAdminConfigPreview{Key: "profile.profile_key", Label: "Profile key", NewValue: normalized.Key},
			assistantAdminConfigPreview{Key: "profile.tags", Label: "Profile tags", NewValue: string(encodedTags)},
			assistantAdminConfigPreview{Key: "profile.strategy", Label: "Profile strategy", NewValue: normalized.Strategy},
			assistantAdminConfigPreview{Key: "profile.enabled", Label: "Profile enabled", NewValue: strconv.FormatBool(normalized.Enabled)},
		)
		if current == nil {
			for index := range profilePreview[:len(profilePreview)-4] {
				profilePreview[index].OldValue = "—"
			}
		}
		preview = profilePreview
	}
	change.Operation = strings.ToLower(change.Operation)
	payload := assistantAdminChangePayload{Kind: assistantAdminUserSkillChangeKind, UserSkill: &change}
	token, err := createAssistantAdminFlow(c, userID, payload)
	if err != nil {
		return map[string]any{"ok": false, "error": "administrator browser session is required to prepare a user skill change"}
	}
	action := map[string]any{
		"type": "admin_config_change", "scope": "user_skills", "target_user_id": targetID,
		"confirmation_token": token, "requires_confirmation": true,
		"expires_in_seconds": int(assistantAdminChangeLifetime / time.Second), "changes": preview,
	}
	c.Set(assistantClientActionKey, action)
	return map[string]any{
		"ok": true, "status": "confirmation_required", "action": "admin_user_skill_change",
		"target_user_id": targetID, "changes": preview,
		"next_step": "Show the exact user-skill preview and ask the administrator to confirm in the UI.",
	}
}

func executeAssistantAdminChannelChangeTool(c *gin.Context, userID int, input map[string]any) map[string]any {
	if _, err := assistantAdminUser(userID); err != nil {
		return map[string]any{"ok": false, "error": err.Error()}
	}
	channelID, changes, err := assistantAdminChannelChanges(input)
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error()}
	}
	current, channel, err := assistantAdminChannelState(channelID)
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error()}
	}
	keys := sortedAssistantAdminChangeKeys(changes)
	preview := make([]assistantAdminConfigPreview, 0, len(keys))
	expected := make(map[string]string, len(keys))
	for _, field := range keys {
		label, _ := assistantAdminChannelFieldLabel(field)
		preview = append(preview, assistantAdminConfigPreview{
			Key:      fmt.Sprintf("channel.%d.%s", channelID, field),
			Label:    fmt.Sprintf("Channel #%d (%s): %s", channelID, channel.Name, label),
			OldValue: current[field],
			NewValue: changes[field],
		})
		expected[field] = current[field]
	}
	payload := assistantAdminChangePayload{
		Kind: assistantAdminChannelChangeKind,
		Channel: &assistantAdminChannelChange{
			ChannelID: channelID,
			Changes:   changes,
			Expected:  expected,
		},
	}
	token, err := createAssistantAdminFlow(c, userID, payload)
	if err != nil {
		return map[string]any{"ok": false, "error": "administrator browser session is required to prepare a channel change"}
	}
	action := map[string]any{
		"type":                  "admin_config_change",
		"scope":                 "channel",
		"channel_id":            channelID,
		"channel_name":          channel.Name,
		"confirmation_token":    token,
		"requires_confirmation": true,
		"expires_in_seconds":    int(assistantAdminChangeLifetime / time.Second),
		"changes":               preview,
	}
	c.Set(assistantClientActionKey, action)
	return map[string]any{
		"ok":        true,
		"status":    "confirmation_required",
		"action":    "admin_channel_change",
		"channel":   channel.Name,
		"changes":   preview,
		"next_step": "Show the exact channel preview and ask the administrator to confirm in the UI.",
	}
}

func assistantAdminOptionalNumber(input map[string]any, key string) (*float64, error) {
	if _, exists := input[key]; !exists {
		return nil, nil
	}
	value, ok := inputNumber(input, key)
	if !ok || value < 0 || value > 1_000_000 {
		return nil, fmt.Errorf("%s must be a finite number between 0 and 1000000", key)
	}
	return &value, nil
}

func assistantAdminCurrentPricingState(modelID string) assistantAdminPricingState {
	modelPrices := ratio_setting.GetModelPriceCopy()
	modelRatios := ratio_setting.GetModelRatioCopy()
	completionRatios := ratio_setting.GetCompletionRatioCopy()
	cacheRatios := ratio_setting.GetCacheRatioCopy()
	createCacheRatios := ratio_setting.GetCreateCacheRatioCopy()
	imageRatios := ratio_setting.GetImageRatioCopy()
	audioRatios := ratio_setting.GetAudioRatioCopy()
	audioCompletionRatios := ratio_setting.GetAudioCompletionRatioCopy()
	state := assistantAdminPricingState{
		Mode:                 "ratio",
		Value:                modelRatios[modelID],
		CompletionRatio:      completionRatios[modelID],
		CacheRatio:           cacheRatios[modelID],
		CreateCacheRatio:     createCacheRatios[modelID],
		ImageRatio:           imageRatios[modelID],
		AudioRatio:           audioRatios[modelID],
		AudioCompletionRatio: audioCompletionRatios[modelID],
	}
	if price, ok := modelPrices[modelID]; ok {
		state.Mode = "fixed_request"
		state.Value = price
	}
	return state
}

func assistantAdminPricingStateMap(state assistantAdminPricingState) map[string]any {
	return map[string]any{
		"mode":                   state.Mode,
		"value":                  state.Value,
		"completion_ratio":       state.CompletionRatio,
		"cache_ratio":            state.CacheRatio,
		"create_cache_ratio":     state.CreateCacheRatio,
		"image_ratio":            state.ImageRatio,
		"audio_ratio":            state.AudioRatio,
		"audio_completion_ratio": state.AudioCompletionRatio,
	}
}

func assistantAdminPricingPreview(change assistantAdminPricingChange) map[string]any {
	oldState := assistantAdminCurrentPricingState(change.ModelID)
	nextState := oldState
	nextState.Mode = change.Mode
	nextState.Value = change.Value
	if change.CompletionRatio != nil {
		nextState.CompletionRatio = *change.CompletionRatio
	}
	if change.CacheRatio != nil {
		nextState.CacheRatio = *change.CacheRatio
	}
	if change.CreateCacheRatio != nil {
		nextState.CreateCacheRatio = *change.CreateCacheRatio
	}
	if change.ImageRatio != nil {
		nextState.ImageRatio = *change.ImageRatio
	}
	if change.AudioRatio != nil {
		nextState.AudioRatio = *change.AudioRatio
	}
	if change.AudioCompletionRatio != nil {
		nextState.AudioCompletionRatio = *change.AudioCompletionRatio
	}
	return map[string]any{
		"model_id": change.ModelID,
		"old":      assistantAdminPricingStateMap(oldState),
		"next":     assistantAdminPricingStateMap(nextState),
	}
}

func executeAssistantAdminPricingChangeTool(c *gin.Context, userID int, input map[string]any) map[string]any {
	if _, err := assistantRootUser(userID); err != nil {
		return map[string]any{"ok": false, "error": err.Error()}
	}
	modelID := inputString(input, "model_id")
	mode := inputString(input, "mode")
	value, valueOK := inputNumber(input, "value")
	if modelID == "" || len([]rune(modelID)) > 200 {
		return map[string]any{"ok": false, "error": "an exact model_id is required"}
	}
	if mode != "ratio" && mode != "fixed_request" {
		return map[string]any{"ok": false, "error": "mode must be ratio or fixed_request"}
	}
	if !valueOK || value < 0 || value > 1_000_000 {
		return map[string]any{"ok": false, "error": "value must be a finite number between 0 and 1000000"}
	}
	pricing := getPricingCache()
	modelFound := false
	for _, candidate := range pricing {
		if candidate.ModelName == modelID {
			if candidate.BillingMode == billing_setting.BillingModeTieredExpr {
				return map[string]any{"ok": false, "error": "this model uses tiered billing; change billing_setting.billing_mode and billing_setting.billing_expr through the administrator configuration preview"}
			}
			modelFound = true
			break
		}
	}
	if !modelFound {
		return map[string]any{"ok": false, "error": "model_id is not currently enabled; call get_available_models first"}
	}
	completionRatio, err := assistantAdminOptionalNumber(input, "completion_ratio")
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error()}
	}
	cacheRatio, err := assistantAdminOptionalNumber(input, "cache_ratio")
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error()}
	}
	createCacheRatio, err := assistantAdminOptionalNumber(input, "create_cache_ratio")
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error()}
	}
	imageRatio, err := assistantAdminOptionalNumber(input, "image_ratio")
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error()}
	}
	audioRatio, err := assistantAdminOptionalNumber(input, "audio_ratio")
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error()}
	}
	audioCompletionRatio, err := assistantAdminOptionalNumber(input, "audio_completion_ratio")
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error()}
	}
	change := assistantAdminPricingChange{
		ModelID:              modelID,
		Mode:                 mode,
		Value:                value,
		CompletionRatio:      completionRatio,
		CacheRatio:           cacheRatio,
		CreateCacheRatio:     createCacheRatio,
		ImageRatio:           imageRatio,
		AudioRatio:           audioRatio,
		AudioCompletionRatio: audioCompletionRatio,
	}
	currentState := assistantAdminCurrentPricingState(modelID)
	change.Expected = &currentState
	payload := assistantAdminChangePayload{Kind: assistantAdminPricingChangeKind, Pricing: &change}
	token, err := createAssistantAdminFlow(c, userID, payload)
	if err != nil {
		return map[string]any{"ok": false, "error": "administrator browser session is required to prepare a pricing change"}
	}
	action := map[string]any{
		"type":                  "admin_pricing_change",
		"confirmation_token":    token,
		"requires_confirmation": true,
		"expires_in_seconds":    int(assistantAdminChangeLifetime / time.Second),
		"pricing":               assistantAdminPricingPreview(change),
	}
	c.Set(assistantClientActionKey, action)
	return map[string]any{
		"ok":        true,
		"status":    "confirmation_required",
		"action":    "admin_pricing_change",
		"preview":   assistantAdminPricingPreview(change),
		"next_step": "Show the exact old and new model pricing and ask the administrator to confirm in the UI.",
	}
}

func assistantAdminPricingOptions(change assistantAdminPricingChange) (map[string]string, error) {
	modelPrices := ratio_setting.GetModelPriceCopy()
	modelRatios := ratio_setting.GetModelRatioCopy()
	completionRatios := ratio_setting.GetCompletionRatioCopy()
	cacheRatios := ratio_setting.GetCacheRatioCopy()
	createCacheRatios := ratio_setting.GetCreateCacheRatioCopy()
	imageRatios := ratio_setting.GetImageRatioCopy()
	audioRatios := ratio_setting.GetAudioRatioCopy()
	audioCompletionRatios := ratio_setting.GetAudioCompletionRatioCopy()
	if change.Mode == "fixed_request" {
		modelPrices[change.ModelID] = change.Value
		delete(modelRatios, change.ModelID)
	} else {
		modelRatios[change.ModelID] = change.Value
		delete(modelPrices, change.ModelID)
	}
	if change.CompletionRatio != nil {
		completionRatios[change.ModelID] = *change.CompletionRatio
	}
	if change.CacheRatio != nil {
		cacheRatios[change.ModelID] = *change.CacheRatio
	}
	if change.CreateCacheRatio != nil {
		createCacheRatios[change.ModelID] = *change.CreateCacheRatio
	}
	if change.ImageRatio != nil {
		imageRatios[change.ModelID] = *change.ImageRatio
	}
	if change.AudioRatio != nil {
		audioRatios[change.ModelID] = *change.AudioRatio
	}
	if change.AudioCompletionRatio != nil {
		audioCompletionRatios[change.ModelID] = *change.AudioCompletionRatio
	}
	encode := func(value any) (string, error) {
		encoded, err := common.Marshal(value)
		return string(encoded), err
	}
	modelPrice, err := encode(modelPrices)
	if err != nil {
		return nil, err
	}
	modelRatio, err := encode(modelRatios)
	if err != nil {
		return nil, err
	}
	completionRatio, err := encode(completionRatios)
	if err != nil {
		return nil, err
	}
	cacheRatio, err := encode(cacheRatios)
	if err != nil {
		return nil, err
	}
	createCacheRatio, err := encode(createCacheRatios)
	if err != nil {
		return nil, err
	}
	imageRatio, err := encode(imageRatios)
	if err != nil {
		return nil, err
	}
	audioRatio, err := encode(audioRatios)
	if err != nil {
		return nil, err
	}
	audioCompletionRatio, err := encode(audioCompletionRatios)
	if err != nil {
		return nil, err
	}
	return map[string]string{
		"ModelPrice":           modelPrice,
		"ModelRatio":           modelRatio,
		"CompletionRatio":      completionRatio,
		"CacheRatio":           cacheRatio,
		"CreateCacheRatio":     createCacheRatio,
		"ImageRatio":           imageRatio,
		"AudioRatio":           audioRatio,
		"AudioCompletionRatio": audioCompletionRatio,
	}, nil
}

func applyAssistantAdminChannelField(channel *model.Channel, field, value string) (bool, error) {
	switch field {
	case "status":
		return false, nil
	case "name":
		channel.Name = value
	case "test_model":
		channel.TestModel = common.GetPointer[string](value)
	case "weight":
		// Parse directly into the signed type used by validation so the value
		// cannot pass through a wider unsigned integer before being stored.
		weight, err := strconv.Atoi(value)
		if err != nil {
			return false, err
		}
		if weight < 0 {
			return false, errors.New("weight must be non-negative")
		}
		converted := uint(weight)
		channel.Weight = &converted
	case "models":
		channel.Models = value
	case "group":
		channel.Group = value
	case "model_mapping":
		channel.ModelMapping = common.GetPointer[string](value)
	case "status_code_mapping":
		channel.StatusCodeMapping = common.GetPointer[string](value)
	case "priority":
		priority, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return false, err
		}
		channel.Priority = &priority
	case "auto_ban":
		autoBan, err := strconv.ParseBool(value)
		if err != nil {
			return false, err
		}
		encoded := 0
		if autoBan {
			encoded = 1
		}
		channel.AutoBan = &encoded
	case "tag":
		channel.Tag = common.GetPointer[string](value)
	case "remark":
		channel.Remark = common.GetPointer[string](value)
	default:
		return false, fmt.Errorf("channel field %q is not available to the assistant", field)
	}
	return true, nil
}

func applyAssistantAdminChannelChange(change assistantAdminChannelChange) error {
	if change.ChannelID <= 0 || len(change.Changes) == 0 || len(change.Expected) != len(change.Changes) {
		return errors.New("administrator channel preview is invalid; prepare it again")
	}
	current, _, err := assistantAdminChannelState(change.ChannelID)
	if err != nil {
		return err
	}
	for field, expected := range change.Expected {
		if current[field] != expected {
			return errors.New("administrator channel changed after the preview; prepare it again")
		}
	}
	for field, value := range change.Changes {
		if _, ok := assistantAdminChannelFieldLabel(field); !ok {
			return errors.New("administrator channel preview contains an unavailable field")
		}
		if err := validateAssistantAdminChannelValue(field, value); err != nil {
			return fmt.Errorf("%s: %w", field, err)
		}
	}
	channel, err := model.GetChannelById(change.ChannelID, true)
	if err != nil || channel == nil {
		if err != nil {
			return err
		}
		return errors.New("channel could not be loaded")
	}
	targetStatus := channel.Status
	metadataChanged := false
	for field, value := range change.Changes {
		if field == "status" {
			parsed, parseErr := strconv.Atoi(value)
			if parseErr != nil {
				return parseErr
			}
			targetStatus = parsed
			continue
		}
		changed, fieldErr := applyAssistantAdminChannelField(channel, field, value)
		if fieldErr != nil {
			return fieldErr
		}
		metadataChanged = metadataChanged || changed
	}
	if metadataChanged {
		if err := channel.Update(); err != nil {
			return err
		}
	}
	if targetStatus != channel.Status {
		if !model.UpdateChannelStatus(channel.Id, "", targetStatus, "assistant administrator operation") {
			return errors.New("channel status could not be updated")
		}
		if targetStatus != common.ChannelStatusEnabled {
			closeActiveChannelWebSockets([]int{channel.Id})
		}
	}
	model.InitChannelCache()
	return nil
}

func applyAssistantAdminUserSkillChange(actorUserID int, change assistantAdminUserSkillChange) error {
	if change.TargetUserID <= 0 || change.Operation == "" {
		return errors.New("administrator user skill preview is invalid; prepare it again")
	}
	scope, err := service.OpenSkills(actorUserID, change.TargetUserID)
	if err != nil {
		return errors.New("the target user's skills are outside the administrator's permitted scope")
	}
	switch change.Operation {
	case "delete":
		if change.Memory == nil || change.Memory.ID <= 0 {
			return errors.New("administrator memory deletion preview is invalid")
		}
		current, err := model.GetMemory(change.TargetUserID, change.Memory.ID)
		if err != nil {
			return err
		}
		if current.UpdatedAt != change.Memory.ExpectedUpdatedAt {
			return errors.New("user memory changed after the preview; prepare it again")
		}
		return scope.Forget(change.Memory.ID)
	case "upsert":
		if change.Memory != nil {
			normalized, err := model.NormalizeMemoryInput(model.MemoryInput{
				ID: change.Memory.ID, Title: change.Memory.Title, Content: change.Memory.Content,
				Tags: change.Memory.Tags, Source: model.AssistantMemorySourceAdmin, Enabled: change.Memory.Enabled,
			})
			if err != nil {
				return err
			}
			if normalized.ID > 0 {
				current, err := model.GetMemory(change.TargetUserID, normalized.ID)
				if err != nil {
					return err
				}
				if current.UpdatedAt != change.Memory.ExpectedUpdatedAt {
					return errors.New("user memory changed after the preview; prepare it again")
				}
			} else {
				memories, err := scope.Memories(true)
				if err != nil {
					return err
				}
				for _, memory := range memories {
					if memory.Title == normalized.Title {
						return errors.New("a memory with this title already exists; prepare an update with memory_id")
					}
				}
			}
			_, err = scope.SetMemory(service.MemoryDraft{
				ID: normalized.ID, Title: normalized.Title, Content: normalized.Content,
				Tags: normalized.Tags, Enabled: normalized.Enabled,
			})
			return err
		}
		if change.Profile == nil {
			return errors.New("administrator user skill preview is empty")
		}
		current, err := scope.Profile()
		if err != nil {
			return err
		}
		if (current != nil) != change.Profile.Exists || (current != nil && current.UpdatedAt != change.Profile.ExpectedUpdatedAt) {
			return errors.New("user profile skill changed after the preview; prepare it again")
		}
		normalized, err := model.NormalizeProfileInput(model.ProfileInput{
			Key: change.Profile.ProfileKey, Tags: change.Profile.Tags, Strategy: change.Profile.Strategy,
			Source: model.AssistantProfileSourceAdmin, Enabled: change.Profile.Enabled,
		})
		if err != nil {
			return err
		}
		_, err = scope.SetProfile(service.ProfileDraft{
			Key: normalized.Key, Tags: normalized.Tags, Strategy: normalized.Strategy, Enabled: normalized.Enabled,
		})
		return err
	default:
		return errors.New("administrator user skill operation is invalid")
	}
}

func applyAssistantAdminChange(c *gin.Context, payload assistantAdminChangePayload) error {
	switch payload.Kind {
	case assistantAdminConfigChangeKind:
		if len(payload.ConfigChanges) == 0 {
			return errors.New("administrator configuration change is empty")
		}
		if len(payload.ConfigExpected) != len(payload.ConfigChanges) {
			return errors.New("administrator configuration preview is stale; prepare it again")
		}
		current := assistantAdminCurrentOptions(sortedAssistantAdminChangeKeys(payload.ConfigChanges))
		for key, expected := range payload.ConfigExpected {
			if current[key] != expected {
				return errors.New("administrator configuration changed after the preview; prepare it again")
			}
		}
		if err := validateAssistantAdminDynamicPricingChange(payload.ConfigChanges); err != nil {
			return err
		}
		for key, value := range payload.ConfigChanges {
			if _, allowed := assistantAdminConfigLabel(key); !allowed {
				return errors.New("administrator configuration contains an unavailable key")
			}
			if strings.HasPrefix(key, "dynamic_pricing_setting.") {
				continue
			}
			if err := validateAssistantAdminConfigValue(key, value); err != nil {
				return fmt.Errorf("%s: %w", key, err)
			}
		}
		if err := model.UpdateOptionsBulk(payload.ConfigChanges); err != nil {
			return err
		}
		for key := range payload.ConfigChanges {
			if key == "GroupRatio" || key == "GroupGroupRatio" || key == "TopupGroupRatio" {
				if err := refreshPricingCache(); err != nil {
					return err
				}
				break
			}
		}
	case assistantAdminPricingChangeKind:
		if payload.Pricing == nil {
			return errors.New("administrator pricing change is empty")
		}
		if payload.Pricing.Expected == nil || assistantAdminCurrentPricingState(payload.Pricing.ModelID) != *payload.Pricing.Expected {
			return errors.New("model pricing changed after the preview; prepare it again")
		}
		options, err := assistantAdminPricingOptions(*payload.Pricing)
		if err != nil {
			return err
		}
		if err := model.UpdateOptionsBulk(options); err != nil {
			return err
		}
		if err := refreshPricingCache(); err != nil {
			return err
		}
	case assistantAdminChannelChangeKind:
		if payload.Channel == nil {
			return errors.New("administrator channel change is empty")
		}
		return applyAssistantAdminChannelChange(*payload.Channel)
	case assistantAdminUserSkillChangeKind:
		if payload.UserSkill == nil {
			return errors.New("administrator user skill change is empty")
		}
		return applyAssistantAdminUserSkillChange(c.GetInt("id"), *payload.UserSkill)
	case assistantAdminModelSyncChangeKind:
		if payload.ModelSync == nil {
			return errors.New("administrator model sync change is empty")
		}
		return applyAssistantAdminModelSync(*payload.ModelSync)
	default:
		return errors.New("unknown administrator assistant change")
	}
	return nil
}

// ApplyAssistantAdminChange consumes a session-bound, one-time preview token.
// The browser never submits authoritative values; the server rehydrates the
// signed payload and applies only the allowlisted change that was previewed.
func ApplyAssistantAdminChange(c *gin.Context) {
	var input assistantAdminApplyInput
	if err := c.ShouldBindJSON(&input); err != nil || !input.Confirmed || strings.TrimSpace(input.ConfirmationToken) == "" {
		writeAssistantError(c, http.StatusUnprocessableEntity, "ASSISTANT_ADMIN_CONFIRMATION_REQUIRED", errors.New("explicit confirmation of the administrator preview is required"))
		return
	}
	sessionID, ok := assistantAdminSession(c)
	if !ok {
		writeAssistantError(c, http.StatusForbidden, "ASSISTANT_ADMIN_SESSION_REQUIRED", errors.New("a browser login session is required for administrator changes"))
		return
	}
	user, err := assistantAdminUser(c.GetInt("id"))
	if err != nil {
		writeAssistantError(c, http.StatusForbidden, "ASSISTANT_ADMIN_REQUIRED", err)
		return
	}
	flow, err := model.ConsumeAuthFlow(input.ConfirmationToken, model.AuthFlowMatch{
		Purpose:   model.AuthFlowPurposeAssistantAdmin,
		UserId:    user.Id,
		SessionId: sessionID,
	})
	if err != nil {
		writeAssistantError(c, http.StatusUnprocessableEntity, "ASSISTANT_ADMIN_CONFIRMATION_INVALID", errors.New("administrator preview is invalid or expired; ask the assistant to prepare it again"))
		return
	}
	var payload assistantAdminChangePayload
	if err := json.Unmarshal([]byte(flow.Payload), &payload); err != nil {
		writeAssistantError(c, http.StatusInternalServerError, "ASSISTANT_ADMIN_CHANGE_INVALID", errors.New("administrator preview could not be decoded"))
		return
	}
	if payload.Kind == assistantAdminConfigChangeKind || payload.Kind == assistantAdminPricingChangeKind || payload.Kind == assistantAdminModelSyncChangeKind {
		if _, err := assistantRootUser(user.Id); err != nil {
			writeAssistantError(c, http.StatusForbidden, "ASSISTANT_ROOT_REQUIRED", err)
			return
		}
	}
	if err := applyAssistantAdminChange(c, payload); err != nil {
		writeAssistantError(c, http.StatusUnprocessableEntity, "ASSISTANT_ADMIN_CHANGE_FAILED", err)
		return
	}
	if payload.Kind == assistantAdminPricingChangeKind && payload.Pricing != nil {
		recordManageAudit(c, "assistant.admin_pricing_apply", map[string]interface{}{
			"model_id": payload.Pricing.ModelID,
			"mode":     payload.Pricing.Mode,
			"value":    payload.Pricing.Value,
		})
	} else if payload.Kind == assistantAdminChannelChangeKind && payload.Channel != nil {
		fields := make([]string, 0, len(payload.Channel.Changes))
		for field := range payload.Channel.Changes {
			fields = append(fields, field)
		}
		sort.Strings(fields)
		recordManageAudit(c, "assistant.admin_channel_apply", map[string]interface{}{
			"channel_id": payload.Channel.ChannelID,
			"fields":     fields,
		})
	} else if payload.Kind == assistantAdminUserSkillChangeKind && payload.UserSkill != nil {
		fields := []string{"user_skill"}
		if payload.UserSkill.Memory != nil {
			fields = []string{"memory"}
		}
		recordManageAudit(c, "assistant.admin_user_skill_apply", map[string]interface{}{
			"target_user_id": payload.UserSkill.TargetUserID,
			"operation":      payload.UserSkill.Operation,
			"fields":         fields,
		})
	} else if payload.Kind == assistantAdminModelSyncChangeKind && payload.ModelSync != nil {
		recordManageAudit(c, "assistant.admin_model_sync_apply", map[string]interface{}{
			"locale":        payload.ModelSync.Locale,
			"model_count":   len(payload.ModelSync.Models),
			"source_digest": payload.ModelSync.SourceDigest,
		})
	} else {
		keys := make([]string, 0, len(payload.ConfigChanges))
		for key := range payload.ConfigChanges {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		recordManageAudit(c, "assistant.admin_config_apply", map[string]interface{}{"keys": keys})
	}
	common.ApiSuccess(c, gin.H{
		"applied": true,
		"kind":    payload.Kind,
	})
}
