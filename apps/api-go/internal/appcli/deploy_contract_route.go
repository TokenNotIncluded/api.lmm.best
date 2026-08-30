package appcli

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
)

var (
	routeContractVersionPattern  = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\n$`)
	routeContractRevisionPattern = regexp.MustCompile(`^[0-9a-f]{64}\n$`)
)

type routeContractRuntime struct {
	versionPath  string
	revisionPath string
}

func runDeployContract(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "route" {
		writeDeployContractUsage(stderr)
		return ExitUsage
	}
	runtime := routeContractRuntime{
		versionPath:  filepath.Join("contracts", "api-route", "VERSION"),
		revisionPath: "/usr/share/doc/lmm-api-go-bin/API_ROUTE_CONTRACT_REVISION",
	}
	if err := runtime.run(args[1:], stdout); err != nil {
		_, _ = fmt.Fprintf(stderr, "%s deploy contract route: %v\n", ProgramName, err)
		return ExitError
	}
	return ExitOK
}

func writeDeployContractUsage(output io.Writer) {
	_, _ = fmt.Fprintln(output, `Usage:
  lmm-api deploy contract route print
  lmm-api deploy contract route generate OUTPUT
  lmm-api deploy contract route verify REVISION_FILE`)
}

func (runtime routeContractRuntime) revision() (string, error) {
	info, err := os.Lstat(runtime.versionPath)
	if errors.Is(err, os.ErrNotExist) && runtime.revisionPath != "" {
		return readPackagedRouteContractRevision(runtime.revisionPath)
	}
	if err != nil {
		return "", fmt.Errorf("contract version is missing: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", errors.New("contract version must be a non-symlink regular file")
	}
	value, err := os.ReadFile(runtime.versionPath)
	if err != nil {
		return "", err
	}
	if !routeContractVersionPattern.Match(value) {
		return "", errors.New("contract version must contain exactly one newline-terminated stable semantic version")
	}
	digest := sha256.Sum256(value)
	return fmt.Sprintf("%x", digest[:]), nil
}

func readPackagedRouteContractRevision(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("packaged contract revision is missing: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", errors.New("packaged contract revision must be a non-symlink regular file")
	}
	value, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if !routeContractRevisionPattern.Match(value) {
		return "", errors.New("packaged contract revision is malformed")
	}
	return string(value[:len(value)-1]), nil
}

func (runtime routeContractRuntime) run(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New("choose print, generate, or verify")
	}
	revision, err := runtime.revision()
	if err != nil {
		return err
	}
	switch args[0] {
	case "print":
		if len(args) != 1 {
			return errors.New("print accepts no arguments")
		}
		_, err = fmt.Fprintln(stdout, revision)
		return err
	case "generate":
		if len(args) != 2 {
			return errors.New("generate requires one output path")
		}
		return writeRouteContractRevision(args[1], revision)
	case "verify":
		if len(args) != 2 {
			return errors.New("verify requires one revision file")
		}
		info, statErr := os.Lstat(args[1])
		if statErr != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return errors.New("revision file must be a non-symlink regular file")
		}
		value, readErr := os.ReadFile(args[1])
		if readErr != nil {
			return readErr
		}
		if string(value) != revision+"\n" {
			return errors.New("revision file does not match the API route contract")
		}
		return nil
	default:
		return fmt.Errorf("unknown action %q", args[0])
	}
}

func writeRouteContractRevision(path, revision string) error {
	if path == "" {
		return errors.New("output path is empty")
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return errors.New("output path is unsafe")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".api-route-revision.*.new")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o644); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := fmt.Fprintln(temporary, revision); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	return syncDirectory(directory)
}
