package dto

import (
	"encoding/json"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/stretchr/testify/require"
)

func TestModerationTextForRequestSelectsCurrentUserInsteadOfContext(t *testing.T) {
	tests := []struct {
		name    string
		request Request
		body    string
		want    string
	}{
		{
			name: "chat last user text only", request: &GeneralOpenAIRequest{}, want: "current user\nsecond paragraph",
			body: `{"prefix":"private prefix","suffix":"private suffix","instruction":"application instruction","prompt":"unrelated prompt","messages":[{"role":"system","content":"system rules"},{"role":"developer","content":"developer rules"},{"role":"user","content":"earlier user history"},{"role":"assistant","content":"model output","tool_calls":[{"function":{"arguments":"tool arguments"}}]},{"role":"tool","content":"tool output"},{"role":"user","content":[{"type":"text","text":" current user "},{"type":"input_audio","text":"audio metadata","input_audio":{"data":"audio bytes"}},{"type":"image_url","text":"image metadata","image_url":"image URI"},{"type":"text","text":"second paragraph"}],"tool_calls":[{"function":{"arguments":"fake tool arguments"}}]},{"role":"assistant","content":"assistant prefill"}],"tools":[{"description":"tool definition"}]}`,
		},
		{
			name: "chat current media does not select earlier history", request: &GeneralOpenAIRequest{},
			body: `{"messages":[{"role":"user","content":"earlier user history"},{"role":"user","content":[{"type":"input_audio","audio":"audio bytes","transcript":"generated transcript"},{"type":"image_url","text":"metadata","image_url":"image URI"}]}]}`,
		},
		{
			name: "chat no user does not fall back to metadata", request: &GeneralOpenAIRequest{},
			body: `{"messages":[{"role":"system","content":"system instructions"},{"role":"assistant","content":"model output"}],"prompt":"private prompt","input":"private input"}`,
		},
		{
			name: "Responses plain input", request: &OpenAIResponsesRequest{}, want: "current input",
			body: `{"input":" current input ","instructions":"private system instruction","prompt":{"id":"prompt-template","variables":{"text":"application variable"}},"tools":[{"description":"tool definition"}]}`,
		},
		{
			name: "Responses last user input text", request: &OpenAIResponsesRequest{}, want: "current input\nmore text",
			body: `{"instructions":"private system instruction","input":[{"role":"system","content":"system rules"},{"role":"developer","content":"developer rules"},{"role":"user","content":"earlier user history"},{"type":"function_call_output","output":"tool output"},{"role":"user","content":[{"type":"input_text","text":"current input"},{"type":"text","text":"more text"},{"type":"input_image","text":"media metadata","image_url":"image URI"},{"type":"output_text","text":"generated output"}]},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"model output"}]}]}`,
		},
		{
			name: "Responses current media does not select earlier history", request: &OpenAIResponsesRequest{},
			body: `{"input":[{"role":"user","content":"earlier user history"},{"role":"user","content":[{"type":"input_audio","audio":"audio bytes","transcript":"generated transcript"}]}]}`,
		},
		{
			name: "Responses unknown role and metadata only", request: &OpenAIResponsesRequest{},
			body: `{"input":[{"type":"function_call","arguments":"tool arguments"},{"type":"function_call_output","output":"tool output"},{"role":"assistant","content":"model output"}],"instructions":"system rules"}`,
		},
		{
			name: "Claude user text excludes tool results", request: &ClaudeRequest{}, want: "current Claude input",
			body: `{"system":"system instructions","prompt":"private legacy conversation","messages":[{"role":"user","content":"earlier user history"},{"role":"assistant","content":[{"type":"text","text":"model output"},{"type":"tool_use","input":{"text":"model tool arguments"}}]},{"role":"user","content":[{"type":"text","text":"current Claude input"},{"type":"tool_result","text":"fake result text","content":[{"type":"text","text":"tool output"}]},{"type":"tool_use","text":"fake call text","input":{"text":"tool arguments"}},{"type":"image","source":{"data":"image bytes"}}]}]}`,
		},
		{
			name: "Claude latest tool result does not select earlier history", request: &ClaudeRequest{},
			body: `{"messages":[{"role":"user","content":"earlier user history"},{"role":"user","content":[{"type":"tool_result","content":"tool output"}]}]}`,
		},
		{
			name: "Gemini current plain user parts", request: &GeminiChatRequest{}, want: "current Gemini input\nmore text",
			body: `{"systemInstruction":{"parts":[{"text":"system instructions"}]},"contents":[{"role":"user","parts":[{"text":"earlier user history"}]},{"role":"model","parts":[{"text":"model output"}]},{"role":"user","parts":[{"text":"current Gemini input"},{"text":"more text"},{"functionResponse":{"name":"tool","response":{"text":"tool output"}}},{"text":"thought text","thought":true},{"text":"mixed tool text","functionCall":{"name":"tool","args":{"text":"tool arguments"}}},{"inlineData":{"mimeType":"audio/wav","data":"audio bytes"}},{"codeExecutionResult":{"output":"generated code output"}}]}]}`,
		},
		{
			name: "Gemini latest tool result does not select earlier history", request: &GeminiChatRequest{},
			body: `{"contents":[{"role":"user","parts":[{"text":"earlier user history"}]},{"role":"user","parts":[{"functionResponse":{"name":"tool","response":{"text":"tool output"}}}]}]}`,
		},
		{
			name: "Gemini first omitted role is accepted user input", request: &GeminiChatRequest{}, want: "implicit first user input",
			body: `{"systemInstruction":{"parts":[{"text":"system instructions"}]},"contents":[{"parts":[{"text":"implicit first user input"}]},{"role":"model","parts":[{"text":"model output"}]}]}`,
		},
		{
			name: "Gemini omitted role does not reclassify tools or later contents", request: &GeminiChatRequest{},
			body: `{"contents":[{"parts":[{"functionResponse":{"name":"tool","response":{"text":"tool output"}}}]},{"parts":[{"text":"unknown later author"}]}]}`,
		},
		{
			name: "Gemini batch keeps only each current user turn", request: &GeminiChatRequest{}, want: "one current input\ntwo current input",
			body: `{"requests":[{"systemInstruction":{"parts":[{"text":"system instructions"}]},"contents":[{"role":"user","parts":[{"text":"old history"}]},{"role":"user","parts":[{"text":"one current input"}]}]},{"contents":[{"parts":[{"text":"two current input"},{"functionResponse":{"name":"tool","response":{"text":"tool output"}}}]}]}]}`,
		},
		{
			name: "image only prompt", request: &ImageRequest{}, want: "current image prompt",
			body: `{"prompt":" current image prompt ","image":"image bytes","mask":"mask bytes","extra_fields":{"instructions":"application metadata"}}`,
		},
		{
			name: "speech only input text", request: &AudioRequest{}, want: "current speech text",
			body: `{"input":" current speech text ","instructions":"speech style instructions","ref_text":"reference transcript","ref_audio":"reference audio bytes","metadata":{"text":"metadata"}}`,
		},
		{
			name: "embedding explicit current texts", request: &EmbeddingRequest{}, want: "one input\ntwo input",
			body: `{"input":[" one input ","two input"],"user":"account metadata"}`,
		},
		{
			name: "embedding unknown object not metadata", request: &EmbeddingRequest{},
			body: `{"input":{"text":"unknown input text","instructions":"system instructions"}}`,
		},
		{
			name: "Gemini embedding has only declared input text", request: &GeminiEmbeddingRequest{}, want: "current embedding text",
			body: `{"content":{"parts":[{"text":"current embedding text"},{"functionResponse":{"name":"tool","response":{"text":"tool output"}}}]},"title":"document metadata"}`,
		},
		{
			name: "Gemini embedding batch excludes model role", request: &GeminiBatchEmbeddingRequest{}, want: "one embedding\ntwo embedding",
			body: `{"requests":[{"content":{"parts":[{"text":"one embedding"}]},"title":"document metadata"},{"content":{"role":"user","parts":[{"text":"two embedding"}]}},{"content":{"role":"model","parts":[{"text":"model output"}]}}]}`,
		},
		{
			name: "rerank excludes retrieved documents", request: &RerankRequest{}, want: "current query",
			body: `{"query":" current query ","documents":["retrieved document",{"text":"tool result"}]}`,
		},
		{
			name: "System One context does not declare a user turn", request: &SystemOneRequest{},
			body: `{"model":"jev-latest","state":{"text":"application or tool context"},"questions":{"judge":{"type":"noul","instructions":"application instruction"}}}`,
		},
		{
			name: "compaction input is a historical snapshot", request: &OpenAIResponsesCompactionRequest{},
			body: `{"input":[{"role":"user","content":"historical user input"}],"instructions":"summarize instructions"}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.NoError(t, json.Unmarshal([]byte(test.body), test.request))
			require.Equal(t, test.want, ModerationTextForRequest(test.request))
		})
	}
}

type unknownModerationRequest struct{ BaseRequest }

func (*unknownModerationRequest) GetTokenCountMeta() *types.TokenCountMeta {
	panic("moderation must not inspect token-counting context")
}

func (*unknownModerationRequest) GetSecurityText() string {
	panic("moderation must not use the retired literal text provider")
}

func TestModerationTextDoesNotUseUnknownProvidersAndHandlesTypedNil(t *testing.T) {
	requests := []Request{
		nil, &unknownModerationRequest{}, &BaseRequest{},
		(*GeneralOpenAIRequest)(nil), (*OpenAIResponsesRequest)(nil), (*ClaudeRequest)(nil),
		(*GeminiChatRequest)(nil), (*ImageRequest)(nil), (*AudioRequest)(nil),
		(*EmbeddingRequest)(nil), (*GeminiEmbeddingRequest)(nil), (*GeminiBatchEmbeddingRequest)(nil), (*RerankRequest)(nil),
		&AlphaSearchRequest{RawBody: json.RawMessage(`{"query":"unknown search query","metadata":"private metadata"}`)},
	}
	for _, request := range requests {
		require.Empty(t, ModerationTextForRequest(request))
	}
	request := &GeneralOpenAIRequest{Prompt: []string{"one completion", "two completion"}, Prefix: "private prefix", Suffix: "private suffix", Instruction: "private instruction"}
	require.Equal(t, "one completion\ntwo completion", ModerationTextForRequest(request))
	request.Prompt = nil
	request.Input = []any{"one embedding", "two embedding"}
	require.Equal(t, "one embedding\ntwo embedding", ModerationTextForRequest(request))
	request.Input = []any{"input", map[string]any{"text": "metadata"}}
	require.Empty(t, ModerationTextForRequest(request))
}

func TestModerationTextFromRealtimeJSONOnlyClientUserMessages(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "new user message", want: "current realtime user\nmore text",
			body: `{"type":"conversation.item.create","instructions":"top-level instructions","session":{"instructions":"session instructions","tools":[{"description":"tool definitions"}]},"item":{"type":"message","role":"user","content":[{"type":"input_text","text":"current realtime user"},{"type":"text","text":"more text"},{"type":"input_audio","audio":"audio bytes","transcript":"generated transcript"},{"type":"input_image","text":"image metadata","image_url":"image URI"}],"tool_calls":[{"arguments":"tool arguments"}]}}`,
		},
		{
			name: "response explicit new user input", want: "current custom input",
			body: `{"type":"response.create","response":{"instructions":"response instructions","tools":[{"description":"tool definitions"}],"input":[{"type":"item_reference","id":"old-context"},{"type":"message","role":"user","content":[{"type":"input_text","text":"older user history"}]},{"type":"message","role":"user","content":[{"type":"input_text","text":"current custom input"}]},{"type":"message","role":"assistant","content":[{"type":"text","text":"model output"}]},{"type":"function_call_output","output":"tool output"}]}}`,
		},
		{name: "session settings", body: `{"type":"session.update","session":{"instructions":"system instructions","tools":[{"description":"tool definition"}]}}`},
		{name: "response instructions only", body: `{"type":"response.create","response":{"instructions":"application instructions","input":[]}}`},
		{name: "response audio input", body: `{"type":"response.create","response":{"input":[{"type":"message","role":"user","content":[{"type":"input_audio","audio":"audio bytes","transcript":"generated transcript"}]}]}}`},
		{name: "client audio bytes", body: `{"type":"input_audio_buffer.append","audio":"audio bytes","text":"not a declared text input"}`},
		{name: "assistant message", body: `{"type":"conversation.item.create","item":{"type":"message","role":"assistant","content":[{"type":"text","text":"model output"}]}}`},
		{name: "tool result", body: `{"type":"conversation.item.create","item":{"type":"function_call_output","role":"user","content":[{"type":"text","text":"tool output"}]}}`},
		{name: "missing user role", body: `{"type":"conversation.item.create","item":{"type":"message","content":[{"type":"input_text","text":"unknown author"}]}}`},
		{name: "missing message type", body: `{"type":"conversation.item.create","item":{"role":"user","content":[{"type":"input_text","text":"unknown shape"}]}}`},
		{name: "server item replay", body: `{"type":"conversation.item.created","item":{"type":"message","role":"user","content":[{"type":"input_text","text":"history replay"}]}}`},
		{name: "server output", body: `{"type":"response.output_text.delta","delta":"model output","text":"model output"}`},
		{name: "unknown event", body: `{"type":"custom","input":"unattributed input","metadata":{"text":"private metadata"}}`},
		{name: "malformed JSON", body: `{"type":`},
		{name: "multiple JSON objects", body: `{"type":"session.update"}{"type":"conversation.item.create"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, ModerationTextFromRealtimeJSON([]byte(test.body)))
		})
	}
}
