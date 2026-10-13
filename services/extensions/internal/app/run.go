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
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/operations"
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
	selected, err := modules.Select(strings.Join(cfg.Host.Modules, ","), defaults, []string{"identity", "store", "support", "assistant", "operations"})
	if err != nil {
		return err
	}
	var scripts *operations.ScriptConfig
	if cfg.Scripts.Root != "" || cfg.Scripts.Manifest != nil {
		manifest := make([]operations.ReviewedScript, len(cfg.Scripts.Manifest))
		for i, entry := range cfg.Scripts.Manifest {
			manifest[i] = operations.ReviewedScript{Name: entry.Name, SHA256: entry.SHA256}
		}
		scripts = &operations.ScriptConfig{Root: cfg.Scripts.Root, Manifest: manifest}
	}
	runtime, err := composition.New(context.Background(), composition.Config{
		Selected: selected, Socket: socket, TokenFile: tokenFile,
		CommerceTokenFile: cfg.Core.CommerceTokenFile,
		Store:             composition.StorageConfig{Component: "store", URLFile: cfg.Store.DatabaseURLFile, Database: cfg.Store.DatabaseName},
		Support:           composition.StorageConfig{Component: "support", URLFile: cfg.Support.DatabaseURLFile, Database: cfg.Support.DatabaseName},
		Assistant:         composition.StorageConfig{Component: "assistant", URLFile: cfg.Assistant.DatabaseURLFile, Database: cfg.Assistant.DatabaseName}, AssistantCursorKeyFile: cfg.Assistant.CursorKeyFile,
		Operations: composition.StorageConfig{Component: "operations", URLFile: cfg.Operations.DatabaseURLFile, Database: cfg.Operations.DatabaseName}, Scripts: scripts,
	})
	if err != nil {
		return err
	}
	defer runtime.Close()
	var auth *webcompat.Authenticator
	if cfg.Host.WebCompat || runtime.Operations != nil {
		auth, err = webcompat.NewAuthenticator(runtime.Client, credential)
		if err != nil {
			return err
		}
	}
	if runtime.Operations != nil {
		runtime.Modules = append(runtime.Modules, webcompat.NewOperationsModule(auth, runtime.Operations))
	}
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
		if runtime.Store == nil && runtime.AssistantHistory == nil && runtime.Operations == nil {
			return errors.New("Web compatibility requires an assembled Web module")
		}
		api = webcompat.Mount(host, auth, runtime.Store, runtime.Support, webcompat.AdditionalServices{Assistant: runtime.AssistantHistory, Operations: runtime.Operations})
	}
	handler := listenerHandler(policies, api)
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

// listenerHandler guards the actual listener entry before either the legal or
// host mux can canonicalize a script path, even without Web compatibility.
func listenerHandler(policies *legal.Handler, api http.Handler) http.Handler {
	return webcompat.OperationsGuard(policies.Mount(api))
}

func readHostCredential(path string) ([]byte, error) {
	return coreclient.ReadTokenFile(path)
}
