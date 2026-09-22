package helper

import (
	"bufio"
	"github.com/gin-gonic/gin"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/iotest"

	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/stretchr/testify/require"
)

func TestSSEScannerAndWriterRoundTrip(t *testing.T) {
	for _, ending := range []string{"\n", "\r\n", "\r"} {
		t.Run(strings.ReplaceAll(ending, "\r", "CR"), func(t *testing.T) {
			body := "data: hello\ndata: world\n\ndata: {}\ndata: []\n\ndata:\ndata: tail\ndata:\n\ndata: [DONE]\ndata: more\n\ndata: :payload\n\ndata: [DONE]\n\n"
			c, response, info := setupStreamTest(t, iotest.OneByteReader(strings.NewReader(strings.ReplaceAll(body, "\n", ending))))
			var got []string
			var writeErr error
			StreamScannerHandler(c, response, info, func(data string, result *StreamResult) {
				got = append(got, data)
				if err := StringData(c, data); err != nil {
					writeErr = err
					result.Stop(err)
				}
			})
			require.NoError(t, writeErr)
			want := []string{"hello\nworld", "{}\n[]", "\ntail\n", "[DONE]\nmore", ":payload"}
			require.Equal(t, want, got)
			require.Equal(t, len(want), info.ReceivedResponseCount)
			require.Equal(t, relaycommon.StreamEndReasonDone, info.StreamStatus.EndReason)
		})
	}
}

// Exercise the actual writer and then independently parse its event fields.
// This catches unprefixed lines and accidental event/field injection on CR.
func TestSSEWriterKeepsEveryPayloadLineInsideData(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/", nil)
	for _, data := range []string{"a\nb", "a\rb", "a\r\nb", "\nx\n", ":not-a-comment", "a\revent: injected"} {
		// Each call appends exactly one event to the actual Gin writer.
		require.NoError(t, StringData(c, data))
	}
	scanner := bufio.NewScanner(strings.NewReader(w.Body.String()))
	var events []string
	var fields []string
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			events = append(events, strings.Join(fields, "\n"))
			fields = nil
			continue
		}
		require.True(t, strings.HasPrefix(line, "data: "), line)
		fields = append(fields, strings.TrimPrefix(line, "data: "))
	}
	require.NoError(t, scanner.Err())
	require.Empty(t, fields)
	require.Equal(t, []string{"a\nb", "a\nb", "a\nb", "\nx\n", ":not-a-comment", "a\nevent: injected"}, events)
}
