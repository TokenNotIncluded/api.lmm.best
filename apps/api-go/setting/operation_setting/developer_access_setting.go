package operation_setting

import (
	"math"

	"github.com/LIghtJUNction/api.lmm.best/setting/config"
)

// DeveloperAccessSetting describes when recharge history alone is allowed to
// establish the L1 developer boundary. Everything it does not grant still
// reaches the console through manual review.
type DeveloperAccessSetting struct {
	// PaidActivationEnabled lets a qualifying real-money recharge grant access
	// without review. Turning it off routes every account through review.
	PaidActivationEnabled bool `json:"paid_activation_enabled"`
	// PaidActivationMinAmount is the cumulative credited amount in USD the
	// account has to reach. Zero keeps the historical behaviour where any
	// successful real-money recharge qualifies.
	PaidActivationMinAmount float64 `json:"paid_activation_min_amount"`
}

var developerAccessSetting = DeveloperAccessSetting{
	PaidActivationEnabled:   true,
	PaidActivationMinAmount: 1,
}

func init() {
	config.GlobalConfig.Register("developer_access_setting", &developerAccessSetting)
}

func GetDeveloperAccessSetting() *DeveloperAccessSetting {
	return &developerAccessSetting
}

// PaidActivationMinAmountMicros normalizes the configured threshold into the
// USD micros the payment aggregates are measured in. A negative or otherwise
// unusable value degrades to "any successful recharge" instead of locking the
// boundary shut on a typo.
func PaidActivationMinAmountMicros() int64 {
	amount := developerAccessSetting.PaidActivationMinAmount
	if !(amount > 0) || math.IsInf(amount, 0) {
		return 0
	}
	return int64(math.Round(amount * 1_000_000))
}
