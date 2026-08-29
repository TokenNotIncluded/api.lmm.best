package appcli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

const (
	backendCanonicalName = "lmm-api"
	backendGoName        = "lmm-api-go"
	backendRustName      = "lmm-api-rs"
)

type backendPaths struct {
	Canonical string
	Go        string
	Rust      string
}

type backendOwnershipRunner interface {
	Owner(path string) (string, error)
}

type pacmanBackendOwnershipRunner struct{}

func (pacmanBackendOwnershipRunner) Owner(path string) (string, error) {
	output, err := exec.Command(commandPacman, "-Qqo", "--", path).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

type backendRuntime struct {
	paths       backendPaths
	owner       backendOwnershipRunner
	effectiveID func() int
	requiredUID uint32
}

type backendProvider struct {
	Name    string
	Target  string
	Path    string
	Package string
}

func defaultBackendRuntime() *backendRuntime {
	return &backendRuntime{
		paths: backendPaths{
			Canonical: "/usr/bin/" + backendCanonicalName,
			Go:        "/usr/bin/" + backendGoName,
			Rust:      "/usr/bin/" + backendRustName,
		},
		owner:       pacmanBackendOwnershipRunner{},
		effectiveID: os.Geteuid,
		requiredUID: 0,
	}
}

func runBackend(args []string, stdout, stderr io.Writer) int {
	return defaultBackendRuntime().run(args, stdout, stderr)
}

func (runtime *backendRuntime) run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		writeBackendUsage(stderr)
		return ExitUsage
	}
	switch args[0] {
	case "status":
		flags := flag.NewFlagSet("backend status", flag.ContinueOnError)
		flags.SetOutput(stderr)
		flags.Usage = func() { writeBackendUsage(stderr) }
		if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 {
			if err == nil {
				_, _ = fmt.Fprintln(stderr, "backend status: unexpected positional arguments")
			}
			return ExitUsage
		}
		provider, err := runtime.status()
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "%s backend status: %v\n", ProgramName, err)
			return ExitError
		}
		_, _ = fmt.Fprintf(stdout, "provider=%s target=%s package=%s\n", provider.Name, provider.Target, provider.Package)
		return ExitOK
	case "select":
		flags := flag.NewFlagSet("backend select", flag.ContinueOnError)
		flags.SetOutput(stderr)
		flags.Usage = func() { writeBackendUsage(stderr) }
		if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 1 {
			if err == nil {
				_, _ = fmt.Fprintln(stderr, "backend select: choose exactly one provider: go or rust")
			}
			return ExitUsage
		}
		provider, err := runtime.selectProvider(flags.Arg(0))
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "%s backend select: %v\n", ProgramName, err)
			return ExitError
		}
		_, _ = fmt.Fprintf(stdout, "provider=%s target=%s package=%s\n", provider.Name, provider.Target, provider.Package)
		return ExitOK
	case "help", "--help", "-h":
		writeBackendUsage(stdout)
		return ExitOK
	default:
		_, _ = fmt.Fprintf(stderr, "backend: unknown command %q\n", args[0])
		writeBackendUsage(stderr)
		return ExitUsage
	}
}

func writeBackendUsage(output io.Writer) {
	_, _ = fmt.Fprintln(output, `Usage:
  lmm-api backend status
  lmm-api backend select go|rust

The canonical /usr/bin/lmm-api entry remains a one-hop relative symlink to one
verified, package-owned provider executable. Selection requires root.`)
}

func (runtime *backendRuntime) provider(selection string) (backendProvider, error) {
	switch selection {
	case "go", backendGoName:
		return backendProvider{Name: "go", Target: backendGoName, Path: runtime.paths.Go}, nil
	case "rust", backendRustName:
		return backendProvider{Name: "rust", Target: backendRustName, Path: runtime.paths.Rust}, nil
	default:
		return backendProvider{}, errors.New("provider must be go or rust")
	}
}

func (runtime *backendRuntime) validateProvider(provider backendProvider) (backendProvider, error) {
	if filepath.Dir(provider.Path) != filepath.Dir(runtime.paths.Canonical) || filepath.Base(provider.Path) != provider.Target {
		return backendProvider{}, errors.New("provider path is outside the canonical binary directory")
	}
	info, err := os.Lstat(provider.Path)
	if err != nil {
		return backendProvider{}, fmt.Errorf("provider executable is missing: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 || info.Mode().Perm()&0o022 != 0 {
		return backendProvider{}, errors.New("provider executable is not a safe, non-writable regular executable")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != runtime.requiredUID {
		return backendProvider{}, errors.New("provider executable has an unsafe owner")
	}
	owner, err := runtime.owner.Owner(provider.Path)
	if err != nil || strings.ContainsAny(owner, " \t\r\n") {
		return backendProvider{}, errors.New("provider package ownership could not be verified")
	}
	allowed := map[string][]string{
		"go":   {"lmm-api-go", "lmm-api-go-bin", "lmm-api-go-git"},
		"rust": {"lmm-api-rs", "lmm-api-rs-bin", "lmm-api-rs-git"},
	}
	valid := false
	for _, name := range allowed[provider.Name] {
		if owner == name {
			valid = true
			break
		}
	}
	if !valid {
		return backendProvider{}, fmt.Errorf("provider executable has unexpected package owner %q", owner)
	}
	provider.Package = owner
	return provider, nil
}

func (runtime *backendRuntime) status() (backendProvider, error) {
	info, err := os.Lstat(runtime.paths.Canonical)
	if err != nil {
		return backendProvider{}, fmt.Errorf("canonical backend link is missing: %w", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return backendProvider{}, errors.New("canonical backend path is not a symlink")
	}
	target, err := os.Readlink(runtime.paths.Canonical)
	if err != nil || filepath.IsAbs(target) || filepath.Base(target) != target {
		return backendProvider{}, errors.New("canonical backend link target is not one-hop relative")
	}
	provider, err := runtime.provider(target)
	if err != nil || provider.Target != target {
		return backendProvider{}, errors.New("canonical backend link has an unsupported target")
	}
	return runtime.validateProvider(provider)
}

func (runtime *backendRuntime) selectProvider(selection string) (backendProvider, error) {
	if runtime.effectiveID() != 0 {
		return backendProvider{}, errors.New("must run as root")
	}
	provider, err := runtime.provider(selection)
	if err != nil {
		return backendProvider{}, err
	}
	provider, err = runtime.validateProvider(provider)
	if err != nil {
		return backendProvider{}, err
	}
	if _, err := os.Lstat(runtime.paths.Canonical); err == nil {
		if _, err := runtime.status(); err != nil {
			return backendProvider{}, fmt.Errorf("refuse to replace unsafe canonical backend path: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return backendProvider{}, fmt.Errorf("inspect canonical backend path: %w", err)
	}
	directory := filepath.Dir(runtime.paths.Canonical)
	temporary, err := os.CreateTemp(directory, ".lmm-api-link.*.new")
	if err != nil {
		return backendProvider{}, fmt.Errorf("reserve canonical backend link: %w", err)
	}
	temporaryPath := temporary.Name()
	if closeErr := temporary.Close(); closeErr != nil {
		_ = os.Remove(temporaryPath)
		return backendProvider{}, closeErr
	}
	if err := os.Remove(temporaryPath); err != nil {
		return backendProvider{}, err
	}
	defer os.Remove(temporaryPath)
	if err := os.Symlink(provider.Target, temporaryPath); err != nil {
		return backendProvider{}, fmt.Errorf("create canonical backend link: %w", err)
	}
	if target, err := os.Readlink(temporaryPath); err != nil || target != provider.Target {
		return backendProvider{}, errors.New("temporary canonical backend link validation failed")
	}
	if err := os.Rename(temporaryPath, runtime.paths.Canonical); err != nil {
		return backendProvider{}, fmt.Errorf("activate canonical backend link: %w", err)
	}
	dir, err := os.Open(directory)
	if err != nil {
		return backendProvider{}, fmt.Errorf("open canonical backend directory for sync: %w", err)
	}
	syncErr := dir.Sync()
	closeErr := dir.Close()
	if syncErr != nil {
		return backendProvider{}, fmt.Errorf("sync canonical backend directory: %w", syncErr)
	}
	if closeErr != nil {
		return backendProvider{}, fmt.Errorf("close canonical backend directory: %w", closeErr)
	}
	return runtime.status()
}
