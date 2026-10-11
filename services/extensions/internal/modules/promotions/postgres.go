package promotions

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"time"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/assistant/access"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/assistant/storage"
)

// Schema is installed explicitly in a new, separate module database.
//
//go:embed schema.sql
var Schema string

type Postgres struct{ db *sql.DB }

// NewPostgres checks an injected pool. It never opens a pool or installs tables.
// The assembly layer must use a role restricted to lmm_promotions only.
func NewPostgres(ctx context.Context, db *sql.DB, expectedDatabase string) (*Postgres, error) {
	if db == nil || expectedDatabase == "" {
		return nil, access.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var database string
	var hasCore bool
	var version int
	if e := db.QueryRowContext(ctx, `SELECT current_database(), EXISTS(SELECT 1 FROM pg_namespace WHERE nspname LIKE 'core\_%' ESCAPE '\')`).Scan(&database, &hasCore); e != nil {
		return nil, access.ErrUnavailable
	}
	if database != expectedDatabase || hasCore {
		return nil, access.ErrForbidden
	}
	if e := db.QueryRowContext(ctx, `SELECT version FROM lmm_promotions.contract WHERE singleton`).Scan(&version); e != nil || version != 1 {
		return nil, access.ErrUnavailable
	}
	return &Postgres{db}, nil
}
func (p *Postgres) Within(ctx context.Context, scope string, fn func(storage.Tx) error) error {
	if len(scope) == 0 || len(scope) > 256 || fn == nil {
		return access.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	tx, e := p.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if e != nil {
		return access.ErrUnavailable
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "lmm_promotions:"+scope); e != nil {
		return access.ErrUnavailable
	}
	if e = fn(&postgresTx{ctx, tx, scope}); e != nil {
		return e
	}
	if tx.Commit() != nil {
		return access.ErrUnavailable
	}
	return nil
}

type postgresTx struct {
	ctx   context.Context
	tx    *sql.Tx
	scope string
}

func bucket(name string) bool {
	switch name {
	case "claims", "applications", "user_totals", "campaign_totals", "events":
		return true
	}
	return false
}
func (t *postgresTx) Get(kind, id string, out any) error {
	if !bucket(kind) {
		return access.ErrInvalid
	}
	var b []byte
	e := t.tx.QueryRowContext(t.ctx, `SELECT body FROM lmm_promotions.records WHERE scope=$1 AND bucket=$2 AND id=$3`, t.scope, kind, id).Scan(&b)
	if errors.Is(e, sql.ErrNoRows) {
		return access.ErrNotFound
	}
	if e != nil || json.Unmarshal(b, out) != nil {
		return access.ErrUnavailable
	}
	return nil
}
func (t *postgresTx) Put(kind, id string, v any) error {
	if !bucket(kind) || len(id) == 0 || len(id) > 256 {
		return access.ErrInvalid
	}
	b, e := json.Marshal(v)
	if e != nil || len(b) > 16384 {
		return access.ErrInvalid
	}
	_, e = t.tx.ExecContext(t.ctx, `INSERT INTO lmm_promotions.records(scope,bucket,id,body) VALUES($1,$2,$3,$4::jsonb) ON CONFLICT(scope,bucket,id) DO UPDATE SET body=EXCLUDED.body`, t.scope, kind, id, string(b))
	if e != nil {
		return access.ErrUnavailable
	}
	return nil
}
func (t *postgresTx) List(kind, after string, limit int) ([]json.RawMessage, error) {
	if !bucket(kind) || limit < 1 || limit > 100 {
		return nil, access.ErrInvalid
	}
	rows, e := t.tx.QueryContext(t.ctx, `SELECT body FROM lmm_promotions.records WHERE scope=$1 AND bucket=$2 AND id>$3 ORDER BY id LIMIT $4`, t.scope, kind, after, limit)
	if e != nil {
		return nil, access.ErrUnavailable
	}
	defer rows.Close()
	out := []json.RawMessage{}
	for rows.Next() {
		var b []byte
		if rows.Scan(&b) != nil {
			return nil, access.ErrUnavailable
		}
		out = append(out, append(json.RawMessage(nil), b...))
	}
	if rows.Err() != nil {
		return nil, access.ErrUnavailable
	}
	return out, nil
}
