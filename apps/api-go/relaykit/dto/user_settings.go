package dto

import (
	"encoding/json"
	"fmt"
	"strings"
)

type UserSetting struct {
	SessionAutoLogout                *bool   `json:"session_auto_logout,omitempty"`                  // nil enables weekly sign-out for existing users.
	NotifyType                       string  `json:"notify_type,omitempty"`                          // QuotaWarningType 额度预警类型
	QuotaWarningThreshold            float64 `json:"quota_warning_threshold,omitempty"`              // QuotaWarningThreshold 额度预警阈值
	WebhookUrl                       string  `json:"webhook_url,omitempty"`                          // WebhookUrl webhook地址
	WebhookSecret                    string  `json:"webhook_secret,omitempty"`                       // WebhookSecret webhook密钥
	NotificationEmail                string  `json:"notification_email,omitempty"`                   // NotificationEmail 通知邮箱地址
	BarkUrl                          string  `json:"bark_url,omitempty"`                             // BarkUrl Bark推送URL
	GotifyUrl                        string  `json:"gotify_url,omitempty"`                           // GotifyUrl Gotify服务器地址
	GotifyToken                      string  `json:"gotify_token,omitempty"`                         // GotifyToken Gotify应用令牌
	GotifyPriority                   int     `json:"gotify_priority"`                                // GotifyPriority Gotify消息优先级
	UpstreamModelUpdateNotifyEnabled bool    `json:"upstream_model_update_notify_enabled,omitempty"` // 是否接收上游模型更新定时检测通知（仅管理员）
	AcceptUnsetRatioModel            bool    `json:"accept_unset_model_ratio_model,omitempty"`       // AcceptUnsetRatioModel 是否接受未设置价格的模型
	RecordIpLog                      bool    `json:"record_ip_log,omitempty"`                        // 是否记录请求和错误日志IP
	SidebarModules                   string  `json:"sidebar_modules,omitempty"`                      // SidebarModules 左侧边栏模块配置
	BillingPreference                string  `json:"billing_preference,omitempty"`                   // BillingPreference 扣费策略（订阅/钱包）
	Language                         string  `json:"language,omitempty"`                             // Language 用户语言偏好 (zh, en)
	SettlementCurrency               string  `json:"settlement_currency,omitempty"`                  // Customer fiat preference; empty follows language, not quota display.
	UsageLeaderboardVisibility       string  `json:"usage_leaderboard_visibility,omitempty"`         // 用户使用排行榜展示方式
}

// NormalizeSettlementCurrencyPreference accepts only currencies supported by
// the platform's real-fiat conversion. Empty means follow the interface language.
func NormalizeSettlementCurrencyPreference(value string) (string, error) {
	value = strings.ToUpper(strings.TrimSpace(value))
	switch value {
	case "", "CNY", "USD":
		return value, nil
	default:
		return "", fmt.Errorf("settlement currency must be CNY or USD")
	}
}

func (setting UserSetting) EffectiveSettlementCurrency(languageHint string) string {
	if currency, err := NormalizeSettlementCurrencyPreference(setting.SettlementCurrency); err == nil && currency != "" {
		return currency
	}
	language := strings.TrimSpace(setting.Language)
	if language == "" {
		language = languageHint
	}
	language = strings.ToLower(strings.TrimSpace(strings.Split(language, ",")[0]))
	language = strings.Split(language, ";")[0]
	if language == "zh" || strings.HasPrefix(language, "zh-") || strings.HasPrefix(language, "zh_") {
		return "CNY"
	}
	return "USD"
}

func (setting UserSetting) IsSessionAutoLogoutEnabled() bool {
	return setting.SessionAutoLogout == nil || *setting.SessionAutoLogout
}

const SidebarModulesMaxBytes = 16 * 1024

// ValidateSidebarModules bounds the user-controlled navigation preference
// blob before it reaches the users.setting column or the settings cache.
// Legacy section-only objects remain valid; the versioned envelope only gets
// lightweight shape checks here. Visibility and role checks stay in the web
// router and sidebar, never in this persistence guard.
func ValidateSidebarModules(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if len([]byte(value)) > SidebarModulesMaxBytes {
		return fmt.Errorf("sidebar modules exceed %d bytes", SidebarModulesMaxBytes)
	}

	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(value), &object); err != nil || object == nil {
		return fmt.Errorf("sidebar modules must be a JSON object")
	}

	if raw, ok := object["modules"]; ok {
		if err := requireJSONObject(raw, "modules"); err != nil {
			return err
		}
	}
	if raw, ok := object["preferences"]; ok {
		var preferences map[string]json.RawMessage
		if err := json.Unmarshal(raw, &preferences); err != nil || preferences == nil {
			return fmt.Errorf("sidebar preferences must be a JSON object")
		}
		if density, ok := preferences["density"]; ok {
			var value string
			if err := json.Unmarshal(density, &value); err != nil || (value != "compact" && value != "comfortable") {
				return fmt.Errorf("sidebar density is invalid")
			}
		}
		if route, ok := preferences["default_route"]; ok {
			var value string
			if err := json.Unmarshal(route, &value); err != nil || !isInternalSidebarRoute(value) {
				return fmt.Errorf("sidebar default route is invalid")
			}
		}
		for _, key := range []string{"section_order", "hidden_sections", "hidden"} {
			if raw, ok := preferences[key]; ok {
				if err := requireJSONArray(raw, "sidebar "+key); err != nil {
					return err
				}
			}
		}
		if raw, ok := preferences["module_order"]; ok {
			if err := requireJSONObject(raw, "sidebar module_order"); err != nil {
				return err
			}
		}
	}

	return nil
}

func requireJSONObject(raw json.RawMessage, name string) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return fmt.Errorf("%s must be a JSON object", name)
	}
	return nil
}

func requireJSONArray(raw json.RawMessage, name string) error {
	var values []json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil || values == nil {
		return fmt.Errorf("%s must be a JSON array", name)
	}
	return nil
}

func isInternalSidebarRoute(value string) bool {
	return value == "" || (strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "//") && !strings.Contains(value, "\\"))
}

const (
	UsageLeaderboardVisibilityPublic    = "public"
	UsageLeaderboardVisibilityAnonymous = "anonymous"
	UsageLeaderboardVisibilityHidden    = "hidden"
)

// NormalizeUsageLeaderboardVisibility keeps legacy and malformed settings
// private by default. A blank value is intentionally treated as anonymous.
func NormalizeUsageLeaderboardVisibility(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case UsageLeaderboardVisibilityPublic:
		return UsageLeaderboardVisibilityPublic
	case UsageLeaderboardVisibilityHidden:
		return UsageLeaderboardVisibilityHidden
	default:
		return UsageLeaderboardVisibilityAnonymous
	}
}

func IsValidUsageLeaderboardVisibility(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case UsageLeaderboardVisibilityPublic, UsageLeaderboardVisibilityAnonymous, UsageLeaderboardVisibilityHidden:
		return true
	default:
		return false
	}
}

var (
	NotifyTypeEmail   = "email"   // Email 邮件
	NotifyTypeWebhook = "webhook" // Webhook
	NotifyTypeBark    = "bark"    // Bark 推送
	NotifyTypeGotify  = "gotify"  // Gotify 推送
)
