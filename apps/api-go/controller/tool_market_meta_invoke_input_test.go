package controller

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestToolMarketMetaInvokePreservesExactArguments(t *testing.T) {
	arguments := `{"id":9007199254740993,"decimal":0.123456789012345678901,"nested":{"action":"status","client_id":"business-field"},"text":"你好"}`
	raw := `{"action":"invoke","tool_id":"tool","version_id":"version","request_id":"request","arguments":` + arguments + `}`
	input, err := decodeToolMarketMetaInput(json.RawMessage(raw))
	if err != nil {
		t.Fatal(err)
	}
	if input.ToolID != "tool" || input.VersionID != "version" || input.RequestID != "request" {
		t.Fatalf("invocation identity was changed: %+v", input)
	}
	if string(input.Arguments) != arguments {
		t.Fatalf("business arguments were changed: %s", input.Arguments)
	}
}

func TestToolMarketMetaInvokeRejectsInvalidEnvelope(t *testing.T) {
	base := `{"action":"invoke","tool_id":"tool","version_id":"version","request_id":"request","arguments":{}}`
	cases := map[string]string{
		"missing tool":       strings.Replace(base, `"tool_id":"tool",`, "", 1),
		"missing version":    strings.Replace(base, `"version_id":"version",`, "", 1),
		"missing request":    strings.Replace(base, `"request_id":"request",`, "", 1),
		"missing arguments":  strings.Replace(base, `,"arguments":{}`, "", 1),
		"empty tool":         strings.Replace(base, `"tool_id":"tool"`, `"tool_id":""`, 1),
		"empty version":      strings.Replace(base, `"version_id":"version"`, `"version_id":""`, 1),
		"empty request":      strings.Replace(base, `"request_id":"request"`, `"request_id":""`, 1),
		"numeric request":    strings.Replace(base, `"request_id":"request"`, `"request_id":123`, 1),
		"null arguments":     strings.Replace(base, `"arguments":{}`, `"arguments":null`, 1),
		"array arguments":    strings.Replace(base, `"arguments":{}`, `"arguments":[]`, 1),
		"string arguments":   strings.Replace(base, `"arguments":{}`, `"arguments":"{}"`, 1),
		"number arguments":   strings.Replace(base, `"arguments":{}`, `"arguments":1`, 1),
		"boolean arguments":  strings.Replace(base, `"arguments":{}`, `"arguments":true`, 1),
		"tool too long":      strings.Replace(base, `"tool"`, fmt.Sprintf("%q", strings.Repeat("x", 129)), 1),
		"version too long":   strings.Replace(base, `"version"`, fmt.Sprintf("%q", strings.Repeat("x", 129)), 1),
		"request too long":   strings.Replace(base, `"request"`, fmt.Sprintf("%q", strings.Repeat("x", 129)), 1),
		"request byte limit": strings.Replace(base, `"request"`, fmt.Sprintf("%q", strings.Repeat("中", 43)), 1),
		"action alias":       strings.Replace(base, `"action"`, `"Action"`, 1),
		"arguments alias":    strings.Replace(base, `"arguments"`, `"Arguments"`, 1),
		"request alias":      strings.Replace(base, `"request_id"`, `"Request_ID"`, 1),
		"duplicate action":   strings.Replace(base, `"action":"invoke"`, `"action":"status","action":"invoke"`, 1),
		"duplicate request":  strings.Replace(base, `"request_id":"request"`, `"request_id":"other","request_id":"request"`, 1),
		"duplicate args":     strings.Replace(base, `"arguments":{}`, `"arguments":{},"arguments":{}`, 1),
		"account override":   strings.Replace(base, `"action":"invoke"`, `"action":"invoke","user_id":42`, 1),
		"client override":    strings.Replace(base, `"action":"invoke"`, `"action":"invoke","client_id":"other"`, 1),
		"grant override":     strings.Replace(base, `"action":"invoke"`, `"action":"invoke","grant_id":"other"`, 1),
		"budget override":    strings.Replace(base, `"action":"invoke"`, `"action":"invoke","max_total_quota":100`, 1),
		"endpoint override":  strings.Replace(base, `"action":"invoke"`, `"action":"invoke","endpoint":"https://example.com"`, 1),
		"nested state":       strings.Replace(base, `"action":"invoke"`, `"action":"invoke","request_state":"state"`, 1),
		"nested responses":   strings.Replace(base, `"action":"invoke"`, `"action":"invoke","input_responses":{}`, 1),
		"trailing object":    base + ` {}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeToolMarketMetaInput(json.RawMessage(raw)); err == nil {
				t.Fatal("invalid invocation was accepted")
			}
		})
	}
}

func TestToolMarketMetaInvokeSizeLimits(t *testing.T) {
	for _, size := range []int{8192, 8193, 128 << 10, (128 << 10) + 1} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			arguments := `{"text":"` + strings.Repeat("x", size-len(`{"text":""}`)) + `"}`
			raw := `{"action":"invoke","tool_id":"tool","version_id":"version","request_id":"request","arguments":` + arguments + `}`
			input, err := decodeToolMarketMetaInput(json.RawMessage(raw))
			if size > 128<<10 {
				if err == nil {
					t.Fatal("oversized business arguments were accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(input.Arguments) != size {
				t.Fatal("business argument bytes were changed")
			}
		})
	}
	base := `{"action":"invoke","tool_id":"tool","version_id":"version","request_id":"request","arguments":{}}`
	for _, size := range []int{256 << 10, (256 << 10) + 1} {
		t.Run(fmt.Sprintf("envelope-%d", size), func(t *testing.T) {
			_, err := decodeToolMarketMetaInput(json.RawMessage(base + strings.Repeat(" ", size-len(base))))
			if (err == nil) != (size <= 256<<10) {
				t.Fatalf("unexpected envelope limit result: %v", err)
			}
		})
	}
}

func TestToolMarketMetaManagementKeepsSmallEnvelope(t *testing.T) {
	for _, size := range []int{8192, 8193} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			base := `{"action":"status"}`
			_, err := decodeToolMarketMetaInput(json.RawMessage(base + strings.Repeat(" ", size-len(base))))
			if (err == nil) != (size <= 8192) {
				t.Fatalf("unexpected management limit result: %v", err)
			}
		})
	}
	for _, raw := range []string{
		`{"action":"status","arguments":{}}`,
		`{"action":"load","tool_id":"tool","version_id":"version","request_id":"request"}`,
	} {
		if _, err := decodeToolMarketMetaInput(json.RawMessage(raw)); err == nil {
			t.Fatal("invocation-only fields were accepted for management")
		}
	}
}
