package toolmarket

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

// Store is an encrypted, transactionally replaced module-private snapshot.
// One writer process owns the file lock. There is no database DSN or core SQL.
// The directory and 32-byte encryption key must be owned by this module only.
type Store struct {
	mu     sync.Mutex
	data   database
	path   string
	aead   cipher.AEAD
	lock   *os.File
	closed bool
	broken bool
}

// InitStore is an explicit fresh-install operation. It refuses an existing file.
func InitStore(path string, key []byte) (*Store, error) { return fileStore(path, key, true) }

// OpenStore never creates, migrates, repairs or clears an existing database.
func OpenStore(path string, key []byte) (*Store, error) { return fileStore(path, key, false) }
func fileStore(path string, key []byte, create bool) (s *Store, err error) {
	if !filepath.IsAbs(path) || len(key) != 32 {
		return nil, ErrInvalid
	}
	dir, e := os.Lstat(filepath.Dir(path))
	if e != nil || !dir.IsDir() || dir.Mode().Perm()&0077 != 0 {
		return nil, invalid("private directory required")
	}
	block, e := aes.NewCipher(key)
	if e != nil {
		return nil, e
	}
	aead, e := cipher.NewGCM(block)
	if e != nil {
		return nil, e
	}
	fd, e := syscall.Open(path+".lock", syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
	if e != nil {
		return nil, ErrConflict
	}
	lock := os.NewFile(uintptr(fd), path+".lock")
	if e = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		lock.Close()
		return nil, ErrConflict
	}
	s = &Store{data: emptyDB(), path: path, aead: aead, lock: lock}
	defer func() {
		if err != nil {
			s.Close()
		}
	}()
	if create {
		f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return s, ErrConflict
		}
		if e = f.Close(); e != nil {
			return s, e
		}
		if e = s.persist(s.data); e != nil {
			return s, e
		}
		return s, nil
	}
	info, e := os.Lstat(path)
	if e != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 64<<20 {
		return s, ErrInvalid
	}
	b, e := os.ReadFile(path)
	if e != nil {
		return s, e
	}
	if len(b) < aead.NonceSize() {
		return s, ErrInvalid
	}
	plain, e := aead.Open(nil, b[:aead.NonceSize()], b[aead.NonceSize():], []byte("lmm-toolmarket-v1"))
	if e != nil {
		return s, invalid("invalid store or encryption key")
	}
	if e = json.Unmarshal(plain, &s.data); e != nil || s.data.Version != 1 || s.data.Servers == nil || s.data.Releases == nil || s.data.Installs == nil || s.data.Secrets == nil || s.data.States == nil || s.data.Executions == nil {
		return s, ErrInvalid
	}
	return s, nil
}
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	if s.lock != nil {
		e := s.lock.Close()
		s.lock = nil
		return e
	}
	return nil
}
func (s *Store) persist(d database) error {
	if s.path == "" {
		return nil
	} // Only unexported in-memory stores used by tests.
	plain, e := json.Marshal(d)
	if e != nil {
		return e
	}
	if len(plain) > 63<<20 {
		return ErrLimit
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, e = io.ReadFull(rand.Reader, nonce); e != nil {
		return e
	}
	b := s.aead.Seal(nonce, nonce, plain, []byte("lmm-toolmarket-v1"))
	f, e := os.CreateTemp(filepath.Dir(s.path), ".toolmarket-*")
	if e != nil {
		return e
	}
	name := f.Name()
	defer os.Remove(name)
	if _, e = f.Write(b); e == nil {
		e = f.Sync()
	}
	e = errors.Join(e, f.Close())
	if e != nil {
		return e
	}
	if e = os.Rename(name, s.path); e != nil {
		return e
	}
	dir, e := os.Open(filepath.Dir(s.path))
	if e == nil {
		e = errors.Join(dir.Sync(), dir.Close())
	}
	// After rename a failed directory sync has an uncertain durability outcome.
	// Refuse all later operations until the process reopens the database.
	if e != nil {
		s.broken = true
	}
	return e
}
func (s *Store) view(fn func(database) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.broken {
		return ErrUnavailable
	}
	// A deep copy prevents accidental mutation and races through returned maps.
	b, e := json.Marshal(s.data)
	if e != nil {
		return e
	}
	var d database
	if e = json.Unmarshal(b, &d); e != nil {
		return e
	}
	return fn(d)
}
func (s *Store) update(fn func(*database) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.broken {
		return ErrUnavailable
	}
	b, e := json.Marshal(s.data)
	if e != nil {
		return e
	}
	var next database
	if e = json.Unmarshal(b, &next); e != nil {
		return e
	}
	if e = fn(&next); e != nil {
		return e
	}
	// Break aliases captured or returned by the transaction callback.
	b, e = json.Marshal(next)
	if e != nil {
		return e
	}
	var committed database
	if e = json.Unmarshal(b, &committed); e != nil {
		return e
	}
	if e = s.persist(committed); e != nil {
		return e
	}
	s.data = committed
	return nil
}
