// Package app assembles the Go extension process. It has no core database,
// identity authority, wallet, model routing, or schema-upgrade entry point.
package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/composition"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/coreclient"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/legal"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/webcompat"
)

func Run() error {
	path, err := configPath(os.Args[1:])
	if err != nil {
		return err
	}
	var cfg GoConfig
	if path == "" {
		cfg, err = legacyConfig(os.Getenv)
	} else {
		if err = validateEnvironment(os.Getenv); err != nil {
			return err
		}
		cfg, err = LoadConfig(path)
	}
	if err != nil {
		return err
	}
	credential, err := readHostCredential(cfg.Host.TokenFile)
	if err != nil {
		return err
	}
	socket, tokenFile := cfg.Core.Socket, cfg.Core.IdentityTokenFile
	if (socket == "") != (tokenFile == "") {
		return errors.New("configure both core RPC socket and credential file")
	}
	var defaults []string
	if socket != "" {
		defaults = []string{"identity"}
	}
	selected, err := modules.Select(strings.Join(cfg.Host.Modules, ","), defaults, []string{"identity", "store", "support", "assistant"})
	if err != nil {
		return err
	}
	runtime, err := composition.New(context.Background(), composition.Config{
		Selected: selected, Socket: socket, TokenFile: tokenFile,
		CommerceTokenFile: cfg.Core.CommerceTokenFile,
		Store:             composition.StorageConfig{Component: "store", URLFile: cfg.Store.DatabaseURLFile, Database: cfg.Store.DatabaseName},
		Support:           composition.StorageConfig{Component: "support", URLFile: cfg.Support.DatabaseURLFile, Database: cfg.Support.DatabaseName},
		Assistant:         composition.StorageConfig{Component: "assistant", URLFile: cfg.Assistant.DatabaseURLFile, Database: cfg.Assistant.DatabaseName}, AssistantCursorKeyFile: cfg.Assistant.CursorKeyFile,
	})
	if err != nil {
		return err
	}
	defer runtime.Close()
	host, err := modules.NewHost(runtime.Modules, credential)
	if err != nil {
		return err
	}
	policies, err := legal.Load(cfg.Legal.ConfigFile)
	if err != nil {
		return err
	}
	var api http.Handler = host
	if cfg.Host.WebCompat {
		if runtime.Store == nil && runtime.AssistantHistory == nil {
			return errors.New("Web compatibility requires an assembled Web module")
		}
		auth, err := webcompat.NewAuthenticator(runtime.Client, credential)
		if err != nil {
			return err
		}
		api = webcompat.Mount(host, auth, runtime.Store, runtime.Support, webcompat.AdditionalServices{Assistant: runtime.AssistantHistory})
	}
	handler := policies.Mount(api)
	address := cfg.Host.Listen
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
	runtime.StartWorkers(ctx, host)
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
	return coreclient.ReadTokenFile(path)
}
