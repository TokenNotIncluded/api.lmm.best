package appcli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func cleanAbsoluteNonRoot(value string) (string, error) {
	if !filepath.IsAbs(value) {
		return "", errors.New("path must be absolute")
	}
	clean := filepath.Clean(value)
	if clean == string(filepath.Separator) {
		return "", errors.New("filesystem root is not allowed")
	}
	return clean, nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open directory for sync: %w", err)
	}
	defer directory.Close()
	if err := flushDirectory(directory); err != nil {
		return fmt.Errorf("sync directory: %w", err)
	}
	return nil
}

// CleanAbsoluteNonRoot validates a filesystem path shared by client tools.
func CleanAbsoluteNonRoot(value string) (string, error) { return cleanAbsoluteNonRoot(value) }

// SyncDirectory persists a completed rename without running deployment code.
func SyncDirectory(path string) error { return syncDirectory(path) }

// FlushDirectory shares the platform-specific directory sync operation.
func FlushDirectory(directory *os.File) error { return flushDirectory(directory) }
