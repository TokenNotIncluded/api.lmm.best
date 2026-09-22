package helper

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStreamScannerHandler_HTTPEventBoundaryAndCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	firstField := make(chan struct{})
	finishEvent := make(chan struct{})
	released := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(released)
		fmt.Fprint(w, "data: {}\n")
		w.(http.Flusher).Flush()
		close(firstField)
		select {
		case <-finishEvent:
		case <-r.Context().Done():
			return
		}
		fmt.Fprint(w, "data: [DONE]\ndata:\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer upstream.Close()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, upstream.URL, nil)
	require.NoError(t, err)
	response, err := upstream.Client().Do(request)
	require.NoError(t, err)
	c, _, info := setupStreamTest(t, strings.NewReader(""))
	c.Request = c.Request.WithContext(ctx)
	data := make(chan string, 4)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		StreamScannerHandler(c, response, info, func(value string, _ *StreamResult) {
			data <- value
		})
	}()
	<-firstField
	select {
	case value := <-data:
		cancel()
		t.Fatalf("dispatched before blank-line boundary: %q", value)
	case <-time.After(100 * time.Millisecond):
	}
	close(finishEvent)
	select {
	case value := <-data:
		require.Equal(t, "{}\n[DONE]\n", value)
	case <-ctx.Done():
		t.Fatal("complete event was not delivered")
	}
	cancel()
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("scanner did not return after cancellation")
	}
	select {
	case <-released:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream connection did not release")
	}
}
