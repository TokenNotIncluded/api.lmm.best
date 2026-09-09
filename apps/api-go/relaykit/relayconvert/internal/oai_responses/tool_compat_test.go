package oairesponses

import (
	"encoding/json"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/relayconvert/convmeta"
)

func TestNormalizeResponsesToolsLeavesOrdinaryRequestUntouched(t *testing.T) {
	req := &dto.OpenAIResponsesRequest{Model: "test", Tools: json.RawMessage(`[{"type":"function","name":"read","parameters":{"type":"object"},"strict":false}]`), Input: json.RawMessage(`"hello"`)}
	out, mapping, err := NormalizeResponsesTools(req)
	if err != nil || out != req || mapping != nil {
		t.Fatalf("ordinary request changed: out=%p req=%p mapping=%v err=%v", out, req, mapping, err)
	}
}

func TestNormalizeResponsesNamespacesPreserveIdentityAndStrict(t *testing.T) {
	req := &dto.OpenAIResponsesRequest{
		Model: "test",
		Tools: json.RawMessage(`[
			{"type":"function","name":"read","parameters":{"type":"object"},"strict":false},
			{"type":"namespace","name":"files","description":"Read project files","tools":[{"type":"function","name":"read","description":"Read a file","defer_loading":true,"parameters":{"type":"object","properties":{"max":{"type":"integer","const":9007199254740993}}},"strict":true}]},
			{"type":"namespace","name":"database","tools":[{"type":"function","name":"read","parameters":{"type":"object"}}]}
		]`),
		Input:      json.RawMessage(`[{"type":"function_call","namespace":"files","name":"read","call_id":"c1","arguments":"{}"},{"type":"function_call_output","call_id":"c1","output":"ok"}]`),
		ToolChoice: json.RawMessage(`{"type":"function","namespace":"files","name":"read"}`),
	}
	before, _ := json.Marshal(req)
	out, mapping, err := NormalizeResponsesTools(req)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(req)
	if out == req || string(before) != string(after) {
		t.Fatal("normalization mutated the original request")
	}
	tools := decodedTools(t, out.Tools)
	if len(tools) != 3 || tools[0]["name"] != "read" || tools[0]["strict"] != false || tools[1]["strict"] != true {
		t.Fatalf("definitions or strict changed: %s", out.Tools)
	}
	alias := tools[1]["name"].(string)
	if alias == tools[2]["name"] || !regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`).MatchString(alias) {
		t.Fatalf("invalid or colliding aliases: %s", out.Tools)
	}
	if !strings.Contains(tools[1]["description"].(string), "files.read") || tools[2]["description"] != "database.read" {
		t.Fatalf("qualified tool names lost from model-visible descriptions: %s", out.Tools)
	}
	if mapping[alias] != (convmeta.ResponsesToolIdentity{Name: "read", Namespace: "files"}) {
		t.Fatalf("identity lost: %#v", mapping)
	}
	if _, found := tools[1]["defer_loading"]; found || !strings.Contains(string(out.Tools), "9007199254740993") {
		t.Fatalf("deferred flag or schema corrupted: %s", out.Tools)
	}
	input := decodedTools(t, out.Input)
	if input[0]["name"] != alias || input[0]["namespace"] != nil || !strings.Contains(string(out.ToolChoice), alias) {
		t.Fatalf("history and forced choice do not match: input=%s choice=%s", out.Input, out.ToolChoice)
	}
	// Reordering declarations must not change identities sent upstream.
	reordered := *req
	var declarations []json.RawMessage
	if err := json.Unmarshal(req.Tools, &declarations); err != nil {
		t.Fatal(err)
	}
	declarations[1], declarations[2] = declarations[2], declarations[1]
	reordered.Tools, _ = json.Marshal(declarations)
	_, again, err := NormalizeResponsesTools(&reordered)
	if err != nil || !reflect.DeepEqual(mapping, again) {
		t.Fatalf("aliases changed with tool order: %v %v (%v)", mapping, again, err)
	}
}

func TestNormalizeResponsesToolsAvoidsReservedAliasCollision(t *testing.T) {
	req := &dto.OpenAIResponsesRequest{Tools: json.RawMessage(`[{"type":"namespace","name":"files","tools":[{"type":"function","name":"read","parameters":{"type":"object"}}]}]`)}
	first, _, err := NormalizeResponsesTools(req)
	if err != nil {
		t.Fatal(err)
	}
	alias := decodedTools(t, first.Tools)[0]["name"].(string)
	var source []map[string]any
	if err := json.Unmarshal(req.Tools, &source); err != nil {
		t.Fatal(err)
	}
	source = append(source, map[string]any{"type": "function", "name": alias, "parameters": map[string]any{"type": "object"}})
	req.Tools, _ = json.Marshal(source)
	out, mapping, err := NormalizeResponsesTools(req)
	if err != nil {
		t.Fatal(err)
	}
	tools := decodedTools(t, out.Tools)
	if tools[0]["name"] == alias || tools[1]["name"] != alias || len(mapping) != 1 {
		t.Fatalf("top-level function was renamed or collision survived: %s %v", out.Tools, mapping)
	}
}

func TestNormalizeResponsesClientToolSearchRoundTrip(t *testing.T) {
	req := &dto.OpenAIResponsesRequest{
		Tools: json.RawMessage(`[{"type":"tool_search","execution":"client","description":"Find integrations","parameters":{"type":"object","properties":{"query":{"type":"string"}},"required":["query"],"additionalProperties":false}}]`),
		Input: json.RawMessage(`[
			{"type":"tool_search_call","execution":"client","call_id":"search1","arguments":{"query":"find files"}},
			{"type":"tool_search_output","execution":"client","call_id":"search1","status":"completed","tools":[{"type":"namespace","name":"files","tools":[{"type":"function","name":"read","defer_loading":true,"parameters":{"type":"object"}}]}]},
			{"type":"function_call","namespace":"files","name":"read","call_id":"read1","arguments":"{}"},
			{"type":"function_call_output","call_id":"read1","output":"contents"}
		]`),
		ToolChoice: json.RawMessage(`{"type":"tool_search"}`),
	}
	out, mapping, err := NormalizeResponsesTools(req)
	if err != nil {
		t.Fatal(err)
	}
	tools, input := decodedTools(t, out.Tools), decodedTools(t, out.Input)
	if len(tools) != 2 || len(input) != 4 {
		t.Fatalf("missing tools/history: tools=%s input=%s", out.Tools, out.Input)
	}
	searchAlias := tools[0]["name"].(string)
	functionAlias := tools[1]["name"].(string)
	if !mapping[searchAlias].ToolSearch || mapping[functionAlias].Namespace != "files" {
		t.Fatalf("identities missing: %v", mapping)
	}
	parameters := tools[0]["parameters"].(map[string]any)
	if !reflect.DeepEqual(parameters["required"], []any{"query"}) || parameters["properties"].(map[string]any)["goal"] != nil {
		t.Fatalf("search schema was replaced: %s", out.Tools)
	}
	if input[0]["type"] != "function_call" || input[0]["name"] != searchAlias || input[0]["arguments"] != `{"query":"find files"}` || input[0]["call_id"] != "search1" {
		t.Fatalf("search call was not converted correctly: %s", out.Input)
	}
	if input[1]["type"] != "function_call_output" || !strings.Contains(input[1]["output"].(string), functionAlias) || input[2]["name"] != functionAlias {
		t.Fatalf("loaded tools or subsequent invocation lost: %s", out.Input)
	}
	if !strings.Contains(string(out.ToolChoice), searchAlias) {
		t.Fatalf("forced search choice was not mapped: %s", out.ToolChoice)
	}
}

func TestNormalizeResponsesSearchHistoryWithoutSearchDeclaration(t *testing.T) {
	req := &dto.OpenAIResponsesRequest{Input: json.RawMessage(`[
		{"type":"tool_search_call","execution":"client","call_id":"s1","arguments":"{\"goal\":\"find\"}"},
		{"type":"tool_search_output","execution":"client","call_id":"s1","tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}]},
		{"type":"function_call","namespace":"lookup","name":"lookup","call_id":"f1","arguments":"{}"},
		{"type":"function_call_output","call_id":"f1","output":"ok"}
	]`)}
	out, mapping, err := NormalizeResponsesTools(req)
	if err != nil {
		t.Fatal(err)
	}
	tools, input := decodedTools(t, out.Tools), decodedTools(t, out.Input)
	if len(tools) != 1 || tools[0]["name"] != "lookup" || input[2]["name"] != "lookup" {
		t.Fatalf("discovered standalone function was renamed: tools=%s input=%s", out.Tools, out.Input)
	}
	if mapping["lookup"].Namespace != "lookup" || !mapping[input[0]["name"].(string)].ToolSearch {
		t.Fatalf("history identities lost: %v", mapping)
	}
}

func TestNormalizeResponsesServerToolSearchEagerlyLoadsTools(t *testing.T) {
	req := &dto.OpenAIResponsesRequest{
		Tools: json.RawMessage(`[{"type":"tool_search"},{"type":"function","name":"read","defer_loading":true,"parameters":{"type":"object"}}]`),
		Input: json.RawMessage(`[
			{"type":"message","role":"user","content":"Read the file"},
			{"type":"tool_search_call","execution":"server","call_id":null,"arguments":{"paths":["read"]}},
			{"type":"tool_search_output","execution":"server","call_id":null,"tools":[{"type":"function","name":"read","parameters":{"type":"object"}},{"type":"function","name":"write","defer_loading":true,"parameters":{"type":"object"}}]}
		]`),
	}
	out, mapping, err := NormalizeResponsesTools(req)
	if err != nil {
		t.Fatal(err)
	}
	tools, input := decodedTools(t, out.Tools), decodedTools(t, out.Input)
	if len(tools) != 2 || len(input) != 1 || mapping != nil || strings.Contains(string(out.Tools), "defer_loading") || strings.Contains(string(out.Input), "tool_search") {
		t.Fatalf("hosted search not flattened: tools=%s input=%s mapping=%v", out.Tools, out.Input, mapping)
	}
}

func TestNormalizeResponsesClientSearchDefaultsOnlyMissingSchema(t *testing.T) {
	req := &dto.OpenAIResponsesRequest{Tools: json.RawMessage(`[{"type":"tool_search","execution":"client"},{"type":"function","name":"tool_search","parameters":{"type":"object"}}]`)}
	out, mapping, err := NormalizeResponsesTools(req)
	if err != nil {
		t.Fatal(err)
	}
	tools := decodedTools(t, out.Tools)
	if tools[0]["name"] == "tool_search" || tools[1]["name"] != "tool_search" || !mapping[tools[0]["name"].(string)].ToolSearch || !strings.Contains(string(out.Tools), `"goal"`) {
		t.Fatalf("search default/collision incorrect: %s %v", out.Tools, mapping)
	}
}

func TestNormalizeResponsesToolsRejectsUnrepresentableInputs(t *testing.T) {
	tests := []struct {
		name, tools, input, choice, want string
	}{
		{"custom namespace", `[{"type":"namespace","name":"tools","tools":[{"type":"custom","name":"apply"}]}]`, `[]`, ``, "unsupported tool type"},
		{"nested namespace", `[{"type":"namespace","name":"a","tools":[{"type":"namespace","name":"b","tools":[]}]}]`, `[]`, ``, "unsupported tool type"},
		{"invalid discovered tools", `[]`, `[{"type":"tool_search_output","execution":"client","call_id":"s1","tools":{}}]`, ``, "tools must be an array"},
		{"conflicting discovered tools", `[{"type":"function","name":"read","parameters":{"type":"object"}}]`, `[{"type":"tool_search_output","execution":"client","call_id":"s1","tools":[{"type":"function","name":"read","parameters":{"type":"string"}}]}]`, ``, "conflicting tool definitions"},
		{"conflicting namespace tools", `[{"type":"namespace","name":"a","tools":[{"type":"function","name":"read","strict":true},{"type":"function","name":"read","strict":false}]}]`, `[]`, ``, "conflicting tool definitions"},
		{"forced server search", `[{"type":"tool_search"},{"type":"function","name":"read"}]`, `[]`, `{"type":"tool_search"}`, "forced server tool_search"},
		{"server search without catalog", `[{"type":"tool_search"}]`, `[]`, ``, "requires supplied function definitions"},
		{"unsupported allowed tools", `[{"type":"function","name":"read"}]`, `[]`, `{"type":"allowed_tools","mode":"auto","tools":[{"type":"function","namespace":"files","name":"read"}]}`, "allowed_tools cannot be converted"},
		{"unknown forced namespace", `[]`, `[]`, `{"type":"function","namespace":"a","name":"missing"}`, "unavailable namespaced function"},
		{"invalid execution", `[{"type":"tool_search","execution":"unknown"}]`, `[]`, ``, "unsupported tool_search execution"},
		{"mixed execution", `[{"type":"tool_search","execution":"client"},{"type":"tool_search","execution":"server"}]`, `[]`, ``, "cannot combine client and server"},
		{"null schema", `[{"type":"tool_search","execution":"client","parameters":null}]`, `[]`, ``, "parameters must be a JSON schema object"},
		{"missing call ID", `[]`, `[{"type":"tool_search_call","execution":"client","arguments":{}}]`, ``, "requires call_id"},
		{"invalid arguments", `[]`, `[{"type":"tool_search_call","execution":"client","call_id":"s1","arguments":"not JSON"}]`, ``, "arguments must encode a JSON object"},
		{"custom history namespace", `[]`, `[{"type":"custom_tool_call","namespace":"a","name":"read","input":"file"}]`, ``, "namespaced custom tools"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &dto.OpenAIResponsesRequest{Tools: json.RawMessage(tt.tools), Input: json.RawMessage(tt.input), ToolChoice: json.RawMessage(tt.choice)}
			_, _, err := NormalizeResponsesTools(req)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("got %v; want %q", err, tt.want)
			}
		})
	}
}

func decodedTools(t *testing.T, raw json.RawMessage) []map[string]any {
	t.Helper()
	var result []map[string]any
	if err := decodeToolJSON(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result
}
