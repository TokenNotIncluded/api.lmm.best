package deploycli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/LIghtJUNction/api.lmm.best/internal/appcli"
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

// Bind target activation arguments to the canonical controller plan. A schema
// policy is never a target-side escape hatch for another package tuple.
func validateProductionExistingSchemaApply(options productionTransactionOptions, plan productionReleasePlan) error {
	if err := validateProductionExistingSchemaPlan(plan); err != nil {
		return err
	}
	if options.Action != "apply" || options.SchemaMode != productionSchemaModeVerifyExisting ||
		plan.Format != productionExistingSchemaPlanFormat || options.MaintenanceHandoffPath != "" || options.MaintenanceHandoffSHA256 != "" ||
		options.OperatorUser != plan.OperatorUser || options.ExpectedVersion != plan.ExpectedVersion ||
		options.GoChanged != plan.GoChanged || options.WebChanged != plan.WebChanged ||
		options.WithBackups != plan.WithBackups || options.PreserveEdgePolicy != plan.PreserveEdgePolicy ||
		options.ObservationWindow != time.Duration(plan.ObservationSeconds)*time.Second {
		return errors.New("verify-existing activation arguments differ from the immutable release plan")
	}
	stage := filepath.Join(options.Workspace, "staging")
	if filepath.Base(options.Workspace) != plan.DeploymentID || options.StagedPlanPath != filepath.Join(stage, productionReleasePlanFilename) ||
		!productionSHA256Pattern.MatchString(options.StagedPlanSHA256) {
		return errors.New("verify-existing activation workspace differs from the immutable release plan")
	}
	for _, pair := range []struct {
		path, digest string
		planned      productionReleasePackagePlan
	}{
		{options.GoPackage, options.GoPackageSHA256, plan.GoCandidate},
		{options.GoRollbackPackage, options.GoRollbackSHA256, plan.GoRollback},
		{options.WebPackage, options.WebPackageSHA256, plan.WebCandidate},
		{options.WebRollbackPackage, options.WebRollbackSHA256, plan.WebRollback},
	} {
		if pair.path != filepath.Join(stage, filepath.Base(pair.planned.PackagePath)) || pair.digest != pair.planned.PackageSHA256 {
			return errors.New("verify-existing activation package differs from the immutable release plan")
		}
	}
	if options.ProbeBinary != filepath.Join(stage, backendGoName) || options.ProbeBinarySHA256 != plan.ProbeBinary.SHA256 ||
		options.OperatorBinary != filepath.Join(stage, filepath.Base(plan.OperatorBinary.Path)) || options.OperatorBinarySHA256 != plan.OperatorBinary.SHA256 {
		return errors.New("verify-existing activation provider differs from the immutable release plan")
	}
	if plan.WithBackups && (options.BackupDir != "" || options.ControllerBackup.PublicKey != plan.ControllerBackupPublicKey ||
		options.ControllerBackup.PlanSHA256 != options.StagedPlanSHA256) {
		return errors.New("verify-existing activation backup binding differs from the immutable release plan")
	}
	return nil
}

func productionSchemaMigrationRuns(manifest productionManifest, candidate, rollback string) ([]migrationRun, error) {
	switch manifest.SchemaMode {
	case "":
		if manifest.ExistingSchemaContract != nil || manifest.SchemaPlanSHA256 != "" {
			return nil, errors.New("historical schema activation cannot contain an existing-schema policy")
		}
		return []migrationRun{
			{name: "candidate-apply", binary: candidate, mode: "apply"},
			{name: "candidate-verify", binary: candidate, mode: "verify"},
			{name: "rollback-verify", binary: rollback, mode: "verify"},
		}, nil
	case productionSchemaModeVerifyExisting:
		if err := validateProductionExistingSchemaManifest(manifest); err != nil {
			return nil, err
		}
		return []migrationRun{
			{name: "candidate-verify", binary: candidate, mode: "verify"},
			{name: "rollback-verify", binary: rollback, mode: "verify"},
		}, nil
	default:
		return nil, errors.New("unsupported schema activation policy")
	}
}

func (runtime *productionRuntime) verifyExistingSchemaLifecycle(ctx context.Context, manifest productionManifest) error {
	if manifest.SchemaMode != productionSchemaModeVerifyExisting {
		return nil
	}
	if err := runtime.verifyExistingSchemaStartupMode(ctx, manifest); err != nil {
		return err
	}
	return runtime.verifyExistingSchemaContract(ctx, manifest.ExistingSchemaContract)
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
		RollbackOAuthManagedTokenIsolation:        rollback.OAuthManagedTokenIsolation,
		RollbackManagedBillingSettlementIsolation: rollback.ManagedBillingSettlementIsolation,
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
	var manifest productionManifest
	var err error
	if runtime.billingRollback {
		// Recovery must preserve the retained-provider checks without requiring
		// an already damaged candidate or unused auxiliary backup to survive.
		manifest, err = runtime.readManifestForRollback(workspace)
	} else {
		manifest, err = runtime.readManifest(workspace)
	}
	if err != nil {
		return errors.New("package mutation requires its immutable deployment/recovery manifest")
	}
	if err := runtime.requestMerchantStoreFence(ctx, workspace, manifest, false); err != nil {
		return err
	}
	args := []string{"--user", userName, "--", runtime.paths.ParuBinary, "-U", "--noconfirm", "--", packagePath}
	_, err = runtime.runner.Run(ctx, productionCommand{Name: commandRunuser, Args: args, Timeout: 5 * time.Minute})
	return err
}

func (runtime *productionRuntime) verifyManifestArchives(ctx context.Context, workspace productionWorkspace, manifest productionManifest) error {
	restoredEnvironment, err := readPrivateRegularFile(filepath.Join(manifest.ConfigRestorePath, "lmm-api-go.env"), 1<<20)
	if err != nil || fmt.Sprintf("%x", sha256Bytes(restoredEnvironment)) != manifest.EnvironmentRestoreSHA256 {
		return errors.New("configuration rollback snapshot no longer matches the deployment manifest")
	}
	if manifest.BackupEvidenceFormat == controllerBackupEvidenceFormat {
		if err := runtime.verifyControllerBackupEvidence(workspace, manifest, false); err != nil {
			return err
		}
	} else if manifest.BackupsEnabled {
		if _, err := runtime.validateBackupSet(ctx, workspace, manifest.BackupDir); err != nil {
			return fmt.Errorf("revalidate target production backup: %w", err)
		}
		attestation, err := readBackupAttestation(manifest.BackupDir, manifest.DeploymentID)
		if err != nil {
			return err
		}
		if manifest.BackupEvidenceFormat == 2 {
			if attestation.Format != 1 || attestation.EvidenceFormat != 2 || attestation.TargetDigest != manifest.TargetBackupSHA256 ||
				attestation.ControllerDigest != manifest.ControllerBackupSHA256 || attestation.OffhostDigest != manifest.OffhostBackupSHA256 {
				return errors.New("production backup attestation differs from immutable manifest digests")
			}
			targetDigest, err := sha256File(filepath.Join(manifest.BackupDir, "SHA256SUMS"))
			if err != nil || targetDigest != manifest.TargetBackupSHA256 {
				return errors.New("target backup checksum manifest differs from immutable deployment evidence")
			}
		} else if manifest.BackupEvidenceFormat != 0 || attestation.Format != 1 || attestation.EvidenceFormat != 0 {
			return errors.New("legacy deployment manifest cannot accept an unbound current backup attestation")
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
	if pairs[0].candidate.DeployEngineSHA256 != manifest.DeployEngineSHA256 || pairs[0].candidate.deploymentSHA256() != manifest.OperatorBinarySHA256 || pairs[0].candidate.BinarySHA256 != manifest.ProbeBinarySHA256 || pairs[1].candidate.IndexSHA256 != manifest.Frontend.NewIndexSHA256 || pairs[1].rollback.IndexSHA256 != manifest.Frontend.OldIndexSHA256 {
		return errors.New("manifest binary or frontend hash does not match staged package archives")
	}
	return nil
}

func (runtime *productionRuntime) verifyRollbackManifestArchives(ctx context.Context, manifest productionManifest) error {
	if manifest.Go.Changed {
		restoredEnvironment, err := readPrivateRegularFile(filepath.Join(manifest.ConfigRestorePath, "lmm-api-go.env"), 1<<20)
		if err != nil || fmt.Sprintf("%x", sha256Bytes(restoredEnvironment)) != manifest.EnvironmentRestoreSHA256 {
			return errors.New("configuration rollback snapshot no longer matches the deployment manifest")
		}
	}
	for _, transition := range []productionPackageTransition{manifest.Go, manifest.Web} {
		if !transition.Changed {
			continue
		}
		rollback, err := runtime.packageMetadata(ctx, transition.RollbackPath, transition.RollbackPackageName)
		if err != nil {
			return err
		}
		if rollback.Identity != transition.RollbackIdentity || rollback.GitRevision != transition.RollbackGitRevision ||
			rollback.ContractRevision != transition.RollbackContractRevision {
			return fmt.Errorf("%s rollback metadata does not match the staged package archive", transition.RollbackPackageName)
		}
		if transition.RollbackPackageName == productionWebPackageName && rollback.IndexSHA256 != manifest.Frontend.OldIndexSHA256 {
			return errors.New("rollback Web index hash does not match the deployment manifest")
		}
	}
	if manifest.Go.Changed && manifest.NginxEdgeRestoreSHA256 != "" {
		if err := runtime.validateEdgePolicyBackup(filepath.Join(manifest.ConfigRestorePath, "nginx-edge"), manifest.NginxEdgeRestoreSHA256); err != nil {
			return fmt.Errorf("validate edge-policy rollback evidence: %w", err)
		}
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
	if err := runtime.verifyTransitionCLI(ctx, manifest.Go, rollback); err != nil {
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
		if _, err := os.Lstat(runtime.paths.LegacyDeployBinary); errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		installedName, installedIdentity, err := runtime.installedGoPackage(ctx)
		if err != nil {
			return "", fmt.Errorf("unowned legacy deployment CLI remains before provider migration: %w", err)
		}
		if err := runtime.verifyInstalledGoPackage(ctx, installedName, installedIdentity); err != nil {
			return "", fmt.Errorf("unowned legacy deployment CLI remains before provider migration: %w", err)
		}
		if err := runtime.verifyPackageOwnedDeployEntrypoint(ctx, installedIdentity); err != nil {
			return "", fmt.Errorf("unowned legacy deployment CLI remains before provider migration: %w", err)
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

func (runtime *productionRuntime) verifyTransitionCLI(ctx context.Context, transition productionPackageTransition, rollback bool) error {
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
	// Older providers did not include this entrypoint. The caller's package
	// integrity check proves whether absence is expected; newer providers own it.
	_, err = os.Lstat(runtime.paths.LegacyDeployBinary)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return runtime.verifyPackageOwnedDeployEntrypoint(ctx, identity)
}

func (runtime *productionRuntime) verifyPackageOwnedDeployEntrypoint(ctx context.Context, identity string) error {
	deployPath := runtime.paths.LegacyDeployBinary
	deploy, err := os.Lstat(deployPath)
	if err != nil || deploy.Mode()&os.ModeSymlink != 0 || !deploy.Mode().IsRegular() ||
		deploy.Mode().Perm()&0o100 == 0 || deploy.Mode().Perm()&0o022 != 0 {
		return errors.New("deployment entrypoint is unsafe")
	}
	owner, links, ok := deploymentFileOwnership(deploy)
	if !ok || owner != runtime.requiredOwnerUID || links != 1 {
		return errors.New("deployment entrypoint ownership or link count is unsafe")
	}
	ownership, err := runtime.runner.Run(ctx, productionCommand{Name: commandPacman, Args: []string{"-Qo", deployPath}, Env: append(os.Environ(), "LC_ALL=C")})
	if err != nil || strings.TrimSpace(string(ownership)) != deployPath+" is owned by "+identity {
		return errors.New("deployment entrypoint is not owned by the expected backend package")
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
	if err := flushDirectory(directory); err != nil {
		return fmt.Errorf("sync provider directory after legacy unlink: %w", err)
	}
	return nil
}

func (runtime *productionRuntime) selectInstalledProvider(ctx context.Context, target string) error {

	if target != backendGoName && target != backendRustName {
		return errors.New("installed provider target is unsupported")
	}
	if err := appcli.SelectBackendProvider(appcli.BackendPaths{
		Canonical: runtime.paths.InstalledBinary, Go: runtime.paths.LegacyGoBinary,
		Rust: filepath.Join(filepath.Dir(runtime.paths.InstalledBinary), backendRustName),
	}, productionBackendOwner{ctx: ctx, runner: runtime.runner}, runtime.effectiveUID, runtime.requiredOwnerUID, target); err != nil {
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
	if err := runtime.refuseUnstoppedPostMutation(); err != nil {
		return productionStatus{}, err
	}
	if runtime.maintenanceHandoff != nil && options.Action != "maintenance-capture" && !runtime.maintenanceStopped() {
		return productionStatus{}, errors.New("maintenance apply requires official all-stopped owner handoff")
	}
	if runtime.maintenanceHandoff != nil && options.WebChanged {
		return productionStatus{}, errors.New("maintenance handoff requires unchanged exact frontend identity while guardian holds frontend lock")
	}
	if !options.GoChanged && !options.WebChanged && !runtime.maintenanceStopped() {
		return productionStatus{}, errors.New("at least one of --go-changed or --web-changed is required")
	}
	if err := validateControllerBackupTransactionOptions(options); err != nil {
		return productionStatus{}, err
	}
	if options.SchemaMode == productionSchemaModeVerifyExisting {
		if runtime.maintenanceHandoff != nil {
			return productionStatus{}, errors.New("verify-existing cannot use a financial maintenance handoff")
		}
		plan, err := loadStagedProductionExistingSchemaPlan(workspace, options.StagedPlanPath, options.StagedPlanSHA256)
		if err != nil {
			return productionStatus{}, err
		}
		if err := validateProductionExistingSchemaApply(options, plan); err != nil {
			return productionStatus{}, err
		}
		options.ExistingSchemaContract = plan.ExistingSchemaContract
		options.MerchantStoreWriter = plan.MerchantStoreWriter
		if err := runtime.verifyExistingSchemaSignedUnitBinding(ctx, options.ExistingSchemaContract, options.GoPackage, options.GoRollbackPackage); err != nil {
			return productionStatus{}, err
		}
	} else if options.SchemaMode != "" || options.ExistingSchemaContract != nil {
		return productionStatus{}, errors.New("existing-schema activation policy is invalid")
	} else if err := validateProductionExistingSchemaManifestPlan(workspace, productionManifest{}); err != nil {
		return productionStatus{}, fmt.Errorf("activation cannot omit its immutable schema policy: %w", err)
	}
	if options.Action == "maintenance-retry" {
		if err := runtime.archiveMaintenancePrearmFailure(ctx, workspace); err != nil {
			return productionStatus{}, err
		}
	}
	if _, err := os.Lstat(workspace.manifestPath); !errors.Is(err, os.ErrNotExist) {
		return productionStatus{}, errors.New("deployment manifest already exists")
	}
	if _, err := os.Lstat(workspace.statusPath); !errors.Is(err, os.ErrNotExist) {
		return productionStatus{}, errors.New("deployment status already exists")
	}
	if err := runtime.activateMaintenanceStagingIntent(ctx, workspace); err != nil {
		return productionStatus{}, err
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
		if runtime.maintenanceHandoff != nil {
			if statusErr := runtime.writeStatus(workspace, productionStatus{Phase: "MAINTENANCE_PREARM_FAILED", Version: options.ExpectedVersion, Reason: "maintenance-preparation-failed", Failure: returnErr.Error()}); statusErr != nil {
				returnErr = errors.Join(returnErr, statusErr)
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
	if runtime.maintenanceHandoff != nil && runtime.maintenanceHandoff.Stage == "prebridge" {
		if err := validateMaintenanceServiceReader(runtime.maintenanceHandoff.PrepareConfigPath); err != nil {
			return productionStatus{}, err
		}
	}

	if options.OperatorBinary == "" {
		options.OperatorBinary = options.ProbeBinary
	}
	if options.OperatorBinarySHA256 == "" {
		options.OperatorBinarySHA256 = options.ProbeBinarySHA256
	}
	separateEngine := options.OperatorBinary != options.ProbeBinary
	if separateEngine {
		if options.OperatorBinary != filepath.Join(workspace.stagingDir, deployEngineName) || !productionSHA256Pattern.MatchString(options.OperatorBinarySHA256) {
			return productionStatus{}, errors.New("candidate deployment tool is outside its exact staging path")
		}
	} else if options.OperatorBinarySHA256 != options.ProbeBinarySHA256 {
		return productionStatus{}, errors.New("candidate probe and operator evidence must identify the same lmm-api-go provider")
	}
	candidateEntrypoint, err := runtime.validateCandidateEntrypoint(workspace, options.ProbeBinary, options.ProbeBinarySHA256)
	if err != nil {
		return productionStatus{}, err
	}
	staged := []productionStagedFile{
		{options.GoPackage, options.GoPackageSHA256, "candidate Go package", false},
		{options.GoRollbackPackage, options.GoRollbackSHA256, "rollback Go package", false},
		{options.WebPackage, options.WebPackageSHA256, "candidate Web package", false},
		{options.WebRollbackPackage, options.WebRollbackSHA256, "rollback Web package", false},
		{options.ProbeBinary, options.ProbeBinarySHA256, "candidate provider binary", true},
	}
	if separateEngine {
		staged = append(staged, productionStagedFile{options.OperatorBinary, options.OperatorBinarySHA256, "candidate deployment tool", true})
	}
	if err := runtime.validateOperatorWorkspace(ctx, workspace, options.OperatorUser, staged); err != nil {
		return productionStatus{}, err
	}
	for _, file := range staged {
		if err := runtime.validateStagedFile(workspace, file.path, file.digest, file.label); err != nil {
			return productionStatus{}, err
		}
	}
	if candidateEntrypoint, err = runtime.validateCandidateEntrypoint(workspace, options.ProbeBinary, options.ProbeBinarySHA256); err != nil {
		return productionStatus{}, err
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
	if runtime.maintenanceStopped() {
		archivedEnvironment, err = runtime.maintenanceEnvironment()
	}
	if err != nil {
		return productionStatus{}, err
	}
	databaseBackupSHA256 := ""
	backupAttestation := productionBackupAttestation{}
	if options.BackupDir != "" {
		databaseBackupSHA256, err = sha256File(filepath.Join(options.BackupDir, "database.archive"))
		if err != nil || !productionSHA256Pattern.MatchString(databaseBackupSHA256) {
			return productionStatus{}, errors.New("authorized database backup is missing or empty")
		}
		backupAttestation, err = readBackupAttestation(options.BackupDir, workspace.id)
		if err != nil || backupAttestation.Format != 1 || backupAttestation.EvidenceFormat != 2 {
			return productionStatus{}, errors.New("production backup attestation is not bound to all three copy digests")
		}
		targetDigest, err := sha256File(filepath.Join(options.BackupDir, "SHA256SUMS"))
		if err != nil || targetDigest != backupAttestation.TargetDigest {
			return productionStatus{}, errors.New("target backup changed after controller verification")
		}
	}
	if runtime.maintenanceStopped() {
		if err := runtime.validateStoppedMaintenanceWriter(ctx); err != nil {
			return productionStatus{}, err
		}
	} else if _, err := runtime.runner.Run(ctx, productionCommand{Name: commandSystemctl, Args: []string{"is-active", "--quiet", runtime.paths.Service}}); err != nil {
		return productionStatus{}, errors.New("pre-upgrade lmm-api service is not active")
	}
	if _, err := runtime.runner.Run(ctx, productionCommand{Name: commandSystemctl, Args: []string{"is-enabled", "--quiet", runtime.paths.Service}}); err != nil {
		return productionStatus{}, errors.New("pre-upgrade lmm-api service is not enabled")
	}
	if err := validateMemoryOverrides(runtime.paths.DropInDir); err != nil {
		return productionStatus{}, fmt.Errorf("memory configuration preflight: %w", err)
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
	if goCandidate.deploymentSHA256() != options.OperatorBinarySHA256 || (goCandidate.DeployEngineSHA256 != "") != separateEngine {
		return productionStatus{}, errors.New("candidate deployment tool is not the separate executable contained in the Go package")
	}
	goIdentityChanged := options.GoChanged
	if runtime.maintenancePost() {
		if options.ProbeBinarySHA256 != runtime.maintenanceHandoff.ProviderSHA256 || goRollback.BinarySHA256 != runtime.maintenanceHandoff.ProviderSHA256 || options.GoPackageSHA256 != options.GoRollbackSHA256 {
			return productionStatus{}, errors.New("post handoff requires the exact installed bridge as candidate and true N-1")
		}
		goIdentityChanged = false
	}
	if runtime.maintenanceHandoff != nil && options.ProbeBinarySHA256 != runtime.maintenanceHandoff.ProviderSHA256 {
		return productionStatus{}, errors.New("candidate provider differs from maintenance identity")
	}
	if err := validateChangedIdentity(goIdentityChanged, goCandidate, goRollback, options.GoPackageSHA256, options.GoRollbackSHA256); err != nil {
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
	probeVersion, err := runVerifiedBinary(ctx, runtime.runner, candidateEntrypoint, []string{"version"}, nil, "", productionCommandTimeout, false)
	if err != nil || strings.TrimSpace(string(probeVersion)) != options.ExpectedVersion {
		return productionStatus{}, errors.New("candidate probe binary version mismatch")
	}
	oldVersion := ""
	if !runtime.maintenanceStopped() {
		oldVersion, err = runtime.probeStatus(ctx, candidateEntrypoint, runtime.paths.LocalBaseURL, "")
	}
	if runtime.maintenanceStopped() {
		var output []byte
		output, err = runVerifiedBinary(ctx, runtime.runner, runtime.paths.InstalledBinary, []string{"version"}, nil, "", productionCommandTimeout, false)
		oldVersion = strings.TrimSpace(string(output))
	}
	if err != nil {
		return productionStatus{}, fmt.Errorf("pre-upgrade local status probe failed: %w", err)
	}
	if !productionPackageMatches(goRollback.Version, oldVersion) {
		return productionStatus{}, errors.New("rollback Go package version does not match the running service")
	}
	if !runtime.maintenanceStopped() {
		if _, err := runtime.probeStatus(ctx, candidateEntrypoint, runtime.paths.PublicBaseURL, oldVersion); err != nil {
			return productionStatus{}, fmt.Errorf("pre-upgrade public status probe failed: %w", err)
		}
	}
	comparisonOutput, err := runtime.runner.Run(ctx, productionCommand{Name: commandVercmp, Args: []string{oldVersion, options.ExpectedVersion}})
	if err != nil {
		return productionStatus{}, fmt.Errorf("compare release versions: %w", err)
	}
	comparison, err := strconv.Atoi(strings.TrimSpace(string(comparisonOutput)))
	if err != nil || (options.GoChanged && comparison >= 0 && !(runtime.maintenancePost() && comparison == 0)) {
		return productionStatus{}, fmt.Errorf("candidate is not an upgrade: %s -> %s", oldVersion, options.ExpectedVersion)
	}
	oldTarget, err := currentFrontendTarget(runtime.paths.FrontendRoot)
	if err != nil {
		return productionStatus{}, err
	}
	oldIndexSHA, err := sha256File(filepath.Join(runtime.paths.FrontendRoot, "current", "index.html"))
	if err != nil || oldIndexSHA != webRollback.IndexSHA256 || oldTarget != frontendTargetFor(webRollback) {
		return productionStatus{}, fmt.Errorf("active frontend does not exactly match rollback Web package: active target=%q index_sha256=%q; rollback package=%q target=%q index_sha256=%q; read_error=%v", oldTarget, oldIndexSHA, webRollback.Identity, frontendTargetFor(webRollback), webRollback.IndexSHA256, err)
	}
	if !runtime.maintenanceStopped() {
		if err := runtime.probeFrontend(ctx, candidateEntrypoint, oldIndexSHA); err != nil {
			return productionStatus{}, fmt.Errorf("pre-upgrade public frontend probe failed: %w", err)
		}
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
	if runtime.maintenanceStopped() {
		if err := runtime.verifyMaintenanceDatabase(ctx, archivedEnvironment, runtime.maintenancePost()); err != nil {
			return productionStatus{}, err
		}
	} else {
		if err := runtime.probeModels(ctx, candidateEntrypoint, workspace.probeToken); err != nil {
			return productionStatus{}, fmt.Errorf("pre-upgrade authenticated business probe failed: %w", err)
		}
		if err := runtime.probeLive(ctx, candidateEntrypoint); err != nil {
			return productionStatus{}, fmt.Errorf("pre-upgrade live probe failed: %w", err)
		}
	}

	previousProviderTarget, err := providerLinkState(runtime.paths.InstalledBinary)
	if err != nil {
		return productionStatus{}, fmt.Errorf("capture previous provider link: %w", err)
	}
	preflightManifest := productionManifest{DatabaseSchema: databaseSchema, SchemaMode: options.SchemaMode, ExistingSchemaContract: options.ExistingSchemaContract}
	if options.SchemaMode == productionSchemaModeVerifyExisting {
		if databaseSchema != options.ExistingSchemaContract.Schema {
			return productionStatus{}, errors.New("existing-schema contract differs from migration search path")
		}
		if err := runtime.verifyExistingSchemaContract(ctx, options.ExistingSchemaContract); err != nil {
			return productionStatus{}, fmt.Errorf("existing-schema preflight: %w", err)
		}
		if err := runtime.runMigration(ctx, workspace, preflightManifest, migrationRun{name: "candidate-preflight", binary: candidateEntrypoint, mode: "verify"}); err != nil {
			return productionStatus{}, fmt.Errorf("candidate existing-schema preflight hard stop: %w", err)
		}
		if err := runtime.verifyExistingSchemaContract(ctx, options.ExistingSchemaContract); err != nil {
			return productionStatus{}, err
		}
	}
	if options.GoChanged {
		if err := runtime.runMigration(ctx, workspace, preflightManifest, migrationRun{name: "rollback-preflight", binary: runtime.paths.InstalledBinary, mode: "verify"}); err != nil {
			return productionStatus{}, fmt.Errorf("N-1 schema preflight hard stop: %w", err)
		}
	}
	if options.SchemaMode == productionSchemaModeVerifyExisting {
		if err := runtime.verifyExistingSchemaContract(ctx, options.ExistingSchemaContract); err != nil {
			return productionStatus{}, err
		}
	}
	manifest := productionManifest{
		DeployEngineSHA256:  goCandidate.DeployEngineSHA256,
		MerchantStoreWriter: options.MerchantStoreWriter,
		SchemaMode:          options.SchemaMode, ExistingSchemaContract: options.ExistingSchemaContract,
		MaintenanceHandoff: runtime.maintenanceHandoff,
		Format:             productionTransactionFormat, DeploymentID: workspace.id,
		OperatorUser: options.OperatorUser,
		Go:           transitionFromMetadata(options.GoChanged, options.GoPackage, options.GoRollbackPackage, options.GoPackageSHA256, options.GoRollbackSHA256, goCandidate, goRollback),
		Web:          transitionFromMetadata(options.WebChanged, options.WebPackage, options.WebRollbackPackage, options.WebPackageSHA256, options.WebRollbackSHA256, webCandidate, webRollback),
		Frontend:     productionFrontendTransition{OldTarget: oldTarget, NewTarget: newTarget, OldIndexSHA256: oldIndexSHA, NewIndexSHA256: webCandidate.IndexSHA256},
		ProbeBinary:  options.ProbeBinary, ProbeBinarySHA256: options.ProbeBinarySHA256,
		OperatorBinary: options.OperatorBinary, OperatorBinarySHA256: options.OperatorBinarySHA256,
		ExpectedVersion: options.ExpectedVersion, OldVersion: oldVersion,
		PreviousProviderTarget: previousProviderTarget, NewProviderTarget: backendGoName,
		BackupDir: options.BackupDir, BackupsEnabled: options.WithBackups, BackupEvidenceFormat: backupAttestation.EvidenceFormat,
		DatabaseBackupSHA256: databaseBackupSHA256, TargetBackupSHA256: backupAttestation.TargetDigest,
		ControllerBackupSHA256: backupAttestation.ControllerDigest, OffhostBackupSHA256: backupAttestation.OffhostDigest,
		DatabaseSchema:     databaseSchema,
		ObservationSeconds: int64(options.ObservationWindow / time.Second), ConfigRestorePath: workspace.configRestore, EnvironmentRestoreSHA256: environmentRestoreSHA256,
		NginxEdgeRestoreSHA256: nginxEdgeRestoreSHA256, PreserveEdgePolicy: options.PreserveEdgePolicy,
	}
	if options.SchemaMode == productionSchemaModeVerifyExisting {
		manifest.Format = productionExistingSchemaTransactionFormat
		manifest.SchemaPlanSHA256 = options.StagedPlanSHA256
		if err := runtime.verifyExistingSchemaStartupMode(ctx, manifest); err != nil {
			return productionStatus{}, err
		}
	}
	if options.ControllerBackup != (controllerBackupBinding{}) {
		binding := options.ControllerBackup
		receipt, err := readControllerBackupReceipt(binding.ReceiptPath, binding.PublicKey, binding.ReceiptSHA256, runtime.requiredOwnerUID)
		if err != nil {
			return productionStatus{}, err
		}
		manifest.BackupEvidenceFormat = controllerBackupEvidenceFormat
		manifest.ControllerOnlyBackup = &binding
		manifest.DatabaseBackupSHA256 = receipt.ArchivePlaintexts["database"]
		manifest.ControllerBackupSHA256 = receipt.BackupSetSHA256
		if receipt.GoRollbackPayloadSHA256 != goRollback.BinarySHA256 {
			return productionStatus{}, errors.New("controller backup payload does not match the verified rollback Go package")
		}
		if err := runtime.verifyControllerBackupEvidence(workspace, manifest, true); err != nil {
			return productionStatus{}, err
		}
	}
	if options.SchemaMode == productionSchemaModeVerifyExisting {
		if err := validateProductionExistingSchemaManifestPlan(workspace, manifest); err != nil {
			return productionStatus{}, err
		}
	}
	if options.GoChanged {
		for _, rollback := range []bool{false, true} {
			if err := runtime.checkMerchantStoreWriterLifecycle(ctx, workspace, manifest, false, rollback); err != nil {
				return productionStatus{}, fmt.Errorf("merchant writer pre-stop qualification: %w", err)
			}
		}
		if !runtime.maintenanceStopped() {
			if err := runtime.preflightBillingWriter(ctx, &manifest); err != nil {
				return productionStatus{}, fmt.Errorf("billing writer preflight: %w", err)
			}
		}
	}
	if runtime.maintenanceStopped() {
		if err := runtime.adoptMaintenanceBarrier(workspace, &manifest); err != nil {
			return productionStatus{}, err
		}
	}
	if err := runtime.writeManifest(workspace, manifest); err != nil {
		return productionStatus{}, fmt.Errorf("write deployment manifest: %w", err)
	}
	if manifest.Go.Changed {
		if err := runtime.ensureMerchantStoreFence(ctx, workspace, manifest); err != nil {
			return productionStatus{}, err
		}
	}
	if options.Action == "maintenance-capture" {
		if runtime.maintenanceHandoff == nil || runtime.maintenanceStopped() {
			return productionStatus{}, errors.New("maintenance capture requires live unactivated prebridge binding")
		}
		unit, err := runtime.billingUnitState(ctx, runtime.paths.Service)
		if err != nil {
			return productionStatus{}, err
		}
		pid, err := strconv.Atoi(unit["MainPID"])
		if err != nil || pid <= 1 || unit["ActiveState"] != "active" || len(unit["InvocationID"]) != 32 {
			return productionStatus{}, errors.New("capture writer identity unavailable")
		}
		if err := runtime.verifyMaintenanceDatabase(ctx, archivedEnvironment, false); err != nil {
			return productionStatus{}, err
		}
		installedSHA, err := sha256File(runtime.paths.InstalledBinary)
		if err != nil {
			return productionStatus{}, err
		}
		manifest.MaintenanceCapture = &productionMaintenanceCapture{Format: "lmm-credit-maintenance-capture-v1", TransitionID: runtime.maintenanceHandoff.TransitionID, TransitionIntentSHA256: runtime.maintenanceHandoff.TransitionIntentSHA256, ProviderSHA256: installedSHA, Version: oldVersion, PID: pid, InvocationID: unit["InvocationID"], ArchivedEnvironmentPath: filepath.Join(workspace.configRestore, "lmm-api-go.env"), ArchivedEnvironmentSHA256: environmentRestoreSHA256, DatabaseSchema: databaseSchema, FrontendTarget: oldTarget, FrontendSHA256: oldIndexSHA}
		if err := runtime.captureMaintenanceProcessEnvironment(ctx, workspace, manifest.MaintenanceCapture, archivedEnvironment); err != nil {
			return productionStatus{}, err
		}
		if err := runtime.writeManifest(workspace, manifest); err != nil {
			return productionStatus{}, err
		}
		return runtime.persistMaintenanceCapture(workspace, manifest, "CAPTURED")
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
	if candidateEntrypoint, err = runtime.validateCandidateEntrypoint(workspace, options.ProbeBinary, options.ProbeBinarySHA256); err != nil {
		return productionStatus{}, err
	}
	if manifest.Go.Changed {
		if manifest.SchemaMode == productionSchemaModeVerifyExisting {
			if err := runtime.verifyExistingSchemaContract(ctx, manifest.ExistingSchemaContract); err != nil {
				return productionStatus{}, err
			}
		}
		if err := runtime.removeLegacyDeployPackageForProviderMigration(ctx, goCandidate); err != nil {
			return productionStatus{}, err
		}
		if err := runtime.closeBillingAdmission(ctx, workspace, &manifest); err != nil {
			return productionStatus{}, err
		}
		if err := runtime.verifyExistingSchemaLifecycle(ctx, manifest); err != nil {
			return productionStatus{}, fmt.Errorf("pre-stop existing-schema invariant: %w", err)
		}
		if runtime.maintenanceStopped() {
			if err := runtime.validateStoppedMaintenanceWriter(ctx); err != nil {
				return productionStatus{}, err
			}
		} else if err := runtime.stopBillingWriter(ctx, workspace, &manifest); err != nil {
			return productionStatus{}, err
		}
		if err := runtime.writeStatus(workspace, productionStatus{Phase: "MIGRATING", Version: options.ExpectedVersion, Previous: oldVersion}); err != nil {
			return productionStatus{}, err
		}
		if manifest.SchemaMode == productionSchemaModeVerifyExisting {
			if err := runtime.verifyExistingSchemaContract(ctx, manifest.ExistingSchemaContract); err != nil {
				return productionStatus{}, err
			}
		}
		migrationRuns, err := productionSchemaMigrationRuns(manifest, candidateEntrypoint, runtime.paths.InstalledBinary)
		if err != nil {
			return productionStatus{}, err
		}
		for _, migration := range migrationRuns {
			if err := runtime.runMigration(ctx, workspace, manifest, migration); err != nil {
				return productionStatus{}, err
			}
			if manifest.SchemaMode == productionSchemaModeVerifyExisting {
				if err := runtime.verifyExistingSchemaContract(ctx, manifest.ExistingSchemaContract); err != nil {
					return productionStatus{}, err
				}
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

		if err := retireKnownMemoryOverrides(runtime.paths.DropInDir); err != nil {
			return productionStatus{}, err
		}
		if err := runtime.hardenProductionTransactionConfiguration(manifest); err != nil {
			return productionStatus{}, err
		}
		if err := runtime.configureMaintenanceService(workspace, manifest, false); err != nil {
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
		if err := runtime.verifyTransitionCLI(ctx, manifest.Go, false); err != nil {
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
		if err := runtime.verifyExistingSchemaLifecycle(ctx, manifest); err != nil {
			return productionStatus{}, fmt.Errorf("candidate startup existing-schema invariant: %w", err)
		}
		if err := runtime.checkMerchantStoreWriterLifecycle(ctx, workspace, manifest, true, false); err != nil {
			return productionStatus{}, fmt.Errorf("candidate installed writer qualification: %w", err)
		}
		if err := runtime.requestMerchantStoreFence(ctx, workspace, manifest, false); err != nil {
			return productionStatus{}, err
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
	if err := runtime.probeBackendLocalEventually(ctx, workspace, manifest, options.ExpectedVersion); err != nil {
		return productionStatus{}, fmt.Errorf("candidate local backend health gate failed: %w", err)
	}
	if err := runtime.verifyServiceRestartBaseline(ctx, manifest); err != nil {
		return productionStatus{}, fmt.Errorf("candidate local backend health gate changed restart baseline: %w", err)
	}
	if manifest.SchemaMode == productionSchemaModeVerifyExisting {
		if err := runtime.verifyExistingSchemaStartupMode(ctx, manifest); err != nil {
			return productionStatus{}, err
		}
		if err := runtime.verifyExistingSchemaContract(ctx, manifest.ExistingSchemaContract); err != nil {
			return productionStatus{}, err
		}
	}
	if err := runtime.reopenBillingAdmission(ctx, workspace, &manifest); err != nil {
		return productionStatus{}, err
	}
	if manifest.MaintenanceHandoff == nil && manifest.Go.Changed && manifest.NginxEdgeRestoreSHA256 != "" && !manifest.PreserveEdgePolicy {
		if err := runtime.applyEdgePolicyAssets(ctx, runtime.paths.EdgeAssetRoot, filepath.Join(workspace.configRestore, "nginx-edge"), true); err != nil {
			return productionStatus{}, fmt.Errorf("install managed nginx edge policy: %w", err)
		}
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
	if err := runtime.probeRelease(ctx, workspace, manifest, options.ExpectedVersion, manifest.Frontend.NewIndexSHA256); err != nil {
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

func (runtime *productionRuntime) probeBackendLocalEventually(ctx context.Context, workspace productionWorkspace, manifest productionManifest, expectedVersion string) error {
	return runtime.probeBackendLocalEventuallyWithBinary(ctx, workspace, manifest, "", expectedVersion)
}

func (runtime *productionRuntime) probeBackendLocalEventuallyWithBinary(ctx context.Context, workspace productionWorkspace, manifest productionManifest, binary, expectedVersion string) error {
	var probeErr error
	for attempt := 0; attempt < 30; attempt++ {
		if binary == "" {
			probeErr = runtime.probeBackendLocal(ctx, workspace, manifest, expectedVersion)
		} else {
			probeErr = runtime.probeBackendLocalWithBinary(ctx, binary, expectedVersion)
		}
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
	if err := runtime.refuseUnstoppedPostMutation(); err != nil {
		return productionStatus{}, err
	}
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
	if status.Phase != "AWAITING_CONFIRMATION" && status.Phase != "CONFIRMING" && status.Phase != productionMaintenanceConfirmedPhase {
		return productionStatus{}, fmt.Errorf("deployment phase %s is not awaiting confirmation", status.Phase)
	}
	if err := runtime.validateTransactionLock(workspace); err != nil {
		return productionStatus{}, err
	}
	if err := runtime.verifyManifestArchives(ctx, workspace, manifest); err != nil {
		return productionStatus{}, fmt.Errorf("deployment manifest archive verification failed: %w", err)
	}
	if manifest.BackupsEnabled && manifest.BackupEvidenceFormat == 2 {
		if err := runtime.validateBackupConfirmation(manifest.BackupDir, manifest, runtime.now()); err != nil {
			return productionStatus{}, err
		}
	}
	if manifest.BackupEvidenceFormat == controllerBackupEvidenceFormat {
		if err := runtime.verifyControllerBackupConfirmation(workspace, manifest); err != nil {
			return productionStatus{}, err
		}
	}
	observationWindow := time.Duration(manifest.ObservationSeconds) * time.Second
	observationEnd := manifest.ObservationStartedUTC.Add(observationWindow)
	if manifest.ObservationStartedUTC.IsZero() || observationWindow < 2*time.Minute || runtime.now().Before(observationEnd) {
		return productionStatus{}, errors.New("confirmation requires a completed observation window of at least 120 seconds")
	}
	if err := runtime.verifyExistingSchemaLifecycle(ctx, manifest); err != nil {
		return productionStatus{}, fmt.Errorf("final existing-schema invariant failed: %w", err)
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
	if err := runtime.verifyManifestArchives(ctx, workspace, manifest); err != nil {
		return productionStatus{}, fmt.Errorf("final deployment archive verification failed: %w", err)
	}
	if manifest.BackupsEnabled && manifest.BackupEvidenceFormat == 2 {
		if err := runtime.validateBackupConfirmation(manifest.BackupDir, manifest, runtime.now()); err != nil {
			return productionStatus{}, err
		}
	}
	if manifest.BackupEvidenceFormat == controllerBackupEvidenceFormat {
		if err := runtime.verifyControllerBackupConfirmation(workspace, manifest); err != nil {
			return productionStatus{}, err
		}
	}
	if err := runtime.healthCheck(ctx, workspace, manifest); err != nil {
		return productionStatus{}, fmt.Errorf("final production health and identity recheck failed: %w", err)
	}
	if err := runtime.verifyExistingSchemaLifecycle(ctx, manifest); err != nil {
		return productionStatus{}, fmt.Errorf("final existing-schema invariant recheck failed: %w", err)
	}
	confirmed := productionStatus{
		Phase: "CONFIRMED", Version: manifest.ExpectedVersion, Previous: manifest.OldVersion,
		Reason: "native-cli-health-and-identity-gates-passed",
	}
	if manifest.MaintenanceHandoff != nil {
		confirmed.Phase = productionMaintenanceConfirmedPhase
		confirmed.Reason = "bound-maintenance-health-and-identity-gates-passed"
		confirmed.MaintenanceConfirmation = true
	}
	if err := runtime.writeStatus(workspace, confirmed); err != nil {
		return productionStatus{}, err
	}
	if err := runtime.requestMerchantStoreFence(ctx, workspace, manifest, true); err != nil {
		return productionStatus{}, err
	}
	if manifest.MaintenanceHandoff == nil {
		if err := runtime.finalizeTransactionFiles(workspace); err != nil {
			return productionStatus{}, err
		}
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
	if err := runtime.refuseUnstoppedPostMutation(); err != nil {
		return productionStatus{}, err
	}
	manifest, err := runtime.readManifestForRollback(workspace)
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
	if status.Phase == "FAILED_PREARM" {
		return runtime.rollbackFailedPrearm(ctx, workspace, manifest, status, reason)
	}
	switch status.Phase {
	case "MUTATION_PENDING", "MIGRATING", "DEPLOYING", "DEPLOYING_GO", "DEPLOYING_WEB", "OBSERVING", "AWAITING_CONFIRMATION", "CONFIRMING", "ROLLBACK_REQUIRED", "ROLLING_BACK", productionMaintenanceConfirmedPhase:
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
	if err := runtime.verifyRollbackManifestArchives(ctx, manifest); err != nil {
		return fail(fmt.Errorf("rollback evidence verification failed: %w", err))
	}
	if err := runtime.validateTransactionLock(workspace); err != nil {
		return fail(err)
	}
	if err := validateMemoryOverrides(runtime.paths.DropInDir); err != nil {
		return fail(fmt.Errorf("rollback memory configuration preflight: %w", err))
	}
	if err := runtime.verifyExistingSchemaLifecycle(ctx, manifest); err != nil {
		return fail(fmt.Errorf("rollback existing-schema preflight: %w", err))
	}
	if err := runtime.checkMerchantStoreWriterLifecycle(ctx, workspace, manifest, false, true); err != nil {
		return fail(fmt.Errorf("rollback writer pre-stop qualification: %w", err))
	}
	if err := runtime.requestMerchantStoreFence(ctx, workspace, manifest, false); err != nil {
		return fail(err)
	}
	if early, earlyStatus, earlyErr := runtime.rollbackBeforeWriterStop(ctx, workspace, &manifest, status); early {
		if earlyErr != nil {
			return fail(earlyErr)
		}
		return earlyStatus, nil
	}
	if manifest.Go.Changed {
		if err := runtime.refuseManagedBillingRollback(ctx, workspace, manifest); err != nil {
			return fail(err)
		}
	}
	if err := runtime.writeStatus(workspace, rolling); err != nil {
		return productionStatus{}, err
	}
	runtime.billingRollback = true
	if manifest.Go.Changed {
		if err := runtime.closeBillingAdmission(ctx, workspace, &manifest); err != nil {
			return fail(err)
		}
		if err := runtime.verifyExistingSchemaLifecycle(ctx, manifest); err != nil {
			return fail(fmt.Errorf("rollback pre-stop existing-schema invariant: %w", err))
		}
		if err := runtime.stopBillingWriter(ctx, workspace, &manifest); err != nil {
			return fail(err)
		}
		if err := runtime.verifyExistingSchemaLifecycle(ctx, manifest); err != nil {
			return fail(fmt.Errorf("rollback stopped existing-schema invariant: %w", err))
		}
		if err := runtime.prepareLegacyProviderRollback(manifest); err != nil {
			return fail(err)
		}
		if manifest.Web.Changed {
			if err := runtime.paruInstall(ctx, workspace, manifest.OperatorUser, manifest.Web.RollbackPath); err != nil {
				return fail(fmt.Errorf("install rollback Web package before legacy backend: %w", err))
			}
		}
		if err := runtime.paruInstall(ctx, workspace, manifest.OperatorUser, manifest.Go.RollbackPath); err != nil {
			return fail(fmt.Errorf("install rollback backend package: %w", err))
		}
		if err := runtime.restoreConfiguration(workspace, manifest); err != nil {
			return fail(err)
		}

		if manifest.PreviousProviderTarget == backendGoName || manifest.PreviousProviderTarget == "legacy-regular" {
			if err := runtime.hardenProductionTransactionConfiguration(manifest); err != nil {
				return fail(err)
			}
		}
		if err := runtime.configureMaintenanceService(workspace, manifest, true); err != nil {
			return fail(err)
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
		if err := runtime.verifyTransitionCLI(ctx, manifest.Go, true); err != nil {
			return fail(err)
		}
		if manifest.SchemaMode == productionSchemaModeVerifyExisting {
			if err := runtime.runMigration(ctx, workspace, manifest, migrationRun{name: "rollback-recovery-verify", binary: runtime.paths.InstalledBinary, mode: "verify"}); err != nil {
				return fail(fmt.Errorf("installed N-1 existing-schema recovery verification failed: %w", err))
			}
			if err := runtime.verifyExistingSchemaLifecycle(ctx, manifest); err != nil {
				return fail(fmt.Errorf("N-1 existing-schema restart invariant: %w", err))
			}
		}
		if err := runtime.checkMerchantStoreWriterLifecycle(ctx, workspace, manifest, true, true); err != nil {
			return fail(fmt.Errorf("installed rollback writer qualification: %w", err))
		}
		if err := runtime.requestMerchantStoreFence(ctx, workspace, manifest, false); err != nil {
			return fail(err)
		}
		if _, err := runtime.runner.Run(ctx, productionCommand{Name: commandSystemctl, Args: []string{"enable", "--now", runtime.paths.Service}}); err != nil {
			return fail(fmt.Errorf("start rolled-back backend service: %w", err))
		}
		if err := runtime.probeBackendLocalEventuallyWithBinary(ctx, workspace, manifest, runtime.paths.InstalledBinary, manifest.OldVersion); err != nil {
			return fail(fmt.Errorf("rolled-back local backend health gate failed: %w", err))
		}
	}
	if manifest.Web.Changed && !manifest.Go.Changed {
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
	if err := runtime.verifyExistingSchemaLifecycle(ctx, manifest); err != nil {
		return fail(fmt.Errorf("rollback existing-schema admission invariant: %w", err))
	}
	if err := runtime.reopenBillingAdmission(ctx, workspace, &manifest); err != nil {
		return fail(err)
	}
	if manifest.MaintenanceHandoff == nil && manifest.NginxEdgeRestoreSHA256 != "" {
		if err := runtime.restoreEdgePolicyBackup(ctx, filepath.Join(workspace.configRestore, "nginx-edge"), manifest.NginxEdgeRestoreSHA256); err != nil {
			return fail(fmt.Errorf("restore nginx edge policy: %w", err))
		}
	}
	if err := runtime.probeReleaseWithBinary(ctx, workspace, runtime.paths.InstalledBinary, manifest.OldVersion, manifest.Frontend.OldIndexSHA256); err != nil {
		return fail(fmt.Errorf("rolled-back release probes failed: %w", err))
	}
	if err := runtime.verifyExistingSchemaLifecycle(ctx, manifest); err != nil {
		return fail(fmt.Errorf("rollback final existing-schema invariant: %w", err))
	}
	rolledBack := productionStatus{Phase: "ROLLED_BACK", Version: manifest.OldVersion, Previous: manifest.ExpectedVersion, Reason: reason}
	if runtime.maintenancePost() {
		// Post rollback reinstalls the exact same compatible bridge provider.
		// Re-observe its new invocation before it may participate in global release.
		baseline, err := runtime.readServiceRestarts(ctx)
		if err != nil {
			return fail(err)
		}
		manifest.ServiceRestartBaseline = baseline
		manifest.ObservationStartedUTC = utcSecond(runtime.now())
		if err := runtime.writeManifest(workspace, manifest); err != nil {
			return fail(err)
		}
		if err := runtime.observe(ctx, workspace, manifest, time.Duration(manifest.ObservationSeconds)*time.Second); err != nil {
			return fail(err)
		}
		rolledBack.Phase = productionMaintenanceConfirmedPhase
		rolledBack.MaintenanceConfirmation = true
		rolledBack.Reason = "compatible-post-rollback-reobserved; " + reason
	}
	if err := runtime.writeStatus(workspace, rolledBack); err != nil {
		return fail(err)
	}
	if err := runtime.requestMerchantStoreFence(ctx, workspace, manifest, true); err != nil {
		return fail(err)
	}
	if !runtime.maintenancePost() {
		if err := runtime.finalizeTransactionFiles(workspace); err != nil {
			return fail(err)
		}
	}
	return runtime.readStatus(workspace)
}

// rollbackBeforeWriterStop closes only the transaction bookkeeping when the
// failed apply never changed the running N-1 release. It deliberately returns
// (true, err) for a claimed-but-inconsistent pre-stop gate so the normal
// rollback path cannot stop or replace an unverified writer.
func (runtime *productionRuntime) rollbackBeforeWriterStop(ctx context.Context, workspace productionWorkspace, manifest *productionManifest, status productionStatus) (bool, productionStatus, error) {
	if status.Phase != "ROLLBACK_REQUIRED" || !manifest.Go.Changed || manifest.BillingGate == nil || manifest.BillingGate.StopVerified {
		return false, productionStatus{}, nil
	}
	gate := manifest.BillingGate
	state, err := runtime.billingUnitState(ctx, runtime.paths.Service)
	if err != nil {
		return true, productionStatus{}, fmt.Errorf("pre-stop rollback writer evidence unavailable: %w", err)
	}
	resumeStopped := state["ActiveState"] == "inactive" && state["InvocationID"] == gate.GoInvocationID &&
		gate.AdmissionClosed && !gate.AdmissionReopened && !gate.StopStartedUTC.IsZero() && manifest.ObservationStartedUTC.IsZero()
	if state["ActiveState"] != "active" && !resumeStopped {
		return false, productionStatus{}, nil
	}
	if resumeStopped {
		// StopVerified is durable before any schema/package mutation. Re-audit
		// the original exit before resuming this exact, unchanged provider.
		if err := cleanBillingUnitExit(state, gate.GoPID); err != nil {
			return true, productionStatus{}, err
		}
		if err := runtime.verifyNoUntrackedRefunds(ctx, manifest); err != nil {
			return true, productionStatus{}, err
		}
		since := fmt.Sprintf("@%d.%06d", gate.StopStartedUTC.Unix(), gate.StopStartedUTC.Nanosecond()/1000)
		journal, err := runtime.runner.Run(ctx, productionCommand{Name: commandJournalctl, Args: []string{"--no-pager", "--output=cat", "--since", since, "-u", runtime.paths.Service, "_PID=" + strconv.Itoa(gate.GoPID), "_SYSTEMD_INVOCATION_ID=" + gate.GoInvocationID}})
		if err != nil {
			return true, productionStatus{}, err
		}
		if err := validateBillingShutdownJournal(journal); err != nil {
			return true, productionStatus{}, err
		}
		tracked, err := runtime.trackedRefundWriter(ctx, manifest)
		if err != nil || (tracked && !bytes.Contains(bytes.ToLower(journal), []byte("refund_tasks execution_complete="))) {
			return true, productionStatus{}, errors.New("unchanged stopped writer lacks refund completion evidence")
		}
	}
	sameWriter := resumeStopped || (state["MainPID"] == strconv.Itoa(gate.GoPID) && state["InvocationID"] == gate.GoInvocationID)
	if gate.GoPID == 0 && gate.GoInvocationID == "" && gate.StopStartedUTC.IsZero() && !gate.AdmissionClosed {
		// Admission can time out before stopBillingWriter records an identity.
		// Only restore ingress if systemd proves this writer predates the gate.
		if err := runtime.verifyUnchangedAdmissionWriter(ctx, *manifest, state); err != nil {
			return true, productionStatus{}, err
		}
		sameWriter = true
	}
	if !sameWriter {
		// A replacement writer cannot use the unchanged-writer shortcut. The
		// normal rollback must drain and verify this instance before mutation.
		return false, productionStatus{}, nil
	}
	if err := runtime.verifyManifestInstalled(ctx, *manifest, true); err != nil {
		return true, productionStatus{}, fmt.Errorf("pre-stop rollback installed N-1 evidence failed: %w", err)
	}
	currentEnvironment, err := readPrivateRegularFile(filepath.Join(runtime.paths.ConfigDir, "lmm-api-go.env"), 1<<20)
	if err != nil {
		return true, productionStatus{}, errors.New("pre-stop rollback environment unavailable")
	}
	restoredEnvironment, err := readPrivateRegularFile(filepath.Join(workspace.configRestore, "lmm-api-go.env"), 1<<20)
	if err != nil || fmt.Sprintf("%x", sha256Bytes(restoredEnvironment)) != manifest.EnvironmentRestoreSHA256 || !bytes.Equal(currentEnvironment, restoredEnvironment) {
		return true, productionStatus{}, errors.New("pre-stop rollback environment changed")
	}
	if err := verifyFrontendIdentity(runtime.paths.FrontendRoot, manifest.Frontend.OldTarget, manifest.Frontend.OldIndexSHA256); err != nil {
		return true, productionStatus{}, fmt.Errorf("pre-stop rollback frontend evidence failed: %w", err)
	}
	if err := runtime.verifyPreStopEdgeState(workspace, *manifest); err != nil {
		return true, productionStatus{}, fmt.Errorf("pre-stop rollback edge evidence failed: %w", err)
	}
	if err := runtime.verifyExistingSchemaLifecycle(ctx, *manifest); err != nil {
		return true, productionStatus{}, fmt.Errorf("pre-stop rollback existing-schema invariant: %w", err)
	}
	if resumeStopped {
		if err := runtime.runMigration(ctx, workspace, *manifest, migrationRun{name: "unchanged-provider-recovery", binary: runtime.paths.InstalledBinary, mode: "verify"}); err != nil {
			return true, productionStatus{}, err
		}
		if err := runtime.verifyExistingSchemaLifecycle(ctx, *manifest); err != nil {
			return true, productionStatus{}, err
		}
		if err := runtime.checkMerchantStoreWriterLifecycle(ctx, workspace, *manifest, true, true); err != nil {
			return true, productionStatus{}, err
		}
		if err := runtime.requestMerchantStoreFence(ctx, workspace, *manifest, false); err != nil {
			return true, productionStatus{}, err
		}
		if _, err := runtime.runner.Run(ctx, productionCommand{Name: commandSystemctl, Args: []string{"enable", "--now", runtime.paths.Service}}); err != nil {
			return true, productionStatus{}, err
		}
		if err := runtime.probeBackendLocalEventuallyWithBinary(ctx, workspace, *manifest, runtime.paths.InstalledBinary, manifest.OldVersion); err != nil {
			return true, productionStatus{}, err
		}
	}
	if err := runtime.verifyExistingSchemaLifecycle(ctx, *manifest); err != nil {
		return true, productionStatus{}, fmt.Errorf("pre-stop rollback admission invariant: %w", err)
	}
	previousRollback := runtime.billingRollback
	runtime.billingRollback = true // The unchanged installed writer is still N-1.
	defer func() { runtime.billingRollback = previousRollback }()
	if err := runtime.reopenBillingAdmission(ctx, workspace, manifest); err != nil {
		return true, productionStatus{}, fmt.Errorf("pre-stop rollback billing restore failed: %w", err)
	}
	if err := runtime.probeReleaseWithBinary(ctx, workspace, runtime.paths.InstalledBinary, manifest.OldVersion, manifest.Frontend.OldIndexSHA256); err != nil {
		return true, productionStatus{}, fmt.Errorf("pre-stop rollback N-1 probe failed: %w", err)
	}
	if err := runtime.verifyExistingSchemaLifecycle(ctx, *manifest); err != nil {
		return true, productionStatus{}, fmt.Errorf("pre-stop rollback final existing-schema invariant: %w", err)
	}
	rolledBack := productionStatus{Phase: "ROLLED_BACK", Version: manifest.OldVersion, Previous: manifest.ExpectedVersion, Reason: "unchanged-writer-restored"}
	if !manifest.BillingGate.AdmissionClosed {
		rolledBack.Reason = productionUnchangedAdmissionRecoveryReason
	}
	if err := runtime.writeStatus(workspace, rolledBack); err != nil {
		return true, productionStatus{}, err
	}
	if err := runtime.requestMerchantStoreFence(ctx, workspace, *manifest, true); err != nil {
		return true, productionStatus{}, err
	}
	persisted, err := runtime.readStatus(workspace)
	if err != nil {
		return true, productionStatus{}, err
	}
	if err := runtime.finalizeTransactionFiles(workspace); err != nil {
		return true, productionStatus{}, err
	}
	return true, persisted, nil
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
