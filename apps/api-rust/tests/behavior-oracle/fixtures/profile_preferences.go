// Export the current Go sidebar and privacy-preference validators.
package main

import (
	"encoding/json"
	"os"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
)

func main() {
	if len(os.Args) != 2 {
		panic("expected output path")
	}
	inputs := []string{"", "  ", "{}", "null", "[]", "invalid",
		`{"legacy":{"enabled":true}}`, `{"modules":{}}`, `{"modules":null}`,
		`{"preferences":null}`, `{"preferences":{"density":"compact"}}`,
		`{"preferences":{"density":"comfortable"}}`, `{"preferences":{"density":null}}`,
		`{"preferences":{"default_route":null}}`, `{"preferences":{"default_route":""}}`,
		`{"preferences":{"default_route":"//outside.test"}}`, `{"preferences":{"default_route":"/dashboard"}}`,
		`{"preferences":{"default_route":"/\\outside.test"}}`, `{"preferences":{"hidden":[]}}`,
		`{"preferences":{"hidden":null}}`, `{"preferences":{"module_order":{}}}`,
		`{"unknown":1e999}`, `{"unknown":"` + strings.Repeat("x", 16*1024) + `"}`,
	}
	sidebar := make([]map[string]any, 0, len(inputs))
	for _, input := range inputs {
		sidebar = append(sidebar, map[string]any{"input": input, "valid": dto.ValidateSidebarModules(input) == nil})
	}
	visibility := []map[string]any{}
	for _, input := range []string{"", "  ", "public", " PUBLIC ", "anonymous", " Anonymous ", "hidden", "HIDDEN", "everyone", "private"} {
		visibility = append(visibility, map[string]any{"input": input, "valid": dto.IsValidUsageLeaderboardVisibility(input), "normalized": dto.NormalizeUsageLeaderboardVisibility(input)})
	}
	encoded, err := json.MarshalIndent(map[string]any{"sidebar": sidebar, "visibility": visibility}, "", "  ")
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile(os.Args[1], append(encoded, '\n'), 0600); err != nil {
		panic(err)
	}
}
