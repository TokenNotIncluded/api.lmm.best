// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package remotecontrol

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/coreclient"
	pb "github.com/TokenNotIncluded/api.lmm.best/extensions/internal/corepb"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Controlled live Core interface double, NOT Rust/OAuth acceptance. State changes
// are synchronized because actual HTTP streams and requests execute concurrently.
type streamCore struct {
	mu         sync.Mutex
	err        error
	kind       pb.CredentialKind
	account    int64
	block      bool
	calls      int
	credential int64
}

func (c *streamCore) AuthorizeRemote(ctx context.Context, token string) (*coreclient.RemoteAuthority, error) {
	c.mu.Lock()
	c.calls++
	err, kind, account, block, credential := c.err, c.kind, c.account, c.block, c.credential
	c.mu.Unlock()
	if block {
		<-ctx.Done()
		return nil, status.Error(codes.DeadlineExceeded, "private timeout")
	}
	if err != nil {
		return nil, err
	}
	id := int64(1)
	if token == bobToken {
		id = 2
	} else if token != aliceToken {
		return nil, status.Error(codes.Unauthenticated, "private token")
	}
	if account == 0 {
		account = id
	}
	if kind != pb.CredentialKind_CREDENTIAL_KIND_SESSION {
		return nil, status.Error(codes.PermissionDenied, "session required")
	}
	actor := remoteAuthority(id, account)
	if credential != 0 {
		actor.Source.NativeCredentialID = credential
	}
	return actor, nil
}
func (c *streamCore) change(err error, account int64, kind pb.CredentialKind, block bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.err = err
	c.account = account
	c.kind = kind
	c.block = block
}
func streamSetup(t *testing.T, timing streamTiming) (*Module, *streamCore, *httptest.Server, context.CancelFunc) {
	t.Helper()
	core := &streamCore{kind: pb.CredentialKind_CREDENTIAL_KIND_SESSION}
	m, err := New(core)
	if err != nil {
		t.Fatal(err)
	}
	m.timing = timing
	host, err := modules.New([]modules.Module{m}, []byte(hostToken))
	if err != nil {
		t.Fatal(err)
	}
	base, cancel := context.WithCancel(context.Background())
	server := httptest.NewUnstartedServer(host)
	server.Config.BaseContext = func(net.Listener) context.Context { return base }
	server.Start()
	t.Cleanup(func() { cancel(); server.Close() })
	return m, core, server, cancel
}

const streamPath = "/extensions/v1/remote-control/pi/sessions/session_abcdefgh/events"

func streamRequest(t *testing.T, server *httptest.Server, ctx context.Context, path, token string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, "GET", server.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+hostToken)
	req.Header.Set("X-LMM-User-Credential", token)
	response, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { response.Body.Close() })
	return response
}

type receivedEvent struct{ name, data string }
type eventReader struct {
	events chan receivedEvent
	done   chan struct{}
}

func readEvents(body io.Reader) *eventReader {
	reader := &eventReader{make(chan receivedEvent, 32), make(chan struct{})}
	go func() {
		defer close(reader.done)
		defer close(reader.events)
		scanner := bufio.NewScanner(body)
		scanner.Buffer(make([]byte, 4096), 2<<20)
		event := receivedEvent{}
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				if event.name != "" {
					reader.events <- event
				}
				event = receivedEvent{}
				continue
			}
			if strings.HasPrefix(line, "event: ") {
				event.name = strings.TrimPrefix(line, "event: ")
			}
			if strings.HasPrefix(line, "data: ") {
				event.data = strings.TrimPrefix(line, "data: ")
			}
		}
	}()
	return reader
}
func nextEvent(t *testing.T, reader *eventReader, name string) receivedEvent {
	t.Helper()
	select {
	case event, ok := <-reader.events:
		if !ok || event.name != name {
			t.Fatalf("event %+v open=%v want %s", event, ok, name)
		}
		return event
	case <-time.After(time.Second):
		t.Fatal("event not delivered within one second")
		return receivedEvent{}
	}
}
func batchEvent(t *testing.T, reader *eventReader, name string) eventBatch {
	t.Helper()
	event := nextEvent(t, reader, name)
	var batch eventBatch
	if err := json.Unmarshal([]byte(event.data), &batch); err != nil {
		t.Fatal(err)
	}
	if batch.Messages == nil || len(batch.Messages) > MaxMessages {
		t.Fatal("invalid batch bound/array")
	}
	return batch
}
func waitDone(t *testing.T, reader *eventReader) {
	t.Helper()
	select {
	case <-reader.done:
	case <-time.After(time.Second):
		t.Fatal("stream did not close")
	}
}
func waitQuota(t *testing.T, m *Module, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		m.streamsMu.Lock()
		count := m.streamCount
		m.streamsMu.Unlock()
		if count == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("quota did not reach %d", want)
}
func appendStream(t *testing.T, m *Module, owner int64) Message {
	t.Helper()
	message, err := m.relay.append(owner, "session_abcdefgh", "controller", cipher(16))
	if err != nil {
		t.Fatal(err)
	}
	return message
}

func TestSSEReadyCatchupAppendAndRequestCancellation(t *testing.T) {
	m, _, server, _ := streamSetup(t, streamTiming{})
	snapshot := heartbeat(t, m.relay, 1, "session_abcdefgh")
	appendStream(t, m, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	response := streamRequest(t, server, ctx, streamPath+"?after=0&generation="+snapshot.Generation, aliceToken)
	if response.StatusCode != 200 || response.Header.Get("Content-Type") != "text/event-stream" || response.Header.Get("Cache-Control") != "no-store" || response.Header.Get("X-Accel-Buffering") != "no" {
		t.Fatal("invalid handshake", response.StatusCode, response.Header)
	}
	reader := readEvents(response.Body)
	ready := batchEvent(t, reader, "ready")
	if ready.Generation != snapshot.Generation || len(ready.Messages) != 1 || ready.Messages[0].Sequence != 1 || ready.Messages[0].Ciphertext != cipher(16) {
		t.Fatal("invalid ready backlog", ready)
	}
	started := time.Now()
	appendStream(t, m, 1)
	next := batchEvent(t, reader, "messages")
	if len(next.Messages) != 1 || next.Messages[0].Sequence != 2 || time.Since(started) > time.Second {
		t.Fatal("append did not immediately wake stream")
	}
	cancel()
	waitDone(t, reader)
	waitQuota(t, m, 0)
}

func TestSSEResumeGenerationAndRolledHistory(t *testing.T) {
	for _, query := range []string{"?after=1", "?after=9007199254740991&generation=stale_generation", "?after=129&generation=actual", "?after=9007199254740991&generation=actual"} {
		t.Run(query, func(t *testing.T) {
			m, _, server, _ := streamSetup(t, streamTiming{})
			snapshot := heartbeat(t, m.relay, 1, "session_abcdefgh")
			for i := 0; i < 130; i++ {
				appendStream(t, m, 1)
			}
			query = strings.ReplaceAll(query, "actual", snapshot.Generation)
			response := streamRequest(t, server, context.Background(), streamPath+query, aliceToken)
			ready := batchEvent(t, readEvents(response.Body), "ready")
			want := 128
			first := int64(3)
			if strings.Contains(query, "after=129") {
				want = 1
				first = 130
			}
			if ready.Generation != snapshot.Generation || len(ready.Messages) != want || ready.Messages[0].Sequence != first || ready.Messages[len(ready.Messages)-1].Sequence != 130 {
				t.Fatal("invalid rolled resume", ready)
			}
			response.Body.Close()
			waitQuota(t, m, 0)
		})
	}
}

func TestSSEFreshAuthClosesBeforeAnyLaterCiphertext(t *testing.T) {
	cases := []struct {
		name, code string
		err        error
		account    int64
		kind       pb.CredentialKind
		block      bool
	}{
		{"revoked", "unauthorized", status.Error(codes.Unauthenticated, "secret"), 0, pb.CredentialKind_CREDENTIAL_KIND_SESSION, false},
		{"forbidden", "unauthorized", status.Error(codes.PermissionDenied, "secret"), 0, pb.CredentialKind_CREDENTIAL_KIND_SESSION, false},
		{"owner changed", "unauthorized", nil, 999, pb.CredentialKind_CREDENTIAL_KIND_SESSION, false},
		{"API key", "unauthorized", nil, 0, pb.CredentialKind_CREDENTIAL_KIND_API_KEY, false},
		{"Core failure", "remote_unavailable", status.Error(codes.Unavailable, "secret"), 0, pb.CredentialKind_CREDENTIAL_KIND_SESSION, false},
		{"RPC timeout", "remote_unavailable", nil, 0, pb.CredentialKind_CREDENTIAL_KIND_SESSION, true},
	}
	for _, trigger := range []string{"batch", "keepalive"} {
		for _, tc := range cases {
			t.Run(trigger+"/"+tc.name, func(t *testing.T) {
				m, core, server, _ := streamSetup(t, streamTiming{reauthorize: 30 * time.Millisecond, rpc: 30 * time.Millisecond})
				heartbeat(t, m.relay, 1, "session_abcdefgh")
				response := streamRequest(t, server, context.Background(), streamPath, aliceToken)
				reader := readEvents(response.Body)
				batchEvent(t, reader, "ready")
				core.change(tc.err, tc.account, tc.kind, tc.block)
				if trigger == "batch" {
					appendStream(t, m, 1)
				}
				event := nextEvent(t, reader, "error")
				if event.data != fmt.Sprintf(`{"code":%q}`, tc.code) {
					t.Fatal("private/wrong error", event)
				}
				waitDone(t, reader)
				if _, ok := <-reader.events; ok {
					t.Fatal("ciphertext sent after failed auth")
				}
				waitQuota(t, m, 0)
			})
		}
	}
}

func TestSSEStrictQueryAndPreHeaderGuards(t *testing.T) {
	m, core, server, _ := streamSetup(t, streamTiming{rpc: 20 * time.Millisecond})
	heartbeat(t, m.relay, 1, "session_abcdefgh")
	for _, query := range []string{"after=-1", "after=+1", "after=%2B1", "after=", "after=1&after=2", "after=9007199254740992", "after=1.0", "after=%ZZ", "generation=", "generation=short", "generation=valid_generation&generation=other_generation", "unknown=1", "after=0;other=1"} {
		response := streamRequest(t, server, context.Background(), streamPath+"?"+query, aliceToken)
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != 400 || strings.Contains(string(body), "event:") || response.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("query %s: %d %s", query, response.StatusCode, body)
		}
	}
	for _, tc := range []struct {
		token string
		err   error
		kind  pb.CredentialKind
		want  int
	}{
		{bobToken, nil, pb.CredentialKind_CREDENTIAL_KIND_SESSION, 404},
		{aliceToken, status.Error(codes.Unauthenticated, "secret"), pb.CredentialKind_CREDENTIAL_KIND_SESSION, 401},
		{aliceToken, nil, pb.CredentialKind_CREDENTIAL_KIND_API_KEY, 403},
		{aliceToken, status.Error(codes.Unavailable, "secret"), pb.CredentialKind_CREDENTIAL_KIND_SESSION, 503},
	} {
		core.change(tc.err, 0, tc.kind, false)
		response := streamRequest(t, server, context.Background(), streamPath, tc.token)
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != tc.want || strings.Contains(string(body), "event:") || strings.Contains(string(body), "ciphertext") || strings.Contains(string(body), "secret") {
			t.Fatalf("guard %d %s", response.StatusCode, body)
		}
	}
	// Unauthorized even for an absent session with a malformed query.
	core.change(status.Error(codes.Unauthenticated, "secret"), 0, pb.CredentialKind_CREDENTIAL_KIND_SESSION, false)
	response := streamRequest(t, server, context.Background(), strings.Replace(streamPath, "session_abcdefgh", "session_missing", 1)+"?unknown=1", aliceToken)
	if response.StatusCode != 401 {
		t.Fatal("existence oracle before auth")
	}
	response.Body.Close()
	waitQuota(t, m, 0)
}

func TestSSEHostCapacityLeavesCommandSlotsAndOwnersIsolated(t *testing.T) {
	m, _, server, _ := streamSetup(t, streamTiming{})
	for _, owner := range []int64{1, 2} {
		heartbeat(t, m.relay, owner, "session_abcdefgh")
	}
	readers := make([]*eventReader, 0, 4)
	responses := make([]*http.Response, 0, 4)
	for _, token := range []string{aliceToken, aliceToken, bobToken, bobToken} {
		response := streamRequest(t, server, context.Background(), streamPath, token)
		responses = append(responses, response)
		reader := readEvents(response.Body)
		readers = append(readers, reader)
		ready := batchEvent(t, reader, "ready")
		if len(ready.Messages) != 0 {
			t.Fatal("cross-owner backlog")
		}
	}
	waitQuota(t, m, 4)
	overflow := streamRequest(t, server, context.Background(), streamPath, aliceToken)
	body, _ := io.ReadAll(overflow.Body)
	overflow.Body.Close()
	if overflow.StatusCode != 429 || overflow.Header.Get("Retry-After") != "1" || strings.Contains(string(body), "event:") {
		t.Fatal("stream admission", overflow.StatusCode, string(body))
	}
	// Real host and actual HTTP PUT/POST/DELETE, not an unwrapped module success mock.
	do := func(method, token string, input any) *http.Response {
		raw, _ := json.Marshal(input)
		req, _ := http.NewRequest(method, server.URL+strings.TrimSuffix(streamPath, "/events"), strings.NewReader(string(raw)))
		if method == "POST" {
			req.URL.Path += "/messages"
		}
		req.Header.Set("Authorization", "Bearer "+hostToken)
		req.Header.Set("X-LMM-User-Credential", token)
		response, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != 200 {
			t.Fatalf("%s starved: %d %s", method, response.StatusCode, data)
		}
		return response
	}
	before := m.relay.list(1)[0].ExpiresAt
	do("PUT", aliceToken, putInput())
	do("POST", aliceToken, postInput())
	for _, reader := range readers[:2] {
		batch := batchEvent(t, reader, "messages")
		if len(batch.Messages) != 1 || batch.Messages[0].Sequence != 1 {
			t.Fatal("command not delivered", batch)
		}
	}
	// Bob receives zero Alice ciphertext; deletion yields only Bob's error event.
	do("DELETE", bobToken, nil)
	for _, reader := range readers[2:] {
		nextEvent(t, reader, "error")
		waitDone(t, reader)
	}
	if m.relay.list(1)[0].ExpiresAt < before {
		t.Fatal("heartbeat state corrupted")
	}
	for _, response := range responses {
		response.Body.Close()
	}
	waitQuota(t, m, 0)
	// Per-user limit is independently enforced below the global limit.
	for i := 0; i < 2; i++ {
		response := streamRequest(t, server, context.Background(), streamPath, aliceToken)
		responses[i] = response
		batchEvent(t, readEvents(response.Body), "ready")
	}
	overflow = streamRequest(t, server, context.Background(), streamPath, aliceToken)
	if overflow.StatusCode != 429 {
		t.Fatal("per-user limit")
	}
	overflow.Body.Close()
	for _, response := range responses[:2] {
		response.Body.Close()
	}
	waitQuota(t, m, 0)
}

func TestSSEExpiryDeleteLeaseAndHostBaseContextDrain(t *testing.T) {
	for _, operation := range []string{"expiry", "delete", "lease", "host cancel"} {
		t.Run(operation, func(t *testing.T) {
			timing := streamTiming{reauthorize: 20 * time.Millisecond}
			if operation == "lease" {
				timing.lease = 70 * time.Millisecond
			}
			m, _, server, drain := streamSetup(t, timing)
			snapshot := heartbeat(t, m.relay, 1, "session_abcdefgh")
			if operation == "expiry" {
				m.relay.mu.Lock()
				m.relay.owners[1][snapshot.SessionID].expires = time.Now().Add(80 * time.Millisecond)
				m.relay.mu.Unlock()
			}
			response := streamRequest(t, server, context.Background(), streamPath, aliceToken)
			reader := readEvents(response.Body)
			batchEvent(t, reader, "ready")
			switch operation {
			case "delete":
				m.relay.remove(1, snapshot.SessionID)
			case "host cancel":
				drain()
			}
			if operation == "expiry" || operation == "delete" {
				event := nextEvent(t, reader, "error")
				if event.data != `{"code":"remote_unavailable"}` {
					t.Fatal(event)
				}
			}
			waitDone(t, reader)
			waitQuota(t, m, 0)
			if operation == "lease" && m.relay.list(1)[0].ExpiresAt != snapshot.ExpiresAt {
				t.Fatal("keepalive extended TTL")
			}
		})
	}
}

func TestSSEOrdinaryShutdownDoesNotSubstituteForBaseContextDrain(t *testing.T) {
	m, _, server, drain := streamSetup(t, streamTiming{})
	heartbeat(t, m.relay, 1, "session_abcdefgh")
	response := streamRequest(t, server, context.Background(), streamPath, aliceToken)
	reader := readEvents(response.Body)
	batchEvent(t, reader, "ready")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := server.Config.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("plain Shutdown unexpectedly drained active stream", err)
	}
	waitQuota(t, m, 1)
	drain()
	waitDone(t, reader)
	waitQuota(t, m, 0)
}

func TestRelayEventsSnapshotAtomicWakeAndBroadcast(t *testing.T) {
	r := newRelay()
	snapshot := heartbeat(t, r, 1, "session_abcdefgh")
	batch, wake, _, err := r.eventsSnapshot(1, snapshot.SessionID, 0, snapshot.Generation)
	if err != nil || len(batch.Messages) != 0 {
		t.Fatal(err)
	}
	// Append exactly between snapshot and wait: waiting on the captured channel
	// must observe it immediately (no check-then-subscribe lost-wake window).
	_, err = r.append(1, snapshot.SessionID, "controller", cipher(16))
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-wake:
	default:
		t.Fatal("lost wake between read and wait")
	}
	batch, wake, _, _ = r.eventsSnapshot(1, snapshot.SessionID, 0, snapshot.Generation)
	if len(batch.Messages) != 1 {
		t.Fatal("lost backlog")
	}
	batch.Messages[0].Sender = "mutated"
	heartbeat(t, r, 1, snapshot.SessionID)
	select {
	case <-wake:
	default:
		t.Fatal("heartbeat failed to broadcast")
	}
	_, wake, _, _ = r.eventsSnapshot(1, snapshot.SessionID, 1, snapshot.Generation)
	r.remove(1, snapshot.SessionID)
	select {
	case <-wake:
	default:
		t.Fatal("delete failed to broadcast")
	}
	fresh := heartbeat(t, r, 1, snapshot.SessionID)
	_, _ = r.append(1, snapshot.SessionID, "plugin", cipher(16))
	batch, wake, _, _ = r.eventsSnapshot(1, snapshot.SessionID, 99, snapshot.Generation)
	if fresh.Generation == snapshot.Generation || batch.Generation != fresh.Generation || len(batch.Messages) != 1 || batch.Messages[0].Sequence != 1 {
		t.Fatal("generation reset")
	}
	r.mu.Lock()
	r.expire(time.Now().Add(SessionTTL))
	r.mu.Unlock()
	select {
	case <-wake:
	default:
		t.Fatal("expiry failed to broadcast")
	}
}

func TestSSESlowReaderWriteDeadlineReleasesQuota(t *testing.T) {
	m, _, server, _ := streamSetup(t, streamTiming{write: 40 * time.Millisecond, lease: 2 * time.Second})
	heartbeat(t, m.relay, 1, "session_abcdefgh")
	conn, err := net.Dial("tcp", server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if tcp, ok := conn.(*net.TCPConn); ok {
		if err := tcp.SetReadBuffer(1024); err != nil {
			t.Fatal(err)
		}
	}
	fmt.Fprintf(conn, "GET %s HTTP/1.1\r\nHost: test\r\nAuthorization: Bearer %s\r\nX-LMM-User-Credential: %s\r\n\r\n", streamPath, hostToken, aliceToken)
	input := bufio.NewReader(conn)
	response, err := http.ReadResponse(input, &http.Request{Method: "GET"})
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 {
		t.Fatal(response.StatusCode)
	}
	// Read just the ready frame, then stop reading the actual TCP response.
	for {
		line, err := input.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(line, `"messages":[]`) {
			break
		}
	}
	waitQuota(t, m, 1)
	started := time.Now()
	for i := 0; i < 500; i++ {
		if _, err := m.relay.append(1, "session_abcdefgh", "plugin", cipher(64<<10)); err != nil {
			t.Fatal(err)
		}
		m.streamsMu.Lock()
		active := m.streamCount
		m.streamsMu.Unlock()
		if active == 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	waitQuota(t, m, 0)
	if time.Since(started) >= time.Second {
		t.Fatal("slow reader was released by lease instead of short write deadline")
	}
}

func TestSSETimingCannotWeakenBounds(t *testing.T) {
	m, err := New(&streamCore{})
	if err != nil {
		t.Fatal(err)
	}
	m.timing = streamTiming{time.Hour, time.Hour, time.Hour, time.Hour}
	timing := m.streamTimings()
	if timing.lease != StreamLease || timing.rpc != StreamRPCDeadline || timing.write != StreamWriteDeadline || timing.reauthorize+timing.rpc+timing.write > StreamReauthorize {
		t.Fatal("timing weakens production bounds", timing)
	}
}

// Intercept deadlines while forwarding to the actual HTTP server writer.
type deadlineWriter struct {
	http.ResponseWriter
	record func(time.Time)
}

func (w deadlineWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w deadlineWriter) SetWriteDeadline(deadline time.Time) error {
	w.record(deadline)
	return http.NewResponseController(w.ResponseWriter).SetWriteDeadline(deadline)
}
func TestSSEWriteDeadlineRestoredForKeepaliveConnections(t *testing.T) {
	core := &streamCore{kind: pb.CredentialKind_CREDENTIAL_KIND_SESSION}
	m, err := New(core)
	if err != nil {
		t.Fatal(err)
	}
	m.timing = streamTiming{lease: 50 * time.Millisecond, reauthorize: 10 * time.Millisecond, write: 20 * time.Millisecond}
	heartbeat(t, m.relay, 1, "session_abcdefgh")
	host, err := modules.New([]modules.Module{m}, []byte(hostToken))
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var deadlines []time.Time
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host.ServeHTTP(deadlineWriter{w, func(d time.Time) { mu.Lock(); defer mu.Unlock(); deadlines = append(deadlines, d) }}, r)
	}))
	defer server.Close()
	response := streamRequest(t, server, context.Background(), streamPath, aliceToken)
	reader := readEvents(response.Body)
	batchEvent(t, reader, "ready")
	waitDone(t, reader)
	waitQuota(t, m, 0)
	mu.Lock()
	defer mu.Unlock()
	if len(deadlines) < 3 || !deadlines[len(deadlines)-1].IsZero() {
		t.Fatal("write deadline was not restored", deadlines)
	}
	for _, deadline := range deadlines[:len(deadlines)-1] {
		if deadline.IsZero() {
			t.Fatal("stream write was unbounded")
		}
	}
}

func TestSSEInitialRPCDeadlineAndCredentialGuards(t *testing.T) {
	m, core, server, _ := streamSetup(t, streamTiming{rpc: 20 * time.Millisecond})
	heartbeat(t, m.relay, 1, "session_abcdefgh")
	core.change(nil, 0, pb.CredentialKind_CREDENTIAL_KIND_SESSION, true)
	started := time.Now()
	response := streamRequest(t, server, context.Background(), streamPath, aliceToken)
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 504 || time.Since(started) > time.Second || strings.Contains(string(body), "event:") {
		t.Fatal("initial RPC unbounded/not JSON", response.StatusCode, string(body))
	}
	core.change(nil, 0, pb.CredentialKind_CREDENTIAL_KIND_SESSION, false)
	for _, header := range []string{"", "short"} {
		response = streamRequest(t, server, context.Background(), streamPath, header)
		if response.StatusCode != 401 {
			t.Fatal("missing/malformed user credential accepted")
		}
		response.Body.Close()
	}
	req, _ := http.NewRequest("GET", server.URL+streamPath, nil)
	req.Header.Set("X-LMM-User-Credential", aliceToken)
	response, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 401 {
		t.Fatal("host bypassed")
	}
	waitQuota(t, m, 0)
}

func TestSSESourceIdentityCannotMigrateAndHostDrainReleasesStreams(t *testing.T) {
	for _, trigger := range []string{"source changed", "host drain"} {
		t.Run(trigger, func(t *testing.T) {
			m, core, server, _ := streamSetup(t, streamTiming{reauthorize: 30 * time.Millisecond})
			heartbeat(t, m.relay, 1, "session_abcdefgh")
			response := streamRequest(t, server, context.Background(), streamPath, aliceToken)
			reader := readEvents(response.Body)
			batchEvent(t, reader, "ready")
			if trigger == "source changed" {
				core.mu.Lock()
				core.credential = 99
				core.mu.Unlock()
				appendStream(t, m, 1)
				e := nextEvent(t, reader, "error")
				if e.data != `{"code":"unauthorized"}` {
					t.Fatal("source migrated", e)
				}
			} else {
				host, err := modules.NewHost([]modules.Module{m}, []byte(hostToken))
				if err != nil {
					t.Fatal(err)
				}
				if err := host.MarkReady(); err != nil {
					t.Fatal(err)
				}
				host.BeginDrain()
			}
			waitDone(t, reader)
			waitQuota(t, m, 0)
		})
	}
}

// Real Host, module router and TCP streams; only Core authorization is a
// controlled interface double, not Rust or financial acceptance.
// @mark remote-control.events role=test
func TestSSEPreservesHostCapacity(t *testing.T) {
	for _, capacity := range []int{1, 2, 3, 4, 5, 16, 64} {
		t.Run(fmt.Sprint(capacity), func(t *testing.T) {
			m, err := New(&streamCore{kind: pb.CredentialKind_CREDENTIAL_KIND_SESSION})
			if err != nil {
				t.Fatal(err)
			}
			host, err := modules.NewHostWithLimit([]modules.Module{m}, []byte(hostToken), capacity)
			if err != nil {
				t.Fatal(err)
			}
			if err := host.MarkReady(); err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(host)
			server.Client().Timeout = 2 * time.Second
			t.Cleanup(func() { host.BeginDrain(); server.Close() })
			heartbeat(t, m.relay, 1, "session_abcdefgh")
			heartbeat(t, m.relay, 2, "session_abcdefgh")
			allowed := min(MaxStreams, capacity-1)
			readers := make([]*eventReader, 0, allowed)
			for i := 0; i < allowed; i++ {
				token := aliceToken
				if i%2 == 1 {
					token = bobToken
				}
				response := streamRequest(t, server, context.Background(), streamPath, token)
				if response.StatusCode != http.StatusOK {
					t.Fatalf("stream %d: %d", i, response.StatusCode)
				}
				reader := readEvents(response.Body)
				batchEvent(t, reader, "ready")
				readers = append(readers, reader)
			}
			waitQuota(t, m, allowed)
			extraToken := aliceToken
			if allowed%2 == 1 {
				extraToken = bobToken
			}
			extra := streamRequest(t, server, context.Background(), streamPath, extraToken)
			extra.Body.Close()
			if extra.StatusCode != http.StatusTooManyRequests {
				t.Fatalf("extra stream was not quota-rejected: %d", extra.StatusCode)
			}
			waitQuota(t, m, allowed)
			// Real heartbeat and command handlers still pass through both admission gates.
			requireStatus(t, request(t, host, http.MethodPut, "/pi/sessions/session_abcdefgh", aliceToken, putInput()), http.StatusOK)
			requireStatus(t, request(t, host, http.MethodPost, "/pi/sessions/session_abcdefgh/messages", aliceToken, postInput()), http.StatusOK)
			release, err := host.TryAcquire()
			if err != nil {
				t.Fatalf("SSE retained the worker admission slot: %v", err)
			}
			release()
			// Drain owns stream cancellation and must join all host permits.
			host.BeginDrain()
			for _, reader := range readers {
				waitDone(t, reader)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := host.Wait(ctx); err != nil {
				t.Fatal(err)
			}
			waitQuota(t, m, 0)
		})
	}
}
