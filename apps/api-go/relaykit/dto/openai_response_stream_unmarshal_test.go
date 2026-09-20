package dto

import (
	"testing"

	kitutil "github.com/LIghtJUNction/api.lmm.best/relaykit/relayconvert/kitutil"
)

func TestChatCompletionsStreamResponseChoicesNullOrEmptyObject(t *testing.T) {
	for _, raw := range []string{
		`{"id":"cmpl-1","object":"chat.completion.chunk","created":1710000000,"model":"vllm","choices":null,"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`,
		`{"id":"cmpl-1","object":"chat.completion.chunk","created":1710000000,"model":"vllm","choices":{},"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`,
	} {
		var resp ChatCompletionsStreamResponse
		if err := kitutil.Unmarshal([]byte(raw), &resp); err != nil {
			t.Fatalf("unexpected decode error: %v", err)
		}
		if len(resp.Choices) != 0 {
			t.Fatalf("choices length = %d, want 0", len(resp.Choices))
		}
		if resp.IsFinished() {
			t.Fatal("empty choices must not mark the stream as finished")
		}
		if resp.Usage == nil || resp.Usage.PromptTokens != 3 || resp.Usage.CompletionTokens != 1 {
			t.Fatalf("usage was not preserved: %#v", resp.Usage)
		}
	}
}

func TestChatCompletionsStreamResponseChoicesArrayStillUnmarshals(t *testing.T) {
	raw := `{"id":"cmpl-1","object":"chat.completion.chunk","created":1710000000,"model":"vllm","choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":null}]}`
	var resp ChatCompletionsStreamResponse
	if err := kitutil.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("unexpected decode error: %v", err)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("choices length = %d, want 1", len(resp.Choices))
	}
	if got := resp.Choices[0].Delta.GetContentString(); got != "hi" {
		t.Fatalf("content = %q, want hi", got)
	}
}

func TestChatCompletionsStreamResponseRejectsNonEmptyObjectChoices(t *testing.T) {
	var resp ChatCompletionsStreamResponse
	if err := kitutil.Unmarshal([]byte(`{"choices":{"index":0}}`), &resp); err == nil {
		t.Fatal("non-empty object choices must remain invalid")
	}
}
