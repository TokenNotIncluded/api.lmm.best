package dto

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func decodeAndValidateSystemOne(raw []byte) (*SystemOneRequest, error) {
	var request SystemOneRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		return nil, err
	}
	return &request, request.Validate()
}

func systemOneTestBody(state any, question map[string]any) map[string]any {
	return map[string]any{
		"model":     "jev-latest",
		"state":     state,
		"questions": map[string]any{"decision": question},
	}
}

func TestSystemOneRequestAcceptsTypeSafeEntryShapes(t *testing.T) {
	entries := []struct {
		name  string
		value any
	}{
		{"null", nil},
		{"text", "判断这段文字"},
		{"object", map[string]any{"text": "hello", "count": 3}},
		{"array", []any{"hello", 3, true, nil}},
	}
	for _, entry := range entries {
		t.Run(entry.name, func(t *testing.T) {
			body := systemOneTestBody(entry.value, map[string]any{
				"type":         "choice",
				"instructions": entry.value,
				"criteria":     map[string]any{"match": entry.value},
			})
			raw, err := json.Marshal(body)
			require.NoError(t, err)
			_, err = decodeAndValidateSystemOne(raw)
			require.NoError(t, err)
		})
	}
	// Instructions can be omitted; state cannot. A public model alias is resolved
	// to a declared Jev model later by channel model mapping.
	raw := []byte(`{"model":"my-jev-alias","state":null,"questions":{"ok":{"type":"noul"}},"stream":false}`)
	request, err := decodeAndValidateSystemOne(raw)
	require.NoError(t, err)
	assert.False(t, request.IsStream(httptest.NewRequest("POST", "/typesafe/v1/systemone", nil)))
	request.SetModelName("jev-preview")
	assert.Equal(t, "jev-preview", request.Model)
}

func TestSystemOneRequestRejectsInvalidProtocolShapes(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{"null request", `null`},
		{"array request", `[]`},
		{"missing model", `{"state":null,"questions":{"a":{"type":"noul"}}}`},
		{"blank model", `{"model":" \t","state":null,"questions":{"a":{"type":"noul"}}}`},
		{"nonstring model", `{"model":3,"state":null,"questions":{"a":{"type":"noul"}}}`},
		{"case changed model key", `{"Model":"jev-latest","state":null,"questions":{"a":{"type":"noul"}}}`},
		{"duplicate model", `{"model":"jev-latest","model":"jev-preview","state":null,"questions":{"a":{"type":"noul"}}}`},
		{"missing state", `{"model":"jev-latest","questions":{"a":{"type":"noul"}}}`},
		{"numeric state", `{"model":"jev-latest","state":3,"questions":{"a":{"type":"noul"}}}`},
		{"boolean state", `{"model":"jev-latest","state":false,"questions":{"a":{"type":"noul"}}}`},
		{"missing questions", `{"model":"jev-latest","state":null}`},
		{"null questions", `{"model":"jev-latest","state":null,"questions":null}`},
		{"empty questions", `{"model":"jev-latest","state":null,"questions":{}}`},
		{"array questions", `{"model":"jev-latest","state":null,"questions":[]}`},
		{"null question", `{"model":"jev-latest","state":null,"questions":{"a":null}}`},
		{"array question", `{"model":"jev-latest","state":null,"questions":{"a":[]}}`},
		{"unknown question type", `{"model":"jev-latest","state":null,"questions":{"a":{"type":"chat"}}}`},
		{"case changed question type", `{"model":"jev-latest","state":null,"questions":{"a":{"type":"Noul"}}}`},
		{"numeric instructions", `{"model":"jev-latest","state":null,"questions":{"a":{"type":"noul","instructions":3}}}`},
		{"boolean instructions", `{"model":"jev-latest","state":null,"questions":{"a":{"type":"noul","instructions":true}}}`},
		{"stream true", `{"model":"jev-latest","state":null,"questions":{"a":{"type":"noul"}},"stream":true}`},
		{"stream null", `{"model":"jev-latest","state":null,"questions":{"a":{"type":"noul"}},"stream":null}`},
		{"stream number", `{"model":"jev-latest","state":null,"questions":{"a":{"type":"noul"}},"stream":0}`},
		{"stream string", `{"model":"jev-latest","state":null,"questions":{"a":{"type":"noul"}},"stream":"false"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := decodeAndValidateSystemOne([]byte(test.raw))
			require.Error(t, err)
		})
	}
}

func TestSystemOneRequestCriteriaBoundaries(t *testing.T) {
	choiceCriteria := func(size int) map[string]any {
		criteria := make(map[string]any, size)
		for index := 0; index < size; index++ {
			criteria[fmt.Sprintf("option%d", index)] = "description"
		}
		return criteria
	}
	scoreCriteria := func(size int) []any {
		criteria := make([]any, size)
		for index := range criteria {
			criteria[index] = "description"
		}
		return criteria
	}
	tests := []struct {
		name     string
		kind     string
		criteria any
		valid    bool
	}{
		{"choice missing", "choice", nil, false},
		{"choice empty", "choice", choiceCriteria(0), false},
		{"choice minimum", "choice", choiceCriteria(1), true},
		{"choice maximum", "choice", choiceCriteria(255), true},
		{"choice too many", "choice", choiceCriteria(256), false},
		{"choice array", "choice", scoreCriteria(2), false},
		{"score missing", "score", nil, false},
		{"score too short", "score", scoreCriteria(1), false},
		{"score minimum", "score", scoreCriteria(2), true},
		{"score maximum", "score", scoreCriteria(10), true},
		{"score too long", "score", scoreCriteria(11), false},
		{"score object", "score", choiceCriteria(2), false},
		{"noul null", "noul", nil, true},
		{"noul empty", "noul", map[string]any{}, true},
		{"noul true only", "noul", map[string]any{"true": "yes"}, true},
		{"noul both", "noul", map[string]any{"true": nil, "false": []any{"no"}}, true},
		{"noul other key", "noul", map[string]any{"yes": "yes"}, false},
		{"noul array", "noul", []any{"yes", "no"}, false},
		{"choice numeric description", "choice", map[string]any{"a": 1}, false},
		{"score boolean description", "score", []any{"no", false}, false},
		{"noul numeric description", "noul", map[string]any{"true": 1}, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := systemOneTestBody("context", map[string]any{"type": test.kind, "criteria": test.criteria})
			raw, err := json.Marshal(body)
			require.NoError(t, err)
			_, err = decodeAndValidateSystemOne(raw)
			if test.valid {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestSystemOneRequestPreservesStructuredEntries(t *testing.T) {
	raw := []byte(`{"model":"jev-latest","state":{"text":"你好","records":[{"value":true},null]},"questions":{"classify":{"type":"choice","instructions":["选择类别",{"context":"判断"}],"criteria":{"yes":{"text":"接受"},"no":["拒绝",null]}}}}`)
	request, err := decodeAndValidateSystemOne(raw)
	require.NoError(t, err)
	encoded, err := json.Marshal(request)
	require.NoError(t, err)
	assert.JSONEq(t, string(raw), string(encoded))
	text := request.GetSecurityText()
	for _, fragment := range []string{"你好", "选择类别", "判断", "接受", "拒绝"} {
		assert.Contains(t, text, fragment)
	}
	assert.NotContains(t, text, "jev-latest")
}

func TestSystemOneDeclaredModels(t *testing.T) {
	for _, model := range []string{"jev-1.13.0", "jev-latest", "jev-preview"} {
		assert.True(t, IsSystemOneModel(model), model)
	}
	for _, model := range []string{"", "jev", "jev-1.12.0", "gpt-4o", "jev-latest ", "JEV-LATEST"} {
		assert.False(t, IsSystemOneModel(model), model)
	}
	assert.Equal(t, 65536, SystemOneMaxInputTokens)
}

func TestSystemOneUpstreamRequestPreservesQuestionExtensions(t *testing.T) {
	raw := []byte(`{"model":"my-jev-alias","state":null,"questions":{"ok":{"type":"noul","vendor_extension":{"enabled":true}}},"stream":false,"api_key":"client-secret","gateway_extension":"ignore"}`)
	request, err := decodeAndValidateSystemOne(raw)
	require.NoError(t, err)
	request.SetModelName("jev-preview")
	upstream, err := json.Marshal(request.UpstreamRequest())
	require.NoError(t, err)
	assert.JSONEq(t, `{"model":"jev-preview","state":null,"questions":{"ok":{"type":"noul","vendor_extension":{"enabled":true}}}}`, string(upstream))
	assert.NotContains(t, string(upstream), "client-secret")
}

func TestSystemOneResponseRequiresMatchingAnswers(t *testing.T) {
	questions := map[string]SystemOneQuestion{"decision": {Type: "noul"}}
	for _, raw := range []string{
		`null`,
		`[]`,
		`{"answers":{"decision":{"type":"noul"}}}`,
		`{"model":"","answers":{"decision":{"type":"noul"}}}`,
		`{"model":"jev-latest"}`,
		`{"model":"jev-latest","answers":null}`,
		`{"model":"jev-latest","answers":[]}`,
		`{"model":"jev-latest","answers":{}}`,
		`{"model":"jev-latest","answers":{"decision":null}}`,
		`{"model":"jev-latest","answers":{"decision":{"type":"choice"}}}`,
	} {
		_, err := ValidateSystemOneResponse([]byte(raw), questions)
		require.Error(t, err, raw)
	}
	response, err := ValidateSystemOneResponse([]byte(`{"model":"jev-1.13.0","answers":{"decision":{"type":"noul","value":true},"extra":{"type":"choice"}},"usage":{"input_tokens":0,"output_tokens":900}}`), questions)
	require.NoError(t, err)
	assert.Equal(t, "jev-1.13.0", response.Model)
	tokens, authoritative := response.InputTokenUsage()
	assert.True(t, authoritative, "a reported zero is authoritative and must settle the full reservation")
	assert.Zero(t, tokens, "free output tokens must never be added to input usage")
}

func TestSystemOneInputTokenUsageIsAuthoritativeOnlyWithinContext(t *testing.T) {
	for _, test := range []struct {
		usage string
		want  int
		valid bool
	}{
		{`{"input_tokens":0}`, 0, true},
		{`{"input_tokens":1}`, 1, true},
		{`{"input_tokens":65536}`, 65536, true},
		{`{"input_tokens":1.0}`, 1, true},
		{`{"input_tokens":1e3}`, 1000, true},
		{`{"input_tokens":65537}`, 0, false},
		{`{"input_tokens":-1}`, 0, false},
		{`{"input_tokens":1.25}`, 0, false},
		{`{"input_tokens":"10"}`, 0, false},
		{`{"input_tokens":false}`, 0, false},
		{`{"input_tokens":null}`, 0, false},
		{`{"input_tokens":1e309}`, 0, false},
		{`{"output_tokens":10}`, 0, false},
		{`null`, 0, false},
		{`[]`, 0, false},
		{``, 0, false},
	} {
		t.Run(test.usage, func(t *testing.T) {
			response := &SystemOneResponse{Usage: json.RawMessage(test.usage)}
			tokens, valid := response.InputTokenUsage()
			assert.Equal(t, test.want, tokens)
			assert.Equal(t, test.valid, valid)
		})
	}
}
