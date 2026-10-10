// Package handoff is an opt-in local-disk reference for extension task handoff.
// It is not the payment database, an authority service, or a cross-host store.
// Call Receive only AFTER authenticating and normalizing an external callback.
// No business module is automatically connected to this package.
package handoff

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"syscall"
)

var (
	ErrBusy        = errors.New("handoff store is busy")
	ErrFenced      = errors.New("task owner or task version changed")
	ErrConflict    = errors.New("event key has different content")
	ErrState       = errors.New("task is not in the required state")
	ErrLimit       = errors.New("handoff store limit exceeded")
	ErrUnavailable = errors.New("handoff store unavailable; reopen and reconcile")
	validID        = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.:/-]{0,159}$`)
)

const (
	MaxTasks    = 1024
	MaxPayload  = 64 << 10
	MaxSnapshot = 8 << 20
)

type Owner struct {
	ID    string `json:"id"`
	Epoch uint64 `json:"epoch"`
}
type Task struct {
	Key     string `json:"key"`
	Digest  string `json:"digest"`
	Payload []byte `json:"payload"`
	State   string `json:"state"` // pending, claimed, uncertain, done
	Version uint64 `json:"version"`
	Epoch   uint64 `json:"epoch"`
	Receipt string `json:"receipt,omitempty"`
}
type Snapshot struct {
	Format int             `json:"format"`
	Owner  Owner           `json:"owner"`
	Tasks  map[string]Task `json:"tasks"`
}
type Receipt struct{ Key, Digest, Reference string }
type Store struct {
	mu     sync.Mutex
	dir    string
	lock   *os.File
	broken bool
}

// Init is explicit and refuses existing data. Open does not create or migrate
// task data. A private local directory on one host is required; no NFS support.
func Init(dir string) (*Store, error) { return open(dir, true) }
func Open(dir string) (*Store, error) { return open(dir, false) }
func open(dir string, create bool) (*Store, error) {
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !filepath.IsAbs(dir) || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("private absolute directory required")
	}
	path := filepath.Join(dir, "tasks.lock")
	fd, err := syscall.Open(path, syscall.O_RDWR|syscall.O_CREAT|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	s := &Store{dir: dir, lock: os.NewFile(uintptr(fd), path)}
	li, err := s.lock.Stat()
	if err != nil || !li.Mode().IsRegular() || li.Mode().Perm()&0077 != 0 {
		s.Close()
		return nil, ErrUnavailable
	}
	if create {
		err = s.locked(func() error {
			_, e := os.Lstat(filepath.Join(dir, "tasks.json"))
			if !errors.Is(e, os.ErrNotExist) {
				return errors.New("task data already exists or cannot be inspected")
			}
			return s.save(Snapshot{Format: 1, Tasks: map[string]Task{}})
		})
	} else {
		err = s.locked(func() error { _, e := s.load(); return e })
	}
	if err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lock == nil {
		return nil
	}
	err := s.lock.Close()
	s.lock = nil
	return err
}
func (s *Store) locked(fn func() error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lock == nil || s.broken {
		return ErrUnavailable
	}
	if err := syscall.Flock(int(s.lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return ErrBusy
		}
		return err
	}
	defer syscall.Flock(int(s.lock.Fd()), syscall.LOCK_UN)
	return fn()
}
func (s *Store) load() (Snapshot, error) {
	var d Snapshot
	fd, err := syscall.Open(filepath.Join(s.dir, "tasks.json"), syscall.O_RDONLY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return d, err
	}
	f := os.NewFile(uintptr(fd), "tasks.json")
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > MaxSnapshot {
		return d, ErrUnavailable
	}
	b, err := io.ReadAll(io.LimitReader(f, MaxSnapshot+1))
	if err != nil {
		return d, err
	}
	if len(b) > MaxSnapshot {
		return d, ErrLimit
	}
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&d); err != nil {
		return d, err
	}
	var extra any
	if err = decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return d, ErrUnavailable
	}
	if d.Format != 1 || d.Tasks == nil || len(d.Tasks) > MaxTasks {
		return d, ErrUnavailable
	}
	if (d.Owner.Epoch == 0) != (d.Owner.ID == "") {
		return d, ErrUnavailable
	}
	for key, t := range d.Tasks {
		if key != t.Key || !validID.MatchString(key) || len(t.Payload) > MaxPayload || t.Digest != digest(t.Payload) || t.Version == 0 {
			return d, ErrUnavailable
		}
		switch t.State {
		case "pending", "claimed", "uncertain", "done":
		default:
			return d, ErrUnavailable
		}
		if t.State == "done" && t.Receipt == "" {
			return d, ErrUnavailable
		}
	}
	return d, nil
}
func (s *Store) save(d Snapshot) error {
	b, err := json.Marshal(d)
	if err != nil {
		return err
	}
	if len(b) > MaxSnapshot {
		return ErrLimit
	}
	f, err := os.CreateTemp(s.dir, ".handoff-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	n, err := f.Write(b)
	if err == nil && n != len(b) {
		err = io.ErrShortWrite
	}
	if err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return err
	}
	if err = os.Rename(name, filepath.Join(s.dir, "tasks.json")); err != nil {
		return err
	}
	dir, err := os.Open(s.dir)
	if err == nil {
		err = errors.Join(dir.Sync(), dir.Close())
	}
	if err != nil {
		s.broken = true
	} // rename happened: do not guess commit outcome.
	return err
}
func (s *Store) change(fn func(*Snapshot) error) error {
	return s.locked(func() error {
		d, e := s.load()
		if e != nil {
			return e
		}
		if e = fn(&d); e != nil {
			return e
		}
		return s.save(d)
	})
}
func digest(b []byte) string { v := sha256.Sum256(b); return hex.EncodeToString(v[:]) }
func next(v uint64) (uint64, error) {
	if v == math.MaxUint64 {
		return 0, ErrLimit
	}
	return v + 1, nil
}
func owned(d *Snapshot, o Owner) error {
	if o.Epoch == 0 || d.Owner != o {
		return ErrFenced
	}
	return nil
}

// Receive returns only after file fsync + rename + directory fsync. Repeat
// delivery with the same key and content is accepted, including after done.
// A changed payload under the same key cannot replace the stored event.
func (s *Store) Receive(key string, payload []byte) (Task, error) {
	var out Task
	if !validID.MatchString(key) || len(payload) == 0 || len(payload) > MaxPayload {
		return out, ErrLimit
	}
	err := s.change(func(d *Snapshot) error {
		if t, ok := d.Tasks[key]; ok {
			if t.Digest != digest(payload) {
				return ErrConflict
			}
			out = t
			return nil
		}
		if len(d.Tasks) >= MaxTasks {
			return ErrLimit
		}
		out = Task{Key: key, Digest: digest(payload), Payload: append([]byte(nil), payload...), State: "pending", Version: 1}
		d.Tasks[key] = out
		return nil
	})
	return out, err
}

// Takeover is a LOCAL trusted supervisor action, not a public API. The epoch
// advances durably. A claimed task can be retried because Start has not run.
// Any task whose external operation might have started remains uncertain.
// Never call an external operation until Start succeeds with a current ticket.
func (s *Store) Takeover(id string) (Owner, error) {
	var o Owner
	if !validID.MatchString(id) {
		return o, ErrState
	}
	err := s.change(func(d *Snapshot) error {
		epoch, e := next(d.Owner.Epoch)
		if e != nil {
			return e
		}
		o = Owner{id, epoch}
		d.Owner = o
		for k, t := range d.Tasks {
			if t.State == "claimed" || t.State == "uncertain" {
				if t.State == "claimed" {
					t.State = "pending"
				}
				t.Epoch = epoch
				t.Version, e = next(t.Version)
				if e != nil {
					return e
				}
				d.Tasks[k] = t
			}
		}
		return nil
	})
	return o, err
}
func (s *Store) Claim(o Owner, key string) (Task, error) {
	var out Task
	err := s.change(func(d *Snapshot) error {
		if e := owned(d, o); e != nil {
			return e
		}
		t, ok := d.Tasks[key]
		if !ok || t.State != "pending" {
			return ErrState
		}
		v, e := next(t.Version)
		if e != nil {
			return e
		}
		t.Version = v
		t.Epoch = o.Epoch
		t.State = "claimed"
		d.Tasks[key] = t
		out = t
		return nil
	})
	return out, err
}

// Start durably records uncertainty BEFORE the external call. A crash in the
// following gap can require manual reconciliation even when no effect ran.
func (s *Store) Start(o Owner, t Task) (Task, error) {
	return s.transition(o, t, "claimed", "uncertain", Receipt{})
}

// Complete is for a verified result or verified provider lookup. It does NOT
// verify provider signatures; the module adapter must do that. There is no
// automatic uncertain -> pending transition and no generic retry-on-error.
func (s *Store) Complete(o Owner, t Task, r Receipt) (Task, error) {
	return s.transition(o, t, "uncertain", "done", r)
}
func (s *Store) transition(o Owner, t Task, from, to string, r Receipt) (Task, error) {
	var out Task
	err := s.change(func(d *Snapshot) error {
		if e := owned(d, o); e != nil {
			return e
		}
		current, ok := d.Tasks[t.Key]
		if !ok || current.Epoch != o.Epoch || current.Version != t.Version {
			return ErrFenced
		}
		if current.State != from {
			return ErrState
		}
		if to == "done" {
			if r.Key != current.Key || r.Digest != current.Digest || r.Reference == "" || len(r.Reference) > 1024 {
				return fmt.Errorf("receipt mismatch: %w", ErrConflict)
			}
			current.Receipt = r.Reference
		}
		v, e := next(current.Version)
		if e != nil {
			return e
		}
		current.Version = v
		current.State = to
		d.Tasks[t.Key] = current
		out = current
		return nil
	})
	return out, err
}
func (s *Store) Snapshot() (Snapshot, error) {
	var d Snapshot
	err := s.locked(func() error { var e error; d, e = s.load(); return e })
	return d, err
}
