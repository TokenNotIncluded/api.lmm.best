// Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later
package controller

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// These are controller boundary tests, not database or billing acceptance.
func TestAssistantBI04RelayRequiresCompleteTerminal(t *testing.T) {
	prefix := "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n"
	for _, tail := range []string{
		"", "data: [DONE]", "data: [DONE]\n", "data: [DONE]\r", "data: [DONE]\r\n",
		"data: {\"choices\":[{\"finish_reason\":\"stop\"}]}\n\n",
		"data: {\"usage\":{\"total_tokens\":12}}\n\n",
	} {
		t.Run(tail, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			writer := newAssistantStreamingRelayWriter(c.Writer, nil)
			if _, err := writer.Write([]byte(prefix + tail)); err != nil {
				t.Fatal(err)
			}
			body, err := writer.responseBody()
			if err == nil || !strings.Contains(err.Error(), "ended before completion") || body != nil {
				t.Fatalf("incomplete stream became a response: body=%q err=%v", body, err)
			}
		})
	}
}

func TestAssistantBI04CompletedStreamIgnoresLateEvents(t *testing.T) {
	for _, ending := range []string{"\n", "\r", "\r\n"} {
		t.Run(ending, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			writer := newAssistantStreamingRelayWriter(c.Writer, nil)
			data := strings.ReplaceAll("data: {\"choices\":[{\"delta\":{\"content\":\"你好🌍\"}}]}\n\ndata: [DONE]\n\ndata: {\"error\":\"late\"}\n\n", "\n", ending)
			for _, b := range []byte(data) {
				if _, err := writer.Write([]byte{b}); err != nil {
					t.Fatal(err)
				}
			}
			body, err := writer.responseBody()
			if err != nil || !strings.Contains(string(body), "你好🌍") {
				t.Fatalf("complete stream failed: body=%q err=%v", body, err)
			}
		})
	}
}

func TestAssistantBI04RelayCannotResetAcceptedBodyForReplay(t *testing.T) {
	for _, body := range []string{
		"data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n",
		"data: {\"usage\":{\"total_tokens\":12}}\n\n",
		"data: {\"choices\":",
	} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		writer := newAssistantStreamingRelayWriter(c.Writer, nil)
		if _, err := writer.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
		if err := writer.ResetForRelayRetry(); err == nil {
			t.Fatal("accepted stream bytes authorized another upstream attempt")
		}
		if string(writer.body.Bytes()) != body {
			t.Fatal("failed reset discarded the previous attempt's evidence")
		}
	}
}

type assistantBI04FailingWriter struct {
	gin.ResponseWriter
	failWrite bool
	failFlush bool
	writes    int
}

func (w *assistantBI04FailingWriter) Write(data []byte) (int, error) {
	w.writes++
	if w.failWrite {
		return 0, io.ErrClosedPipe
	}
	return w.ResponseWriter.Write(data)
}

func (w *assistantBI04FailingWriter) FlushError() error {
	if w.failFlush {
		return io.ErrClosedPipe
	}
	w.ResponseWriter.Flush()
	return nil
}

func TestAssistantBI04FailedTerminalCancelsAndFencesLateWrites(t *testing.T) {
	for _, failure := range []string{"write", "flush"} {
		t.Run(failure, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			writer := &assistantBI04FailingWriter{ResponseWriter: c.Writer}
			session := newAssistantStreamSession(writer)
			if err := session.start(); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			stop := session.watch(ctx, cancel, time.Hour, nil)
			defer func() { cancel(); stop() }()
			writer.failWrite = failure == "write"
			writer.failFlush = failure == "flush"
			if err := session.finish([]byte(`{"content":"complete"}`)); !errors.Is(err, io.ErrClosedPipe) {
				t.Fatalf("terminal failure was lost: %v", err)
			}
			if !errors.Is(ctx.Err(), context.Canceled) {
				t.Fatal("failed terminal left request-owned work running")
			}
			_, finished := session.startedAndFinished()
			if !finished {
				t.Fatal("failed terminal was not latched")
			}
			writes := writer.writes
			if err := session.fail(503, "ASSISTANT_RUN_FAILED", "late failure"); err != nil {
				t.Fatal(err)
			}
			if err := session.finish([]byte(`{}`)); err != nil {
				t.Fatal(err)
			}
			if writer.writes != writes {
				t.Fatal("a late terminal appended to the failed frame")
			}
		})
	}
}

func TestAssistantBI04BufferedJSONRemainsSupportedWithoutUsage(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	writer := newAssistantStreamingRelayWriter(c.Writer, nil)
	if _, err := writer.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"complete"}}]}`)); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		body, err := writer.responseBody()
		if err != nil || !strings.Contains(string(body), "complete") {
			t.Fatalf("buffered response changed on read %d: body=%q err=%v", i, body, err)
		}
	}
}
