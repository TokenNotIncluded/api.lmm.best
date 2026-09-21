//go:build linux

package deployslots

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Observe the actual waiter descriptor, rather than assuming a sleep means
// flock has started. These Linux-only tests use private temporary files only.
func waitForStoreLockWaiter(t *testing.T, held *os.File) {
	t.Helper()
	identity, err := held.Stat()
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		entries, err := os.ReadDir("/proc/self/fd")
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		for _, entry := range entries {
			info, err := os.Stat(filepath.Join("/proc/self/fd", entry.Name()))
			if err == nil && os.SameFile(identity, info) {
				count++
			}
		}
		if count >= 2 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("store did not open the contended lock")
}

func TestFileStoreLockIdentityAfterWait(t *testing.T) {
	for _, kind := range []string{"retained", "replaced", "removed", "symlink", "hardlink", "permissions"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			must := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			must(os.Chmod(root, 0700))
			store, err := OpenFileStore(root)
			must(err)
			pair := Pair{Go: Artifact{"1", strings.Repeat("a", 40), strings.Repeat("a", 64)},
				Web: Artifact{"1", strings.Repeat("b", 40), strings.Repeat("b", 64)}}
			initial, err := store.Initialize(context.Background(), Receipt{"baseline", strings.Repeat("a", 64), "CONFIRMED", pair})
			must(err)
			statePath := filepath.Join(root, "state.json")
			before, err := os.ReadFile(statePath)
			must(err)
			lockPath := filepath.Join(root, "state.lock")
			held, err := os.OpenFile(lockPath, os.O_RDWR, 0600)
			must(err)
			defer held.Close()
			must(syscall.Flock(int(held.Fd()), syscall.LOCK_EX))
			defer syscall.Flock(int(held.Fd()), syscall.LOCK_UN)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			type outcome struct {
				err    error
				called bool
			}
			completed := make(chan outcome, 1)
			go func() {
				called := false
				_, err := store.Update(ctx, initial.Generation, func(current State) (State, error) {
					called = true
					candidate := pair
					candidate.Go.Version = "2"
					return current.Stage(current.Generation, "update", strings.Repeat("c", 64), candidate)
				})
				completed <- outcome{err, called}
			}()
			waitForStoreLockWaiter(t, held)
			switch kind {
			case "replaced":
				must(os.Remove(lockPath))
				replacement, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
				must(err)
				defer replacement.Close()
				// A second holder owns the CURRENT lock throughout the test.
				must(syscall.Flock(int(replacement.Fd()), syscall.LOCK_EX))
				defer syscall.Flock(int(replacement.Fd()), syscall.LOCK_UN)
			case "removed":
				must(os.Remove(lockPath))
			case "symlink":
				must(os.Remove(lockPath))
				must(os.Symlink("/dev/null", lockPath))
			case "hardlink":
				must(os.Link(lockPath, filepath.Join(root, "lock-alias")))
			case "permissions":
				must(os.Chmod(lockPath, 0644))
			}
			must(syscall.Flock(int(held.Fd()), syscall.LOCK_UN))
			var result outcome
			select {
			case result = <-completed:
			case <-ctx.Done():
				t.Fatal("store did not finish after the original lock was released")
			}
			after, err := os.ReadFile(statePath)
			must(err)
			if kind == "retained" {
				if result.err != nil || !result.called || bytes.Equal(before, after) {
					t.Fatal("unchanged lock should permit the transition", result.err)
				}
				return
			}
			if result.err == nil || result.called || !bytes.Equal(before, after) {
				t.Fatalf("changed lock allowed a state transition: error=%v callback=%v changed=%v",
					result.err, result.called, !bytes.Equal(before, after))
			}
			if errors.Is(result.err, context.DeadlineExceeded) {
				t.Fatal("lock identity was not rejected promptly")
			}
		})
	}
}
