package common

import (
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

type failingEventWriter struct {
	*httptest.ResponseRecorder
	failAfter int
	writes    int
}

type shortEventWriter struct{ *httptest.ResponseRecorder }

func (w shortEventWriter) Write(data []byte) (int, error) {
	return w.ResponseRecorder.Write(data[:len(data)-1])
}

func (w *failingEventWriter) Write(data []byte) (int, error) {
	if w.writes >= w.failAfter {
		return 0, io.ErrClosedPipe
	}
	w.writes++
	return w.ResponseRecorder.Write(data)
}

func TestCustomEventPropagatesWriteErrors(t *testing.T) {
	for _, failAfter := range []int{0, 1} {
		writer := &failingEventWriter{ResponseRecorder: httptest.NewRecorder(), failAfter: failAfter}
		if err := (CustomEvent{Data: "data: value"}).Render(writer); !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("failAfter=%d, error=%v, want closed pipe", failAfter, err)
		}
	}
	writer := shortEventWriter{httptest.NewRecorder()}
	if err := (CustomEvent{Data: "data: value"}).Render(writer); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("short-write error=%v, want io.ErrShortWrite", err)
	}
}

func TestCustomEventPreservesSSEBytes(t *testing.T) {
	for _, tc := range []struct{ data, want string }{
		{data: "data: value", want: "data: value\n\n"},
		{data: "event: response.completed\n", want: "event: response.completed\n"},
		{data: "data: first\ndata: second", want: "data: first\ndata: second\n\n"},
		{data: "data: first\r\nsecond\r", want: "data: first\\r\nsecond\\r\n\n"},
		{data: ": PING\n\n", want: ": PING\n\n"},
		{data: "", want: ""},
	} {
		writer := httptest.NewRecorder()
		if err := (CustomEvent{Data: tc.data}).Render(writer); err != nil {
			t.Fatal(err)
		}
		if got := writer.Body.String(); got != tc.want {
			t.Fatalf("bytes=%q, want=%q", got, tc.want)
		}
	}
}

func TestCustomEventRejectsNonStringDataBeforeWriting(t *testing.T) {
	for _, data := range []any{nil, 42, []byte("data: value")} {
		writer := httptest.NewRecorder()
		if err := (CustomEvent{Data: data}).Render(writer); err == nil {
			t.Fatalf("data type %T: expected an error", data)
		}
		if writer.Body.Len() != 0 || len(writer.Header()) != 0 {
			t.Fatalf("data type %T: invalid input changed the response", data)
		}
	}
}

func TestCustomEventHeadersAreIndependent(t *testing.T) {
	for _, header := range []string{"Content-Type", "Cache-Control"} {
		t.Run(header, func(t *testing.T) {
			first, second := httptest.NewRecorder(), httptest.NewRecorder()
			(CustomEvent{}).WriteContentType(first)
			(CustomEvent{}).WriteContentType(second)
			want := second.Header().Get(header)
			// Restore the value so this test also leaves the old implementation clean.
			defer func() { first.Header()[header][0] = want }()
			first.Header()[header][0] = "changed"
			if got := second.Header().Get(header); got != want {
				t.Fatalf("other response's %s=%q, want=%q", header, got, want)
			}
		})
	}
}

func TestCustomEventPreservesCacheControl(t *testing.T) {
	for _, policy := range []string{"no-cache, no-transform", ""} {
		writer := httptest.NewRecorder()
		writer.Header().Set("Cache-Control", policy)
		if err := (CustomEvent{Data: "data: value"}).Render(writer); err != nil {
			t.Fatal(err)
		}
		if got := writer.Header().Get("Cache-Control"); got != policy {
			t.Fatalf("Cache-Control=%q, want=%q", got, policy)
		}
	}
}

func TestCustomEventConcurrentStreams(t *testing.T) {
	for stream := 0; stream < 8; stream++ {
		t.Run("stream", func(t *testing.T) {
			t.Parallel()
			writer := httptest.NewRecorder()
			for event := 0; event < 100; event++ {
				if err := (CustomEvent{Data: "data: value"}).Render(writer); err != nil {
					t.Fatal(err)
				}
				if got := writer.Header().Get("Content-Type"); got != "text/event-stream" {
					t.Fatalf("Content-Type=%q", got)
				}
				writer.Header()["Content-Type"][0] = "private to this response"
			}
			if got, want := writer.Body.String(), strings.Repeat("data: value\n\n", 100); got != want {
				t.Fatalf("stream bytes differ: got %d bytes, want %d", len(got), len(want))
			}
		})
	}
}
