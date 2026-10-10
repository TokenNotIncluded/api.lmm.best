package transportpolicy

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// This opt-in probe uses real HTTP/2 and the unchanged Capabilities wire format.
// Its peer is the Python laboratory fixture, NOT the production Rust core.
func TestGoGRPCFixture(t *testing.T) {
	root := os.Getenv("DP24_FIXTURE_ROOT")
	if root == "" {
		t.Skip("isolated fixture was not requested")
	}
	for _, path := range []string{"fixture.sqlite", "a.key", "b.key"} {
		if _, err := os.ReadFile(filepath.Join(root, "core", path)); err == nil {
			t.Fatal("extension can read core-only test files")
		}
	}
	for _, key := range []string{"DATABASE_URL", "LMM_CORE_DATABASE_URL", "PGPASSWORD"} {
		if os.Getenv(key) != "" {
			t.Fatal("extension inherited a core database credential")
		}
	}
	read := func(name string) []byte {
		b, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	var pins map[string]string
	if err := json.Unmarshal(read("pins.json"), &pins); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	var store Store
	p := Policy{Version: 1, IssuedAt: now.Add(-time.Second), ExpiresAt: now.Add(30 * time.Second), Peers: []Peer{{URI: "spiffe://lmm.test/core/a", SHA256: pins["a"], NotAfter: now.Add(30 * time.Second)}}}
	if err := store.Replace(p, now); err != nil {
		t.Fatal(err)
	}
	tlsConfig, err := ClientTLS(read("ca.pem"), read("clients/old.pem"), read("clients/old.key"), "core-a.dp24.test", "spiffe://lmm.test/core/a", store.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var dialer net.Dialer
	tr := &http.Transport{TLSClientConfig: tlsConfig, ForceAttemptHTTP2: true, MaxConnsPerHost: 1, MaxIdleConnsPerHost: 1, MaxIdleConns: 1, MaxResponseHeaderBytes: 8192, TLSHandshakeTimeout: time.Second, ResponseHeaderTimeout: time.Second,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			return dialer.DialContext(ctx, "tcp", os.Getenv("DP24_FIXTURE_ADDRESS"))
		}}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	reused := false
	call := func() (int, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		var mu sync.Mutex
		var peerError error
		ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) {
			mu.Lock()
			defer mu.Unlock()
			reused = reused || info.Reused
			conn, ok := info.Conn.(*tls.Conn)
			if !ok {
				peerError = ErrDenied
				cancel()
				return
			}
			if err := store.Snapshot().CheckPeer(conn.ConnectionState(), "spiffe://lmm.test/core/a", time.Now()); err != nil {
				peerError = err
				cancel()
			}
		}})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://core-a.dp24.test/lmm.core.v1.CoreControl/Capabilities", bytes.NewReader([]byte{0, 0, 0, 0, 0}))
		if err != nil {
			return 0, err
		}
		req.GetBody = nil // No replayable request body or idempotency headers.
		req.Header.Set("Content-Type", "application/grpc")
		req.Header.Set("TE", "trailers")
		req.Header.Set("authorization", "Bearer dp24_"+"test_only_test_only_test_only_test_only_")
		req.Header.Set("x-lmm-protocol", "1")
		resp, err := client.Do(req)
		mu.Lock()
		verificationError := peerError
		mu.Unlock()
		if verificationError != nil {
			if resp != nil {
				resp.Body.Close()
			}
			return 0, verificationError
		}
		if err != nil {
			return 0, err
		}
		defer resp.Body.Close()
		raw, err := io.ReadAll(io.LimitReader(resp.Body, 65542))
		if err != nil {
			return 0, err
		}
		if resp.ProtoMajor != 2 || resp.StatusCode != 200 || resp.Trailer.Get("Grpc-Status") != "0" || len(raw) != 9 || raw[0] != 0 || binary.BigEndian.Uint32(raw[1:5]) != 4 || !bytes.Equal(raw[5:], []byte{8, 1, 16, 1}) {
			t.Fatalf("unexpected bounded gRPC response: HTTP %d protocol %d grpc status %q bytes %x", resp.StatusCode, resp.ProtoMajor, resp.Trailer.Get("Grpc-Status"), raw)
		}
		return len(raw), nil
	}
	for i := 0; i < 2; i++ {
		if _, err := call(); err != nil {
			t.Fatal(err)
		}
	}
	if !reused {
		t.Fatal("connection was not reused")
	}
	p.Version++
	p.Peers[0].NotAfter = time.Now().Add(-time.Second)
	if err := store.Replace(p, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := call(); err != ErrDenied {
		t.Fatalf("revoked server peer not rejected before RPC: %v", err)
	}
	t.Log("real gRPC wire accepted twice; one connection reused; revocation blocked next RPC; core files masked")
}
