package handoff

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// A duplicate is a read of an existing durable result, not a state change.
// Keep the inode as well as the contents: equal bytes alone hide a rewrite.
func TestDuplicateReceiveDoesNotRewrite(t *testing.T) {
	for _, state := range []string{"pending", "claimed", "uncertain", "done"} {
		t.Run(state, func(t *testing.T) {
			s := newStore(t)
			payload := []byte("synthetic-event")
			task, err := s.Receive("duplicate", payload)
			if err != nil {
				t.Fatal(err)
			}
			if state != "pending" {
				owner, err := s.Takeover("test-owner")
				if err != nil {
					t.Fatal(err)
				}
				task, err = s.Claim(owner, task.Key)
				if err != nil {
					t.Fatal(err)
				}
				if state == "uncertain" || state == "done" {
					task, err = s.Start(owner, task)
					if err != nil {
						t.Fatal(err)
					}
				}
				if state == "done" {
					task, err = s.Complete(owner, task, Receipt{task.Key, task.Digest, "synthetic-receipt"})
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			path := filepath.Join(s.dir, "tasks.json")
			before, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 3; i++ {
				got, err := s.Receive(task.Key, payload)
				if err != nil || !reflect.DeepEqual(got, task) {
					t.Fatalf("duplicate changed result: got=%+v err=%v", got, err)
				}
				after, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				if !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) {
					t.Fatal("duplicate rewrote the durable snapshot")
				}
			}
		})
	}
}

func TestDuplicateReceiveStillChecksStorage(t *testing.T) {
	for _, mode := range []string{"closed", "broken", "corrupt", "missing"} {
		t.Run(mode, func(t *testing.T) {
			s := newStore(t)
			if _, err := s.Receive("same", []byte("payload")); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(s.dir, "tasks.json")
			switch mode {
			case "closed":
				if err := s.Close(); err != nil {
					t.Fatal(err)
				}
			case "broken":
				s.broken = true // An earlier rename has an unknown commit outcome.
			case "corrupt":
				if err := os.WriteFile(path, []byte("not-json"), 0600); err != nil {
					t.Fatal(err)
				}
			case "missing":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.Receive("same", []byte("payload")); err == nil {
				t.Fatal("duplicate bypassed failed storage")
			}
		})
	}
}

func TestReceiveConflictDoesNotChangeSnapshot(t *testing.T) {
	s := newStore(t)
	if _, err := s.Receive("same", []byte("original")); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.dir, "tasks.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Receive("same", []byte("different")); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected conflict, got %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("conflict changed stored state: %v", err)
	}
}

// These child processes open their own descriptors. They do not share a Go
// mutex, so the test also exercises the existing OS-level file lock.
func TestDuplicateReceiveAcrossProcesses(t *testing.T) {
	const envKey = "LMM_HANDOFF_REPLAY_TEST_DIR"
	if dir := os.Getenv(envKey); dir != "" {
		deadline := time.Now().Add(3 * time.Second)
		var s *Store
		for {
			var err error
			s, err = Open(dir)
			if err == nil {
				break
			}
			if !errors.Is(err, ErrBusy) || time.Now().After(deadline) {
				t.Fatal(err)
			}
			time.Sleep(time.Millisecond)
		}
		defer s.Close()
		for i := 0; i < 8; i++ {
			for {
				task, err := s.Receive("shared", []byte("synthetic-shared-event"))
				if err == nil {
					if task.State != "pending" || task.Version != 1 {
						t.Fatalf("duplicate changed task: %+v", task)
					}
					break
				}
				if !errors.Is(err, ErrBusy) || time.Now().After(deadline) {
					t.Fatal(err)
				}
				time.Sleep(time.Millisecond)
			}
		}
		return
	}
	s := newStore(t)
	if _, err := s.Receive("shared", []byte("synthetic-shared-event")); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.dir, "tasks.json")
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	const children = 4
	results := make(chan error, children)
	for i := 0; i < children; i++ {
		go func() {
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestDuplicateReceiveAcrossProcesses$", "-test.count=1")
			cmd.Env = append(os.Environ(), envKey+"="+s.dir)
			out, err := cmd.CombinedOutput()
			if err != nil {
				err = fmt.Errorf("child: %w\n%s", err, out)
			}
			results <- err
		}()
	}
	for i := 0; i < children; i++ {
		if err := <-results; err != nil {
			t.Error(err)
		}
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("cross-process duplicates rewrote the snapshot")
	}
	reopened, err := Open(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	snapshot, err := reopened.Snapshot()
	if err != nil || len(snapshot.Tasks) != 1 || snapshot.Tasks["shared"].Version != 1 {
		t.Fatalf("restart did not preserve exactly one event: %+v, %v", snapshot, err)
	}
}

// Kill a separate worker after it records an unknown external outcome. Replay
// must keep that state, not make the task eligible for another external call.
// This is a local handoff test; no payment provider or core service is used.
func TestReceiveAfterWorkerExitPreservesUncertainty(t *testing.T) {
	const envKey = "LMM_HANDOFF_EXIT_TEST_DIR"
	if dir := os.Getenv(envKey); dir != "" {
		s, err := Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		owner, err := s.Takeover("worker")
		if err != nil {
			t.Fatal(err)
		}
		task, err := s.Claim(owner, "interrupted")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Start(owner, task); err != nil {
			t.Fatal(err)
		}
		if _, err := fmt.Fprintln(os.Stdout, "handoff-persisted"); err != nil {
			t.Fatal(err)
		}
		for {
			time.Sleep(time.Hour)
		}
	}
	s := newStore(t)
	payload := []byte("synthetic-interrupted-event")
	if _, err := s.Receive("interrupted", payload); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestReceiveAfterWorkerExitPreservesUncertainty$", "-test.count=1")
	cmd.Env = append(os.Environ(), envKey+"="+s.dir)
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	ready := make(chan error, 1)
	go func() {
		line, err := bufio.NewReader(pipe).ReadString('\n')
		if err == nil && line != "handoff-persisted\n" {
			err = fmt.Errorf("unexpected child marker %q", line)
		}
		ready <- err
	}()
	var readyErr error
	select {
	case readyErr = <-ready:
	case <-ctx.Done():
		readyErr = ctx.Err()
	}
	killErr := cmd.Process.Kill()
	waitErr := cmd.Wait()
	if readyErr != nil || killErr != nil || waitErr == nil || ctx.Err() != nil {
		t.Fatalf("worker interruption: ready=%v kill=%v wait=%v context=%v stderr=%s", readyErr, killErr, waitErr, ctx.Err(), stderr.String())
	}
	reopened, err := Open(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	owner, err := reopened.Takeover("replacement")
	if err != nil {
		t.Fatal(err)
	}
	task, err := reopened.Receive("interrupted", payload)
	if err != nil || task.State != "uncertain" || task.Epoch != owner.Epoch {
		t.Fatalf("replay lost uncertainty: %+v %v", task, err)
	}
	if _, err := reopened.Claim(owner, task.Key); !errors.Is(err, ErrState) {
		t.Fatalf("unknown outcome became retryable: %v", err)
	}
}

func BenchmarkDuplicateReceive(b *testing.B) {
	dir := b.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		b.Fatal(err)
	}
	s, err := Init(dir)
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	payload := bytes.Repeat([]byte("x"), 16<<10)
	for i := 0; i < 16; i++ {
		if _, err := s.Receive(fmt.Sprintf("event-%d", i), payload); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.Receive("event-0", payload); err != nil {
			b.Fatal(err)
		}
	}
}
