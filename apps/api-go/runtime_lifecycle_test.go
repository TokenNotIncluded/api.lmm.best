package main

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestRuntimeLoopsStopCancelsAndClosesAdmission(t *testing.T) {
	loops := newRuntimeLoops(context.Background())
	started := make(chan struct{})
	finished := make(chan struct{})
	if !loops.Go(func(ctx context.Context) {
		close(started)
		<-ctx.Done()
		close(finished)
	}) {
		t.Fatal("initial loop was not admitted")
	}
	<-started

	loops.Stop()
	if loops.Go(func(context.Context) {}) {
		t.Fatal("loop admitted after Stop")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := loops.Wait(ctx); err != nil {
		t.Fatalf("Wait after cancellation: %v", err)
	}
	select {
	case <-finished:
	default:
		t.Fatal("Wait returned before cancelled loop finished")
	}
}

func TestRuntimeLoopsWaitHonorsDeadline(t *testing.T) {
	loops := newRuntimeLoops(context.Background())
	release := make(chan struct{})
	if !loops.Go(func(context.Context) { <-release }) {
		t.Fatal("loop was not admitted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := loops.Wait(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait error = %v, want context.Canceled", err)
	}
	close(release)
	loops.Stop()
}

func TestShutdownRuntimeOrderingAndTimeout(t *testing.T) {
	var mu sync.Mutex
	var order []string
	record := func(step string) {
		mu.Lock()
		defer mu.Unlock()
		order = append(order, step)
	}

	err := shutdownRuntime(shutdownSteps{
		markUnready: func() { record("unready") },
		stopLoops:   func() { record("cancel") },
		drainSockets: func(ctx context.Context) error {
			record("websocket")
			if _, ok := ctx.Deadline(); !ok {
				t.Fatal("WebSocket drain context has no deadline")
			}
			return nil
		},
		shutdownHTTP: func(ctx context.Context) error {
			record("http")
			if _, ok := ctx.Deadline(); !ok {
				t.Fatal("HTTP shutdown context has no deadline")
			}
			return nil
		},
		waitLoops: func(ctx context.Context) error {
			record("wait")
			<-ctx.Done()
			return ctx.Err()
		},
		flushQuota:  func() { record("quota") },
		flushBatch:  func() { record("batch") },
		flushPerf:   func() { record("perf") },
		closeValkey: func() error { record("valkey"); return nil },
		closeDB:     func() error { record("db"); return nil },
	}, time.Second, time.Nanosecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown error = %v, want deadline exceeded", err)
	}

	want := []string{"unready", "cancel", "websocket", "http", "wait", "quota", "batch", "perf", "valkey", "db"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("shutdown order = %v, want %v", order, want)
	}
}

func TestShutdownRuntimeSharesOneTotalDeadline(t *testing.T) {
	var httpSawExpired bool
	var waitSawExpired bool
	err := shutdownRuntime(shutdownSteps{
		drainSockets: func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		},
		shutdownHTTP: func(ctx context.Context) error {
			httpSawExpired = errors.Is(ctx.Err(), context.DeadlineExceeded)
			return ctx.Err()
		},
		waitLoops: func(ctx context.Context) error {
			waitSawExpired = errors.Is(ctx.Err(), context.DeadlineExceeded)
			return ctx.Err()
		},
	}, time.Nanosecond, time.Second)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown error = %v, want deadline exceeded", err)
	}
	if !httpSawExpired || !waitSawExpired {
		t.Fatalf("shared deadline not observed: http=%v wait=%v", httpSawExpired, waitSawExpired)
	}
}

func TestConfiguredShutdownTimeoutFitsSystemdStopBudget(t *testing.T) {
	tests := []struct {
		name    string
		seconds int
		want    time.Duration
	}{
		{name: "default", seconds: 0, want: 30 * time.Second},
		{name: "negative", seconds: -1, want: 30 * time.Second},
		{name: "custom", seconds: 20, want: 20 * time.Second},
		{name: "clamped", seconds: 120, want: 40 * time.Second},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := configuredShutdownTimeout(test.seconds); got != test.want {
				t.Fatalf("configuredShutdownTimeout(%d) = %s, want %s", test.seconds, got, test.want)
			}
		})
	}
	if maxHTTPShutdownTimeout >= 45*time.Second {
		t.Fatal("process drain budget must leave margin below systemd TimeoutStopSec")
	}
}
