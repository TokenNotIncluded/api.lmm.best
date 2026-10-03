package dto

import (
	"encoding/json"

	"github.com/LIghtJUNction/api.lmm.best/relaykit/relayconvert/kitutil"
)

// Tool schemas can contain integers and decimals that float64 cannot represent.
// Decode schemas and dynamic tool declarations with UseNumber; the other
// request fields keep their existing JSON decoding behavior.
func (r *FunctionRequest) UnmarshalJSON(data []byte) error {
	type plain FunctionRequest
	aux := struct {
		*plain
		Parameters json.RawMessage `json:"parameters"`
	}{plain: (*plain)(r)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if len(aux.Parameters) > 0 {
		return kitutil.UnmarshalWithNumber(aux.Parameters, &r.Parameters)
	}
	return nil
}

func (t *Tool) UnmarshalJSON(data []byte) error {
	type plain Tool
	aux := struct {
		*plain
		InputSchema json.RawMessage `json:"input_schema"`
	}{plain: (*plain)(t)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if len(aux.InputSchema) > 0 {
		return kitutil.UnmarshalWithNumber(aux.InputSchema, &t.InputSchema)
	}
	return nil
}

func (r *ClaudeRequest) UnmarshalJSON(data []byte) error {
	type plain ClaudeRequest
	aux := struct {
		*plain
		Tools json.RawMessage `json:"tools"`
	}{plain: (*plain)(r)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if len(aux.Tools) == 0 {
		return nil
	}
	return kitutil.UnmarshalWithNumber(aux.Tools, &r.Tools)
}

func (t *GeminiChatTool) UnmarshalJSON(data []byte) error {
	type plain GeminiChatTool
	aux := struct {
		*plain
		FunctionDeclarations json.RawMessage `json:"functionDeclarations"`
	}{plain: (*plain)(t)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if len(aux.FunctionDeclarations) > 0 {
		return kitutil.UnmarshalWithNumber(aux.FunctionDeclarations, &t.FunctionDeclarations)
	}
	return nil
}

// An explicit method also prevents the embedded FunctionRequest's unmarshaler
// from consuming the outer declaration and dropping parametersJsonSchema.
func (r *GeminiFunctionDeclaration) UnmarshalJSON(data []byte) error {
	if err := json.Unmarshal(data, &r.FunctionRequest); err != nil {
		return err
	}
	var aux struct {
		ParametersJsonSchema json.RawMessage `json:"parametersJsonSchema"`
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if len(aux.ParametersJsonSchema) > 0 {
		return kitutil.UnmarshalWithNumber(aux.ParametersJsonSchema, &r.ParametersJsonSchema)
	}
	return nil
}
