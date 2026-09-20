package dto

import (
	"bytes"
	"encoding/json"

	kitutil "github.com/LIghtJUNction/api.lmm.best/relaykit/relayconvert/kitutil"
)

// UnmarshalJSON tolerates providers that emit null or an empty object for
// choices in a streaming chat-completions chunk. Normal arrays keep the
// existing decoding behavior and malformed non-empty objects still fail.
func (c *ChatCompletionsStreamResponse) UnmarshalJSON(data []byte) error {
	type streamResponse ChatCompletionsStreamResponse
	aux := &struct {
		Choices json.RawMessage `json:"choices"`
		*streamResponse
	}{
		streamResponse: (*streamResponse)(c),
	}
	if err := kitutil.Unmarshal(data, aux); err != nil {
		return err
	}
	if len(aux.Choices) == 0 {
		return nil
	}

	trimmed := bytes.TrimSpace(aux.Choices)
	if bytes.Equal(trimmed, []byte("null")) || bytes.Equal(trimmed, []byte("{}")) {
		c.Choices = []ChatCompletionsStreamResponseChoice{}
		return nil
	}

	var choices []ChatCompletionsStreamResponseChoice
	if err := kitutil.Unmarshal(trimmed, &choices); err != nil {
		return err
	}
	c.Choices = choices
	return nil
}
