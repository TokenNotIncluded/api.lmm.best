package model

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting"
)

func isAdvancedSecurityOptionKey(key string) bool {
	switch key {
	case setting.AdvancedSecurityEnabledOptionKey,
		setting.AdvancedSecurityOnPromptOptionKey,
		setting.AdvancedSecurityActionOptionKey,
		setting.AdvancedSecurityRulesOptionKey:
		return true
	default:
		return false
	}
}

func currentAdvancedSecurityOptionValues() map[string]string {
	current := setting.GetAdvancedSecuritySettings()
	rules, err := json.Marshal(current.RuleSet)
	if err != nil {
		rules = []byte(`{"version":1,"rules":[]}`)
	}
	return map[string]string{
		setting.AdvancedSecurityEnabledOptionKey:  strconv.FormatBool(current.Enabled),
		setting.AdvancedSecurityOnPromptOptionKey: strconv.FormatBool(current.OnPrompt),
		setting.AdvancedSecurityActionOptionKey:   current.Action,
		setting.AdvancedSecurityRulesOptionKey:    string(rules),
	}
}

// applyAdvancedSecurityOptionValues validates the complete persisted policy
// before publishing either its runtime settings or OptionMap representation.
// This prevents periodic multi-instance reloads from exposing mixed snapshots
// such as a new enable flag with stale rules.
func applyAdvancedSecurityOptionValues(values map[string]string) error {
	enabled, err := strconv.ParseBool(values[setting.AdvancedSecurityEnabledOptionKey])
	if err != nil {
		return fmt.Errorf("%s must be a boolean: %w", setting.AdvancedSecurityEnabledOptionKey, err)
	}
	onPrompt, err := strconv.ParseBool(values[setting.AdvancedSecurityOnPromptOptionKey])
	if err != nil {
		return fmt.Errorf("%s must be a boolean: %w", setting.AdvancedSecurityOnPromptOptionKey, err)
	}
	action := values[setting.AdvancedSecurityActionOptionKey]
	rules := values[setting.AdvancedSecurityRulesOptionKey]

	if err := setting.ApplyAdvancedSecuritySettings(enabled, onPrompt, action, rules); err != nil {
		return err
	}

	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	common.OptionMap[setting.AdvancedSecurityEnabledOptionKey] = values[setting.AdvancedSecurityEnabledOptionKey]
	common.OptionMap[setting.AdvancedSecurityOnPromptOptionKey] = values[setting.AdvancedSecurityOnPromptOptionKey]
	common.OptionMap[setting.AdvancedSecurityActionOptionKey] = values[setting.AdvancedSecurityActionOptionKey]
	common.OptionMap[setting.AdvancedSecurityRulesOptionKey] = values[setting.AdvancedSecurityRulesOptionKey]
	common.OptionMapRWMutex.Unlock()
	return nil
}
