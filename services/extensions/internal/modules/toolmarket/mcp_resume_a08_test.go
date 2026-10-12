// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package toolmarket

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const resumePrimer = "private-stream-primer"

type resumeFixture struct {
	mode          string
	gets          atomic.Int32
	initializes   atomic.Int32
	deletes       atomic.Int32
	mu            sync.Mutex
	id            json.RawMessage
	auth          string
	invalidResult bool
	release       chan struct{}
	entered       chan struct{}
	onCall        func()
}

func (f *resumeFixture) serveCall(w http.ResponseWriter, r *http.Request, id any) {
	f.mu.Lock()
	f.id, _ = json.Marshal(id)
	f.auth = r.Header.Get("Authorization")
	mode := f.mode
	f.mu.Unlock()
	if f.onCall != nil {
		f.onCall()
	}
	w.Header().Set("Content-Type", "text/event-stream")
	if mode == "missing-cursor" {
		fmt.Fprint(w, "data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\"}\n\n")
	} else {
		fmt.Fprintf(w, "id: %s\nretry: 0\ndata:\n\n", resumePrimer)
	}
	if mode == "large-retry" {
		fmt.Fprint(w, "retry: 999999\n\n")
	}
	if mode == "overflow-retry" {
		fmt.Fprint(w, "retry: 9999999999999999999999999999999999999999\n\n")
	}
	w.(http.Flusher).Flush()
}
func (f *resumeFixture) serveGet(w http.ResponseWriter, r *http.Request) {
	f.gets.Add(1)
	f.mu.Lock()
	id := append(json.RawMessage(nil), f.id...)
	auth, invalid, mode := f.auth, f.invalidResult, f.mode
	f.mu.Unlock()
	if r.Header.Get("Authorization") != auth || r.Header.Get("Mcp-Session-Id") != "s-"+digest(auth)[:16] ||
		r.Header.Get("Last-Event-ID") != resumePrimer || r.Header.Get("Mcp-Protocol-Version") != ProtocolVersion {
		w.WriteHeader(403)
		return
	}
	if f.entered != nil {
		select {
		case f.entered <- struct{}{}:
		default:
		}
	}
	if f.release != nil {
		select {
		case <-f.release:
		case <-r.Context().Done():
			return
		}
	}
	if mode == "expired-session" {
		w.WriteHeader(404)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	if mode == "no-progress" {
		fmt.Fprintf(w, "id: %s\nretry: 0\ndata:\n\n", resumePrimer)
		return
	}
	if mode == "wrong-id" {
		id = json.RawMessage(`999`)
	}
	result := json.RawMessage(`{"content":[{"type":"text","text":"saved tool result"}],"password":"synthetic-result-secret","id":9007199254740993}`)
	if invalid {
		result = json.RawMessage(`{"content":null}`)
	}
	data, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	fmt.Fprintf(w, "id: terminal-cursor-must-not-ack-early\ndata: %s\n\n", data)
}
func resumableMarket(t *testing.T, mode string) (*Module, *mockMCP, *resumeFixture, ExecuteInput) {
	t.Helper()
	f := &resumeFixture{mode: mode}
	mock := newMockConfigured(t, func(mock *mockMCP) { mock.resume = f })
	m := testModule(t, mock)
	_, i, _ := prepared(t, m, mock, "api_key", "free")
	return m, mock, f, invocation(t, m, alice, i.ID, "resumable-operation")
}
func savedOperation(t *testing.T, m *Module, in ExecuteInput) Execution {
	t.Helper()
	id := digest([]string{"toolmarket/v1", alice.key(), in.IdempotencyKey})
	var out Execution
	if err := m.store.view(func(d database) error { out = d.Executions[id]; return nil }); err != nil {
		t.Fatal(err)
	}
	return out
}
func TestA08SSEPrimerResumesByGETWithoutRepeatingCall(t *testing.T) {
	m, mock, f, in := resumableMarket(t, "success")
	initCount := f.initializes.Load()
	result, err := m.execute(context.Background(), alice, "alice", in)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(result)
	if strings.Contains(string(data), "synthetic-result-secret") || !strings.Contains(string(data), "9007199254740993") {
		t.Fatal("resumed result was not filtered exactly")
	}
	if mock.calls.Load() != 1 || f.gets.Load() != 1 || f.initializes.Load() != initCount+1 {
		t.Fatal("resumption repeated initialization or invocation")
	}
	record := savedOperation(t, m, in)
	if record.State != "done" || record.Resume != nil {
		t.Fatal("resumed result was not durably completed")
	}
	if _, err = m.execute(context.Background(), alice, "alice", in); err != nil || mock.calls.Load() != 1 || f.gets.Load() != 1 {
		t.Fatal("completed retry contacted provider", err)
	}
}
func TestA08SSEFailureKeepsIntentAndNeverReposts(t *testing.T) {
	for _, mode := range []string{"missing-cursor", "expired-session", "wrong-id", "no-progress"} {
		t.Run(mode, func(t *testing.T) {
			m, mock, f, in := resumableMarket(t, mode)
			before := f.initializes.Load()
			_, err := m.execute(context.Background(), alice, "alice", in)
			if !errors.Is(err, ErrPending) || mock.calls.Load() != 1 || f.initializes.Load() != before+1 {
				t.Fatal("uncertainty repeated a side effect", err)
			}
			wantGets := int32(1)
			if mode == "missing-cursor" {
				wantGets = 0
			}
			if mode == "no-progress" {
				wantGets = maxStreamResumes
			}
			if f.gets.Load() != wantGets {
				t.Fatal("unbounded or wrong recovery request count", f.gets.Load())
			}
			r := savedOperation(t, m, in)
			if r.State != "dispatched" || len(r.Result) != 0 {
				t.Fatal("uncertainty became a completed result")
			}
			g, err := m.grantStatus(alice, in.Grant)
			if err != nil || g.UsedCalls != 1 {
				t.Fatal("uncertain call released consent")
			}
			w := post(t, m, "/executions/status", "alice", `{"idempotency_key":"resumable-operation"}`)
			if w.Code != 200 || strings.Contains(w.Body.String(), resumePrimer) || strings.Contains(w.Body.String(), `"resume"`) || strings.Contains(w.Body.String(), `"session"`) {
				t.Fatal("status exposed private stream coordinates")
			}
		})
	}
}
func TestA08TerminalSSECursorDoesNotSkipUnstoredResult(t *testing.T) {
	m, mock, f, in := resumableMarket(t, "success")
	f.mu.Lock()
	f.invalidResult = true
	f.mu.Unlock()
	_, err := m.execute(context.Background(), alice, "alice", in)
	if !errors.Is(err, ErrPending) {
		t.Fatal(err)
	}
	r := savedOperation(t, m, in)
	if r.Resume == nil || r.Resume.Cursor != resumePrimer {
		t.Fatal("final cursor advanced before result was stored")
	}
	before := f.initializes.Load()
	f.mu.Lock()
	f.invalidResult = false
	f.mu.Unlock()
	_, err = m.execute(context.Background(), alice, "alice", in)
	if err != nil || mock.calls.Load() != 1 || f.initializes.Load() != before || f.gets.Load() != 2 {
		t.Fatal("retry did not fetch the original result", err)
	}
}

type revocableStreamAuthority struct {
	Authority
	revoked atomic.Bool
}

func (a *revocableStreamAuthority) Authorize(ctx context.Context, c, account, action string) (Principal, error) {
	if a.revoked.Load() {
		return Principal{}, ErrDenied
	}
	return a.Authority.Authorize(ctx, c, account, action)
}
func TestA08ResumeRechecksLiveAuthorityAndConnectionGeneration(t *testing.T) {
	for _, mode := range []string{"permission", "generation"} {
		t.Run(mode, func(t *testing.T) {
			m, mock, f, in := resumableMarket(t, "success")
			if mode == "permission" {
				a := &revocableStreamAuthority{Authority: m.authority}
				m.authority = a
				f.onCall = func() { a.revoked.Store(true) }
			} else {
				f.onCall = func() {
					if err := m.setKey(alice, KeyInput{in.Installation, "rotated-test-key"}); err != nil {
						t.Error(err)
					}
				}
			}
			_, err := m.execute(context.Background(), alice, "alice", in)
			if !errors.Is(err, ErrPending) || mock.calls.Load() != 1 || f.gets.Load() != 0 {
				t.Fatal("revoked authority reached a recovery request", err)
			}
		})
	}
}
func TestA08SSERecoverySettlesOriginalChargeOnlyOnce(t *testing.T) {
	mock := newMockConfigured(t, func(mock *mockMCP) { mock.resume = &resumeFixture{mode: "success"} })
	m := testModule(t, mock)
	funds := newFunds()
	m.funds = funds
	_, i, _ := prepared(t, m, mock, "none", "fixed")
	in := invocation(t, m, alice, i.ID, "paid-stream")
	if _, err := m.execute(context.Background(), alice, "alice", in); err != nil {
		t.Fatal(err)
	}
	if _, err := m.execute(context.Background(), alice, "alice", in); err != nil {
		t.Fatal(err)
	}
	if mock.calls.Load() != 1 || funds.reserveCalls != 1 || funds.commitCalls != 1 || len(funds.payments) != 1 {
		t.Fatal("recovery repeated charge or invocation")
	}
}
func TestA08SSECursorSurvivesKilledProcess(t *testing.T) {
	f := &resumeFixture{mode: "success", release: make(chan struct{}), entered: make(chan struct{}, 4)}
	mock := newMockConfigured(t, func(mock *mockMCP) { mock.resume = f })
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
	in := invocation(t, m, alice, i.ID, "process-stream")
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
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		cmd := exec.CommandContext(ctx, executable, "-test.run=^TestA08ToolmarketProcessHelper$", "-test.count=1")
		cmd.Env = append(os.Environ(), "LMM_A08_PROCESS_HELPER="+mode, "LMM_A08_STORE="+path, "LMM_A08_INPUT="+input)
		return cmd, cancel
	}
	cmd, cancel := command("killed-stream")
	defer cancel()
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.entered:
	case <-time.After(8 * time.Second):
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("child did not persist and resume: %s", output.Bytes())
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("child was not killed")
	}
	initializations := f.initializes.Load()
	close(f.release)
	cmd, cancel2 := command("replay")
	defer cancel2()
	if data, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("recovery child failed: %v %s", err, data)
	}
	if mock.calls.Load() != 1 || f.initializes.Load() != initializations || f.gets.Load() != 2 {
		t.Fatal("restarted process reinitialized or repeated the tool call")
	}
	store, err = OpenStore(path, bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	m.store = store
	r := savedOperation(t, m, in)
	g, err := m.grantStatus(alice, in.Grant)
	if err != nil || r.State != "done" || r.Resume != nil || g.UsedCalls != 1 {
		t.Fatal("durable result or consent was lost")
	}
}

func TestA08UnsupportedRetryCannotBeBypassedWithAnotherExecute(t *testing.T) {
	for _, mode := range []string{"large-retry", "overflow-retry"} {
		t.Run(mode, func(t *testing.T) {
			m, mock, f, in := resumableMarket(t, mode)
			for attempt := 0; attempt < 2; attempt++ {
				if _, err := m.execute(context.Background(), alice, "alice", in); !errors.Is(err, ErrPending) {
					t.Fatal(err)
				}
			}
			if mock.calls.Load() != 1 || f.gets.Load() != 0 {
				t.Fatal("retried earlier than the server requested")
			}
		})
	}
}
func TestA08ResumeCoordinatesAreBoundToOriginalOperation(t *testing.T) {
	for _, mode := range []string{"changed-input", "other-user", "expired-cursor"} {
		t.Run(mode, func(t *testing.T) {
			m, mock, f, in := resumableMarket(t, "no-progress")
			if _, err := m.execute(context.Background(), alice, "alice", in); !errors.Is(err, ErrPending) {
				t.Fatal(err)
			}
			reads := f.gets.Load()
			p, token := alice, "alice"
			switch mode {
			case "changed-input":
				in.Arguments = json.RawMessage(`{"q":"different request"}`)
			case "other-user":
				p, token = bob, "bob"
			case "expired-cursor":
				now := m.now().Add(6 * time.Minute)
				m.now = func() time.Time { return now }
			}
			if _, err := m.execute(context.Background(), p, token, in); err == nil {
				t.Fatal("unbound recovery accepted")
			}
			if f.gets.Load() != reads || mock.calls.Load() != 1 {
				t.Fatal("unbound request contacted provider")
			}
		})
	}
}
