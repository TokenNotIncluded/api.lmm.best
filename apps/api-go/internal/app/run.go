// Package app assembles the Go extension process. It has no core database,
// identity authority, wallet, model routing, or schema-upgrade entry point.
package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/coreclient"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/identity"
)

func Run() error {
	if len(os.Args) != 1 {
		return errors.New("lmm-extensions accepts no subcommands; configure it with LMM_EXTENSION_* variables")
	}
	credential, err := readHostCredential(os.Getenv("LMM_EXTENSION_TOKEN_FILE"))
	if err != nil {
		return err
	}
	socket, tokenFile := os.Getenv("LMM_CORE_RPC_SOCKET"), os.Getenv("LMM_CORE_RPC_TOKEN_FILE")
	if (socket == "") != (tokenFile == "") {
		return errors.New("configure both core RPC socket and credential file")
	}
	var defaults []string
	if socket != "" {
		defaults = []string{"identity"}
	}
	selected, err := modules.Select(os.Getenv("LMM_EXTENSION_MODULES"), defaults, []string{"identity"})
	if err != nil {
		return err
	}
	var registered []modules.Module
	for _, name := range selected {
		switch name {
		case "identity":
			if socket == "" {
				return errors.New("identity module requires core RPC configuration")
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
			// Construction is lazy: a stopped Rust process cannot block Go startup.
			registered = append(registered, identity.New(client))
		}
	}
	handler, err := modules.New(registered, credential)
	if err != nil {
		return err
	}
	address := os.Getenv("LMM_EXTENSION_LISTEN")
	if address == "" {
		address = "0.0.0.0:8081"
	}
	server := &http.Server{
		Addr: address, Handler: handler,
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second,
		IdleTimeout: time.Minute, MaxHeaderBytes: 16 << 10,
	}
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

func readHostCredential(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open extension service credential: %w", err)
	}
	credential, readErr := io.ReadAll(io.LimitReader(file, 4097))
	if err := errors.Join(readErr, file.Close()); err != nil {
		return nil, err
	}
	if len(credential) > 4096 {
		return nil, errors.New("extension service credential file is too large")
	}
	return bytes.TrimSpace(credential), nil
}
