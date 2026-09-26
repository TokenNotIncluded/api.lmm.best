// Run from apps/api-go to freeze the current backend's default pricing maps.
package main

import (
	"encoding/json"
	"os"

	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
)

func main() {
	if len(os.Args) != 2 {
		panic("expected output path")
	}
	ratio_setting.InitRatioSettings()
	values := map[string]json.RawMessage{
		"ModelPrice":       json.RawMessage(ratio_setting.ModelPrice2JSONString()),
		"ModelRatio":       json.RawMessage(ratio_setting.ModelRatio2JSONString()),
		"CompletionRatio":  json.RawMessage(ratio_setting.CompletionRatio2JSONString()),
		"CacheRatio":       json.RawMessage(ratio_setting.CacheRatio2JSONString()),
		"CreateCacheRatio": json.RawMessage(ratio_setting.CreateCacheRatio2JSONString()),
		"ImageRatio":       json.RawMessage(ratio_setting.ImageRatio2JSONString()),
		"GroupRatio":       json.RawMessage(ratio_setting.GroupRatio2JSONString()),
		"GroupGroupRatio":  json.RawMessage(ratio_setting.GroupGroupRatio2JSONString()),
	}
	data, err := json.MarshalIndent(values, "", "  ")
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile(os.Args[1], append(data, '\n'), 0644); err != nil {
		panic(err)
	}
}
