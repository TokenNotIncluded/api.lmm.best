package composition

import (
	"context"
	"errors"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/coreclient"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/identity"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/store"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/store/rustauth"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/support"
)

type Config struct {
	Selected  []string
	Socket    string
	TokenFile string
	Store     StorageConfig
	Support   StorageConfig
}

type Runtime struct {
	Modules []modules.Module
	Client  *coreclient.Client
	Store   *store.Service
	Support *support.Service
	close   []func() error
}

func (r *Runtime) Close() error {
	var err error
	for i := len(r.close) - 1; i >= 0; i-- {
		err = errors.Join(err, r.close[i]())
	}
	r.close = nil
	return err
}

func New(ctx context.Context, cfg Config) (_ *Runtime, resultErr error) {
	r := &Runtime{}
	defer func() {
		if resultErr != nil {
			_ = r.Close()
		}
	}()
	if len(cfg.Selected) == 0 {
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
		r.Store, err = store.New(repo, auth, store.DisabledFunds{})
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
	for _, name := range cfg.Selected {
		switch name {
		case "identity":
			r.Modules = append(r.Modules, identity.New(r.Client))
		case "store":
			r.Modules = append(r.Modules, r.Store)
		case "support":
			r.Modules = append(r.Modules, r.Support)
		default:
			return nil, errors.New("unsupported composed extension module")
		}
	}
	return r, nil
}
