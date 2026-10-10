package coreclient

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"

	pb "github.com/TokenNotIncluded/api.lmm.best/extensions/internal/corepb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// SQLInboxSchema is installed by the extension's explicit fresh-db initializer.
// This adapter takes a caller-owned EXTENSION database, never opens a connection
// and never receives core's DSN, SQL credentials or tables.
//
//go:embed inbox.sql
var SQLInboxSchema string

type SQLInbox struct {
	db       *sql.DB
	consumer string
}
type TransactionalEventHandler func(context.Context, *sql.Tx, *pb.EventEnvelope) error

func NewSQLInbox(db *sql.DB, consumer string) (*SQLInbox, error) {
	if db == nil || !eventName(consumer, 64) {
		return nil, errors.New("extension database and valid consumer are required")
	}
	return &SQLInbox{db: db, consumer: consumer}, nil
}

// Apply returns true for an already committed event. A unique key serializes
// concurrent deliveries. A handler failure or process crash rolls back both
// the receipt and its local effects. The caller may ACK only after this returns.
func (i *SQLInbox) Apply(ctx context.Context, e *pb.EventEnvelope, handle TransactionalEventHandler) (bool, error) {
	if i == nil || i.db == nil || handle == nil {
		return false, errors.New("transactional event handler is required")
	}
	digest, err := EventFingerprint(e)
	if err != nil {
		return false, err
	}
	if !equalDigest(digest[:], e.ContentSha256) {
		return false, status.Error(codes.DataLoss, "event fingerprint mismatch")
	}
	tx, err := i.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, "INSERT INTO extension_events.inbox(consumer_id,event_id,content_sha256) VALUES($1,$2,$3) ON CONFLICT DO NOTHING", i.consumer, e.EventId, digest[:])
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if n == 0 {
		var old []byte
		if err = tx.QueryRowContext(ctx, "SELECT content_sha256 FROM extension_events.inbox WHERE consumer_id=$1 AND event_id=$2", i.consumer, e.EventId).Scan(&old); err != nil {
			return false, err
		}
		if !equalDigest(old, digest[:]) {
			return false, status.Error(codes.AlreadyExists, "event ID was reused with different content")
		}
	} else if err = handle(ctx, tx, e); err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return n == 0, nil
}
