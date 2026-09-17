//go:build linux

package deployslots

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func storeReceipt(version string) Receipt {
	a := Artifact{Version: version, Revision: strings.Repeat("a", 40), SHA256: strings.Repeat("b", 64)}
	return Receipt{DeploymentID: "deployment-" + version, PlanSHA256: strings.Repeat("c", 64), Phase: "CONFIRMED", Pair: Pair{Go: a, Web: a}}
}

func testStore(t *testing.T) (*FileStore, State) {
	t.Helper()
	directory := t.TempDir()
	if err := os.Chmod(directory, 0700); err != nil {
		t.Fatal(err)
	}
	store, err := OpenFileStore(directory)
	if err != nil {
		t.Fatal(err)
	}
	state, err := store.Initialize(context.Background(), storeReceipt("1.0.0"))
	if err != nil {
		t.Fatal(err)
	}
	return store, state
}

func stageStore(store *FileStore, state State) (State, error) {
	next := storeReceipt("2.0.0")
	return store.Update(context.Background(), state.Generation, func(current State) (State, error) {
		return current.Stage(current.Generation, next.DeploymentID, next.PlanSHA256, next.Pair)
	})
}

func TestFileStoreRoundTripAndUncertainActivation(t *testing.T) {
	store, state := testStore(t)
	staged, err := stageStore(store, state)
	if err != nil {
		t.Fatal(err)
	}
	started, err := store.Update(context.Background(), staged.Generation, func(current State) (State, error) {
		return current.Begin(current.Generation, current.Pending.DeploymentID, current.Pending.PlanSHA256)
	})
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenFileStore(store.directory)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := reopened.Load(context.Background())
	if err != nil || loaded.Generation != started.Generation || loaded.Pending == nil || !loaded.Pending.Started || loaded.Active != "A" {
		t.Fatalf("uncertain activation lost: %+v %v", loaded, err)
	}
	if _, err := stageStore(reopened, loaded); !errors.Is(err, ErrConflict) {
		t.Fatalf("repeated activation accepted: %v", err)
	}
	_, err = reopened.Update(context.Background(), loaded.Generation, func(current State) (State, error) {
		return current.AbortStage(current.Generation, current.Pending.DeploymentID, current.Pending.PlanSHA256)
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("started activation was discarded: %v", err)
	}
	receipt := storeReceipt("2.0.0")
	finished, err := reopened.Update(context.Background(), loaded.Generation, func(current State) (State, error) {
		return current.Finish(current.Generation, receipt)
	})
	if err != nil || finished.Active != "B" || finished.Pending != nil || finished.Slots["A"] != state.Slots["A"] {
		t.Fatalf("paired confirmation lost rollback evidence: %+v %v", finished, err)
	}
	if _, err := reopened.Initialize(context.Background(), receipt); !errors.Is(err, ErrConflict) {
		t.Fatalf("existing state overwritten by initialization: %v", err)
	}
}

func TestFileStoreRejectsInvalidUpdatesWithoutChangingBytes(t *testing.T) {
	store, state := testStore(t)
	path := filepath.Join(store.directory, "state.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	injected := errors.New("rejected transition")
	cases := []func(State) (State, error){
		nil,
		func(current State) (State, error) { return current, nil },
		func(current State) (State, error) { current.Generation += 2; return current, nil },
		func(current State) (State, error) { current.Generation++; current.Active = "C"; return current, nil },
		func(current State) (State, error) { current.Slots["A"] = Pair{}; return current, injected },
	}
	for _, transition := range cases {
		if _, err := store.Update(context.Background(), state.Generation, transition); err == nil {
			t.Fatal("invalid transition accepted")
		}
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(before, got) {
			t.Fatal("rejected update changed the state file", err)
		}
	}
	called := false
	_, err = store.Update(context.Background(), 0, func(current State) (State, error) { called = true; return current, nil })
	if !errors.Is(err, ErrConflict) || called {
		t.Fatal("stale generation reached transition", err)
	}
}

func TestFileStoreConcurrentWritersHaveOneWinner(t *testing.T) {
	store, state := testStore(t)
	start := make(chan struct{})
	results := make(chan error, 12)
	var group sync.WaitGroup
	for i := 0; i < cap(results); i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			other, err := OpenFileStore(store.directory)
			<-start
			if err == nil {
				_, err = stageStore(other, state)
			}
			results <- err
		}()
	}
	close(start)
	group.Wait()
	close(results)
	wins := 0
	for err := range results {
		if err == nil {
			wins++
		} else if !errors.Is(err, ErrConflict) {
			t.Fatal(err)
		}
	}
	if wins != 1 {
		t.Fatalf("expected exactly one persisted update, got %d", wins)
	}
}

func TestFileStoreMalformedStateFailsClosed(t *testing.T) {
	_, initial := testStore(t)
	valid, err := json.Marshal(initial)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string][]byte{
		"duplicate":       append([]byte(`{"generation":9,`), valid[1:]...),
		"case-alias":      append([]byte(`{"Generation":9,`), valid[1:]...),
		"nested":          bytes.Replace(valid, []byte(`"version":`), []byte(`"version":"9","version":`), 1),
		"unknown":         append([]byte(`{"unknown":true,`), valid[1:]...),
		"trailing":        append(append([]byte{}, valid...), []byte(`{}`)...),
		"truncated":       valid[:len(valid)-3],
		"oversized":       bytes.Repeat([]byte(" "), maxStateBytes+1),
		"deep":            []byte(strings.Repeat("[", 18) + "0" + strings.Repeat("]", 18)),
		"non-utf8":        {0xff},
		"zero-generation": bytes.Replace(valid, []byte(`"generation":1`), []byte(`"generation":0`), 1),
		"null":            []byte(`null`),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			store, _ := testStore(t)
			path := filepath.Join(store.directory, "state.json")
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := store.Load(context.Background()); err == nil {
				t.Fatal("malformed state accepted")
			}
			if _, err := store.Initialize(context.Background(), storeReceipt("1.0.0")); err == nil {
				t.Fatal("malformed evidence overwritten")
			}
		})
	}
}

func TestFileStoreRejectsUnsafeFiles(t *testing.T) {
	for _, name := range []string{"state.json", "state.lock"} {
		for _, kind := range []string{"symlink", "hardlink", "fifo", "public", "owner"} {
			t.Run(name+"/"+kind, func(t *testing.T) {
				store, _ := testStore(t)
				path := filepath.Join(store.directory, name)
				must := func(err error) {
					t.Helper()
					if err != nil {
						t.Fatal(err)
					}
				}
				switch kind {
				case "symlink":
					must(os.Remove(path))
					must(os.Symlink("/dev/null", path))
				case "hardlink":
					must(os.Link(path, filepath.Join(store.directory, "alias")))
				case "fifo":
					must(os.Remove(path))
					must(syscall.Mkfifo(path, 0600))
				case "public":
					must(os.Chmod(path, 0644))
				case "owner":
					if os.Geteuid() != 0 {
						t.Skip("ownership mutation requires root")
					}
					must(os.Chown(path, 1, -1))
				}
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				if _, err := store.Load(ctx); err == nil {
					t.Fatal("unsafe file accepted")
				}
			})
		}
	}
}

func TestFileStoreRejectsUnsafeDirectories(t *testing.T) {
	store, _ := testStore(t)
	for _, path := range []string{"relative", "/", store.directory + "/../other"} {
		if _, err := OpenFileStore(path); err == nil {
			t.Fatal("unsafe path accepted", path)
		}
	}
	link := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(store.directory, link); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenFileStore(link); err == nil {
		t.Fatal("symlinked directory accepted")
	}
	child := filepath.Join(store.directory, "child")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenFileStore(filepath.Join(link, "child")); err == nil {
		t.Fatal("symlinked ancestor accepted")
	}
	if err := os.Chmod(store.directory, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(context.Background()); err == nil {
		t.Fatal("public directory accepted")
	}
}

func TestFileStoreWriteFailureAndPostRenameUncertainty(t *testing.T) {
	store, state := testStore(t)
	injected := errors.New("injected I/O failure")
	store.beforeRename = func() error { return injected }
	if _, err := stageStore(store, state); !errors.Is(err, injected) {
		t.Fatal(err)
	}
	loaded, err := store.Load(context.Background())
	if err != nil || loaded.Generation != state.Generation || loaded.Pending != nil {
		t.Fatal("pre-rename failure changed state", err)
	}
	store.beforeRename = nil
	store.syncDir = func(*os.File) error { return injected }
	if _, err := stageStore(store, state); !errors.Is(err, ErrDurability) || !errors.Is(err, injected) {
		t.Fatal("durability failure hidden", err)
	}
	loaded, err = store.Load(context.Background())
	if err != nil || loaded.Generation != state.Generation+1 || loaded.Pending == nil {
		t.Fatal("renamed state was lost", err)
	}
}

func TestFileStoreCancellationDoesNotWrite(t *testing.T) {
	store, state := testStore(t)
	lock, err := os.OpenFile(filepath.Join(store.directory, "state.lock"), os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := store.Load(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("lock wait ignored deadline", err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	store.beforeRename = func() error { cancel(); return nil }
	_, err = store.Update(ctx, state.Generation, func(current State) (State, error) {
		receipt := storeReceipt("2.0.0")
		return current.Stage(current.Generation, receipt.DeploymentID, receipt.PlanSHA256, receipt.Pair)
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	loaded, err := store.Load(context.Background())
	if err != nil || loaded.Generation != state.Generation {
		t.Fatal("cancelled write persisted", err)
	}
}

func TestFileStoreProcessCrash(t *testing.T) {
	if directory := os.Getenv("DEPLOYSLOTS_CRASH_TEST_DIRECTORY"); directory != "" {
		store, err := OpenFileStore(directory)
		if err != nil {
			t.Fatal(err)
		}
		state, err := store.Load(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if os.Getenv("DEPLOYSLOTS_CRASH_TEST_POINT") == "before" {
			store.beforeRename = func() error { os.Exit(88); return nil }
		} else {
			store.syncDir = func(*os.File) error { os.Exit(89); return nil }
		}
		_, _ = store.Update(context.Background(), state.Generation, func(current State) (State, error) {
			return current.Begin(current.Generation, current.Pending.DeploymentID, current.Pending.PlanSHA256)
		})
		t.Fatal("crash hook did not exit")
	}
	for _, point := range []string{"before", "after"} {
		t.Run(point, func(t *testing.T) {
			store, initial := testStore(t)
			staged, err := stageStore(store, initial)
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestFileStoreProcessCrash$")
			cmd.Env = append(os.Environ(), "DEPLOYSLOTS_CRASH_TEST_DIRECTORY="+store.directory, "DEPLOYSLOTS_CRASH_TEST_POINT="+point)
			var exit *exec.ExitError
			if err := cmd.Run(); !errors.As(err, &exit) || (exit.ExitCode() != 88 && exit.ExitCode() != 89) {
				t.Fatalf("unexpected child exit: %v", err)
			}
			loaded, err := store.Load(context.Background())
			if err != nil || loaded.Pending == nil || loaded.Active != initial.Active {
				t.Fatalf("lost recovery state: %+v %v", loaded, err)
			}
			if point == "before" && (loaded.Generation != staged.Generation || loaded.Pending.Started) {
				t.Fatal("pre-rename crash corrupted prior state")
			}
			if point == "after" && (loaded.Generation != staged.Generation+1 || !loaded.Pending.Started) {
				t.Fatal("post-rename crash lost uncertainty")
			}
		})
	}
}
