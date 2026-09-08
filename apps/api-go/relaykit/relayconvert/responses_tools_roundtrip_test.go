package relayconvert

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/relayconvert/convmeta"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/stretchr/testify/require"
)

func TestResponsesNamespaceRoundTripThroughRegistry(t *testing.T) {
	request := &dto.OpenAIResponsesRequest{
		Model: "test-model", Input: json.RawMessage(`"Look up SKU A1"`),
		Tools: json.RawMessage(`[
			{"type":"namespace","name":"inventory","tools":[{"type":"function","name":"lookup","strict":true,"defer_loading":true,"parameters":{"type":"object","properties":{"sku":{"type":"string"}}}}]},
			{"type":"namespace","name":"orders","tools":[{"type":"function","name":"lookup","parameters":{"type":"object","properties":{}}}]},
			{"type":"tool_search"}
		]`),
	}
	before, err := json.Marshal(request)
	require.NoError(t, err)
	meta := &convmeta.Values{}
	converted, err := ConvertRequest(context.Background(), meta, types.RelayFormatOpenAI, request)
	require.NoError(t, err)
	chat := converted.Value.(*dto.GeneralOpenAIRequest)
	require.Len(t, chat.Tools, 2, "server search eagerly exposes both namespace functions")
	require.Equal(t, "function", chat.Tools[0].Type)
	require.NotNil(t, chat.Tools[0].Function.Strict)
	require.True(t, *chat.Tools[0].Function.Strict)
	require.NotEqual(t, chat.Tools[0].Function.Name, chat.Tools[1].Function.Name)
	after, err := json.Marshal(request)
	require.NoError(t, err)
	require.JSONEq(t, string(before), string(after), "conversion must not mutate the original request used by retries")

	call := dto.ToolCallRequest{ID: "call_lookup", Type: "function", Function: dto.FunctionRequest{Name: chat.Tools[0].Function.Name, Arguments: `{"sku":"A1"}`}}
	calls, err := json.Marshal([]dto.ToolCallRequest{call})
	require.NoError(t, err)
	response, err := ConvertResponse(context.Background(), meta, types.RelayFormatOpenAIResponses, &dto.OpenAITextResponse{
		Id: "resp_lookup", Model: request.Model,
		Choices: []dto.OpenAITextResponseChoice{{Message: dto.Message{Role: "assistant", ToolCalls: calls}, FinishReason: "tool_calls"}},
		Usage:   dto.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
	})
	require.NoError(t, err)
	output := response.Value.(*dto.OpenAIResponsesResponse)
	require.Len(t, output.Output, 1)
	wire, err := json.Marshal(output.Output[0])
	require.NoError(t, err)
	var item map[string]any
	require.NoError(t, json.Unmarshal(wire, &item))
	require.Equal(t, "inventory", item["namespace"])
	require.Equal(t, "lookup", item["name"])
	require.Equal(t, "call_lookup", item["call_id"])
	require.Equal(t, `{"sku":"A1"}`, item["arguments"])
	require.Equal(t, 15, response.Usage.TotalTokens)

	// The next request must translate the restored identity to the same alias.
	request.Input = append(json.RawMessage{'['}, append(wire, ']')...)
	next, err := ConvertRequest(context.Background(), meta, types.RelayFormatOpenAI, request)
	require.NoError(t, err)
	require.Equal(t, call.Function.Name, next.Value.(*dto.GeneralOpenAIRequest).Messages[0].ParseToolCalls()[0].Function.Name)
}

func TestResponsesClientSearchStreamThroughRegistry(t *testing.T) {
	meta := &convmeta.Values{}
	converted, err := ConvertRequest(context.Background(), meta, types.RelayFormatOpenAI, &dto.OpenAIResponsesRequest{
		Model: "test-model", Input: json.RawMessage(`"Find inventory tools"`),
		Tools: json.RawMessage(`[{"type":"tool_search","execution":"client","parameters":{"type":"object","properties":{"goal":{"type":"string"}},"required":["goal"]}}]`),
	})
	require.NoError(t, err)
	chat := converted.Value.(*dto.GeneralOpenAIRequest)
	require.Len(t, chat.Tools, 1)
	require.Equal(t, "function", chat.Tools[0].Type)
	state, err := NewResponseStreamState(types.RelayFormatOpenAI, types.RelayFormatOpenAIResponses, ResponseStreamOptions{ID: "resp_search", Model: "test-model"})
	require.NoError(t, err)
	index := 0
	finish := "tool_calls"
	var events []ResponseResult
	for _, chunk := range []*dto.ChatCompletionsStreamResponse{
		{Choices: []dto.ChatCompletionsStreamResponseChoice{{Delta: dto.ChatCompletionsStreamResponseChoiceDelta{ToolCalls: []dto.ToolCallResponse{{Index: &index, ID: "call_search", Type: "function", Function: dto.FunctionResponse{Name: chat.Tools[0].Function.Name, Arguments: `{"goal":`}}}}}}},
		{Choices: []dto.ChatCompletionsStreamResponseChoice{{Delta: dto.ChatCompletionsStreamResponseChoiceDelta{ToolCalls: []dto.ToolCallResponse{{Index: &index, Function: dto.FunctionResponse{Arguments: `"inventory"}`}}}}, FinishReason: &finish}}},
	} {
		batch, err := ConvertStreamResponseChunk(context.Background(), meta, state, chunk)
		require.NoError(t, err)
		events = append(events, batch...)
	}
	final, err := FinalizeStreamResponse(context.Background(), meta, state)
	require.NoError(t, err)
	events = append(events, final...)
	seenDone, seenCompleted := false, false
	for _, result := range events {
		event := result.Value.(ChatToResponsesStreamEvent)
		require.NotContains(t, event.Type, "function_call_arguments", "search must not leak synthetic function events")
		if event.Type == "response.output_item.done" {
			seenDone = true
			require.Equal(t, "tool_search_call", event.Payload.Item.Type)
			require.Equal(t, "call_search", event.Payload.Item.CallId)
			require.JSONEq(t, `{"goal":"inventory"}`, string(event.Payload.Item.Arguments))
		}
		if event.Type == "response.completed" {
			seenCompleted = true
			require.Equal(t, "tool_search_call", event.Payload.Response.Output[0].Type)
			require.JSONEq(t, `{"goal":"inventory"}`, string(event.Payload.Response.Output[0].Arguments))
		}
	}
	require.True(t, seenDone)
	require.True(t, seenCompleted)
}

func TestResponsesToolMetadataClearedForNextAttempt(t *testing.T) {
	meta := &convmeta.Values{}
	_, err := ConvertRequest(context.Background(), meta, types.RelayFormatOpenAI, &dto.OpenAIResponsesRequest{
		Model: "test", Input: json.RawMessage(`"hi"`), Tools: json.RawMessage(`[{"type":"tool_search","execution":"client"}]`),
	})
	require.NoError(t, err)
	require.NotEmpty(t, meta.ConvOptions().ResponsesTools)
	_, err = ConvertRequest(context.Background(), meta, types.RelayFormatOpenAI, &dto.OpenAIResponsesRequest{Model: "test", Input: json.RawMessage(`"hi"`)})
	require.NoError(t, err)
	require.Empty(t, meta.ConvOptions().ResponsesTools)
}

func TestResponsesDynamicToolsReachGeminiRegistry(t *testing.T) {
	meta := &convmeta.Values{ChannelMetaAttached: true, UpstreamModelName: "gemini-test"}
	converted, err := ConvertRequest(context.Background(), meta, types.RelayFormatGemini, &dto.OpenAIResponsesRequest{
		Model: "gemini-test",
		Tools: json.RawMessage(`[{"type":"tool_search","execution":"client"}]`),
		Input: json.RawMessage(`[
			{"role":"user","content":"Find inventory tools"},
			{"type":"tool_search_call","execution":"client","call_id":"search1","arguments":{"goal":"inventory"}},
			{"type":"tool_search_output","execution":"client","call_id":"search1","tools":[{"type":"namespace","name":"inventory","tools":[{"type":"function","name":"lookup","parameters":{"type":"object","properties":{"sku":{"type":"string"}}}}]}]}
		]`),
	})
	require.NoError(t, err)
	wire, err := json.Marshal(converted.Value)
	require.NoError(t, err)
	var alias string
	for name, identity := range meta.ConvOptions().ResponsesTools {
		if identity.Namespace == "inventory" {
			alias = name
		}
	}
	require.NotEmpty(t, alias)
	require.Contains(t, string(wire), alias)
	require.NotContains(t, string(wire), `"type":"tool_search"`)

	result, err := ConvertResponse(context.Background(), meta, types.RelayFormatOpenAIResponses, &dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{{Content: dto.GeminiChatContent{Role: "model", Parts: []dto.GeminiPart{
			{FunctionCall: &dto.FunctionCall{FunctionName: alias, Arguments: map[string]interface{}{"sku": "A1"}}},
		}}}},
	})
	require.NoError(t, err)
	output := result.Value.(*dto.OpenAIResponsesResponse).Output
	require.Len(t, output, 1)
	require.Equal(t, "inventory", output[0].Namespace)
	require.Equal(t, "lookup", output[0].Name)
	require.JSONEq(t, `{"sku":"A1"}`, output[0].ArgumentsString())
}
