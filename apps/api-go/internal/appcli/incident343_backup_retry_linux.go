//go:build linux

package appcli

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// Reuse no failed output and erase no evidence. A single new child audit is
// allowed only for the observed empty pg_dump failure, before backup validation,
// service stop or DDL. The caller still verifies the live native transaction,
// installed packages, admission barrier, old writer receipt and absent tables.
func prepareIncident343BackupAudit(root string, manifestJSON, statusJSON []byte, owner uint32) (string, error) {
	if err := os.Mkdir(root, 0700); err == nil {
		return root, nil
	} else if !errors.Is(err, os.ErrExist) {
		return "", err
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		return "", errors.New("previous recovery audit is not a private real directory")
	}
	identity, ok := info.Sys().(*syscall.Stat_t)
	if !ok || identity.Uid != owner {
		return "", errors.New("previous recovery audit owner mismatch")
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 3 {
		return "", errors.New("previous recovery progressed or contains unexpected evidence; no replay")
	}
	allowed := map[string][]byte{
		"before-manifest.json":        manifestJSON,
		"before-status.json":          statusJSON,
		"before-schema.database.dump": nil,
	}
	for _, entry := range entries {
		expected, found := allowed[entry.Name()]
		if !found {
			return "", errors.New("previous recovery contains unexpected evidence; no replay")
		}
		actual, err := readIncident343RetryEvidence(filepath.Join(root, entry.Name()), owner)
		if err != nil {
			return "", err
		}
		if entry.Name() == "before-schema.database.dump" {
			if len(actual) != 0 {
				return "", errors.New("previous database snapshot is not empty; inspect rather than replay")
			}
		} else if !bytes.Equal(bytes.TrimSpace(actual), bytes.TrimSpace(expected)) {
			return "", errors.New("native state changed after failed backup; no replay")
		}
	}
	child := filepath.Join(root, "after-backup-permission-fix")
	if err := os.Mkdir(child, 0700); err != nil {
		return "", errors.New("backup correction already attempted; inspect rather than replay")
	}
	return child, nil
}

func readIncident343RetryEvidence(path string, owner uint32) ([]byte, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > 1<<20 {
		return nil, errors.New("unsafe previous recovery evidence")
	}
	identity, ok := info.Sys().(*syscall.Stat_t)
	if !ok || identity.Uid != owner || identity.Nlink != 1 {
		return nil, errors.New("previous recovery evidence owner or link count mismatch")
	}
	data, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return nil, errors.New("previous recovery evidence exceeded its limit")
	}
	return data, nil
}
