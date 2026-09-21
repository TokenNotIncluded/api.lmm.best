package system_setting

import (
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/setting/config"
)

type LegalSettings struct {
	UserAgreement   string `json:"user_agreement"`
	PrivacyPolicy   string `json:"privacy_policy"`
	UserAgreementEn string `json:"user_agreement_en"`
	PrivacyPolicyEn string `json:"privacy_policy_en"`
}

var defaultLegalSettings LegalSettings

func init() {
	config.GlobalConfig.Register("legal", &defaultLegalSettings)
}

func GetLegalSettings() *LegalSettings {
	return &defaultLegalSettings
}

func UserAgreementPublished() bool {
	return strings.TrimSpace(defaultLegalSettings.UserAgreement) != ""
}

func PrivacyPolicyPublished() bool {
	return strings.TrimSpace(defaultLegalSettings.PrivacyPolicy) != ""
}

func RegistrationPoliciesPublished() bool {
	return UserAgreementPublished() && PrivacyPolicyPublished()
}

// UserAgreementForLanguage returns the English variant when requested and
// configured, falling back to the primary (Chinese) content so an admin who
// has only filled in one language never serves a blank document.
func UserAgreementForLanguage(english bool) string {
	if english && strings.TrimSpace(defaultLegalSettings.UserAgreementEn) != "" {
		return defaultLegalSettings.UserAgreementEn
	}
	return defaultLegalSettings.UserAgreement
}

// PrivacyPolicyForLanguage mirrors UserAgreementForLanguage for the privacy
// policy document.
func PrivacyPolicyForLanguage(english bool) string {
	if english && strings.TrimSpace(defaultLegalSettings.PrivacyPolicyEn) != "" {
		return defaultLegalSettings.PrivacyPolicyEn
	}
	return defaultLegalSettings.PrivacyPolicy
}
