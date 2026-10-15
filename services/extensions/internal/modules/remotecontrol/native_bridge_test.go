package remotecontrol

import (
	"context"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/coreclient"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules"
)

// Invoked by Core's remote_control integration test, not a synthetic authorizer.
// Core owns the new PostgreSQL fixture, public Axum listener and Rust RPC.
func TestNativeRelayBridge(t *testing.T) {
	root := os.Getenv("LMM_NATIVE_REMOTE_FIXTURE_DIR")
	if root == "" {
		t.Skip("requires Core-owned native relay fixture")
	}
	token, err := coreclient.ReadTokenFile(filepath.Join(root, "rpc.token"))
	if err != nil {
		t.Fatal(err)
	}
	client, err := coreclient.New(filepath.Join(root, "core.sock"), token)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	module, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	hostToken, err := coreclient.ReadTokenFile(filepath.Join(root, "host.token"))
	if err != nil {
		t.Fatal(err)
	}
	host, err := modules.NewHost([]modules.Module{module}, hostToken)
	if err != nil {
		t.Fatal(err)
	}
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := httptest.NewUnstartedServer(host)
	server.Config.BaseContext = func(net.Listener) context.Context { return base }
	server.Start()
	defer server.Close()
	if err := host.MarkReady(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "host.addr"), []byte(server.URL), 0600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(90 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(root, "stop")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Core fixture did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
	host.BeginDrain()
	ctx, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	if err := host.Wait(ctx); err != nil {
		t.Fatal("stream quota/host permits not released", err)
	}
	module.streamsMu.Lock()
	count := module.streamCount
	module.streamsMu.Unlock()
	if count != 0 {
		t.Fatal("stream quota leaked", count)
	}
}
