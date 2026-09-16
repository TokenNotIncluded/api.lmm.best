//go:build linux

package appcli

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestIncident343WaitObservesStartupExitWithoutMutation(t *testing.T) {
	reads, sleeps := 0, 0
	err := waitIncident343Quiescent(context.Background(), func(ctx context.Context) (map[string]string, error) {
		reads++
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("service reads must have a deadline")
		}
		if reads == 1 {
			return map[string]string{"MainPID": "200", "ActiveState": "activating", "SubState": "start-post"}, nil
		}
		return map[string]string{"MainPID": "0", "ActiveState": "activating", "SubState": "auto-restart"}, nil
	}, func(ctx context.Context, duration time.Duration) error {
		sleeps++
		if duration != 500*time.Millisecond {
			t.Fatal("unexpected interval")
		}
		return nil
	})
	if err != nil || reads != 2 || sleeps != 1 {
		t.Fatalf("err=%v reads=%d sleeps=%d", err, reads, sleeps)
	}
}

func TestIncident343WaitRejectsStableWriterAndInvalidEvidence(t *testing.T) {
	for _, state := range []map[string]string{
		{"MainPID": "200", "ActiveState": "active", "SubState": "running"},
		{"MainPID": "0", "ActiveState": "active", "SubState": "running"},
		{"MainPID": "200", "ActiveState": "deactivating", "SubState": "stop"},
		{"MainPID": "200", "ActiveState": "activating", "SubState": "unknown"},
		{"MainPID": "-1", "ActiveState": "failed"},
		{"MainPID": "1", "ActiveState": "failed"},
		{"MainPID": "not-a-pid", "ActiveState": "failed"},
		{"ActiveState": "failed"},
		{"MainPID": "0"},
	} {
		err := waitIncident343Quiescent(context.Background(), func(context.Context) (map[string]string, error) { return state, nil },
			func(context.Context, time.Duration) error { t.Fatal("invalid/stable state must not retry"); return nil })
		if err == nil {
			t.Fatalf("accepted state: %#v", state)
		}
	}
}

func TestIncident343WaitAcceptsAlreadyStoppedCandidate(t *testing.T) {
	for _, active := range []string{"failed", "inactive", "activating"} {
		err := waitIncident343Quiescent(context.Background(), func(context.Context) (map[string]string, error) {
			return map[string]string{"MainPID": "0", "ActiveState": active}, nil
		}, func(context.Context, time.Duration) error { t.Fatal("stopped unit must not wait"); return nil })
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestIncident343WaitIsBoundedAndPreservesReadErrors(t *testing.T) {
	calls := 0
	err := waitIncident343Quiescent(context.Background(), func(context.Context) (map[string]string, error) {
		calls++
		return map[string]string{"MainPID": "200", "ActiveState": "activating", "SubState": "start"}, nil
	}, func(context.Context, time.Duration) error { return nil })
	if err == nil || calls != 90 {
		t.Fatalf("unbounded wait: %v reads=%d", err, calls)
	}
	sentinel := errors.New("synthetic read error")
	err = waitIncident343Quiescent(context.Background(), func(context.Context) (map[string]string, error) { return nil, sentinel },
		func(context.Context, time.Duration) error { t.Fatal("read error must not retry"); return nil })
	if !errors.Is(err, sentinel) {
		t.Fatal("read error lost", err)
	}
}

func TestIncident343WaitHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := waitIncident343Quiescent(ctx, func(context.Context) (map[string]string, error) {
		t.Fatal("cancelled context read")
		return nil, nil
	}, sleepIncident343)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := sleepIncident343(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
