package oaichat

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/relayconvert/convmeta"
)

// RestoreResponsesToolOutput restores identities hidden by the Chat-compatible
// request conversion. Changes are applied only after every output validates.
func RestoreResponsesToolOutput(resp *dto.OpenAIResponsesResponse, mapping convmeta.ResponsesToolMap) error {
	if resp == nil || len(mapping) == 0 || len(resp.Output) == 0 {
		return nil
	}
	output := append([]dto.ResponsesOutput(nil), resp.Output...)
	for i := range output {
		if err := restoreResponsesToolItem(&output[i], mapping); err != nil {
			return fmt.Errorf("restore response output %d: %w", i, err)
		}
	}
	resp.Output = output
	return nil
}

func restoreResponsesToolItem(item *dto.ResponsesOutput, mapping convmeta.ResponsesToolMap) error {
	if item.Type != responsesOutputTypeFunctionCall {
		return nil
	}
	identity, ok := mapping[item.Name]
	if !ok {
		return nil
	}
	if !identity.ToolSearch {
		item.Name = identity.Name
		item.Namespace = identity.Namespace
		return nil
	}

	// Chat function arguments are a JSON-encoded string. Responses client-side
	// tool search requires an actual JSON object, including on streamed items.
	raw := item.Arguments
	var encoded string
	if json.Unmarshal(raw, &encoded) == nil {
		raw = []byte(encoded)
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' || !json.Valid(raw) {
		return fmt.Errorf("tool_search call %q arguments must be a valid JSON object", item.CallId)
	}
	item.Type = "tool_search_call"
	item.Execution = "client"
	item.Name = ""
	item.Namespace = ""
	item.Arguments = append(json.RawMessage(nil), raw...)
	return nil
}
