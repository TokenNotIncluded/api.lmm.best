//go:build linux

package appcli

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// prepareIncident343RecoveryAudit permits one fresh backup attempt only when
// the previous attempt demonstrably stopped before completing its backup.
// The caller must hold the native deployment lock and revalidate the closed
// admission barrier, installed packages, stopped writer and absent relations.
// Neither the original evidence nor a completed recovery may be overwritten.
func prepareIncident343RecoveryAudit(stateDir string, manifestJSON, statusJSON []byte, ownerUID int) (string, error) {
	if ownerUID < 0 || len(manifestJSON) > 1<<20 || len(statusJSON) > 1<<20 ||
		!json.Valid(manifestJSON) || !json.Valid(statusJSON) {
		return "", errors.New("invalid incident audit identity")
	}
	if err := incident343AuditDirectory(stateDir, ownerUID); err != nil {
		return "", err
	}
	original := filepath.Join(stateDir, "incident-343-schema-recovery")
	retry := filepath.Join(stateDir, "incident-343-schema-recovery-backup-retry-1")
	if _, err := os.Lstat(retry); !errors.Is(err, os.ErrNotExist) {
		return "", errors.New("backup retry evidence already exists or cannot be inspected; no replay")
	}
	if err := os.Mkdir(original, 0700); err == nil {
		return original, nil
	} else if !errors.Is(err, os.ErrExist) {
		return "", errors.New("cannot create incident audit directory")
	}
	if err := incident343AuditDirectory(original, ownerUID); err != nil {
		return "", err
	}
	entries, err := os.ReadDir(original)
	if err != nil || len(entries) != 3 {
		return "", errors.New("prior recovery has incomplete or additional evidence; no backup retry")
	}
	expected := map[string][]byte{
		"before-manifest.json":        manifestJSON,
		"before-status.json":          statusJSON,
		"before-schema.database.dump": {},
	}
	for _, entry := range entries {
		want, allowed := expected[entry.Name()]
		if !allowed {
			return "", errors.New("prior recovery may have progressed beyond backup; no retry")
		}
		got, err := incident343AuditFile(filepath.Join(original, entry.Name()), len(want), ownerUID)
		if err != nil || !bytes.Equal(got, want) {
			return "", errors.New("prior recovery identity or empty-backup evidence mismatch")
		}
	}
	// A separate exclusive directory preserves all prior evidence. This is
	// intentionally a single retry, not an unbounded recovery replay loop.
	if err := os.Mkdir(retry, 0700); err != nil {
		return "", errors.New("cannot reserve a fresh backup retry audit directory")
	}
	return retry, nil
}

func incident343AuditDirectory(path string, ownerUID int) error {
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
		return errors.New("incident audit directory is not private and regular")
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || uint64(owner.Uid) != uint64(ownerUID) {
		return errors.New("incident audit directory owner mismatch")
	}
	return nil
}

func incident343AuditFile(path string, size, ownerUID int) ([]byte, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, errors.New("cannot open private incident audit evidence")
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() != int64(size) {
		return nil, errors.New("incident audit evidence type, mode or size mismatch")
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || uint64(owner.Uid) != uint64(ownerUID) || owner.Nlink != 1 {
		return nil, errors.New("incident audit evidence ownership mismatch")
	}
	return io.ReadAll(io.LimitReader(file, int64(size)+1))
}
