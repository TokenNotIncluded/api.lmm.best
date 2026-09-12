package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestShutdownRefundBarrier(t *testing.T) {
	for _, tc := range []struct {
		name     string
		complete bool
		err      error
	}{
		{"success", true, nil},
		{"financial_failure", true, errors.New("financial reconciliation required")},
		{"timeout", false, context.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var order []string
			err := shutdownRuntime(shutdownSteps{
				markUnready:  func() { order = append(order, "unready") },
				stopLoops:    func() { order = append(order, "stop") },
				shutdownHTTP: func(context.Context) error { order = append(order, "http"); return nil },
				waitLoops:    func(context.Context) error { order = append(order, "loops"); return nil },
				waitRefunds: func(ctx context.Context) (bool, error) {
					_, ok := ctx.Deadline()
					require.True(t, ok)
					order = append(order, "refunds")
					return tc.complete, tc.err
				},
				flushBatch: func() { order = append(order, "batch") },
				closeDB:    func() error { order = append(order, "db"); return nil },
			}, time.Second, time.Second)
			want := []string{"unready", "stop", "http", "loops", "refunds"}
			if tc.complete {
				want = append(want, "batch", "db")
			}
			require.Equal(t, want, order)
			if tc.err == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, tc.err)
			}
		})
	}
}
