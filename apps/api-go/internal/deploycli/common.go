// Package deploycli implements the standalone deployment tool. The API server
// must never import this package. Runtime database fences remain in the server.
package deploycli

import "github.com/LIghtJUNction/api.lmm.best/internal/appcli"

const (
	ProgramName          = appcli.ProgramName
	ExitOK               = appcli.ExitOK
	ExitError            = appcli.ExitError
	ExitUsage            = appcli.ExitUsage
	backendCanonicalName = "lmm-api"
	backendGoName        = "lmm-api-go"
	backendRustName      = "lmm-api-rs"
)

var cleanAbsoluteNonRoot = appcli.CleanAbsoluteNonRoot
var syncDirectory = appcli.SyncDirectory
var flushDirectory = appcli.FlushDirectory
