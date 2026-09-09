package model

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
)

const ModelPriceLockOptionKey = "ModelPriceLock"

// Serialize lock checks, persistence and cache refreshes within this process.
// The option-map mutex only protects the individual in-memory map accesses.
var optionUpdateMutex sync.Mutex

var modelPricingOptionKeys = map[string]bool{
	"ModelRatio": true, "ModelPrice": true, "CompletionRatio": true,
	"AudioRatio": true, "AudioCompletionRatio": true,
	"CacheRatio": false, "CreateCacheRatio": false, "ImageRatio": false,
	"billing_setting.billing_mode": false, "billing_setting.billing_expr": false,
}

func decodePricingObject(value string) (map[string]json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(value), &object); err != nil {
		return nil, err
	}
	if object == nil {
		object = make(map[string]json.RawMessage)
	}
	return object, nil
}

func parseModelPriceLocks(value string) (map[string]bool, error) {
	if value == "" {
		return map[string]bool{}, nil
	}
	if strings.TrimSpace(value) == "null" {
		return nil, fmt.Errorf("%s must be a JSON object", ModelPriceLockOptionKey)
	}
	object, err := decodePricingObject(value)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", ModelPriceLockOptionKey, err)
	}
	locks := make(map[string]bool, len(object))
	for name, raw := range object {
		if strings.TrimSpace(name) == "" || (string(raw) != "true" && string(raw) != "false") {
			return nil, fmt.Errorf("%s must map non-empty model names to booleans", ModelPriceLockOptionKey)
		}
		locks[name] = string(raw) == "true"
	}
	return locks, nil
}

func modelNameLocked(name string, locks map[string]bool, normalize bool) bool {
	if locks[name] {
		return true
	}
	if normalize {
		name = ratio_setting.FormatMatchingModelName(name)
		for lockedName, locked := range locks {
			if locked && ratio_setting.FormatMatchingModelName(lockedName) == name {
				return true
			}
		}
	}
	return false
}

func IsModelPricingLocked(name string) bool {
	common.OptionMapRWMutex.RLock()
	value := common.OptionMap[ModelPriceLockOptionKey]
	common.OptionMapRWMutex.RUnlock()
	locks, err := parseModelPriceLocks(value)
	return err == nil && modelNameLocked(name, locks, true)
}

func pricingValuesEqual(left, right json.RawMessage) bool {
	var a, b any
	return json.Unmarshal(left, &a) == nil && json.Unmarshal(right, &b) == nil && reflect.DeepEqual(a, b)
}

// FilterLockedModelPricing restores saved entries, including their absence, for
// locked models. Filtering happens before validating entry values so an invalid
// attempted edit to a locked price is ignored just like a valid edit. The saved
// lock map applies even when the request also unlocks a model.
func FilterLockedModelPricing(values map[string]string) (map[string]string, []string, error) {
	common.OptionMapRWMutex.RLock()
	saved := make(map[string]string, len(modelPricingOptionKeys)+1)
	saved[ModelPriceLockOptionKey] = common.OptionMap[ModelPriceLockOptionKey]
	for key := range modelPricingOptionKeys {
		saved[key] = common.OptionMap[key]
	}
	common.OptionMapRWMutex.RUnlock()
	locks, err := parseModelPriceLocks(saved[ModelPriceLockOptionKey])
	if err != nil {
		return nil, nil, err
	}
	filtered := make(map[string]string, len(values))
	changedModels := make(map[string]struct{})
	for key, value := range values {
		filtered[key] = value
		normalize, isPricing := modelPricingOptionKeys[key]
		if !isPricing {
			continue
		}
		candidate, err := decodePricingObject(value)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", key, err)
		}
		previousValue := saved[key]
		if previousValue == "" {
			previousValue = "{}"
		}
		previous, err := decodePricingObject(previousValue)
		if err != nil {
			return nil, nil, fmt.Errorf("saved %s: %w", key, err)
		}
		names := make(map[string]struct{}, len(previous)+len(candidate))
		for name := range previous {
			names[name] = struct{}{}
		}
		for name := range candidate {
			names[name] = struct{}{}
		}
		changed := false
		for name := range names {
			if !modelNameLocked(name, locks, normalize) {
				continue
			}
			before, existed := previous[name]
			after, exists := candidate[name]
			if existed == exists && pricingValuesEqual(before, after) {
				continue
			}
			if existed {
				candidate[name] = before
			} else {
				delete(candidate, name)
			}
			changedModels[name] = struct{}{}
			changed = true
		}
		if changed {
			encoded, err := json.Marshal(candidate)
			if err != nil {
				return nil, nil, err
			}
			filtered[key] = string(encoded)
		}
	}
	names := make([]string, 0, len(changedModels))
	for name := range changedModels {
		names = append(names, name)
	}
	sort.Strings(names)
	warnings := make([]string, 0, len(names))
	for _, name := range names {
		warnings = append(warnings, fmt.Sprintf("模型 %s 的价格已锁定，已忽略本次价格修改", name))
	}
	return filtered, warnings, nil
}

func validateModelPricingOption(key, value string) error {
	if key == ModelPriceLockOptionKey {
		if value == "" {
			return fmt.Errorf("%s must be a JSON object", key)
		}
		_, err := parseModelPriceLocks(value)
		return err
	}
	if _, ok := modelPricingOptionKeys[key]; !ok {
		return nil
	}
	var err error
	if strings.HasPrefix(key, "billing_setting.") {
		var entries map[string]string
		err = json.Unmarshal([]byte(value), &entries)
	} else {
		var entries map[string]float64
		err = json.Unmarshal([]byte(value), &entries)
	}
	return err
}

func logPricingWarnings(warnings []string) {
	for _, warning := range warnings {
		common.SysLog(warning)
	}
}
