package middleware

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestWaitAdminAuditsIsBounded(t *testing.T) {
	if err := WaitAdminAudits(context.Background()); err != nil {
		t.Fatalf("empty audit wait failed: %v", err)
	}

	adminAuditTasks.Add(1)
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if err := WaitAdminAudits(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("bounded audit wait error = %v, want deadline exceeded", err)
	}
	adminAuditTasks.Done()
	if err := WaitAdminAudits(context.Background()); err != nil {
		t.Fatalf("completed audit wait failed: %v", err)
	}
}
