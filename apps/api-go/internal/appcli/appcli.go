// Package appcli implements the command contract exposed by the lmm-api
// backend binary. It deliberately avoids initializing server resources for
// client-only commands.
package appcli

import (
	"fmt"
	"io"
	"strings"
)

const (
	ProgramName     = "lmm-api"
	ExitOK          = 0
	ExitError       = 1
	ExitHTTPFailure = 22
	ExitUsage       = 64
)

// Mode tells the process entry point whether to exit or start the backend.
type Mode uint8

const (
	ModeExit Mode = iota
	ModeServe
	ModeMigrateApply
	ModeMigrateVerify
)

// Result is the side-effect-free command dispatch result.
type Result struct {
	Mode      Mode
	ExitCode  int
	ServeArgs []string
}

// Dispatch runs client-only commands or returns the arguments for the server.
func Dispatch(args []string, version string, stdout, stderr io.Writer) Result {
	if len(args) == 0 {
		return Result{Mode: ModeServe}
	}

	command := args[0]
	switch command {
	case "serve":
		return Result{Mode: ModeServe, ServeArgs: append([]string(nil), args[1:]...)}
	case "migrate":
		return dispatchMigration(args[1:], stdout, stderr)
	case "request":
		return Result{ExitCode: RunRequest(args[1:], version, stdout, stderr)}
	case "deploy":
		return Result{ExitCode: RunDeploy(args[1:], stdout, stderr)}
	case "backend":
		return Result{ExitCode: runBackend(args[1:], stdout, stderr)}
	case "geoip":
		return Result{ExitCode: RunGeoIP(args[1:], stdout, stderr)}
	case "status":
		return Result{ExitCode: runRouteCommand(args[1:], "/api/status", version, stdout, stderr)}
	case "doctor":
		return Result{ExitCode: runRouteCommand(args[1:], "/api/livez", version, stdout, stderr)}
	case "version", "--version":
		_, _ = fmt.Fprintln(stdout, version)
		return Result{ExitCode: ExitOK}
	case "help", "--help", "-h":
		WriteUsage(stdout)
		return Result{ExitCode: ExitOK}
	default:
		// Server flags may be passed without spelling out the optional serve
		// command. Unknown words fail instead of starting a service unexpectedly.
		if strings.HasPrefix(command, "-") {
			return Result{Mode: ModeServe, ServeArgs: append([]string(nil), args...)}
		}
		_, _ = fmt.Fprintf(stderr, "%s: unknown command %q\n", ProgramName, command)
		WriteUsage(stderr)
		return Result{ExitCode: ExitUsage}
	}
}

func dispatchMigration(args []string, stdout, stderr io.Writer) Result {
	if len(args) == 1 {
		switch args[0] {
		case "--apply":
			return Result{Mode: ModeMigrateApply}
		case "--verify":
			return Result{Mode: ModeMigrateVerify}
		case "--help", "-h":
			writeMigrationUsage(stdout)
			return Result{ExitCode: ExitOK}
		}
	}
	_, _ = fmt.Fprintf(stderr, "%s migrate: choose exactly one of --apply or --verify\n", ProgramName)
	writeMigrationUsage(stderr)
	return Result{ExitCode: ExitUsage}
}

func writeMigrationUsage(output io.Writer) {
	_, _ = fmt.Fprintf(output, "Usage: %s migrate --apply|--verify\n", ProgramName)
}

func runRouteCommand(args []string, route, version string, stdout, stderr io.Writer) int {
	routeArgs := make([]string, 0, len(args)+3)
	routeArgs = append(routeArgs, args...)
	// Route aliases own their method, target, and HTTP failure behavior. Append
	// these flags last so callers cannot redirect status/doctor to another URL.
	routeArgs = append(routeArgs, "--method", "GET", "--path", route, "--fail")
	return RunRequest(routeArgs, version, stdout, stderr)
}

// WriteUsage prints the stable public command surface.
func WriteUsage(output io.Writer) {
	_, _ = fmt.Fprintln(output, `Usage:
  lmm-api [serve] [server options]
  lmm-api migrate --apply|--verify
  lmm-api request [request options] [URL-or-path]
  lmm-api deploy build --repo DIR --workspace DIR [--production]
  lmm-api deploy frontend publish --source DIR --release ID [--root DIR] [--keep N]
  lmm-api deploy frontend rollback [--release ID] [--root DIR] [--keep N]
  lmm-api deploy production plan [signed candidate and rollback inputs]
  lmm-api deploy production stage|promote|status|confirm|rollback \
    --plan FILE --plan-sha256 HEX --confirm api.lmm.best
  lmm-api deploy production edge-policy install|verify
  lmm-api geoip update
  lmm-api backend status
  lmm-api backend select go|rust
  lmm-api status [request options]
  lmm-api doctor [request options]
  lmm-api version
  lmm-api help

The lmm-api invocation is a one-hop provider-selection symlink. This Go build is
installed as lmm-api-go; backend status/select validates and atomically manages
the canonical link. Migration mode is explicit: --apply may change the database,
while --verify is read-only. The request, status,
and doctor commands use the binary's native HTTP client and do not initialize the
server, database, or cache. Deployment commands are implemented by this binary;
they do not delegate release state to shell scripts.`)
}
