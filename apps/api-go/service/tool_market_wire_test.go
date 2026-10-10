package service

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func marketWireTestResponse(t *testing.T, capture *marketWireCapture, method, id, contentType, raw string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, "https://fixture.invalid/mcp", strings.NewReader(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"method":%q}`, id, method)))
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	recorder.Header().Set("Content-Type", contentType)
	_, err = recorder.WriteString(raw)
	require.NoError(t, err)
	response := recorder.Result()
	capture.wrap(request, response)
	t.Cleanup(func() { _ = response.Body.Close() })
	return response
}

func TestToolMarketWireJSONPreservesExactNumbersAfterReadingBody(t *testing.T) {
	for _, method := range []string{"initialize", "tools/list", "tools/call"} {
		t.Run(method, func(t *testing.T) {
			capture := &marketWireCapture{}
			raw := `{"jsonrpc":"2.0","id":9007199254740993,"result":{"large":9007199254740993,"nested":{"maximum":18446744073709551615},"values":[-9007199254740993,0.12345678901234567890123456789,1.25e+20]}}`
			response := marketWireTestResponse(t, capture, method, "9007199254740993", "application/json; charset=utf-8", raw)
			// Restoration is based on the bytes consumed by the SDK body reader.
			_, err := capture.result(method)
			require.ErrorIs(t, err, ErrMarketRemoteResult)
			consumed, err := io.ReadAll(response.Body)
			require.NoError(t, err)
			require.Equal(t, raw, string(consumed))
			result, err := capture.result(method)
			require.NoError(t, err)
			require.Equal(t, json.Number("9007199254740993"), result["large"])
			require.Equal(t, json.Number("18446744073709551615"), result["nested"].(map[string]any)["maximum"])
			require.Equal(t, []any{json.Number("-9007199254740993"), json.Number("0.12345678901234567890123456789"), json.Number("1.25e+20")}, result["values"])
		})
	}
}

func TestToolMarketWireSSEOnlyUsesCompleteMatchingMessage(t *testing.T) {
	for _, eventType := range []string{"", "event: message\r\n"} {
		t.Run(fmt.Sprintf("event=%q", eventType), func(t *testing.T) {
			capture := &marketWireCapture{}
			raw := ": server heartbeat\r\n\r\n" +
				"event: message\r\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\",\"params\":{\"progress\":50}}\r\n\r\n" +
				"data: {\"jsonrpc\":\"2.0\",\"id\":8,\"result\":{\"source\":\"wrong-id\"}}\r\n\r\n" +
				"data: {\"jsonrpc\":\"2.0\",\"id\":\"7\",\"result\":{\"source\":\"wrong-id-type\"}}\r\n\r\n" +
				"event: other\r\ndata: {\"jsonrpc\":\"2.0\",\"id\":7,\"result\":{\"source\":\"wrong-event\"}}\r\n\r\n" +
				eventType + "id: ignored-sse-id\r\ndata: {\"jsonrpc\":\"2.0\",\"id\":7,\r\ndata: \"result\":{\"source\":\"matching\",\"n\":9007199254740993}}\r\n\r\n" +
				"data: {\"jsonrpc\":\"2.0\",\"id\":7,\"result\":{\"source\":\"incomplete-trailing\"}}"
			response := marketWireTestResponse(t, capture, "tools/call", "7", "text/event-stream; charset=utf-8", raw)
			_, err := io.Copy(io.Discard, response.Body)
			require.NoError(t, err)
			result, err := capture.result("tools/call")
			require.NoError(t, err)
			require.Equal(t, "matching", result["source"])
			require.Equal(t, json.Number("9007199254740993"), result["n"])
		})
	}
}

func TestToolMarketWireRejectsMalformedUnmatchedAndIncompleteResponses(t *testing.T) {
	matching := `{"jsonrpc":"2.0","id":7,"result":{"ok":true}}`
	tests := []struct {
		name, contentType, raw string
	}{
		{"missing-content-type", "", matching},
		{"wrong-content-type", "text/plain", matching},
		{"malformed-content-type", "application/json; =oops", matching},
		{"malformed-json", "application/json", `{"jsonrpc":`},
		{"extra-json", "application/json", matching + `{}`},
		{"missing-id", "application/json", `{"jsonrpc":"2.0","result":{"ok":true}}`},
		{"null-id", "application/json", `{"jsonrpc":"2.0","id":null,"result":{"ok":true}}`},
		{"wrong-id", "application/json", `{"jsonrpc":"2.0","id":8,"result":{"ok":true}}`},
		{"wrong-version", "application/json", `{"jsonrpc":"1.0","id":7,"result":{"ok":true}}`},
		{"rpc-error", "application/json", `{"jsonrpc":"2.0","id":7,"error":{"code":-32603,"message":"failed"}}`},
		{"non-object-result", "application/json", `{"jsonrpc":"2.0","id":7,"result":[1]}`},
		{"sse-progress-only", "text/event-stream", "data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\"}\n\n"},
		{"sse-unmatched-only", "text/event-stream", "data: {\"jsonrpc\":\"2.0\",\"id\":8,\"result\":{}}\n\n"},
		{"sse-non-message-only", "text/event-stream", "event: progress\ndata: " + matching + "\n\n"},
		{"sse-missing-id", "text/event-stream", "data: {\"jsonrpc\":\"2.0\",\"result\":{}}\n\n"},
		{"sse-malformed", "text/event-stream", "data: {\"jsonrpc\":\n\n"},
		{"sse-no-final-line-ending", "text/event-stream", "data: " + matching},
		{"sse-single-final-lf", "text/event-stream", "data: " + matching + "\n"},
		{"sse-single-final-crlf", "text/event-stream", "data: " + matching + "\r\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			capture := &marketWireCapture{}
			response := marketWireTestResponse(t, capture, "tools/call", "7", tc.contentType, tc.raw)
			_, err := io.Copy(io.Discard, response.Body)
			require.NoError(t, err)
			_, err = capture.result("tools/call")
			require.ErrorIs(t, err, ErrMarketRemoteResult)
		})
	}
}

func TestToolMarketWireResponseSizeLimitRejectsTruncatedCapture(t *testing.T) {
	const limit = 2 << 20
	prefix, suffix := `{"jsonrpc":"2.0","id":7,"result":{"text":"`, `"}}`
	for _, size := range []int{limit, limit + 1} {
		t.Run(fmt.Sprintf("bytes=%d", size), func(t *testing.T) {
			capture := &marketWireCapture{}
			raw := prefix + strings.Repeat("a", size-len(prefix)-len(suffix)) + suffix
			response := marketWireTestResponse(t, capture, "tools/call", "7", "application/json", raw)
			consumed, err := io.ReadAll(response.Body)
			if size == limit {
				require.NoError(t, err)
				require.Len(t, consumed, limit)
				result, err := capture.result("tools/call")
				require.NoError(t, err)
				require.Len(t, result["text"].(string), size-len(prefix)-len(suffix))
			} else {
				require.ErrorIs(t, err, ErrMarketRemoteResult)
				_, err = capture.result("tools/call")
				require.ErrorIs(t, err, ErrMarketRemoteResult)
			}
		})
	}
}

func TestToolMarketWireRetiredPageReaderCannotContaminateCurrentResponse(t *testing.T) {
	capture := &marketWireCapture{}
	old := marketWireTestResponse(t, capture, "tools/list", "1", "application/json", `{"jsonrpc":"2.0","id":1,"result":{"page":"old"}}`)
	buffer := make([]byte, 17)
	_, err := io.ReadFull(old.Body, buffer)
	require.NoError(t, err)
	currentRaw := `{"jsonrpc":"2.0","id":2,"result":{"page":"current","exact":9007199254740993}}`
	current := marketWireTestResponse(t, capture, "tools/list", "2", "application/json", currentRaw)
	// The SDK may still drain an old reader after the next page is installed.
	_, err = io.Copy(io.Discard, old.Body)
	require.NoError(t, err)
	_, err = capture.result("tools/list")
	require.ErrorIs(t, err, ErrMarketRemoteResult)
	_, err = io.Copy(io.Discard, current.Body)
	require.NoError(t, err)
	result, err := capture.result("tools/list")
	require.NoError(t, err)
	require.Equal(t, "current", result["page"])
	require.Equal(t, json.Number("9007199254740993"), result["exact"])
	// The byte ceiling is per response rather than cumulative across pages.
	largeRetired := marketWireTestResponse(t, capture, "tools/list", "3", "application/json", strings.Repeat("a", (2<<20)+1))
	newPage := marketWireTestResponse(t, capture, "tools/list", "4", "application/json", `{"jsonrpc":"2.0","id":4,"result":{"page":"after-retired"}}`)
	_, err = io.Copy(io.Discard, largeRetired.Body)
	require.NoError(t, err)
	_, err = io.Copy(io.Discard, newPage.Body)
	require.NoError(t, err)
	result, err = capture.result("tools/list")
	require.NoError(t, err)
	require.Equal(t, "after-retired", result["page"])
}

func TestToolMarketWireSameRPCIDsStayIsolatedAcrossConcurrentSessions(t *testing.T) {
	captures := []*marketWireCapture{{}, {}}
	responses := make([]*http.Response, len(captures))
	for index, capture := range captures {
		responses[index] = marketWireTestResponse(t, capture, "tools/call", "7", "application/json", fmt.Sprintf(`{"jsonrpc":"2.0","id":7,"result":{"session":%d,"exact":9007199254740993}}`, index))
	}
	start := make(chan struct{})
	errs := make(chan error, len(captures)*2)
	var wg sync.WaitGroup
	for index, capture := range captures {
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			buffer := make([]byte, 1)
			for {
				_, err := responses[index].Body.Read(buffer)
				if err == io.EOF {
					errs <- nil
					return
				}
				if err != nil {
					errs <- err
					return
				}
			}
		}()
		go func() {
			defer wg.Done()
			<-start
			// result() must safely snapshot a body concurrently being consumed.
			for range 128 {
				result, err := capture.result("tools/call")
				if err == nil && result["session"] != json.Number(fmt.Sprint(index)) {
					errs <- fmt.Errorf("session %d received another session's response", index)
					return
				}
				if err != nil && err != ErrMarketRemoteResult {
					errs <- err
					return
				}
			}
			errs <- nil
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	for index, capture := range captures {
		result, err := capture.result("tools/call")
		require.NoError(t, err)
		require.Equal(t, json.Number(fmt.Sprint(index)), result["session"])
		require.Equal(t, json.Number("9007199254740993"), result["exact"])
	}
}
