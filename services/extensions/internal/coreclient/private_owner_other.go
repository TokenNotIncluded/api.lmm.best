//go:build !unix

package coreclient

import "os"

// Native production hosts require Unix ownership semantics. Other platforms
// fail closed until an equivalent credential ACL verifier is implemented.
func privateOwner(os.FileInfo) bool { return false }
