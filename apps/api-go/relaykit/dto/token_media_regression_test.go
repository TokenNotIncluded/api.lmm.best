package dto

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInlineImageTokenMetadata(t *testing.T) {
	var imageBytes bytes.Buffer
	require.NoError(t, png.Encode(&imageBytes, image.NewRGBA(image.Rect(0, 0, 8, 8))))
	encoded := base64.StdEncoding.EncodeToString(imageBytes.Bytes())
	imageBlock := `{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` + encoded + `"}}`
	for _, tc := range []struct {
		name, content string
		files         int
	}{
		{"nested", `[{"type":"tool_result","tool_use_id":"test","content":[{"type":"text","text":"real explanation"},` + imageBlock + `]}]`, 1},
		{"top and nested", `[` + imageBlock + `,{"type":"tool_result","content":[{"type":"text","text":"real explanation"},` + imageBlock + `]}]`, 2},
		{"string", `[{"type":"tool_result","content":"real explanation"}]`, 0},
		{"unknown", `[{"type":"tool_result","content":[{"type":"future","payload":"real explanation"}]}]`, 0},
	} {
		t.Run("claude/"+tc.name, func(t *testing.T) {
			var request ClaudeRequest
			require.NoError(t, json.Unmarshal([]byte(`{"messages":[{"role":"user","content":`+tc.content+`}]}`), &request))
			meta := request.GetTokenCountMeta()
			require.Len(t, meta.Files, tc.files)
			require.NotContains(t, meta.CombineText, encoded)
			require.Contains(t, meta.CombineText, "real explanation")
		})
	}
	for _, tc := range []struct {
		name, input string
		files       int
	}{
		{"image", `[{"role":"user","content":[{"type":"input_text","text":"real explanation"},{"type":"input_image","image_url":"data:image/png;base64,` + encoded + `"}]}]`, 1},
		{"multiple images", `[{"role":"user","content":[{"type":"input_text","text":"real explanation"},{"type":"input_image","image_url":"data:image/png;base64,` + encoded + `"},{"type":"input_image","image_url":{"url":"data:image/png;base64,` + encoded + `"}}]}]`, 2},
		{"string", `"real explanation"`, 0},
		{"tool output", `[{"type":"function_call_output","call_id":"test","output":"real explanation"}]`, 0},
		{"unknown", `[{"type":"future","payload":"real explanation"}]`, 0},
		{"malformed", `[{"real explanation":`, 0},
	} {
		t.Run("compact/"+tc.name, func(t *testing.T) {
			request := OpenAIResponsesCompactionRequest{Input: json.RawMessage(tc.input), Instructions: json.RawMessage(`"instruction"`), Tools: json.RawMessage(`[{"name":"test_tool"}]`)}
			meta := request.GetTokenCountMeta()
			require.Len(t, meta.Files, tc.files)
			require.NotContains(t, meta.CombineText, encoded)
			for _, text := range []string{"real explanation", "instruction", "test_tool"} {
				require.Contains(t, meta.CombineText, text)
			}
			responses := OpenAIResponsesRequest{Input: request.Input, Instructions: request.Instructions, Tools: request.Tools}
			regularMeta := responses.GetTokenCountMeta()
			require.ElementsMatch(t, strings.Split(meta.CombineText, "\n"), strings.Split(regularMeta.CombineText, "\n"))
			require.Len(t, regularMeta.Files, tc.files)
		})
	}
	// Long genuine tool text must not be removed by a size heuristic.
	longText := strings.Repeat("tool text ", 10000)
	request := ClaudeRequest{Messages: []ClaudeMessage{{Role: "user", Content: []ClaudeMediaMessage{{Type: "tool_result", Content: longText}}}}}
	require.Contains(t, request.GetTokenCountMeta().CombineText, longText)
}
