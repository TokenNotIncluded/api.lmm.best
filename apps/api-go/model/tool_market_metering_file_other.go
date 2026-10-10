//go:build !(linux || darwin || freebsd || openbsd || netbsd || dragonfly)

package model

import "os"

// Unsupported file-ownership semantics must never enable paid metering.
func toolMarketMeteringFileOwned(os.FileInfo) bool { return false }
