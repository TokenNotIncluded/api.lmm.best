package oaichat

import (
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChatCompletionsResponseToResponsesDropsUnnamedFunctionCalls(t *testing.T) {
	usage := dto.Usage{PromptTokens: 20, CompletionTokens: 5, TotalTokens: 25}
	usage.PromptTokensDetails.CachedTokens = 7
	usage.BillingUsage = dto.NewOpenAIChatBillingUsage(&usage)
	message := dto.Message{Role: "assistant"}
	message.SetToolCalls([]dto.ToolCallRequest{
		{
			ID:   "call_empty",
			Type: "function",
			Function: dto.FunctionRequest{
				Arguments: `{"ignored":true}`,
			},
		},
		{
			ID: "call_whitespace",
			Function: dto.FunctionRequest{
				Name:      " \t\n",
				Arguments: `{}`,
			},
		},
		{
			ID:   "call_valid",
			Type: "function",
			Function: dto.FunctionRequest{
				Name:      "lookup",
				Arguments: `{"q":"x"}`,
			},
		},
	})

	resp, convertedUsage, err := ChatCompletionsResponseToResponsesResponse(&dto.OpenAITextResponse{
		Usage: usage,
		Choices: []dto.OpenAITextResponseChoice{{
			Message:      message,
			FinishReason: "tool_calls",
		}},
	}, "resp_1")
	require.NoError(t, err)

	require.Len(t, resp.Output, 1)
	assert.Equal(t, "call_valid", resp.Output[0].CallId)
	assert.Equal(t, "lookup", resp.Output[0].Name)
	assert.Equal(t, `"{\"q\":\"x\"}"`, string(resp.Output[0].Arguments))
	assert.Equal(t, 20, convertedUsage.InputTokens)
	assert.Equal(t, 5, convertedUsage.OutputTokens)
	assert.Equal(t, 25, convertedUsage.TotalTokens)
	require.NotNil(t, convertedUsage.InputTokensDetails)
	assert.Equal(t, 7, convertedUsage.InputTokensDetails.CachedTokens)
	assert.Equal(t, usage.BillingUsage, convertedUsage.BillingUsage)
}

func TestChatCompletionsStreamToResponsesDropsToolsThatFinishUnnamed(t *testing.T) {
	state := NewChatToResponsesStreamState("resp_1", "gpt-test")
	invalidIndex := 0
	validIndex := 1

	var events []ChatToResponsesStreamEvent
	events = append(events, mustResponsesEventsFromChatChunk(t, state, &dto.ChatCompletionsStreamResponse{
		Choices: []dto.ChatCompletionsStreamResponseChoice{{
			Index: 0,
			Delta: dto.ChatCompletionsStreamResponseChoiceDelta{ToolCalls: []dto.ToolCallResponse{
				{
					Index: &invalidIndex,
					ID:    "call_invalid",
					Type:  "function",
					Function: dto.FunctionResponse{
						Name:      " \t",
						Arguments: `{"ignored":true}`,
					},
				},
				{
					Index: &validIndex,
					ID:    "call_valid",
					Type:  "function",
					Function: dto.FunctionResponse{
						Name:      "lookup",
						Arguments: `{"q":"x"}`,
					},
				},
			}},
		}},
	})...)

	finishReason := "tool_calls"
	events = append(events, mustResponsesEventsFromChatChunk(t, state, &dto.ChatCompletionsStreamResponse{
		Choices: []dto.ChatCompletionsStreamResponseChoice{{Index: 0, FinishReason: &finishReason}},
	})...)
	events = append(events, FinalizeChatCompletionsStreamToResponses(state)...)

	for _, event := range events {
		if event.Payload.Item != nil && event.Payload.Item.Type == responsesOutputTypeFunctionCall {
			assert.NotEmpty(t, event.Payload.Item.Name)
			assert.Equal(t, "lookup", event.Payload.Item.Name)
		}
		if event.Payload.OutputIndex != nil && (event.Payload.Item == nil || event.Payload.Item.Type == responsesOutputTypeFunctionCall) {
			assert.Equal(t, 0, *event.Payload.OutputIndex)
		}
	}

	completed := events[len(events)-1].Payload.Response
	require.NotNil(t, completed)
	require.Len(t, completed.Output, 1)
	assert.Equal(t, "call_valid", completed.Output[0].CallId)
	assert.Equal(t, "lookup", completed.Output[0].Name)
}

func TestChatCompletionsStreamToResponsesBuffersArgumentsUntilNameArrives(t *testing.T) {
	state := NewChatToResponsesStreamState("resp_1", "gpt-test")
	toolIndex := 0

	firstEvents := mustResponsesEventsFromChatChunk(t, state, &dto.ChatCompletionsStreamResponse{
		Choices: []dto.ChatCompletionsStreamResponseChoice{{
			Index: 0,
			Delta: dto.ChatCompletionsStreamResponseChoiceDelta{ToolCalls: []dto.ToolCallResponse{{
				Index: &toolIndex,
				Type:  "function",
				Function: dto.FunctionResponse{
					Arguments: `{"q":"x"}`,
				},
			}}},
		}},
	})
	require.Len(t, firstEvents, 1)
	assert.Equal(t, responsesEventCreated, firstEvents[0].Type)

	secondEvents := mustResponsesEventsFromChatChunk(t, state, &dto.ChatCompletionsStreamResponse{
		Choices: []dto.ChatCompletionsStreamResponseChoice{{
			Index: 0,
			Delta: dto.ChatCompletionsStreamResponseChoiceDelta{ToolCalls: []dto.ToolCallResponse{{
				Index: &toolIndex,
				ID:    "call_1",
				Function: dto.FunctionResponse{
					Name: "lookup",
				},
			}}},
		}},
	})
	require.Len(t, secondEvents, 2)
	assert.Equal(t, responsesEventOutputItemAdded, secondEvents[0].Type)
	require.NotNil(t, secondEvents[0].Payload.Item)
	assert.Equal(t, "lookup", secondEvents[0].Payload.Item.Name)
	require.NotNil(t, secondEvents[0].Payload.OutputIndex)
	assert.Equal(t, 0, *secondEvents[0].Payload.OutputIndex)
	assert.Equal(t, responsesEventFunctionArgsDelta, secondEvents[1].Type)
	assert.Equal(t, `{"q":"x"}`, secondEvents[1].Payload.Delta)

	finalEvents := FinalizeChatCompletionsStreamToResponses(state)
	completed := finalEvents[len(finalEvents)-1].Payload.Response
	require.NotNil(t, completed)
	require.Len(t, completed.Output, 1)
	assert.Equal(t, "call_1", completed.Output[0].CallId)
	assert.Equal(t, "lookup", completed.Output[0].Name)
}
