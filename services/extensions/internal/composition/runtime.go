package composition

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/coreclient"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/assistant"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/assistant/access"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/identity"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/operations"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/remotecontrol"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/store"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/store/rustauth"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/store/rustfunds"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/support"
)

type Config struct {
	Selected               []string
	Socket                 string
	TokenFile              string
	CommerceTokenFile      string
	Store                  StorageConfig
	Support                StorageConfig
	Assistant              StorageConfig
	AssistantCursorKeyFile string
	Operations             StorageConfig
	Scripts                *operations.ScriptConfig
}

type Runtime struct {
	Modules          []modules.Module
	Client           *coreclient.Client
	CommerceClient   *coreclient.Client
	Store            *store.Service
	Support          *support.Service
	Assistant        *assistant.Service
	AssistantHistory *assistant.HistoryService
	Operations       *operations.Service
	close            []func() error
	workerCancel     context.CancelFunc
	workerDone       chan struct{}
	workerOnce       sync.Once
}

func (r *Runtime) Close() error {
	if r.workerCancel != nil {
		r.workerCancel()
		<-r.workerDone
		r.workerCancel = nil
	}
	var err error
	for i := len(r.close) - 1; i >= 0; i-- {
		err = errors.Join(err, r.close[i]())
	}
	r.close = nil
	return err
}

// StartWorkers is explicit and runs after listener readiness. One bounded
// recovery batch shares host admission and is joined before closing resources.
func (r *Runtime) StartWorkers(parent context.Context, host *modules.Host) {
	r.workerOnce.Do(func() {
		ctx, cancel := context.WithCancel(parent)
		r.workerCancel = cancel
		r.workerDone = make(chan struct{})
		go func() {
			defer close(r.workerDone)
			ticker := time.NewTicker(5 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
				if r.Store == nil {
					continue
				}
				release, ok := host.Acquire()
				if !ok {
					return
				}
				bounded, stop := context.WithTimeout(ctx, 20*time.Second)
				_ = r.Store.RecoverDeliveries(bounded)
				stop()
				release()
			}
		}()
	})
}

func New(ctx context.Context, cfg Config) (_ *Runtime, resultErr error) {
	r := &Runtime{}
	defer func() {
		if resultErr != nil {
			_ = r.Close()
		}
	}()
	if len(cfg.Selected) == 0 {
		if cfg.Operations.URLFile != "" || cfg.Operations.Database != "" || cfg.Scripts != nil {
			return nil, errors.New("operations configuration requires explicit module selection")
		}
		return r, nil
	}
	if cfg.Socket == "" || cfg.TokenFile == "" {
		return nil, errors.New("selected modules require core RPC configuration")
	}
	token, err := coreclient.ReadTokenFile(cfg.TokenFile)
	if err != nil {
		return nil, err
	}
	r.Client, err = coreclient.New(cfg.Socket, token)
	if err != nil {
		return nil, err
	}
	r.close = append(r.close, r.Client.Close)
	selected := map[string]bool{}
	for _, name := range cfg.Selected {
		selected[name] = true
	}
	if selected["support"] && !selected["store"] {
		return nil, errors.New("support requires the store module")
	}
	if cfg.CommerceTokenFile != "" && !selected["store"] {
		return nil, errors.New("commerce RPC configuration requires store")
	}
	if selected["store"] {
		db, err := openStorage(ctx, cfg.Store)
		if err != nil {
			return nil, err
		}
		r.close = append(r.close, db.Close)
		repo, err := store.NewPostgres(db)
		if err != nil {
			return nil, err
		}
		auth, err := rustauth.New(r.Client)
		if err != nil {
			return nil, err
		}
		// A real commerce funds adapter is deliberately required before money
		// operations can become usable. Identity does not grant spending.
		var funds store.Funds = store.DisabledFunds{}
		if cfg.CommerceTokenFile != "" {
			commerceToken, err := coreclient.ReadTokenFile(cfg.CommerceTokenFile)
			if err != nil {
				return nil, err
			}
			if string(commerceToken) == string(token) {
				return nil, errors.New("commerce and identity RPC credentials must be distinct")
			}
			r.CommerceClient, err = coreclient.New(cfg.Socket, commerceToken)
			if err != nil {
				return nil, err
			}
			r.close = append(r.close, r.CommerceClient.Close)
			caps, err := r.Client.Capabilities(ctx)
			if err != nil || caps == nil {
				return nil, errors.New("commerce core capabilities unavailable")
			}
			funds, err = rustfunds.New(r.CommerceClient, caps.GetFeatures())
			if err != nil {
				return nil, errors.New("commerce core capabilities unavailable")
			}
		}
		r.Store, err = store.New(repo, auth, funds)
		if err != nil {
			return nil, err
		}
		if selected["support"] {
			db, err := openStorage(ctx, cfg.Support)
			if err != nil {
				return nil, err
			}
			r.close = append(r.close, db.Close)
			repo, err := support.NewPostgres(db)
			if err != nil {
				return nil, err
			}
			r.Support, err = support.New(repo, auth, r.Store)
			if err != nil {
				return nil, err
			}
		}
	}
	if selected["assistant"] {
		db, err := openStorage(ctx, cfg.Assistant)
		if err != nil {
			return nil, err
		}
		r.close = append(r.close, db.Close)
		repo, err := assistant.NewPostgres(ctx, db, cfg.Assistant.Database)
		if err != nil {
			return nil, err
		}
		auth, err := access.New(r.Client)
		if err != nil {
			return nil, err
		}
		key, err := coreclient.ReadTokenFile(cfg.AssistantCursorKeyFile)
		if err != nil {
			return nil, errors.New("assistant private cursor key unavailable")
		}
		r.AssistantHistory, err = assistant.NewHistory(repo, auth, key)
		if err != nil {
			return nil, err
		}
		r.Assistant, err = assistant.New(repo, auth, nil, assistant.Options{})
		if err != nil {
			return nil, err
		}
	}
	if selected["operations"] {
		config := operations.Config{Scripts: cfg.Scripts}
		if cfg.Operations.URLFile != "" || cfg.Operations.Database != "" {
			dsn, err := storageURL(cfg.Operations)
			if err != nil {
				return nil, err
			}
			config.Releases = &operations.DatabaseConfig{DSN: dsn}
		}
		if config.Releases == nil && config.Scripts == nil {
			return nil, errors.New("operations requires configured releases or reviewed scripts")
		}
		r.Operations, err = operations.New(ctx, config)
		if err != nil {
			return nil, err
		}
		r.close = append(r.close, r.Operations.Close)
	} else if cfg.Operations.URLFile != "" || cfg.Operations.Database != "" || cfg.Scripts != nil {
		return nil, errors.New("operations configuration requires explicit module selection")
	}
	for _, name := range cfg.Selected {
		switch name {
		case "identity":
			r.Modules = append(r.Modules, identity.New(r.Client))
		case "remote-control":
			remote, err := remotecontrol.New(r.Client)
			if err != nil {
				return nil, err
			}
			r.Modules = append(r.Modules, remote)
		case "store":
			r.Modules = append(r.Modules, r.Store)
		case "support":
			r.Modules = append(r.Modules, r.Support)
		case "assistant":
			r.Modules = append(r.Modules, r.Assistant)
		case "operations":
			// The process entry mounts its authenticated adapter with the
			// separately loaded host credential before constructing the host.
		default:
			return nil, errors.New("unsupported composed extension module")
		}
	}
	return r, nil
}
