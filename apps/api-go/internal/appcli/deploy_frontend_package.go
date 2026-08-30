package appcli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	defaultFrontendPackageSource   = "/usr/share/lmm-api-web/frontend-dist"
	defaultFrontendRevisionFile    = "/usr/share/doc/lmm-api-web-bin/REVISION"
	frontendPackageStateFormat     = 1
	frontendPackageStateDirectory  = ".deployment-transactions"
	frontendPackageMaximumRevision = 128
	frontendPackageMaximumState    = 64 * 1024
)

var (
	frontendPackageVersionPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+:-]{0,80}$`)
	frontendPackageRevisionPattern = regexp.MustCompile(`^[0-9a-f]{40,64}$`)
)

type frontendPackageActivateOptions struct {
	PackageVersion string
	Root           string
	Source         string
	RevisionFile   string
	Keep           int
}

type frontendPackageState struct {
	Format         int       `json:"format"`
	Release        string    `json:"release"`
	Previous       string    `json:"previous,omitempty"`
	PackageVersion string    `json:"package_version"`
	Revision       string    `json:"revision"`
	SourceSHA256   string    `json:"source_sha256"`
	Phase          string    `json:"phase"`
	Failure        string    `json:"failure,omitempty"`
	UpdatedUTC     time.Time `json:"updated_utc"`
}

type frontendPackageCommandRunner interface {
	Run(context.Context, string, ...string) error
}

type osFrontendPackageCommandRunner struct{}

func (osFrontendPackageCommandRunner) Run(ctx context.Context, name string, args ...string) error {
	var command *exec.Cmd
	switch name {
	case "/usr/bin/nginx":
		command = exec.CommandContext(ctx, "/usr/bin/nginx", args...)
	case "/usr/bin/systemctl":
		command = exec.CommandContext(ctx, "/usr/bin/systemctl", args...)
	default:
		return fmt.Errorf("frontend package command is not allowlisted: %s", filepath.Base(name))
	}
	if err := command.Run(); err != nil {
		return fmt.Errorf("%s failed: %w", filepath.Base(name), err)
	}
	return nil
}

type frontendPackageRuntime struct {
	runner       frontendPackageCommandRunner
	now          func() time.Time
	effectiveUID func() int
}

func defaultFrontendPackageRuntime() frontendPackageRuntime {
	return frontendPackageRuntime{
		runner:       osFrontendPackageCommandRunner{},
		now:          time.Now,
		effectiveUID: os.Geteuid,
	}
}

func runFrontendPackageActivate(args []string, stdout, stderr io.Writer) int {
	options, err := parseFrontendPackageActivateOptions(args, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return ExitOK
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s deploy frontend package-activate: %v\n", ProgramName, err)
		return ExitUsage
	}
	state, err := defaultFrontendPackageRuntime().activate(context.Background(), options)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s deploy frontend package-activate: %v\n", ProgramName, err)
		return ExitError
	}
	_, _ = fmt.Fprintf(stdout, "release=%s\nphase=%s\n", state.Release, state.Phase)
	return ExitOK
}

func parseFrontendPackageActivateOptions(args []string, stderr io.Writer) (frontendPackageActivateOptions, error) {
	options := frontendPackageActivateOptions{
		Root:         defaultFrontendRoot,
		Source:       defaultFrontendPackageSource,
		RevisionFile: defaultFrontendRevisionFile,
		Keep:         defaultReleaseKeep,
	}
	flags := flag.NewFlagSet("deploy frontend package-activate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&options.PackageVersion, "package-version", "", "installed Web package version")
	flags.StringVar(&options.Root, "root", defaultFrontendRoot, "frontend release root")
	flags.StringVar(&options.Source, "source", defaultFrontendPackageSource, "packaged frontend directory")
	flags.StringVar(&options.RevisionFile, "revision-file", defaultFrontendRevisionFile, "installed release revision file")
	flags.IntVar(&options.Keep, "keep", defaultReleaseKeep, "total releases to retain")
	if err := flags.Parse(args); err != nil {
		return frontendPackageActivateOptions{}, err
	}
	if flags.NArg() != 0 {
		return frontendPackageActivateOptions{}, errors.New("unexpected positional arguments")
	}
	if !frontendPackageVersionPattern.MatchString(options.PackageVersion) {
		return frontendPackageActivateOptions{}, errors.New("--package-version is missing or invalid")
	}
	if options.Keep < 1 {
		return frontendPackageActivateOptions{}, errors.New("--keep must be positive")
	}
	root, err := cleanAbsoluteNonRoot(options.Root)
	if err != nil {
		return frontendPackageActivateOptions{}, fmt.Errorf("invalid --root: %w", err)
	}
	options.Root = root
	for label, value := range map[string]string{"source": options.Source, "revision-file": options.RevisionFile} {
		absolute, err := filepath.Abs(value)
		if err != nil {
			return frontendPackageActivateOptions{}, fmt.Errorf("resolve --%s: %w", label, err)
		}
		if absolute == string(filepath.Separator) {
			return frontendPackageActivateOptions{}, fmt.Errorf("--%s cannot be the filesystem root", label)
		}
		switch label {
		case "source":
			options.Source = filepath.Clean(absolute)
		case "revision-file":
			options.RevisionFile = filepath.Clean(absolute)
		}
	}
	return options, nil
}

func (runtime frontendPackageRuntime) activate(ctx context.Context, options frontendPackageActivateOptions) (state frontendPackageState, returnErr error) {
	if runtime.effectiveUID == nil || runtime.effectiveUID() != 0 {
		return state, errors.New("package activation requires root")
	}
	if runtime.runner == nil || runtime.now == nil {
		return state, errors.New("package activation runtime is incomplete")
	}
	if err := validateFrontendTree(options.Source); err != nil {
		return state, fmt.Errorf("validate packaged frontend: %w", err)
	}
	revision, err := readFrontendPackageRevision(options.RevisionFile)
	if err != nil {
		return state, err
	}
	releaseVersion := strings.NewReplacer(":", "-", "+", "_").Replace(options.PackageVersion)
	release := releaseVersion + ".g" + revision[:12]
	if !releaseIDPattern.MatchString(release) {
		return state, errors.New("derived frontend release id is unsafe")
	}
	sourceDigest, err := frontendTreeSHA256(options.Source)
	if err != nil {
		return state, fmt.Errorf("hash packaged frontend: %w", err)
	}
	if err := prepareFrontendRoot(options.Root); err != nil {
		return state, err
	}
	previous, currentErr := currentFrontendRelease(options.Root)
	if currentErr != nil && !errors.Is(currentErr, os.ErrNotExist) {
		return state, fmt.Errorf("read current frontend release: %w", currentErr)
	}
	state = frontendPackageState{
		Format: frontendPackageStateFormat, Release: release, Previous: previous,
		PackageVersion: options.PackageVersion, Revision: revision, SourceSHA256: sourceDigest,
		Phase: "PREPARING", UpdatedUTC: runtime.now().UTC().Truncate(time.Second),
	}
	statePath, err := frontendPackageStatePath(options.Root, release)
	if err != nil {
		return state, err
	}
	if existing, readErr := readFrontendPackageState(statePath); readErr == nil {
		if !sameFrontendPackageIdentity(existing, state) {
			return state, errors.New("existing frontend package transaction identity differs")
		}
		if existing.Phase == "CONFIRMED" {
			current, err := currentFrontendRelease(options.Root)
			if err == nil && current == release {
				return existing, nil
			}
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return state, fmt.Errorf("read active frontend before confirmed retry: %w", err)
			}
			// An explicit production rollback can restore the previous current
			// link while retaining this immutable release and its confirmed state.
			// Re-enter the normal digest and nginx gates instead of permanently
			// blocking a later deployment of the same signed package.
		}
		if existing.Phase == "ROLLBACK_REQUIRED" {
			return existing, errors.New("frontend transaction requires explicit rollback")
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return state, readErr
	}
	if err := runtime.writeState(statePath, state); err != nil {
		return state, err
	}
	if err := runtime.runNginxPreflight(ctx); err != nil {
		state.Phase, state.Failure = "FAILED_PREARM", "nginx-preflight"
		state.UpdatedUTC = runtime.now().UTC().Truncate(time.Second)
		_ = runtime.writeState(statePath, state)
		return state, err
	}

	state.Phase = "MUTATION_PENDING"
	state.UpdatedUTC = runtime.now().UTC().Truncate(time.Second)
	if err := runtime.writeState(statePath, state); err != nil {
		return state, err
	}
	mutated := false
	defer func() {
		if returnErr == nil || !mutated {
			return
		}
		state.Phase, state.Failure = "ROLLBACK_REQUIRED", "frontend-activation"
		state.UpdatedUTC = runtime.now().UTC().Truncate(time.Second)
		if writeErr := runtime.writeState(statePath, state); writeErr != nil {
			returnErr = errors.Join(returnErr, writeErr)
		}
	}()

	mutated = true // Publication may switch `current` before a later fsync error.
	publishOptions := frontendDeployOptions{
		Action: "publish", Root: options.Root, Source: options.Source, Release: release, Keep: options.Keep,
	}
	target := filepath.Join(options.Root, "releases", release)
	if info, statErr := os.Lstat(target); statErr == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return state, errors.New("existing frontend release target is unsafe")
		}
		targetDigest, digestErr := frontendTreeSHA256(target)
		if digestErr != nil {
			return state, digestErr
		}
		if targetDigest != sourceDigest {
			return state, errors.New("existing frontend release has different contents")
		}
		if err := switchFrontendCurrent(options.Root, release); err != nil {
			return state, err
		}
	} else if errors.Is(statErr, os.ErrNotExist) {
		if err := executeFrontendDeploy(publishOptions); err != nil {
			return state, err
		}
	} else {
		return state, fmt.Errorf("inspect frontend release target: %w", statErr)
	}

	if err := runtime.runNginxConfirmation(ctx); err != nil {
		return state, err
	}
	state.Phase, state.Failure = "CONFIRMED", ""
	state.UpdatedUTC = runtime.now().UTC().Truncate(time.Second)
	if err := runtime.writeState(statePath, state); err != nil {
		return state, err
	}
	return state, nil
}

func readFrontendPackageRevision(path string) (string, error) {
	contents, err := readSafeRegularFile(path, frontendPackageMaximumRevision)
	if err != nil {
		return "", fmt.Errorf("read frontend revision: %w", err)
	}
	revision := strings.TrimSuffix(string(contents), "\n")
	if string(contents) != revision+"\n" || !frontendPackageRevisionPattern.MatchString(revision) {
		return "", errors.New("frontend revision must be one lowercase Git object id line")
	}
	return revision, nil
}

func frontendPackageStatePath(root, release string) (string, error) {
	stateRoot := filepath.Join(root, frontendPackageStateDirectory)
	if err := os.MkdirAll(stateRoot, 0o700); err != nil {
		return "", fmt.Errorf("create frontend transaction directory: %w", err)
	}
	info, err := os.Lstat(stateRoot)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("frontend transaction directory is unsafe")
	}
	if err := os.Chmod(stateRoot, 0o700); err != nil {
		return "", fmt.Errorf("secure frontend transaction directory: %w", err)
	}
	return filepath.Join(stateRoot, release+".json"), nil
}

func decodeFrontendPackageState(contents []byte, state *frontendPackageState) error {
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(state); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("frontend package state contains multiple JSON values")
		}
		return err
	}
	return nil
}

func readFrontendPackageState(path string) (frontendPackageState, error) {
	var state frontendPackageState
	if _, err := os.Lstat(path); err != nil {
		return state, err
	}
	contents, err := readSafeRegularFile(path, frontendPackageMaximumState)
	if err != nil {
		return state, err
	}
	if err := decodeFrontendPackageState(contents, &state); err != nil {
		return state, fmt.Errorf("decode frontend package state: %w", err)
	}
	if state.Format != frontendPackageStateFormat || !releaseIDPattern.MatchString(state.Release) {
		return state, errors.New("frontend package state is invalid")
	}
	return state, nil
}

func (runtime frontendPackageRuntime) writeState(path string, state frontendPackageState) error {
	encoded, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode frontend package state: %w", err)
	}
	encoded = append(encoded, '\n')
	if err := writeAtomicRegularFile(path, encoded, 0o600); err != nil {
		return fmt.Errorf("write frontend package state: %w", err)
	}
	return nil
}

func sameFrontendPackageIdentity(first, second frontendPackageState) bool {
	return first.Format == second.Format && first.Release == second.Release &&
		first.PackageVersion == second.PackageVersion && first.Revision == second.Revision &&
		first.SourceSHA256 == second.SourceSHA256
}

func (runtime frontendPackageRuntime) runNginxPreflight(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := runtime.runner.Run(ctx, "/usr/bin/nginx", "-t"); err != nil {
		return fmt.Errorf("nginx preflight failed: %w", err)
	}
	return nil
}

func (runtime frontendPackageRuntime) runNginxConfirmation(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := runtime.runner.Run(ctx, "/usr/bin/nginx", "-t"); err != nil {
		return fmt.Errorf("post-activation nginx validation failed: %w", err)
	}
	if err := runtime.runner.Run(ctx, "/usr/bin/systemctl", "reload", "nginx.service"); err != nil {
		return fmt.Errorf("reload nginx: %w", err)
	}
	if err := runtime.runner.Run(ctx, "/usr/bin/systemctl", "is-active", "--quiet", "nginx.service"); err != nil {
		return fmt.Errorf("confirm nginx active: %w", err)
	}
	return nil
}

func frontendTreeSHA256(root string) (string, error) {
	var paths []string
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("frontend tree contains symlink: %s", relative)
		}
		if entry.Type().IsRegular() {
			paths = append(paths, relative)
		}
		return nil
	}); err != nil {
		return "", err
	}
	sort.Strings(paths)
	digest := sha256.New()
	for _, relative := range paths {
		contents, err := os.ReadFile(filepath.Join(root, relative))
		if err != nil {
			return "", err
		}
		_, _ = io.WriteString(digest, filepath.ToSlash(relative))
		_, _ = digest.Write([]byte{0})
		_, _ = digest.Write(contents)
		_, _ = digest.Write([]byte{0})
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}
