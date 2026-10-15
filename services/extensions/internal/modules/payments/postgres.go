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

// Bound pool/lock/query waits independently of HTTP socket write deadlines.
// Each transaction shares one budget, including commit. Expiry remains an
// unavailable/possibly unknown storage outcome, never permission to retry a
// provider call or release money. Earlier caller deadlines are preserved.
const storageTimeout = 3 * time.Second

type Postgres struct{ db *sql.DB }

func NewPostgres(ctx context.Context, db *sql.DB, expectedDatabase string) (*Postgres, error) {
	ctx, cancel := context.WithTimeout(ctx, storageTimeout)
	defer cancel()
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
	ctx, cancel := context.WithTimeout(ctx, storageTimeout)
	defer cancel()
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

// @mark payments.storage-deadline role=implementation
func (p *Postgres) Get(ctx context.Context, id string) (Order, error) {
	ctx, cancel := context.WithTimeout(ctx, storageTimeout)
	defer cancel()
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
	ctx, cancel := context.WithTimeout(ctx, storageTimeout)
	defer cancel()
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

// @mark payments.storage-deadline role=transaction
func (p *Postgres) Mutate(ctx context.Context, id string, fn func(*Order) error) error {
	ctx, cancel := context.WithTimeout(ctx, storageTimeout)
	defer cancel()
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

// mergeEvent preserves signed bytes. Inbox done means only that local work was
// queued, so a missing proof may still be promoted and reapplied to repair a
// quarantined job. Final money jobs and receipts remain immutable in apply.
func mergeEvent(old, next Event) (Event, bool, error) {
	if old.Digest != next.Digest || eventHash(old.Evidence.Payload) != eventHash(next.Evidence.Payload) || !samePayment(old.Evidence, next.Evidence) {
		return Event{}, false, ErrConflict
	}
	if old.Evidence.AcceptedProofID != "" && next.Evidence.AcceptedProofID != "" && old.Evidence.AcceptedProofID != next.Evidence.AcceptedProofID {
		return Event{}, false, ErrConflict
	}
	if !strongerProof(old.Evidence, next.Evidence) {
		return old, false, nil
	}
	old.Evidence = next.Evidence
	old.Status, old.ErrorCode, old.NextAttempt = "pending", "", next.NextAttempt
	return old, true, nil
}
func (p *Postgres) PutEvent(ctx context.Context, e Event) error {
	ctx, cancel := context.WithTimeout(ctx, storageTimeout)
	defer cancel()
	b, err := json.Marshal(e)
	if err != nil || len(b) > 96<<10 {
		return ErrInvalid
	}
	tx, err := p.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return ErrUnavailable
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `INSERT INTO payment_events(event_key,digest,snapshot,status,next_attempt) VALUES($1,$2,$3,'pending',$4) ON CONFLICT(event_key) DO NOTHING`, e.Key, e.Digest, string(b), e.NextAttempt)
	if err != nil {
		return ErrUnavailable
	}
	count, err := result.RowsAffected()
	if err != nil {
		return ErrUnavailable
	}
	if count == 0 {
		var old Event
		var raw []byte
		if err = tx.QueryRowContext(ctx, `SELECT snapshot,status,error_code,next_attempt FROM payment_events WHERE event_key=$1 FOR UPDATE`, e.Key).Scan(&raw, &old.Status, &old.ErrorCode, &old.NextAttempt); err != nil {
			return ErrUnavailable
		}
		status, code, next := old.Status, old.ErrorCode, old.NextAttempt
		if json.Unmarshal(raw, &old) != nil {
			return ErrUnavailable
		}
		old.Status, old.ErrorCode, old.NextAttempt = status, code, next
		merged, changed, mergeErr := mergeEvent(old, e)
		if mergeErr != nil {
			return mergeErr
		}
		if changed {
			raw, err = json.Marshal(merged)
			if err != nil || len(raw) > 96<<10 {
				return ErrInvalid
			}
			if _, err = tx.ExecContext(ctx, `UPDATE payment_events SET snapshot=$2,status=$3,error_code=$4,next_attempt=$5,updated_at=now() WHERE event_key=$1`, e.Key, string(raw), merged.Status, merged.ErrorCode, merged.NextAttempt); err != nil {
				return ErrUnavailable
			}
		}
	}
	if tx.Commit() != nil {
		return ErrUnavailable
	}
	return nil
}
func (p *Postgres) Events(ctx context.Context, now time.Time, limit int) ([]Event, error) {
	ctx, cancel := context.WithTimeout(ctx, storageTimeout)
	defer cancel()
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
	ctx, cancel := context.WithTimeout(ctx, storageTimeout)
	defer cancel()
	_, err := p.db.ExecContext(ctx, `UPDATE payment_events SET status=$2,error_code=$3,next_attempt=$4,updated_at=now() WHERE event_key=$1 AND status<>'done' AND NOT ($2='review' AND $3='independent_provider_evidence_required' AND COALESCE(snapshot->'evidence'->>'accepted_proof_id','')<>'')`, key, status, code, next)
	if err != nil {
		return ErrUnavailable
	}
	return nil
}
func (p *Postgres) DueOrders(ctx context.Context, now time.Time, limit int) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, storageTimeout)
	defer cancel()
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
