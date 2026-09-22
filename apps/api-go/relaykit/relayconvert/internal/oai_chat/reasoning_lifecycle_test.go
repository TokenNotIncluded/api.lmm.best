package oaichat

import (
	"fmt"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChatCompletionsStreamResumedReasoningUsesNewOutputItem(t *testing.T) {
	state := NewChatToResponsesStreamState("resp_reasoning", "gpt-test")
	feed := func(chunk *dto.ChatCompletionsStreamResponse) []ChatToResponsesStreamEvent {
		t.Helper()
		events, err := ChatCompletionsStreamChunkToResponsesEvents(chunk, state)
		require.NoError(t, err)
		return events
	}

	first := feed(&dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{
		Delta: dto.ChatCompletionsStreamResponseChoiceDelta{ReasoningContent: ptr("round 1")},
	}}})
	toolIndex := 0
	tool := feed(&dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{
		Delta: dto.ChatCompletionsStreamResponseChoiceDelta{ToolCalls: []dto.ToolCallResponse{{
			Index: &toolIndex,
			ID:    "call_test",
			Type:  "function",
			Function: dto.FunctionResponse{
				Name:      "example",
				Arguments: "{}",
			},
		}}},
	}}})
	finish := "tool_calls"
	closed := feed(&dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{
		FinishReason: &finish,
	}}})
	resumed := feed(&dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{
		Delta: dto.ChatCompletionsStreamResponseChoiceDelta{ReasoningContent: ptr("round 2")},
	}}})
	stop := "stop"
	feed(&dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{
		FinishReason: &stop,
	}}})
	final := FinalizeChatCompletionsStreamToResponses(state)

	allEvents := append(append(append(first, tool...), closed...), resumed...)
	closedIDs := make(map[string]bool)
	addedIDs := make(map[string]bool)
	var reasoningDeltaIDs []string
	for _, event := range allEvents {
		if event.Payload.Item != nil && event.Type == responsesEventOutputItemAdded && event.Payload.Item.Type == responsesOutputTypeReasoning {
			addedIDs[event.Payload.Item.ID] = true
		}
		if event.Type == responsesEventOutputItemDone && event.Payload.Item != nil && event.Payload.Item.Type == responsesOutputTypeReasoning {
			closedIDs[event.Payload.Item.ID] = true
		}
		if event.Type == responsesEventReasoningSummaryDelta {
			reasoningDeltaIDs = append(reasoningDeltaIDs, event.Payload.ItemID)
			assert.False(t, closedIDs[event.Payload.ItemID], "reasoning delta references closed item %s", event.Payload.ItemID)
			assert.True(t, addedIDs[event.Payload.ItemID], "reasoning delta must follow its item-added event")
		}
	}
	assert.Equal(t, []string{"resp_reasoning_reasoning_0", "resp_reasoning_reasoning_1"}, reasoningDeltaIDs)

	require.NotEmpty(t, final)
	completed := final[len(final)-1]
	require.Equal(t, responsesEventCompleted, completed.Type)
	require.NotNil(t, completed.Payload.Response)
	var reasoning []dto.ResponsesOutput
	for _, output := range completed.Payload.Response.Output {
		if output.Type == responsesOutputTypeReasoning {
			reasoning = append(reasoning, output)
		}
	}
	require.Len(t, reasoning, 2)
	assert.Equal(t, "resp_reasoning_reasoning_0", reasoning[0].ID)
	assert.Equal(t, "round 1", reasoning[0].Content[0].Text)
	assert.Equal(t, "resp_reasoning_reasoning_1", reasoning[1].ID)
	assert.Equal(t, "round 2", reasoning[1].Content[0].Text)
	assert.Empty(t, FinalizeChatCompletionsStreamToResponses(state))
}

func TestChatCompletionsStreamEmptyReasoningDoesNotCreateSegment(t *testing.T) {
	state := NewChatToResponsesStreamState("resp_empty", "gpt-test")
	feed := func(chunk *dto.ChatCompletionsStreamResponse) []ChatToResponsesStreamEvent {
		t.Helper()
		events, err := ChatCompletionsStreamChunkToResponsesEvents(chunk, state)
		require.NoError(t, err)
		return events
	}
	feed(&dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{
		Delta: dto.ChatCompletionsStreamResponseChoiceDelta{ReasoningContent: ptr("first")},
	}}})
	finish := "tool_calls"
	feed(&dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{FinishReason: &finish}}})
	empty := feed(&dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{
		Delta: dto.ChatCompletionsStreamResponseChoiceDelta{},
	}}})
	for _, event := range empty {
		assert.NotEqual(t, responsesEventOutputItemAdded, event.Type)
		assert.NotEqual(t, responsesEventReasoningSummaryDelta, event.Type)
	}
}

func TestChatCompletionsStreamKeepsThreeReasoningSegmentsOrdered(t *testing.T) {
	state := NewChatToResponsesStreamState("resp_three", "gpt-test")
	feed := func(chunk *dto.ChatCompletionsStreamResponse) {
		t.Helper()
		_, err := ChatCompletionsStreamChunkToResponsesEvents(chunk, state)
		require.NoError(t, err)
	}
	finish := func(reason string) {
		feed(&dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{
			FinishReason: &reason,
		}}})
	}
	for index, text := range []string{"one", "two", "three"} {
		feed(&dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{
			Delta: dto.ChatCompletionsStreamResponseChoiceDelta{ReasoningContent: ptr(text)},
		}}})
		if index < 2 {
			finish("tool_calls")
		}
	}
	finish("stop")
	events := FinalizeChatCompletionsStreamToResponses(state)
	require.NotEmpty(t, events)
	completed := events[len(events)-1]
	require.NotNil(t, completed.Payload.Response)
	var reasoning []dto.ResponsesOutput
	for _, output := range completed.Payload.Response.Output {
		if output.Type == responsesOutputTypeReasoning {
			reasoning = append(reasoning, output)
		}
	}
	require.Len(t, reasoning, 3)
	for index, expected := range []string{"one", "two", "three"} {
		assert.Equal(t, "resp_three_reasoning_"+fmt.Sprint(index), reasoning[index].ID)
		assert.Equal(t, expected, reasoning[index].Content[0].Text)
	}
}
