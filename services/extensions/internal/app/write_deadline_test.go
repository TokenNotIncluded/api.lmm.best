package app

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules"
)

type writeDeadlineModule struct{ handler http.Handler }

func (m writeDeadlineModule) Name() string          { return "deadline" }
func (m writeDeadlineModule) Handler() http.Handler { return m.handler }

// Keep the slow-reader case independent of platform TCP send-buffer defaults.
type smallSendBufferListener struct{ net.Listener }

func (l smallSendBufferListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	if err := conn.(*net.TCPConn).SetWriteBuffer(1024); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

// @mark extensions.http-write-deadline role=test
func TestSlowResponseWriteReleasesHostAdmission(t *testing.T) {
	credential := []byte("isolated-write-deadline-credential-000001")
	outcome := make(chan error, 1)
	handled := make(chan struct{})
	module := writeDeadlineModule{http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/large" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		chunk := bytes.Repeat([]byte("x"), 32<<10)
		for i := 0; i < 512; i++ {
			if _, err := w.Write(chunk); err != nil {
				outcome <- err
				return
			}
		}
		outcome <- nil
	})}
	host, err := modules.NewHostWithLimit([]modules.Module{module}, credential, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := host.MarkReady(); err != nil {
		t.Fatal(err)
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host.ServeHTTP(w, r)
		if r.URL.Path == "/extensions/v1/deadline/large" {
			close(handled)
		}
	})
	server := httptest.NewUnstartedServer(handler)
	server.Config = newHTTPServer("", handler)
	if server.Config.WriteTimeout <= 0 || server.Config.WriteTimeout > 30*time.Second {
		t.Fatalf("ordinary response write deadline is unbounded: %s", server.Config.WriteTimeout)
	}
	// Shorten only the real server's socket deadline, never mock the writer.
	server.Config.WriteTimeout = 200 * time.Millisecond
	server.Listener = smallSendBufferListener{server.Listener}
	server.Start()
	defer server.Close()
	conn, err := net.DialTimeout("tcp", server.Listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.(*net.TCPConn).SetReadBuffer(1024); err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintf(conn, "GET /extensions/v1/deadline/large HTTP/1.1\r\nHost: local\r\nAuthorization: Bearer %s\r\n\r\n", credential); err != nil {
		t.Fatal(err)
	}
	// Intentionally never read from conn. The client stays connected until after
	// the host releases its permit, so disconnect cannot explain recovery.
	select {
	case <-handled:
	case <-time.After(3 * time.Second):
		t.Fatal("slow reader retained host admission")
	}
	var timeout net.Error
	if err := <-outcome; !errors.As(err, &timeout) || !timeout.Timeout() {
		t.Fatalf("expected actual socket write timeout, got %v", err)
	}
	request, err := http.NewRequest(http.MethodGet, server.URL+"/extensions/v1/deadline/probe", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+string(credential))
	client := &http.Client{Timeout: time.Second}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("host did not recover: %d", response.StatusCode)
	}
	host.BeginDrain()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := host.Wait(ctx); err != nil {
		t.Fatal(err)
	}
}

// @mark extensions.http-write-deadline role=test
func TestStreamingWriteDeadlineOverridesServerDefault(t *testing.T) {
	next := make(chan struct{})
	failure := make(chan error, 1)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		controller := http.NewResponseController(w)
		send := func(message string) error {
			if err := controller.SetWriteDeadline(time.Now().Add(2 * time.Second)); err != nil {
				return err
			}
			if _, err := fmt.Fprintf(w, "data: %s\n\n", message); err != nil {
				return err
			}
			return controller.Flush()
		}
		defer controller.SetWriteDeadline(time.Time{})
		w.Header().Set("Content-Type", "text/event-stream")
		if err := send("first"); err != nil {
			failure <- err
			return
		}
		select {
		case <-next:
		case <-r.Context().Done():
			failure <- r.Context().Err()
			return
		}
		failure <- send("second")
	})
	server := httptest.NewUnstartedServer(handler)
	server.Config = newHTTPServer("", handler)
	server.Config.WriteTimeout = 100 * time.Millisecond
	server.Start()
	defer server.Close()
	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	reader := bufio.NewReader(response.Body)
	if line, err := reader.ReadString('\n'); err != nil || line != "data: first\n" {
		t.Fatalf("first event: %q %v", line, err)
	}
	if _, err := reader.ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	// Cross the ordinary response deadline while the stream is idle.
	time.Sleep(250 * time.Millisecond)
	close(next)
	if line, err := reader.ReadString('\n'); err != nil || line != "data: second\n" {
		t.Fatalf("second event after server deadline: %q %v", line, err)
	}
	if err := <-failure; err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, reader); err != nil {
		t.Fatal(err)
	}
}
