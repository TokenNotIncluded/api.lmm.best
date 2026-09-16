//go:build linux

package appcli

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// A failed unit briefly has a MainPID while systemd retries startup. Observe
// that transition without stopping a process or interpreting a read failure as
// proof that no writer exists. A stable active writer is never interrupted.
func waitIncident343Quiescent(ctx context.Context, read func(context.Context) (map[string]string, error), sleep func(context.Context, time.Duration) error) error {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	for attempt := 0; attempt < 90; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		state, err := read(ctx)
		if err != nil {
			return fmt.Errorf("read recovery service state: %w", err)
		}
		pid, err := strconv.Atoi(state["MainPID"])
		if err != nil || pid < 0 || pid == 1 {
			return errors.New("invalid recovery service PID evidence")
		}
		active := state["ActiveState"]
		if pid == 0 && (active == "failed" || active == "activating" || active == "inactive") {
			return nil
		}
		if active != "activating" {
			return errors.New("recovery will not interrupt a running application writer")
		}
		switch state["SubState"] {
		case "start-pre", "start", "start-post", "auto-restart":
			// Read-only wait for the failed startup to exit on its own.
		default:
			return errors.New("unexpected recovery service startup state")
		}
		if err := sleep(ctx, 500*time.Millisecond); err != nil {
			return err
		}
	}
	return errors.New("candidate did not leave startup within the bounded recovery wait")
}

func sleepIncident343(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
