package dto

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
)

// Ported from QuantumNous/new-api-plugins TypeSafe 1.0.0, commit
// 97a16e9a8d98b73ffb76e1c5a00b4d0f855f4d0a. Jev uses a proprietary tokenizer:
// reserve its full input context, then replace that reservation with reported usage.
const SystemOneMaxInputTokens = 65536

type SystemOneRequest struct {
	Model     string                       `json:"model"`
	State     json.RawMessage              `json:"state"`
	Questions map[string]SystemOneQuestion `json:"questions"`
	Stream    json.RawMessage              `json:"stream,omitempty"`
}

type SystemOneQuestion struct {
	Type         string                     `json:"type"`
	Instructions json.RawMessage            `json:"instructions,omitempty"`
	Criteria     json.RawMessage            `json:"criteria,omitempty"`
	Extra        map[string]json.RawMessage `json:"-"`
}

// Exact keys and duplicate rejection keep routing/authorization and the payload
// sent upstream consistent. encoding/json's default struct binding accepts Model.
func systemOneObject(raw []byte) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, errors.New("value must be a JSON object")
	}
	fields := make(map[string]json.RawMessage)
	for decoder.More() {
		token, err = decoder.Token()
		if err != nil {
			return nil, err
		}
		key, ok := token.(string)
		if !ok {
			return nil, errors.New("invalid JSON object key")
		}
		if _, exists := fields[key]; exists {
			return nil, fmt.Errorf("duplicate JSON field %q", key)
		}
		var value json.RawMessage
		if err = decoder.Decode(&value); err != nil {
			return nil, err
		}
		fields[key] = value
	}
	if _, err = decoder.Token(); err != nil {
		return nil, err
	}
	if _, err = decoder.Token(); err != io.EOF {
		return nil, errors.New("unexpected trailing JSON value")
	}
	return fields, nil
}

func (r *SystemOneRequest) UnmarshalJSON(raw []byte) error {
	fields, err := systemOneObject(raw)
	if err != nil {
		return err
	}
	*r = SystemOneRequest{State: fields["state"], Stream: fields["stream"]}
	if err = json.Unmarshal(fields["model"], &r.Model); err != nil {
		return errors.New("model must be a string")
	}
	if err = json.Unmarshal(fields["questions"], &r.Questions); err != nil {
		return errors.New("questions must be a nonempty object")
	}
	return nil
}

func (q *SystemOneQuestion) UnmarshalJSON(raw []byte) error {
	fields, err := systemOneObject(raw)
	if err != nil {
		return errors.New("each question must be a JSON object")
	}
	*q = SystemOneQuestion{Instructions: fields["instructions"], Criteria: fields["criteria"], Extra: fields}
	if err = json.Unmarshal(fields["type"], &q.Type); err != nil {
		return errors.New("each question must have type choice, score, or noul")
	}
	return nil
}

func (q SystemOneQuestion) MarshalJSON() ([]byte, error) {
	fields := make(map[string]json.RawMessage, len(q.Extra)+3)
	for key, value := range q.Extra {
		fields[key] = value
	}
	fields["type"], _ = json.Marshal(q.Type)
	if len(q.Instructions) > 0 {
		fields["instructions"] = q.Instructions
	}
	if len(q.Criteria) > 0 {
		fields["criteria"] = q.Criteria
	}
	return json.Marshal(fields)
}

func isSystemOneEntry(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	if !json.Valid(raw) || len(raw) == 0 {
		return false
	}
	return bytes.Equal(raw, []byte("null")) || raw[0] == '"' || raw[0] == '{' || raw[0] == '['
}

func (r *SystemOneRequest) Validate() error {
	if r == nil || strings.TrimSpace(r.Model) == "" {
		return errors.New("model is required")
	}
	if !isSystemOneEntry(r.State) {
		return errors.New("state must be text, an object, an array, or null")
	}
	if len(r.Questions) == 0 {
		return errors.New("questions must be a nonempty object")
	}
	if len(r.Stream) > 0 && !bytes.Equal(bytes.TrimSpace(r.Stream), []byte("false")) {
		return errors.New("TypeSafe System One does not support streaming")
	}
	for _, question := range r.Questions {
		if len(question.Instructions) > 0 && !isSystemOneEntry(question.Instructions) {
			return errors.New("question instructions must be text, an object, an array, or null")
		}
		criteria := bytes.TrimSpace(question.Criteria)
		var descriptions []json.RawMessage
		switch question.Type {
		case "choice", "noul":
			if question.Type == "noul" && (len(criteria) == 0 || bytes.Equal(criteria, []byte("null"))) {
				continue
			}
			options, err := systemOneObject(criteria)
			if question.Type == "choice" && (err != nil || len(options) < 1 || len(options) > 255) {
				return errors.New("choice criteria must contain between 1 and 255 options")
			}
			if err != nil {
				return errors.New("noul criteria may only describe true and false")
			}
			for key, value := range options {
				if question.Type == "noul" && key != "true" && key != "false" {
					return errors.New("noul criteria may only describe true and false")
				}
				descriptions = append(descriptions, value)
			}
		case "score":
			if err := json.Unmarshal(criteria, &descriptions); err != nil || len(descriptions) < 2 || len(descriptions) > 10 {
				return errors.New("score criteria must contain between 2 and 10 levels")
			}
		default:
			return errors.New("each question must have type choice, score, or noul")
		}
		for _, description := range descriptions {
			if !isSystemOneEntry(description) {
				return errors.New("criterion descriptions must be text, objects, arrays, or null")
			}
		}
	}
	return nil
}

func IsSystemOneModel(model string) bool {
	switch model {
	case "jev-1.13.0", "jev-latest", "jev-preview":
		return true
	default:
		return false
	}
}

// UpstreamRequest drops gateway-only fields, including stream and credentials.
func (r *SystemOneRequest) UpstreamRequest() any {
	return struct {
		Model     string                       `json:"model"`
		State     json.RawMessage              `json:"state"`
		Questions map[string]SystemOneQuestion `json:"questions"`
	}{r.Model, r.State, r.Questions}
}

func (r *SystemOneRequest) GetSecurityText() string {
	if r == nil {
		return ""
	}
	body, _ := json.Marshal(struct {
		State     json.RawMessage              `json:"state"`
		Questions map[string]SystemOneQuestion `json:"questions"`
	}{r.State, r.Questions})
	// Jev accepts structured text, not a media protocol. Keys such as audio,
	// file_url or base64 are ordinary model-facing text and cannot be skipped
	// by the generic multimodal request collector's binary-field heuristics.
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return ""
	}
	var parts []string
	appendSystemOneSecurityText(&parts, value)
	return joinSecurityText(parts)
}

func appendSystemOneSecurityText(parts *[]string, value any) {
	switch typed := value.(type) {
	case string:
		appendSecurityString(parts, typed)
	case json.Number:
		appendSecurityString(parts, typed.String())
	case bool:
		appendSecurityString(parts, fmt.Sprint(typed))
	case []any:
		for _, item := range typed {
			appendSystemOneSecurityText(parts, item)
		}
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			appendSecurityString(parts, key)
			appendSystemOneSecurityText(parts, typed[key])
		}
	}
}

func (r *SystemOneRequest) GetTokenCountMeta() *types.TokenCountMeta {
	return &types.TokenCountMeta{TokenType: types.TokenTypeTokenizer, CombineText: r.GetSecurityText()}
}

func (r *SystemOneRequest) IsStream(_ *http.Request) bool { return false }

func (r *SystemOneRequest) SetModelName(model string) {
	if model != "" {
		r.Model = model
	}
}

type SystemOneResponse struct {
	Model   string
	Answers map[string]json.RawMessage
	Usage   json.RawMessage
}

func ValidateSystemOneResponse(raw []byte, questions map[string]SystemOneQuestion) (*SystemOneResponse, error) {
	fields, err := systemOneObject(raw)
	if err != nil {
		return nil, errors.New("TypeSafe response must be a JSON object")
	}
	response := &SystemOneResponse{Usage: fields["usage"]}
	if err = json.Unmarshal(fields["model"], &response.Model); err != nil || response.Model == "" {
		return nil, errors.New("TypeSafe response is missing model or answers")
	}
	response.Answers, err = systemOneObject(fields["answers"])
	if err != nil {
		return nil, errors.New("TypeSafe response is missing model or answers")
	}
	for id, question := range questions {
		answer, err := systemOneObject(response.Answers[id])
		var answerType string
		if err != nil || json.Unmarshal(answer["type"], &answerType) != nil || answerType != question.Type {
			return nil, errors.New("TypeSafe response is missing a matching answer for a question")
		}
	}
	return response, nil
}

// InputTokenUsage distinguishes an authoritative zero from missing or invalid
// usage; output tokens are deliberately ignored because Jev output is free.
func (r *SystemOneResponse) InputTokenUsage() (int, bool) {
	if r == nil {
		return 0, false
	}
	usage, err := systemOneObject(r.Usage)
	if err != nil {
		return 0, false
	}
	decoder := json.NewDecoder(bytes.NewReader(usage["input_tokens"]))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return 0, false
	}
	number, ok := value.(json.Number)
	if !ok {
		return 0, false
	}
	tokens, err := number.Float64()
	if err != nil || math.IsNaN(tokens) || math.IsInf(tokens, 0) || tokens < 0 || tokens > SystemOneMaxInputTokens || math.Trunc(tokens) != tokens {
		return 0, false
	}
	return int(tokens), true
}
