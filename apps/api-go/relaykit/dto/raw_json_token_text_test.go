package dto

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeRawJSONForTokenCount(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "basic CJK", raw: `{"x":"\u4e2d"}`, want: `{"x":"中"}`},
		{name: "surrogate pair", raw: `{"x":"\uD83D\uDE00"}`, want: `{"x":"😀"}`},
		{name: "escaped backslash", raw: `{"x":"\\u4e2d"}`, want: `{"x":"\\u4e2d"}`},
		{name: "escaped quote before unicode", raw: `{"x":"a\"b\u4e2d"}`, want: `{"x":"a\"b中"}`},
		{name: "lone high surrogate", raw: `{"x":"\uD83D!"}`, want: `{"x":"\uD83D!"}`},
		{name: "lone low surrogate", raw: `{"x":"\uDE00"}`, want: `{"x":"\uDE00"}`},
		{name: "malformed hex", raw: `{"x":"\u12G4"}`, want: `{"x":"\u12G4"}`},
		{name: "truncated", raw: `{"x":"\u123"}`, want: `{"x":"\u123"}`},
		{name: "outside string", raw: `{"x":1,"raw":u4e2d}`, want: `{"x":1,"raw":u4e2d}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, normalizeRawJSONForTokenCount(json.RawMessage(tc.raw)))
		})
	}
}

func TestResponsesRawJSONUnicodeNormalization(t *testing.T) {
	rawUTF8 := OpenAIResponsesRequest{
		Instructions: json.RawMessage(`"你好"`),
		Metadata:     json.RawMessage(`{"label":"中文"}`),
		Text:         json.RawMessage(`{"format":{"description":"天气"}}`),
		ToolChoice:   json.RawMessage(`{"type":"function","name":"查询"}`),
		Prompt:       json.RawMessage(`{"id":"提示"}`),
		Tools:        json.RawMessage(`[{"type":"function","name":"天气"}]`),
	}
	escaped := OpenAIResponsesRequest{
		Instructions: json.RawMessage(`"\u4f60\u597d"`),
		Metadata:     json.RawMessage(`{"label":"\u4e2d\u6587"}`),
		Text:         json.RawMessage(`{"format":{"description":"\u5929\u6c14"}}`),
		ToolChoice:   json.RawMessage(`{"type":"function","name":"\u67e5\u8be2"}`),
		Prompt:       json.RawMessage(`{"id":"\u63d0\u793a"}`),
		Tools:        json.RawMessage(`[{"type":"function","name":"\u5929\u6c14"}]`),
	}
	require.Equal(t, rawUTF8.GetTokenCountMeta().CombineText, escaped.GetTokenCountMeta().CombineText)
}

func TestCompactionRawJSONUnicodeNormalization(t *testing.T) {
	rawUTF8 := OpenAIResponsesCompactionRequest{
		Instructions: json.RawMessage(`"摘要"`),
		Tools:        json.RawMessage(`[{"name":"工具"}]`),
		Input:        json.RawMessage(`[{"type":"function_call_output","output":"中文"}]`),
	}
	escaped := OpenAIResponsesCompactionRequest{
		Instructions: json.RawMessage(`"\u6458\u8981"`),
		Tools:        json.RawMessage(`[{"name":"\u5de5\u5177"}]`),
		Input:        json.RawMessage(`[{"type":"function_call_output","output":"\u4e2d\u6587"}]`),
	}
	require.Equal(t, rawUTF8.GetTokenCountMeta().CombineText, escaped.GetTokenCountMeta().CombineText)
}

func TestSemanticStringsAreNotRecursivelyDecoded(t *testing.T) {
	compact := OpenAIResponsesCompactionRequest{Input: json.RawMessage(`"\\u4e2d"`)}
	require.Equal(t, `\u4e2d`, compact.GetTokenCountMeta().CombineText)

	var chat GeneralOpenAIRequest
	require.NoError(t, json.Unmarshal([]byte(`{"messages":[{"role":"user","content":"\\u4e2d"}]}`), &chat))
	meta := chat.GetTokenCountMeta()
	require.Contains(t, meta.CombineText, `\u4e2d`)
	require.NotContains(t, meta.CombineText, "中")
}

func TestAlphaSearchRawJSONUnicodeNormalization(t *testing.T) {
	rawUTF8 := AlphaSearchRequest{RawBody: json.RawMessage(`{"query":"中文"}`)}
	escaped := AlphaSearchRequest{RawBody: json.RawMessage(`{"query":"\u4e2d\u6587"}`)}
	require.Equal(t, rawUTF8.GetTokenCountMeta().CombineText, escaped.GetTokenCountMeta().CombineText)
}
