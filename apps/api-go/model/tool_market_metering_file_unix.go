//go:build linux || darwin || freebsd || openbsd || netbsd || dragonfly

package model

import (
	"os"
	"syscall"
)

func toolMarketMeteringFileOwned(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Nlink == 1 && (stat.Uid == 0 || int(stat.Uid) == os.Geteuid())
}
