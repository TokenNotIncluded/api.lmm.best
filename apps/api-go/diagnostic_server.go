package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"
)

// Keep diagnostic draining below the runtime loop shutdown budget.
const diagnosticShutdownTimeout = 5 * time.Second

func runDiagnosticServer(ctx context.Context, address string, handler http.Handler) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var config net.ListenConfig
	listener, err := config.Listen(ctx, "tcp", address)
	if err != nil {
		return err
	}
	return serveDiagnosticServer(ctx, listener, handler, diagnosticShutdownTimeout)
}

// serveDiagnosticServer owns the listener and joins its Serve goroutine before
// returning. BaseContext cancels in-flight pprof/trace requests on shutdown.
func serveDiagnosticServer(ctx context.Context, listener net.Listener, handler http.Handler, timeout time.Duration) error {
	defer listener.Close()
	requestCtx, cancelRequests := context.WithCancel(ctx)
	defer cancelRequests()
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return requestCtx },
	}
	defer server.Close()
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()

	var serveErr, shutdownErr error
	select {
	case serveErr = <-done:
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		shutdownErr = server.Shutdown(shutdownCtx)
		if shutdownErr != nil {
			shutdownErr = errors.Join(shutdownErr, server.Close())
		}
		serveErr = <-done
	}
	if errors.Is(serveErr, http.ErrServerClosed) {
		serveErr = nil
	}
	return errors.Join(shutdownErr, serveErr)
}
