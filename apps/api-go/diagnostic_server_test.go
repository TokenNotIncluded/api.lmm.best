package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	httppprof "net/http/pprof"
	"runtime/pprof"
	"testing"
	"time"
)

func diagnosticListener(t *testing.T) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	return listener
}

func awaitDiagnosticResult(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal("diagnostic server or request failed to stop")
		return nil
	}
}

func awaitDiagnosticStart(t *testing.T, started <-chan struct{}) {
	t.Helper()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("diagnostic handler did not start")
	}
}

func diagnosticRequest(url string) <-chan error {
	done := make(chan error, 1)
	go func() {
		client := &http.Client{Timeout: 3 * time.Second}
		response, err := client.Get(url)
		if err == nil {
			_, readErr := io.Copy(io.Discard, response.Body)
			err = errors.Join(readErr, response.Body.Close())
			if response.StatusCode != http.StatusOK {
				err = errors.Join(err, fmt.Errorf("unexpected HTTP status %d", response.StatusCode))
			}
		}
		done <- err
	}()
	return done
}

func TestDiagnosticServerCancelsRequestsAndClosesListener(t *testing.T) {
	listener := diagnosticListener(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- serveDiagnosticServer(ctx, listener, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			close(started)
			<-r.Context().Done()
			w.WriteHeader(http.StatusOK)
		}), time.Second)
	}()
	request := diagnosticRequest("http://" + listener.Addr().String())
	awaitDiagnosticStart(t, started)
	cancel()
	if err := awaitDiagnosticResult(t, done); err != nil {
		t.Fatal(err)
	}
	if err := awaitDiagnosticResult(t, request); err != nil {
		t.Fatal(err)
	}
	if connection, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second); err == nil {
		connection.Close()
		t.Fatal("diagnostic listener remained open")
	}
}

func TestDiagnosticServerClosesIdleConnections(t *testing.T) {
	listener := diagnosticListener(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() {
		done <- serveDiagnosticServer(ctx, listener, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Length", "0")
			w.WriteHeader(http.StatusOK)
		}), time.Second)
	}()
	connection, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if err := connection.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprint(connection, "GET / HTTP/1.1\r\nHost: localhost\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(connection)
	response, err := http.ReadResponse(reader, nil)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	cancel()
	if err := awaitDiagnosticResult(t, done); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.ReadByte(); !errors.Is(err, io.EOF) {
		t.Fatalf("idle connection did not close: %v", err)
	}
}

func TestDiagnosticServerCancelsCPUProfile(t *testing.T) {
	listener := diagnosticListener(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- serveDiagnosticServer(ctx, listener, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			close(started)
			httppprof.Profile(w, r)
		}), time.Second)
	}()
	request := diagnosticRequest("http://" + listener.Addr().String() + "/debug/pprof/profile?seconds=60")
	awaitDiagnosticStart(t, started)
	cancel()
	if err := awaitDiagnosticResult(t, done); err != nil {
		t.Fatal(err)
	}
	if err := awaitDiagnosticResult(t, request); err != nil {
		t.Fatal(err)
	}
	// The process-global profiler must be released after cancellation.
	if err := pprof.StartCPUProfile(io.Discard); err != nil {
		t.Fatal(err)
	}
	pprof.StopCPUProfile()
}

func TestDiagnosticServerForcesCloseAfterDrainTimeout(t *testing.T) {
	listener := diagnosticListener(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	started, release, stopped := make(chan struct{}), make(chan struct{}), make(chan struct{})
	defer close(release)
	done := make(chan error, 1)
	go func() {
		done <- serveDiagnosticServer(ctx, listener, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			close(started)
			<-release
			close(stopped)
		}), 20*time.Millisecond)
	}()
	request := diagnosticRequest("http://" + listener.Addr().String())
	awaitDiagnosticStart(t, started)
	cancel()
	if err := awaitDiagnosticResult(t, done); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected bounded drain failure, got %v", err)
	}
	if err := awaitDiagnosticResult(t, request); err == nil {
		t.Fatal("expected the active connection to be closed")
	}
	// The deliberately non-cooperative test handler is released during cleanup.
	t.Cleanup(func() { awaitDiagnosticStart(t, stopped) })
}

func TestDiagnosticServerReturnsServeAndBindFailures(t *testing.T) {
	listener := diagnosticListener(t)
	if err := runDiagnosticServer(context.Background(), listener.Addr().String(), http.NotFoundHandler()); err == nil {
		t.Fatal("expected occupied-port error")
	}
	listener.Close()
	if err := serveDiagnosticServer(context.Background(), listener, http.NotFoundHandler(), time.Second); err == nil {
		t.Fatal("expected closed-listener error")
	}
}

func TestDiagnosticServerRejectsCanceledStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := runDiagnosticServer(ctx, "127.0.0.1:0", http.NotFoundHandler()); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected canceled startup, got %v", err)
	}
}
