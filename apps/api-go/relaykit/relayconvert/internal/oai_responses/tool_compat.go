package oairesponses

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/relayconvert/convmeta"
)

// NormalizeResponsesTools adapts namespaces and tool discovery to upstream
// protocols that only support flat functions. Server discovery eagerly loads
// the supplied definitions; client discovery remains a client-executed call.
// Native Responses forwarding must bypass this conversion. The request and its
// raw JSON fields are never mutated, so channel retries see the original input.
func NormalizeResponsesTools(req *dto.OpenAIResponsesRequest) (*dto.OpenAIResponsesRequest, convmeta.ResponsesToolMap, error) {
	if req == nil {
		return nil, nil, fmt.Errorf("request is nil")
	}
	var tools, input []map[string]any
	if rawJSONPresent(req.Tools) {
		if err := decodeToolJSON(req.Tools, &tools); err != nil {
			return nil, nil, fmt.Errorf("invalid Responses tools: %w", err)
		}
	}
	if raw := bytes.TrimSpace(req.Input); len(raw) > 0 && raw[0] == '[' {
		if err := decodeToolJSON(raw, &input); err != nil {
			return nil, nil, fmt.Errorf("invalid Responses input: %w", err)
		}
	}
	var choice map[string]any
	if raw := bytes.TrimSpace(req.ToolChoice); len(raw) > 0 && raw[0] == '{' {
		if err := decodeToolJSON(raw, &choice); err != nil {
			return nil, nil, fmt.Errorf("invalid Responses tool_choice: %w", err)
		}
	}
	special := toolString(choice, "namespace") != "" || toolString(choice, "type") == "tool_search" || toolString(choice, "type") == "allowed_tools"
	for _, tool := range tools {
		_, deferred := tool["defer_loading"]
		special = special || deferred || toolString(tool, "type") == "namespace" || toolString(tool, "type") == "tool_search"
	}
	for _, item := range input {
		kind := toolString(item, "type")
		special = special || toolString(item, "namespace") != "" || kind == "tool_search_call" || kind == "tool_search_output"
	}
	if !special {
		return req, nil, nil
	}
	if toolString(choice, "type") == "allowed_tools" {
		return nil, nil, fmt.Errorf("tool_choice allowed_tools cannot be converted to this upstream protocol; use auto, required, none, or a specific function")
	}

	n := &responsesToolsNormalizer{
		reserved: make(map[string]bool), aliases: make(map[convmeta.ResponsesToolIdentity]string),
		mapping: make(convmeta.ResponsesToolMap), definitions: make(map[string]map[string]any),
		tools: make([]map[string]any, 0),
	}
	// Reserve all original top-level names before generating aliases. Definitions
	// discovered in history participate, preventing collisions across turns.
	batches := [][]map[string]any{tools}
	loaded := make(map[int][]map[string]any)
	for i, item := range input {
		if toolString(item, "type") == "tool_search_output" {
			batch, err := toolObjectArray(item["tools"])
			if err != nil {
				return nil, nil, fmt.Errorf("input[%d].tools: %w", i, err)
			}
			batches = append(batches, batch)
			loaded[i] = batch
		}
		if toolString(item, "namespace") == "" {
			if name := toolString(item, "name"); name != "" {
				n.reserved[name] = true
			}
		}
	}
	for _, batch := range batches {
		for _, tool := range batch {
			if toolString(tool, "type") != "namespace" && toolString(tool, "type") != "tool_search" {
				if name := toolString(tool, "name"); name != "" {
					n.reserved[name] = true
				}
			}
		}
	}
	if _, err := n.addTools(tools); err != nil {
		return nil, nil, err
	}
	// Process discovered definitions in input order, never map iteration order.
	for i := range input {
		if batch, ok := loaded[i]; ok {
			flat, err := n.addTools(batch)
			if err != nil {
				return nil, nil, fmt.Errorf("input[%d].tools: %w", i, err)
			}
			loaded[i] = flat
		}
	}
	if n.serverSearch {
		hasFunction := false
		for _, tool := range n.tools {
			hasFunction = hasFunction || toolString(tool, "type") == "function"
		}
		if !hasFunction {
			return nil, nil, fmt.Errorf("server tool_search requires supplied function definitions for this upstream protocol; provide a tool catalog or use client-executed search")
		}
	}
	convertedInput := make([]map[string]any, 0, len(input))
	for i, item := range input {
		kind := toolString(item, "type")
		switch kind {
		case "function_call":
			if ns := toolString(item, "namespace"); ns != "" {
				name := toolString(item, "name")
				if name == "" {
					return nil, nil, fmt.Errorf("input[%d]: namespaced function call requires a name", i)
				}
				item["name"] = n.functionName(ns, name)
				delete(item, "namespace")
			}
		case "custom_tool_call":
			if toolString(item, "namespace") != "" {
				return nil, nil, fmt.Errorf("input[%d]: namespaced custom tools cannot be converted to flat functions", i)
			}
		case "tool_search_call", "tool_search_output":
			client, err := clientToolSearch(item)
			if err != nil {
				return nil, nil, fmt.Errorf("input[%d]: %w", i, err)
			}
			if !client {
				// Hosted search has no client tool-call ID. The tools it loaded
				// are already merged above, so omit these internal trace items.
				continue
			}
			callID := toolString(item, "call_id")
			if callID == "" {
				return nil, nil, fmt.Errorf("input[%d]: client tool search requires call_id", i)
			}
			if kind == "tool_search_call" {
				arguments, err := toolSearchArguments(item["arguments"])
				if err != nil {
					return nil, nil, fmt.Errorf("input[%d]: %w", i, err)
				}
				item = map[string]any{"type": "function_call", "name": n.searchName(), "call_id": callID, "arguments": arguments}
			} else {
				output, err := json.Marshal(map[string]any{"tools": loaded[i]})
				if err != nil {
					return nil, nil, err
				}
				item = map[string]any{"type": "function_call_output", "call_id": callID, "output": string(output)}
			}
		}
		convertedInput = append(convertedInput, item)
	}
	if choice != nil {
		switch toolString(choice, "type") {
		case "function":
			if ns := toolString(choice, "namespace"); ns != "" {
				name := n.functionName(ns, toolString(choice, "name"))
				if _, ok := n.definitions[name]; !ok {
					return nil, nil, fmt.Errorf("tool_choice references an unavailable namespaced function")
				}
				choice["name"] = name
				delete(choice, "namespace")
			}
		case "tool_search":
			if !n.clientSearch {
				return nil, nil, fmt.Errorf("forced server tool_search cannot be emulated by this upstream protocol; use auto or client-executed tool search")
			}
			choice = map[string]any{"type": "function", "name": n.searchName()}
		}
	}
	out := *req
	var err error
	out.Tools, err = json.Marshal(n.tools)
	if err != nil {
		return nil, nil, err
	}
	if input != nil {
		out.Input, err = json.Marshal(convertedInput)
		if err != nil {
			return nil, nil, err
		}
	}
	if choice != nil {
		out.ToolChoice, err = json.Marshal(choice)
		if err != nil {
			return nil, nil, err
		}
	}
	if len(n.mapping) == 0 {
		return &out, nil, nil
	}
	return &out, n.mapping, nil
}

type responsesToolsNormalizer struct {
	reserved     map[string]bool
	aliases      map[convmeta.ResponsesToolIdentity]string
	mapping      convmeta.ResponsesToolMap
	definitions  map[string]map[string]any
	tools        []map[string]any
	clientSearch bool
	serverSearch bool
}

func (n *responsesToolsNormalizer) addTools(tools []map[string]any) ([]map[string]any, error) {
	flat := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		switch toolString(tool, "type") {
		case "namespace":
			ns := toolString(tool, "name")
			if ns == "" {
				return nil, fmt.Errorf("namespace requires a name")
			}
			children, err := toolObjectArray(tool["tools"])
			if err != nil {
				return nil, fmt.Errorf("namespace %q: %w", ns, err)
			}
			for _, child := range children {
				if toolString(child, "type") != "function" {
					return nil, fmt.Errorf("namespace %q contains unsupported tool type %q; only functions can be converted", ns, toolString(child, "type"))
				}
				name := toolString(child, "name")
				if name == "" {
					return nil, fmt.Errorf("namespace %q contains a function without a name", ns)
				}
				child["name"] = n.alias(convmeta.ResponsesToolIdentity{Name: name, Namespace: ns})
				// Hash aliases carry no semantic hint for the model. Always keep
				// the qualified original name alongside both descriptions.
				child["description"] = strings.TrimSpace(ns + "." + name + "\n" + toolString(tool, "description") + "\n" + toolString(child, "description"))
				delete(child, "defer_loading")
				if err := n.addDefinition(child); err != nil {
					return nil, err
				}
				flat = append(flat, child)
			}
		case "tool_search":
			client, err := clientToolSearch(tool)
			if err != nil {
				return nil, err
			}
			if !client {
				n.serverSearch = true
				if n.clientSearch {
					return nil, fmt.Errorf("cannot combine client and server tool_search definitions")
				}
				continue
			}
			if n.serverSearch {
				return nil, fmt.Errorf("cannot combine client and server tool_search definitions")
			}
			n.clientSearch = true
			parameters, exists := tool["parameters"]
			if !exists {
				parameters = map[string]any{"type": "object", "properties": map[string]any{"goal": map[string]any{"type": "string"}}, "required": []any{"goal"}, "additionalProperties": false}
			}
			if _, ok := parameters.(map[string]any); !ok {
				return nil, fmt.Errorf("client tool_search parameters must be a JSON schema object")
			}
			function := map[string]any{"type": "function", "name": n.searchName(), "parameters": parameters}
			if description, ok := tool["description"]; ok {
				function["description"] = description
			} else {
				function["description"] = "Search for and load tools needed to complete the task."
			}
			if strict, ok := tool["strict"]; ok {
				function["strict"] = strict
			}
			if err := n.addDefinition(function); err != nil {
				return nil, err
			}
			flat = append(flat, function)
		default:
			delete(tool, "defer_loading")
			if err := n.addDefinition(tool); err != nil {
				return nil, err
			}
			flat = append(flat, tool)
		}
	}
	return flat, nil
}

func (n *responsesToolsNormalizer) addDefinition(tool map[string]any) error {
	name := toolString(tool, "name")
	if name != "" {
		if previous, ok := n.definitions[name]; ok {
			if !reflect.DeepEqual(previous, tool) {
				return fmt.Errorf("conflicting tool definitions for %q", name)
			}
			return nil
		}
		n.definitions[name] = tool
	}
	n.tools = append(n.tools, tool)
	return nil
}

func (n *responsesToolsNormalizer) functionName(namespace, name string) string {
	identity := convmeta.ResponsesToolIdentity{Name: name, Namespace: namespace}
	if alias, ok := n.aliases[identity]; ok {
		return alias
	}
	// Discovered standalone functions can be echoed with namespace == name.
	if namespace == name {
		if tool, ok := n.definitions[name]; ok && toolString(tool, "type") == "function" {
			n.mapping[name] = identity
			n.aliases[identity] = name
			return name
		}
	}
	return n.alias(identity)
}

func (n *responsesToolsNormalizer) searchName() string {
	return n.alias(convmeta.ResponsesToolIdentity{Name: "tool_search", ToolSearch: true})
}

func (n *responsesToolsNormalizer) alias(identity convmeta.ResponsesToolIdentity) string {
	if alias, ok := n.aliases[identity]; ok {
		return alias
	}
	key, _ := json.Marshal(identity)
	for attempt := 0; ; attempt++ {
		digest := sha256.Sum256(append(key, []byte(fmt.Sprintf("/%d", attempt))...))
		name := fmt.Sprintf("lmm_tool_%x", digest[:24])
		if n.reserved[name] {
			continue
		}
		if other, exists := n.mapping[name]; exists && other != identity {
			continue
		}
		n.mapping[name] = identity
		n.aliases[identity] = name
		return name
	}
}

func clientToolSearch(tool map[string]any) (bool, error) {
	switch execution := toolString(tool, "execution"); execution {
	case "client":
		return true, nil
	case "server":
		return false, nil
	case "":
		return toolString(tool, "call_id") != "", nil
	default:
		return false, fmt.Errorf("unsupported tool_search execution %q", execution)
	}
}

func toolSearchArguments(arguments any) (string, error) {
	if raw, ok := arguments.(string); ok {
		var object map[string]any
		if err := decodeToolJSON([]byte(raw), &object); err != nil || object == nil {
			return "", fmt.Errorf("client tool_search arguments must encode a JSON object")
		}
		return raw, nil
	}
	if _, ok := arguments.(map[string]any); !ok {
		return "", fmt.Errorf("client tool_search arguments must be a JSON object")
	}
	encoded, err := json.Marshal(arguments)
	return string(encoded), err
}

func toolObjectArray(value any) ([]map[string]any, error) {
	array, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("tools must be an array of objects")
	}
	objects := make([]map[string]any, 0, len(array))
	for _, entry := range array {
		object, ok := entry.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("tools must contain objects")
		}
		objects = append(objects, object)
	}
	return objects, nil
}

func toolString(object map[string]any, key string) string {
	value, _ := object[key].(string)
	return value
}

func decodeToolJSON(raw []byte, target any) error {
	if !json.Valid(raw) {
		return fmt.Errorf("invalid JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	return decoder.Decode(target)
}
