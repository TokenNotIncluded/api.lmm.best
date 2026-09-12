//go:build !windows

package appcli

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestProductionBillingGateStopsBeforeMigrationOnBadDrain(t *testing.T) {
	for _, mode := range []string{"nginx-timeout", "active-generation", "writer-killed", "flush-failed", "refund-intent", "missing-history", "journal-loss"} {
		t.Run(mode, func(t *testing.T) {
			f := newProductionFixture(t)
			switch mode {
			case "refund-intent":
				f.runner.refundIntent = true
			case "missing-history":
				f.runner.missingStartup = true
			case "journal-loss":
				f.runner.journalLoss = true
			case "active-generation":
				f.runtime.billingConnections = func() (int, error) { return 1, nil }
			case "nginx-timeout":
				f.runner.nginxDrainFailure = true
			case "writer-killed":
				f.runner.badWriterStop = true
			case "flush-failed":
				f.runner.shutdownJournalFailure = true
			}
			if _, err := f.runtime.apply(context.Background(), f.workspace, f.options); err == nil {
				t.Fatal("unsafe drain accepted")
			}
			for _, event := range f.runner.events {
				if event == "migrate:--apply" || event == "paru-go" {
					t.Fatalf("mutation after failed gate: %s", event)
				}
				if (mode == "nginx-timeout" || mode == "active-generation" || mode == "refund-intent" || mode == "missing-history" || mode == "journal-loss") && event == "systemd-stop" {
					t.Fatal("backend stopped before existing requests drained")
				}
			}
			status, err := f.runtime.readStatus(f.workspace)
			if err != nil || status.Phase != "ROLLBACK_REQUIRED" {
				t.Fatalf("recovery state=%+v err=%v", status, err)
			}
		})
	}
}

func TestProductionBillingGateBlocksManagedRollback(t *testing.T) {
	for _, count := range []string{"1", "42", " ", "not-a-count"} {
		t.Run(count, func(t *testing.T) {
			f := newProductionFixture(t)
			if _, err := f.runtime.apply(context.Background(), f.workspace, f.options); err != nil {
				t.Fatal(err)
			}
			f.runner.managedBillingRows = count
			before := len(f.runner.events)
			if _, err := f.runtime.rollback(context.Background(), f.workspace, "billing-review"); err == nil {
				t.Fatal("managed rollback accepted")
			}
			if f.runner.serviceActive || !f.runner.nginxClosed {
				t.Fatal("blocked rollback reopened writes")
			}
			for _, event := range f.runner.events[before:] {
				if event == "paru-go" || event == "systemd-start" {
					t.Fatal("old writer activated")
				}
			}
			status, err := f.runtime.readStatus(f.workspace)
			if err != nil || status.Phase != "ROLLBACK_REQUIRED" {
				t.Fatalf("state=%+v err=%v", status, err)
			}
		})
	}
}

func TestProductionBillingShutdownRequiresPositiveCleanEvidence(t *testing.T) {
	for _, text := range []string{"", "server exited", "received signal: terminated\nserver exited\nfailed to batch update token quota", "received signal: terminated\nshutdown completed with errors\nserver exited"} {
		if validateBillingShutdownJournal([]byte(text)) == nil {
			t.Fatalf("accepted %q", text)
		}
	}
	if err := validateBillingShutdownJournal([]byte("received signal: terminated\nbatch update finished\nserver exited")); err != nil {
		t.Fatal(err)
	}
}

func TestProductionBillingGateWaitsForExistingGeneration(t *testing.T) {
	f := newProductionFixture(t)
	reads := 0
	f.runtime.billingConnections = func() (int, error) {
		reads++
		if reads < 4 {
			return 1, nil
		}
		return 0, nil
	}
	if _, err := f.runtime.apply(context.Background(), f.workspace, f.options); err != nil {
		t.Fatal(err)
	}
	if reads != 4 {
		t.Fatalf("upstream was not drained: reads=%d", reads)
	}
	for _, c := range f.runner.commands {
		if c.Name == commandSystemctl && (c.Args[0] == "stop" || c.Args[0] == "kill" || c.Args[0] == "restart") && strings.Contains(strings.Join(c.Args, " "), "nginx") {
			t.Fatal("deployment stopped independent nginx services")
		}
	}
}

// Actual nginx reload: LMM becomes 503 while an unrelated listener stays 200
// and an already-running LMM generation finishes normally.
func TestProductionBillingGateRealNginxScopedBarrier(t *testing.T) {
	binary, err := exec.LookPath("nginx")
	if err != nil {
		t.Skip("nginx is required")
	}
	started, release := make(chan struct{}, 1), make(chan struct{})
	var once sync.Once
	releaseRequest := func() { once.Do(func() { close(release) }) }
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case started <- struct{}{}:
		default:
		}
		<-release
		_, _ = io.WriteString(w, "finished")
	}))
	defer backend.Close()
	defer releaseRequest()
	allocate := func() string {
		l, e := net.Listen("tcp", "127.0.0.1:0")
		if e != nil {
			t.Fatal(e)
		}
		a := l.Addr().String()
		_ = l.Close()
		return a
	}
	address, other := allocate(), allocate()
	root := t.TempDir()
	locations := filepath.Join(root, "lmm-locations.conf")
	original := []byte(fmt.Sprintf("error_page 500 502 503 504 =503 /error; location = /error { return 200 'rewritten-error'; } location @lmm_api_backend { proxy_pass %s; } location / { proxy_pass %s; proxy_buffering off; }", backend.URL, backend.URL))
	if err := os.WriteFile(locations, original, 0600); err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf("pid %s/nginx.pid; error_log stderr notice; worker_processes 1; events {} http { access_log off; client_body_temp_path %s/body; proxy_temp_path %s/proxy; fastcgi_temp_path %s/f; uwsgi_temp_path %s/u; scgi_temp_path %s/s; server { listen %s; include %s; } server { listen %s; return 200 'independent'; } }", root, root, root, root, root, root, address, locations, other)
	path := filepath.Join(root, "nginx.conf")
	if err := os.WriteFile(path, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(binary, "-e", "stderr", "-p", root, "-c", path, "-g", "daemon off;")
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- command.Wait() }()
	defer func() { releaseRequest(); _ = command.Process.Kill() }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		c, e := net.DialTimeout("tcp", address, 100*time.Millisecond)
		if e == nil {
			_ = c.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("nginx not ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	response := make(chan error, 1)
	go func() {
		resp, e := (&http.Client{Timeout: 10 * time.Second}).Get("http://" + address + "/slow")
		if e == nil {
			body, re := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			e = re
			if string(body) != "finished" {
				e = fmt.Errorf("body=%q", body)
			}
		}
		response <- e
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("request not started")
	}
	barrier, err := billingBarrier(original, "test-scoped")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(locations, barrier, 0600); err != nil {
		t.Fatal(err)
	}
	if err := command.Process.Signal(syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	check := func(addr, want string, status int) {
		client := &http.Client{Timeout: 200 * time.Millisecond, Transport: &http.Transport{DisableKeepAlives: true}}
		defer client.CloseIdleConnections()
		until := time.Now().Add(5 * time.Second)
		for {
			resp, e := client.Get("http://" + addr + "/v1/models")
			if e == nil {
				body, _ := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				if resp.StatusCode == status && string(body) == want {
					return
				}
			}
			if time.Now().After(until) {
				t.Fatalf("listener %s failed expected %d", addr, status)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	check(address, "lmm-billing-drain:test-scoped", 503)
	check(other, "independent", 200)
	select {
	case err := <-response:
		t.Fatalf("existing request interrupted: %v", err)
	default:
	}
	releaseRequest()
	if err := <-response; err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(locations, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := command.Process.Signal(syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	check(address, "finished", 200)
	check(other, "independent", 200)
	_ = command.Process.Signal(syscall.SIGQUIT)
	select {
	case err := <-exited:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("test nginx did not exit")
	}
}

func TestProductionBillingGateRecordsLiveEvidence(t *testing.T) {
	f := newProductionFixture(t)
	if _, err := f.runtime.apply(context.Background(), f.workspace, f.options); err != nil {
		t.Fatal(err)
	}
	manifest, err := f.runtime.readManifest(f.workspace)
	if err != nil {
		t.Fatal(err)
	}
	g := manifest.BillingGate
	if g == nil || !g.StopVerified || !g.AdmissionClosed || !g.AdmissionReopened || len(g.ShutdownJournalSHA256) != 64 {
		t.Fatalf("missing live gate evidence: %+v", g)
	}
	found := false
	for _, c := range f.runner.commands {
		if c.Name == commandJournalctl && strings.Contains(strings.Join(c.Args, " "), "_SYSTEMD_INVOCATION_ID=") {
			found = true
		}
	}
	if !found {
		t.Fatal("journal was not bound to writer invocation")
	}
}
