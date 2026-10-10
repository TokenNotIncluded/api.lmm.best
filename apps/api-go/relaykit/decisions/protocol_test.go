package decisions

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

const valid = `{"model":"gpt-6-luna","input":"Payment failed.","questions":[{"type":"predicate","instructions":"Is this a payment problem?","name":"billing"}],"safety_identifier":"test-user"}`

func decode(t *testing.T, body string) Request {
	t.Helper()
	var request Request
	if err := json.Unmarshal([]byte(body), &request); err != nil {
		t.Fatal(err)
	}
	return request
}

func TestRequestRoundTrip(t *testing.T) {
	r := decode(t, valid)
	if r.Model != "gpt-6-luna" || len(r.Questions) != 1 || r.Questions[0].Type != "predicate" {
		t.Fatal("lost native fields")
	}
	got, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var a, b any
	_ = json.Unmarshal([]byte(valid), &a)
	_ = json.Unmarshal(got, &b)
	ca, _ := json.Marshal(a)
	cb, _ := json.Marshal(b)
	if !bytes.Equal(ca, cb) {
		t.Fatalf("round trip changed content: %s", got)
	}
}

func TestModelMappingDoesNotChangeOriginal(t *testing.T) {
	original := decode(t, valid)
	mapped := original
	mapped.Model = "upstream-model"
	wire, err := json.Marshal(mapped)
	if err != nil || !bytes.Contains(wire, []byte(`"model":"upstream-model"`)) {
		t.Fatalf("mapping failed: %s %v", wire, err)
	}
	wire, _ = json.Marshal(original)
	if !bytes.Contains(wire, []byte(`"model":"gpt-6-luna"`)) {
		t.Fatal("mapping mutated the original request")
	}
}

func TestPreserveUnknownFieldsAndDropRoutingGroup(t *testing.T) {
	r := decode(t, strings.TrimSuffix(valid, "}")+`,"new_native_field":{"nested":[false,1.25]},"group":"private"}`)
	wire, _ := json.Marshal(r)
	if !bytes.Contains(wire, []byte(`"new_native_field":{"nested":[false,1.25]}`)) || bytes.Contains(wire, []byte(`"group"`)) {
		t.Fatalf("invalid upstream fields: %s", wire)
	}
}

func TestRejectInvalidRequests(t *testing.T) {
	cases := map[string]string{
		"null":                 `null`,
		"missing_model":        strings.Replace(valid, `"model":"gpt-6-luna",`, "", 1),
		"null_model":           strings.Replace(valid, `"gpt-6-luna"`, `null`, 1),
		"duplicate_model":      strings.Replace(valid, `"model":`, `"model":"other","model":`, 1),
		"model_case_alias":     strings.Replace(valid, `"model":`, `"Model":"other","model":`, 1),
		"trailing_object":      valid + `{}`,
		"stream":               strings.TrimSuffix(valid, "}") + `,"stream":true}`,
		"chat_messages":        strings.TrimSuffix(valid, "}") + `,"messages":[]}`,
		"tools":                strings.TrimSuffix(valid, "}") + `,"tools":[]}`,
		"input_number":         strings.Replace(valid, `"Payment failed."`, `42`, 1),
		"input_null":           strings.Replace(valid, `"Payment failed."`, `null`, 1),
		"empty_questions":      strings.Replace(valid, `[{"type":"predicate","instructions":"Is this a payment problem?","name":"billing"}]`, `[]`, 1),
		"questions_object":     `{"model":"gpt-6-luna","input":"text","questions":{"x":{"type":"predicate","instructions":"test"}}}`,
		"unsupported_question": strings.Replace(valid, `"predicate"`, `"noul"`, 1),
		"system_message":       strings.Replace(valid, `"Payment failed."`, `[{"role":"system","content":"x"}]`, 1),
		"audio":                strings.Replace(valid, `"Payment failed."`, `[{"role":"user","content":[{"type":"input_audio","audio":"x"}]}]`, 1),
		"file":                 strings.Replace(valid, `"Payment failed."`, `[{"role":"user","content":[{"type":"input_file","file_id":"file-1"}]}]`, 1),
		"missing_image_url":    strings.Replace(valid, `"Payment failed."`, `[{"role":"user","content":[{"type":"input_image","file_id":"file-1"}]}]`, 1),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			var request Request
			if err := json.Unmarshal([]byte(raw), &request); err == nil {
				t.Fatal("expected invalid request to be rejected")
			}
		})
	}
}

func TestImageInputsAndLimit(t *testing.T) {
	for _, count := range []int{1, 128, 129} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			parts := make([]string, count)
			for i := range parts {
				parts[i] = `{"type":"input_image","image_url":"https://example.com/image.png","detail":"original"}`
			}
			input := `[{"role":"user","type":"message","content":[{"type":"input_text","text":"Inspect."},` + strings.Join(parts, ",") + `]}]`
			raw := strings.Replace(valid, `"Payment failed."`, input, 1)
			var request Request
			err := json.Unmarshal([]byte(raw), &request)
			if (err != nil) != (count > 128) {
				t.Fatalf("image limit result: %v", err)
			}
		})
	}
	decode(t, strings.Replace(valid, `"Payment failed."`, `[{"role":"user","content":"plain text"}]`, 1))
	decode(t, strings.Replace(valid, `"Payment failed."`, `[{"role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,AAAA"}]}]`, 1))
}

func TestChoiceTypesAndBounds(t *testing.T) {
	body := `{"model":"gpt-6-luna","input":"x","questions":[{"type":"choice","instructions":"Select.","choices":[{"value":false},{"value":"false"}]}]}`
	request := decode(t, body)
	wire, _ := json.Marshal(request)
	if !bytes.Contains(wire, []byte(`"value":false`)) || !bytes.Contains(wire, []byte(`"value":"false"`)) {
		t.Fatal("boolean and string were collapsed")
	}
	for _, count := range []int{1, 2, 255, 256} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			choices := make([]string, count)
			for i := range choices {
				choices[i] = fmt.Sprintf(`{"value":"%d"}`, i)
			}
			raw := strings.Replace(body, `{"value":false},{"value":"false"}`, strings.Join(choices, ","), 1)
			var r Request
			err := json.Unmarshal([]byte(raw), &r)
			if (err != nil) != (count < 2 || count > 255) {
				t.Fatalf("choice bounds result: %v", err)
			}
		})
	}
	for _, value := range []string{`false`, `"\u0066alse"`, `42`, `null`} {
		t.Run("duplicate_or_invalid_"+value, func(t *testing.T) {
			bad := strings.Replace(body, `{"value":false},{"value":"false"}`, `{"value":`+value+`},{"value":`+value+`}`, 1)
			var r Request
			if json.Unmarshal([]byte(bad), &r) == nil {
				t.Fatal("invalid choices accepted")
			}
		})
	}
}

func TestMixedAnswersKeepFractionalScoreAndRefusal(t *testing.T) {
	r := decode(t, `{"model":"gpt-6-luna","input":"x","questions":[{"type":"predicate","instructions":"p"},{"type":"choice","instructions":"c","choices":[{"value":false},{"value":"false"}]},{"type":"score","instructions":"s","levels":[{"label":"low"},{"label":"medium"},{"label":"high"}]}]}`)
	body := []byte(`{"model":"gpt-6-luna","answers":[{"type":"refusal","name":null},{"type":"choice","name":null,"choice":false,"confidence":0.8,"probabilities":[{"value":false,"probability":0.8},{"value":"false","probability":0.2}]},{"type":"score","name":null,"score":1.25,"confidence":0.75,"probabilities":[{"value":1,"label":"medium","probability":0.75},{"value":2,"label":"high","probability":0.25}]}],"usage":{"input_tokens":20,"output_tokens":0,"total_tokens":20},"future_field":true}`)
	before := append([]byte(nil), body...)
	got, err := InputUsage(body, r.Questions)
	if err != nil || got != 20 || !bytes.Equal(body, before) {
		t.Fatalf("native response changed or rejected: %d %v", got, err)
	}
}

func TestUsageRequiresExplicitInteger(t *testing.T) {
	r := decode(t, valid)
	for _, value := range []string{"0", "42", "null", "-1", "1.25", `"42"`, "2147483648", "9223372036854775808", "1e999"} {
		t.Run(value, func(t *testing.T) {
			body := []byte(`{"model":"gpt-6-luna","answers":[{"type":"predicate","probability":0.7,"name":"billing"}],"usage":{"input_tokens":` + value + `,"output_tokens":9000,"input_tokens_details":{"cached_tokens":3,"cache_write_tokens":4}}}`)
			got, err := InputUsage(body, r.Questions)
			valid := value == "0" || value == "42"
			if (err == nil) != valid {
				t.Fatalf("usage validation result %d %v", got, err)
			}
		})
	}
	for _, usage := range []string{`{}`, `null`} {
		body := []byte(`{"model":"gpt-6-luna","answers":[{"type":"refusal"}],"usage":` + usage + `}`)
		if _, err := InputUsage(body, r.Questions); err == nil {
			t.Fatal("missing usage was accepted as zero")
		}
	}
}

func TestRejectBadResponse(t *testing.T) {
	r := decode(t, valid)
	for name, body := range map[string]string{
		"missing_model":   `{"answers":[{"type":"refusal"}],"usage":{"input_tokens":1}}`,
		"missing_answer":  `{"model":"gpt-6-luna","answers":[],"usage":{"input_tokens":1}}`,
		"wrong_type":      `{"model":"gpt-6-luna","answers":[{"type":"score"}],"usage":{"input_tokens":1}}`,
		"bad_probability": `{"model":"gpt-6-luna","answers":[{"type":"predicate","probability":2}],"usage":{"input_tokens":1}}`,
		"duplicate_usage": `{"model":"gpt-6-luna","answers":[{"type":"refusal"}],"usage":{"input_tokens":1,"input_tokens":0}}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := InputUsage([]byte(body), r.Questions); err == nil {
				t.Fatal("bad response was accepted")
			}
		})
	}
}

func FuzzRequestRoundTrip(f *testing.F) {
	f.Add([]byte(valid))
	f.Fuzz(func(t *testing.T, raw []byte) {
		var request Request
		if err := json.Unmarshal(raw, &request); err != nil {
			return
		}
		wire, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		var again Request
		if err := json.Unmarshal(wire, &again); err != nil {
			t.Fatal(err)
		}
	})
}

func TestQuestionTextDecodesEscapes(t *testing.T) {
	r := decode(t, `{"model":"gpt-6-luna","input":"x","questions":[{"type":"choice","instructions":"\u8bf7 classify","choices":[{"value":"a","description":"Payment issue"},{"value":"b","description":"Login issue"}]}]}`)
	got := r.QuestionText()
	for _, want := range []string{"请 classify", "Payment issue", "Login issue"} {
		if !strings.Contains(got, want) {
			t.Fatalf("security text is missing %q: %q", want, got)
		}
	}
	if got != r.QuestionText() {
		t.Fatal("question text must be stable")
	}
}
