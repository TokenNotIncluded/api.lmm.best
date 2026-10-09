package setting

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every registered tool must support persisted switches and its own editable
// level rule. This table automatically includes newly registered tools.
func TestAssistantEveryToolConfigurationRoundTrip(t *testing.T) {
	count := 0
	for _, group := range AssistantToolCatalogue() {
		for _, tool := range group.Tools {
			count++
			t.Run(tool.Name, func(t *testing.T) {
				defaults := DefaultAssistantToolRule(tool.Name)
				policy := AssistantToolPolicy{Version: 1, Groups: map[string]bool{}, Tools: map[string]bool{tool.Name: true}, Rules: map[string]AssistantToolRule{tool.Name: defaults}}
				encode := func() AssistantToolPolicy {
					raw, err := json.Marshal(policy)
					require.NoError(t, err)
					canonical, parsed, err := NormalizeAssistantToolPolicy(string(raw))
					require.NoError(t, err)
					_, again, err := NormalizeAssistantToolPolicy(canonical)
					require.NoError(t, err)
					assert.Equal(t, parsed, again)
					return parsed
				}
				parsed := encode()
				for level := 0; level <= 6; level++ {
					assert.Equal(t, level >= defaults.MinLevel && level <= defaults.MaxLevel, parsed.AllowedAtLevel(tool.Name, level))
				}
				narrowed := defaults
				narrowed.MaxLevel = narrowed.MinLevel
				policy.Rules[tool.Name] = narrowed
				parsed = encode()
				assert.True(t, parsed.AllowedAtLevel(tool.Name, narrowed.MinLevel))
				policy.Tools[tool.Name] = false
				parsed = encode()
				assert.False(t, parsed.Enabled(tool.Name))
				policy.Tools[tool.Name] = true
				policy.Groups[group.ID] = false
				parsed = encode()
				assert.False(t, parsed.Enabled(tool.Name))
			})
		}
	}
	require.Equal(t, 70, count)
}
