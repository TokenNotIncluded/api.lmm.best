package dto

import (
	"bytes"
	"encoding/json"
)

// Only containers retain index entries: two ints each, with no copied JSON,
// scalar nodes or decoded field names. next skips all descendant containers.
type responsesJSONContainer struct{ end, next int }
type responsesJSONValue struct{ start, end, container, next int }

// These span helpers are used only after json.Valid. They do not implement a
// second validity grammar: encoding/json owns syntax and its depth boundary.
func responsesJSONStringEnd(raw []byte, start int) int {
	for i := start + 1; ; i++ {
		if raw[i] == '\\' {
			i++
		} else if raw[i] == '"' {
			return i + 1
		}
	}
}

func responsesJSONStart(raw []byte, start int) int {
	for start < len(raw) {
		switch raw[start] {
		case ' ', '\t', '\r', '\n', ',', ':':
			start++
		default:
			return start
		}
	}
	return start
}

func indexResponsesJSONContainers(raw []byte) []responsesJSONContainer {
	// Two passes allocate exactly the required index. Only array elements and
	// object content fields can be traversed by the protocol collector; opaque
	// fields (tool output/arguments, metadata, URLs) need no descendant entries.
	containers := make([]responsesJSONContainer, scanResponsesJSONContainers(raw, nil))
	scanResponsesJSONContainers(raw, containers)
	return containers
}

func scanResponsesJSONContainers(raw []byte, containers []responsesJSONContainer) int {
	type frame struct {
		index  int
		object bool
	}
	position := responsesJSONStart(raw, 0)
	stack := []frame{{index: 0, object: raw[position] == '{'}}
	next := 1
	position++
	for len(stack) > 0 {
		position = responsesJSONStart(raw, position)
		parent := stack[len(stack)-1]
		if raw[position] == ']' || raw[position] == '}' {
			if containers != nil {
				containers[parent.index] = responsesJSONContainer{end: position + 1, next: next}
			}
			stack = stack[:len(stack)-1]
			position++
			continue
		}
		if parent.object {
			end := responsesJSONStringEnd(raw, position)
			content := bytes.EqualFold(responsesJSONFieldName(raw, position, end), []byte("content"))
			position = responsesJSONStart(raw, end)
			if !content {
				position = responsesJSONValueEnd(raw, position)
				continue
			}
		}
		if raw[position] == '[' || raw[position] == '{' {
			stack = append(stack, frame{index: next, object: raw[position] == '{'})
			next++
			position++
		} else {
			position = responsesJSONValueEnd(raw, position)
		}
	}
	return next
}

func responsesJSONValueEnd(raw []byte, start int) int {
	if raw[start] == '"' {
		return responsesJSONStringEnd(raw, start)
	}
	if raw[start] != '[' && raw[start] != '{' {
		end := start
		for end < len(raw) && !bytes.ContainsRune([]byte(" \t\r\n,]}"), rune(raw[end])) {
			end++
		}
		return end
	}
	depth := 1
	for i := start + 1; ; i++ {
		switch raw[i] {
		case '"':
			i = responsesJSONStringEnd(raw, i) - 1
		case '[', '{':
			depth++
		case ']', '}':
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}
}

func responsesJSONFieldName(raw []byte, start, end int) []byte {
	name := raw[start+1 : end-1]
	if bytes.IndexByte(name, '\\') < 0 {
		return name
	}
	var decoded string
	_ = json.Unmarshal(raw[start:end], &decoded)
	return []byte(decoded)
}

func nextResponsesJSONValue(raw []byte, start, next int, containers []responsesJSONContainer) responsesJSONValue {
	start = responsesJSONStart(raw, start)
	value := responsesJSONValue{start: start, container: -1, next: next}
	switch raw[start] {
	case '[', '{':
		value.container = next
		value.end, value.next = containers[next].end, containers[next].next
	default:
		value.end = responsesJSONValueEnd(raw, start)
	}
	return value
}

// Only typed string fields affect whether the old collector traverses a
// message. Decode them in source order, preserving duplicate-null and any
// type-error behavior. Raw content selects its last occurrence unchanged.
func responsesMessageContent(raw []byte, containers []responsesJSONContainer, value responsesJSONValue) (string, responsesJSONValue, bool) {
	if raw[value.start] != '{' {
		return "", responsesJSONValue{}, false
	}
	itemType, role, ignored := "", "", ""
	content := responsesJSONValue{container: -1}
	position, next := responsesJSONStart(raw, value.start+1), value.container+1
	for raw[position] != '}' {
		nameEnd := responsesJSONStringEnd(raw, position)
		name := responsesJSONFieldName(raw, position, nameEnd)
		fieldStart := responsesJSONStart(raw, nameEnd)
		field := responsesJSONValue{start: fieldStart, container: -1, next: next}
		if bytes.EqualFold(name, []byte("content")) {
			field = nextResponsesJSONValue(raw, fieldStart, next, containers)
		} else {
			field.end = responsesJSONValueEnd(raw, fieldStart)
		}
		var target *string
		switch {
		case bytes.EqualFold(name, []byte("type")):
			target = &itemType
		case bytes.EqualFold(name, []byte("role")):
			target = &role
		case bytes.EqualFold(name, []byte("content")):
			content = field
		case bytes.EqualFold(name, []byte("text")), bytes.EqualFold(name, []byte("file_data")), bytes.EqualFold(name, []byte("detail")):
			target = &ignored
		}
		if target != nil && json.Unmarshal(raw[field.start:field.end], target) != nil {
			return "", responsesJSONValue{}, false
		}
		position, next = responsesJSONStart(raw, field.end), field.next
	}
	return role, content, (itemType == "" || itemType == "message") && role != "" && content.end > 0
}
