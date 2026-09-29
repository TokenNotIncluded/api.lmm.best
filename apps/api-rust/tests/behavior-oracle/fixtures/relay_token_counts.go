// Regenerate from the current Go backend, not from an independent imitation:
// cd apps/api-go && GOMAXPROCS=2 go run -p=2 ../api-rust/tests/behavior-oracle/fixtures/relay_token_counts.go OUTPUT.json
package main

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
)

type vector struct {
	Model     string `json:"model"`
	Endpoint  string `json:"endpoint,omitempty"`
	Request   string `json:"request,omitempty"`
	Text      string `json:"text,omitempty"`
	Combined  string `json:"combined,omitempty"`
	Tokens    int    `json:"tokens"`
	MaxTokens int    `json:"max_tokens,omitempty"`
}

func main() {
	if len(os.Args) != 2 {
		panic("expected output file")
	}
	service.InitTokenEncoders()
	constant.CountToken = true
	constant.GetMediaToken = false
	gin.SetMode(gin.TestMode)
	var vectors []vector
	models := []string{"gpt-4o", "gpt-4o-mini", "gpt-5.6-sol", "o3", "o3-mini", "o4-mini", "Gpt-4o", "vendor/gpt-4o", "ft:gpt-4", "chatgpt-4o-latest", "gemini-2.5-pro", "claude-sonnet-4", "deepseek-chat"}
	for _, model := range models {
		for _, text := range []string{"", "Hello, world!", "你好，世界！こんにちは 한글", "<|endoftext|> <|fim_prefix|>", "a\u0301 🙂👨‍👩‍👧‍👦\r\n", `\u4e2d`, strings.Repeat("7", 300), "https://api.example.com/v1/models?q=test", "∑ √ Ⅻ ² 𝟘 \u0345"} {
			vectors = append(vectors, vector{Model: model, Text: text, Tokens: service.CountTextToken(text, model)})
		}
		requests := []struct{ endpoint, raw string }{
			{"chat", `{"messages":[{"role":"system","content":"Be precise."},{"role":"user","name":"Alice","content":"你好"}],"max_tokens":20,"max_completion_tokens":30}`},
			{"chat", `{"messages":[{"role":"user","content":[{"type":"text","text":"weather"},{"type":"text","text":"tomorrow"}]},{"role":"assistant","content":null,"name":"ignored","tool_calls":[{"function":{"name":"f","arguments":"{}"}}]}],"tools":[{"type":"function","function":{"name":"weather","description":"天气","parameters":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}}}]}`},
			{"chat", `{"messages":[{"role":"tool","content":"\\u4e2d","tools":[{"type":"function","function":{"name":"dynamic","parameters":{"limit":2,"nested":{"x":true,"v":null}}}}]}]}`},
			{"completions", `{"prompt":["one","two",3],"input":["three",4],"max_tokens":10}`},
			{"responses", `{"input":"hello","instructions":"Be precise.","max_output_tokens":60}`},
			{"responses", `{"input":[{"role":"user","content":[{"type":"input_text","text":"你好"}]},{"type":"reasoning","encrypted_content":"secret-ciphertext-must-not-be-counted","summary":[{"text":"summary"}],"content":[{"text":"visible"}]},{"type":"function_call_output","call_id":"abc","output":"real output"}],"metadata": { "x":"\u4e2d" },"tools":[{"type":"function","name":"f","parameters": {"type":"object"}}],"tool_choice":"auto"}`},
			{"compact", `{"input":[{"role":"user","content":"hello"},{"type":"function_call_output","call_id":"abc","output":"real output"}],"instructions":"\u6458\u8981","tools":[{"name":"工具"}]}`},
		}
		for _, item := range requests {
			var request dto.Request
			format := types.RelayFormatOpenAI
			switch item.endpoint {
			case "responses":
				request = &dto.OpenAIResponsesRequest{}
				format = types.RelayFormatOpenAIResponses
			case "compact":
				request = &dto.OpenAIResponsesCompactionRequest{}
				format = types.RelayFormatOpenAIResponses
			default:
				request = &dto.GeneralOpenAIRequest{}
			}
			if err := json.Unmarshal([]byte(item.raw), request); err != nil {
				panic(err)
			}
			meta := request.GetTokenCountMeta()
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			common.SetContextKey(c, constant.ContextKeyOriginalModel, model)
			info := &relaycommon.RelayInfo{RelayFormat: format}
			count, err := service.EstimateRequestToken(c, meta, info)
			if err != nil {
				panic(err)
			}
			vectors = append(vectors, vector{Model: model, Endpoint: item.endpoint, Request: item.raw, Combined: meta.CombineText, Tokens: count, MaxTokens: meta.MaxTokens})
		}
	}
	data, err := json.MarshalIndent(vectors, "", "  ")
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile(os.Args[1], append(data, '\n'), 0644); err != nil {
		panic(err)
	}
	fmt.Printf("wrote %d current-Go token vectors\n", len(vectors))
}
