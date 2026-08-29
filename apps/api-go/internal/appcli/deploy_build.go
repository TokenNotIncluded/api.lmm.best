package appcli

import (
	"archive/tar"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const buildCommandTimeout = 30 * time.Minute

var goReleaseTagPattern = regexp.MustCompile(`^go-v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

type buildDeployOptions struct {
	Repo       string
	Workspace  string
	OutputDir  string
	Version    string
	Production bool
}

type buildDeployResult struct {
	Version             string `json:"version"`
	Revision            string `json:"revision"`
	Dirty               bool   `json:"dirty"`
	Binary              string `json:"binary"`
	BinarySHA256        string `json:"binary_sha256"`
	Frontend            string `json:"frontend"`
	FrontendIndexSHA256 string `json:"frontend_index_sha256"`
	Package             string `json:"package"`
	PackageSHA256       string `json:"package_sha256"`
}

type buildDeployRuntime struct {
	runner productionCommandRunner
	now    func() time.Time
}

func runBuildDeploy(args []string, stdout, stderr io.Writer) int {
	options, err := parseBuildDeployOptions(args, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return ExitOK
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s deploy build: %v\n", ProgramName, err)
		return ExitUsage
	}
	runtime := &buildDeployRuntime{runner: osProductionCommandRunner{}, now: time.Now}
	result, err := runtime.build(context.Background(), options)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s deploy build: %v\n", ProgramName, err)
		return ExitError
	}
	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s deploy build: encode result: %v\n", ProgramName, err)
		return ExitError
	}
	_, _ = stdout.Write(append(encoded, '\n'))
	return ExitOK
}

func parseBuildDeployOptions(args []string, stderr io.Writer) (buildDeployOptions, error) {
	options := buildDeployOptions{}
	flags := flag.NewFlagSet("deploy build", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&options.Repo, "repo", "", "api.lmm.best source checkout")
	flags.StringVar(&options.Workspace, "workspace", "", "marker-owned persistent build workspace")
	flags.StringVar(&options.OutputDir, "output-dir", "", "artifact output directory inside the workspace")
	flags.StringVar(&options.Version, "version", "", "explicit build version")
	flags.BoolVar(&options.Production, "production", false, "require clean origin/main source and release identity")
	flags.Usage = func() { writeDeployUsage(stderr) }
	if err := flags.Parse(args); err != nil {
		return buildDeployOptions{}, err
	}
	if flags.NArg() != 0 {
		return buildDeployOptions{}, errors.New("unexpected positional arguments")
	}
	if options.Repo == "" || options.Workspace == "" {
		return buildDeployOptions{}, errors.New("--repo and --workspace are required")
	}
	for label, value := range map[string]*string{"--repo": &options.Repo, "--workspace": &options.Workspace} {
		clean, err := cleanAbsoluteNonRoot(*value)
		if err != nil {
			return buildDeployOptions{}, fmt.Errorf("invalid %s: %w", label, err)
		}
		*value = clean
	}
	if options.OutputDir != "" {
		clean, err := cleanAbsoluteNonRoot(options.OutputDir)
		if err != nil {
			return buildDeployOptions{}, fmt.Errorf("invalid --output-dir: %w", err)
		}
		options.OutputDir = clean
	}
	if options.Version != "" && !productionVersionPattern.MatchString(options.Version) {
		return buildDeployOptions{}, errors.New("invalid --version")
	}
	return options, nil
}

func (buildRuntime *buildDeployRuntime) build(ctx context.Context, options buildDeployOptions) (buildDeployResult, error) {
	if err := validateBuildRepository(options.Repo); err != nil {
		return buildDeployResult{}, err
	}
	if err := validateBuildWorkspace(options.Workspace); err != nil {
		return buildDeployResult{}, err
	}
	outputDir := options.OutputDir
	if outputDir == "" {
		outputDir = filepath.Join(options.Workspace, "artifacts")
	}
	if !pathWithinRoot(options.Workspace, outputDir) {
		return buildDeployResult{}, errors.New("artifact output must stay inside the marker-owned workspace")
	}
	if err := ensureRealDirectory(outputDir, 0o700); err != nil {
		return buildDeployResult{}, fmt.Errorf("prepare artifact output: %w", err)
	}
	temporaryRoot := filepath.Join(options.Workspace, "tmp")
	if err := ensureRealDirectory(temporaryRoot, 0o700); err != nil {
		return buildDeployResult{}, fmt.Errorf("prepare build temporary directory: %w", err)
	}
	revision, version, dirty, err := buildRuntime.resolveBuildIdentity(ctx, options)
	if err != nil {
		return buildDeployResult{}, err
	}

	frontend := filepath.Join(options.Repo, "apps", "web", "dist")
	frontendEnvironment := append(os.Environ(), "VITE_REACT_APP_VERSION="+version)
	if _, err := buildRuntime.runner.Run(ctx, productionCommand{Name: commandBun, Args: []string{"run", "build:web"}, Dir: options.Repo,
		Env: frontendEnvironment, Timeout: buildCommandTimeout}); err != nil {
		return buildDeployResult{}, fmt.Errorf("build web frontend: %w", err)
	}
	if _, err := buildRuntime.runner.Run(ctx, productionCommand{Name: commandBun, Args: []string{"run", "--filter", "@lmm/web", "bundle:check"},
		Dir: options.Repo, Timeout: buildCommandTimeout}); err != nil {
		return buildDeployResult{}, fmt.Errorf("check frontend bundle: %w", err)
	}
	if err := validateFrontendTree(frontend); err != nil {
		return buildDeployResult{}, fmt.Errorf("validate built frontend: %w", err)
	}
	frontendSHA256, err := sha256File(filepath.Join(frontend, "index.html"))
	if err != nil {
		return buildDeployResult{}, fmt.Errorf("hash built frontend index: %w", err)
	}

	binary := filepath.Join(outputDir, backendGoName+"-"+version)
	if _, err := os.Lstat(binary); !errors.Is(err, os.ErrNotExist) {
		return buildDeployResult{}, errors.New("versioned Go binary output already exists")
	}
	temporaryBinary, err := os.CreateTemp(outputDir, ".lmm-api-go.*.new")
	if err != nil {
		return buildDeployResult{}, fmt.Errorf("create binary output: %w", err)
	}
	temporaryBinaryPath := temporaryBinary.Name()
	if err := temporaryBinary.Close(); err != nil {
		_ = os.Remove(temporaryBinaryPath)
		return buildDeployResult{}, err
	}
	defer os.Remove(temporaryBinaryPath)
	goEnvironment := append(os.Environ(), "CGO_ENABLED=0")
	linkerFlags := "-s -w -extldflags=-static -X github.com/LIghtJUNction/api.lmm.best/common.Version=" + version
	if _, err := buildRuntime.runner.Run(ctx, productionCommand{Name: commandGo, Args: []string{"build", "-trimpath", "-buildvcs=false", "-ldflags", linkerFlags, "-o", temporaryBinaryPath, "."},
		Dir: filepath.Join(options.Repo, "apps", "api-go"), Env: goEnvironment, Timeout: buildCommandTimeout}); err != nil {
		return buildDeployResult{}, fmt.Errorf("build Go backend and CLI: %w", err)
	}
	if err := os.Chmod(temporaryBinaryPath, 0o755); err != nil {
		return buildDeployResult{}, err
	}
	versionOutput, err := runVerifiedBinary(ctx, buildRuntime.runner, temporaryBinaryPath, []string{"version"}, nil, "", productionCommandTimeout, false)
	if err != nil || strings.TrimSpace(string(versionOutput)) != version {
		return buildDeployResult{}, errors.New("built Go binary version assertion failed")
	}
	fileOutput, err := buildRuntime.runner.Run(ctx, productionCommand{Name: commandFile, Args: []string{"-Lb", temporaryBinaryPath}})
	if err != nil || !strings.Contains(string(fileOutput), "statically linked") {
		return buildDeployResult{}, fmt.Errorf("built Go binary is not statically linked: %s", strings.TrimSpace(string(fileOutput)))
	}
	if err := os.Link(temporaryBinaryPath, binary); err != nil {
		return buildDeployResult{}, fmt.Errorf("publish built Go binary: %w", err)
	}
	if err := os.Remove(temporaryBinaryPath); err != nil {
		return buildDeployResult{}, fmt.Errorf("remove temporary Go binary link: %w", err)
	}
	if err := syncDirectory(outputDir); err != nil {
		return buildDeployResult{}, err
	}
	binarySHA256, err := sha256File(binary)
	if err != nil {
		return buildDeployResult{}, err
	}
	packagePath, packageSHA256, err := buildRuntime.buildPackage(ctx, options, revision, version, binary, frontend, outputDir, temporaryRoot)
	if err != nil {
		return buildDeployResult{}, err
	}
	return buildDeployResult{
		Version: version, Revision: revision, Dirty: dirty,
		Binary: binary, BinarySHA256: binarySHA256,
		Frontend: frontend, FrontendIndexSHA256: frontendSHA256,
		Package: packagePath, PackageSHA256: packageSHA256,
	}, nil
}

func validateBuildRepository(repo string) error {
	if err := requireRealDirectory(repo); err != nil {
		return fmt.Errorf("source repository is missing or unsafe: %w", err)
	}
	for _, relative := range []string{
		"VERSION", "LICENSE", "NOTICE", "THIRD-PARTY-LICENSES.md",
		"apps/api-go/go.mod", "apps/web/package.json",
		"packaging/local/lmm-api-go/PKGBUILD",
		"packaging/common/lmm-api/lmm-api.service",
		"packaging/common/lmm-api/lmm-api-go.env",
		"packaging/common/lmm-api/lmm-api-operator.sysusers",
		"packaging/common/lmm-api/lmm-api-operator.tmpfiles",
		"packaging/common/lmm-api/lmm-api-operator.sudoers",
		"packaging/common/lmm-api/geoip2-country-update.service",
		"packaging/common/lmm-api/geoip2-country-update.timer",
		"packaging/common/lmm-api/edge-policy/nginx/http-map.conf",
		"packaging/common/lmm-api/edge-policy/nginx/lmm-api-locations.conf",
		"packaging/common/lmm-api/edge-policy/nginx/mime.types",
		"packaging/common/lmm-api/edge-policy/nginx/new-api.conf",
		"packaging/common/lmm-api/edge-policy/nginx/lmm-api-region-policy.conf",
	} {
		path := filepath.Join(repo, filepath.FromSlash(relative))
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("required source file is missing or unsafe: %s", relative)
		}
	}
	return nil
}

func validateBuildWorkspace(workspace string) error {
	if err := requireRealDirectory(workspace); err != nil {
		return fmt.Errorf("build workspace is missing or unsafe: %w", err)
	}
	canonical, err := filepath.EvalSymlinks(workspace)
	if err != nil || filepath.Clean(canonical) != workspace {
		return errors.New("build workspace must be canonical and symlink-free")
	}
	marker, err := readPrivateRegularFile(filepath.Join(workspace, productionWorkspaceMarker), 16<<10)
	if err != nil {
		return fmt.Errorf("read build workspace marker: %w", err)
	}
	values, err := parseSimpleManifest(marker)
	if err != nil || values["deployment_id"] == "" || !productionIDPattern.MatchString(values["deployment_id"]) {
		return errors.New("build workspace marker has an invalid deployment ID")
	}
	return nil
}

func (buildRuntime *buildDeployRuntime) resolveBuildIdentity(ctx context.Context, options buildDeployOptions) (string, string, bool, error) {
	runGit := func(arguments ...string) (string, error) {
		output, err := buildRuntime.runner.Run(ctx, productionCommand{Name: commandGit, Args: append([]string{"-C", options.Repo}, arguments...), Timeout: productionCommandTimeout})
		return strings.TrimSpace(string(output)), err
	}
	revision, err := runGit("rev-parse", "HEAD")
	if err != nil || !regexpGitRevision(revision) {
		return "", "", false, errors.New("source HEAD is not a valid Git revision")
	}
	status, err := runGit("status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return "", "", false, fmt.Errorf("inspect source changes: %w", err)
	}
	dirty := status != ""
	countText, err := runGit("rev-list", "--count", "HEAD")
	if err != nil {
		return "", "", false, fmt.Errorf("count source revisions: %w", err)
	}
	count, err := strconv.ParseUint(countText, 10, 64)
	if err != nil || count == 0 {
		return "", "", false, errors.New("Git revision count is invalid")
	}
	shortRevision, err := runGit("rev-parse", "--short=9", "HEAD")
	if err != nil || len(shortRevision) != 9 || !regexpGitRevision(shortRevision) {
		return "", "", false, errors.New("short Git revision is invalid")
	}
	baseVersionContent, err := readPrivateRegularFile(filepath.Join(options.Repo, "VERSION"), 1024)
	if err != nil {
		return "", "", false, err
	}
	baseVersion := strings.TrimSpace(string(baseVersionContent))
	if !productionVersionPattern.MatchString(baseVersion) {
		return "", "", false, errors.New("VERSION is invalid")
	}
	if options.Production {
		if dirty {
			return "", "", false, errors.New("production build requires a clean tracked and untracked worktree")
		}
		goReleaseVersion, err := buildRuntime.resolveMergedGoReleaseVersion(runGit)
		if err != nil {
			return "", "", false, err
		}
		baseVersion = goReleaseVersion
		remoteLine, err := runGit("ls-remote", "origin", "refs/heads/main")
		if err != nil {
			return "", "", false, fmt.Errorf("read origin/main: %w", err)
		}
		fields := strings.Fields(remoteLine)
		if len(fields) != 2 || fields[0] != revision || fields[1] != "refs/heads/main" {
			return "", "", false, errors.New("production source HEAD must equal origin/main")
		}
	}
	computedVersion := fmt.Sprintf("%s.r%d.g%s", baseVersion, count, shortRevision)
	if dirty {
		computedVersion += ".dirty." + buildRuntime.now().UTC().Format("20060102T150405Z")
	}
	if options.Production {
		if options.Version != "" && options.Version != computedVersion {
			return "", "", false, errors.New("explicit production version does not match the source release identity")
		}
	}
	version := options.Version
	if version == "" {
		version = computedVersion
	}
	if !productionVersionPattern.MatchString(version) {
		return "", "", false, errors.New("resolved build version is invalid")
	}
	return revision, version, dirty, nil
}

// resolveMergedGoReleaseVersion makes native production candidates follow the
// independently published Go release line. VERSION belongs to the unified
// product release and can legitimately lag behind a Go-only hotfix.
func (buildRuntime *buildDeployRuntime) resolveMergedGoReleaseVersion(
	runGit func(arguments ...string) (string, error),
) (string, error) {
	output, err := runGit("tag", "--merged", "HEAD", "--list", "go-v*", "--sort=-v:refname")
	if err != nil {
		return "", fmt.Errorf("list merged Go release tags: %w", err)
	}
	var selected [3]uint64
	selectedSet := false
	for _, tag := range strings.Fields(output) {
		match := goReleaseTagPattern.FindStringSubmatch(tag)
		if match == nil {
			continue
		}
		var candidate [3]uint64
		valid := true
		for index := range candidate {
			value, parseErr := strconv.ParseUint(match[index+1], 10, 64)
			if parseErr != nil {
				valid = false
				break
			}
			candidate[index] = value
		}
		if !valid || selectedSet && !goReleaseVersionAfter(candidate, selected) {
			continue
		}
		selected = candidate
		selectedSet = true
	}
	if !selectedSet {
		return "", errors.New("production source has no valid merged Go release tag")
	}
	return fmt.Sprintf("%d.%d.%d", selected[0], selected[1], selected[2]), nil
}

func goReleaseVersionAfter(left, right [3]uint64) bool {
	for index := range left {
		if left[index] != right[index] {
			return left[index] > right[index]
		}
	}
	return false
}

func regexpGitRevision(value string) bool {
	if len(value) < 7 || len(value) > 64 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func (buildRuntime *buildDeployRuntime) buildPackage(
	ctx context.Context,
	options buildDeployOptions,
	revision, version, binary, frontend, outputDir, temporaryRoot string,
) (string, string, error) {
	buildDir, err := os.MkdirTemp(temporaryRoot, "lmm-api-go-package.*")
	if err != nil {
		return "", "", err
	}
	defer os.RemoveAll(buildDir)
	pkgdest, err := os.MkdirTemp(temporaryRoot, "lmm-api-go-pkgdest.*")
	if err != nil {
		return "", "", err
	}
	defer os.RemoveAll(pkgdest)
	makepkgBuild := filepath.Join(buildDir, "makepkg")
	if err := os.Mkdir(makepkgBuild, 0o700); err != nil {
		return "", "", err
	}
	inputs := []struct {
		source      string
		destination string
		mode        fs.FileMode
	}{
		{filepath.Join(options.Repo, "packaging/local/lmm-api-go/PKGBUILD"), filepath.Join(buildDir, "PKGBUILD"), 0o644},
		{binary, filepath.Join(buildDir, backendGoName), 0o755},
		{filepath.Join(options.Repo, "packaging/common/lmm-api/lmm-api.service"), filepath.Join(buildDir, "lmm-api.service"), 0o644},
		{filepath.Join(options.Repo, "packaging/common/lmm-api/lmm-api-go.env"), filepath.Join(buildDir, "lmm-api-go.env"), 0o600},
		{filepath.Join(options.Repo, "packaging/common/lmm-api/lmm-api-operator.sysusers"), filepath.Join(buildDir, "lmm-api-operator.sysusers"), 0o644},
		{filepath.Join(options.Repo, "packaging/common/lmm-api/lmm-api-operator.tmpfiles"), filepath.Join(buildDir, "lmm-api-operator.tmpfiles"), 0o644},
		{filepath.Join(options.Repo, "packaging/common/lmm-api/lmm-api-operator.sudoers"), filepath.Join(buildDir, "lmm-api-operator.sudoers"), 0o440},
		{filepath.Join(options.Repo, "LICENSE"), filepath.Join(buildDir, "LICENSE"), 0o644},
		{filepath.Join(options.Repo, "NOTICE"), filepath.Join(buildDir, "NOTICE"), 0o644},
		{filepath.Join(options.Repo, "THIRD-PARTY-LICENSES.md"), filepath.Join(buildDir, "THIRD-PARTY-LICENSES.md"), 0o644},
		{filepath.Join(options.Repo, "packaging/common/lmm-api/edge-policy/nginx/http-map.conf"), filepath.Join(buildDir, "nginx-http-map.conf"), 0o644},
		{filepath.Join(options.Repo, "packaging/common/lmm-api/edge-policy/nginx/lmm-api-locations.conf"), filepath.Join(buildDir, "nginx-locations.conf"), 0o644},
		{filepath.Join(options.Repo, "packaging/common/lmm-api/edge-policy/nginx/mime.types"), filepath.Join(buildDir, "nginx-mime.types"), 0o644},
		{filepath.Join(options.Repo, "packaging/common/lmm-api/edge-policy/nginx/new-api.conf"), filepath.Join(buildDir, "nginx-new-api.conf"), 0o644},
		{filepath.Join(options.Repo, "packaging/common/lmm-api/edge-policy/nginx/lmm-api-region-policy.conf"), filepath.Join(buildDir, "nginx-region-policy.conf"), 0o644},
		{filepath.Join(options.Repo, "packaging/common/lmm-api/geoip2-country-update.service"), filepath.Join(buildDir, "geoip2-country-update.service"), 0o644},
		{filepath.Join(options.Repo, "packaging/common/lmm-api/geoip2-country-update.timer"), filepath.Join(buildDir, "geoip2-country-update.timer"), 0o644},
	}
	for _, input := range inputs {
		if err := copyRegularFile(input.source, input.destination, input.mode, true); err != nil {
			return "", "", fmt.Errorf("stage package input %s: %w", filepath.Base(input.source), err)
		}
	}
	if err := writeAtomicRegularFile(filepath.Join(buildDir, "REVISION"), []byte(revision+"\n"), 0o644); err != nil {
		return "", "", err
	}
	if err := writeDeterministicTar(filepath.Join(buildDir, "frontend-dist.tar"), frontend); err != nil {
		return "", "", fmt.Errorf("archive frontend dist: %w", err)
	}
	makepkgEnvironment := append(os.Environ(),
		"BUILDDIR="+makepkgBuild,
		"PKGDEST="+pkgdest,
		"LMM_API_PKGVER="+version,
		"LMM_API_PKGREL=1",
	)
	if _, err := buildRuntime.runner.Run(ctx, productionCommand{Name: commandMakepkg, Args: []string{"--force", "--nodeps", "--noconfirm", "--cleanbuild"},
		Dir: buildDir, Env: makepkgEnvironment, Timeout: buildCommandTimeout}); err != nil {
		return "", "", fmt.Errorf("build Arch package: %w", err)
	}
	entries, err := os.ReadDir(pkgdest)
	if err != nil {
		return "", "", err
	}
	packageArchitecture, err := archPackageArchitecture()
	if err != nil {
		return "", "", err
	}
	prefix := productionAURPackageName + "-" + version + "-1-" + packageArchitecture + ".pkg.tar."
	var sourcePackage string
	for _, entry := range entries {
		if entry.Type().IsRegular() && strings.HasPrefix(entry.Name(), prefix) && !strings.HasSuffix(entry.Name(), ".sha256") {
			if sourcePackage != "" {
				return "", "", errors.New("makepkg produced more than one candidate package")
			}
			sourcePackage = filepath.Join(pkgdest, entry.Name())
		}
	}
	if sourcePackage == "" {
		return "", "", errors.New("makepkg did not produce the expected candidate package")
	}
	destination := filepath.Join(outputDir, filepath.Base(sourcePackage))
	if _, err := os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
		return "", "", errors.New("candidate package destination already exists")
	}
	if err := copyRegularFile(sourcePackage, destination, 0o644, true); err != nil {
		return "", "", err
	}
	digest, err := sha256File(destination)
	if err != nil {
		return "", "", err
	}
	identity, err := buildRuntime.runner.Run(ctx, productionCommand{Name: commandPacman, Args: []string{"-Qp", destination}})
	if err != nil || strings.TrimSpace(string(identity)) != productionAURPackageName+" "+version+"-1" {
		return "", "", errors.New("built Arch package identity mismatch")
	}
	checksumPath := destination + ".sha256"
	if err := writeAtomicRegularFile(checksumPath, []byte(digest+"  "+filepath.Base(destination)+"\n"), 0o644); err != nil {
		return "", "", err
	}
	return destination, digest, nil
}

func archPackageArchitecture() (string, error) {
	switch runtime.GOARCH {
	case "amd64":
		return "x86_64", nil
	case "arm64":
		return "aarch64", nil
	default:
		return "", fmt.Errorf("unsupported Arch package architecture: %s", runtime.GOARCH)
	}
}

func writeDeterministicTar(destination, source string) (returnErr error) {
	if err := validateFrontendTree(source); err != nil {
		return err
	}
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		closeErr := output.Close()
		if returnErr == nil && closeErr != nil {
			returnErr = closeErr
		}
		if returnErr != nil {
			_ = os.Remove(destination)
		}
	}()
	writer := tar.NewWriter(output)
	defer func() {
		closeErr := writer.Close()
		if returnErr == nil && closeErr != nil {
			returnErr = closeErr
		}
	}()
	epoch := time.Unix(0, 0).UTC()
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
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("frontend archive contains symlink: %s", path)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(relative)
		header.Uid = 0
		header.Gid = 0
		header.Uname = ""
		header.Gname = ""
		header.ModTime = epoch
		header.AccessTime = time.Time{}
		header.ChangeTime = time.Time{}
		if entry.IsDir() {
			header.Mode = 0o755
			header.Name += "/"
		} else if entry.Type().IsRegular() {
			header.Mode = 0o644
		} else {
			return fmt.Errorf("frontend archive contains unsupported entry: %s", path)
		}
		if err := writer.WriteHeader(header); err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(writer, input)
		closeErr := input.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
}
