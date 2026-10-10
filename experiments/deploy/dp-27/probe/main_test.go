package main

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestRejectPort(t *testing.T) {
	for _, port := range []string{"80", "443", "8080/other", "example.org", ""} {
		if check(context.Background(), port) == nil {
			t.Fatalf("accepted %q", port)
		}
	}
}

func TestProbe(t *testing.T) {
	for _, status := range []int{200, 204, 302, 503} {
		listener, err := net.Listen("tcp", "127.0.0.1:8081")
		if err != nil {
			t.Fatal(err)
		}
		server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/health/live" {
				t.Errorf("wrong path: %s", r.URL.Path)
			}
			if status == 302 {
				w.Header().Set("Location", "http://127.0.0.1:9/not-allowed")
			}
			w.WriteHeader(status)
		}), ReadHeaderTimeout: time.Second}
		go server.Serve(listener)
		err = check(context.Background(), "8081")
		server.Close()
		if (err == nil) != (status == 200) {
			t.Fatalf("status %d: %v", status, err)
		}
	}
}

func TestCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if check(ctx, "8081") == nil {
		t.Fatal("accepted canceled request")
	}
}

func TestMalformedStatus(t *testing.T) {
	for _, value := range []string{"not-http\r\n", "HTTP/1.1 2000 OK\r\n", "HTTP/1.1 200 OK\n", "HTTP/1.1 200\r\n", "HTTP/1.1 200 OK\r\n", "HTTP/1.1 200 OK\r\ninvalid\r\n\r\n"} {
		listener, err := net.Listen("tcp", "127.0.0.1:8081")
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(time.Second))
			request := make([]byte, 512)
			conn.Read(request)
			conn.Write([]byte(value))
		}()
		err = check(context.Background(), "8081")
		listener.Close()
		<-done
		if err == nil {
			t.Fatalf("accepted invalid status %q", value)
		}
	}
}
