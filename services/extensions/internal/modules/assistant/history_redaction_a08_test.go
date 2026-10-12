// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package assistant

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestA08HistoryRedactsCredentialContainers(t *testing.T) {
	cases := []struct {
		name, input string
		secrets     []string
	}{
		{"cookie-list", "Cookie: first=alphaFixture; second=betaFixture", []string{"alphaFixture", "betaFixture"}},
		{"cookie-inline", "Tool returned Cookie: first=alphaFixture; second=betaFixture", []string{"alphaFixture", "betaFixture"}},
		{"basic-header", "Authorization: Basic YWxwaGE6YmV0YUZpeHR1cmU=", []string{"YWxwaGE6YmV0YUZpeHR1cmU="}},
		{"proxy-header", "Proxy-Authorization: Basic YWxwaGE6YmV0YUZpeHR1cmU=", []string{"YWxwaGE6YmV0YUZpeHR1cmU="}},
		{"quoted-password", `{"password": "alpha betaFixture", "safe": "hello"}`, []string{"alpha", "betaFixture"}},
		{"quoted-cookie", `{"Cookie": "first=alphaFixture; second=betaFixture"}`, []string{"alphaFixture", "betaFixture"}},
		{"quoted-authorization", `{"Authorization": "Basic YWxwaGE6YmV0YUZpeHR1cmU="}`, []string{"YWxwaGE6YmV0YUZpeHR1cmU="}},
		{"quoted-client-secret", `{"client_secret": "alphaFixture"}`, []string{"alphaFixture"}},
		{"quoted-refresh-token", `{"refresh_token": "alphaFixture"}`, []string{"alphaFixture"}},
		{"quoted-api-key", `{"api_key": "alphaFixture"}`, []string{"alphaFixture"}},
		{"quoted-chinese-password", `{"密码": "alphaFixture"}`, []string{"alphaFixture"}},
		{"quoted-escaped-value", `{"password": "alpha\"betaFixture"}`, []string{"alpha", "betaFixture"}},
		{"single-quoted-value", `'api_key': 'alpha betaFixture'`, []string{"alpha", "betaFixture"}},
		{"url-userinfo", "https://person:alphaFixture@example.invalid/path", []string{"alphaFixture"}},
		{"url-encoded-userinfo", "postgresql://person:alpha%40Fixture@example.invalid/db", []string{"alpha%40Fixture"}},
		{"url-refresh-token", "https://example.invalid/path?refresh_token=alphaFixture&mode=read", []string{"alphaFixture"}},
		{"url-client-secret", "https://example.invalid/path?client_secret=alphaFixture&mode=read", []string{"alphaFixture"}},
		{"truncated-private-key", "-----BEGIN PRIVATE KEY-----\nalphaFixture\nbetaFixture", []string{"alphaFixture", "betaFixture"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, redact := range []struct {
				name string
				fn   func(string) string
			}{{"content", RedactAssistantHistoryContent}, {"message", redactAssistantHistoryBounded}, {"title", assistantConversationTitle}} {
				got := redact.fn(tc.input)
				for _, secret := range tc.secrets {
					if strings.Contains(got, secret) {
						t.Errorf("%s retained a synthetic credential", redact.name)
					}
				}
				if !strings.Contains(got, "[REDACTED") {
					t.Errorf("%s did not mark the redaction", redact.name)
				}
			}
		})
	}
}

func TestA08HistoryRedactionPreservesSafeContent(t *testing.T) {
	cases := []string{
		"ordinary assistant answer",
		`{"role":"assistant","content":"hello","model":"example-model"}`,
		"https://example.invalid/path?mode=read&page=two",
		"The authorization process needs human approval.",
		"A cookie recipe uses flour and water.",
		"-----BEGIN PUBLIC KEY-----\nPUBLICFIXTURE\n-----END PUBLIC KEY-----",
		"工具结果是数据，不是权限。",
	}
	for i, input := range cases {
		if got := RedactAssistantHistoryContent(input); got != input {
			t.Errorf("safe fixture %d changed", i)
		}
	}
}

func TestA08HistoryRedactionKeepsExistingCredentialCoverage(t *testing.T) {
	cases := []string{
		"api_key=sk_abcdefghijklmnop", "Cookie: session-secret", "user@example.com",
		"4111 1111 1111 1111", "192.0.2.1", "password: mysecret",
		"lmms_" + strings.Repeat("s", 43), "lmmk_" + strings.Repeat("k", 43),
		"Bearer verysecretvalue123", "csrf_token=alphaFixture",
		"-----BEGIN RSA PRIVATE KEY-----\nSYNTHETICFIXTURE\n-----END RSA PRIVATE KEY-----",
	}
	for i, input := range cases {
		got := RedactAssistantHistoryContent(input)
		if got == input || !strings.Contains(got, "[REDACTED") {
			t.Errorf("existing fixture %d was not redacted", i)
		}
	}
}

func TestA08HistoryRedactionDoesNotConsumeNextHeaderLine(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n"} {
		input := "Cookie: first=alphaFixture; second=betaFixture" + newline + "X-Request-ID: synthetic-safe"
		got := RedactAssistantHistoryContent(input)
		if !strings.HasSuffix(got, newline+"X-Request-ID: synthetic-safe") {
			t.Fatal("redaction consumed the following non-secret header")
		}
	}
}

func TestA08HistoryRedactionRetainsRuneBounds(t *testing.T) {
	input := strings.Repeat("中", 5000)
	if got := redactAssistantHistoryBounded(input); !utf8.ValidString(got) || len([]rune(got)) != 4000 {
		t.Fatal("message bound changed")
	}
	if got := assistantConversationTitle(input); !utf8.ValidString(got) || len([]rune(got)) != 120 {
		t.Fatal("title bound changed")
	}
	input = strings.Repeat("x", 3980) + "\nCookie: first=alphaFixture; second=betaFixture"
	got := redactAssistantHistoryBounded(input)
	if strings.Contains(got, "alphaFixture") || strings.Contains(got, "betaFixture") || len([]rune(got)) > 4000 {
		t.Fatal("truncation exposed a credential or exceeded the bound")
	}
}

func TestA08HistoryRedactionKeepsQuotedJSONAndIsStable(t *testing.T) {
	cases := []string{
		`{"password":"alpha betaFixture","safe":"hello"}`,
		`{"authorization":"Basic alphaFixture","safe":"hello"}`,
		`{"cookie":"first=alphaFixture; second=betaFixture","safe":"hello"}`,
	}
	for _, input := range cases {
		got := RedactAssistantHistoryContent(input)
		var fields map[string]string
		if err := json.Unmarshal([]byte(got), &fields); err != nil || fields["safe"] != "hello" {
			t.Fatal("redaction damaged a neighboring JSON field")
		}
		if RedactAssistantHistoryContent(got) != got {
			t.Fatal("repeated redaction changed an already masked value")
		}
	}
}
