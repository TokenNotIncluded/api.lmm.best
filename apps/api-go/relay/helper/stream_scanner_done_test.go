package helper

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStreamScannerHandler_DONEMarkersDoNotBecomePayloads(t *testing.T) {
	const payload = `{"a":1}`
	markers := []struct {
		name  string
		value string
	}{
		{"bare", "[DONE]"},
		{"bare whitespace", " \t[DONE]\t "},
		{"prefixed", "data: [DONE]"},
		{"prefixed without space", "data:[DONE]"},
		{"prefixed whitespace", "data: \t[DONE]\t "},
	}
	for _, marker := range markers {
		for _, withPayload := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/payload=%t", marker.name, withPayload), func(t *testing.T) {
				body := marker.value + "\n\ndata: {\"must_not_be_read\":true}\n"
				if withPayload {
					body = "data: " + payload + "\n\n" + body
				}
				c, resp, info := setupStreamTest(t, strings.NewReader(body))
				var mu sync.Mutex
				var received []string
				StreamScannerHandler(c, resp, info, func(data string, _ *StreamResult) {
					mu.Lock()
					received = append(received, data)
					mu.Unlock()
				})
				mu.Lock()
				defer mu.Unlock()
				if withPayload {
					require.Equal(t, []string{payload}, received)
					require.Equal(t, 1, info.ReceivedResponseCount)
				} else {
					require.Empty(t, received)
					require.Zero(t, info.ReceivedResponseCount)
				}
				require.NotNil(t, info.StreamStatus)
				require.Contains(t, info.StreamStatus.Summary(), "reason=done")
			})
		}
	}
}
