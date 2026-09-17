package model

import (
	"maps"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
)

// GetOptionsSnapshot waits for an entire local option publication, including
// database reloads. The returned values and derived ratio metadata describe
// the same publication. Callers still apply their normal visibility policy.
// Do not call from a writer that already holds optionUpdateMutex.
func GetOptionsSnapshot() map[string]string {
	optionUpdateMutex.Lock()
	defer optionUpdateMutex.Unlock()
	common.OptionMapRWMutex.RLock()
	values := maps.Clone(common.OptionMap)
	common.OptionMapRWMutex.RUnlock()
	if values == nil {
		values = make(map[string]string)
	}
	values["CompletionRatioMeta"] = buildCompletionRatioMetaValue(values)
	return values
}

var completionRatioMetaOptionKeys = []string{
	"ModelPrice",
	"ModelRatio",
	"CompletionRatio",
	"CacheRatio",
	"CreateCacheRatio",
	"ImageRatio",
	"AudioRatio",
	"AudioCompletionRatio",
}

func collectModelNamesFromOptionValue(raw string, modelNames map[string]struct{}) {
	if strings.TrimSpace(raw) == "" {
		return
	}

	parsed := make(map[string]any)
	if err := common.UnmarshalJsonStr(raw, &parsed); err != nil {
		return
	}
	if len(parsed) == 0 {
		return
	}

	for modelName := range maps.Keys(parsed) {
		modelNames[modelName] = struct{}{}
	}
}

func buildCompletionRatioMetaValue(optionValues map[string]string) string {
	modelNames := make(map[string]struct{})
	for _, key := range completionRatioMetaOptionKeys {
		collectModelNamesFromOptionValue(optionValues[key], modelNames)
	}
	if len(modelNames) == 0 {
		return "{}"
	}

	meta := make(map[string]ratio_setting.CompletionRatioInfo, len(modelNames))
	for modelName := range maps.Keys(modelNames) {
		meta[modelName] = ratio_setting.GetCompletionRatioInfo(modelName)
	}

	jsonBytes, err := common.Marshal(meta)
	if err != nil {
		return "{}"
	}
	return string(jsonBytes)
}
