package openai

import (
	"encoding/json"
	"strings"
)

// Observe generated content locally without expanding the DTO used by response
// converters. Images, encrypted reasoning and tool metadata are not text usage.
func responsesTerminalOutputText(data string) string {
	type part struct {
		Type    string `json:"type"`
		Text    string `json:"text"`
		Refusal string `json:"refusal"`
	}
	var event struct {
		Response struct {
			Output []struct {
				Type      string          `json:"type"`
				Content   []part          `json:"content"`
				Summary   []part          `json:"summary"`
				Arguments json.RawMessage `json:"arguments"`
			} `json:"output"`
		} `json:"response"`
	}
	if json.Unmarshal([]byte(data), &event) != nil {
		return ""
	}
	var text strings.Builder
	for _, output := range event.Response.Output {
		switch output.Type {
		case "message":
			for _, content := range output.Content {
				switch content.Type {
				case "output_text":
					text.WriteString(content.Text)
				case "refusal":
					text.WriteString(content.Refusal)
				}
			}
		case "function_call":
			var arguments string
			if json.Unmarshal(output.Arguments, &arguments) == nil {
				text.WriteString(arguments)
			}
		case "reasoning":
			for _, content := range output.Content {
				if content.Type == "reasoning_text" {
					text.WriteString(content.Text)
				}
			}
			for _, summary := range output.Summary {
				if summary.Type == "summary_text" {
					text.WriteString(summary.Text)
				}
			}
		}
	}
	return text.String()
}
