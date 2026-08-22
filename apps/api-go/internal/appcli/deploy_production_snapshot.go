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
	"sort"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

type productionWorkspaceResult struct {
	DeploymentID   string `json:"deployment_id"`
	Workspace      string `json:"workspace"`
	Transaction    string `json:"transaction_lock"`
	TransactionSet bool   `json:"transaction_active"`
}

type productionBackupOptions struct {
	Workspace       string
	RollbackPackage string
	RollbackSHA256  string
	CandidateSHA256 string
	ExpectedVersion string
	GitRevision     string
}

type productionBackupResult struct {
	DeploymentID      string `json:"deployment_id"`
	BackupDir         string `json:"backup_dir"`
	FrontendRelease   string `json:"frontend_release"`
	RollbackPackage   string `json:"rollback_package"`
	RollbackSHA256    string `json:"rollback_sha256"`
	DatabaseEngine    string `json:"database_engine"`
	ConfigurationMode string `json:"configuration_mode"`
}

type productionPackageResult struct {
	Package       string `json:"package"`
	PackageSHA256 string `json:"package_sha256"`
	Identity      string `json:"identity"`
	Source        string `json:"source"`
}

type productionArchiveRoot struct {
	Source   string
	Prefix   string
	DirMode  fs.FileMode
	FileMode fs.FileMode
}

func runProductionWorkspace(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintf(stderr, "%s deploy production workspace: choose create, abort, or cleanup\n", ProgramName)
		return ExitUsage
	}
	if args[0] == "cleanup" {
		flags := flag.NewFlagSet("deploy production workspace cleanup", flag.ContinueOnError)
		flags.SetOutput(stderr)
		retention := productionWorkspaceCleanupRetention
		execute := false
		flags.DurationVar(&retention, "older-than", retention, "only clean terminal workspaces older than this duration")
		flags.BoolVar(&execute, "execute", false, "remove eligible disposable children; default is a dry-run preview")
		flags.Usage = func() { writeDeployUsage(stderr) }
		if err := flags.Parse(args[1:]); errors.Is(err, flag.ErrHelp) {
			return ExitOK
		} else if err != nil || flags.NArg() != 0 {
			return ExitUsage
		}
		runtime := defaultProductionRuntime()
		result, err := runtime.cleanupWorkspaces(context.Background(), productionWorkspaceCleanupOptions{OlderThan: retention, Execute: execute})
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "%s deploy production workspace cleanup: %v\n", ProgramName, err)
			return ExitError
		}
		return writeJSONCommandResult(result, stdout, stderr, "production workspace cleanup")
	}
	if args[0] == "abort" {
		flags := flag.NewFlagSet("deploy production workspace abort", flag.ContinueOnError)
		flags.SetOutput(stderr)
		workspacePath := ""
		flags.StringVar(&workspacePath, "workspace", "", "marker-owned target deployment workspace")
		if err := flags.Parse(args[1:]); errors.Is(err, flag.ErrHelp) {
			return ExitOK
		} else if err != nil || flags.NArg() != 0 {
			return ExitUsage
		}
		clean, err := cleanAbsoluteNonRoot(workspacePath)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "%s deploy production workspace abort: invalid --workspace: %v\n", ProgramName, err)
			return ExitUsage
		}
		runtime := defaultProductionRuntime()
		status, err := runtime.abortWorkspace(context.Background(), clean)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "%s deploy production workspace abort: %v\n", ProgramName, err)
			return ExitError
		}
		return writeJSONCommandResult(status, stdout, stderr, "production workspace abort")
	}
	if args[0] != "create" {
		_, _ = fmt.Fprintf(stderr, "%s deploy production workspace: choose create, abort, or cleanup\n", ProgramName)
		return ExitUsage
	}
	flags := flag.NewFlagSet("deploy production workspace create", flag.ContinueOnError)
	flags.SetOutput(stderr)
	deploymentID := ""
	flags.StringVar(&deploymentID, "deployment-id", "", "unique release-scoped deployment ID")
	flags.Usage = func() { writeDeployUsage(stderr) }
	if err := flags.Parse(args[1:]); errors.Is(err, flag.ErrHelp) {
		return ExitOK
	} else if err != nil {
		return ExitUsage
	}
	if flags.NArg() != 0 || !productionIDPattern.MatchString(deploymentID) {
		_, _ = fmt.Fprintf(stderr, "%s deploy production workspace create: valid --deployment-id is required\n", ProgramName)
		return ExitUsage
	}
	runtime := defaultProductionRuntime()
	result, err := runtime.createWorkspace(context.Background(), deploymentID)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s deploy production workspace create: %v\n", ProgramName, err)
		return ExitError
	}
	return writeJSONCommandResult(result, stdout, stderr, "production workspace create")
}

func runProductionPackage(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 || args[0] != "current" {
		_, _ = fmt.Fprintf(stderr, "%s deploy production package: choose current\n", ProgramName)
		return ExitUsage
	}
	runtime := defaultProductionRuntime()
	result, err := runtime.currentPackage(context.Background())
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s deploy production package current: %v\n", ProgramName, err)
		return ExitError
	}
	return writeJSONCommandResult(result, stdout, stderr, "production package current")
}

func runProductionBackup(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintf(stderr, "%s deploy production backup: choose create, export, or attest\n", ProgramName)
		return ExitUsage
	}
	switch args[0] {
	case "export":
		return runProductionBackupExport(args[1:], stdout, stderr)
	case "verify":
		return runProductionBackupVerify(args[1:], stdout, stderr)
	case "attest":
		return runProductionBackupAttest(args[1:], stdout, stderr)
	case "create":
	default:
		_, _ = fmt.Fprintf(stderr, "%s deploy production backup: choose create, export, or attest\n", ProgramName)
		return ExitUsage
	}
	options, err := parseProductionBackupOptions(args[1:], stderr)
	if errors.Is(err, flag.ErrHelp) {
		return ExitOK
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s deploy production backup: %v\n", ProgramName, err)
		return ExitUsage
	}
	runtime := defaultProductionRuntime()
	result, err := runtime.createBackup(context.Background(), options)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s deploy production backup: %v\n", ProgramName, err)
		return ExitError
	}
	return writeJSONCommandResult(result, stdout, stderr, "production backup")
}

func writeJSONCommandResult(value any, stdout, stderr io.Writer, label string) int {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s deploy %s: encode result: %v\n", ProgramName, label, err)
		return ExitError
	}
	_, _ = stdout.Write(append(encoded, '\n'))
	return ExitOK
}

func parseProductionBackupOptions(args []string, stderr io.Writer) (productionBackupOptions, error) {
	options := productionBackupOptions{}
	flags := flag.NewFlagSet("deploy production backup", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&options.Workspace, "workspace", "", "marker-owned target deployment workspace")
	flags.StringVar(&options.RollbackPackage, "rollback-package", "", "captured package matching installed lmm-api-go")
	flags.StringVar(&options.RollbackSHA256, "rollback-sha256", "", "rollback package SHA-256")
	flags.StringVar(&options.CandidateSHA256, "candidate-sha256", "", "candidate package SHA-256")
	flags.StringVar(&options.ExpectedVersion, "expected-version", "", "candidate release version")
	flags.StringVar(&options.GitRevision, "git-revision", "", "candidate Git revision")
	flags.Usage = func() { writeDeployUsage(stderr) }
	if err := flags.Parse(args); err != nil {
		return productionBackupOptions{}, err
	}
	if flags.NArg() != 0 {
		return productionBackupOptions{}, errors.New("unexpected positional arguments")
	}
	for label, value := range map[string]string{
		"--workspace": options.Workspace, "--rollback-package": options.RollbackPackage,
		"--rollback-sha256": options.RollbackSHA256, "--candidate-sha256": options.CandidateSHA256,
		"--expected-version": options.ExpectedVersion, "--git-revision": options.GitRevision,
	} {
		if value == "" {
			return productionBackupOptions{}, fmt.Errorf("%s is required", label)
		}
	}
	var err error
	options.Workspace, err = cleanAbsoluteNonRoot(options.Workspace)
	if err != nil {
		return productionBackupOptions{}, fmt.Errorf("invalid --workspace: %w", err)
	}
	options.RollbackPackage, err = cleanAbsoluteNonRoot(options.RollbackPackage)
	if err != nil {
		return productionBackupOptions{}, fmt.Errorf("invalid --rollback-package: %w", err)
	}
	if !productionSHA256Pattern.MatchString(options.RollbackSHA256) || !productionSHA256Pattern.MatchString(options.CandidateSHA256) {
		return productionBackupOptions{}, errors.New("backup SHA-256 values must be 64 lowercase hexadecimal characters")
	}
	if !productionVersionPattern.MatchString(options.ExpectedVersion) || !regexpGitRevision(options.GitRevision) {
		return productionBackupOptions{}, errors.New("backup release version or Git revision is invalid")
	}
	return options, nil
}

func (runtime *productionRuntime) assertProductionMutation() error {
	if runtime.effectiveUID() != 0 {
		return errors.New("must run as root")
	}
	hostname, err := runtime.hostname()
	if err != nil {
		return fmt.Errorf("read production host identity: %w", err)
	}
	if hostname != runtime.paths.ExpectedHost {
		return fmt.Errorf("production host identity mismatch: got %q", hostname)
	}
	return nil
}

func (runtime *productionRuntime) withGlobalLock(ctx context.Context, operation func() error) error {
	lock, err := runtime.acquireGlobalLock(ctx)
	if err != nil {
		return err
	}
	defer func() {
		_ = unix.Flock(int(lock.Fd()), unix.LOCK_UN)
		_ = lock.Close()
	}()
	return operation()
}

func (runtime *productionRuntime) createWorkspace(ctx context.Context, deploymentID string) (productionWorkspaceResult, error) {
	if err := runtime.assertProductionMutation(); err != nil {
		return productionWorkspaceResult{}, err
	}
	if !productionIDPattern.MatchString(deploymentID) {
		return productionWorkspaceResult{}, errors.New("invalid deployment ID")
	}
	var result productionWorkspaceResult
	err := runtime.withGlobalLock(ctx, func() (returnErr error) {
		if err := ensureRealDirectory(runtime.paths.WorkRoot, 0o700); err != nil {
			return fmt.Errorf("prepare production work root: %w", err)
		}
		if err := ensureRealDirectory(runtime.paths.BackupRoot, 0o700); err != nil {
			return fmt.Errorf("prepare production backup root: %w", err)
		}
		deployRoot := filepath.Dir(runtime.paths.WorkRoot)
		if deployRoot == string(filepath.Separator) || deployRoot != filepath.Dir(runtime.paths.BackupRoot) {
			return errors.New("production work and backup roots must share a dedicated parent")
		}
		if err := runtime.requireOwnedSafePath(deployRoot, true); err != nil {
			return fmt.Errorf("production deploy root must be root-owned and safe: %w", err)
		}
		for label, root := range map[string]string{"work": runtime.paths.WorkRoot, "backup": runtime.paths.BackupRoot} {
			canonical, err := filepath.EvalSymlinks(root)
			if err != nil || filepath.Clean(canonical) != filepath.Clean(root) {
				return fmt.Errorf("production %s root must not contain symlink components", label)
			}
			if err := runtime.requireOwnedSafePath(root, true); err != nil {
				return fmt.Errorf("production %s root must be root-owned and safe: %w", label, err)
			}
			info, err := os.Lstat(root)
			if err != nil {
				return fmt.Errorf("inspect production %s root: %w", label, err)
			}
			mode := info.Mode().Perm()
			if (label == "backup" && mode != 0o700) || (label == "work" && mode != 0o700 && mode != 0o710) {
				return fmt.Errorf("production %s root has unsafe permissions", label)
			}
		}
		workspace := filepath.Join(runtime.paths.WorkRoot, deploymentID)
		if _, err := os.Lstat(workspace); !errors.Is(err, os.ErrNotExist) {
			return errors.New("release-scoped production workspace already exists or is unsafe")
		}
		if _, err := os.Lstat(runtime.paths.TransactionLock); !errors.Is(err, os.ErrNotExist) {
			return errors.New("another release owns the production transaction lock")
		}
		if err := os.Mkdir(workspace, 0o700); err != nil {
			return err
		}
		workspaceCreated := true
		transactionCreated := false
		defer func() {
			if returnErr == nil {
				return
			}
			if transactionCreated {
				_ = os.Remove(filepath.Join(runtime.paths.TransactionLock, productionTransactionMarker))
				_ = os.Remove(runtime.paths.TransactionLock)
			}
			if workspaceCreated {
				_ = os.RemoveAll(workspace)
			}
		}()
		for _, directory := range []string{"staging", "state"} {
			if err := os.Mkdir(filepath.Join(workspace, directory), 0o700); err != nil {
				return err
			}
		}
		marker := fmt.Sprintf("format=1\ndeployment_id=%s\nrole=target\ncreated_at_utc=%s\n", deploymentID, utcSecond(runtime.now()).Format(time.RFC3339))
		if err := writeAtomicRegularFile(filepath.Join(workspace, productionWorkspaceMarker), []byte(marker), 0o600); err != nil {
			return err
		}
		if err := os.Mkdir(runtime.paths.TransactionLock, 0o700); err != nil {
			return fmt.Errorf("claim production transaction lock: %w", err)
		}
		transactionCreated = true
		transaction := fmt.Sprintf("format=1\ndeployment_id=%s\nstatus=ACTIVE\n", deploymentID)
		if err := writeAtomicRegularFile(filepath.Join(runtime.paths.TransactionLock, productionTransactionMarker), []byte(transaction), 0o600); err != nil {
			return err
		}
		result = productionWorkspaceResult{
			DeploymentID: deploymentID, Workspace: workspace,
			Transaction: runtime.paths.TransactionLock, TransactionSet: true,
		}
		return nil
	})
	return result, err
}

func (runtime *productionRuntime) abortWorkspace(ctx context.Context, root string) (productionStatus, error) {
	if err := runtime.assertProductionMutation(); err != nil {
		return productionStatus{}, err
	}
	workspace, err := runtime.openWorkspace(root)
	if err != nil {
		return productionStatus{}, err
	}
	var status productionStatus
	err = runtime.withGlobalLock(ctx, func() error {
		if err := runtime.validateTransactionLock(workspace); err != nil {
			return err
		}
		if _, err := os.Lstat(workspace.manifestPath); !errors.Is(err, os.ErrNotExist) {
			return errors.New("deployment activation has started; refusing controller-side abort")
		}
		if existing, err := runtime.readStatus(workspace); err == nil {
			if existing.Phase == "ABORTED" {
				status = existing
				return runtime.releaseTransactionLock(workspace)
			}
			if existing.Phase != "PREPARING" && existing.Phase != "FAILED_PREARM" {
				return fmt.Errorf("deployment phase %s is not safe to abort", existing.Phase)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		status = productionStatus{Phase: "ABORTED", Reason: "controller-preapply-failure"}
		if err := runtime.writeStatus(workspace, status); err != nil {
			return err
		}
		return runtime.releaseTransactionLock(workspace)
	})
	return status, err
}

func (runtime *productionRuntime) currentPackage(ctx context.Context) (productionPackageResult, error) {
	if err := runtime.assertProductionMutation(); err != nil {
		return productionPackageResult{}, err
	}
	var result productionPackageResult
	err := runtime.withGlobalLock(ctx, func() error {
		packageName, installed, err := runtime.installedGoPackage(ctx)
		if err != nil {
			return fmt.Errorf("query installed Go package: %w", err)
		}
		type candidate struct {
			path   string
			source string
		}
		candidates := make([]candidate, 0)
		for _, root := range []struct {
			path   string
			source string
		}{{runtime.paths.ReleasePackages, "preserved-release"}, {runtime.paths.PackageCache, "pacman-cache"}} {
			entries, err := os.ReadDir(root.path)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return fmt.Errorf("read %s package directory: %w", root.source, err)
			}
			for _, entry := range entries {
				if !entry.Type().IsRegular() || !strings.HasPrefix(entry.Name(), packageName+"-") || !strings.Contains(entry.Name(), ".pkg.tar.") || strings.HasSuffix(entry.Name(), ".sig") {
					continue
				}
				path := filepath.Join(root.path, entry.Name())
				info, err := os.Lstat(path)
				if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() == 0 {
					return fmt.Errorf("candidate rollback package is unsafe: %s", path)
				}
				identity, err := runtime.runner.Run(ctx, productionCommand{Name: commandPacman, Args: []string{"-Qp", path}})
				if err == nil && strings.TrimSpace(string(identity)) == installed {
					candidates = append(candidates, candidate{path: path, source: root.source})
				}
			}
			if len(candidates) > 0 {
				break
			}
		}
		if len(candidates) == 0 {
			return errors.New("no exact package for the installed Go release is preserved")
		}
		sort.Slice(candidates, func(i, j int) bool { return candidates[i].path < candidates[j].path })
		firstDigest, err := sha256File(candidates[0].path)
		if err != nil {
			return err
		}
		for _, candidate := range candidates[1:] {
			digest, err := sha256File(candidate.path)
			if err != nil {
				return err
			}
			if digest != firstDigest {
				return errors.New("multiple different package files claim the installed Go package identity")
			}
		}
		result = productionPackageResult{
			Package: candidates[0].path, PackageSHA256: firstDigest,
			Identity: installed, Source: candidates[0].source,
		}
		return nil
	})
	return result, err
}

func (runtime *productionRuntime) createBackup(ctx context.Context, options productionBackupOptions) (productionBackupResult, error) {
	if err := runtime.assertProductionMutation(); err != nil {
		return productionBackupResult{}, err
	}
	workspace, err := runtime.openWorkspace(options.Workspace)
	if err != nil {
		return productionBackupResult{}, err
	}
	var result productionBackupResult
	err = runtime.withGlobalLock(ctx, func() error {
		if err := runtime.validateTransactionLock(workspace); err != nil {
			return err
		}
		if err := runtime.validateStagedFile(workspace, options.RollbackPackage, options.RollbackSHA256, "rollback package"); err != nil {
			return err
		}
		packageName, installed, err := runtime.installedGoPackage(ctx)
		if err != nil {
			return fmt.Errorf("query installed Go package: %w", err)
		}
		rollbackIdentity, err := runtime.runner.Run(ctx, productionCommand{Name: commandPacman, Args: []string{"-Qp", options.RollbackPackage}})
		if err != nil || strings.TrimSpace(string(rollbackIdentity)) != installed {
			return errors.New("rollback package does not exactly match the installed Go package")
		}
		if _, err := runtime.runner.Run(ctx, productionCommand{Name: commandSystemctl, Args: []string{"is-active", "--quiet", runtime.paths.Service}}); err != nil {
			return errors.New("production Go service is not active before backup")
		}
		frontendRelease, err := currentFrontendRelease(runtime.paths.FrontendRoot)
		if err != nil {
			return fmt.Errorf("read current frontend release: %w", err)
		}
		environmentPath := filepath.Join(runtime.paths.ConfigDir, "lmm-api-go.env")
		environment, err := readPrivateRegularFile(environmentPath, 1<<20)
		if err != nil {
			return fmt.Errorf("read production environment: %w", err)
		}
		environmentValues, err := parseProductionEnvironment(environment)
		if err != nil {
			return err
		}
		databaseURL, childEnvironment, err := productionDatabaseCommand(environmentValues)
		if err != nil {
			return err
		}
		backupDir := filepath.Join(runtime.paths.BackupRoot, workspace.id)
		if _, err := os.Lstat(backupDir); !errors.Is(err, os.ErrNotExist) {
			return errors.New("release-scoped target backup already exists or is unsafe")
		}
		stage, err := os.MkdirTemp(runtime.paths.BackupRoot, "."+workspace.id+".*.stage")
		if err != nil {
			return fmt.Errorf("create backup stage: %w", err)
		}
		if err := os.Chmod(stage, 0o700); err != nil {
			_ = os.RemoveAll(stage)
			return err
		}
		published := false
		defer func() {
			if !published {
				_ = os.RemoveAll(stage)
			}
		}()

		rollbackCopy := filepath.Join(stage, "rollback.package")
		if err := copyRegularFile(options.RollbackPackage, rollbackCopy, 0o600, true); err != nil {
			return fmt.Errorf("copy rollback package into backup: %w", err)
		}
		applicationStage, err := os.MkdirTemp(workspace.stateDir, "application-backup.*")
		if err != nil {
			return err
		}
		defer os.RemoveAll(applicationStage)
		if err := os.Mkdir(filepath.Join(applicationStage, "metadata"), 0o700); err != nil {
			return err
		}
		if err := copyRegularFile(options.RollbackPackage, filepath.Join(applicationStage, "rollback.package"), 0o600, true); err != nil {
			return err
		}
		packageInfo, err := runtime.runner.Run(ctx, productionCommand{Name: commandPacman, Args: []string{"-Qi", packageName}})
		if err != nil {
			return fmt.Errorf("capture installed package metadata: %w", err)
		}
		serviceState, err := runtime.runner.Run(ctx, productionCommand{Name: commandSystemctl, Args: []string{"show", runtime.paths.Service, "--property=LoadState", "--property=ActiveState", "--property=SubState", "--property=UnitFileState"}})
		if err != nil {
			return fmt.Errorf("capture service state: %w", err)
		}
		if err := writeAtomicRegularFile(filepath.Join(applicationStage, "metadata", "package-info.txt"), packageInfo, 0o600); err != nil {
			return err
		}
		if err := writeAtomicRegularFile(filepath.Join(applicationStage, "metadata", "service-state.txt"), serviceState, 0o600); err != nil {
			return err
		}
		if info, err := os.Lstat(runtime.paths.DropInDir); err == nil {
			if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
				return errors.New("production systemd drop-in directory is unsafe")
			}
			if err := copyPrivateTree(runtime.paths.DropInDir, filepath.Join(applicationStage, "systemd-dropins")); err != nil {
				return err
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := writeTreeArchive(filepath.Join(stage, "application.archive"), []productionArchiveRoot{{
			Source: applicationStage, Prefix: ".", DirMode: 0o700, FileMode: 0o600,
		}}); err != nil {
			return fmt.Errorf("archive application rollback state: %w", err)
		}
		frontendSource := filepath.Join(runtime.paths.FrontendRoot, "releases", frontendRelease)
		if err := writeTreeArchive(filepath.Join(stage, "frontend.archive"), []productionArchiveRoot{{
			Source: frontendSource, Prefix: ".", DirMode: 0o755, FileMode: 0o644,
		}}); err != nil {
			return fmt.Errorf("archive current frontend: %w", err)
		}
		if err := writeTreeArchive(filepath.Join(stage, "configuration.archive"), []productionArchiveRoot{{
			Source: runtime.paths.ConfigDir, Prefix: "lmm-api-go", DirMode: 0o700, FileMode: 0o600,
		}}); err != nil {
			return fmt.Errorf("archive production configuration: %w", err)
		}
		databasePath := filepath.Join(stage, "database.archive")
		databaseTemporary := databasePath + ".new"
		if _, err := runtime.runner.Run(ctx, productionCommand{Name: commandPGDump, Args: []string{"--format=custom", "--file=" + databaseTemporary, databaseURL},
			Env: childEnvironment, Timeout: 10 * time.Minute, Sensitive: true}); err != nil {
			return fmt.Errorf("create PostgreSQL production backup: %w", err)
		}
		info, err := os.Lstat(databaseTemporary)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() == 0 {
			return errors.New("pg_dump did not produce a safe non-empty backup")
		}
		if err := os.Chmod(databaseTemporary, 0o600); err != nil {
			return err
		}
		if _, err := runtime.runner.Run(ctx, productionCommand{Name: commandPGRestore, Args: []string{"--list", databaseTemporary}}); err != nil {
			return fmt.Errorf("validate PostgreSQL production backup: %w", err)
		}
		if err := os.Rename(databaseTemporary, databasePath); err != nil {
			return err
		}
		createdAt := utcSecond(runtime.now()).Format(time.RFC3339)
		manifest := fmt.Sprintf(
			"format=1\ncreated_at_utc=%s\ndeployment_id=%s\nverified_host=%s\nrelease_id=%s\ngit_revision=%s\ncandidate_sha256=%s\nrollback_sha256=%s\nfrontend_release=%s\ndatabase_engine=postgres\n",
			createdAt, workspace.id, runtime.paths.ExpectedHost, options.ExpectedVersion, options.GitRevision,
			options.CandidateSHA256, options.RollbackSHA256, frontendRelease,
		)
		if err := writeAtomicRegularFile(filepath.Join(stage, "manifest.env"), []byte(manifest), 0o600); err != nil {
			return err
		}
		checksumNames := []string{
			"application.archive", "frontend.archive", "configuration.archive", "database.archive", "rollback.package",
		}
		var checksums strings.Builder
		for _, name := range checksumNames {
			digest, err := sha256File(filepath.Join(stage, name))
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(&checksums, "%s  %s\n", digest, name)
		}
		if err := writeAtomicRegularFile(filepath.Join(stage, "SHA256SUMS"), []byte(checksums.String()), 0o600); err != nil {
			return err
		}
		if err := os.Rename(stage, backupDir); err != nil {
			return fmt.Errorf("publish verified target backup: %w", err)
		}
		published = true
		if err := syncDirectory(runtime.paths.BackupRoot); err != nil {
			return err
		}
		result = productionBackupResult{
			DeploymentID: workspace.id, BackupDir: backupDir, FrontendRelease: frontendRelease,
			RollbackPackage: filepath.Join(backupDir, "rollback.package"), RollbackSHA256: options.RollbackSHA256,
			DatabaseEngine: "postgres", ConfigurationMode: "protected-plain-target-copy",
		}
		return nil
	})
	return result, err
}

func copyPrivateTree(source, destination string) error {
	if err := requireRealDirectory(source); err != nil {
		return err
	}
	if err := os.Mkdir(destination, 0o700); err != nil {
		return err
	}
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
			return fmt.Errorf("private tree contains symlink: %s", path)
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.Mkdir(target, 0o700)
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("private tree contains unsupported entry: %s", path)
		}
		return copyRegularFile(path, target, 0o600, true)
	})
}

func writeTreeArchive(destination string, roots []productionArchiveRoot) (returnErr error) {
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		_ = output.Close()
		if returnErr != nil {
			_ = os.Remove(destination)
		}
	}()
	writer := tar.NewWriter(output)
	epoch := time.Unix(0, 0).UTC()
	for _, root := range roots {
		if err := requireRealDirectory(root.Source); err != nil {
			return err
		}
		prefix := filepath.ToSlash(filepath.Clean(root.Prefix))
		if prefix == ".." || strings.HasPrefix(prefix, "../") || strings.HasPrefix(prefix, "/") {
			return errors.New("archive prefix is unsafe")
		}
		if err := filepath.WalkDir(root.Source, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("archive source contains symlink: %s", path)
			}
			relative, err := filepath.Rel(root.Source, path)
			if err != nil {
				return err
			}
			name := prefix
			if relative != "." {
				name = filepath.ToSlash(filepath.Join(prefix, relative))
			}
			if name == "." {
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			header, err := tar.FileInfoHeader(info, "")
			if err != nil {
				return err
			}
			header.Name = name
			header.Uid, header.Gid, header.Uname, header.Gname = 0, 0, "", ""
			header.ModTime, header.AccessTime, header.ChangeTime = epoch, time.Time{}, time.Time{}
			if entry.IsDir() {
				header.Mode = int64(root.DirMode.Perm())
				header.Name += "/"
			} else if entry.Type().IsRegular() {
				header.Mode = int64(root.FileMode.Perm())
			} else {
				return fmt.Errorf("archive source contains unsupported entry: %s", path)
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
		}); err != nil {
			return err
		}
	}
	if err := writer.Close(); err != nil {
		return err
	}
	if err := output.Sync(); err != nil {
		return err
	}
	return output.Close()
}
