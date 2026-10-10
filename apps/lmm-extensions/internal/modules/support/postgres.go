package support

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/store"
	"time"
)

// Schema is a fresh-install schema, not a legacy migration. Installation is an
// explicit integration/deployment step; constructing the module runs no DDL.
//
//go:embed schema.sql
var Schema string

type Postgres struct{ db *sql.DB }

// NewPostgres takes a dedicated extension database pool opened by composition.
// Its database role must have rights ONLY on lmm_support, never Rust core tables.
func NewPostgres(db *sql.DB) (*Postgres, error) {
	if db == nil {
		return nil, store.ErrInvalid
	}
	return &Postgres{db}, nil
}
func (p *Postgres) Within(ctx context.Context, shopID string, fn func(Tx) error) error {
	if !store.Text(shopID, 128) || fn == nil {
		return store.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	tx, err := p.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Transaction-scoped database lock, not a Go mutex. Different shops proceed
	// independently; a hash collision only reduces concurrency, never correctness.
	if _, err = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", "lmm_support:"+shopID); err != nil {
		return err
	}
	if err = fn(&postgresTx{ctx, tx, shopID}); err != nil {
		return err
	}
	return tx.Commit()
}

type postgresTx struct {
	ctx  context.Context
	tx   *sql.Tx
	shop string
}

func supportTable(name string) (string, error) {
	switch name {
	case "customers", "conversations", "messages", "commands":
		return "lmm_support." + name, nil
	}
	return "", store.ErrInvalid
}
func (t *postgresTx) Get(kind, id string, out any) error {
	table, err := supportTable(kind)
	if err != nil {
		return err
	}
	var data []byte
	err = t.tx.QueryRowContext(t.ctx, "SELECT body FROM "+table+" WHERE shop_id=$1 AND id=$2", t.shop, id).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return store.ErrNotFound
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}
func (t *postgresTx) Put(kind, id string, value any) error {
	table, err := supportTable(kind)
	if err != nil {
		return err
	}
	if !store.Text(id, 128) {
		return store.ErrInvalid
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = t.tx.ExecContext(t.ctx, "INSERT INTO "+table+" (shop_id,id,body) VALUES ($1,$2,$3::jsonb) ON CONFLICT (shop_id,id) DO UPDATE SET body=EXCLUDED.body", t.shop, id, string(data))
	return err
}
func (t *postgresTx) List(kind, prefix, after string, limit int) ([]json.RawMessage, error) {
	table, err := supportTable(kind)
	if err != nil {
		return nil, err
	}
	if !store.Page(limit) {
		return nil, store.ErrInvalid
	}
	rows, err := t.tx.QueryContext(t.ctx, "SELECT body FROM "+table+" WHERE shop_id=$1 AND id>$2 AND left(id,length($3))=$3 ORDER BY id LIMIT $4", t.shop, after, prefix, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []json.RawMessage{}
	for rows.Next() {
		var data []byte
		if err = rows.Scan(&data); err != nil {
			return nil, err
		}
		out = append(out, json.RawMessage(append([]byte(nil), data...)))
	}
	return out, rows.Err()
}
