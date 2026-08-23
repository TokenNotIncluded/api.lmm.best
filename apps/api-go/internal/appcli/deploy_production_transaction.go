package appcli

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type productionStagedFile struct {
	path       string
	digest     string
	label      string
	executable bool
}

func (runtime *productionRuntime) loadRollbackEnvironment(ctx context.Context, workspace productionWorkspace, backupDir string) ([]byte, error) {
	if backupDir == "" {
		content, err := readPrivateRegularFile(filepath.Join(runtime.paths.ConfigDir, "lmm-api-go.env"), 1<<20)
		if err != nil {
			return nil, fmt.Errorf("read live environment for rollback state: %w", err)
		}
		return content, nil
	}
	content, err := runtime.validateBackupSet(ctx, workspace, backupDir)
	if err != nil {
		return nil, err
	}
	if err := validateBackupAttestation(backupDir, workspace.id); err != nil {
		return nil, err
	}
	return content, nil
}

func (runtime *productionRuntime) validateOperatorWorkspace(ctx context.Context, workspace productionWorkspace, userName string, files []productionStagedFile) error {
	return runtime.prepareOperatorWorkspacePermissions(ctx, workspace, userName, files, false)
}

func (runtime *productionRuntime) prepareOperatorWorkspace(ctx context.Context, workspace productionWorkspace, userName string, files []productionStagedFile) error {
	return runtime.prepareOperatorWorkspacePermissions(ctx, workspace, userName, files, true)
}

func (runtime *productionRuntime) prepareOperatorWorkspacePermissions(ctx context.Context, workspace productionWorkspace, userName string, files []productionStagedFile, mutate bool) error {
	if userName != productionOperatorUser {
		return errors.New("operator user is not the package-owned deployment account")
	}
	uidOutput, err := runtime.runner.Run(ctx, productionCommand{Name: commandID, Args: []string{"-u", userName}})
	if err != nil {
		return errors.New("operator user does not exist")
	}
	uid, err := strconv.ParseUint(strings.TrimSpace(string(uidOutput)), 10, 32)
	if err != nil || uid == 0 {
		return errors.New("operator user must have uid greater than zero")
	}
	gidOutput, err := runtime.runner.Run(ctx, productionCommand{Name: commandID, Args: []string{"-g", userName}})
	if err != nil {
		return errors.New("operator primary group is unavailable")
	}
	gid, err := strconv.Atoi(strings.TrimSpace(string(gidOutput)))
	if err != nil || gid < 0 || uint64(gid) > uint64(math.MaxUint32) {
		return errors.New("operator primary group is invalid")
	}
	deployRoot := filepath.Dir(runtime.paths.WorkRoot)
	if deployRoot == string(filepath.Separator) || deployRoot != filepath.Dir(runtime.paths.BackupRoot) {
		return errors.New("production work and backup roots must share a dedicated parent")
	}
	type operatorPath struct {
		path      string
		mode      os.FileMode
		directory bool
	}
	paths := []operatorPath{
		{deployRoot, 0o710, true}, {runtime.paths.WorkRoot, 0o710, true}, {workspace.root, 0o710, true},
		{filepath.Join(workspace.root, productionWorkspaceMarker), 0o640, false}, {workspace.stagingDir, 0o750, true},
	}
	for _, file := range files {
		if !pathWithinRoot(workspace.stagingDir, file.path) || filepath.Dir(file.path) != workspace.stagingDir {
			return fmt.Errorf("operator payload path escapes staging: %s", file.path)
		}
		mode := os.FileMode(0o640)
		if file.executable {
			mode = 0o750
		}
		paths = append(paths, operatorPath{file.path, mode, false})
	}
	// Validate every path and root-only state before the first chmod/chown.
	for _, item := range paths {
		info, err := os.Lstat(item.path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o022 != 0 || item.directory != info.IsDir() {
			return fmt.Errorf("operator payload path is missing, writable, or unsafe: %s", item.path)
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != runtime.requiredOwnerUID || (!item.directory && stat.Nlink != 1) {
			return fmt.Errorf("operator payload path ownership or link count is unsafe: %s", item.path)
		}
		canonical, err := filepath.EvalSymlinks(item.path)
		if err != nil || filepath.Clean(canonical) != filepath.Clean(item.path) {
			return fmt.Errorf("operator payload path has a symlink component: %s", item.path)
		}
	}
	for label, path := range map[string]string{"state": workspace.stateDir, "backups": runtime.paths.BackupRoot, "transaction": runtime.paths.TransactionLock} {
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
			return fmt.Errorf("deployment %s directory must remain root-only", label)
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		canonical, canonicalErr := filepath.EvalSymlinks(path)
		if !ok || stat.Uid != runtime.requiredOwnerUID || canonicalErr != nil || filepath.Clean(canonical) != filepath.Clean(path) {
			return fmt.Errorf("deployment %s directory ownership or path is unsafe", label)
		}
	}
	if mutate {
		for _, item := range paths {
			if err := os.Chown(item.path, int(runtime.requiredOwnerUID), gid); err != nil {
				return fmt.Errorf("assign operator staging group: %w", err)
			}
			if err := os.Chmod(item.path, item.mode); err != nil {
				return fmt.Errorf("set operator staging permissions: %w", err)
			}
		}
	}
	return nil
}

func validateChangedIdentity(changed bool, candidate, rollback productionPackageMetadata, candidateSHA, rollbackSHA string) error {
	equal := candidate.Identity == rollback.Identity && candidate.GitRevision == rollback.GitRevision &&
		candidate.ContractRevision == rollback.ContractRevision && candidateSHA == rollbackSHA
	if !changed && !equal {
		return errors.New("unchanged flag requires byte-identical package identities")
	}
	if changed && equal {
		return errors.New("changed flag requires a distinct candidate identity")
	}
	return nil
}

func transitionFromMetadata(changed bool, candidatePath, rollbackPath, candidateSHA, rollbackSHA string, candidate, rollback productionPackageMetadata) productionPackageTransition {
	return productionPackageTransition{
		CandidatePackageName: candidate.Name, RollbackPackageName: rollback.Name,
		Changed: changed, CandidatePath: candidatePath, RollbackPath: rollbackPath,
		CandidateIdentity: candidate.Identity, RollbackIdentity: rollback.Identity,
		CandidateSHA256: candidateSHA, RollbackSHA256: rollbackSHA,
		CandidateGitRevision: candidate.GitRevision, RollbackGitRevision: rollback.GitRevision,
		CandidateContractRevision: candidate.ContractRevision, RollbackContractRevision: rollback.ContractRevision,
	}
}

func frontendTargetFor(metadata productionPackageMetadata) string {
	return "releases/" + strings.ReplaceAll(metadata.Version, ":", "-") + ".g" + metadata.GitRevision[:12]
}

func currentFrontendTarget(root string) (string, error) {
	target, err := os.Readlink(filepath.Join(root, "current"))
	if err != nil || !strings.HasPrefix(target, "releases/") || !releaseIDPattern.MatchString(strings.TrimPrefix(target, "releases/")) {
		return "", errors.New("active frontend symlink target is unsafe")
	}
	return target, nil
}

func verifyFrontendIdentity(root, target, digest string) error {
	current, err := currentFrontendTarget(root)
	if err != nil || current != target {
		return errors.New("active frontend symlink target mismatch")
	}
	actual, err := sha256File(filepath.Join(root, "current", "index.html"))
	if err != nil || actual != digest {
		return errors.New("active frontend index SHA-256 mismatch")
	}
	return nil
}

func changedPackagePaths(manifest productionManifest, rollback bool) []string {
	paths := make([]string, 0, 2)
	for _, transition := range []productionPackageTransition{manifest.Go, manifest.Web} {
		if !transition.Changed {
			continue
		}
		if rollback {
			paths = append(paths, transition.RollbackPath)
		} else {
			paths = append(paths, transition.CandidatePath)
		}
	}
	return paths
}

func (runtime *productionRuntime) validateParuPackagePath(workspace productionWorkspace, packagePath string) error {
	if workspace.root != filepath.Join(runtime.paths.WorkRoot, workspace.id) || filepath.Dir(packagePath) != workspace.stagingDir ||
		packagePath != filepath.Join(workspace.stagingDir, filepath.Base(packagePath)) || !productionPackageFilenamePattern.MatchString(filepath.Base(packagePath)) {
		return errors.New("paru package path is not an exact release-scoped Go/Web archive")
	}
	info, err := os.Lstat(packagePath)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o022 != 0 {
		return errors.New("paru package must be a non-writable regular file")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != runtime.requiredOwnerUID || stat.Nlink != 1 {
		return errors.New("paru package must be root-owned with exactly one link")
	}
	canonical, err := filepath.EvalSymlinks(packagePath)
	if err != nil || canonical != packagePath {
		return errors.New("paru package path contains a symlink component")
	}
	return nil
}

func (runtime *productionRuntime) preflightParuInstall(ctx context.Context, workspace productionWorkspace, userName, packagePath string) error {
	if userName != productionOperatorUser {
		return errors.New("paru operator is not the package-owned deployment account")
	}
	if err := runtime.validateParuPackagePath(workspace, packagePath); err != nil {
		return err
	}
	uidOutput, err := runtime.runner.Run(ctx, productionCommand{Name: commandID, Args: []string{"-u", userName}})
	uid, parseErr := strconv.ParseUint(strings.TrimSpace(string(uidOutput)), 10, 32)
	if err != nil || parseErr != nil || uid == 0 {
		return errors.New("paru operator is missing or no longer unprivileged")
	}
	args := []string{"--user", userName, "--", commandSudo, "-n", "-l", "--", commandPacman, "--upgrade", "--noconfirm", "--", packagePath}
	if _, err := runtime.runner.Run(ctx, productionCommand{Name: commandRunuser, Args: args}); err != nil {
		return fmt.Errorf("operator lacks exact non-interactive pacman privilege for %s: %w", filepath.Base(packagePath), err)
	}
	return nil
}

func (runtime *productionRuntime) paruInstall(ctx context.Context, workspace productionWorkspace, userName, packagePath string) error {
	if err := runtime.preflightParuInstall(ctx, workspace, userName, packagePath); err != nil {
		return err
	}
	args := []string{"--user", userName, "--", runtime.paths.ParuBinary, "-U", "--noconfirm", "--", packagePath}
	_, err := runtime.runner.Run(ctx, productionCommand{Name: commandRunuser, Args: args, Timeout: 5 * time.Minute})
	return err
}

func (runtime *productionRuntime) verifyManifestArchives(ctx context.Context, manifest productionManifest) error {
	restoredEnvironment, err := readPrivateRegularFile(filepath.Join(manifest.ConfigRestorePath, "lmm-api-go.env"), 1<<20)
	if err != nil || fmt.Sprintf("%x", sha256Bytes(restoredEnvironment)) != manifest.EnvironmentRestoreSHA256 {
		return errors.New("configuration rollback snapshot no longer matches the deployment manifest")
	}
	if manifest.BackupsEnabled {
		if err := validateBackupAttestation(manifest.BackupDir, manifest.DeploymentID); err != nil {
			return err
		}
		backupDigest, err := sha256File(filepath.Join(manifest.BackupDir, "database.archive"))
		if err != nil || backupDigest != manifest.DatabaseBackupSHA256 {
			return errors.New("optional manifest backup no longer matches the bound evidence")
		}
	}
	pairs := []struct {
		transition          productionPackageTransition
		candidate, rollback productionPackageMetadata
	}{
		{transition: manifest.Go}, {transition: manifest.Web},
	}
	for index := range pairs {
		pair := &pairs[index]
		var err error
		pair.candidate, err = runtime.packageMetadata(ctx, pair.transition.CandidatePath, pair.transition.CandidatePackageName)
		if err != nil {
			return err
		}
		pair.rollback, err = runtime.packageMetadata(ctx, pair.transition.RollbackPath, pair.transition.RollbackPackageName)
		if err != nil {
			return err
		}
		if pair.candidate.Identity != pair.transition.CandidateIdentity || pair.rollback.Identity != pair.transition.RollbackIdentity ||
			pair.candidate.GitRevision != pair.transition.CandidateGitRevision || pair.rollback.GitRevision != pair.transition.RollbackGitRevision ||
			pair.candidate.ContractRevision != pair.transition.CandidateContractRevision || pair.rollback.ContractRevision != pair.transition.RollbackContractRevision {
			return fmt.Errorf("%s manifest metadata does not match staged package archives", pair.transition.CandidatePackageName)
		}
	}
	if pairs[0].candidate.BinarySHA256 != manifest.ProbeBinarySHA256 || pairs[1].candidate.IndexSHA256 != manifest.Frontend.NewIndexSHA256 || pairs[1].rollback.IndexSHA256 != manifest.Frontend.OldIndexSHA256 {
		return errors.New("manifest binary or frontend hash does not match staged package archives")
	}
	return nil
}

func (runtime *productionRuntime) verifyTransitionInstalled(ctx context.Context, transition productionPackageTransition, rollback, verifyMemory bool) error {
	name, identity, revision, contract := transition.CandidatePackageName, transition.CandidateIdentity, transition.CandidateGitRevision, transition.CandidateContractRevision
	if rollback {
		name, identity, revision, contract = transition.RollbackPackageName, transition.RollbackIdentity, transition.RollbackGitRevision, transition.RollbackContractRevision
	}
	if err := runtime.verifyInstalledPackage(ctx, name, identity); err != nil {
		return err
	}
	if verifyMemory {
		if err := runtime.verifyMemoryPackageOwner(ctx, identity); err != nil {
			return err
		}
	}
	actualRevision, actualContract, err := runtime.readInstalledReleaseMetadata(name, identity)
	if err != nil || actualRevision != revision || actualContract != contract {
		return fmt.Errorf("installed %s Git/contract metadata mismatch", name)
	}
	return nil
}

func (runtime *productionRuntime) verifyManifestInstalled(ctx context.Context, manifest productionManifest, rollback bool) error {
	if err := runtime.verifyTransitionInstalled(ctx, manifest.Go, rollback, true); err != nil {
		return err
	}
	return runtime.verifyTransitionInstalled(ctx, manifest.Web, rollback, false)
}

func (runtime *productionRuntime) apply(ctx context.Context, workspace productionWorkspace, options productionTransactionOptions) (result productionStatus, returnErr error) {
	transactionCtx := ctx
	if !options.GoChanged && !options.WebChanged {
		return productionStatus{}, errors.New("at least one of --go-changed or --web-changed is required")
	}
	if options.WithBackups != (options.BackupDir != "") {
		return productionStatus{}, errors.New("optional business backups require both --with-backups and --backup-dir")
	}
	if _, err := os.Lstat(workspace.manifestPath); !errors.Is(err, os.ErrNotExist) {
		return productionStatus{}, errors.New("deployment manifest already exists")
	}
	if _, err := os.Lstat(workspace.statusPath); !errors.Is(err, os.ErrNotExist) {
		return productionStatus{}, errors.New("deployment status already exists")
	}
	if err := runtime.validateTransactionLock(workspace); err != nil {
		return productionStatus{}, err
	}
	if err := runtime.writeStatus(workspace, productionStatus{Phase: "PREPARING", Version: options.ExpectedVersion}); err != nil {
		return productionStatus{}, err
	}
	armed, awaitingConfirmation := false, false
	watchdogDeadline := time.Time{}
	defer func() {
		if returnErr == nil || awaitingConfirmation {
			return
		}
		if armed {
			if errors.Is(returnErr, context.DeadlineExceeded) || (!watchdogDeadline.IsZero() && !runtime.now().Before(watchdogDeadline)) {
				returnErr = errors.Join(returnErr, errors.New("fixed deployment deadline reached; persistent systemd watchdog owns rollback"))
				return
			}
			if _, rollbackErr := runtime.rollback(transactionCtx, workspace, "activation-failure"); rollbackErr != nil {
				returnErr = errors.Join(returnErr, fmt.Errorf("automatic rollback failed: %w", rollbackErr))
			}
			return
		}
		_ = os.Remove(workspace.probeToken)
		_ = runtime.writeStatus(workspace, productionStatus{Phase: "FAILED_PREARM", Version: options.ExpectedVersion, Reason: "activation-preparation-failed"})
		_ = runtime.releaseTransactionLock(workspace)
	}()

	staged := []productionStagedFile{
		{options.GoPackage, options.GoPackageSHA256, "candidate Go package", false},
		{options.GoRollbackPackage, options.GoRollbackSHA256, "rollback Go package", false},
		{options.WebPackage, options.WebPackageSHA256, "candidate Web package", false},
		{options.WebRollbackPackage, options.WebRollbackSHA256, "rollback Web package", false},
		{options.ProbeBinary, options.ProbeBinarySHA256, "probe binary", true},
	}
	if err := runtime.validateOperatorWorkspace(ctx, workspace, options.OperatorUser, staged); err != nil {
		return productionStatus{}, err
	}
	for _, file := range staged {
		if err := runtime.validateStagedFile(workspace, file.path, file.digest, file.label); err != nil {
			return productionStatus{}, err
		}
	}
	preflightPackages := make([]string, 0, 4)
	if options.GoChanged {
		preflightPackages = append(preflightPackages, options.GoPackage, options.GoRollbackPackage)
	}
	if options.WebChanged {
		preflightPackages = append(preflightPackages, options.WebPackage, options.WebRollbackPackage)
	}
	for _, packagePath := range preflightPackages {
		if err := runtime.preflightParuInstall(ctx, workspace, options.OperatorUser, packagePath); err != nil {
			return productionStatus{}, err
		}
	}

	archivedEnvironment, err := runtime.loadRollbackEnvironment(ctx, workspace, options.BackupDir)
	if err != nil {
		return productionStatus{}, err
	}
	databaseBackupSHA256 := ""
	if options.BackupDir != "" {
		databaseBackupSHA256, err = sha256File(filepath.Join(options.BackupDir, "database.archive"))
		if err != nil || !productionSHA256Pattern.MatchString(databaseBackupSHA256) {
			return productionStatus{}, errors.New("authorized optional database backup is missing or empty")
		}
	}
	if _, err := runtime.runner.Run(ctx, productionCommand{Name: commandSystemctl, Args: []string{"is-active", "--quiet", runtime.paths.Service}}); err != nil {
		return productionStatus{}, errors.New("pre-upgrade lmm-api service is not active")
	}
	if _, err := runtime.runner.Run(ctx, productionCommand{Name: commandSystemctl, Args: []string{"is-enabled", "--quiet", runtime.paths.Service}}); err != nil {
		return productionStatus{}, errors.New("pre-upgrade lmm-api service is not enabled")
	}
	if err := runtime.verifyCanonicalOperator(ctx); err != nil {
		return productionStatus{}, err
	}
	if err := verifyProductionMemoryDropIn(filepath.Join(runtime.paths.PackagedDropInDir, productionMemoryFileName)); err != nil {
		return productionStatus{}, err
	}
	goCandidate, err := runtime.packageMetadata(ctx, options.GoPackage, productionAURPackageName)
	if err != nil {
		return productionStatus{}, err
	}
	goRollback, err := runtime.packageMetadata(ctx, options.GoRollbackPackage, productionAURPackageName, productionSourcePackageName)
	if err != nil {
		return productionStatus{}, err
	}
	webCandidate, err := runtime.packageMetadata(ctx, options.WebPackage, productionWebPackageName)
	if err != nil {
		return productionStatus{}, err
	}
	webRollback, err := runtime.packageMetadata(ctx, options.WebRollbackPackage, productionWebPackageName)
	if err != nil {
		return productionStatus{}, err
	}
	if goCandidate.ContractRevision != webCandidate.ContractRevision {
		return productionStatus{}, errors.New("candidate Go API and Web route contract revisions differ")
	}
	if goRollback.ContractRevision != webRollback.ContractRevision {
		return productionStatus{}, errors.New("rollback Go API and Web route contract revisions differ")
	}
	if !productionPackageMatches(goCandidate.Version, options.ExpectedVersion) {
		return productionStatus{}, errors.New("candidate Go package version does not match --expected-version")
	}
	if goCandidate.BinarySHA256 != options.ProbeBinarySHA256 {
		return productionStatus{}, errors.New("candidate probe binary is not the binary contained in the Go package")
	}
	if err := validateChangedIdentity(options.GoChanged, goCandidate, goRollback, options.GoPackageSHA256, options.GoRollbackSHA256); err != nil {
		return productionStatus{}, fmt.Errorf("Go package pair: %w", err)
	}
	if err := validateChangedIdentity(options.WebChanged, webCandidate, webRollback, options.WebPackageSHA256, options.WebRollbackSHA256); err != nil {
		return productionStatus{}, fmt.Errorf("Web package pair: %w", err)
	}
	for _, installed := range []productionPackageMetadata{goRollback, webRollback} {
		if err := runtime.verifyInstalledPackage(ctx, installed.Name, installed.Identity); err != nil {
			return productionStatus{}, fmt.Errorf("rollback package does not match installed state: %w", err)
		}
		revision, contract, err := runtime.readInstalledReleaseMetadata(installed.Name, installed.Identity)
		if err != nil || revision != installed.GitRevision || contract != installed.ContractRevision {
			return productionStatus{}, fmt.Errorf("installed %s release metadata does not match rollback package", installed.Name)
		}
	}
	if err := runtime.verifyMemoryPackageOwner(ctx, goRollback.Identity); err != nil {
		return productionStatus{}, err
	}
	probeVersion, err := runVerifiedBinary(ctx, runtime.runner, options.ProbeBinary, []string{"version"}, nil, "", productionCommandTimeout, false)
	if err != nil || strings.TrimSpace(string(probeVersion)) != options.ExpectedVersion {
		return productionStatus{}, errors.New("candidate probe binary version mismatch")
	}
	oldVersion, err := runtime.probeStatus(ctx, options.ProbeBinary, runtime.paths.LocalBaseURL, "")
	if err != nil {
		return productionStatus{}, fmt.Errorf("pre-upgrade local status probe failed: %w", err)
	}
	if !productionPackageMatches(goRollback.Version, oldVersion) {
		return productionStatus{}, errors.New("rollback Go package version does not match the running service")
	}
	if _, err := runtime.probeStatus(ctx, options.ProbeBinary, runtime.paths.PublicBaseURL, oldVersion); err != nil {
		return productionStatus{}, fmt.Errorf("pre-upgrade public status probe failed: %w", err)
	}
	comparisonOutput, err := runtime.runner.Run(ctx, productionCommand{Name: commandVercmp, Args: []string{oldVersion, options.ExpectedVersion}})
	if err != nil {
		return productionStatus{}, fmt.Errorf("compare release versions: %w", err)
	}
	comparison, err := strconv.Atoi(strings.TrimSpace(string(comparisonOutput)))
	if err != nil || (options.GoChanged && comparison >= 0) {
		return productionStatus{}, fmt.Errorf("candidate is not an upgrade: %s -> %s", oldVersion, options.ExpectedVersion)
	}
	oldTarget, err := currentFrontendTarget(runtime.paths.FrontendRoot)
	if err != nil {
		return productionStatus{}, err
	}
	oldIndexSHA, err := sha256File(filepath.Join(runtime.paths.FrontendRoot, "current", "index.html"))
	if err != nil || oldIndexSHA != webRollback.IndexSHA256 || oldTarget != frontendTargetFor(webRollback) {
		return productionStatus{}, errors.New("active frontend does not exactly match rollback Web package")
	}
	if err := runtime.probeFrontend(ctx, options.ProbeBinary, oldIndexSHA); err != nil {
		return productionStatus{}, fmt.Errorf("pre-upgrade public frontend probe failed: %w", err)
	}
	newTarget := frontendTargetFor(webCandidate)
	if !options.WebChanged && newTarget != oldTarget {
		return productionStatus{}, errors.New("unchanged Web package would change frontend target")
	}
	environmentRestoreSHA256, err := runtime.saveRestoreState(workspace, archivedEnvironment)
	if err != nil {
		return productionStatus{}, err
	}
	nginxEdgeRestoreSHA256 := ""
	if runtime.paths.EdgeAssetRoot != "" {
		nginxEdgeRestoreSHA256, err = runtime.captureEdgePolicyBackup(filepath.Join(workspace.configRestore, "nginx-edge"))
		if err != nil {
			return productionStatus{}, fmt.Errorf("capture nginx edge-policy restore state: %w", err)
		}
	}
	databaseSchema, err := runtime.captureDatabaseAccess(ctx, workspace, archivedEnvironment)
	if err != nil {
		return productionStatus{}, err
	}
	if err := runtime.probeModels(ctx, options.ProbeBinary, workspace.probeToken); err != nil {
		return productionStatus{}, fmt.Errorf("pre-upgrade authenticated business probe failed: %w", err)
	}
	if err := runtime.probeLive(ctx, options.ProbeBinary); err != nil {
		return productionStatus{}, fmt.Errorf("pre-upgrade live probe failed: %w", err)
	}

	if options.RollbackWindow != productionDefaultRollback {
		return productionStatus{}, errors.New("rollback watchdog window must be exactly 600 seconds")
	}
	armedUTC := utcSecond(runtime.now())
	deadline := armedUTC.Add(productionDefaultRollback)
	watchdogDeadline = deadline
	preflightManifest := productionManifest{DatabaseSchema: databaseSchema}
	if options.GoChanged {
		if err := runtime.runMigration(ctx, workspace, preflightManifest, migrationRun{name: "rollback-preflight", binary: runtime.paths.InstalledBinary, mode: "verify"}); err != nil {
			return productionStatus{}, fmt.Errorf("N-1 schema preflight hard stop: %w", err)
		}
	}
	manifest := productionManifest{
		Format: productionTransactionFormat, DeploymentID: workspace.id,
		OperatorUser: options.OperatorUser,
		Go:           transitionFromMetadata(options.GoChanged, options.GoPackage, options.GoRollbackPackage, options.GoPackageSHA256, options.GoRollbackSHA256, goCandidate, goRollback),
		Web:          transitionFromMetadata(options.WebChanged, options.WebPackage, options.WebRollbackPackage, options.WebPackageSHA256, options.WebRollbackSHA256, webCandidate, webRollback),
		Frontend:     productionFrontendTransition{OldTarget: oldTarget, NewTarget: newTarget, OldIndexSHA256: oldIndexSHA, NewIndexSHA256: webCandidate.IndexSHA256},
		ProbeBinary:  options.ProbeBinary, ProbeBinarySHA256: options.ProbeBinarySHA256,
		ExpectedVersion: options.ExpectedVersion, OldVersion: oldVersion, BackupDir: options.BackupDir, BackupsEnabled: options.WithBackups,
		DatabaseBackupSHA256: databaseBackupSHA256, DatabaseSchema: databaseSchema, ArmedUTC: armedUTC, DeadlineUTC: deadline,
		ObservationSeconds: int64(options.ObservationWindow / time.Second), ConfigRestorePath: workspace.configRestore, EnvironmentRestoreSHA256: environmentRestoreSHA256,
		NginxEdgeRestoreSHA256: nginxEdgeRestoreSHA256, PreserveEdgePolicy: options.PreserveEdgePolicy,
	}
	if err := runtime.writeManifest(workspace, manifest); err != nil {
		return productionStatus{}, fmt.Errorf("write deployment manifest: %w", err)
	}
	if err := runtime.writeStatus(workspace, productionStatus{Phase: "ARMING", Version: options.ExpectedVersion, Previous: oldVersion, RollbackTimer: workspace.timerUnit, DeadlineUTC: deadline}); err != nil {
		return productionStatus{}, err
	}
	timerMayBeActive, err := runtime.armRollbackTimer(ctx, workspace, manifest)
	armed = timerMayBeActive
	if err != nil {
		return productionStatus{}, err
	}
	if err := runtime.writeStatus(workspace, productionStatus{Phase: "ARMED", Version: options.ExpectedVersion, Previous: oldVersion, RollbackTimer: workspace.timerUnit, DeadlineUTC: deadline}); err != nil {
		return productionStatus{}, err
	}
	remaining := manifest.DeadlineUTC.Sub(runtime.now())
	if remaining <= 0 {
		return productionStatus{}, errors.New("rollback deadline expired while arming watchdog")
	}
	deadlineCtx, cancelDeadline := context.WithTimeout(transactionCtx, remaining)
	defer cancelDeadline()
	ctx = deadlineCtx
	if err := runtime.prepareOperatorWorkspace(ctx, workspace, options.OperatorUser, staged); err != nil {
		return productionStatus{}, err
	}
	if manifest.Go.Changed {
		if _, err := runtime.runner.Run(ctx, productionCommand{Name: commandSystemctl, Args: []string{"stop", runtime.paths.Service}}); err != nil {
			return productionStatus{}, fmt.Errorf("stop current Go service: %w", err)
		}
		if err := runtime.writeStatus(workspace, productionStatus{Phase: "MIGRATING", Version: options.ExpectedVersion, Previous: oldVersion, RollbackTimer: workspace.timerUnit, DeadlineUTC: deadline}); err != nil {
			return productionStatus{}, err
		}
		for _, migration := range []migrationRun{{name: "candidate-apply", binary: manifest.ProbeBinary, mode: "apply"}, {name: "candidate-verify", binary: manifest.ProbeBinary, mode: "verify"}, {name: "rollback-verify", binary: runtime.paths.InstalledBinary, mode: "verify"}} {
			if err := runtime.runMigration(ctx, workspace, manifest, migration); err != nil {
				return productionStatus{}, err
			}
		}
		if err := runtime.writeStatus(workspace, productionStatus{Phase: "DEPLOYING_GO", Version: options.ExpectedVersion, Previous: oldVersion, RollbackTimer: workspace.timerUnit, DeadlineUTC: deadline}); err != nil {
			return productionStatus{}, err
		}
		if err := runtime.retireContractlessMemoryDropInForUpgrade(ctx, manifest.Go.RollbackIdentity); err != nil {
			return productionStatus{}, err
		}
		if err := runtime.paruInstall(ctx, workspace, manifest.OperatorUser, manifest.Go.CandidatePath); err != nil {
			return productionStatus{}, fmt.Errorf("install candidate Go package: %w", err)
		}
		if err := runtime.restoreConfiguration(workspace, manifest); err != nil {
			return productionStatus{}, err
		}
		if manifest.NginxEdgeRestoreSHA256 != "" && !manifest.PreserveEdgePolicy {
			if err := runtime.applyEdgePolicyAssets(ctx, runtime.paths.EdgeAssetRoot, filepath.Join(workspace.configRestore, "nginx-edge"), true); err != nil {
				return productionStatus{}, fmt.Errorf("install managed nginx edge policy: %w", err)
			}
		}
		if err := retireKnownMemoryOverrides(runtime.paths.DropInDir); err != nil {
			return productionStatus{}, err
		}
		if err := hardenProductionConfiguration(productionHardenOptions{EnvFile: filepath.Join(runtime.paths.ConfigDir, "lmm-api-go.env"), DropInDir: runtime.paths.PackagedDropInDir, OverrideDropInDir: runtime.paths.DropInDir}); err != nil {
			return productionStatus{}, err
		}
		if _, err := runtime.runner.Run(ctx, productionCommand{Name: commandSystemctl, Args: []string{"daemon-reload"}}); err != nil {
			return productionStatus{}, fmt.Errorf("reload systemd after Go package installation: %w", err)
		}
		if err := runtime.verifyTransitionInstalled(ctx, manifest.Go, false, true); err != nil {
			return productionStatus{}, err
		}
		installedVersion, err := runVerifiedBinary(ctx, runtime.runner, runtime.paths.InstalledBinary, []string{"version"}, nil, "", productionCommandTimeout, false)
		if err != nil || strings.TrimSpace(string(installedVersion)) != options.ExpectedVersion {
			return productionStatus{}, errors.New("installed binary version mismatch")
		}
		for _, removed := range runtime.paths.RemovedPaths {
			if _, err := os.Lstat(removed); err == nil || !errors.Is(err, os.ErrNotExist) {
				return productionStatus{}, fmt.Errorf("removed split-architecture path remains: %s", removed)
			}
		}
		if _, err := runtime.runner.Run(ctx, productionCommand{Name: commandSystemctl, Args: []string{"reset-failed", runtime.paths.Service}}); err != nil {
			return productionStatus{}, fmt.Errorf("reset candidate Go service restart counter: %w", err)
		}
		if _, err := runtime.runner.Run(ctx, productionCommand{Name: commandSystemctl, Args: []string{"enable", "--now", runtime.paths.Service}}); err != nil {
			return productionStatus{}, fmt.Errorf("start candidate Go service: %w", err)
		}
		restartBaseline, err := runtime.readServiceRestarts(ctx)
		if err != nil {
			return productionStatus{}, fmt.Errorf("candidate Go service restart baseline hard stop: %w", err)
		}
		if restartBaseline != 0 {
			return productionStatus{}, fmt.Errorf("candidate Go service restart baseline hard stop: got=%d want=0", restartBaseline)
		}
		manifest.ServiceRestartBaseline = 0
		manifest.ObservationStartedUTC = utcSecond(runtime.now())
		if err := runtime.writeManifest(workspace, manifest); err != nil {
			return productionStatus{}, err
		}
	} else {
		restartBaseline, err := runtime.readServiceRestarts(ctx)
		if err != nil {
			return productionStatus{}, err
		}
		manifest.ServiceRestartBaseline = restartBaseline
		manifest.ObservationStartedUTC = utcSecond(runtime.now())
		if err := runtime.writeManifest(workspace, manifest); err != nil {
			return productionStatus{}, err
		}
	}
	if err := runtime.probeBackendLocalEventually(ctx, manifest, options.ExpectedVersion); err != nil {
		return productionStatus{}, fmt.Errorf("candidate local backend health gate failed: %w", err)
	}
	if err := runtime.verifyServiceRestartBaseline(ctx, manifest); err != nil {
		return productionStatus{}, fmt.Errorf("candidate local backend health gate changed restart baseline: %w", err)
	}
	if manifest.Web.Changed {
		if err := runtime.writeStatus(workspace, productionStatus{Phase: "DEPLOYING_WEB", Version: options.ExpectedVersion, Previous: oldVersion, RollbackTimer: workspace.timerUnit, DeadlineUTC: deadline}); err != nil {
			return productionStatus{}, err
		}
		if err := runtime.paruInstall(ctx, workspace, manifest.OperatorUser, manifest.Web.CandidatePath); err != nil {
			return productionStatus{}, fmt.Errorf("install candidate Web package: %w", err)
		}
		if err := runtime.verifyServiceRestartBaseline(ctx, manifest); err != nil {
			return productionStatus{}, fmt.Errorf("candidate Web installation changed restart baseline: %w", err)
		}
	}
	if err := runtime.verifyManifestInstalled(ctx, manifest, false); err != nil {
		return productionStatus{}, err
	}
	if err := verifyFrontendIdentity(runtime.paths.FrontendRoot, manifest.Frontend.NewTarget, manifest.Frontend.NewIndexSHA256); err != nil {
		return productionStatus{}, err
	}
	if err := runtime.verifyServiceRestartBaseline(ctx, manifest); err != nil {
		return productionStatus{}, fmt.Errorf("candidate release identity verification changed restart baseline: %w", err)
	}
	if err := runtime.probeRelease(ctx, manifest, options.ExpectedVersion, manifest.Frontend.NewIndexSHA256); err != nil {
		return productionStatus{}, fmt.Errorf("candidate release probes failed: %w", err)
	}
	if err := runtime.verifyServiceRestartBaseline(ctx, manifest); err != nil {
		return productionStatus{}, fmt.Errorf("candidate release probes changed restart baseline: %w", err)
	}
	awaiting := productionStatus{Phase: "AWAITING_CONFIRMATION", Version: options.ExpectedVersion, Previous: oldVersion, RollbackTimer: workspace.timerUnit, DeadlineUTC: deadline, AutoConfirm: !options.ManualConfirm, ObservationSec: int64(options.ObservationWindow / time.Second)}
	if err := runtime.writeStatus(workspace, awaiting); err != nil {
		return productionStatus{}, err
	}
	awaitingConfirmation = true
	if options.ManualConfirm {
		return awaiting, nil
	}
	if err := runtime.observe(ctx, workspace, manifest, options.ObservationWindow); err != nil {
		return productionStatus{}, &productionObservationError{err: fmt.Errorf("observation detected an anomaly; rollback timer remains armed: %w", err)}
	}
	return runtime.confirmLoaded(ctx, workspace, manifest)
}

func (runtime *productionRuntime) probeBackendLocalEventually(ctx context.Context, manifest productionManifest, expectedVersion string) error {
	var probeErr error
	for attempt := 0; attempt < 30; attempt++ {
		probeErr = runtime.probeBackendLocal(ctx, manifest, expectedVersion)
		if probeErr == nil {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if attempt < 29 {
			runtime.sleep(time.Second)
		}
	}
	return probeErr
}

func (runtime *productionRuntime) observe(ctx context.Context, workspace productionWorkspace, manifest productionManifest, window time.Duration) error {
	deadline := runtime.now().Add(window)
	if !deadline.Before(manifest.DeadlineUTC) {
		return errors.New("observation window cannot complete before rollback deadline")
	}
	for {
		if !runtime.now().Before(manifest.DeadlineUTC) {
			return errors.New("rollback deadline expired during observation")
		}
		if err := runtime.healthCheck(ctx, workspace, manifest); err != nil {
			return err
		}
		remaining := deadline.Sub(runtime.now())
		if remaining <= 0 {
			return nil
		}
		interval := productionObservationInterval
		if remaining < interval {
			interval = remaining
		}
		runtime.sleep(interval)
	}
}

func (runtime *productionRuntime) confirm(ctx context.Context, workspace productionWorkspace) (productionStatus, error) {
	manifest, err := runtime.readManifest(workspace)
	if err != nil {
		return productionStatus{}, err
	}
	return runtime.confirmLoaded(ctx, workspace, manifest)
}

func (runtime *productionRuntime) confirmLoaded(ctx context.Context, workspace productionWorkspace, manifest productionManifest) (productionStatus, error) {
	status, err := runtime.readStatus(workspace)
	if err != nil {
		return productionStatus{}, err
	}
	if status.Phase == "CONFIRMED" {
		if err := runtime.disarmRollbackTimer(ctx, workspace, false); err != nil {
			return productionStatus{}, err
		}
		if err := runtime.finalizeTransactionFiles(workspace); err != nil {
			return productionStatus{}, err
		}
		return status, nil
	}
	if status.Phase != "AWAITING_CONFIRMATION" && status.Phase != "CONFIRMING" {
		return productionStatus{}, fmt.Errorf("deployment phase %s is not awaiting confirmation", status.Phase)
	}
	if err := runtime.validateTransactionLock(workspace); err != nil {
		return productionStatus{}, err
	}
	if err := runtime.verifyManifestArchives(ctx, manifest); err != nil {
		return productionStatus{}, fmt.Errorf("deployment manifest archive verification failed: %w", err)
	}
	now := runtime.now()
	if !now.Before(manifest.DeadlineUTC) {
		return productionStatus{}, errors.New("rollback deadline has expired; confirmation is forbidden")
	}
	if manifest.DeadlineUTC.Sub(now) < productionConfirmationMargin {
		return productionStatus{}, errors.New("rollback deadline has insufficient time remaining for confirmation")
	}
	observationWindow := time.Duration(manifest.ObservationSeconds) * time.Second
	observationEnd := manifest.ObservationStartedUTC.Add(observationWindow)
	if manifest.ObservationStartedUTC.IsZero() || !observationEnd.Before(manifest.DeadlineUTC) {
		return productionStatus{}, errors.New("observation window cannot complete before the rollback deadline")
	}
	if now.Before(observationEnd) {
		return productionStatus{}, errors.New("confirmation requires the configured observation window of at least 120 seconds")
	}
	timerState, err := runtime.readRollbackTimerState(ctx, workspace)
	if err != nil {
		return productionStatus{}, err
	}
	if err := validateArmedRollbackTimer(timerState, manifest.DeadlineUTC); err != nil {
		return productionStatus{}, fmt.Errorf("confirmation is forbidden: %w", err)
	}
	if err := runtime.healthCheck(ctx, workspace, manifest); err != nil {
		return productionStatus{}, fmt.Errorf("final production health gate failed: %w", err)
	}
	if err := runtime.preserveConfirmedPackage(manifest); err != nil {
		return productionStatus{}, fmt.Errorf("preserve confirmed rollback package: %w", err)
	}
	if status.Phase == "AWAITING_CONFIRMATION" {
		if err := runtime.writeStatus(workspace, productionStatus{
			Phase: "CONFIRMING", Version: manifest.ExpectedVersion, Previous: manifest.OldVersion,
			RollbackTimer: workspace.timerUnit, DeadlineUTC: manifest.DeadlineUTC,
		}); err != nil {
			return productionStatus{}, err
		}
	}
	finalNow := runtime.now()
	if !finalNow.Before(manifest.DeadlineUTC) || manifest.DeadlineUTC.Sub(finalNow) < productionConfirmationMargin {
		return productionStatus{}, errors.New("rollback deadline became insufficient during final confirmation gates")
	}
	finalTimerState, err := runtime.readRollbackTimerState(ctx, workspace)
	if err != nil {
		return productionStatus{}, err
	}
	if err := validateArmedRollbackTimer(finalTimerState, manifest.DeadlineUTC); err != nil {
		return productionStatus{}, fmt.Errorf("confirmation is forbidden after final watchdog check: %w", err)
	}
	confirmed := productionStatus{
		Phase: "CONFIRMED", Version: manifest.ExpectedVersion, Previous: manifest.OldVersion,
		Reason: "native-cli-health-gates-passed", RollbackTimer: workspace.timerUnit, DeadlineUTC: manifest.DeadlineUTC,
	}
	// Persist CONFIRMED before disabling the watchdog. If disarming fails, the
	// timer sees the durable terminal state and cannot roll back a confirmed release.
	if err := runtime.writeStatus(workspace, confirmed); err != nil {
		return productionStatus{}, err
	}
	if err := runtime.disarmRollbackTimer(ctx, workspace, false); err != nil {
		return productionStatus{}, err
	}
	if err := runtime.finalizeTransactionFiles(workspace); err != nil {
		return productionStatus{}, err
	}
	return confirmed, nil
}

func (runtime *productionRuntime) rollback(ctx context.Context, workspace productionWorkspace, reason string) (productionStatus, error) {
	manifest, err := runtime.readManifest(workspace)
	if err != nil {
		return productionStatus{}, err
	}
	status, err := runtime.readStatus(workspace)
	if err != nil {
		return productionStatus{}, err
	}
	if status.Phase == "CONFIRMED" || status.Phase == "ROLLED_BACK" {
		if err := runtime.disarmRollbackTimer(ctx, workspace, false); err != nil {
			return productionStatus{}, err
		}
		if err := runtime.finalizeTransactionFiles(workspace); err != nil {
			return productionStatus{}, err
		}
		return status, nil
	}
	switch status.Phase {
	case "ARMING", "ARMED", "MIGRATING", "DEPLOYING", "DEPLOYING_GO", "DEPLOYING_WEB", "AWAITING_CONFIRMATION", "ROLLING_BACK", "ROLLBACK_FAILED":
	default:
		return productionStatus{}, fmt.Errorf("deployment phase %s is not rollback-eligible", status.Phase)
	}
	if !productionReasonPattern.MatchString(reason) {
		return productionStatus{}, errors.New("rollback reason is not audit-safe")
	}
	if err := runtime.verifyManifestArchives(ctx, manifest); err != nil {
		return productionStatus{}, fmt.Errorf("deployment manifest archive verification failed: %w", err)
	}
	if err := runtime.verifyCanonicalOperator(ctx); err != nil {
		return productionStatus{}, err
	}
	if err := runtime.validateTransactionLock(workspace); err != nil {
		return productionStatus{}, err
	}
	rolling := productionStatus{Phase: "ROLLING_BACK", Version: manifest.ExpectedVersion, Previous: manifest.OldVersion, Reason: reason, RollbackTimer: workspace.timerUnit, DeadlineUTC: manifest.DeadlineUTC}
	if err := runtime.writeStatus(workspace, rolling); err != nil {
		return productionStatus{}, err
	}
	fail := func(operationErr error) (productionStatus, error) {
		failed := rolling
		failed.Phase = "ROLLBACK_FAILED"
		failed.Reason = reason + ":" + operationErr.Error()
		_ = runtime.writeStatus(workspace, failed)
		return productionStatus{}, operationErr
	}
	if manifest.Go.Changed {
		_, _ = runtime.runner.Run(ctx, productionCommand{Name: commandSystemctl, Args: []string{"stop", runtime.paths.Service}})
		if err := runtime.paruInstall(ctx, workspace, manifest.OperatorUser, manifest.Go.RollbackPath); err != nil {
			return fail(fmt.Errorf("install rollback Go package: %w", err))
		}
		if err := runtime.restoreConfiguration(workspace, manifest); err != nil {
			return fail(err)
		}
		if manifest.NginxEdgeRestoreSHA256 != "" {
			if err := runtime.restoreEdgePolicyBackup(ctx, filepath.Join(workspace.configRestore, "nginx-edge"), manifest.NginxEdgeRestoreSHA256); err != nil {
				return fail(fmt.Errorf("restore nginx edge policy: %w", err))
			}
		}
		if err := hardenProductionConfiguration(productionHardenOptions{EnvFile: filepath.Join(runtime.paths.ConfigDir, "lmm-api-go.env"), DropInDir: runtime.paths.PackagedDropInDir, OverrideDropInDir: runtime.paths.DropInDir}); err != nil {
			return fail(err)
		}
		if _, err := runtime.runner.Run(ctx, productionCommand{Name: commandSystemctl, Args: []string{"daemon-reload"}}); err != nil {
			return fail(fmt.Errorf("reload systemd for rollback: %w", err))
		}
		if err := runtime.verifyTransitionInstalled(ctx, manifest.Go, true, true); err != nil {
			return fail(err)
		}
		if _, err := runtime.runner.Run(ctx, productionCommand{Name: commandSystemctl, Args: []string{"enable", "--now", runtime.paths.Service}}); err != nil {
			return fail(fmt.Errorf("start rolled-back Go service: %w", err))
		}
		if err := runtime.probeBackendLocalEventually(ctx, manifest, manifest.OldVersion); err != nil {
			return fail(fmt.Errorf("rolled-back local backend health gate failed: %w", err))
		}
	}
	if manifest.Web.Changed {
		if err := runtime.paruInstall(ctx, workspace, manifest.OperatorUser, manifest.Web.RollbackPath); err != nil {
			return fail(fmt.Errorf("install rollback Web package: %w", err))
		}
	}
	if err := runtime.verifyManifestInstalled(ctx, manifest, true); err != nil {
		return fail(err)
	}
	if err := verifyFrontendIdentity(runtime.paths.FrontendRoot, manifest.Frontend.OldTarget, manifest.Frontend.OldIndexSHA256); err != nil {
		return fail(err)
	}
	if err := runtime.probeRelease(ctx, manifest, manifest.OldVersion, manifest.Frontend.OldIndexSHA256); err != nil {
		return fail(fmt.Errorf("rolled-back release probes failed: %w", err))
	}
	if err := runtime.disarmRollbackTimer(ctx, workspace, false); err != nil {
		return fail(err)
	}
	rolledBack := productionStatus{Phase: "ROLLED_BACK", Version: manifest.OldVersion, Previous: manifest.ExpectedVersion, Reason: reason}
	if err := runtime.writeStatus(workspace, rolledBack); err != nil {
		return fail(err)
	}
	if err := runtime.finalizeTransactionFiles(workspace); err != nil {
		return fail(err)
	}
	return rolledBack, nil
}

type productionRollbackTimerState struct {
	LoadState, ActiveState, SubState, UnitFileState string
	NextElapseUTC, LastTriggerUTC                   time.Time
}

func parseSystemctlProperties(output []byte) (map[string]string, error) {
	properties := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || key == "" {
			return nil, errors.New("systemctl show returned malformed properties")
		}
		if _, duplicate := properties[key]; duplicate {
			return nil, errors.New("systemctl show returned a duplicate property")
		}
		properties[key] = value
	}
	return properties, nil
}

// pi-lens-ignore: go-bare-error
func parseSystemdTimestamp(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" || value == "n/a" {
		return time.Time{}, nil
	}
	if strings.HasPrefix(value, "@") {
		seconds, err := strconv.ParseInt(strings.TrimPrefix(value, "@"), 10, 64)
		if err != nil {
			return time.Time{}, fmt.Errorf("invalid systemd unix timestamp %q", value)
		}
		return time.Unix(seconds, 0).UTC(), nil
	}
	layouts := []string{"Mon 2006-01-02 15:04:05.999999 MST", "Mon 2006-01-02 15:04:05 MST", time.RFC3339Nano}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid systemd timestamp %q", value)
}

func (runtime *productionRuntime) readRollbackTimerState(ctx context.Context, workspace productionWorkspace) (productionRollbackTimerState, error) {
	output, err := runtime.runner.Run(ctx, productionCommand{Name: commandSystemctl, Args: []string{
		"show", workspace.timerUnit, "--timestamp=unix", "--property=LoadState", "--property=ActiveState", "--property=SubState", "--property=UnitFileState", "--property=NextElapseUSecRealtime", "--property=LastTriggerUSec",
	}})
	if err != nil {
		return productionRollbackTimerState{}, fmt.Errorf("read rollback timer state: %w", err)
	}
	properties, err := parseSystemctlProperties(output)
	if err != nil {
		return productionRollbackTimerState{}, err
	}
	next, err := parseSystemdTimestamp(properties["NextElapseUSecRealtime"])
	if err != nil {
		return productionRollbackTimerState{}, err
	}
	last, err := parseSystemdTimestamp(properties["LastTriggerUSec"])
	if err != nil {
		return productionRollbackTimerState{}, err
	}
	return productionRollbackTimerState{
		LoadState: properties["LoadState"], ActiveState: properties["ActiveState"], SubState: properties["SubState"],
		UnitFileState: properties["UnitFileState"], NextElapseUTC: next, LastTriggerUTC: last,
	}, nil
}

func validateArmedRollbackTimer(state productionRollbackTimerState, deadline time.Time) error {
	if state.LoadState != "loaded" || state.ActiveState != "active" || state.SubState != "waiting" || state.UnitFileState != "enabled" {
		return errors.New("rollback timer is not loaded, enabled, active, and waiting")
	}
	if !state.LastTriggerUTC.IsZero() {
		return errors.New("rollback timer has already triggered")
	}
	if state.NextElapseUTC.IsZero() || !state.NextElapseUTC.Equal(deadline.UTC()) {
		return errors.New("rollback timer next elapse does not match the fixed deployment deadline")
	}
	return nil
}

func (runtime *productionRuntime) armRollbackTimer(ctx context.Context, workspace productionWorkspace, manifest productionManifest) (bool, error) {
	if err := ensureRealDirectory(runtime.paths.SystemdUnitRoot, 0o755); err != nil {
		return false, fmt.Errorf("prepare systemd unit directory: %w", err)
	}
	for _, path := range []string{workspace.timerPath, workspace.rollbackPath} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			return false, errors.New("release-scoped rollback unit already exists or is unsafe")
		}
	}
	rollbackContent := fmt.Sprintf(`[Unit]
Description=LMM API Go release-scoped automatic rollback (%s)

[Service]
Type=oneshot
ExecStart=%s deploy production rollback --workspace %s --reason watchdog-deadline
TimeoutStartSec=10min
Restart=on-failure
RestartSec=10s
`, workspace.id, productionOperatorBinary, workspace.root)
	timerContent := fmt.Sprintf(`[Unit]
Description=LMM API Go rollback deadline (%s)

[Timer]
OnCalendar=@%d
AccuracySec=1s
Persistent=true
Unit=%s

[Install]
WantedBy=timers.target
`, workspace.id, manifest.DeadlineUTC.Unix(), workspace.rollbackUnit)
	if err := writeAtomicRegularFile(workspace.rollbackPath, []byte(rollbackContent), 0o644); err != nil {
		return false, fmt.Errorf("write rollback service: %w", err)
	}
	if err := writeAtomicRegularFile(workspace.timerPath, []byte(timerContent), 0o644); err != nil {
		_ = os.Remove(workspace.rollbackPath)
		return false, fmt.Errorf("write rollback timer: %w", err)
	}
	if _, err := runtime.runner.Run(ctx, productionCommand{Name: commandSystemctl, Args: []string{"daemon-reload"}}); err != nil {
		_ = os.Remove(workspace.timerPath)
		_ = os.Remove(workspace.rollbackPath)
		_, _ = runtime.runner.Run(ctx, productionCommand{Name: commandSystemctl, Args: []string{"daemon-reload"}})
		return false, fmt.Errorf("reload rollback units: %w", err)
	}
	if _, err := runtime.runner.Run(ctx, productionCommand{Name: commandSystemctl, Args: []string{"enable", "--now", workspace.timerUnit}}); err != nil {
		// systemctl may have started the timer before reporting an error. Treat
		// this as armed so the caller performs the release-scoped rollback path
		// and retains the transaction lock if disarming cannot be proven.
		return true, fmt.Errorf("arm rollback timer: %w", err)
	}
	state, err := runtime.readRollbackTimerState(ctx, workspace)
	if err != nil {
		return true, err
	}
	if err := validateArmedRollbackTimer(state, manifest.DeadlineUTC); err != nil {
		return true, err
	}
	return true, nil
}

func (runtime *productionRuntime) disarmRollbackTimer(ctx context.Context, workspace productionWorkspace, stopRollbackService bool) error {
	timerExists := false
	rollbackExists := false
	for path, exists := range map[string]*bool{workspace.timerPath: &timerExists, workspace.rollbackPath: &rollbackExists} {
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("rollback unit path is unsafe: %s", path)
		}
		*exists = true
	}
	if timerExists {
		if _, err := runtime.runner.Run(ctx, productionCommand{Name: commandSystemctl, Args: []string{"disable", "--now", workspace.timerUnit}}); err != nil {
			return fmt.Errorf("disable rollback timer: %w", err)
		}
	}
	if rollbackExists && stopRollbackService {
		_, _ = runtime.runner.Run(ctx, productionCommand{Name: commandSystemctl, Args: []string{"stop", workspace.rollbackUnit}})
	}
	_, _ = runtime.runner.Run(ctx, productionCommand{Name: commandSystemctl, Args: []string{"reset-failed", workspace.timerUnit, workspace.rollbackUnit}})
	if _, err := runtime.runner.Run(ctx, productionCommand{Name: commandSystemctl, Args: []string{"is-active", "--quiet", workspace.timerUnit}}); err == nil {
		return errors.New("rollback timer remains active after disable")
	}
	if stopRollbackService {
		if _, err := runtime.runner.Run(ctx, productionCommand{Name: commandSystemctl, Args: []string{"is-active", "--quiet", workspace.rollbackUnit}}); err == nil {
			return errors.New("rollback service remains active after stop")
		}
	}
	for _, path := range []string{workspace.timerPath, workspace.rollbackPath} {
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("rollback unit path is unsafe: %s", path)
		}
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("remove rollback unit %s: %w", filepath.Base(path), err)
		}
	}
	if _, err := runtime.runner.Run(ctx, productionCommand{Name: commandSystemctl, Args: []string{"daemon-reload"}}); err != nil {
		return fmt.Errorf("reload systemd after removing rollback units: %w", err)
	}
	return nil
}

func (runtime *productionRuntime) finalizeTransactionFiles(workspace productionWorkspace) error {
	if err := os.Remove(workspace.probeToken); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove production probe token: %w", err)
	}
	return runtime.releaseTransactionLock(workspace)
}

func (runtime *productionRuntime) preserveConfirmedPackage(manifest productionManifest) error {
	if err := ensureRealDirectory(runtime.paths.ReleasePackages, 0o700); err != nil {
		return err
	}
	for _, transition := range []productionPackageTransition{manifest.Go, manifest.Web} {
		if !strings.Contains(filepath.Base(transition.CandidatePath), ".pkg.tar.") {
			return errors.New("candidate package filename is not an Arch package")
		}
		destination := filepath.Join(runtime.paths.ReleasePackages, filepath.Base(transition.CandidatePath))
		if info, err := os.Lstat(destination); err == nil {
			if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
				return errors.New("preserved release package path is unsafe")
			}
			digest, err := sha256File(destination)
			if err != nil || digest != transition.CandidateSHA256 {
				return errors.New("preserved release package conflicts with the confirmed candidate")
			}
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := copyRegularFile(transition.CandidatePath, destination, 0o600, true); err != nil {
			return err
		}
	}
	return syncDirectory(runtime.paths.ReleasePackages)
}
