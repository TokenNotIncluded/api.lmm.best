package helper

import (
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/stretchr/testify/require"
)

func TestStreamScannerHandler_EventBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want []string
	}{
		{"plain multiline", "data: hello\ndata: world\n\n", []string{"hello\nworld"}},
		{"complete JSON first", "data: {}\ndata: {}\n\n", []string{"{}\n{}"}},
		{"empty fields", "data:\ndata\ndata: tail\n\n", []string{"\n\ntail"}},
		{"empty event", "data:\n\n", []string{""}},
		{"embedded done", "data: first\ndata: [DONE]\ndata: last\n\n", []string{"first\n[DONE]\nlast"}},
		{"done first field", "data: [DONE]\ndata: last\n\n", []string{"[DONE]\nlast"}},
		{"colon payload", "data: :business\n\n", []string{":business"}},
		{"preserve spaces", "data:  value \n\n", []string{" value "}},
		{"incomplete EOF", "data: partial\n", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, resp, info := setupStreamTest(t, strings.NewReader(tc.body))
			var got []string
			StreamScannerHandler(c, resp, info, func(data string, _ *StreamResult) {
				got = append(got, data)
			})
			require.Equal(t, tc.want, got)
		})
	}
}

func TestStreamScannerHandler_EventSizeIncludesEmptyFieldSeparator(t *testing.T) {
	previous := constant.MaxResponseBodyMB
	constant.MaxResponseBodyMB = 1
	t.Cleanup(func() { constant.MaxResponseBodyMB = previous })
	const limit = 1 << 20
	// Each physical line is below the scanner limit; only event assembly
	// determines whether the extra empty field's LF exceeds the total budget.
	body := "data: " + strings.Repeat("a", limit/2-1) + "\ndata: " + strings.Repeat("b", limit/2) + "\n"
	for _, extra := range []bool{false, true} {
		t.Run(map[bool]string{false: "exact limit", true: "one byte over"}[extra], func(t *testing.T) {
			input := body
			if extra {
				input += "data:\n"
			}
			c, resp, info := setupStreamTest(t, strings.NewReader(input+"\n"))
			var got []string
			StreamScannerHandler(c, resp, info, func(value string, _ *StreamResult) { got = append(got, value) })
			if extra {
				require.Empty(t, got)
				require.ErrorIs(t, info.StreamStatus.EndError, ErrSSEEventTooLarge)
			} else {
				require.Len(t, got, 1)
				require.Len(t, got[0], limit)
			}
		})
	}
}
