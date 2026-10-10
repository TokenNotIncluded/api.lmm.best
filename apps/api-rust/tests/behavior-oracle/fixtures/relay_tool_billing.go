// Generate vectors using current Go relay handlers and tool price policy.
// Run from apps/api-go: go run ../api-rust/tests/behavior-oracle/fixtures/relay_tool_billing.go OUTPUT.json
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/relay/channel/openai"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/gin-gonic/gin"
)

type testcase struct {
	Name    string           `json:"name"`
	Model   string           `json:"model"`
	Prices  string           `json:"prices"`
	Request string           `json:"request"`
	Stream  bool             `json:"stream"`
	Wire    string           `json:"wire"`
	Items   []map[string]any `json:"items"`
}

func main() {
	if len(os.Args) != 2 {
		panic("expected output path")
	}
	gin.SetMode(gin.TestMode)
	service.InitTokenEncoders()
	constant.StreamingTimeout = 5
	item := `{"type":"image_generation_call","id":"img-1","result":"pixels","status":"completed"}`
	cases := []testcase{
		{Name: "declaration-only", Model: "gpt-4o", Request: `{"tools":[{"type":"web_search"},{"type":"image_generation"}]}`, Wire: `{"status":"completed","output":[]}`},
		{Name: "declared-search", Model: "gpt-4o", Request: `{"tools":[{"type":"web_search"}]}`, Wire: `{"output":[{"type":"web_search_call"},{"type":"file_search_call"}]}`},
		{Name: "undeclared-search-preview-default", Model: "gpt-4o", Request: `{}`, Wire: `{"output":[{"type":"web_search_call"}]}`},
		{Name: "preview-overrides-search-declaration", Model: "gpt-4o", Request: `{"tools":[{"type":"web_search"},{"type":"web_search_preview"}]}`, Wire: `{"output":[{"type":"web_search_call"}]}`},
		{Name: "longest-prefix-zero", Model: "gpt-4o-mini", Prices: `{"web_search_preview:gpt-4o*":30,"web_search_preview:gpt-4o-mini*":0}`, Request: `{}`, Wire: `{"output":[{"type":"web_search_call"}]}`},
		{Name: "invalid-entry-keeps-default-and-valid-sibling", Model: "custom-model", Prices: `{"file_search":-1,"custom_fn":2}`, Request: `{}`, Wire: `{"output":[{"type":"file_search_call"},{"type":"function_call","name":"custom_fn"},{"type":"function_call","name":"unpriced_fn"},{"type":"function_call","name":"image_generation"}]}`},
		{Name: "image-json", Model: "gpt-4o", Request: `{}`, Wire: `{"output":[` + item + `]}`},
		{Name: "failed-image-json-still-counts-web-search", Model: "gpt-4o", Request: `{}`, Wire: `{"status":"FAILED","output":[` + item + `,{"type":"web_search_call"}]}`},
		{Name: "stream-image-deduplicates-terminal", Model: "gpt-4o", Request: `{}`, Stream: true, Wire: "data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":" + item + "}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"output\":[" + item + "]}}\n\n"},
		{Name: "stream-failed-image-retains-file-search", Model: "gpt-4o", Request: `{}`, Stream: true, Wire: "data: {\"type\":\"response.output_item.done\",\"item\":" + item + "}\n\ndata: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"file_search_call\"}}\n\ndata: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\"}}\n\n"},
		{Name: "stream-terminal-alone-does-not-add-web-call", Model: "gpt-4o", Request: `{}`, Stream: true, Wire: "data: {\"type\":\"response.completed\",\"response\":{\"output\":[{\"type\":\"web_search_call\"}]}}\n\n"},
	}
	for i := range cases {
		tc := &cases[i]
		if tc.Prices == "" {
			tc.Prices = "{}"
		}
		operation_setting.LoadToolPricesFromJSONString(tc.Prices)
		var request dto.OpenAIResponsesRequest
		if err := json.Unmarshal([]byte(tc.Request), &request); err != nil {
			panic(err)
		}
		writer := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(writer)
		ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		info := relaycommon.GenRelayInfoResponses(ctx, &request)
		info.ChannelMeta = &relaycommon.ChannelMeta{UpstreamModelName: tc.Model}
		info.OriginModelName = tc.Model
		info.DisablePing = true
		response := &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(tc.Wire))}
		if tc.Stream {
			if _, err := openai.OaiResponsesStreamHandler(ctx, info, response); err != nil {
				panic(err)
			}
		} else if _, err := openai.OaiResponsesHandler(ctx, info, response); err != nil {
			panic(err)
		}
		tc.Items = []map[string]any{}
		for name, tool := range info.ResponsesUsageInfo.BuiltInTools {
			price := operation_setting.GetToolPriceForModel(name, tc.Model)
			if tool != nil && tool.CallCount > 0 && price > 0 {
				tc.Items = append(tc.Items, map[string]any{"name": name, "count": tool.CallCount, "price": price})
			}
		}
		sort.Slice(tc.Items, func(i, j int) bool { return tc.Items[i]["name"].(string) < tc.Items[j]["name"].(string) })
	}
	data, err := json.MarshalIndent(cases, "", "  ")
	if err != nil {
		panic(err)
	}
	if err = os.WriteFile(os.Args[1], append(data, '\n'), 0644); err != nil {
		panic(err)
	}
	fmt.Printf("wrote %d current-Go tool handler/price vectors\n", len(cases))
}
