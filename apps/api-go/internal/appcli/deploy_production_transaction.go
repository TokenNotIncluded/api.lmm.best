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
		uid, linkCount, ok := deploymentFileOwnership(info)
		if !ok || uid != runtime.requiredOwnerUID || (!item.directory && linkCount != 1) {
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
		uid, _, ok := deploymentFileOwnership(info)
		canonical, canonicalErr := filepath.EvalSymlinks(path)
		if !ok || uid != runtime.requiredOwnerUID || canonicalErr != nil || filepath.Clean(canonical) != filepath.Clean(path) {
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
	uid, linkCount, ok := deploymentFileOwnership(info)
	if !ok || uid != runtime.requiredOwnerUID || linkCount != 1 {
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
	if err := runtime.verifyTransitionCLI(manifest.Go, rollback); err != nil {
		return err
	}
	return runtime.verifyTransitionInstalled(ctx, manifest.Web, rollback, false)
}

func (runtime *productionRuntime) validateLegacyDeployPackageForProviderMigration(ctx context.Context, candidate productionPackageMetadata) (string, error) {
	listed, err := runtime.runner.Run(ctx, productionCommand{Name: commandPacman, Args: []string{"-Qq"}, Env: append(os.Environ(), "LC_ALL=C")})
	if err != nil {
		return "", fmt.Errorf("list installed packages before provider migration: %w", err)
	}
	legacyNames := map[string]bool{"lmm-api-deploy": true, "lmm-api-deploy-bin": true}
	installedLegacy := ""
	for _, name := range strings.Fields(string(listed)) {
		if !legacyNames[name] {
			continue
		}
		if installedLegacy != "" && installedLegacy != name {
			return "", errors.New("multiple legacy deployment packages are installed")
		}
		installedLegacy = name
	}
	if installedLegacy == "" {
		if _, err := os.Lstat(runtime.paths.LegacyDeployBinary); err == nil || !errors.Is(err, os.ErrNotExist) {
			return "", errors.New("unowned legacy deployment CLI remains before provider migration")
		}
		return "", nil
	}
	if candidate.Name != productionAURPackageName || candidate.Version == "0.1.69-1" {
		return "", errors.New("legacy deployment package removal requires the new provider package")
	}
	installedName, installedIdentity, err := runtime.installedGoPackage(ctx)
	if err != nil || installedName != productionAURPackageName || installedIdentity != productionAURPackageName+" 0.1.69-1" {
		return "", errors.New("legacy deployment package removal requires the exact integrated rollback floor")
	}
	for _, path := range []string{
		"/etc/sudoers.d/lmm-api-operator",
		"/usr/lib/sysusers.d/lmm-api-operator.conf",
		"/usr/lib/tmpfiles.d/lmm-api-operator.conf",
	} {
		ownership, err := runtime.runner.Run(ctx, productionCommand{Name: commandPacman, Args: []string{"-Qo", path}, Env: append(os.Environ(), "LC_ALL=C")})
		if err != nil || strings.TrimSpace(string(ownership)) != path+" is owned by "+installedIdentity {
			return "", errors.New("integrated rollback floor does not own operator resources")
		}
	}
	ownership, err := runtime.runner.Run(ctx, productionCommand{Name: commandPacman, Args: []string{"-Qo", runtime.paths.LegacyDeployBinary}, Env: append(os.Environ(), "LC_ALL=C")})
	expectedOwnership := runtime.paths.LegacyDeployBinary + " is owned by " + installedLegacy + " "
	if err != nil || !strings.HasPrefix(strings.TrimSpace(string(ownership)), expectedOwnership) {
		return "", errors.New("legacy deployment CLI ownership is invalid")
	}
	integrity, err := runtime.runner.Run(ctx, productionCommand{Name: commandPacman, Args: []string{"-Qkk", installedLegacy}, Env: append(os.Environ(), "LC_ALL=C")})
	if err != nil || !packageIntegrityClean(integrity, installedLegacy) {
		return "", errors.New("legacy deployment package integrity check failed")
	}
	return installedLegacy, nil
}

func (runtime *productionRuntime) removeLegacyDeployPackageForProviderMigration(ctx context.Context, candidate productionPackageMetadata) error {
	installedLegacy, err := runtime.validateLegacyDeployPackageForProviderMigration(ctx, candidate)
	if err != nil || installedLegacy == "" {
		return err
	}
	if _, err := runtime.runner.Run(ctx, productionCommand{Name: commandPacman, Args: []string{"--remove", "--noconfirm", "--", installedLegacy}, Timeout: 2 * time.Minute}); err != nil {
		return fmt.Errorf("remove legacy deployment package for provider migration: %w", err)
	}
	if _, err := os.Lstat(runtime.paths.LegacyDeployBinary); err == nil || !errors.Is(err, os.ErrNotExist) {
		return errors.New("legacy deployment CLI remains after package removal")
	}
	listed, err := runtime.runner.Run(ctx, productionCommand{Name: commandPacman, Args: []string{"-Qq"}, Env: append(os.Environ(), "LC_ALL=C")})
	if err != nil {
		return fmt.Errorf("verify installed packages after provider migration cleanup: %w", err)
	}
	for _, name := range strings.Fields(string(listed)) {
		if name == "lmm-api-deploy" || name == "lmm-api-deploy-bin" {
			return errors.New("legacy deployment package remains after removal")
		}
	}
	return nil
}

func (runtime *productionRuntime) verifyTransitionCLI(transition productionPackageTransition, rollback bool) error {
	name, identity := transition.CandidatePackageName, transition.CandidateIdentity
	if rollback {
		name, identity = transition.RollbackPackageName, transition.RollbackIdentity
	}
	metadata, err := parseNamedPackageIdentity([]byte(identity), name)
	if err != nil {
		return fmt.Errorf("parse installed backend provider identity: %w", err)
	}
	expectedTarget, err := providerTargetForPackage(name)
	if err != nil {
		return err
	}
	if rollback && name == productionAURPackageName && metadata.Version == "0.1.69-1" {
		// The signed N-1 package is the sole permitted old-layout rollback:
		// a regular lmm-api payload with lmm-api-go -> lmm-api.
		canonical, canonicalErr := os.Lstat(runtime.paths.InstalledBinary)
		provider, providerErr := os.Lstat(runtime.paths.LegacyGoBinary)
		target, targetErr := os.Readlink(runtime.paths.LegacyGoBinary)
		if canonicalErr != nil || !canonical.Mode().IsRegular() || canonical.Mode()&0o111 == 0 ||
			providerErr != nil || provider.Mode()&os.ModeSymlink == 0 || targetErr != nil ||
			target != filepath.Base(runtime.paths.InstalledBinary) {
			return errors.New("verified 0.1.69 rollback package does not match its legacy layout")
		}
		return nil
	}
	providerPath := filepath.Join(filepath.Dir(runtime.paths.InstalledBinary), expectedTarget)
	provider, err := os.Lstat(providerPath)
	if err != nil || provider.Mode()&os.ModeSymlink != 0 || !provider.Mode().IsRegular() ||
		provider.Mode()&0o111 == 0 || provider.Mode().Perm()&0o022 != 0 {
		return errors.New("installed backend provider is not a safe real executable")
	}
	canonical, err := os.Lstat(runtime.paths.InstalledBinary)
	if err != nil || canonical.Mode()&os.ModeSymlink == 0 {
		return errors.New("canonical backend path is not a provider-selection symlink")
	}
	target, err := os.Readlink(runtime.paths.InstalledBinary)
	if err != nil || target != expectedTarget {
		return errors.New("canonical backend link does not select the expected provider")
	}
	if _, err := os.Lstat(runtime.paths.LegacyDeployBinary); err == nil || !errors.Is(err, os.ErrNotExist) {
		return errors.New("legacy deployment CLI remains installed")
	}
	return nil
}

type productionBackendOwner struct {
	ctx    context.Context
	runner productionCommandRunner
}

func (owner productionBackendOwner) Owner(path string) (string, error) {
	output, err := owner.runner.Run(owner.ctx, productionCommand{Name: commandPacman, Args: []string{"-Qqo", "--", path}, Env: append(os.Environ(), "LC_ALL=C")})
	return strings.TrimSpace(string(output)), err
}

func (runtime *productionRuntime) prepareLegacyProviderRollback(manifest productionManifest) error {
	if manifest.PreviousProviderTarget != "legacy-regular" {
		return nil
	}
	if manifest.Go.RollbackPackageName != productionAURPackageName || manifest.Go.RollbackIdentity != productionAURPackageName+" 0.1.69-1" {
		return errors.New("legacy rollback package identity is invalid")
	}
	currentTarget, err := providerLinkState(runtime.paths.InstalledBinary)
	if err != nil || currentTarget != manifest.NewProviderTarget {
		return errors.New("active provider link changed before legacy rollback")
	}
	if err := os.Remove(runtime.paths.InstalledBinary); err != nil {
		return fmt.Errorf("remove provider link before legacy rollback: %w", err)
	}
	directory, err := os.Open(filepath.Dir(runtime.paths.InstalledBinary))
	if err != nil {
		return fmt.Errorf("open provider directory after legacy unlink: %w", err)
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("sync provider directory after legacy unlink: %w", err)
	}
	return nil
}

func (runtime *productionRuntime) selectInstalledProvider(ctx context.Context, target string) error {
	selector := backendRuntime{
		paths: backendPaths{Canonical: runtime.paths.InstalledBinary, Go: runtime.paths.LegacyGoBinary, Rust: filepath.Join(filepath.Dir(runtime.paths.InstalledBinary), backendRustName)},
		owner: productionBackendOwner{ctx: ctx, runner: runtime.runner}, effectiveID: runtime.effectiveUID,
		requiredUID: runtime.requiredOwnerUID,
	}
	if target != backendGoName && target != backendRustName {
		return errors.New("installed provider target is unsupported")
	}
	if _, err := selector.selectProvider(target); err != nil {
		return fmt.Errorf("select installed backend provider: %w", err)
	}
	return nil
}

func providerLinkState(path string) (string, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "missing", nil
	}
	if err != nil {
		return "", err
	}
	if info.Mode().IsRegular() && info.Mode()&0o111 != 0 {
		return "legacy-regular", nil
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return "", errors.New("canonical backend path has an unsafe type")
	}
	target, err := os.Readlink(path)
	if err != nil || filepath.IsAbs(target) || filepath.Base(target) != target {
		return "", errors.New("canonical backend link target is unsafe")
	}
	if target != backendGoName && target != backendRustName {
		return "", errors.New("canonical backend link target is unsupported")
	}
	return target, nil
}

func (runtime *productionRuntime) apply(ctx context.Context, workspace productionWorkspace, options productionTransactionOptions) (result productionStatus, returnErr error) {
	if !options.GoChanged && !options.WebChanged {
		return productionStatus{}, errors.New("at least one of --go-changed or --web-changed is required")
	}
	if options.WithBackups != (options.BackupDir != "") {
		return productionStatus{}, errors.New("production backups require both --with-backups and --backup-dir")
	}
	if options.GoChanged && !options.WithBackups {
		return productionStatus{}, errors.New("production Go transactions require verified three-copy backups via --with-backups and --backup-dir")
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
	mutationBoundary := false
	defer func() {
		if returnErr == nil {
			return
		}
		if mutationBoundary {
			failed := productionStatus{
				Phase: "ROLLBACK_REQUIRED", Version: options.ExpectedVersion,
				Reason: "activation-or-observation-failure", Failure: returnErr.Error(),
			}
			if statusErr := runtime.writeStatus(workspace, failed); statusErr != nil {
				returnErr = errors.Join(returnErr, fmt.Errorf("persist ROLLBACK_REQUIRED status: %w", statusErr))
			}
			return
		}
		_ = os.Remove(workspace.probeToken)
		if statusErr := runtime.writeStatus(workspace, productionStatus{Phase: "FAILED_PREARM", Version: options.ExpectedVersion, Reason: "activation-preparation-failed", Failure: returnErr.Error()}); statusErr != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("persist FAILED_PREARM status: %w", statusErr))
		}
		if lockErr := runtime.releaseTransactionLock(workspace); lockErr != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("release pre-mutation transaction lock: %w", lockErr))
		}
	}()

	if options.OperatorBinary == "" {
		options.OperatorBinary = options.ProbeBinary
	}
	if options.OperatorBinarySHA256 == "" {
		options.OperatorBinarySHA256 = options.ProbeBinarySHA256
	}
	staged := []productionStagedFile{
		{options.GoPackage, options.GoPackageSHA256, "candidate Go package", false},
		{options.GoRollbackPackage, options.GoRollbackSHA256, "rollback Go package", false},
		{options.WebPackage, options.WebPackageSHA256, "candidate Web package", false},
		{options.WebRollbackPackage, options.WebRollbackSHA256, "rollback Web package", false},
		{options.ProbeBinary, options.ProbeBinarySHA256, "probe binary", true},
		{options.OperatorBinary, options.OperatorBinarySHA256, "operator binary", true},
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
			return productionStatus{}, errors.New("authorized database backup is missing or empty")
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
	if options.GoChanged && !options.PreserveEdgePolicy {
		if err := runtime.validatePackagedEdgePolicyAssets(ctx, options.GoPackage); err != nil {
			return productionStatus{}, fmt.Errorf("candidate edge-policy preflight: %w", err)
		}
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

	previousProviderTarget, err := providerLinkState(runtime.paths.InstalledBinary)
	if err != nil {
		return productionStatus{}, fmt.Errorf("capture previous provider link: %w", err)
	}
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
		OperatorBinary: options.OperatorBinary, OperatorBinarySHA256: options.OperatorBinarySHA256,
		ExpectedVersion: options.ExpectedVersion, OldVersion: oldVersion,
		PreviousProviderTarget: previousProviderTarget, NewProviderTarget: backendGoName,
		BackupDir: options.BackupDir, BackupsEnabled: options.WithBackups,
		DatabaseBackupSHA256: databaseBackupSHA256, DatabaseSchema: databaseSchema,
		ObservationSeconds: int64(options.ObservationWindow / time.Second), ConfigRestorePath: workspace.configRestore, EnvironmentRestoreSHA256: environmentRestoreSHA256,
		NginxEdgeRestoreSHA256: nginxEdgeRestoreSHA256, PreserveEdgePolicy: options.PreserveEdgePolicy,
	}
	if err := runtime.writeManifest(workspace, manifest); err != nil {
		return productionStatus{}, fmt.Errorf("write deployment manifest: %w", err)
	}
	// Persist complete rollback evidence and an eligible state before the first
	// live mutation. A later status-write failure therefore still leaves this
	// durable MUTATION_PENDING record for explicit operator recovery.
	if err := runtime.writeStatus(workspace, productionStatus{Phase: "MUTATION_PENDING", Version: options.ExpectedVersion, Previous: oldVersion}); err != nil {
		return productionStatus{}, err
	}
	mutationBoundary = true
	if err := runtime.prepareOperatorWorkspace(ctx, workspace, options.OperatorUser, staged); err != nil {
		return productionStatus{}, err
	}
	if manifest.Go.Changed {
		if err := runtime.removeLegacyDeployPackageForProviderMigration(ctx, goCandidate); err != nil {
			return productionStatus{}, err
		}
		if _, err := runtime.runner.Run(ctx, productionCommand{Name: commandSystemctl, Args: []string{"stop", runtime.paths.Service}}); err != nil {
			return productionStatus{}, fmt.Errorf("stop current Go service: %w", err)
		}
		if err := runtime.writeStatus(workspace, productionStatus{Phase: "MIGRATING", Version: options.ExpectedVersion, Previous: oldVersion}); err != nil {
			return productionStatus{}, err
		}
		for _, migration := range []migrationRun{{name: "candidate-apply", binary: manifest.ProbeBinary, mode: "apply"}, {name: "candidate-verify", binary: manifest.ProbeBinary, mode: "verify"}, {name: "rollback-verify", binary: runtime.paths.InstalledBinary, mode: "verify"}} {
			if err := runtime.runMigration(ctx, workspace, manifest, migration); err != nil {
				return productionStatus{}, err
			}
		}
		if err := runtime.writeStatus(workspace, productionStatus{Phase: "DEPLOYING_GO", Version: options.ExpectedVersion, Previous: oldVersion}); err != nil {
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
		if err := runtime.selectInstalledProvider(ctx, manifest.NewProviderTarget); err != nil {
			return productionStatus{}, err
		}
		if err := runtime.verifyTransitionCLI(manifest.Go, false); err != nil {
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
		if err := runtime.writeStatus(workspace, productionStatus{Phase: "DEPLOYING_WEB", Version: options.ExpectedVersion, Previous: oldVersion}); err != nil {
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
	if err := runtime.writeStatus(workspace, productionStatus{Phase: "OBSERVING", Version: options.ExpectedVersion, Previous: oldVersion, ObservationSec: int64(options.ObservationWindow / time.Second)}); err != nil {
		return productionStatus{}, err
	}
	if err := runtime.observe(ctx, workspace, manifest, options.ObservationWindow); err != nil {
		return productionStatus{}, &productionObservationError{err: fmt.Errorf("observation detected an anomaly and manual rollback is required: %w", err)}
	}
	awaiting := productionStatus{Phase: "AWAITING_CONFIRMATION", Version: options.ExpectedVersion, Previous: oldVersion, ObservationSec: int64(options.ObservationWindow / time.Second)}
	if err := runtime.writeStatus(workspace, awaiting); err != nil {
		return productionStatus{}, err
	}
	return awaiting, nil
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
	observationEnd := manifest.ObservationStartedUTC.Add(window)
	if manifest.ObservationStartedUTC.IsZero() || window < 2*time.Minute {
		return errors.New("observation window is invalid")
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := runtime.healthCheck(ctx, workspace, manifest); err != nil {
			return err
		}
		remaining := observationEnd.Sub(runtime.now())
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
	observationWindow := time.Duration(manifest.ObservationSeconds) * time.Second
	observationEnd := manifest.ObservationStartedUTC.Add(observationWindow)
	if manifest.ObservationStartedUTC.IsZero() || observationWindow < 2*time.Minute || runtime.now().Before(observationEnd) {
		return productionStatus{}, errors.New("confirmation requires a completed observation window of at least 120 seconds")
	}
	if err := runtime.healthCheck(ctx, workspace, manifest); err != nil {
		return productionStatus{}, fmt.Errorf("final production health and identity gate failed: %w", err)
	}
	if err := runtime.preserveConfirmedPackage(manifest); err != nil {
		return productionStatus{}, fmt.Errorf("preserve confirmed rollback package: %w", err)
	}
	if status.Phase == "AWAITING_CONFIRMATION" {
		if err := runtime.writeStatus(workspace, productionStatus{Phase: "CONFIRMING", Version: manifest.ExpectedVersion, Previous: manifest.OldVersion}); err != nil {
			return productionStatus{}, err
		}
	}
	// Re-run archive and live identity gates immediately before the terminal
	// write so confirmation cannot bless changed evidence or a degraded release.
	if err := runtime.verifyManifestArchives(ctx, manifest); err != nil {
		return productionStatus{}, fmt.Errorf("final deployment archive verification failed: %w", err)
	}
	if err := runtime.healthCheck(ctx, workspace, manifest); err != nil {
		return productionStatus{}, fmt.Errorf("final production health and identity recheck failed: %w", err)
	}
	confirmed := productionStatus{
		Phase: "CONFIRMED", Version: manifest.ExpectedVersion, Previous: manifest.OldVersion,
		Reason: "native-cli-health-and-identity-gates-passed",
	}
	if err := runtime.writeStatus(workspace, confirmed); err != nil {
		return productionStatus{}, err
	}
	if err := runtime.finalizeTransactionFiles(workspace); err != nil {
		return productionStatus{}, err
	}
	return confirmed, nil
}

func (runtime *productionRuntime) persistRollbackFailure(workspace productionWorkspace, rolling productionStatus, reason string, operationErr error) error {
	failed := rolling
	failed.Phase = "ROLLBACK_REQUIRED"
	failed.Reason = reason
	failed.Failure = operationErr.Error()
	if statusErr := runtime.writeStatus(workspace, failed); statusErr != nil {
		return errors.Join(operationErr, fmt.Errorf("persist ROLLBACK_REQUIRED status: %w", statusErr))
	}
	return operationErr
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
		if err := runtime.finalizeTransactionFiles(workspace); err != nil {
			return productionStatus{}, err
		}
		return status, nil
	}
	switch status.Phase {
	case "MUTATION_PENDING", "MIGRATING", "DEPLOYING", "DEPLOYING_GO", "DEPLOYING_WEB", "OBSERVING", "AWAITING_CONFIRMATION", "CONFIRMING", "ROLLBACK_REQUIRED", "ROLLING_BACK":
	default:
		return productionStatus{}, fmt.Errorf("deployment phase %s is not rollback-eligible", status.Phase)
	}
	if !productionReasonPattern.MatchString(reason) {
		return productionStatus{}, errors.New("rollback reason is not audit-safe")
	}
	rolling := productionStatus{Phase: "ROLLING_BACK", Version: manifest.ExpectedVersion, Previous: manifest.OldVersion, Reason: reason}
	fail := func(operationErr error) (productionStatus, error) {
		return productionStatus{}, runtime.persistRollbackFailure(workspace, rolling, reason, operationErr)
	}
	if err := runtime.verifyManifestArchives(ctx, manifest); err != nil {
		return fail(fmt.Errorf("deployment manifest archive verification failed: %w", err))
	}
	if err := runtime.validateTransactionLock(workspace); err != nil {
		return fail(err)
	}
	if err := runtime.writeStatus(workspace, rolling); err != nil {
		return productionStatus{}, err
	}
	if manifest.Go.Changed {
		_, _ = runtime.runner.Run(ctx, productionCommand{Name: commandSystemctl, Args: []string{"stop", runtime.paths.Service}})
		if err := runtime.prepareLegacyProviderRollback(manifest); err != nil {
			return fail(err)
		}
		if err := runtime.paruInstall(ctx, workspace, manifest.OperatorUser, manifest.Go.RollbackPath); err != nil {
			return fail(fmt.Errorf("install rollback backend package: %w", err))
		}
		if err := runtime.restoreConfiguration(workspace, manifest); err != nil {
			return fail(err)
		}
		if manifest.NginxEdgeRestoreSHA256 != "" {
			if err := runtime.restoreEdgePolicyBackup(ctx, filepath.Join(workspace.configRestore, "nginx-edge"), manifest.NginxEdgeRestoreSHA256); err != nil {
				return fail(fmt.Errorf("restore nginx edge policy: %w", err))
			}
		}
		if manifest.PreviousProviderTarget == backendGoName || manifest.PreviousProviderTarget == "legacy-regular" {
			if err := hardenProductionConfiguration(productionHardenOptions{EnvFile: filepath.Join(runtime.paths.ConfigDir, "lmm-api-go.env"), DropInDir: runtime.paths.PackagedDropInDir, OverrideDropInDir: runtime.paths.DropInDir}); err != nil {
				return fail(err)
			}
		}
		if _, err := runtime.runner.Run(ctx, productionCommand{Name: commandSystemctl, Args: []string{"daemon-reload"}}); err != nil {
			return fail(fmt.Errorf("reload systemd for rollback: %w", err))
		}
		if err := runtime.verifyTransitionInstalled(ctx, manifest.Go, true, true); err != nil {
			return fail(err)
		}
		rollbackMetadata, err := parseNamedPackageIdentity([]byte(manifest.Go.RollbackIdentity), manifest.Go.RollbackPackageName)
		if err != nil {
			return fail(err)
		}
		if !(manifest.Go.RollbackPackageName == productionAURPackageName && rollbackMetadata.Version == "0.1.69-1") {
			if err := runtime.selectInstalledProvider(ctx, manifest.PreviousProviderTarget); err != nil {
				return fail(err)
			}
		}
		if err := runtime.verifyTransitionCLI(manifest.Go, true); err != nil {
			return fail(err)
		}
		if _, err := runtime.runner.Run(ctx, productionCommand{Name: commandSystemctl, Args: []string{"enable", "--now", runtime.paths.Service}}); err != nil {
			return fail(fmt.Errorf("start rolled-back backend service: %w", err))
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
	rolledBack := productionStatus{Phase: "ROLLED_BACK", Version: manifest.OldVersion, Previous: manifest.ExpectedVersion, Reason: reason}
	if err := runtime.writeStatus(workspace, rolledBack); err != nil {
		return fail(err)
	}
	if err := runtime.finalizeTransactionFiles(workspace); err != nil {
		return fail(err)
	}
	return rolledBack, nil
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
