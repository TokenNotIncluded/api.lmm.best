package xunfei

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/relay/helper"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

type xunfeiCommitFailureWriter struct {
	*httptest.ResponseRecorder
}

func (w *xunfeiCommitFailureWriter) FlushError() error { return io.ErrClosedPipe }

func TestXunfeiHeaderCommitFailureClosesSocketBeforeSubmissionAndProducer(t *testing.T) {
	var submissions atomic.Int32
	closed := make(chan struct{})
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := upgrader.Upgrade(writer, request, nil)
		if err != nil {
			return
		}
		defer connection.Close()
		defer close(closed)
		if _, _, err := connection.ReadMessage(); err == nil {
			submissions.Add(1)
		}
	}))
	defer server.Close()
	c, _ := gin.CreateTestContext(&xunfeiCommitFailureWriter{ResponseRecorder: httptest.NewRecorder()})
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	authURL := "ws" + strings.TrimPrefix(server.URL, "http")
	data, done, err := xunfeiMakeRequestWithContext(c, context.Background(), dto.GeneralOpenAIRequest{}, "general", authURL, "app", func() error {
		return helper.CommitEventStreamHeaders(c)
	})
	require.ErrorIs(t, err, io.ErrClosedPipe)
	require.Nil(t, data, "failed commit must not start the frame producer")
	require.Nil(t, done)
	require.True(t, helper.HTTPStreamDownstreamFailed(c))
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("failed header commit left the accepted upstream websocket open")
	}
	require.Zero(t, submissions.Load(), "failed commit must not submit a billable request")
}
