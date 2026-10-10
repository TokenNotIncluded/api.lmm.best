// Package testkit contains isolated test fixtures. Runtime modules do not import it.
package testkit

import (
	"context"
	"encoding/json"
	"sort"
	"sync"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/assistant/access"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/assistant/storage"
)

type Store struct {
	mu   sync.Mutex
	Data map[string]map[string]json.RawMessage
	Fail bool
}

func NewStore() *Store { return &Store{Data: map[string]map[string]json.RawMessage{}} }
func (m *Store) Within(ctx context.Context, scope string, fn func(storage.Tx) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if m.Fail {
		return access.ErrUnavailable
	}
	var copy map[string]map[string]json.RawMessage
	b, _ := json.Marshal(m.Data)
	_ = json.Unmarshal(b, &copy)
	if copy[scope] == nil {
		copy[scope] = map[string]json.RawMessage{}
	}
	if err := fn(tx{copy[scope]}); err != nil {
		return err
	}
	m.Data = copy
	return nil
}

type tx struct{ data map[string]json.RawMessage }

func (t tx) Get(bucket, id string, out any) error {
	b, ok := t.data[bucket+"/"+id]
	if !ok {
		return access.ErrNotFound
	}
	return json.Unmarshal(b, out)
}
func (t tx) Put(bucket, id string, v any) error {
	b, e := json.Marshal(v)
	if e == nil {
		t.data[bucket+"/"+id] = b
	}
	return e
}
func (t tx) List(bucket, after string, limit int) ([]json.RawMessage, error) {
	var keys []string
	for k := range t.data {
		if len(k) > len(bucket)+1 && k[:len(bucket)+1] == bucket+"/" && k[len(bucket)+1:] > after {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	out := []json.RawMessage{}
	for _, k := range keys {
		if len(out) == limit {
			break
		}
		out = append(out, append(json.RawMessage(nil), t.data[k]...))
	}
	return out, nil
}
