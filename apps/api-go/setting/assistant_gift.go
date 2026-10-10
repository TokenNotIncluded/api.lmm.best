package setting

import (
	"errors"
	"strconv"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/shopspring/decimal"
)

const AssistantNewUserGiftMaxCreditsOptionKey = "AssistantNewUserGiftMaxCredits"

// This is the existing policy, used only until an administrator saves a Credit
// cap. Its legacy pricing scale must not become a fiat or wallet denomination.
const assistantLegacyGiftMaxCents = 1000

func ParseAssistantNewUserGiftMaxCredits(value string) (int64, error) {
	amount, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || amount < 0 || amount > common.MaxWalletQuota {
		return 0, errors.New("new-user gift maximum must be a non-negative integer within the supported credit range")
	}
	return amount, nil
}

// Empty is an internal unset value; ValidateAssistantOption rejects it for
// administrator writes. Keeping it unset preserves the pre-existing policy.
func UpdateAssistantNewUserGiftMaxCredits(value string) error {
	value = strings.TrimSpace(value)
	if value != "" {
		amount, err := ParseAssistantNewUserGiftMaxCredits(value)
		if err != nil {
			return err
		}
		value = strconv.FormatInt(amount, 10)
	}
	assistantSettingsMutex.Lock()
	defer assistantSettingsMutex.Unlock()
	assistantSettings.NewUserGiftMaxCredits = value
	return nil
}

func AssistantNewUserGiftMaxCredits(value string) (int, error) {
	if strings.TrimSpace(value) != "" {
		amount, err := ParseAssistantNewUserGiftMaxCredits(value)
		return int(amount), err
	}
	legacy, err := common.LegacyPricingQuotaPerUnit()
	if err != nil {
		return 0, err
	}
	return common.WalletQuotaFromDecimalStrict(legacy.Mul(decimal.NewFromInt(assistantLegacyGiftMaxCents)).Div(decimal.NewFromInt(100)).Round(0))
}
