package setting

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"sync"
	"unicode/utf8"
)

const (
	ModerationEnabledOptionKey          = "ModerationEnabled"
	ModerationGroupOptionKey            = "ModerationGroup"
	ModerationModelOptionKey            = "ModerationModel"
	ModerationGroupPoliciesOptionKey    = "ModerationGroupPolicies"
	AssistantModerationEnabledOptionKey = "AssistantModerationEnabled"
	AssistantModerationGroupOptionKey   = "AssistantModerationGroup"
	AssistantModerationModelOptionKey   = "AssistantModerationModel"
	DefaultModerationGroup              = "default"
	DefaultModerationModel              = "omni-moderation-latest"
	ModerationModeOff                   = "off"
	ModerationModeTolerant              = "tolerant"
	ModerationModeStrict                = "strict"
	ModerationMaxCategoryFineUSD        = 1000
	ModerationAmountCurrencyLegacy      = "legacy_pricing_unit"
	ModerationAmountCurrencyUSD         = "USD"
	moderationMaxGroupPolicies          = 64
	moderationMaxPoliciesJSONBytes      = 65536
)

type ModerationGroupPolicy struct {
	Mode string `json:"mode"`
	// Absence preserves the unit of historical numeric maps. USD is explicit
	// for newly edited policies; loading old policies never rewrites their sums.
	AmountCurrency   string             `json:"amount_currency,omitempty"`
	CategoryFinesUSD map[string]float64 `json:"category_fines_usd,omitempty"`
}

func IsModerationAmountCurrency(currency string) bool {
	return currency == "" || currency == ModerationAmountCurrencyLegacy || currency == ModerationAmountCurrencyUSD
}

func ResolveModerationAmountCurrency(currency string) string {
	if currency == "" {
		return ModerationAmountCurrencyLegacy
	}
	return currency
}

// ModerationSettings keeps assistant and API routing independent. Policies
// apply to the actual account group, never the moderation routing group.
type ModerationSettings struct {
	Enabled          bool
	Group            string
	Model            string
	GroupPolicies    map[string]ModerationGroupPolicy
	AssistantEnabled bool
	AssistantGroup   string
	AssistantModel   string
}

var moderationCategories = []string{
	"harassment", "harassment/threatening", "hate", "hate/threatening",
	"illicit", "illicit/violent", "self-harm", "self-harm/intent",
	"self-harm/instructions", "sexual", "sexual/minors", "violence", "violence/graphic",
}

var (
	moderationSettingsMu sync.RWMutex
	moderationSettings   = DefaultModerationSettings()
)

func DefaultModerationSettings() ModerationSettings {
	return ModerationSettings{
		Group: DefaultModerationGroup, Model: DefaultModerationModel,
		AssistantGroup: DefaultModerationGroup, AssistantModel: DefaultModerationModel,
		GroupPolicies: map[string]ModerationGroupPolicy{},
	}
}

func IsModerationModel(model string) bool {
	return model == DefaultModerationModel || model == "omni-moderation-2024-09-26"
}

func ModerationCategories() []string {
	return append([]string(nil), moderationCategories...)
}

func IsModerationCategory(category string) bool {
	for _, allowed := range moderationCategories {
		if category == allowed {
			return true
		}
	}
	return false
}

func IsModerationOption(key string) bool {
	switch key {
	case ModerationEnabledOptionKey, ModerationGroupOptionKey, ModerationModelOptionKey,
		ModerationGroupPoliciesOptionKey, AssistantModerationEnabledOptionKey,
		AssistantModerationGroupOptionKey, AssistantModerationModelOptionKey:
		return true
	}
	return false
}

func ModerationGroupPoliciesJSON(policies map[string]ModerationGroupPolicy) string {
	if policies == nil {
		return "{}"
	}
	encoded, err := json.Marshal(policies)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}

func (settings ModerationSettings) OptionValues() map[string]string {
	return map[string]string{
		ModerationEnabledOptionKey:          fmt.Sprintf("%t", settings.Enabled),
		ModerationGroupOptionKey:            settings.Group,
		ModerationModelOptionKey:            settings.Model,
		ModerationGroupPoliciesOptionKey:    ModerationGroupPoliciesJSON(settings.GroupPolicies),
		AssistantModerationEnabledOptionKey: fmt.Sprintf("%t", settings.AssistantEnabled),
		AssistantModerationGroupOptionKey:   settings.AssistantGroup,
		AssistantModerationModelOptionKey:   settings.AssistantModel,
	}
}

func ParseModerationGroupPolicies(value string) (map[string]ModerationGroupPolicy, error) {
	if len(value) > moderationMaxPoliciesJSONBytes {
		return nil, errors.New("moderation group policies are too large")
	}
	var policies map[string]ModerationGroupPolicy
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&policies); err != nil {
		return nil, fmt.Errorf("invalid moderation group policies: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, errors.New("moderation group policies must be one JSON object")
	}
	if policies == nil || len(policies) > moderationMaxGroupPolicies {
		return nil, errors.New("moderation group policies must be an object with at most 64 groups")
	}
	// encoding/json otherwise converts an explicit null float to zero. Treat
	// a missing fine as zero, but require every supplied amount to be numeric.
	var rawPolicies map[string]struct {
		AmountCurrency   json.RawMessage            `json:"amount_currency"`
		CategoryFinesUSD map[string]json.RawMessage `json:"category_fines_usd"`
	}
	if err := json.Unmarshal([]byte(value), &rawPolicies); err != nil {
		return nil, err
	}
	for group, policy := range policies {
		if strings.TrimSpace(group) != group || group == "" || group == "*" || utf8.RuneCountInString(group) > 64 {
			return nil, errors.New("moderation policies require explicit account groups; wildcards are not supported")
		}
		if !IsModerationAmountCurrency(policy.AmountCurrency) || strings.TrimSpace(string(rawPolicies[group].AmountCurrency)) == "null" {
			return nil, errors.New("moderation amount_currency must be USD or legacy_pricing_unit")
		}
		switch policy.Mode {
		case ModerationModeOff, ModerationModeTolerant, ModerationModeStrict:
		default:
			return nil, fmt.Errorf("moderation policy for %s must use off, tolerant or strict mode", group)
		}
		for category, fine := range policy.CategoryFinesUSD {
			if strings.TrimSpace(string(rawPolicies[group].CategoryFinesUSD[category])) == "null" {
				return nil, errors.New("moderation category fines must be numeric USD amounts")
			}
			if !IsModerationCategory(category) {
				return nil, fmt.Errorf("unknown moderation category: %s", category)
			}
			if err := ValidateModerationFineUSD(fine); err != nil {
				return nil, err
			}
		}
	}
	return cloneModerationPolicies(policies), nil
}

func ValidateModerationFineUSD(amount float64) error {
	if math.IsNaN(amount) || math.IsInf(amount, 0) || amount < 0 || amount > ModerationMaxCategoryFineUSD || amount != math.Round(amount*1000000)/1000000 {
		return errors.New("moderation category fines must be finite USD amounts between 0 and 1000 with at most 6 decimal places")
	}
	return nil
}

// ParseModerationSettings validates a candidate as a unit. Route availability
// and account-group existence are checked by the model layer before storage.
func ParseModerationSettings(base ModerationSettings, values map[string]string) (ModerationSettings, error) {
	settings := cloneModerationSettings(base)
	for key, raw := range values {
		if !IsModerationOption(key) {
			continue
		}
		value := strings.TrimSpace(raw)
		switch key {
		case ModerationEnabledOptionKey, AssistantModerationEnabledOptionKey:
			if value != "true" && value != "false" {
				return base, fmt.Errorf("%s must be true or false", key)
			}
			if key == ModerationEnabledOptionKey {
				settings.Enabled = value == "true"
			} else {
				settings.AssistantEnabled = value == "true"
			}
		case ModerationGroupOptionKey, AssistantModerationGroupOptionKey:
			if value == "" || value == "*" || utf8.RuneCountInString(value) > 64 {
				return base, fmt.Errorf("%s must be an explicit group of at most 64 characters", key)
			}
			if key == ModerationGroupOptionKey {
				settings.Group = value
			} else {
				settings.AssistantGroup = value
			}
		case ModerationModelOptionKey, AssistantModerationModelOptionKey:
			if !IsModerationModel(value) {
				return base, errors.New("choose omni-moderation-latest or omni-moderation-2024-09-26")
			}
			if key == ModerationModelOptionKey {
				settings.Model = value
			} else {
				settings.AssistantModel = value
			}
		case ModerationGroupPoliciesOptionKey:
			policies, err := ParseModerationGroupPolicies(value)
			if err != nil {
				return base, err
			}
			settings.GroupPolicies = policies
		}
	}
	return settings, nil
}

func GetModerationSettings() ModerationSettings {
	moderationSettingsMu.RLock()
	defer moderationSettingsMu.RUnlock()
	return cloneModerationSettings(moderationSettings)
}

// WithCurrentModerationSettings holds the local configuration read fence
// until a final effect completes. The callback must not read or write these
// settings again. Cross-node effects also lock the authoritative DB options.
func WithCurrentModerationSettings(apply func(ModerationSettings) error) error {
	moderationSettingsMu.RLock()
	defer moderationSettingsMu.RUnlock()
	return apply(cloneModerationSettings(moderationSettings))
}

// UpdateModerationSettings validates and publishes one complete snapshot.
// Invalid persisted configuration turns both reviewers off rather than
// leaving an older enabled policy active indefinitely.
func UpdateModerationSettings(values map[string]string) error {
	moderationSettingsMu.Lock()
	defer moderationSettingsMu.Unlock()
	candidate, err := ParseModerationSettings(moderationSettings, values)
	if err != nil {
		moderationSettings.Enabled = false
		moderationSettings.AssistantEnabled = false
		return err
	}
	moderationSettings = candidate
	return nil
}

func ModerationPolicyForGroup(group string) (ModerationGroupPolicy, bool) {
	return ResolveModerationPolicy(GetModerationSettings(), group)
}

func ResolveModerationPolicy(settings ModerationSettings, group string) (ModerationGroupPolicy, bool) {
	policy, ok := settings.GroupPolicies[group]
	if !ok || group == "*" || group == "" {
		return ModerationGroupPolicy{Mode: ModerationModeOff}, false
	}
	return cloneModerationPolicy(policy), true
}

func cloneModerationSettings(settings ModerationSettings) ModerationSettings {
	settings.GroupPolicies = cloneModerationPolicies(settings.GroupPolicies)
	return settings
}

func cloneModerationPolicy(policy ModerationGroupPolicy) ModerationGroupPolicy {
	if policy.CategoryFinesUSD != nil {
		fines := make(map[string]float64, len(policy.CategoryFinesUSD))
		for category, value := range policy.CategoryFinesUSD {
			fines[category] = value
		}
		policy.CategoryFinesUSD = fines
	}
	return policy
}

func cloneModerationPolicies(source map[string]ModerationGroupPolicy) map[string]ModerationGroupPolicy {
	policies := make(map[string]ModerationGroupPolicy, len(source))
	for group, policy := range source {
		policies[group] = cloneModerationPolicy(policy)
	}
	return policies
}
