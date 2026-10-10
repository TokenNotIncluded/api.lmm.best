//go:build linux

package deploycli

import (
	"errors"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// Open every component relative to an already verified directory descriptor.
// The sole missing component that may be created is the private final leaf.
// EEXIST from a concurrent holder is not approval: reopen NOFOLLOW and verify.
func verifyMerchantStoreSocketDirectory(path string, uid uint32, create bool) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" {
		return errors.New("merchant socket directory is not canonical")
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return errors.New("merchant socket directory root unavailable")
	}
	defer func() { _ = unix.Close(fd) }()
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for i, part := range parts {
		var parent unix.Stat_t
		if unix.Fstat(fd, &parent) != nil || parent.Uid != 0 && parent.Uid != uid || parent.Mode&0022 != 0 && !(parent.Uid == 0 && parent.Mode&unix.S_ISVTX != 0) {
			return errors.New("merchant socket directory ancestor is replaceable")
		}
		next, err := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if errors.Is(err, unix.ENOENT) && create && i == len(parts)-1 {
			if err = unix.Mkdirat(fd, part, 0700); err != nil && !errors.Is(err, unix.EEXIST) {
				return errors.New("merchant socket directory could not be created")
			}
			next, err = unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		}
		if err != nil {
			return errors.New("merchant socket directory is missing or unsafe")
		}
		_ = unix.Close(fd)
		fd = next
	}
	var leaf unix.Stat_t
	if unix.Fstat(fd, &leaf) != nil || leaf.Uid != uid || leaf.Mode&07777 != 0700 {
		return errors.New("merchant socket private directory ownership or mode differs")
	}
	return nil
}
