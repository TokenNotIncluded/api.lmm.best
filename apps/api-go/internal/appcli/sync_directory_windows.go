//go:build windows

package appcli

import "os"

func flushDirectory(_ *os.File) error {
	// FlushFileBuffers does not support directory handles on Windows.
	return nil
}
