package appcli

import (
	"crypto/sha256"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	defaultFrontendRoot = "/srv/lmm-api-frontend"
	defaultReleaseKeep  = 3
)

var (
	releaseIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	indexRefPattern  = regexp.MustCompile(`(?i)(?:src|href)=["']([^"']+)["']`)
)

type frontendDeployOptions struct {
	Action  string
	Root    string
	Source  string
	Release string
	Keep    int
}

// RunDeploy executes deployment operations without starting backend resources.
func RunDeploy(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		writeDeployUsage(stderr)
		return ExitUsage
	}
	switch args[0] {
	case "build":
		return runBuildDeploy(args[1:], stdout, stderr)
	case "frontend":
		return runFrontendDeploy(args[1:], stdout, stderr)
	case "contract":
		return runDeployContract(args[1:], stdout, stderr)
	case "production":
		return runProductionDeploy(args[1:], stdout, stderr)
	case "help", "--help", "-h":
		writeDeployUsage(stdout)
		return ExitOK
	default:
		_, _ = fmt.Fprintf(stderr, "%s deploy: unknown target %q\n", ProgramName, args[0])
		writeDeployUsage(stderr)
		return ExitUsage
	}
}

func writeDeployUsage(output io.Writer) {
	_, _ = fmt.Fprintf(output, `Usage:
  %s deploy build --repo DIR --workspace DIR [--output-dir DIR] [--version VERSION] [--production]
  %s deploy frontend publish --source DIR --release ID [--root DIR] [--keep N]
  %s deploy frontend rollback [--release ID] [--root DIR] [--keep N]
  %s deploy frontend package-activate --package-version VERSION [--root DIR] [--source DIR] [--revision-file FILE] [--keep N]
  %s deploy contract route print|generate|verify [REVISION_FILE]
  %s deploy production harden [--env-file FILE] [--drop-in-dir DIR]
  %s deploy production edge-policy install|verify [--asset-root DIR] [--backup-dir DIR]
  %s deploy production plan --repo DIR --workspace DIR --deployment-id ID \
       --go-package FILE --go-release-asset FILE --go-release-bundle FILE \
       --go-rollback-package FILE --go-rollback-release-asset FILE --go-rollback-release-bundle FILE \
       --web-package FILE --web-release-asset FILE --web-release-bundle FILE \
       --web-rollback-package FILE --web-rollback-release-asset FILE --web-rollback-release-bundle FILE \
       --probe-binary FILE [--with-backups --age-recipient-file FILE]
  %s deploy production stage|promote|status|confirm|rollback \
       --plan FILE --plan-sha256 HEX --confirm api.lmm.best

Production Go changes require --with-backups and the verified target, controller, and off-host copies.
Web-only releases may omit backups.
Target-only recovery commands are listed by the production command's usage.
`, ProgramName, ProgramName, ProgramName, ProgramName, ProgramName, ProgramName, ProgramName, ProgramName, ProgramName)
}

func runFrontendDeploy(args []string, stdout, stderr io.Writer) int {
	if len(args) != 0 && args[0] == "package-activate" {
		return runFrontendPackageActivate(args[1:], stdout, stderr)
	}
	options, err := parseFrontendDeployOptions(args, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return ExitOK
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s deploy frontend: %v\n", ProgramName, err)
		return ExitUsage
	}
	if err := executeFrontendDeploy(options); err != nil {
		_, _ = fmt.Fprintf(stderr, "%s deploy frontend %s: %v\n", ProgramName, options.Action, err)
		return ExitError
	}
	current, err := currentFrontendRelease(options.Root)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s deploy frontend %s: read current release: %v\n", ProgramName, options.Action, err)
		return ExitError
	}
	_, _ = fmt.Fprintf(stdout, "current=%s\n", current)
	return ExitOK
}

func parseFrontendDeployOptions(args []string, stderr io.Writer) (frontendDeployOptions, error) {
	if len(args) == 0 {
		return frontendDeployOptions{}, errors.New("choose publish or rollback")
	}
	options := frontendDeployOptions{Action: args[0], Root: defaultFrontendRoot, Keep: defaultReleaseKeep}
	if options.Action != "publish" && options.Action != "rollback" {
		return frontendDeployOptions{}, fmt.Errorf("unknown action %q", options.Action)
	}
	flags := flag.NewFlagSet("deploy frontend "+options.Action, flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&options.Root, "root", defaultFrontendRoot, "frontend release root")
	flags.StringVar(&options.Source, "source", "", "pre-built frontend directory")
	flags.StringVar(&options.Release, "release", "", "release identifier")
	flags.IntVar(&options.Keep, "keep", defaultReleaseKeep, "total releases to retain")
	flags.Usage = func() { writeDeployUsage(stderr) }
	if err := flags.Parse(args[1:]); err != nil {
		return frontendDeployOptions{}, err
	}
	if flags.NArg() != 0 {
		return frontendDeployOptions{}, errors.New("unexpected positional arguments")
	}
	if options.Keep < 1 {
		return frontendDeployOptions{}, errors.New("--keep must be positive")
	}
	if options.Action == "publish" && (options.Source == "" || options.Release == "") {
		return frontendDeployOptions{}, errors.New("publish requires --source and --release")
	}
	if options.Action == "rollback" && options.Source != "" {
		return frontendDeployOptions{}, errors.New("rollback does not accept --source")
	}
	if options.Release != "" && !releaseIDPattern.MatchString(options.Release) {
		return frontendDeployOptions{}, fmt.Errorf("invalid release id %q", options.Release)
	}
	root, err := cleanAbsoluteNonRoot(options.Root)
	if err != nil {
		return frontendDeployOptions{}, fmt.Errorf("invalid --root: %w", err)
	}
	options.Root = root
	if options.Source != "" {
		source, err := filepath.Abs(options.Source)
		if err != nil {
			return frontendDeployOptions{}, fmt.Errorf("resolve --source: %w", err)
		}
		options.Source = filepath.Clean(source)
	}
	return options, nil
}

func cleanAbsoluteNonRoot(value string) (string, error) {
	if !filepath.IsAbs(value) {
		return "", errors.New("path must be absolute")
	}
	clean := filepath.Clean(value)
	if clean == string(filepath.Separator) {
		return "", errors.New("filesystem root is not allowed")
	}
	return clean, nil
}

func executeFrontendDeploy(options frontendDeployOptions) error {
	if err := prepareFrontendRoot(options.Root); err != nil {
		return err
	}
	lock, err := lockFrontendRelease(options.Root)
	if err != nil {
		return err
	}
	defer func() {
		_ = unlockDeploymentFile(lock)
		_ = lock.Close()
	}()

	protectedRelease := ""
	switch options.Action {
	case "publish":
		// The release that was current when publication began is the only safe
		// rollback target. Keep it even if older leftover releases have newer
		// directory mtimes.
		if current, currentErr := currentFrontendRelease(options.Root); currentErr == nil {
			protectedRelease = current
		} else if !errors.Is(currentErr, os.ErrNotExist) {
			return fmt.Errorf("read pre-publish frontend release: %w", currentErr)
		}
		if err := publishFrontendRelease(options); err != nil {
			return err
		}
	case "rollback":
		if err := rollbackFrontendRelease(options); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported action %q", options.Action)
	}
	return pruneFrontendReleases(options.Root, options.Keep, protectedRelease)
}

func prepareFrontendRoot(root string) error {
	if info, err := os.Lstat(root); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.New("frontend root must be a real directory")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect frontend root: %w", err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return fmt.Errorf("create frontend root: %w", err)
	}
	if err := os.Chmod(root, 0o755); err != nil {
		return fmt.Errorf("set frontend root permissions: %w", err)
	}
	for _, directory := range []struct {
		name string
		mode fs.FileMode
	}{
		{name: "releases", mode: 0o755},
		{name: ".staging", mode: 0o700},
		{name: "assets", mode: 0o755},
	} {
		path := filepath.Join(root, directory.name)
		if err := os.MkdirAll(path, directory.mode); err != nil {
			return fmt.Errorf("create %s: %w", directory.name, err)
		}
		if err := os.Chmod(path, directory.mode); err != nil {
			return fmt.Errorf("set %s permissions: %w", directory.name, err)
		}
	}
	return nil
}

func lockFrontendRelease(root string) (*os.File, error) {
	path := filepath.Join(root, ".release.lock")
	lock, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open release lock: %w", err)
	}
	if err := lock.Chmod(0o600); err != nil {
		_ = lock.Close()
		return nil, fmt.Errorf("protect release lock: %w", err)
	}
	acquired, err := tryDeploymentFileLock(lock)
	if err != nil {
		_ = lock.Close()
		return nil, fmt.Errorf("lock frontend release: %w", err)
	}
	if !acquired {
		_ = lock.Close()
		return nil, errors.New("another frontend release operation is running")
	}
	return lock, nil
}

func publishFrontendRelease(options frontendDeployOptions) (returnErr error) {
	if err := validateFrontendTree(options.Source); err != nil {
		return fmt.Errorf("validate source: %w", err)
	}
	target := filepath.Join(options.Root, "releases", options.Release)
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			return fmt.Errorf("release already exists: %s", options.Release)
		}
		return fmt.Errorf("inspect release target: %w", err)
	}
	stage, err := os.MkdirTemp(filepath.Join(options.Root, ".staging"), options.Release+".")
	if err != nil {
		return fmt.Errorf("create release stage: %w", err)
	}
	defer func() {
		if returnErr != nil {
			_ = os.RemoveAll(stage)
		}
	}()
	if err := copyFrontendTree(options.Source, stage); err != nil {
		return fmt.Errorf("copy release tree: %w", err)
	}
	if err := validateFrontendTree(stage); err != nil {
		return fmt.Errorf("validate staged release: %w", err)
	}
	if err := publishSharedAssets(filepath.Join(stage, "static"), filepath.Join(options.Root, "assets")); err != nil {
		return err
	}
	if err := normalizePublicTree(filepath.Join(options.Root, "assets")); err != nil {
		return fmt.Errorf("normalize shared assets: %w", err)
	}
	if err := normalizePublicTree(stage); err != nil {
		return fmt.Errorf("normalize staged release: %w", err)
	}
	if err := os.Rename(stage, target); err != nil {
		return fmt.Errorf("publish release directory: %w", err)
	}
	if err := syncDirectory(filepath.Dir(target)); err != nil {
		return err
	}
	return switchFrontendCurrent(options.Root, options.Release)
}

func validateFrontendTree(tree string) error {
	root, err := filepath.EvalSymlinks(tree)
	if err != nil {
		return err
	}
	root = filepath.Clean(root)
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return errors.New("tree is not a directory")
	}
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("release tree contains symlink: %s", path)
		}
		if !entry.IsDir() && !entry.Type().IsRegular() {
			return fmt.Errorf("release tree contains unsupported entry: %s", path)
		}
		return nil
	}); err != nil {
		return err
	}
	indexPath := filepath.Join(root, "index.html")
	index, err := os.ReadFile(indexPath)
	if err != nil {
		return fmt.Errorf("read index.html: %w", err)
	}
	for _, match := range indexRefPattern.FindAllSubmatch(index, -1) {
		if len(match) != 2 {
			continue
		}
		reference := string(match[1])
		parsed, err := url.Parse(reference)
		if err != nil {
			return fmt.Errorf("invalid index reference %q", reference)
		}
		if parsed.IsAbs() || parsed.Host != "" || strings.HasPrefix(reference, "//") || parsed.Scheme == "data" {
			continue
		}
		relative := strings.TrimPrefix(parsed.Path, "/")
		if relative == "" {
			continue
		}
		candidate := filepath.Clean(filepath.Join(root, filepath.FromSlash(relative)))
		if !pathWithinRoot(root, candidate) {
			return fmt.Errorf("index reference escapes release: %s", reference)
		}
		candidateInfo, err := os.Stat(candidate)
		if err != nil || !candidateInfo.Mode().IsRegular() {
			return fmt.Errorf("index references missing file: %s", reference)
		}
	}
	return nil
}

func pathWithinRoot(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func copyFrontendTree(source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		target := filepath.Join(destination, relative)
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("source contains symlink: %s", path)
		}
		if entry.IsDir() {
			return os.Mkdir(target, 0o700)
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("source contains unsupported entry: %s", path)
		}
		return copyRegularFile(path, target, 0o600, true)
	})
}

func copyRegularFile(source, destination string, mode fs.FileMode, exclusive bool) (returnErr error) {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	flags := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	if exclusive {
		flags |= os.O_EXCL
	}
	output, err := os.OpenFile(destination, flags, mode)
	if err != nil {
		return err
	}
	defer func() {
		closeErr := output.Close()
		if returnErr == nil && closeErr != nil {
			returnErr = closeErr
		}
	}()
	if _, err := io.Copy(output, input); err != nil {
		return err
	}
	if err := output.Chmod(mode); err != nil {
		return err
	}
	return output.Sync()
}

func publishSharedAssets(sourceStatic, assetRoot string) (returnErr error) {
	info, err := os.Stat(sourceStatic)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !info.IsDir() {
		return errors.New("static asset source is not a directory")
	}
	installed := make([]string, 0)
	defer func() {
		if returnErr != nil {
			for _, path := range installed {
				_ = os.Remove(path)
			}
		}
	}()
	return filepath.WalkDir(sourceStatic, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("static assets contain unsupported entry: %s", path)
		}
		relative, err := filepath.Rel(sourceStatic, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(assetRoot, relative)
		if !pathWithinRoot(assetRoot, destination) {
			return fmt.Errorf("asset destination escapes root: %s", relative)
		}
		if existing, err := os.Stat(destination); err == nil {
			if !existing.Mode().IsRegular() {
				return fmt.Errorf("asset destination is not a regular file: %s", relative)
			}
			equal, err := regularFilesEqual(path, destination)
			if err != nil {
				return err
			}
			if !equal {
				return fmt.Errorf("immutable asset collision with different content: static/%s", filepath.ToSlash(relative))
			}
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		parent := filepath.Dir(destination)
		if err := ensurePublicAssetDirectory(assetRoot, parent); err != nil {
			return err
		}
		temporary := destination + ".tmp." + strconv.Itoa(os.Getpid())
		if err := copyRegularFile(path, temporary, 0o444, true); err != nil {
			return err
		}
		if err := os.Rename(temporary, destination); err != nil {
			_ = os.Remove(temporary)
			return err
		}
		installed = append(installed, destination)
		return syncDirectory(parent)
	})
}

func ensurePublicAssetDirectory(root, directory string) error {
	if !pathWithinRoot(root, directory) {
		return errors.New("asset directory escapes root")
	}
	relative, err := filepath.Rel(root, directory)
	if err != nil {
		return err
	}
	current := root
	if err := os.Chmod(current, 0o755); err != nil {
		return err
	}
	if relative == "." {
		return nil
	}
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		if err := os.Mkdir(current, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		info, err := os.Lstat(current)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("unsafe asset directory: %s", current)
		}
		if err := os.Chmod(current, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func regularFilesEqual(first, second string) (bool, error) {
	digest := func(path string) ([sha256.Size]byte, error) {
		file, err := os.Open(path)
		if err != nil {
			return [sha256.Size]byte{}, err
		}
		defer file.Close()
		hash := sha256.New()
		if _, err := io.Copy(hash, file); err != nil {
			return [sha256.Size]byte{}, err
		}
		var result [sha256.Size]byte
		copy(result[:], hash.Sum(nil))
		return result, nil
	}
	left, err := digest(first)
	if err != nil {
		return false, err
	}
	right, err := digest(second)
	if err != nil {
		return false, err
	}
	return left == right, nil
}

func normalizePublicTree(root string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("public tree contains symlink: %s", path)
		}
		mode := fs.FileMode(0o444)
		if entry.IsDir() {
			mode = 0o755
		} else if !entry.Type().IsRegular() {
			return fmt.Errorf("public tree contains unsupported entry: %s", path)
		}
		return os.Chmod(path, mode)
	})
}

func switchFrontendCurrent(root, release string) error {
	temporary := filepath.Join(root, ".current."+strconv.Itoa(os.Getpid()))
	_ = os.Remove(temporary)
	if err := os.Symlink(filepath.Join("releases", release), temporary); err != nil {
		return fmt.Errorf("create current symlink: %w", err)
	}
	if err := os.Rename(temporary, filepath.Join(root, "current")); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("switch current symlink: %w", err)
	}
	return syncDirectory(root)
}

func rollbackFrontendRelease(options frontendDeployOptions) error {
	release := options.Release
	if release == "" {
		current, err := currentFrontendRelease(options.Root)
		if err != nil {
			return err
		}
		candidates, err := frontendReleaseDirectories(options.Root, current)
		if err != nil {
			return err
		}
		if len(candidates) == 0 {
			return errors.New("no previous release is available")
		}
		release = candidates[0].name
	}
	target := filepath.Join(options.Root, "releases", release)
	if err := validateFrontendTree(target); err != nil {
		return fmt.Errorf("invalid rollback release %s: %w", release, err)
	}
	return switchFrontendCurrent(options.Root, release)
}

type frontendReleaseDirectory struct {
	name    string
	modTime int64
}

func frontendReleaseDirectories(root, exclude string) ([]frontendReleaseDirectory, error) {
	entries, err := os.ReadDir(filepath.Join(root, "releases"))
	if err != nil {
		return nil, err
	}
	releases := make([]frontendReleaseDirectory, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || entry.Name() == exclude || !releaseIDPattern.MatchString(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		releases = append(releases, frontendReleaseDirectory{name: entry.Name(), modTime: info.ModTime().UnixNano()})
	}
	sort.Slice(releases, func(i, j int) bool {
		if releases[i].modTime == releases[j].modTime {
			return releases[i].name > releases[j].name
		}
		return releases[i].modTime > releases[j].modTime
	})
	return releases, nil
}

func pruneFrontendReleases(root string, keep int, protected ...string) error {
	current, err := currentFrontendRelease(root)
	if err != nil {
		return err
	}
	excluded := map[string]struct{}{current: {}}
	for _, release := range protected {
		if release != "" {
			excluded[release] = struct{}{}
		}
	}
	releases, err := frontendReleaseDirectories(root, "")
	if err != nil {
		return err
	}
	retained := len(excluded)
	for _, release := range releases {
		if _, keepRelease := excluded[release.name]; keepRelease {
			continue
		}
		if retained < keep {
			retained++
			continue
		}
		target := filepath.Join(root, "releases", release.name)
		if !pathWithinRoot(filepath.Join(root, "releases"), target) {
			return errors.New("refusing to prune release outside release root")
		}
		if err := os.RemoveAll(target); err != nil {
			return fmt.Errorf("prune release %s: %w", release.name, err)
		}
	}
	return nil
}

func currentFrontendRelease(root string) (string, error) {
	target, err := os.Readlink(filepath.Join(root, "current"))
	if err != nil {
		return "", err
	}
	normalized := filepath.ToSlash(filepath.Clean(target))
	if !strings.HasPrefix(normalized, "releases/") || strings.Count(normalized, "/") != 1 {
		return "", errors.New("current symlink target is unsafe")
	}
	release := strings.TrimPrefix(normalized, "releases/")
	if !releaseIDPattern.MatchString(release) {
		return "", errors.New("current release id is invalid")
	}
	return release, nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open directory for sync: %w", err)
	}
	defer directory.Close()
	if err := flushDirectory(directory); err != nil {
		return fmt.Errorf("sync directory: %w", err)
	}
	return nil
}
