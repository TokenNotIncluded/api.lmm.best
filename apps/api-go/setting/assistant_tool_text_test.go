package setting

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestAssistantToolTextTemplateValidation(t *testing.T) {
	for _, text := range []string{"", "普通用途即可", "0 <= n < {{max_reward_credits}}", "{{ reward_unit }}; L{{min_level}}-L{{max_level}}"} {
		if err := ValidateAssistantToolDescription(text, 4096); err != nil {
			t.Fatalf("valid text %q: %v", text, err)
		}
	}
	for _, text := range []string{"{{secret}}", "{{max_reward_credits", "bad}}", "{{.Execute}}", "{{max_reward_credits()}}", "bad\x00text", strings.Repeat("界", 1366)} {
		if err := ValidateAssistantToolDescription(text, 4096); err == nil {
			t.Errorf("accepted invalid text %q", text)
		}
	}
	text := "0 <= n < {{ max_reward_credits }} {{reward_unit}}"
	actual := RenderAssistantToolDescription(text, map[string]string{"max_reward_credits": "100", "reward_unit": "UNIT"})
	if actual != "0 <= n < 100 UNIT" {
		t.Fatal(actual)
	}
	if actual := RenderAssistantToolDescription("{{max_reward_credits}}", nil); actual != "unavailable" {
		t.Fatal(actual)
	}
}

func TestAssistantToolTextStrictParameterDecoder(t *testing.T) {
	for _, raw := range []string{
		`null`, `[]`, `{"/properties/n":null}`, `{"/properties/n":1}`,
		`{"/properties/n":"first","/properties/n":"second"}`,
		`{"n":"bad path"}`, `{"/properties/a~2b":"bad escape"}`,
		`{"/properties/n":"{{unknown}}"}`,
	} {
		if _, err := decodeAssistantToolParameterDescriptions(json.NewDecoder(strings.NewReader(raw))); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	fields, err := decodeAssistantToolParameterDescriptions(json.NewDecoder(strings.NewReader(`{"":"root","/properties/a~1b":"{{max_reward_credits}}","/properties/empty":""}`)))
	if err != nil || len(fields) != 3 || fields[""] != "root" {
		t.Fatalf("%v %v", fields, err)
	}
	entries := make([]string, 129)
	for i := range entries {
		entries[i] = `"/properties/` + strings.Repeat("x", i+1) + `":"description"`
	}
	if _, err := decodeAssistantToolParameterDescriptions(json.NewDecoder(strings.NewReader("{" + strings.Join(entries, ",") + "}"))); err == nil {
		t.Fatal("accepted more than 128 overrides")
	}
}

func TestAssistantToolTextNestedDescriptionsDoNotChangeSchema(t *testing.T) {
	schema := map[string]any{
		"type": "object", "required": []string{"amount"},
		"properties": map[string]any{
			"amount": map[string]any{"type": "integer", "minimum": 0, "exclusiveMaximum": 100},
			"rows": map[string]any{"type": "array", "items": map[string]any{
				"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string", "enum": []string{"safe"}}},
			}},
			"a/b": map[string]any{"type": "string"},
		},
		"anyOf":    []any{map[string]any{"properties": map[string]any{"x": map[string]any{"type": "string"}}}},
		"examples": []any{map[string]any{"description": "not a schema node"}},
	}
	before, _ := json.Marshal(schema)
	description := "Reward below {{max_reward_credits}}"
	rule := AssistantToolRule{Description: &description, ParameterDescriptions: map[string]string{
		"": "Root", "/properties/amount": "Less than {{max_reward_credits}}",
		"/properties/rows/items/properties/name": "Name", "/properties/a~1b": "Escaped",
		"/anyOf/0/properties/x": "Alternative", "/examples/0": "must not change example",
		"/removed": "must not add a field", "/properties/amount/minimum": "must not change minimum",
	}}
	text, changed := ApplyAssistantToolText(rule, "Original", schema, map[string]string{"max_reward_credits": "100"})
	if text != "Reward below 100" {
		t.Fatal(text)
	}
	paths := map[string]string{}
	for _, field := range AssistantToolParameterTexts(changed) {
		paths[field.Path] = field.Description
	}
	for path, expected := range map[string]string{"": "Root", "/properties/amount": "Less than 100", "/properties/rows/items/properties/name": "Name", "/properties/a~1b": "Escaped", "/anyOf/0/properties/x": "Alternative"} {
		if paths[path] != expected {
			t.Errorf("%s: got %q want %q", path, paths[path], expected)
		}
	}
	properties := changed["properties"].(map[string]any)
	amount := properties["amount"].(map[string]any)
	if amount["minimum"] != 0 || amount["exclusiveMaximum"] != 100 || amount["type"] != "integer" {
		t.Fatal(amount)
	}
	if !reflect.DeepEqual(changed["required"], schema["required"]) || !reflect.DeepEqual(changed["examples"], schema["examples"]) {
		t.Fatal("changed a constraint or example")
	}
	after, _ := json.Marshal(schema)
	if string(after) != string(before) {
		t.Fatal("mutated cached schema")
	}
	again, original := ApplyAssistantToolText(AssistantToolRule{}, "Original", schema, nil)
	if again != "Original" || !reflect.DeepEqual(original, schema) {
		t.Fatal("reset did not restore defaults")
	}
	updated, _ := ApplyAssistantToolText(rule, "Original", schema, map[string]string{"max_reward_credits": "17"})
	if updated != "Reward below 17" {
		t.Fatal("stale cap", updated)
	}
}
