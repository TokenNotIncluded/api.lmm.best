// Package app assembles the Go extension process. It has no core database,
// identity authority, wallet, model routing, or schema-upgrade entry point.
package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/composition"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/legal"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/webcompat"
)

func Run() error {
	if len(os.Args) != 1 {
		return errors.New("lmm-extensions accepts no subcommands; configure it with LMM_EXTENSION_* variables")
	}
	if err := validateEnvironment(os.Getenv); err != nil {
		return err
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
	selected, err := modules.Select(os.Getenv("LMM_EXTENSION_MODULES"), defaults, []string{"identity", "store", "support"})
	if err != nil {
		return err
	}
	webEnabled := os.Getenv("LMM_EXTENSION_WEB_COMPAT")
	if webEnabled != "" && webEnabled != "false" && webEnabled != "true" {
		return errors.New("LMM_EXTENSION_WEB_COMPAT must be true or false")
	}
	runtime, err := composition.New(context.Background(), composition.Config{
		Selected: selected, Socket: socket, TokenFile: tokenFile,
		Store:   composition.StorageConfig{Component: "store", URLFile: os.Getenv("LMM_STORE_DATABASE_URL_FILE"), Database: os.Getenv("LMM_STORE_DATABASE_NAME")},
		Support: composition.StorageConfig{Component: "support", URLFile: os.Getenv("LMM_SUPPORT_DATABASE_URL_FILE"), Database: os.Getenv("LMM_SUPPORT_DATABASE_NAME")},
	})
	if err != nil {
		return err
	}
	defer runtime.Close()
	host, err := modules.NewHost(runtime.Modules, credential)
	if err != nil {
		return err
	}
	policies, err := legal.Load(os.Getenv("LMM_LEGAL_CONFIG_FILE"))
	if err != nil {
		return err
	}
	var api http.Handler = host
	if webEnabled == "true" {
		if runtime.Store == nil {
			return errors.New("Web compatibility requires the store module")
		}
		auth, err := webcompat.NewAuthenticator(runtime.Client, credential)
		if err != nil {
			return err
		}
		api = webcompat.Mount(host, auth, runtime.Store, runtime.Support)
	}
	handler := policies.Mount(api)
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
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("bind extension listener: %w", err)
	}
	defer listener.Close()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := host.MarkReady(); err != nil {
		return err
	}
	defer host.BeginDrain()
	result := make(chan error, 1)
	go func() { result <- server.Serve(listener) }()
	select {
	case err := <-result:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
		host.BeginDrain()
		shutdown, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			return errors.Join(err, server.Close())
		}
		if err := host.Wait(shutdown); err != nil {
			return err
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
