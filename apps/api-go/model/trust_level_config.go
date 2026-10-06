package model

import (
	"encoding/json"
	"errors"
	"io"
	"math"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/shopspring/decimal"
)

const TrustLevelBenefitsOptionKey = "TrustLevelBenefits"

// Thresholds are cumulative, successful external-paid credits, never cash or
// a display exchange rate. Benefit codes describe existing capabilities; they
// cannot grant an administrator role or bypass the developer-access boundary.
type TrustLevelConfiguration struct {
	Version               int                               `json:"version"`
	PaidActivationEnabled bool                              `json:"paid_activation_enabled"`
	DecayPeriodDays       int                               `json:"decay_period_days"`
	Tiers                 []TrustLevelConfigurationTier     `json:"tiers"`
	RoleTiers             []TrustLevelConfigurationRoleTier `json:"role_tiers,omitempty"`
}

type TrustLevelConfigurationRoleTier struct {
	Level         int      `json:"level"`
	Role          int      `json:"role"`
	DiscountRatio float64  `json:"discount_ratio"`
	Benefits      []string `json:"benefits"`
}

func defaultTrustRoleConfiguration() []TrustLevelConfigurationRoleTier {
	return []TrustLevelConfigurationRoleTier{
		{Level: TrustLevelAdmin, Role: common.RoleAdminUser, DiscountRatio: 0.9, Benefits: []string{"administrator_access", "usage_discount"}},
		{Level: TrustLevelRoot, Role: common.RoleRootUser, DiscountRatio: 0.9, Benefits: []string{"superadministrator_access", "usage_discount"}},
	}
}

type TrustLevelConfigurationTier struct {
	Level          int      `json:"level"`
	MinPaidCredits int64    `json:"min_paid_credits"`
	DiscountRatio  float64  `json:"discount_ratio"`
	Benefits       []string `json:"benefits"`
}

func legacyTrustLevelConfiguration() TrustLevelConfiguration {
	old := operation_setting.GetDeveloperAccessSetting()
	l1 := int64(1) // Any positive paid credit preserves the old zero-threshold rule.
	if old.PaidActivationMinAmount > 0 && !math.IsInf(old.PaidActivationMinAmount, 0) {
		amount := decimal.NewFromFloat(old.PaidActivationMinAmount).Mul(decimal.NewFromInt(500000)).Ceil()
		if amount.GreaterThan(decimal.NewFromInt(common.MaxWalletQuota)) {
			l1 = common.MaxWalletQuota
		} else {
			l1 = amount.IntPart()
		}
	}
	result := TrustLevelConfiguration{Version: 1, PaidActivationEnabled: old.PaidActivationEnabled, DecayPeriodDays: 90, RoleTiers: defaultTrustRoleConfiguration()}
	for level := 0; level <= TrustLevelMaxUser; level++ {
		threshold := int64(trustLevelThresholds[level] * 500000)
		if level == 1 {
			threshold = l1
		}
		result.Tiers = append(result.Tiers, TrustLevelConfigurationTier{Level: level, MinPaidCredits: threshold,
			DiscountRatio: trustLevelDiscountRatios[level], Benefits: append([]string(nil), trustLevelBenefits[level]...)})
	}
	return result
}

func ParseTrustLevelConfiguration(raw string) (TrustLevelConfiguration, error) {
	var result TrustLevelConfiguration
	if len(raw) > 16384 {
		return result, errors.New("level configuration is too large")
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return result, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return result, errors.New("level configuration must be one JSON object")
	}
	if result.Version != 1 || len(result.Tiers) != 5 || result.DecayPeriodDays < 0 || result.DecayPeriodDays > 3650 {
		return result, errors.New("configure exactly L0-L4 and a decay period between 0 and 3650 days")
	}
	for level, tier := range result.Tiers {
		if tier.Level != level || tier.MinPaidCredits < 0 || tier.MinPaidCredits > common.MaxWalletQuota ||
			math.IsNaN(tier.DiscountRatio) || math.IsInf(tier.DiscountRatio, 0) || tier.DiscountRatio <= 0 || tier.DiscountRatio > 1 {
			return result, errors.New("levels must be ordered L0-L4 with safe integer credit thresholds and discount ratios in (0,1]")
		}
		if level == 0 && tier.MinPaidCredits != 0 || level > 0 && tier.MinPaidCredits <= result.Tiers[level-1].MinPaidCredits {
			return result, errors.New("cumulative credit thresholds must start at zero and strictly increase")
		}
		if len(tier.Benefits) > 3 {
			return result, errors.New("unknown or duplicate level benefits")
		}
		seen := make(map[string]bool)
		for _, benefit := range tier.Benefits {
			if seen[benefit] || benefit != "standard_access" && benefit != "developer_access" && benefit != "usage_discount" {
				return result, errors.New("unknown or duplicate level benefits")
			}
			seen[benefit] = true
		}
	}
	if result.RoleTiers == nil {
		result.RoleTiers = defaultTrustRoleConfiguration()
	}
	if len(result.RoleTiers) != 2 {
		return result, errors.New("role benefits must configure exactly administrator L5 and superadministrator L6")
	}
	for i, tier := range result.RoleTiers {
		expected := defaultTrustRoleConfiguration()[i]
		if tier.Level != expected.Level || tier.Role != expected.Role || math.IsNaN(tier.DiscountRatio) || math.IsInf(tier.DiscountRatio, 0) || tier.DiscountRatio <= 0 || tier.DiscountRatio > 1 {
			return result, errors.New("role levels and account role mappings are immutable; configure a valid role usage discount")
		}
		if len(tier.Benefits) > 2 {
			return result, errors.New("unknown or duplicate role benefits")
		}
		seen := map[string]bool{}
		for _, benefit := range tier.Benefits {
			if seen[benefit] || benefit != expected.Benefits[0] && benefit != "usage_discount" {
				return result, errors.New("role benefit descriptions cannot grant another account role")
			}
			seen[benefit] = true
		}
	}
	return result, nil
}

// Unconfigured installations retain their existing activation threshold and
// discounts. The option is validated before persistence; a malformed retained
// value fails closed instead of manufacturing paid eligibility.
func GetTrustLevelConfiguration() TrustLevelConfiguration {
	common.OptionMapRWMutex.RLock()
	raw := common.OptionMap[TrustLevelBenefitsOptionKey]
	common.OptionMapRWMutex.RUnlock()
	if raw == "" {
		return legacyTrustLevelConfiguration()
	}
	if parsed, err := ParseTrustLevelConfiguration(raw); err == nil {
		return parsed
	}
	fallback := legacyTrustLevelConfiguration()
	fallback.PaidActivationEnabled = false
	for i := range fallback.Tiers {
		fallback.Tiers[i].DiscountRatio = 1
	}
	for i := range fallback.RoleTiers {
		fallback.RoleTiers[i].DiscountRatio = 1
	}
	return fallback
}

func TrustLevelConfigurationJSON() string {
	raw, _ := json.Marshal(GetTrustLevelConfiguration())
	return string(raw)
}

type TrustLevelRoleTier struct {
	Level           int      `json:"level"`
	Role            int      `json:"role"`
	RoleOnly        bool     `json:"role_only"`
	Benefits        []string `json:"benefits"`
	DiscountRatio   float64  `json:"discount_ratio"`
	DiscountPercent float64  `json:"discount_percent"`
}

func GetTrustLevelRoleTiers() []TrustLevelRoleTier {
	config := GetTrustLevelConfiguration()
	result := make([]TrustLevelRoleTier, 0, 2)
	for _, tier := range config.RoleTiers {
		result = append(result, TrustLevelRoleTier{Level: tier.Level, Role: tier.Role, RoleOnly: true, Benefits: append([]string(nil), tier.Benefits...), DiscountRatio: tier.DiscountRatio, DiscountPercent: (1 - tier.DiscountRatio) * 100})
	}
	return result
}
