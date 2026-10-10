package operation_setting

import (
	"math"

	"github.com/LIghtJUNction/api.lmm.best/setting/config"
)

// DeveloperAccessSetting describes the independent registration and recharge
// paths that can establish the L1 developer boundary without manual review.
type DeveloperAccessSetting struct {
	// PaidActivationEnabled lets a qualifying real-money recharge grant access
	// without review. Turning it off disables only the recharge path.
	PaidActivationEnabled bool `json:"paid_activation_enabled"`
	// InviteRegistrationEnabled activates only newly created accounts whose
	// inviter has been validated in the account-creation transaction. Changing
	// this switch never activates existing accounts or revokes granted access.
	InviteRegistrationEnabled bool `json:"invite_registration_enabled"`
	// PaidActivationMinAmount is the cumulative credited legacy policy amount
	// (credits / immutable Q) an account has to reach, not USD. Existing
	// thresholds retain this basis. Zero keeps the historical behaviour where any
	// successful real-money recharge qualifies.
	PaidActivationMinAmount float64 `json:"paid_activation_min_amount"`
}

var developerAccessSetting = DeveloperAccessSetting{
	PaidActivationEnabled:   true,
	PaidActivationMinAmount: 1,
}

const InviteRegistrationEnabledOptionKey = "developer_access_setting.invite_registration_enabled"

func init() {
	config.GlobalConfig.Register("developer_access_setting", &developerAccessSetting)
}

func GetDeveloperAccessSetting() *DeveloperAccessSetting {
	return &developerAccessSetting
}

// PaidActivationMinAmountMicros normalizes the configured threshold into the
// legacy policy micros the payment aggregates are measured in. A negative or otherwise
// unusable value degrades to "any successful recharge" instead of locking the
// boundary shut on a typo.
func PaidActivationMinAmountMicros() int64 {
	amount := developerAccessSetting.PaidActivationMinAmount
	if !(amount > 0) || math.IsInf(amount, 0) {
		return 0
	}
	return int64(math.Round(amount * 1_000_000))
}
