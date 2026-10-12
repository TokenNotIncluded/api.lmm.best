// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package toolmarket

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// This test binary is the child process. The test-only transport is not exposed
// in product configuration. All credentials/data are synthetic; no Core funds
// adapter participates in these file-store / actual-process assertions.
func TestA08ToolmarketProcessHelper(t *testing.T) {
	mode := os.Getenv("LMM_A08_PROCESS_HELPER")
	if mode == "" {
		t.Skip("subprocess helper")
	}
	store, err := OpenStore(os.Getenv("LMM_A08_STORE"), bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	m, err := New(store, &testAuthority{}, Config{CallbackURL: "https://app.example/callback"})
	if err != nil {
		t.Fatal(err)
	}
	m.net = &network{test: true, client: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	var in ExecuteInput
	data, err := os.ReadFile(os.Getenv("LMM_A08_INPUT"))
	if err != nil || json.Unmarshal(data, &in) != nil {
		t.Fatal("invalid synthetic process fixture")
	}
	_, err = m.execute(context.Background(), alice, "alice", in)
	if mode == "pending" {
		if !errors.Is(err, ErrPending) {
			t.Fatal("uncertain operation was replayed", err)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if mode == "abrupt-success" {
		os.Exit(23)
	} // No Store.Close or other Go cleanup.
}

func TestA08ToolmarketRealProcessRestart(t *testing.T) {
	for _, scenario := range []string{"completed", "killed-after-dispatch"} {
		t.Run(scenario, func(t *testing.T) {
			mock := newMockConfigured(t, func(mock *mockMCP) {
				if scenario == "killed-after-dispatch" {
					mock.enteredCall = make(chan struct{}, 1)
					mock.blockCall = make(chan struct{})
				}
			})
			m := testModule(t, mock)
			dir := t.TempDir()
			if err := os.Chmod(dir, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "market.db")
			store, err := InitStore(path, bytes.Repeat([]byte{7}, 32))
			if err != nil {
				t.Fatal(err)
			}
			m.store = store
			_, i, _ := prepared(t, m, mock, "none", "free")
			in := invocation(t, m, alice, i.ID, "process-operation")
			in.Grant = confirmFor(t, m, grantPreviewFor(t, m, in, 1, 0)).ID
			input := filepath.Join(dir, "request.json")
			data, _ := json.Marshal(in)
			if err := os.WriteFile(input, data, 0600); err != nil {
				t.Fatal(err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			command := func(mode string) (*exec.Cmd, context.CancelFunc) {
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				cmd := exec.CommandContext(ctx, executable, "-test.run=^TestA08ToolmarketProcessHelper$", "-test.count=1")
				cmd.Env = append(os.Environ(), "LMM_A08_PROCESS_HELPER="+mode, "LMM_A08_STORE="+path, "LMM_A08_INPUT="+input)
				return cmd, cancel
			}
			if scenario == "completed" {
				cmd, cancel := command("abrupt-success")
				output, err := cmd.CombinedOutput()
				cancel()
				var exit *exec.ExitError
				if !errors.As(err, &exit) || exit.ExitCode() != 23 {
					t.Fatalf("first real process failed: %v %s", err, output)
				}
			} else {
				cmd, cancel := command("killed")
				defer cancel()
				var output bytes.Buffer
				cmd.Stdout = &output
				cmd.Stderr = &output
				if err := cmd.Start(); err != nil {
					t.Fatal(err)
				}
				select {
				case <-mock.enteredCall:
				case <-time.After(10 * time.Second):
					cmd.Process.Kill()
					cmd.Wait()
					t.Fatalf("provider did not receive call: %s", output.Bytes())
				}
				if err := cmd.Process.Kill(); err != nil {
					t.Fatal(err)
				}
				if err := cmd.Wait(); err == nil {
					t.Fatal("process was not terminated")
				}
				close(mock.blockCall)
			}
			if mock.calls.Load() != 1 {
				t.Fatal("first process call count", mock.calls.Load())
			}
			mode := "replay"
			wantState := "done"
			if scenario != "completed" {
				mode = "pending"
				wantState = "dispatched"
			}
			cmd, cancel := command(mode)
			output, err := cmd.CombinedOutput()
			cancel()
			if err != nil {
				t.Fatalf("restarted process: %v %s", err, output)
			}
			if mock.calls.Load() != 1 {
				t.Fatal("process restart repeated external side effect")
			}
			store, err = OpenStore(path, bytes.Repeat([]byte{7}, 32))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			m.store = store
			if err := store.view(func(d database) error {
				g := d.Grants[in.Grant]
				r := d.Executions[digest([]string{"toolmarket/v1", alice.key(), in.IdempotencyKey})]
				if g.UsedCalls != 1 || r.State != wantState || r.Grant != g.ID {
					t.Fatal("durable consent/status lost", g.UsedCalls, r.State)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			in.IdempotencyKey = "fresh-key-after-crash"
			if _, err := m.execute(context.Background(), alice, "alice", in); !errors.Is(err, ErrLimit) {
				t.Fatal("new key reset consumed consent", err)
			}
			ciphertext, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(ciphertext, []byte("mock result")) || bytes.Contains(ciphertext, []byte(in.Grant)) {
				t.Fatal("plaintext state persisted")
			}
		})
	}
}
