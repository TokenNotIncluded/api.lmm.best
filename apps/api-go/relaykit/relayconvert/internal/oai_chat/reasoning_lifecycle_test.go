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
	stopped := feed(&dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{
		FinishReason: &stop,
	}}})
	final := FinalizeChatCompletionsStreamToResponses(state)

	allEvents := append(append(append(first, tool...), closed...), resumed...)
	allEvents = append(allEvents, stopped...)
	allEvents = append(allEvents, final...)
	closedIDs := make(map[string]bool)
	addedIDs := make(map[string]bool)
	var reasoningDeltaIDs []string
	for _, event := range allEvents {
		if event.Payload.Item != nil && event.Type == responsesEventOutputItemAdded && event.Payload.Item.Type == responsesOutputTypeReasoning {
			addedIDs[event.Payload.Item.ID] = true
		}
		if event.Type == responsesEventOutputItemDone && event.Payload.Item != nil && event.Payload.Item.Type == responsesOutputTypeReasoning {
			assert.False(t, closedIDs[event.Payload.Item.ID], "item must close once")
			closedIDs[event.Payload.Item.ID] = true
		}
		if event.Type == responsesEventReasoningSummaryDelta {
			reasoningDeltaIDs = append(reasoningDeltaIDs, event.Payload.ItemID)
			assert.False(t, closedIDs[event.Payload.ItemID], "reasoning delta references closed item %s", event.Payload.ItemID)
			assert.True(t, addedIDs[event.Payload.ItemID], "reasoning delta must follow its item-added event")
		}
	}
	assert.Equal(t, []string{"resp_reasoning_reasoning_0", "resp_reasoning_reasoning_1"}, reasoningDeltaIDs)
	assert.Len(t, closedIDs, 2)

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

func TestChatCompletionsStreamResumedReasoningWithDelayedToolNames(t *testing.T) {
	for _, name := range []string{"", "lookup", "ns_lookup"} {
		t.Run("name="+name, func(t *testing.T) {
			state := NewChatToResponsesStreamState("resp_tools", "gpt-test")
			state.ToolMapping = responsesToolMappingForTest()
			var events []ChatToResponsesStreamEvent
			feed := func(delta dto.ChatCompletionsStreamResponseChoiceDelta, finish *string) {
				got, err := ChatCompletionsStreamChunkToResponsesEvents(&dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{Delta: delta, FinishReason: finish}}}, state)
				require.NoError(t, err)
				events = append(events, got...)
			}
			index := 0
			feed(dto.ChatCompletionsStreamResponseChoiceDelta{ToolCalls: []dto.ToolCallResponse{{Index: &index, ID: "call_late", Function: dto.FunctionResponse{Arguments: `{"id":"7"}`}}}}, nil)
			feed(dto.ChatCompletionsStreamResponseChoiceDelta{ReasoningContent: ptr("before")}, nil)
			if name != "" {
				feed(dto.ChatCompletionsStreamResponseChoiceDelta{ToolCalls: []dto.ToolCallResponse{{Index: &index, Function: dto.FunctionResponse{Name: name}}}}, nil)
			}
			feed(dto.ChatCompletionsStreamResponseChoiceDelta{}, ptr("tool_calls"))
			feed(dto.ChatCompletionsStreamResponseChoiceDelta{ReasoningContent: ptr("after")}, nil)
			feed(dto.ChatCompletionsStreamResponseChoiceDelta{}, ptr("stop"))
			events = append(events, FinalizeChatCompletionsStreamToResponses(state)...)
			output := events[len(events)-1].Payload.Response.Output
			expectedCount := 3
			if name == "" {
				expectedCount = 2
			}
			require.Len(t, output, expectedCount)
			require.Equal(t, "before", output[0].Content[0].Text)
			require.Equal(t, "after", output[len(output)-1].Content[0].Text)
			for _, event := range events {
				if event.Payload.OutputIndex == nil {
					continue
				}
				index := *event.Payload.OutputIndex
				require.GreaterOrEqual(t, index, 0)
				require.Less(t, index, len(output))
				if event.Payload.Item != nil {
					require.Equal(t, output[index].ID, event.Payload.Item.ID)
				}
				if event.Payload.ItemID != "" {
					require.Equal(t, output[index].ID, event.Payload.ItemID)
				}
			}
			if name == "ns_lookup" {
				require.Equal(t, "lookup", output[1].Name)
				require.Equal(t, "crm", output[1].Namespace)
			}
		})
	}
}

func TestChatCompletionsStreamClosedReasoningKeepsStatus(t *testing.T) {
	for _, finishes := range [][2]string{{"tool_calls", "length"}, {"length", "stop"}} {
		t.Run(finishes[0]+"_then_"+finishes[1], func(t *testing.T) {
			state := NewChatToResponsesStreamState("resp_status", "gpt-test")
			var closed []dto.ResponsesOutput
			for index, finish := range finishes {
				_, err := ChatCompletionsStreamChunkToResponsesEvents(&dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{Delta: dto.ChatCompletionsStreamResponseChoiceDelta{ReasoningContent: ptr(fmt.Sprint(index))}}}}, state)
				require.NoError(t, err)
				events, err := ChatCompletionsStreamChunkToResponsesEvents(&dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{FinishReason: &finish}}}, state)
				require.NoError(t, err)
				for _, event := range events {
					if event.Type == responsesEventOutputItemDone && event.Payload.Item.Type == responsesOutputTypeReasoning {
						closed = append(closed, *event.Payload.Item)
					}
				}
			}
			final := FinalizeChatCompletionsStreamToResponses(state)
			require.NotEmpty(t, final)
			require.Len(t, closed, 2)
			assert.Equal(t, closed, final[len(final)-1].Payload.Response.Output)
			assert.Empty(t, FinalizeChatCompletionsStreamToResponses(state))
		})
	}
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
