// Package storage defines module-private transactions, not a shared database.
package storage

import (
	"context"
	"encoding/json"
)

// Within serializes a bounded transaction for one module-owned scope across
// processes. Callbacks never do network I/O or retain a transaction or buffers.
// A repository is injected at assembly; production has no memory fallback.
type Repository interface {
	Within(context.Context, string, func(Tx) error) error
}
type Tx interface {
	Get(bucket, id string, out any) error
	Put(bucket, id string, value any) error
	List(bucket, after string, limit int) ([]json.RawMessage, error)
}
