package helper

import (
	"strings"
	"testing"

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
