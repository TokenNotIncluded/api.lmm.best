package common

import (
	"errors"
	"io"
	"net/http/httptest"
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
