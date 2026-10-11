//go:build unix

package coreclient

import (
	"os"
	"syscall"
)

func privateOwner(info os.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && (st.Uid == 0 || st.Uid == uint32(os.Geteuid()))
}
