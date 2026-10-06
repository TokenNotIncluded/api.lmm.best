package controller

import (
	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/i18n"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/gin-gonic/gin"
)

func settlementLanguageHint(c *gin.Context) string {
	if c == nil || c.Request == nil {
		return ""
	}
	return c.GetHeader("Accept-Language")
}

// The authenticated account owns this preference. A browser-provided amount or
// currency must never select the amount actually collected by a payment provider.
func userSettlementCurrency(user *model.User, languageHint string) string {
	if user == nil {
		return (dto.UserSetting{}).EffectiveSettlementCurrency(languageHint)
	}
	return user.GetSetting().EffectiveSettlementCurrency(languageHint)
}

// Returns true only for a locale-preference request, including an invalid one
// whose error response was already written. Both fields can be changed together
// without one early-return branch silently dropping the other.
func updateSelfLocalePreferences(c *gin.Context, request map[string]interface{}) bool {
	currencyValue, hasCurrency := request["settlement_currency"]
	languageValue, hasLanguage := request["language"]
	displayValue, hasDisplay := request["wallet_display_currency"]
	if !hasCurrency && !hasLanguage && !hasDisplay {
		return false
	}
	var language, currency, display *string
	if hasLanguage {
		value, ok := languageValue.(string)
		if !ok || len(value) > 64 {
			common.ApiErrorI18n(c, i18n.MsgInvalidParams)
			return true
		}
		language = &value
	}
	if hasCurrency {
		value, ok := currencyValue.(string)
		if !ok {
			common.ApiErrorI18n(c, i18n.MsgInvalidParams)
			return true
		}
		normalized, err := dto.NormalizeSettlementCurrencyPreference(value)
		if err != nil {
			common.ApiErrorI18n(c, i18n.MsgInvalidParams)
			return true
		}
		currency = &normalized
	}
	if hasDisplay {
		value, ok := displayValue.(string)
		if !ok {
			common.ApiErrorI18n(c, i18n.MsgInvalidParams)
			return true
		}
		normalized, err := dto.NormalizeWalletDisplayCurrencyPreference(value)
		if err != nil {
			common.ApiErrorI18n(c, i18n.MsgInvalidParams)
			return true
		}
		display = &normalized
	}
	if err := model.UpdateUserCurrencyPreferences(c.GetInt("id"), language, currency, display); err != nil {
		common.ApiErrorI18n(c, i18n.MsgUpdateFailed)
		return true
	}
	common.ApiSuccessI18n(c, i18n.MsgUpdateSuccess, nil)
	return true
}
