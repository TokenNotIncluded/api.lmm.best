package app

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules"
)

type observedDrainHost struct {
	*modules.Host
	joined chan struct{}
	once   sync.Once
}

func (h *observedDrainHost) Wait(ctx context.Context) error {
	if ctx.Done() == nil {
		h.once.Do(func() { close(h.joined) })
	}
	return h.Host.Wait(ctx)
}

func awaitDrainSignal(t *testing.T, signal <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", name)
	}
}

func TestExceptionalDrainJoinsBeforeResourcesClose(t *testing.T) {
	for _, scenario := range []string{"serve-error", "shutdown-timeout", "worker-wait-timeout"} {
		t.Run(scenario, func(t *testing.T) {
			host, err := modules.NewHost(nil, []byte("isolated-drain-test-credential-00000001"))
			if err != nil {
				t.Fatal(err)
			}
			observed := &observedDrainHost{Host: host, joined: make(chan struct{})}
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			held, release, cancelled := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			defer unblock()
			resourcesClosed, finished := make(chan struct{}), make(chan error, 1)
			server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				permit, ok := host.Acquire()
				if !ok {
					t.Error("request not admitted")
					return
				}
				defer permit()
				close(held)
				<-r.Context().Done()
				close(cancelled)
				// Simulate required durable bookkeeping after cancellation: the
				// connection is closed, but storage is still owned by this permit.
				<-release
			})}
			ready := make(chan struct{})
			start := func(work context.Context) {
				if scenario == "worker-wait-timeout" {
					permit, ok := host.Acquire()
					if !ok {
						t.Error("worker not admitted")
						return
					}
					go func() {
						defer permit()
						close(held)
						<-work.Done()
						close(cancelled)
						<-release
					}()
				}
				close(ready)
			}
			go func() {
				err := serveAndDrain(ctx, server, listener, observed, start, 10*time.Millisecond)
				// The production caller defers runtime.Close until this return.
				close(resourcesClosed)
				finished <- err
			}()
			awaitDrainSignal(t, ready, "ready host")
			if scenario != "worker-wait-timeout" {
				transport := &http.Transport{Proxy: nil, DisableKeepAlives: true}
				defer transport.CloseIdleConnections()
				client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
				go func() {
					response, err := client.Get("http://" + listener.Addr().String() + "/held")
					if err == nil {
						response.Body.Close()
					}
				}()
			}
			awaitDrainSignal(t, held, "admitted work")
			if scenario == "serve-error" {
				if err := listener.Close(); err != nil {
					t.Fatal(err)
				}
			} else {
				cancel()
			}
			awaitDrainSignal(t, observed.joined, "fresh unbounded post-shutdown join")
			awaitDrainSignal(t, cancelled, "accepted work cancellation")
			if permit, ok := host.Acquire(); ok {
				permit()
				t.Fatal("drain admitted new work")
			}
			select {
			case <-resourcesClosed:
				t.Fatal("resources closed while a permit was held")
			default:
			}
			unblock()
			select {
			case err := <-finished:
				want := error(context.DeadlineExceeded)
				if scenario == "serve-error" {
					want = net.ErrClosed
				}
				if !errors.Is(err, want) {
					t.Fatalf("original failure lost: got %v, want %v", err, want)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("drain did not finish after permit release")
			}
		})
	}
}
