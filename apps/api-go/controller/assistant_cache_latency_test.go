package controller

import (
	"context"
	"testing"
	"time"
)

func TestAssistantCacheGateWaitIsBoundedWithoutCancellingRequest(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	release, ok := acquireAssistantCacheGate(ctx, t.Name())
	if !ok {
		t.Fatal("first request could not acquire cache gate")
	}
	defer release()

	started := time.Now()
	waiterRelease, acquired := acquireAssistantCacheGate(ctx, t.Name())
	defer waiterRelease()
	elapsed := time.Since(started)
	if acquired {
		t.Fatal("waiter acquired a gate whose owner has not finished")
	}
	if elapsed < assistantCacheGateMaxWait || elapsed >= time.Second {
		t.Fatalf("cache wait must use its own short deadline, got %s", elapsed)
	}
	if ctx.Err() != nil {
		t.Fatal("cache wait cancelled the parent request")
	}
	t.Logf("contended cache wait=%s; parent request still active", elapsed)

	release()
	release() // ownership cleanup stays idempotent
	nextRelease, acquired := acquireAssistantCacheGate(ctx, t.Name())
	defer nextRelease()
	if !acquired {
		t.Fatal("expired waiter leaked a gate or cancelled the next request")
	}
}

func TestAssistantCacheGateStillCoalescesFastCompletion(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	release, acquired := acquireAssistantCacheGate(ctx, t.Name())
	if !acquired {
		t.Fatal("first request could not acquire gate")
	}
	defer release()
	timer := time.AfterFunc(20*time.Millisecond, release)
	defer timer.Stop()
	nextRelease, acquired := acquireAssistantCacheGate(ctx, t.Name())
	defer nextRelease()
	if !acquired {
		t.Fatal("fast completed request should still be coalesced")
	}
}

func TestAssistantCacheGateHonorsEarlierParentDeadline(t *testing.T) {
	release, acquired := acquireAssistantCacheGate(context.Background(), t.Name())
	if !acquired {
		t.Fatal("could not acquire owner gate")
	}
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	waiterRelease, acquired := acquireAssistantCacheGate(ctx, t.Name())
	defer waiterRelease()
	if acquired || ctx.Err() != context.DeadlineExceeded {
		t.Fatal("cache wait did not honor parent cancellation")
	}
}

func TestAssistantCacheGateRejectsAlreadyCancelledRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	release, acquired := acquireAssistantCacheGate(ctx, t.Name())
	defer release()
	if acquired {
		t.Fatal("already cancelled request acquired an unused gate")
	}
}
