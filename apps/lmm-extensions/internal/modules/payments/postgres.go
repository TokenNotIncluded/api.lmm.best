package payments

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"reflect"
	"time"
)

// Schema must be installed explicitly into a new dedicated database. New and
// NewPostgres never install tables or contact a provider.
//
//go:embed schema.sql
var Schema string

type Postgres struct{ db *sql.DB }

func NewPostgres(ctx context.Context, db *sql.DB, expectedDatabase string) (*Postgres, error) {
	if db == nil || expectedDatabase == "" {
		return nil, ErrInvalid
	}
	var name, contract string
	var coreSchemas bool
	if err := db.QueryRowContext(ctx, `SELECT current_database(), EXISTS(SELECT 1 FROM pg_namespace WHERE nspname LIKE 'core\_%' ESCAPE '\')`).Scan(&name, &coreSchemas); err != nil {
		return nil, ErrUnavailable
	}
	if name != expectedDatabase || coreSchemas {
		return nil, ErrDenied
	}
	if err := db.QueryRowContext(ctx, `SELECT contract FROM payment_storage_guard WHERE singleton`).Scan(&contract); err != nil || contract != "lmm-payments-v1" {
		return nil, ErrUnavailable
	}
	return &Postgres{db: db}, nil
}
func (p *Postgres) Insert(ctx context.Context, o Order) error {
	o.Version = 1
	b, err := encodeOrder(o)
	if err != nil {
		return err
	}
	_, err = p.db.ExecContext(ctx, `INSERT INTO payment_orders(id,namespace,payment_id,version,snapshot,due_at) VALUES($1,$2,$3,1,$4,$5) ON CONFLICT(id) DO NOTHING`, o.ID, o.Namespace, nullable(o.PaymentID), string(b), nullableTime(o.due()))
	if err != nil {
		return storageError(err)
	}
	return nil
}
func (p *Postgres) Get(ctx context.Context, id string) (Order, error) {
	var b []byte
	err := p.db.QueryRowContext(ctx, `SELECT snapshot FROM payment_orders WHERE id=$1`, id).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		return Order{}, ErrNotFound
	}
	if err != nil {
		return Order{}, ErrUnavailable
	}
	var o Order
	if err = json.Unmarshal(b, &o); err != nil {
		return Order{}, ErrUnavailable
	}
	return o, nil
}
func (p *Postgres) FindPayment(ctx context.Context, namespace, transaction string) (Order, error) {
	var id string
	err := p.db.QueryRowContext(ctx, `SELECT id FROM payment_orders WHERE namespace=$1 AND payment_id=$2`, namespace, transaction).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Order{}, ErrNotFound
	}
	if err != nil {
		return Order{}, ErrUnavailable
	}
	return p.Get(ctx, id)
}
func (p *Postgres) Mutate(ctx context.Context, id string, fn func(*Order) error) error {
	tx, err := p.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return ErrUnavailable
	}
	defer tx.Rollback()
	var b []byte
	err = tx.QueryRowContext(ctx, `SELECT snapshot FROM payment_orders WHERE id=$1 FOR UPDATE`, id).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return ErrUnavailable
	}
	var before, o Order
	if json.Unmarshal(b, &before) != nil || json.Unmarshal(b, &o) != nil {
		return ErrUnavailable
	}
	if err = fn(&o); err != nil {
		return err
	}
	if !sameIdentity(before, o) {
		return ErrConflict
	}
	o.Version = before.Version + 1
	b, err = encodeOrder(o)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE payment_orders SET payment_id=$2,version=$3,snapshot=$4,due_at=$5,updated_at=now() WHERE id=$1`, id, nullable(o.PaymentID), o.Version, string(b), nullableTime(o.due()))
	if err != nil {
		return storageError(err)
	}
	if tx.Commit() != nil {
		return ErrUnavailable
	}
	return nil
}
func sameIdentity(a, b Order) bool {
	return a.ID == b.ID && a.ChannelID == b.ChannelID && a.Namespace == b.Namespace && reflect.DeepEqual(a.Intent, b.Intent) && (a.PaymentID == "" || a.PaymentID == b.PaymentID)
}
func encodeOrder(o Order) ([]byte, error) {
	if !validID(o.ID) || o.Intent.ID <= 0 || !validAmount(o.Intent.AmountMinor) || len(o.Jobs) > 256 || len(o.Refunds) > 128 || o.Jobs == nil || o.Refunds == nil {
		return nil, ErrInvalid
	}
	b, err := json.Marshal(o)
	if err != nil || len(b) > 768<<10 {
		return nil, ErrInvalid
	}
	return b, nil
}
func (p *Postgres) PutEvent(ctx context.Context, e Event) error {
	b, err := json.Marshal(e)
	if err != nil || len(b) > 96<<10 {
		return ErrInvalid
	}
	var digest string
	// A duplicate never moves done/review back to pending. A changed normalized
	// event cannot replace the original stored evidence under the same event ID.
	err = p.db.QueryRowContext(ctx, `INSERT INTO payment_events(event_key,digest,snapshot,status,next_attempt) VALUES($1,$2,$3,'pending',$4) ON CONFLICT(event_key) DO UPDATE SET event_key=EXCLUDED.event_key RETURNING digest`, e.Key, e.Digest, string(b), e.NextAttempt).Scan(&digest)
	if err != nil {
		return ErrUnavailable
	}
	if digest != e.Digest {
		return ErrConflict
	}
	return nil
}
func (p *Postgres) Events(ctx context.Context, now time.Time, limit int) ([]Event, error) {
	if limit < 1 || limit > 100 {
		return nil, ErrInvalid
	}
	rows, err := p.db.QueryContext(ctx, `SELECT snapshot FROM payment_events WHERE status='pending' AND next_attempt<=$1 ORDER BY next_attempt,event_key LIMIT $2`, now, limit)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer rows.Close()
	var result []Event
	for rows.Next() {
		var b []byte
		var e Event
		if rows.Scan(&b) != nil || json.Unmarshal(b, &e) != nil {
			return nil, ErrUnavailable
		}
		result = append(result, e)
	}
	if rows.Err() != nil {
		return nil, ErrUnavailable
	}
	return result, nil
}
func (p *Postgres) FinishEvent(ctx context.Context, key, status, code string, next time.Time) error {
	_, err := p.db.ExecContext(ctx, `UPDATE payment_events SET status=$2,error_code=$3,next_attempt=$4,updated_at=now() WHERE event_key=$1 AND status<>'done'`, key, status, code, next)
	if err != nil {
		return ErrUnavailable
	}
	return nil
}
func (p *Postgres) DueOrders(ctx context.Context, now time.Time, limit int) ([]string, error) {
	if limit < 1 || limit > 100 {
		return nil, ErrInvalid
	}
	rows, err := p.db.QueryContext(ctx, `SELECT id FROM payment_orders WHERE due_at<=$1 ORDER BY due_at,id LIMIT $2`, now, limit)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) != nil {
			return nil, ErrUnavailable
		}
		ids = append(ids, id)
	}
	if rows.Err() != nil {
		return nil, ErrUnavailable
	}
	return ids, nil
}
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

func storageError(err error) error {
	var state interface{ SQLState() string }
	if errors.As(err, &state) && state.SQLState() == "23505" {
		return ErrConflict
	}
	return ErrUnavailable
}
