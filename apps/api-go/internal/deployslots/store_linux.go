//go:build linux

package deployslots

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

// ErrDurability means rename succeeded but directory fsync failed. The new
// state may be visible: reload and reconcile native evidence, never blindly
// repeat activation or claim that the previous state was restored.
var ErrDurability = errors.New("slot state replaced but durability is uncertain")

const maxStateBytes = 64 << 10

// FileStore persists planning state only. It never switches services or proves
// a receipt. All callers must also hold the native production deployment lock.
// The directory must already exist, be private, and belong to the current UID.
// Do not expose Update callbacks or receipt construction through an HTTP API.
type FileStore struct {
	directory    string
	beforeRename func() error
	syncDir      func(*os.File) error
}

func OpenFileStore(directory string) (*FileStore, error) {
	store := &FileStore{directory: directory, syncDir: (*os.File).Sync}
	dir, err := store.openDirectory()
	if err != nil {
		return nil, err
	}
	return store, dir.Close()
}

func (store *FileStore) openDirectory() (*os.File, error) {
	if !filepath.IsAbs(store.directory) || filepath.Clean(store.directory) != store.directory || store.directory == "/" {
		return nil, errors.New("slot directory must be an absolute canonical path")
	}
	flags := syscall.O_RDONLY | syscall.O_DIRECTORY | syscall.O_NOFOLLOW | syscall.O_CLOEXEC
	fd, err := syscall.Open("/", flags, 0)
	if err != nil {
		return nil, err
	}
	// Walk by directory descriptors: reject symlinked ancestors as well as a
	// symlinked final directory, without resolving names outside this walk.
	for _, part := range strings.Split(strings.TrimPrefix(store.directory, "/"), "/") {
		next, openErr := syscall.Openat(fd, part, flags, 0)
		_ = syscall.Close(fd)
		if openErr != nil {
			return nil, openErr
		}
		fd = next
	}
	dir := os.NewFile(uintptr(fd), store.directory)
	info, err := dir.Stat()
	if err == nil {
		owner, ok := info.Sys().(*syscall.Stat_t)
		if !ok || owner.Uid != uint32(os.Geteuid()) || info.Mode().Perm() != 0700 {
			err = errors.New("slot directory ownership or permissions are unsafe")
		}
	}
	if err != nil {
		_ = dir.Close()
		return nil, err
	}
	return dir, nil
}

func openStoreFile(dir *os.File, name string, flags int) (*os.File, error) {
	fd, err := syscall.Openat(int(dir.Fd()), name, flags|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), name)
	info, err := file.Stat()
	if err == nil {
		owner, ok := info.Sys().(*syscall.Stat_t)
		if !ok || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || owner.Uid != uint32(os.Geteuid()) || owner.Nlink != 1 {
			err = errors.New("slot file ownership, links, type or permissions are unsafe")
		}
	}
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func (store *FileStore) locked(ctx context.Context, action func(*os.File) (State, error)) (State, error) {
	if err := ctx.Err(); err != nil {
		return State{}, err
	}
	dir, err := store.openDirectory()
	if err != nil {
		return State{}, err
	}
	defer dir.Close()
	lock, err := openStoreFile(dir, "state.lock", syscall.O_RDWR|syscall.O_CREAT)
	if err != nil {
		return State{}, err
	}
	defer lock.Close()
	for {
		err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EINTR) {
			return State{}, err
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return State{}, ctx.Err()
		case <-timer.C:
		}
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	// Opening a safe file before waiting does not prove that it is still the
	// named lock after acquisition. A replaced/unlinked inode would otherwise
	// let this waiter write alongside a holder of the replacement lock.
	currentLock, err := openStoreFile(dir, "state.lock", syscall.O_RDONLY)
	if err != nil {
		return State{}, err
	}
	defer currentLock.Close()
	heldInfo, err := lock.Stat()
	if err != nil {
		return State{}, err
	}
	currentInfo, err := currentLock.Stat()
	if err != nil {
		return State{}, err
	}
	if !os.SameFile(heldInfo, currentInfo) {
		return State{}, errors.New("slot lock identity changed while waiting")
	}
	if err := ctx.Err(); err != nil {
		return State{}, err
	}
	return action(dir)
}

func (store *FileStore) Initialize(ctx context.Context, baseline Receipt) (State, error) {
	state, err := New(baseline)
	if err != nil {
		return State{}, err
	}
	return store.locked(ctx, func(dir *os.File) (State, error) {
		if _, err := readStoreState(dir); !errors.Is(err, os.ErrNotExist) {
			if err == nil {
				err = ErrConflict
			}
			return State{}, err
		}
		return state, store.write(ctx, dir, state)
	})
}

func (store *FileStore) Load(ctx context.Context) (State, error) {
	return store.locked(ctx, readStoreState)
}

// Update compares the on-disk generation while holding an OS file lock. The
// trusted callback uses Stage/Begin/Finish and may not perform external effects:
// those may start only after this method returns a durable successful state.
func (store *FileStore) Update(ctx context.Context, expected uint64, transition func(State) (State, error)) (State, error) {
	if transition == nil {
		return State{}, errors.New("missing slot transition")
	}
	return store.locked(ctx, func(dir *os.File) (State, error) {
		current, err := readStoreState(dir)
		if err != nil {
			return State{}, err
		}
		if current.Generation != expected || expected == ^uint64(0) {
			return State{}, ErrConflict
		}
		next, err := transition(current)
		if err != nil {
			return State{}, err
		}
		if next.Generation != expected+1 {
			return State{}, ErrConflict
		}
		if err := next.Validate(); err != nil {
			return State{}, err
		}
		return next, store.write(ctx, dir, next)
	})
}

func readStoreState(dir *os.File) (State, error) {
	file, err := openStoreFile(dir, "state.json", syscall.O_RDONLY)
	if err != nil {
		return State{}, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxStateBytes+1))
	if err != nil {
		return State{}, err
	}
	if len(data) > maxStateBytes || !utf8.Valid(data) {
		return State{}, errors.New("slot state exceeds its encoding or size limit")
	}
	// encoding/json otherwise accepts duplicate keys and silently takes the
	// last value, including an overwritten pending/started transition.
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := uniqueJSONValue(decoder, 0); err != nil {
		return State{}, err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return State{}, errors.New("slot state contains trailing JSON")
	}
	decoder = json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var state State
	if err := decoder.Decode(&state); err != nil {
		return State{}, err
	}
	return state, state.Validate()
}

func uniqueJSONValue(decoder *json.Decoder, depth int) error {
	if depth > 16 {
		return errors.New("slot state nesting is too deep")
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	opening, container := token.(json.Delim)
	if !container {
		return nil
	}
	if opening != '{' && opening != '[' {
		return errors.New("invalid slot state JSON")
	}
	seen := map[string]bool{}
	for decoder.More() {
		if opening == '{' {
			key, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			name = strings.ToLower(name)
			if !ok || seen[name] {
				return errors.New("ambiguous duplicate slot state key")
			}
			seen[name] = true
		}
		if err := uniqueJSONValue(decoder, depth+1); err != nil {
			return err
		}
	}
	_, err = decoder.Token()
	return err
}

func (store *FileStore) write(ctx context.Context, dir *os.File, state State) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if len(data) > maxStateBytes {
		return errors.New("slot state exceeds its size limit")
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	name := ".state-" + hex.EncodeToString(nonce[:])
	file, err := openStoreFile(dir, name, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_EXCL)
	if err != nil {
		return err
	}
	defer file.Close()
	defer syscall.Unlinkat(int(dir.Fd()), name)
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if store.beforeRename != nil {
		if err := store.beforeRename(); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := syscall.Renameat(int(dir.Fd()), name, int(dir.Fd()), "state.json"); err != nil {
		return err
	}
	if err := store.syncDir(dir); err != nil {
		return errors.Join(ErrDurability, err)
	}
	return nil
}
