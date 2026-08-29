//go:build windows

package appcli

import "os"

func backendFileOwnerUID(_ os.FileInfo) (uint32, bool) {
	return 0, false
}
