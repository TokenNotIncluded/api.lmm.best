//go:build !windows

package appcli

import "os"

func flushDirectory(directory *os.File) error {
	return directory.Sync()
}
