//go:build !windows

package credittransition

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

func readSealedFile(path string) ([]byte, error) {
	for parent := filepath.Dir(path); ; parent = filepath.Dir(parent) {
		info, err := os.Lstat(parent)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o022 != 0 {
			return nil, errors.New("sealed credit transition has an unsafe parent directory")
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != 0 {
			return nil, errors.New("sealed credit transition parents must be root-owned")
		}
		if parent == filepath.Dir(parent) {
			break
		}
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, errors.New("cannot open sealed credit transition")
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, errors.New("cannot inspect sealed credit transition")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 0 || stat.Nlink != 1 || !info.Mode().IsRegular() ||
		(info.Mode().Perm() != 0o600 && info.Mode().Perm() != 0o640) || info.Size() <= 0 || info.Size() > 65536 {
		return nil, errors.New("sealed credit transition must be a root-owned private regular file")
	}
	data, err := io.ReadAll(io.LimitReader(file, 65537))
	if err != nil || len(data) > 65536 {
		return nil, errors.New("cannot read sealed credit transition")
	}
	return data, nil
}
