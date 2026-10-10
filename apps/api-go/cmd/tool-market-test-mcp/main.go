// tool-market-test-mcp is a standalone deterministic MCP service for release
// qualification. Running it never uploads or publishes a marketplace service.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/internal/toolmarketfixture"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:8123", "listen address; plain HTTP is restricted to loopback")
	cert := flag.String("tls-cert", "", "TLS certificate file (requires -tls-key)")
	key := flag.String("tls-key", "", "TLS private key file (requires -tls-cert)")
	flag.Parse()
	if err := run(*listen, *cert, *key); err != nil {
		log.Fatal(err)
	}
}

func validateListen(address, cert, key string) error {
	if (cert == "") != (key == "") {
		return errors.New("both -tls-cert and -tls-key are required")
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return errors.New("-listen must be a host:port address")
	}
	if cert == "" {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return errors.New("plain HTTP test service must listen on a loopback IP; use TLS or a HTTPS reverse proxy")
		}
	}
	return nil
}

func run(address, cert, key string) error {
	if err := validateListen(address, cert, key); err != nil {
		return err
	}
	bearer := os.Getenv("MARKET_TEST_MCP_BEARER")
	if bearer != "" && (len(bearer) < 24 || len(bearer) > 4096 || strings.IndexFunc(bearer, func(r rune) bool { return r <= ' ' || r == 127 }) >= 0) {
		return errors.New("MARKET_TEST_MCP_BEARER must contain 24–4096 non-whitespace characters")
	}
	options := toolmarketfixture.Options{Bearer: bearer}
	server := &http.Server{Addr: address, Handler: toolmarketfixture.Handler(toolmarketfixture.NewServer(options), options), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 32 << 10}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	done := make(chan error, 1)
	go func() {
		if cert != "" {
			done <- server.ListenAndServeTLS(cert, key)
		} else {
			done <- server.ListenAndServe()
		}
	}()
	log.Printf("TEST MCP listening on %s; path=/mcp; bearer_required=%t; no real model or payment calls", address, bearer != "")
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return server.Shutdown(shutdown)
	}
}
