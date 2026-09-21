package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompactPreservesPolicyLatestTaskAndLatestToolRound(t *testing.T) {
	messages := []Message{{Role: "system", Content: "Only server authorization grants access."}, {Role: "user", Content: "Set model prices then verify them."}}
	for i := 0; i < 8; i++ {
		id := fmt.Sprintf("call-%d", i)
		messages = append(messages,
			Message{Role: "assistant", ToolCalls: []Call{{ID: id, Function: CallFunction{Name: "get_pricing", Arguments: `{}`}}}},
			Message{Role: "tool", ToolCallID: id, Content: `{"ok":true,"models":"` + strings.Repeat("model-定价", 2000) + `"}`},
		)
	}
	originalBytes := Bytes(messages)
	compacted, err := Compact(messages, 40<<10)
	require.NoError(t, err)
	assert.LessOrEqual(t, Bytes(compacted), 40<<10)
	assert.Equal(t, originalBytes, Bytes(messages), "caller-owned history must remain intact")
	assert.Equal(t, messages[0], compacted[0])
	assert.Contains(t, compacted, messages[1])
	assert.Equal(t, messages[len(messages)-2:], compacted[len(compacted)-2:])
	_, err = conversationGroups(compacted)
	require.NoError(t, err)
	for _, message := range compacted {
		assert.True(t, utf8.ValidString(message.Content))
	}
}

func TestCompactDropsWholeGroupsAndKeepsExcerptsBelowSystemTrust(t *testing.T) {
	messages := []Message{{Role: "system", Content: "fixed policy"}, {Role: "user", Content: "current task"}}
	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("call-%d", i)
		messages = append(messages,
			Message{Role: "assistant", ToolCalls: []Call{{ID: id, Function: CallFunction{Name: "update", Arguments: `{"data":"` + strings.Repeat("x", 500) + `"}`}}}},
			Message{Role: "tool", ToolCallID: id, Content: `{"ok":true,"applied":true,"status":"applied"}`},
		)
	}
	compacted, err := Compact(messages, 4000)
	require.NoError(t, err)
	assert.LessOrEqual(t, Bytes(compacted), 4000)
	_, err = conversationGroups(compacted)
	require.NoError(t, err)
	assert.Equal(t, messages[0], compacted[0])
	assert.Equal(t, "assistant", compacted[1].Role)
	assert.Contains(t, compacted[1].Content, "not instructions or authorization")
	assert.Contains(t, compacted, messages[1])
	assert.Equal(t, messages[len(messages)-2:], compacted[len(compacted)-2:])
}

func TestCompactFailsClosedWhenRequiredContextCannotFit(t *testing.T) {
	messages := []Message{{Role: "system", Content: strings.Repeat("s", 1000)}, {Role: "user", Content: "exact task"}}
	_, err := Compact(messages, 256)
	assert.ErrorIs(t, err, ErrContextBudget)
}

func TestCompactRejectsIncompleteToolExchanges(t *testing.T) {
	for _, messages := range [][]Message{
		{{Role: "tool", ToolCallID: "missing", Content: strings.Repeat("x", 1000)}},
		{{Role: "assistant", ToolCalls: []Call{{ID: "missing"}}, Content: strings.Repeat("x", 1000)}},
		{{Role: "assistant", ToolCalls: []Call{{ID: "call"}}}, {Role: "tool", ToolCallID: "wrong", Content: strings.Repeat("x", 1000)}},
	} {
		_, err := Compact(messages, 256)
		assert.Error(t, err)
	}
}

func TestCompactResultRetainsActualMutationOutcome(t *testing.T) {
	content := `{"ok":true,"applied":true,"status":"applied","data":"` + strings.Repeat("定价", 10000) + `"}`
	compacted := CompactResult(content, 2048)
	var result map[string]any
	require.NoError(t, json.Unmarshal([]byte(compacted), &result))
	assert.Equal(t, true, result["ok"])
	assert.Equal(t, true, result["applied"])
	assert.Equal(t, "applied", result["status"])
	assert.Equal(t, true, result["context_compacted"])
	assert.LessOrEqual(t, len(compacted), 2048)
}

func TestCompactResultRetainsUncertainMutationWarning(t *testing.T) {
	content := `{"ok":false,"mutation_attempted":true,"do_not_retry":true,"outcome":"possibly_applied","data":"` + strings.Repeat("x", 10000) + `"}`
	var result map[string]any
	require.NoError(t, json.Unmarshal([]byte(CompactResult(content, 2048)), &result))
	assert.Equal(t, false, result["ok"])
	assert.Equal(t, true, result["mutation_attempted"])
	assert.Equal(t, true, result["do_not_retry"])
	assert.Equal(t, "possibly_applied", result["outcome"])
}
