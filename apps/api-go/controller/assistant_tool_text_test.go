package controller

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/setting"
)

func TestAssistantGiftTextUsesExclusiveLiveBound(t *testing.T) {
	tool := assistantNewUserGiftToolDefinitionWithCap(100)
	amount := tool.Function.Parameters["properties"].(map[string]any)["amount_credits"].(map[string]any)
	if amount["minimum"] != 0 || amount["exclusiveMaximum"] != 100 {
		t.Fatal(amount)
	}
	if _, present := amount["maximum"]; present {
		t.Fatal("inclusive cap remains")
	}
	if !strings.Contains(tool.Function.Description, "< 100") || strings.Contains(tool.Function.Description, "{{") {
		t.Fatal(tool.Function.Description)
	}
	required, _ := json.Marshal(tool.Function.Parameters["required"])
	if string(required) != `["amount_credits"]` {
		t.Fatal(string(required))
	}
}

func TestAssistantConfiguredTextAppliesToNonGiftToolsAndResets(t *testing.T) {
	old := setting.GetAssistantSettings().ToolPolicy
	t.Cleanup(func() { _ = setting.UpdateAssistantToolPolicy(old) })
	policy := `{"version":1,"groups":{},"tools":{},"rules":{"calculate_math":{"min_level":0,"max_level":6,"description":"Bound {{max_reward_credits}}","parameter_descriptions":{"/properties/expression":"Use arithmetic; bound {{max_reward_credits}}"}}}}`
	if err := setting.UpdateAssistantToolPolicy(policy); err != nil {
		t.Fatal(err)
	}
	source := []assistantOpenAIToolDefinition{{Type: "function", Function: assistantOpenAIToolFunction{
		Name: "calculate_math", Description: "Original", Parameters: map[string]any{
			"type": "object", "required": []string{"expression"},
			"properties": map[string]any{"expression": map[string]any{"type": "string", "maxLength": 512}},
		},
	}}}
	before, _ := json.Marshal(source)
	first := assistantConfiguredToolDefinitions(source, 23)
	second := assistantConfiguredToolDefinitions(source, 9)
	if first[0].Function.Description != "Bound 23" || second[0].Function.Description != "Bound 9" {
		t.Fatal("text not refreshed")
	}
	expression := second[0].Function.Parameters["properties"].(map[string]any)["expression"].(map[string]any)
	if expression["description"] != "Use arithmetic; bound 9" || expression["maxLength"] != 512 {
		t.Fatal(expression)
	}
	after, _ := json.Marshal(source)
	if string(before) != string(after) {
		t.Fatal("mutated shared catalogue")
	}
	if err := setting.UpdateAssistantToolPolicy(`{"version":1,"groups":{},"tools":{}}`); err != nil {
		t.Fatal(err)
	}
	reset := assistantConfiguredToolDefinitions(source, 9)
	if reset[0].Function.Description != "Original" {
		t.Fatal("stale override", reset)
	}
}
