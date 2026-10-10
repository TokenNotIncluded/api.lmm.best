// Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later
package setting

import (
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	AssistantToolDescriptionMaxBytes          = 4096
	AssistantToolParameterDescriptionMaxBytes = 2048
	AssistantToolParameterDescriptionsMax     = 128
)

var assistantToolTextVariable = regexp.MustCompile(`\{\{\s*([a-z_]+)\s*\}\}`)
var assistantToolTextVariables = map[string]bool{
	"max_reward_credits": true,
	"reward_unit":        true,
	"min_level":          true,
	"max_level":          true,
}

// Templates are plain text with a small, fixed set of substitutions. They
// cannot execute code, read account data, or change a tool's permissions.
func ValidateAssistantToolDescription(text string, maximum int) error {
	if !utf8.ValidString(text) || len(text) > maximum || strings.ContainsRune(text, 0) {
		return errors.New("invalid assistant tool description")
	}
	for _, match := range assistantToolTextVariable.FindAllStringSubmatch(text, -1) {
		if !assistantToolTextVariables[match[1]] {
			return errors.New("unknown assistant tool description variable")
		}
	}
	remainder := assistantToolTextVariable.ReplaceAllString(text, "")
	if strings.Contains(remainder, "{{") || strings.Contains(remainder, "}}") {
		return errors.New("invalid assistant tool description variable")
	}
	return nil
}

func RenderAssistantToolDescription(text string, variables map[string]string) string {
	return assistantToolTextVariable.ReplaceAllStringFunc(text, func(match string) string {
		name := assistantToolTextVariable.FindStringSubmatch(match)[1]
		if value, ok := variables[name]; ok && assistantToolTextVariables[name] {
			return value
		}
		return "unavailable"
	})
}

func assistantToolParameterPathValid(path string) bool {
	if len(path) > 512 || !utf8.ValidString(path) || strings.ContainsRune(path, 0) {
		return false
	}
	if path == "" {
		return true // The parameters object itself has a description too.
	}
	if !strings.HasPrefix(path, "/") {
		return false
	}
	for i := 0; i < len(path); i++ {
		if path[i] == '~' {
			if i+1 >= len(path) || (path[i+1] != '0' && path[i+1] != '1') {
				return false
			}
			i++
		}
	}
	return true
}

func decodeAssistantToolParameterDescriptions(decoder *json.Decoder) (map[string]string, error) {
	invalid := errors.New("invalid assistant tool parameter descriptions")
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return nil, invalid
	}
	result := make(map[string]string)
	for decoder.More() {
		token, err := decoder.Token()
		path, ok := token.(string)
		if err != nil || !ok || !assistantToolParameterPathValid(path) || len(result) >= AssistantToolParameterDescriptionsMax {
			return nil, invalid
		}
		if _, duplicate := result[path]; duplicate {
			return nil, invalid
		}
		var value *string
		if err := decoder.Decode(&value); err != nil || value == nil {
			return nil, invalid
		}
		if err := ValidateAssistantToolDescription(*value, AssistantToolParameterDescriptionMaxBytes); err != nil {
			return nil, err
		}
		result[path] = *value
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return nil, invalid
	}
	return result, nil
}

type AssistantToolParameterText struct {
	Path        string `json:"path"`
	Description string `json:"description"`
}

func assistantToolPointerSegment(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "~", "~0"), "/", "~1")
}

// Walk only schema positions, never arbitrary data in defaults or examples.
// All object fields, array items and alternative schemas can have descriptions,
// including fields whose original description is empty.
func walkAssistantToolSchema(schema map[string]any, path string, visit func(string, map[string]any)) {
	if schema == nil {
		return
	}
	visit(path, schema)
	for _, key := range []string{"properties", "patternProperties", "$defs", "definitions", "dependentSchemas"} {
		children, _ := schema[key].(map[string]any)
		keys := make([]string, 0, len(children))
		for name := range children {
			keys = append(keys, name)
		}
		slices.Sort(keys)
		for _, name := range keys {
			if child, ok := children[name].(map[string]any); ok {
				walkAssistantToolSchema(child, path+"/"+key+"/"+assistantToolPointerSegment(name), visit)
			}
		}
	}
	for _, key := range []string{"items", "additionalProperties", "contains", "not", "if", "then", "else", "propertyNames", "unevaluatedProperties", "unevaluatedItems"} {
		if child, ok := schema[key].(map[string]any); ok {
			walkAssistantToolSchema(child, path+"/"+key, visit)
		}
	}
	for _, key := range []string{"oneOf", "anyOf", "allOf", "prefixItems", "items"} {
		switch children := schema[key].(type) {
		case []any:
			for i, item := range children {
				if child, ok := item.(map[string]any); ok {
					walkAssistantToolSchema(child, path+"/"+key+"/"+strconv.Itoa(i), visit)
				}
			}
		case []map[string]any:
			for i, child := range children {
				walkAssistantToolSchema(child, path+"/"+key+"/"+strconv.Itoa(i), visit)
			}
		}
	}
}

func AssistantToolParameterTexts(schema map[string]any) []AssistantToolParameterText {
	fields := make([]AssistantToolParameterText, 0)
	walkAssistantToolSchema(schema, "", func(path string, node map[string]any) {
		description, _ := node["description"].(string)
		fields = append(fields, AssistantToolParameterText{Path: path, Description: description})
	})
	return fields
}

func cloneAssistantToolSchema(value any) any {
	switch value := value.(type) {
	case map[string]any:
		copy := make(map[string]any, len(value))
		for key, child := range value {
			copy[key] = cloneAssistantToolSchema(child)
		}
		return copy
	case []any:
		copy := make([]any, len(value))
		for i, child := range value {
			copy[i] = cloneAssistantToolSchema(child)
		}
		return copy
	case []map[string]any:
		copy := make([]map[string]any, len(value))
		for i, child := range value {
			copy[i] = cloneAssistantToolSchema(child).(map[string]any)
		}
		return copy
	case []string:
		return append([]string(nil), value...)
	default:
		return value
	}
}

// This function changes descriptions only. Types, required fields, enums and
// amount limits remain code-owned. The cached source schema is never mutated.
// Removed field paths have no effect; the editor exposes them for cleanup.
func ApplyAssistantToolText(rule AssistantToolRule, description string, parameters map[string]any, variables map[string]string) (string, map[string]any) {
	if rule.Description != nil {
		description = *rule.Description
	}
	description = RenderAssistantToolDescription(description, variables)
	if len(rule.ParameterDescriptions) == 0 || parameters == nil {
		return description, parameters
	}
	copy := cloneAssistantToolSchema(parameters).(map[string]any)
	walkAssistantToolSchema(copy, "", func(path string, node map[string]any) {
		if text, configured := rule.ParameterDescriptions[path]; configured {
			node["description"] = RenderAssistantToolDescription(text, variables)
		}
	})
	return description, copy
}
