package dto

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/stretchr/testify/require"
)

// Frozen de2c439 collector, used only with bounded fixtures to check exact
// compatibility independently of the new index/walk implementation.
func referenceResponsesTokenInput(raw json.RawMessage, texts *[]string, files *[]*types.FileMeta) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		*texts = append(*texts, text)
		return
	}
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) == nil {
		for _, item := range items {
			referenceResponsesTokenInput(item, texts, files)
		}
		return
	}
	var item struct {
		Type     string          `json:"type"`
		Role     string          `json:"role"`
		Content  json.RawMessage `json:"content"`
		Text     string          `json:"text"`
		ImageURL json.RawMessage `json:"image_url"`
		FileURL  json.RawMessage `json:"file_url"`
		FileData string          `json:"file_data"`
		Detail   string          `json:"detail"`
	}
	if json.Unmarshal(raw, &item) == nil {
		switch item.Type {
		case "reasoning":
			// encrypted_content is an opaque replay payload. Tokenizing its
			// ciphertext as ordinary text can turn a normal prompt into millions
			// of estimated tokens. Visible summary/content text is countable.
			var reasoning struct {
				Summary []struct {
					Text string `json:"text"`
				} `json:"summary"`
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			}
			if json.Unmarshal(raw, &reasoning) == nil {
				for _, part := range reasoning.Summary {
					*texts = append(*texts, part.Text)
				}
				for _, part := range reasoning.Content {
					*texts = append(*texts, part.Text)
				}
			}
			return
		case "input_text", "output_text":
			*texts = append(*texts, item.Text)
			return
		case "input_image":
			if imageURL := responsesMediaURL(item.ImageURL); imageURL != "" {
				*files = append(*files, &types.FileMeta{FileType: types.FileTypeImage, Source: types.NewFileSourceFromData(imageURL, ""), Detail: item.Detail})
				return
			}
		case "input_file":
			data := responsesMediaURL(item.FileURL)
			if data == "" {
				data = item.FileData
			}
			if data != "" {
				*files = append(*files, &types.FileMeta{FileType: types.FileTypeFile, Source: types.NewFileSourceFromData(data, "")})
				return
			}
		case "", "message":
			if item.Role != "" && len(item.Content) > 0 {
				*texts = append(*texts, item.Role)
				referenceResponsesTokenInput(item.Content, texts, files)
				return
			}
		}
	}
	*texts = append(*texts, normalizeRawJSONForTokenCount(raw))
}

func compareResponsesTokenInput(t *testing.T, input string) {
	t.Helper()
	var oldTexts, newTexts []string
	var oldFiles, newFiles []*types.FileMeta
	referenceResponsesTokenInput(json.RawMessage(input), &oldTexts, &oldFiles)
	collectResponsesTokenInput(json.RawMessage(input), &newTexts, &newFiles)
	require.Equal(t, oldTexts, newTexts, "input: %s", input)
	require.Equal(t, oldFiles, newFiles, "input: %s", input)
	for _, meta := range []*types.TokenCountMeta{
		(&OpenAIResponsesRequest{Input: json.RawMessage(input)}).GetTokenCountMeta(),
		(&OpenAIResponsesCompactionRequest{Input: json.RawMessage(input)}).GetTokenCountMeta(),
	} {
		require.Equal(t, strings.Join(oldTexts, "\n"), meta.CombineText)
		require.Equal(t, len(oldFiles), len(meta.Files))
		for i := range oldFiles {
			require.Equal(t, oldFiles[i], meta.Files[i])
		}
	}
}

func TestResponsesTokenInputCompatibility(t *testing.T) {
	inputs := []string{
		`null`, ` null `, `false`, `123`, `1e1000`, `-0.00E-3`, `""`, `"\u4e2d\uD83D\uDE00"`, `"\\u4e2d"`,
		`[]`, `[null,"",["one","two"]]`, ` { "unknown": 1e1000, "x": "\u4e2d" } `,
		`{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}`,
		`{"role":"user","content":null}`, `{"role":"user","content":0}`, `{"role":"user","content":[]}`,
		`{"role":"user","content":"first","content":"last"}`, `{"role":"user","content":"first","CONTENT":null}`,
		`{"TYPE":"message","ROLE":"user","CONTENT":"upper"}`, `{"type":"message","type":null,"role":"user","role":null,"content":"kept"}`,
		`{"role":"user","content":"counted","text":17}`, `{"role":"user","content":"counted","file_data":{}}`,
		`{"role":"user","content":"counted","detail":[],"detail":"low"}`, `{"type":17,"type":"message","role":"user","content":"kept raw"}`,
		`{"role":"user","content":"counted","image_url":{"invalid":"shape"},"file_url":false}`,
		`{"type":"input_text","text":"first","text":null}`, `{"type":"output_text","text":"\u4e2d","role":12}`,
		`{"type":"input_image","image_url":{"url":"data:image/png;base64,Zml4dHVyZQ=="},"detail":"high"}`,
		`{"type":"input_image","image_url":{"url":"first","URL":null}}`, `{"type":"input_image","image_url":{"url":3,"url":"last"}}`,
		`{"type":"input_file","file_url":"https://fixture.invalid/file","file_data":"fallback"}`, `{"type":"input_file","file_data":"data:application/pdf;base64,Zml4dHVyZQ=="}`,
		`{"type":"function_call_output","output":{"content":[{"type":"input_image","image_url":"not-media"}]}}`,
		`{"type":"future","role":"user","content":[{"type":"input_image","image_url":"not-media"}]}`,
		`{"type":"reasoning","encrypted_content":"ciphertext","summary":[{"text":"visible"}],"content":[{"text":"visible content"}]}`,
		`{"type":"reasoning","summary":[{"text":"first"}],"summary":[null]}`,
		`{"type":"reasoning","SUMMARY":[{"text":"first","text":null}],"content":null}`,
		`{"type":"reasoning","\u017fummary":[{"text":"casefold summary"}]}`,
		`{"type":"reasoning","summary":[{"text":"discarded"},false],"encrypted_content":"ciphertext"}`,
		`{"type":"reasoning","content":"not an array","encrypted_content":"ciphertext"}`,
		`{"type":"reasoning","text":[],"encrypted_content":"raw fallback"}`,
		`{"role":"user","content":"\uD800"}`, `{"role":"user","content":"\uDE00"}`,
		``, ` `, `[`, `{"role":"user","content":["incomplete"}`, `{"x":"\u12G4"}`, `true false`, `[] trailing`,
		"{\"role\":\"user\",\"content\":\"\xff\"}",
	}
	for i, input := range inputs {
		t.Run(fmt.Sprintf("fixture_%d", i), func(t *testing.T) { compareResponsesTokenInput(t, input) })
	}
}

func TestResponsesTokenInputDeterministicDifferential(t *testing.T) {
	random := rand.New(rand.NewSource(755))
	leaves := []string{
		`null`, `true`, `1e1000`, `"literal \\u4e2d"`, `"\u4e2d"`,
		`{"type":"input_text","text":"visible"}`, `{"type":"input_text","text":17}`,
		`{"type":"input_image","image_url":"data:image/png;base64,Zml4dHVyZQ=="}`,
		`{"type":"input_file","file_url":{"url":"https://fixture.invalid/file"}}`,
		`{"type":"reasoning","encrypted_content":"cipher","summary":[{"text":"summary"}]}`,
		`{"type":"function_call_output","output":"tool text"}`, `{"unknown": "raw \u4e2d", "unknown": null}`,
	}
	var generate func(int) string
	generate = func(depth int) string {
		if depth == 0 || random.Intn(3) == 0 {
			return leaves[random.Intn(len(leaves))]
		}
		switch random.Intn(6) {
		case 0:
			return "[" + generate(depth-1) + "," + generate(depth-1) + "]"
		case 1:
			return `{"role":"user","content":` + generate(depth-1) + `}`
		case 2:
			return `{"type":"message","TYPE":null,"role":"user","content":` + generate(depth-1) + `,"CONTENT":` + generate(depth-1) + `}`
		case 3:
			return `{"ROLE":"user","content":` + generate(depth-1) + `,"detail":` + generate(depth-1) + `}`
		case 4:
			return ` {"type":"future","content":` + generate(depth-1) + `} `
		default:
			return `{"type":"reasoning","summary":` + generate(depth-1) + `,"encrypted_content":"ciphertext"}`
		}
	}
	for i := range 400 {
		t.Run(fmt.Sprintf("case_%d", i), func(t *testing.T) { compareResponsesTokenInput(t, generate(4)) })
	}
}

func TestResponsesTokenInputDeepContainersPreserveAllText(t *testing.T) {
	for _, depth := range []int{128, 2048, 9999} {
		t.Run(fmt.Sprintf("arrays_%d", depth), func(t *testing.T) {
			input := strings.Repeat("[", depth) + `"deep text"` + strings.Repeat("]", depth)
			for _, meta := range []*types.TokenCountMeta{
				(&OpenAIResponsesRequest{Input: json.RawMessage(input)}).GetTokenCountMeta(),
				(&OpenAIResponsesCompactionRequest{Input: json.RawMessage(input)}).GetTokenCountMeta(),
			} {
				require.Equal(t, "deep text", meta.CombineText)
			}
		})
	}
	const depth = 2048
	input := strings.Repeat(`{"role":"user","content":`, depth) + `"deep text"` + strings.Repeat("}", depth)
	meta := (&OpenAIResponsesRequest{Input: json.RawMessage(input)}).GetTokenCountMeta()
	require.Equal(t, strings.Repeat("user\n", depth)+"deep text", meta.CombineText)
	tooDeep := strings.Repeat("[", 10001) + `"full fallback"` + strings.Repeat("]", 10001)
	meta = (&OpenAIResponsesRequest{Input: json.RawMessage(tooDeep)}).GetTokenCountMeta()
	require.Equal(t, tooDeep, meta.CombineText, "out-of-parser-bound input retains its full fallback instead of being truncated")
}

func BenchmarkResponsesTokenInputNesting(b *testing.B) {
	for _, shape := range []string{"array", "message"} {
		for _, depth := range []int{128, 512, 2048} {
			prefix, suffix := "[", "]"
			if shape == "message" {
				prefix, suffix = `{"role":"user","content":`, "}"
			}
			input := json.RawMessage(strings.Repeat(prefix, depth) + `"bounded local fixture"` + strings.Repeat(suffix, depth))
			for _, implementation := range []struct {
				name    string
				collect func(json.RawMessage, *[]string, *[]*types.FileMeta)
			}{{"indexed", collectResponsesTokenInput}, {"reference", referenceResponsesTokenInput}} {
				b.Run(fmt.Sprintf("%s/%d/%s", shape, depth, implementation.name), func(b *testing.B) {
					b.ReportAllocs()
					b.SetBytes(int64(len(input)))
					for b.Loop() {
						var texts []string
						var files []*types.FileMeta
						implementation.collect(input, &texts, &files)
					}
				})
			}
		}
	}
}

func BenchmarkResponsesTokenInputDense(b *testing.B) {
	for _, count := range []int{1024, 16384} {
		for _, shape := range []string{"array", "unknown", "tool"} {
			input := "[" + strings.Repeat("0,", count-1) + "0]"
			if shape != "array" {
				input = "{" + strings.Repeat(`"x":0,`, count-1) + `"x":0}`
				if shape == "tool" {
					input = `{"type":"function_call_output","output":` + input + "}"
				}
			}
			for _, implementation := range []struct {
				name    string
				collect func(json.RawMessage, *[]string, *[]*types.FileMeta)
			}{{"indexed", collectResponsesTokenInput}, {"reference", referenceResponsesTokenInput}} {
				b.Run(fmt.Sprintf("%s/%d/%s", shape, count, implementation.name), func(b *testing.B) {
					b.ReportAllocs()
					b.SetBytes(int64(len(input)))
					for b.Loop() {
						var texts []string
						var files []*types.FileMeta
						implementation.collect(json.RawMessage(input), &texts, &files)
					}
				})
			}
		}
	}
}

func BenchmarkResponsesTokenInputOpaqueContainers(b *testing.B) {
	for _, count := range []int{1024, 16384} {
		payload := "[" + strings.Repeat("[],", count-1) + "[]]"
		for _, shape := range []string{"root", "input_array", "message_array"} {
			input := `{"payload":` + payload + `,"type":"future"}`
			if shape == "input_array" {
				input = "[" + input + "]"
			} else if shape == "message_array" {
				input = `[{"role":"user","content":[` + input + "]}]"
			}
			for _, implementation := range []struct {
				name    string
				collect func(json.RawMessage, *[]string, *[]*types.FileMeta)
			}{{"indexed", collectResponsesTokenInput}, {"reference", referenceResponsesTokenInput}} {
				b.Run(fmt.Sprintf("%s/%d/%s", shape, count, implementation.name), func(b *testing.B) {
					b.ReportAllocs()
					b.SetBytes(int64(len(input)))
					for b.Loop() {
						var texts []string
						var files []*types.FileMeta
						implementation.collect(json.RawMessage(input), &texts, &files)
					}
				})
			}
		}
	}
}
