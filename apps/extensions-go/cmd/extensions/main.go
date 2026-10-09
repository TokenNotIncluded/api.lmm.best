package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/coreclient"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/identity"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
func run() error {
	file, err := os.Open(os.Getenv("LMM_EXTENSION_TOKEN_FILE"))
	if err != nil {
		return fmt.Errorf("open extension service credential: %w", err)
	}
	credential, readErr := io.ReadAll(io.LimitReader(file, 4097))
	closeErr := file.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return err
	}
	if len(credential) > 4096 {
		return errors.New("extension service credential file is too large")
	}
	var registered []modules.Module
	socket, tokenFile := os.Getenv("LMM_CORE_RPC_SOCKET"), os.Getenv("LMM_CORE_RPC_TOKEN_FILE")
	if socket != "" || tokenFile != "" {
		if socket == "" || tokenFile == "" {
			return errors.New("configure both core RPC socket and credential file")
		}
		token, err := coreclient.ReadTokenFile(tokenFile)
		if err != nil {
			return err
		}
		client, err := coreclient.New(socket, token)
		if err != nil {
			return err
		}
		defer client.Close()
		// Construction does not dial; unrelated modules can start while Rust is down.
		registered = append(registered, identity.New(client))
	}
	handler, err := modules.New(registered, bytes.TrimSpace(credential))
	if err != nil {
		return err
	}
	address := os.Getenv("LMM_EXTENSION_LISTEN")
	if address == "" {
		address = "0.0.0.0:8081"
	}
	server := &http.Server{Addr: address, Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: time.Minute, MaxHeaderBytes: 16 << 10}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	result := make(chan error, 1)
	go func() { result <- server.ListenAndServe() }()
	select {
	case err := <-result:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			return errors.Join(err, server.Close())
		}
		err := <-result
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}
