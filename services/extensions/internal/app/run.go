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
	selected, err := modules.Select(strings.Join(cfg.Host.Modules, ","), defaults, []string{"identity", "store", "support", "assistant", "operations", "remote-control", "payments"})
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
		Selected: selected, Socket: socket, TokenFile: tokenFile, HostCredential: credential,
		CommerceTokenFile: cfg.Core.CommerceTokenFile,
		PaymentTokenFile:  cfg.Core.PaymentTokenFile,
		PaymentConfigFile: cfg.Payments.ConfigFile,
		Payments:          composition.StorageConfig{Component: "payments", URLFile: cfg.Payments.DatabaseURLFile, Database: cfg.Payments.DatabaseName},
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
	host, err := modules.NewHostWithLimit(runtime.Modules, credential, cfg.Host.MaxInFlight)
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
	if cfg.Payments.CallbacksEnabled {
		handler = mountPaymentCallbacks(host, runtime.Payments.WebhookHandler(), handler)
	}
	address := cfg.Host.Listen
	if address == "" {
		address = "0.0.0.0:8081"
	}
	server := newHTTPServer(address, handler)
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
	return serveAndDrain(ctx, server, listener, host, func(work context.Context) {
		runtime.StartWorkers(work, host)
	}, 30*time.Second)
}

// Bound ordinary response writes so a peer that stops reading cannot retain an
// admitted permit indefinitely. Streaming handlers must maintain their own
// per-write deadlines through ResponseController (as remote-control SSE does).
// This is a socket deadline, not cancellation of business work or a rollback.
// @mark extensions.http-write-deadline role=implementation
func newHTTPServer(address string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr: address, Handler: handler,
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  time.Minute, MaxHeaderBytes: 16 << 10,
	}
}

type drainingHost interface {
	MarkReady() error
	BeginDrain()
	Wait(context.Context) error
}

// serveAndDrain must not return until every admitted permit is released: its
// caller closes module storage on return. Shutdown deadlines bound graceful HTTP
// shutdown, not the lifetime of resources still owned by admitted work.
func serveAndDrain(ctx context.Context, server *http.Server, listener net.Listener, host drainingHost, startWorkers func(context.Context), timeout time.Duration) (runErr error) {
	work, cancelWork := context.WithCancel(context.Background())
	defer cancelWork()
	server.BaseContext = func(net.Listener) context.Context { return work }
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := host.MarkReady(); err != nil {
		return err
	}
	defer func() {
		host.BeginDrain()
		cancelWork()
		runErr = errors.Join(runErr, server.Close())
		// A fresh, unbounded join is intentional. Closing connections or timing
		// out does not join handlers; a stuck permit must not trigger pool closure.
		runErr = errors.Join(runErr, host.Wait(context.Background()))
	}()
	startWorkers(work)
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
		shutdown, cancel := context.WithTimeout(context.Background(), timeout)
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
