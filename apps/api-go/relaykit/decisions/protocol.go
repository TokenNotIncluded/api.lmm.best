// Package decisions implements the native OpenAI Decisions wire contract.
// It does not translate requests or responses into a chat protocol.
package decisions

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"unicode/utf8"
)

const Endpoint = "/v1/decisions"

// Request retains the source fields, including fields added by the provider.
// Model changes are applied to a fresh map, not to the caller's request.
type Request struct {
	Model     string
	Input     json.RawMessage
	Questions []Question
	fields    map[string]json.RawMessage
}

type Question struct {
	Type         string   `json:"type"`
	Instructions string   `json:"instructions"`
	Name         *string  `json:"name,omitempty"`
	Choices      []Choice `json:"choices,omitempty"`
	Levels       []Level  `json:"levels,omitempty"`
}

type Choice struct {
	Value       json.RawMessage `json:"value"`
	Description *string         `json:"description,omitempty"`
}

type Level struct {
	Label       string  `json:"label"`
	Description *string `json:"description,omitempty"`
}

// object rejects duplicate keys and case aliases of known routing fields.
func object(raw []byte) (map[string]json.RawMessage, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	t, err := d.Token()
	if err != nil || t != json.Delim('{') {
		return nil, errors.New("expected a JSON object")
	}
	out := make(map[string]json.RawMessage)
	for d.More() {
		t, err = d.Token()
		if err != nil {
			return nil, err
		}
		key, ok := t.(string)
		if !ok {
			return nil, errors.New("invalid object key")
		}
		if _, exists := out[key]; exists {
			return nil, fmt.Errorf("duplicate field %q", key)
		}
		var value json.RawMessage
		if err := d.Decode(&value); err != nil {
			return nil, err
		}
		out[key] = value
	}
	if _, err := d.Token(); err != nil {
		return nil, err
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, errors.New("unexpected trailing JSON value")
	}
	return out, nil
}

func text(raw json.RawMessage) (string, error) {
	var value string
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &value) != nil {
		return "", errors.New("expected a string")
	}
	return value, nil
}

func (r *Request) UnmarshalJSON(raw []byte) error {
	fields, err := object(raw)
	if err != nil {
		return err
	}
	for key := range fields {
		for _, name := range []string{"model", "input", "questions", "safety_identifier", "group"} {
			if key != name && strings.EqualFold(key, name) {
				return fmt.Errorf("field %q must use exact lowercase spelling", name)
			}
		}
	}
	model, err := text(fields["model"])
	if err != nil || strings.TrimSpace(model) == "" {
		return errors.New("model must be a nonempty string")
	}
	var rawQuestions []json.RawMessage
	if err := json.Unmarshal(fields["questions"], &rawQuestions); err != nil || len(rawQuestions) == 0 {
		return errors.New("questions must be a nonempty ordered array")
	}
	questions := make([]Question, len(rawQuestions))
	for i, rawQuestion := range rawQuestions {
		q, err := parseQuestion(rawQuestion)
		if err != nil {
			return fmt.Errorf("questions[%d]: %w", i, err)
		}
		questions[i] = q
	}
	if err := validateInput(fields["input"]); err != nil {
		return err
	}
	for _, key := range []string{"stream", "messages", "tools", "tool_choice", "max_tokens", "max_output_tokens", "max_completion_tokens", "service_tier", "background"} {
		if _, exists := fields[key]; exists {
			return fmt.Errorf("%s is not part of the Decisions request contract", key)
		}
	}
	if raw := fields["safety_identifier"]; len(raw) > 0 && string(raw) != "null" {
		value, err := text(raw)
		if err != nil || utf8.RuneCountInString(value) > 128 {
			return errors.New("safety_identifier must be a string of at most 128 characters, or null")
		}
	}
	*r = Request{Model: model, Input: fields["input"], Questions: questions, fields: fields}
	return nil
}

func parseQuestion(raw []byte) (Question, error) {
	fields, err := object(raw)
	if err != nil {
		return Question{}, err
	}
	kind, err := text(fields["type"])
	if err != nil {
		return Question{}, errors.New("type is required")
	}
	instructions, err := text(fields["instructions"])
	if err != nil {
		return Question{}, errors.New("instructions must be a string")
	}
	q := Question{Type: kind, Instructions: instructions}
	if name, ok := fields["name"]; ok {
		value, err := text(name)
		if err != nil {
			return q, errors.New("name must be a string when present")
		}
		q.Name = &value
	}
	switch kind {
	case "predicate":
	case "choice":
		if err := json.Unmarshal(fields["choices"], &q.Choices); err != nil || len(q.Choices) < 2 || len(q.Choices) > 255 {
			return q, errors.New("choices must contain 2 to 255 items")
		}
		seen := make(map[string]bool, len(q.Choices))
		for _, choice := range q.Choices {
			key, err := choiceKey(choice.Value)
			if err != nil || seen[key] {
				return q, errors.New("choice values must be unique strings or booleans")
			}
			seen[key] = true
		}
	case "score":
		var levels []json.RawMessage
		if err := json.Unmarshal(fields["levels"], &levels); err != nil || len(levels) == 0 {
			return q, errors.New("levels must be a nonempty array")
		}
		for _, rawLevel := range levels {
			levelFields, err := object(rawLevel)
			if err != nil {
				return q, err
			}
			label, err := text(levelFields["label"])
			if err != nil {
				return q, errors.New("each level must have a string label")
			}
			q.Levels = append(q.Levels, Level{Label: label})
		}
	default:
		return q, errors.New("type must be predicate, choice, or score")
	}
	return q, nil
}

func choiceKey(raw []byte) (string, error) {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", err
	}
	switch value := value.(type) {
	case string:
		return "string:" + value, nil
	case bool:
		return fmt.Sprintf("bool:%t", value), nil
	default:
		return "", errors.New("choice must be a string or boolean")
	}
}

func validateInput(raw json.RawMessage) error {
	if _, err := text(raw); err == nil {
		return nil
	}
	var messages []json.RawMessage
	if err := json.Unmarshal(raw, &messages); err != nil || len(messages) == 0 {
		return errors.New("input must be a string or a nonempty array of user messages")
	}
	images := 0
	for _, rawMessage := range messages {
		message, err := object(rawMessage)
		if err != nil {
			return err
		}
		role, _ := text(message["role"])
		if role != "user" {
			return errors.New("Decisions only accepts user messages")
		}
		if rawType, ok := message["type"]; ok {
			kind, _ := text(rawType)
			if kind != "message" {
				return errors.New("input item type must be message")
			}
		}
		if _, err := text(message["content"]); err == nil {
			continue
		}
		var content []json.RawMessage
		if err := json.Unmarshal(message["content"], &content); err != nil || len(content) == 0 {
			return errors.New("message content must be text or a nonempty array")
		}
		for _, rawPart := range content {
			part, err := object(rawPart)
			if err != nil {
				return err
			}
			kind, _ := text(part["type"])
			switch kind {
			case "input_text":
				if _, err := text(part["text"]); err != nil {
					return errors.New("input_text requires text")
				}
			case "input_image":
				value, err := text(part["image_url"])
				if err != nil || value == "" {
					return errors.New("input_image requires image_url")
				}
				images++
				if images > 128 {
					return errors.New("input contains more than 128 images")
				}
			default:
				return errors.New("Decisions content only supports input_text and input_image")
			}
		}
	}
	return nil
}

func (r Request) MarshalJSON() ([]byte, error) {
	if r.fields == nil {
		return nil, errors.New("Decisions request must first be decoded and validated")
	}
	fields := make(map[string]json.RawMessage, len(r.fields))
	for key, value := range r.fields {
		// group is a gateway routing field, not an upstream API field.
		if key != "group" {
			fields[key] = value
		}
	}
	model, err := json.Marshal(r.Model)
	if err != nil {
		return nil, err
	}
	fields["model"] = model
	return json.Marshal(fields)
}

// InputUsage validates the native envelope without rebuilding its answer data.
// The caller returns the original response bytes, not the decoded structures.
func InputUsage(raw []byte, questions []Question) (int, error) {
	fields, err := object(raw)
	if err != nil {
		return 0, err
	}
	if model, err := text(fields["model"]); err != nil || model == "" {
		return 0, errors.New("upstream response is missing model")
	}
	var answers []json.RawMessage
	if err := json.Unmarshal(fields["answers"], &answers); err != nil || len(answers) != len(questions) {
		return 0, errors.New("upstream answer count differs from question count")
	}
	for i, rawAnswer := range answers {
		answer, err := object(rawAnswer)
		if err != nil {
			return 0, err
		}
		kind, _ := text(answer["type"])
		if kind == "refusal" {
			continue
		}
		if kind != questions[i].Type {
			return 0, errors.New("upstream answer type differs from question type")
		}
		switch kind {
		case "predicate":
			if !finiteNumber(answer["probability"], 0, 1) {
				return 0, errors.New("invalid predicate probability")
			}
		case "choice":
			key, err := choiceKey(answer["choice"])
			matched := false
			for _, choice := range questions[i].Choices {
				candidate, _ := choiceKey(choice.Value)
				matched = matched || key == candidate
			}
			if err != nil || !matched || !finiteNumber(answer["confidence"], 0, 1) {
				return 0, errors.New("invalid choice answer")
			}
		case "score":
			if !finiteNumber(answer["score"], 0, float64(len(questions[i].Levels)-1)) || !finiteNumber(answer["confidence"], 0, 1) {
				return 0, errors.New("invalid score answer")
			}
		}
	}
	usage, err := object(fields["usage"])
	if err != nil {
		return 0, errors.New("upstream response is missing valid usage")
	}
	// Missing, null, negative, fractional and overflowing usage must not be
	// interpreted as a free request. An explicit integer zero is valid.
	value := bytes.TrimSpace(usage["input_tokens"])
	var count int
	if len(value) == 0 || bytes.Equal(value, []byte("null")) || json.Unmarshal(value, &count) != nil || count < 0 || count > math.MaxInt32 {
		return 0, errors.New("upstream input_tokens must be a nonnegative 32-bit integer")
	}
	return count, nil
}

func finiteNumber(raw []byte, low, high float64) bool {
	var value float64
	return len(raw) > 0 && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) && json.Unmarshal(raw, &value) == nil && !math.IsNaN(value) && !math.IsInf(value, 0) && value >= low && value <= high
}

// QuestionText decodes JSON escapes before the existing text checks run.
func (r Request) QuestionText() string {
	var value any
	if json.Unmarshal(r.fields["questions"], &value) != nil {
		return ""
	}
	var parts []string
	var walk func(any)
	walk = func(value any) {
		switch v := value.(type) {
		case string:
			parts = append(parts, v)
		case []any:
			for _, item := range v {
				walk(item)
			}
		case map[string]any:
			keys := make([]string, 0, len(v))
			for key := range v {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				walk(v[key])
			}
		}
	}
	walk(value)
	return strings.Join(parts, "\n")
}
