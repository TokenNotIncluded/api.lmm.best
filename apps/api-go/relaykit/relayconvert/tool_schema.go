package relayconvert

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
)

const toolSchemaMaxDepth = 64

// SanitizeToolSchemas removes null required members only at JSON Schema nodes.
// Call on an isolated request before conversion, never on raw body passthrough.
func SanitizeToolSchemas(request any) error {
	switch r := request.(type) {
	case *dto.GeneralOpenAIRequest:
		for i := range r.Tools {
			if r.Tools[i].Type != "function" {
				continue
			}
			value, err := sanitizeSchemaValue(r.Tools[i].Function.Parameters)
			if err != nil {
				return err
			}
			r.Tools[i].Function.Parameters = value
		}
		raw, err := sanitizeToolList(r.Functions, "chat-functions")
		if err != nil {
			return err
		}
		r.Functions = raw
	case *dto.ClaudeRequest:
		for i, tool := range r.GetTools() {
			switch t := tool.(type) {
			case *dto.Tool:
				value, err := sanitizeSchemaValue(t.InputSchema)
				if err != nil {
					return err
				}
				t.InputSchema, _ = value.(map[string]any)
			case dto.Tool:
				value, err := sanitizeSchemaValue(t.InputSchema)
				if err != nil {
					return err
				}
				t.InputSchema, _ = value.(map[string]any)
				r.GetTools()[i] = t
			case map[string]any:
				if schema, ok := t["input_schema"]; ok {
					value, err := sanitizeSchemaValue(schema)
					if err != nil {
						return err
					}
					t["input_schema"] = value
				}
			}
		}
	case *dto.OpenAIResponsesRequest:
		raw, err := sanitizeToolList(r.Tools, "responses")
		if err != nil {
			return err
		}
		r.Tools = raw
	case *dto.GeminiChatRequest:
		raw, err := sanitizeToolList(r.Tools, "gemini")
		if err != nil {
			return err
		}
		r.Tools = raw
	}
	return nil
}

func decodeToolJSON(raw []byte) (any, error) {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if !json.Valid(raw) {
		return nil, fmt.Errorf("invalid tool schema JSON")
	}
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return value, nil
}

func sanitizeSchemaValue(value any) (any, error) {
	if value == nil {
		return nil, nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	decoded, err := decodeToolJSON(raw)
	if err != nil {
		return nil, err
	}
	changed, err := sanitizeSchemaNode(decoded, 0)
	if err != nil {
		return nil, err
	}
	if !changed {
		return value, nil
	}
	return decoded, nil
}

func sanitizeToolList(raw json.RawMessage, protocol string) (json.RawMessage, error) {
	return sanitizeToolListAtDepth(raw, protocol, 0)
}

func sanitizeToolListAtDepth(raw json.RawMessage, protocol string, depth int) (json.RawMessage, error) {
	if depth >= toolSchemaMaxDepth {
		return nil, fmt.Errorf("tool namespace exceeds maximum depth %d", toolSchemaMaxDepth)
	}
	if len(raw) == 0 {
		return raw, nil
	}
	value, err := decodeToolJSON(raw)
	if err != nil {
		return nil, err
	}
	tools, _ := value.([]any)
	if protocol == "gemini" {
		if tool, ok := value.(map[string]any); ok {
			tools = []any{tool}
		}
	}
	changed := false
	clean := func(tool map[string]any, key string) error {
		schema, ok := tool[key]
		if !ok {
			return nil
		}
		modified, err := sanitizeSchemaNode(schema, 0)
		changed = changed || modified
		return err
	}
	for _, tool := range tools {
		t, ok := tool.(map[string]any)
		if !ok {
			continue
		}
		switch protocol {
		case "responses":
			if t["type"] == "namespace" {
				nested, exists := t["tools"]
				if exists {
					data, err := json.Marshal(nested)
					if err != nil {
						return nil, err
					}
					cleaned, err := sanitizeToolListAtDepth(data, protocol, depth+1)
					if err != nil {
						return nil, err
					}
					if !bytes.Equal(data, cleaned) {
						decoded, err := decodeToolJSON(cleaned)
						if err != nil {
							return nil, err
						}
						t["tools"] = decoded
						changed = true
					}
				}
			}
			if t["type"] == "function" {
				if err := clean(t, "parameters"); err != nil {
					return nil, err
				}
			}
		case "chat-functions":
			if err := clean(t, "parameters"); err != nil {
				return nil, err
			}
		case "gemini":
			for _, key := range []string{"functionDeclarations", "function_declarations"} {
				declarations, _ := t[key].([]any)
				for _, declaration := range declarations {
					d, ok := declaration.(map[string]any)
					if !ok {
						continue
					}
					for _, field := range []string{"parameters", "parametersJsonSchema", "parameters_json_schema"} {
						if err := clean(d, field); err != nil {
							return nil, err
						}
					}
				}
			}
		}
	}
	if !changed {
		return raw, nil
	}
	return json.Marshal(value)
}

func sanitizeSchemaNode(value any, depth int) (bool, error) {
	node, ok := value.(map[string]any)
	if !ok {
		return false, nil
	}
	if depth >= toolSchemaMaxDepth {
		return false, fmt.Errorf("tool schema exceeds maximum depth %d", toolSchemaMaxDepth)
	}
	changed := false
	if required, exists := node["required"]; exists && required == nil {
		delete(node, "required")
		changed = true
	}
	walk := func(child any) error {
		modified, err := sanitizeSchemaNode(child, depth+1)
		changed = changed || modified
		return err
	}
	for key, child := range node {
		switch key {
		case "additionalItems", "additionalProperties", "contains", "contentSchema", "else", "if", "not", "propertyNames", "then", "unevaluatedItems", "unevaluatedProperties":
			if err := walk(child); err != nil {
				return false, err
			}
		case "allOf", "anyOf", "items", "oneOf", "prefixItems":
			if children, ok := child.([]any); ok {
				for _, entry := range children {
					if err := walk(entry); err != nil {
						return false, err
					}
				}
			} else if key == "items" {
				if err := walk(child); err != nil {
					return false, err
				}
			}
		case "$defs", "definitions", "dependentSchemas", "dependencies", "patternProperties", "properties":
			if children, ok := child.(map[string]any); ok {
				for _, entry := range children {
					if err := walk(entry); err != nil {
						return false, err
					}
				}
			}
		}
	}
	return changed, nil
}
