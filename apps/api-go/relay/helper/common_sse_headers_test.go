package helper

import (
	"net/http/httptest"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/gin-gonic/gin"
)

func TestSetEventStreamHeadersPreventsIntermediaryTransforms(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)

	SetEventStreamHeaders(ctx)

	if got, want := recorder.Header().Get("Content-Type"), "text/event-stream"; got != want {
		t.Fatalf("Content-Type = %q, want %q", got, want)
	}
	if got, want := recorder.Header().Get("Cache-Control"), "no-cache, no-transform"; got != want {
		t.Fatalf("Cache-Control = %q, want %q", got, want)
	}
	if got, want := recorder.Header().Get("X-Accel-Buffering"), "no"; got != want {
		t.Fatalf("X-Accel-Buffering = %q, want %q", got, want)
	}

	// CustomEvent is used by several stream helpers. Rendering an event must not
	// downgrade the no-transform directive that protects a long-lived SSE body
	// from intermediary compression/re-encoding.
	if err := (common.CustomEvent{Data: "data: ok"}).Render(ctx.Writer); err != nil {
		t.Fatalf("render CustomEvent: %v", err)
	}
	if got, want := recorder.Header().Get("Cache-Control"), "no-cache, no-transform"; got != want {
		t.Fatalf("Cache-Control after render = %q, want %q", got, want)
	}
}
