package oaichat

import (
	"encoding/json"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/stretchr/testify/require"
)

func feedMessageLifecycleChunk(t *testing.T, state *ChatToResponsesStreamState, raw string) []ChatToResponsesStreamEvent {
	t.Helper()
	var chunk dto.ChatCompletionsStreamResponse
	require.NoError(t, json.Unmarshal([]byte(raw), &chunk))
	events, err := ChatCompletionsStreamChunkToResponsesEvents(&chunk, state)
	require.NoError(t, err)
	return events
}

func TestChatCompletionsStreamResumedMessagesAndReasoningStayIndependent(t *testing.T) {
	state := NewChatToResponsesStreamState("resp_mixed", "gpt-test")
	var events []ChatToResponsesStreamEvent
	for _, raw := range []string{
		`{"choices":[{"delta":{"reasoning_content":"think one"}}]}`,
		`{"choices":[{"delta":{"content":"answer one"}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_lookup","type":"function","function":{"name":"lookup","arguments":"{}"}}]}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`{"choices":[{"delta":{"reasoning_content":"think two"}}]}`,
		`{"choices":[{"delta":{"content":"answer two"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		`{"choices":[],"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18}}`,
	} {
		events = append(events, feedMessageLifecycleChunk(t, state, raw)...)
	}
	usage := state.Usage
	final := FinalizeChatCompletionsStreamToResponses(state)
	require.NotEmpty(t, final)
	events = append(events, final...)
	response := final[len(final)-1].Payload.Response
	require.NotNil(t, response)
	wantIDs := []string{"resp_mixed_reasoning_0", "resp_mixed_msg_0", "call_lookup", "resp_mixed_reasoning_1", "resp_mixed_msg_1"}
	require.Len(t, response.Output, len(wantIDs))
	added := make(map[string]int)
	closed := make(map[string]dto.ResponsesOutput)
	for _, event := range events {
		switch event.Type {
		case responsesEventOutputItemAdded:
			require.NotNil(t, event.Payload.Item)
			require.NotNil(t, event.Payload.OutputIndex)
			id := event.Payload.Item.ID
			require.NotContains(t, added, id, "never reopen a closed item ID")
			require.Equal(t, len(added), *event.Payload.OutputIndex, "added items retain output order")
			added[id] = *event.Payload.OutputIndex
		case responsesEventOutputTextDelta, responsesEventReasoningSummaryDelta:
			id := event.Payload.ItemID
			require.Contains(t, added, id, "delta must follow item-added")
			require.NotContains(t, closed, id, "delta must not target a done item")
			require.NotNil(t, event.Payload.OutputIndex)
			require.Equal(t, added[id], *event.Payload.OutputIndex)
		case responsesEventOutputItemDone:
			require.NotNil(t, event.Payload.Item)
			id := event.Payload.Item.ID
			require.Contains(t, added, id)
			require.NotContains(t, closed, id, "each item closes exactly once")
			closed[id] = *event.Payload.Item
		}
	}
	for index, id := range wantIDs {
		require.Equal(t, id, response.Output[index].ID)
		require.Equal(t, closed[id], response.Output[index], "closed items stay immutable in final output")
	}
	require.Equal(t, "think one", response.Output[0].Content[0].Text)
	require.Equal(t, "answer one", response.Output[1].Content[0].Text)
	require.Equal(t, "think two", response.Output[3].Content[0].Text)
	require.Equal(t, "answer two", response.Output[4].Content[0].Text)
	require.Equal(t, "answer oneanswer two", state.UsageText(), "usage accounting retains all ordinary text")
	require.Same(t, usage, response.Usage)
	require.Empty(t, FinalizeChatCompletionsStreamToResponses(state))
	require.Empty(t, feedMessageLifecycleChunk(t, state, `{"choices":[{"delta":{"content":"ignored after finalization"}}]}`))
}

func TestChatCompletionsStreamClosedMessagesKeepTheirClosingStatus(t *testing.T) {
	for _, finishes := range [][2]string{{"tool_calls", "length"}, {"length", "stop"}} {
		t.Run(finishes[0]+"_then_"+finishes[1], func(t *testing.T) {
			state := NewChatToResponsesStreamState("resp_message_status", "gpt-test")
			var closed []dto.ResponsesOutput
			for _, finish := range finishes {
				feedMessageLifecycleChunk(t, state, `{"choices":[{"delta":{"content":"segment"}}]}`)
				events := feedMessageLifecycleChunk(t, state, `{"choices":[{"delta":{},"finish_reason":"`+finish+`"}]}`)
				for _, event := range events {
					if event.Type == responsesEventOutputItemDone && event.Payload.Item.Type == responsesOutputTypeMessage {
						closed = append(closed, *event.Payload.Item)
					}
				}
			}
			final := FinalizeChatCompletionsStreamToResponses(state)
			require.Len(t, closed, 2)
			require.NotEmpty(t, final)
			require.Equal(t, closed, final[len(final)-1].Payload.Response.Output)
			require.Equal(t, "segmentsegment", state.UsageText())
		})
	}
}

func TestChatCompletionsStreamEmptyTextDoesNotOpenAnotherMessage(t *testing.T) {
	state := NewChatToResponsesStreamState("resp_empty_text", "gpt-test")
	require.Empty(t, state.appendTextDelta(""))
	feedMessageLifecycleChunk(t, state, `{"choices":[{"delta":{"content":"only"},"finish_reason":"tool_calls"}]}`)
	require.Empty(t, state.appendTextDelta(""))
	feedMessageLifecycleChunk(t, state, `{"choices":[{"delta":{},"finish_reason":"stop"}]}`)
	final := FinalizeChatCompletionsStreamToResponses(state)
	require.NotEmpty(t, final)
	output := final[len(final)-1].Payload.Response.Output
	require.Len(t, output, 1)
	require.Equal(t, "resp_empty_text_msg_0", output[0].ID)
	require.Equal(t, "only", output[0].Content[0].Text)
}
