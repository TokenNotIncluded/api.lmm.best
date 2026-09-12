package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"sort"
	"strings"
	"sync"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting/billing_setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/config"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const ModelPriceLocksOptionKey = "ModelPriceLock"

// Serializes persistence and runtime publication, including periodic reloads.
var optionUpdateMutex sync.Mutex

var modelPriceOptionKeys = []string{
	"ModelRatio", "CompletionRatio", "ModelPrice", "CacheRatio", "CreateCacheRatio",
	"ImageRatio", "AudioRatio", "AudioCompletionRatio",
	"billing_setting.billing_mode", "billing_setting.billing_expr",
}

type OptionUpdateResult struct {
	Warnings     []string `json:"warnings,omitempty"`
	LockedModels []string `json:"locked_models,omitempty"`
}

func isModelPriceOption(key string) bool {
	for _, candidate := range modelPriceOptionKeys {
		if key == candidate {
			return true
		}
	}
	return false
}

func parseModelPriceLocks(value string) (map[string]bool, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(value), &raw); err != nil || raw == nil {
		return nil, errors.New("ModelPriceLock must be a JSON object of booleans")
	}
	locks := make(map[string]bool, len(raw))
	for name, value := range raw {
		var locked bool
		if strings.TrimSpace(name) == "" || string(value) == "null" || json.Unmarshal(value, &locked) != nil {
			return nil, errors.New("ModelPriceLock must contain model names and boolean values")
		}
		locks[name] = locked
	}
	return locks, nil
}

func GetModelPriceLocksCopy() map[string]bool {
	common.OptionMapRWMutex.RLock()
	value := common.OptionMap[ModelPriceLocksOptionKey]
	common.OptionMapRWMutex.RUnlock()
	locks, _ := parseModelPriceLocks(value)
	if locks == nil {
		locks = make(map[string]bool)
	}
	return locks
}

func IsModelPriceLocked(model string) bool { return GetModelPriceLocksCopy()[model] }

func priceOptionSnapshot() map[string]string {
	values := map[string]string{
		ModelPriceLocksOptionKey: "{}",
		"ModelRatio":             ratio_setting.ModelRatio2JSONString(),
		"CompletionRatio":        ratio_setting.CompletionRatio2JSONString(),
		"ModelPrice":             ratio_setting.ModelPrice2JSONString(),
		"CacheRatio":             ratio_setting.CacheRatio2JSONString(),
		"CreateCacheRatio":       ratio_setting.CreateCacheRatio2JSONString(),
		"ImageRatio":             ratio_setting.ImageRatio2JSONString(),
		"AudioRatio":             ratio_setting.AudioRatio2JSONString(),
		"AudioCompletionRatio":   ratio_setting.AudioCompletionRatio2JSONString(),
	}
	for key, value := range config.GlobalConfig.ExportAllConfigs() {
		if isModelPriceOption(key) {
			values[key] = value
		}
	}
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	for key := range values {
		if value, exists := common.OptionMap[key]; exists {
			values[key] = value
		}
	}
	return values
}

func loadPriceOptionSnapshot(db *gorm.DB) (map[string]string, error) {
	values := priceOptionSnapshot()
	if db == nil {
		return values, nil
	}
	keys := append([]string{ModelPriceLocksOptionKey}, modelPriceOptionKeys...)
	var saved []Option
	if err := db.Where(clause.IN{Column: clause.Column{Name: "key"}, Values: stringInterfaces(keys)}).Find(&saved).Error; err != nil {
		return nil, err
	}
	for _, option := range saved {
		values[option.Key] = option.Value
	}
	return values, nil
}

func stringInterfaces(values []string) []interface{} {
	result := make([]interface{}, len(values))
	for i, value := range values {
		result[i] = value
	}
	return result
}

// Preserve locked entries, including deletions and invalid attempted values,
// before validating accepted values. A batch protects both old and new locks.
func filterLockedModelPriceChanges(values, current map[string]string) (map[string]string, OptionUpdateResult, error) {
	filtered := maps.Clone(values)
	result := OptionUpdateResult{}
	locks, err := parseModelPriceLocks(current[ModelPriceLocksOptionKey])
	if err != nil {
		return nil, result, err
	}
	if value, exists := values[ModelPriceLocksOptionKey]; exists {
		proposed, err := parseModelPriceLocks(value)
		if err != nil {
			return nil, result, err
		}
		for name, locked := range proposed {
			if locked {
				locks[name] = true
			}
		}
	}
	ignored := make(map[string]bool)
	for key, value := range values {
		if !isModelPriceOption(key) {
			continue
		}
		var proposed, previous map[string]json.RawMessage
		if err := json.Unmarshal([]byte(value), &proposed); err != nil || proposed == nil {
			return nil, result, fmt.Errorf("%s must be a JSON object", key)
		}
		if old := current[key]; old != "" {
			if err := json.Unmarshal([]byte(old), &previous); err != nil {
				return nil, result, fmt.Errorf("cannot read existing %s: %w", key, err)
			}
		}
		for name, locked := range locks {
			if !locked {
				continue
			}
			old, oldExists := previous[name]
			next, nextExists := proposed[name]
			var oldValue, nextValue interface{}
			_ = json.Unmarshal(old, &oldValue)
			_ = json.Unmarshal(next, &nextValue)
			if oldExists == nextExists && reflect.DeepEqual(oldValue, nextValue) {
				continue
			}
			ignored[name] = true
			if oldExists {
				proposed[name] = old
			} else {
				delete(proposed, name)
			}
		}
		encoded, err := json.Marshal(proposed)
		if err != nil {
			return nil, result, err
		}
		filtered[key] = string(encoded)
	}
	for name := range ignored {
		result.LockedModels = append(result.LockedModels, name)
	}
	sort.Strings(result.LockedModels)
	if len(result.LockedModels) > 0 {
		result.Warnings = []string{"已忽略已锁定模型的价格修改：" + strings.Join(result.LockedModels, "、")}
	}
	return filtered, result, nil
}

func hasModelPriceOptions(values map[string]string) bool {
	for key := range values {
		if key == ModelPriceLocksOptionKey || isModelPriceOption(key) {
			return true
		}
	}
	return false
}

// FilterLockedModelPriceChanges is a read-only preview; writes always recheck
// the database policy inside their own transaction.
func FilterLockedModelPriceChanges(values map[string]string) (map[string]string, OptionUpdateResult, error) {
	if !hasModelPriceOptions(values) {
		return maps.Clone(values), OptionUpdateResult{}, nil
	}
	optionUpdateMutex.Lock()
	defer optionUpdateMutex.Unlock()
	current, err := loadPriceOptionSnapshot(DB)
	if err != nil {
		return nil, OptionUpdateResult{}, err
	}
	return filterLockedModelPriceChanges(values, current)
}

func validateModelPriceValues(values map[string]string) error {
	for key, value := range values {
		if key == ModelPriceLocksOptionKey {
			if _, err := parseModelPriceLocks(value); err != nil {
				return err
			}
			continue
		}
		if !isModelPriceOption(key) {
			continue
		}
		var raw map[string]json.RawMessage
		if json.Unmarshal([]byte(value), &raw) != nil || raw == nil {
			return fmt.Errorf("%s must be a JSON object", key)
		}
		for name, encoded := range raw {
			if strings.TrimSpace(name) == "" || string(encoded) == "null" {
				return fmt.Errorf("invalid %s value for model %q", key, name)
			}
			if strings.HasPrefix(key, "billing_setting.") {
				var text string
				if json.Unmarshal(encoded, &text) != nil {
					return fmt.Errorf("%s for %s must be a string", key, name)
				}
				if key == "billing_setting.billing_mode" {
					if text != "" && text != billing_setting.BillingModeRatio && text != billing_setting.BillingModeTieredExpr {
						return fmt.Errorf("invalid billing mode for %s", name)
					}
				} else if strings.TrimSpace(text) != "" {
					if err := billing_setting.SmokeTestExpr(text); err != nil {
						return fmt.Errorf("invalid billing expression for %s: %w", name, err)
					}
				}
			} else {
				var number float64
				if json.Unmarshal(encoded, &number) != nil {
					return fmt.Errorf("%s for %s must be a number", key, name)
				}
			}
		}
	}
	return nil
}

func ValidateOptionValuesWithWarnings(values map[string]string) (OptionUpdateResult, error) {
	filtered, result, err := FilterLockedModelPriceChanges(values)
	if err != nil {
		return result, err
	}
	if err := validateOptionValues(filtered); err != nil {
		return result, err
	}
	return result, validateModelPriceValues(filtered)
}

func UpdateOptionWithWarnings(key, value string) (OptionUpdateResult, error) {
	return UpdateOptionsBulkWithWarnings(map[string]string{key: value})
}

func UpdateOptionsBulkWithWarnings(values map[string]string) (OptionUpdateResult, error) {
	return updateOptionsWithPriceLocks(values, "", false)
}

// UpdateModelPriceLock changes one model against the authoritative stored map,
// so stale tabs and concurrent administrators cannot drop each other's locks.
func UpdateModelPriceLock(model string, locked bool) (OptionUpdateResult, error) {
	if strings.TrimSpace(model) == "" {
		return OptionUpdateResult{}, errors.New("model is required")
	}
	return updateOptionsWithPriceLocks(map[string]string{ModelPriceLocksOptionKey: "{}"}, model, locked)
}

func updateOptionsWithPriceLocks(values map[string]string, lockModel string, locked bool) (OptionUpdateResult, error) {
	result := OptionUpdateResult{}
	if len(values) == 0 {
		return result, nil
	}
	optionUpdateMutex.Lock()
	defer optionUpdateMutex.Unlock()
	values = maps.Clone(values)
	// Route validation may query DB. Do it before opening the transaction so a
	// deployment with one database connection cannot deadlock on a nested query.
	nonPricing := make(map[string]string)
	for key, value := range values {
		if !isModelPriceOption(key) && key != ModelPriceLocksOptionKey {
			nonPricing[key] = value
		}
	}
	if len(nonPricing) > 0 {
		if err := validateOptionValues(nonPricing); err != nil {
			return result, err
		}
	}
	var accepted map[string]string
	var keys []string
	err := DB.Transaction(func(tx *gorm.DB) error {
		accepted = values
		_, groupRatioChanged := values["GroupRatio"]
		_, groupOverrideChanged := values["GroupGroupRatio"]
		if hasModelPriceOptions(values) || groupRatioChanged || groupOverrideChanged {
			// Every price/lock writer locks the same existing policy row, providing
			// database-wide ordering as well as the in-process mutex above.
			policy := Option{Key: ModelPriceLocksOptionKey, Value: "{}"}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&policy).Error; err != nil {
				return err
			}
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&policy, &Option{Key: ModelPriceLocksOptionKey}).Error; err != nil {
				return err
			}
			current, err := loadPriceOptionSnapshot(tx)
			if err != nil {
				return err
			}
			if lockModel != "" {
				locks, err := parseModelPriceLocks(policy.Value)
				if err != nil {
					return err
				}
				if locked {
					locks[lockModel] = true
				} else {
					delete(locks, lockModel)
				}
				encoded, err := json.Marshal(locks)
				if err != nil {
					return err
				}
				values[ModelPriceLocksOptionKey] = string(encoded)
			}
			accepted, result, err = filterLockedModelPriceChanges(values, current)
			if err != nil {
				return err
			}
			if err := validateModelPriceValues(accepted); err != nil {
				return err
			}
		}
		if err := recordRatioNotification(tx, accepted); err != nil {
			return err
		}
		keys = sortedOptionUpdateKeys(accepted)
		for _, key := range keys {
			option := Option{Key: key, Value: accepted[key]}
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "key"}}, DoUpdates: clause.AssignmentColumns([]string{"value"})}).Create(&option).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return result, err
	}
	for _, key := range keys {
		if err := updateOptionMap(key, accepted[key]); err != nil {
			return result, err
		}
	}
	for _, warning := range result.Warnings {
		common.SysLog(warning)
	}
	return result, nil
}

func sortedOptionUpdateKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	// Publish the dynamic-pricing master switch last when enabling, first when disabling.
	const enabledKey = "dynamic_pricing_setting.enabled"
	if value, exists := values[enabledKey]; exists {
		others := make([]string, 0, len(keys)-1)
		for _, key := range keys {
			if key != enabledKey {
				others = append(others, key)
			}
		}
		if value == "false" {
			keys = append([]string{enabledKey}, others...)
		} else {
			keys = append(others, enabledKey)
		}
	}
	return keys
}
