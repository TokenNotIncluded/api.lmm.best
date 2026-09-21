package oaichat

import (
	"encoding/json"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/relayconvert/convmeta"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func responsesToolMappingForTest() convmeta.ResponsesToolMap {
	return convmeta.ResponsesToolMap{
		"compat_search": {Name: "tool_search", ToolSearch: true},
		"ns_lookup":     {Name: "lookup", Namespace: "crm"},
	}
}

func TestRestoreResponsesToolOutputJSON(t *testing.T) {
	resp := &dto.OpenAIResponsesResponse{Output: []dto.ResponsesOutput{
		{Type: "function_call", ID: "search_item", CallId: "search_call", Name: "compat_search", Arguments: chatArgumentsRawMessage(`{"query":"customer data","limit":2}`)},
		{Type: "function_call", ID: "lookup_item", CallId: "lookup_call", Name: "ns_lookup", Arguments: chatArgumentsRawMessage(`{"id":"7"}`)},
		{Type: "function_call", ID: "plain_item", CallId: "plain_call", Name: "plain", Arguments: chatArgumentsRawMessage(`{"x":1}`)},
	}}
	require.NoError(t, RestoreResponsesToolOutput(resp, responsesToolMappingForTest()))
	encoded, err := json.Marshal(resp)
	require.NoError(t, err)
	var wire struct{ Output []map[string]any }
	require.NoError(t, json.Unmarshal(encoded, &wire))
	assert.Equal(t, "tool_search_call", wire.Output[0]["type"])
	assert.Equal(t, "client", wire.Output[0]["execution"])
	assert.Equal(t, "search_call", wire.Output[0]["call_id"])
	assert.Equal(t, map[string]any{"query": "customer data", "limit": float64(2)}, wire.Output[0]["arguments"])
	assert.NotContains(t, wire.Output[0], "name")
	assert.NotContains(t, wire.Output[0], "namespace")
	assert.Equal(t, "lookup", wire.Output[1]["name"])
	assert.Equal(t, "crm", wire.Output[1]["namespace"])
	assert.Equal(t, `{"id":"7"}`, wire.Output[1]["arguments"])
	assert.Equal(t, "plain", wire.Output[2]["name"])
	assert.Equal(t, `{"x":1}`, wire.Output[2]["arguments"])
}

func TestRestoreResponsesToolOutputRejectsInvalidSearchArgumentsAtomically(t *testing.T) {
	for _, arguments := range []string{"", "null", "[]", "42", `"query"`, `{"query":`, `{} {}`} {
		t.Run(arguments, func(t *testing.T) {
			resp := &dto.OpenAIResponsesResponse{Output: []dto.ResponsesOutput{
				{Type: "function_call", Name: "ns_lookup", Arguments: chatArgumentsRawMessage(`{}`)},
				{Type: "function_call", CallId: "call_search", Name: "compat_search", Arguments: chatArgumentsRawMessage(arguments)},
			}}
			err := RestoreResponsesToolOutput(resp, responsesToolMappingForTest())
			require.ErrorContains(t, err, "valid JSON object")
			assert.Equal(t, "ns_lookup", resp.Output[0].Name)
			assert.Equal(t, "function_call", resp.Output[1].Type)
		})
	}
}

func TestResponsesToolStreamRestoresFragmentedNamesAndArguments(t *testing.T) {
	state := NewChatToResponsesStreamState("resp_tools", "test")
	state.ToolMapping = responsesToolMappingForTest()
	// These are wire-format Chat SSE data chunks, including an empty initial
	// function name and name/argument fragments split across several chunks.
	chunks := []string{
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_search","type":"function","function":{"name":"","arguments":""}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"name":"compat_","arguments":"{\"query\":"}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":1,"id":"call_lookup","type":"function","function":{"name":"ns_"}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":2,"id":"call_plain","type":"function","function":{"name":"plain","arguments":"{\"x\":"}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"name":"search","arguments":"\"customer"}},{"index":1,"function":{"name":"lookup","arguments":"{\"id\":"}},{"index":2,"function":{"arguments":"1}"}}]}}]}`,
		`{"choices":[{"delta":{"content":"Working.","tool_calls":[{"index":0,"function":{"arguments":" data\"}"}},{"index":1,"function":{"arguments":"\"7\"}"}}]}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
	}
	var events []ChatToResponsesStreamEvent
	for i, raw := range chunks {
		var chunk dto.ChatCompletionsStreamResponse
		require.NoError(t, json.Unmarshal([]byte(raw), &chunk))
		batch, err := ChatCompletionsStreamChunkToResponsesEvents(&chunk, state)
		require.NoError(t, err)
		if i < len(chunks)-1 {
			for _, event := range batch {
				if event.Payload.Item != nil {
					assert.NotEqual(t, "call_search", event.Payload.Item.CallId)
					assert.NotEqual(t, "call_lookup", event.Payload.Item.CallId)
				}
			}
		}
		events = append(events, batch...)
	}
	events = append(events, FinalizeChatCompletionsStreamToResponses(state)...)
	require.NoError(t, state.Err())
	assert.Empty(t, FinalizeChatCompletionsStreamToResponses(state))

	added := make(map[string]int)
	done := make(map[string]int)
	argumentDeltas := make(map[string]string)
	var addedIndexes []int
	startedIndexes := make(map[int]bool)
	for _, event := range events {
		encoded, err := json.Marshal(event.Payload)
		require.NoError(t, err)
		var wire map[string]any
		require.NoError(t, json.Unmarshal(encoded, &wire))
		if event.Payload.OutputIndex != nil {
			index := *event.Payload.OutputIndex
			if event.Type == responsesEventOutputItemAdded {
				addedIndexes = append(addedIndexes, index)
				startedIndexes[index] = true
			} else {
				assert.True(t, startedIndexes[index], "%s preceded output_item.added at index %d", event.Type, index)
			}
		}
		if event.Type == responsesEventFunctionArgsDelta || event.Type == responsesEventFunctionArgsDone {
			assert.NotEqual(t, "call_search", event.Payload.ItemID, "search must not emit function argument events")
			if event.Type == responsesEventFunctionArgsDelta {
				argumentDeltas[event.Payload.ItemID] += event.Payload.Delta
			} else if event.Payload.ItemID == "call_lookup" {
				var arguments string
				require.NoError(t, json.Unmarshal(event.Payload.Arguments, &arguments))
				assert.Equal(t, argumentDeltas[event.Payload.ItemID], arguments)
			}
		}
		if item := event.Payload.Item; item != nil && item.CallId != "" {
			if event.Type == responsesEventOutputItemAdded {
				added[item.CallId]++
			} else if event.Type == responsesEventOutputItemDone {
				done[item.CallId]++
			}
			switch item.CallId {
			case "call_search":
				assert.Equal(t, 0, *event.Payload.OutputIndex)
				assert.Equal(t, "call_search", item.ID)
				assert.Equal(t, "tool_search_call", item.Type)
				assert.Equal(t, "client", item.Execution)
				assert.Empty(t, item.Name)
				assert.Equal(t, map[string]any{"query": "customer data"}, wire["item"].(map[string]any)["arguments"])
			case "call_lookup":
				assert.Equal(t, 1, *event.Payload.OutputIndex)
				assert.Equal(t, "lookup", item.Name)
				assert.Equal(t, "crm", item.Namespace)
			case "call_plain":
				assert.Equal(t, 2, *event.Payload.OutputIndex)
				assert.Equal(t, "plain", item.Name)
			}
		}
	}
	assert.Equal(t, map[string]int{"call_search": 1, "call_lookup": 1, "call_plain": 1}, added)
	assert.Equal(t, []int{0, 1, 2, 3}, addedIndexes)
	assert.Equal(t, added, done)
	assert.Equal(t, map[string]string{"call_plain": `{"x":1}`, "call_lookup": `{"id":"7"}`}, argumentDeltas)
	completed := events[len(events)-1]
	require.Equal(t, responsesEventCompleted, completed.Type)
	output := completed.Payload.Response.Output
	require.Len(t, output, 4)
	assert.Equal(t, "tool_search_call", output[0].Type)
	assert.JSONEq(t, `{"query":"customer data"}`, string(output[0].Arguments))
	assert.Equal(t, "lookup", output[1].Name)
	assert.Equal(t, "crm", output[1].Namespace)
	assert.Equal(t, `{"id":"7"}`, output[1].ArgumentsString())
	assert.Equal(t, "plain", output[2].Name)
	assert.Equal(t, "message", output[3].Type)
}

func TestResponsesToolStreamKeepsExistingTextLiveWhileHoldingLaterItems(t *testing.T) {
	state := NewChatToResponsesStreamState("resp_live", "test")
	state.ToolMapping = responsesToolMappingForTest()
	feed := func(raw string) []ChatToResponsesStreamEvent {
		t.Helper()
		var chunk dto.ChatCompletionsStreamResponse
		require.NoError(t, json.Unmarshal([]byte(raw), &chunk))
		events, err := ChatCompletionsStreamChunkToResponsesEvents(&chunk, state)
		require.NoError(t, err)
		return events
	}
	initial := feed(`{"choices":[{"delta":{"content":"Before. "}}]}`)
	require.Len(t, initial, 3)
	assert.Equal(t, responsesEventOutputTextDelta, initial[2].Type)
	assert.Empty(t, feed(`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_search","function":{"name":"compat_"}}]}}]}`))
	live := feed(`{"choices":[{"delta":{"content":"Still streaming. "}}]}`)
	require.Len(t, live, 1)
	assert.Equal(t, responsesEventOutputTextDelta, live[0].Type)
	assert.Equal(t, "Still streaming. ", live[0].Payload.Delta)
	assert.Empty(t, feed(`{"choices":[{"delta":{"tool_calls":[{"index":1,"id":"call_plain","function":{"name":"plain","arguments":"{}"}}]}}]}`))
	assert.Empty(t, feed(`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"name":"search","arguments":"{}"}}]}}]}`))
	finished := feed(`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`)
	finished = append(finished, FinalizeChatCompletionsStreamToResponses(state)...)
	require.NoError(t, state.Err())
	var addedIndexes []int
	for _, event := range append(initial, finished...) {
		if event.Type == responsesEventOutputItemAdded {
			addedIndexes = append(addedIndexes, *event.Payload.OutputIndex)
		}
	}
	assert.Equal(t, []int{0, 1, 2}, addedIndexes)
	require.Equal(t, responsesEventCompleted, finished[len(finished)-1].Type)
	assert.Equal(t, "Before. Still streaming. ", finished[len(finished)-1].Payload.Response.Output[0].Content[0].Text)
}

func TestResponsesToolStreamRejectsInvalidSearchAtFinishOrEOF(t *testing.T) {
	for _, arguments := range []string{`{"query":`, `[]`, `null`, `"value"`, ""} {
		for _, finish := range []bool{true, false} {
			t.Run(arguments+map[bool]string{true: "/finish", false: "/eof"}[finish], func(t *testing.T) {
				state := NewChatToResponsesStreamState("resp_bad", "test")
				state.ToolMapping = responsesToolMappingForTest()
				chunk := &dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{
					Delta: dto.ChatCompletionsStreamResponseChoiceDelta{ToolCalls: []dto.ToolCallResponse{{
						ID: "call_search", Function: dto.FunctionResponse{Name: "compat_search", Arguments: arguments},
					}}},
				}}}
				events, err := ChatCompletionsStreamChunkToResponsesEvents(chunk, state)
				require.NoError(t, err)
				require.Len(t, events, 1)
				assert.Equal(t, responsesEventCreated, events[0].Type)
				if finish {
					reason := "tool_calls"
					_, err = ChatCompletionsStreamChunkToResponsesEvents(&dto.ChatCompletionsStreamResponse{
						Choices: []dto.ChatCompletionsStreamResponseChoice{{FinishReason: &reason}},
					}, state)
					require.ErrorContains(t, err, "valid JSON object")
				}
				assert.Empty(t, FinalizeChatCompletionsStreamToResponses(state))
				require.ErrorContains(t, state.Err(), "valid JSON object")
				assert.Empty(t, FinalizeChatCompletionsStreamToResponses(state))
			})
		}
	}
}
