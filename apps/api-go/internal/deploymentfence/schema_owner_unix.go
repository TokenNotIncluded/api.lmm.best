//go:build !windows

package deploymentfence

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

func readSchemaMaintenanceOwnerFile(path string) ([]byte, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, errors.New("maintenance owner path must be canonical and absolute")
	}
	for parent := filepath.Dir(path); ; parent = filepath.Dir(parent) {
		info, err := os.Lstat(parent)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o022 != 0 {
			return nil, errors.New("unsafe maintenance owner parent")
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != 0 {
			return nil, errors.New("maintenance owner parents must be root owned")
		}
		if parent == filepath.Dir(parent) {
			break
		}
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, errors.New("maintenance owner file unavailable")
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, errors.New("maintenance owner file metadata unavailable")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 0 || stat.Nlink != 1 || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || info.Size() <= 0 || info.Size() > 16384 {
		return nil, errors.New("maintenance owner file must be root owned 0600 single-link regular file")
	}
	body, err := io.ReadAll(io.LimitReader(file, 16385))
	if err != nil || len(body) == 0 || len(body) > 16384 {
		return nil, errors.New("maintenance owner file read failed")
	}
	return body, nil
}
