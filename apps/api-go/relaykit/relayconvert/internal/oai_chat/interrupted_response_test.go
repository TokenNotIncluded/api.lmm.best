package oaichat

import (
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/stretchr/testify/require"
)

func TestFailedChatResponsePreservesClosedAndActiveItems(t *testing.T) {
	state := NewChatToResponsesStreamState("resp_failure", "gpt-test")
	feed := func(delta dto.ChatCompletionsStreamResponseChoiceDelta, finish *string) []ChatToResponsesStreamEvent {
		events, err := ChatCompletionsStreamChunkToResponsesEvents(&dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{Delta: delta, FinishReason: finish}}}, state)
		require.NoError(t, err)
		return events
	}
	feed(dto.ChatCompletionsStreamResponseChoiceDelta{ReasoningContent: ptr("closed")}, nil)
	events := feed(dto.ChatCompletionsStreamResponseChoiceDelta{}, ptr("length"))
	var closed *dto.ResponsesOutput
	for _, event := range events {
		if event.Type == responsesEventOutputItemDone {
			closed = event.Payload.Item
		}
	}
	require.NotNil(t, closed)
	feed(dto.ChatCompletionsStreamResponseChoiceDelta{ReasoningContent: ptr("active")}, nil)
	state.Usage = &dto.Usage{TotalTokens: 42}
	failed, err := state.Fail("upstream_stream_interrupted", "safe message")
	require.NoError(t, err)
	require.Equal(t, `"failed"`, string(failed.Status))
	require.Len(t, failed.Output, 2)
	require.Equal(t, *closed, failed.Output[0])
	require.Equal(t, "resp_failure_reasoning_1", failed.Output[1].ID)
	require.Equal(t, "active", failed.Output[1].Content[0].Text)
	require.Equal(t, "in_progress", failed.Output[1].Status)
	require.Equal(t, 42, failed.Usage.TotalTokens)
	require.Empty(t, FinalizeChatCompletionsStreamToResponses(state))
	again, err := state.Fail("duplicate", "duplicate")
	require.NoError(t, err)
	require.Nil(t, again)
}

func TestFailedChatResponseDoesNotPublishBufferedToolIdentity(t *testing.T) {
	state := NewChatToResponsesStreamState("resp_buffered", "gpt-test")
	state.ToolMapping = responsesToolMappingForTest()
	index := 0
	_, err := ChatCompletionsStreamChunkToResponsesEvents(&dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{Delta: dto.ChatCompletionsStreamResponseChoiceDelta{ToolCalls: []dto.ToolCallResponse{{Index: &index, Function: dto.FunctionResponse{Name: "compat_search", Arguments: "{"}}}}}}}, state)
	require.NoError(t, err)
	_, err = ChatCompletionsStreamChunkToResponsesEvents(&dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{Delta: dto.ChatCompletionsStreamResponseChoiceDelta{ReasoningContent: ptr("not yet delivered")}}}}, state)
	require.NoError(t, err)
	failed, err := state.Fail("upstream_stream_interrupted", "safe message")
	require.NoError(t, err)
	require.Empty(t, failed.Output, "blocked items must not appear without added events")
	require.Empty(t, FinalizeChatCompletionsStreamToResponses(state))
}

func TestFailedChatResponseOmitsUnreturnedChunkAfterConversionError(t *testing.T) {
	state := NewChatToResponsesStreamState("resp_error", "gpt-test")
	state.ToolMapping = responsesToolMappingForTest()
	_, err := ChatCompletionsStreamChunkToResponsesEvents(&dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{Delta: dto.ChatCompletionsStreamResponseChoiceDelta{Content: ptr("seen")}}}}, state)
	require.NoError(t, err)
	index := 0
	_, err = ChatCompletionsStreamChunkToResponsesEvents(&dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{Delta: dto.ChatCompletionsStreamResponseChoiceDelta{Content: ptr("unreturned"), ToolCalls: []dto.ToolCallResponse{{Index: &index, Function: dto.FunctionResponse{Name: "compat_search", Arguments: "{"}}}}, FinishReason: ptr("stop")}}}, state)
	require.Error(t, err)
	failed, err := state.Fail("upstream_stream_interrupted", "safe message")
	require.NoError(t, err)
	require.Len(t, failed.Output, 1)
	require.Equal(t, "seen", failed.Output[0].Content[0].Text)
	require.Equal(t, "in_progress", failed.Output[0].Status)
}

func TestFailedChatResponseRejectsToolMutationWithoutLeakingSameChunk(t *testing.T) {
	state := NewChatToResponsesStreamState("resp_mutation", "gpt-test")
	index := 0
	feed := func(delta dto.ChatCompletionsStreamResponseChoiceDelta) error {
		_, err := ChatCompletionsStreamChunkToResponsesEvents(&dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{Delta: delta}}}, state)
		return err
	}
	require.NoError(t, feed(dto.ChatCompletionsStreamResponseChoiceDelta{ReasoningContent: ptr("seen"), ToolCalls: []dto.ToolCallResponse{{Index: &index, ID: "call_original", Function: dto.FunctionResponse{Name: "lookup", Arguments: "{"}}}}))
	require.Error(t, feed(dto.ChatCompletionsStreamResponseChoiceDelta{ReasoningContent: ptr("unreturned"), ToolCalls: []dto.ToolCallResponse{{Index: &index, ID: "call_changed", Function: dto.FunctionResponse{Arguments: "not delivered"}}}}))
	failed, err := state.Fail("upstream_stream_interrupted", "safe message")
	require.NoError(t, err)
	require.Len(t, failed.Output, 2)
	require.Equal(t, "seen", failed.Output[0].Content[0].Text)
	require.Equal(t, "in_progress", failed.Output[0].Status)
	require.Equal(t, "call_original", failed.Output[1].ID)
	require.Equal(t, string(chatArgumentsRawMessage("{")), string(failed.Output[1].Arguments))
}

func TestFailedChatResponseDoesNotCloseUnreturnedReasoningDone(t *testing.T) {
	state := NewChatToResponsesStreamState("resp_done_error", "gpt-test")
	index := 0
	_, err := ChatCompletionsStreamChunkToResponsesEvents(&dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{Delta: dto.ChatCompletionsStreamResponseChoiceDelta{ReasoningContent: ptr("seen"), ToolCalls: []dto.ToolCallResponse{{Index: &index, ID: "call_original", Function: dto.FunctionResponse{Name: "lookup", Arguments: "{}"}}}}}}}, state)
	require.NoError(t, err)
	_, err = ChatCompletionsStreamChunkToResponsesEvents(&dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{
		{FinishReason: ptr("stop")},
		{Index: 1, Delta: dto.ChatCompletionsStreamResponseChoiceDelta{ToolCalls: []dto.ToolCallResponse{{Index: &index, ID: "call_changed"}}}},
	}}, state)
	require.Error(t, err)
	failed, err := state.Fail("upstream_stream_interrupted", "safe message")
	require.NoError(t, err)
	require.Len(t, failed.Output, 2)
	require.Equal(t, "in_progress", failed.Output[0].Status)
	require.Equal(t, "in_progress", failed.Output[1].Status)
}
