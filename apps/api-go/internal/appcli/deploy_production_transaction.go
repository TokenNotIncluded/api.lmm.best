package appcli

import (
	"context"
	"errors"
	"fmt"
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

func (runtime *productionRuntime) prepareOperatorWorkspace(ctx context.Context, workspace productionWorkspace, userName string, files []productionStagedFile) error {
	uidOutput, err := runtime.runner.Run(ctx, productionCommand{Name: "/usr/bin/id", Args: []string{"-u", userName}})
	if err != nil {
		return errors.New("operator user does not exist")
	}
	uid, err := strconv.ParseUint(strings.TrimSpace(string(uidOutput)), 10, 32)
	if err != nil || uid == 0 {
		return errors.New("operator user must have uid greater than zero")
	}
	gidOutput, err := runtime.runner.Run(ctx, productionCommand{Name: "/usr/bin/id", Args: []string{"-g", userName}})
	if err != nil {
		return errors.New("operator primary group is unavailable")
	}
	gid, err := strconv.ParseUint(strings.TrimSpace(string(gidOutput)), 10, 32)
	if err != nil {
		return errors.New("operator primary group is invalid")
	}
	deployRoot := filepath.Dir(runtime.paths.WorkRoot)
	if deployRoot == string(filepath.Separator) || deployRoot != filepath.Dir(runtime.paths.BackupRoot) {
		return errors.New("production work and backup roots must share a dedicated parent")
	}
	paths := []struct {
		path string
		mode os.FileMode
	}{
		{deployRoot, 0o710}, {runtime.paths.WorkRoot, 0o710}, {workspace.root, 0o710},
		{filepath.Join(workspace.root, productionWorkspaceMarker), 0o640}, {workspace.stagingDir, 0o750},
	}
	for _, file := range files {
		mode := os.FileMode(0o640)
		if file.executable {
			mode = 0o750
		}
		paths = append(paths, struct {
			path string
			mode os.FileMode
		}{file.path, mode})
	}
	for _, item := range paths {
		info, err := os.Lstat(item.path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o022 != 0 {
			return fmt.Errorf("operator payload path is missing, writable, or unsafe: %s", item.path)
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != runtime.requiredOwnerUID {
			return fmt.Errorf("operator payload path is not owned by root: %s", item.path)
		}
	}
	for label, path := range map[string]string{"state": workspace.stateDir, "backups": runtime.paths.BackupRoot, "transaction": runtime.paths.TransactionLock} {
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
			return fmt.Errorf("deployment %s directory must remain root-only", label)
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != runtime.requiredOwnerUID {
			return fmt.Errorf("deployment %s directory is not owned by root", label)
		}
	}
	for _, item := range paths {
		if err := os.Chown(item.path, int(runtime.requiredOwnerUID), int(gid)); err != nil {
			return fmt.Errorf("assign operator staging group: %w", err)
		}
		if err := os.Chmod(item.path, item.mode); err != nil {
			return fmt.Errorf("set operator staging permissions: %w", err)
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

func (runtime *productionRuntime) paruInstall(ctx context.Context, userName string, packages []string) error {
	if len(packages) == 0 {
		return errors.New("no changed packages selected")
	}
	uidOutput, err := runtime.runner.Run(ctx, productionCommand{Name: "/usr/bin/id", Args: []string{"-u", userName}})
	uid, parseErr := strconv.ParseUint(strings.TrimSpace(string(uidOutput)), 10, 32)
	if err != nil || parseErr != nil || uid == 0 {
		return errors.New("paru operator is missing or no longer unprivileged")
	}
	args := []string{"--user", userName, "--", runtime.paths.ParuBinary, "-U", "--noconfirm"}
	args = append(args, packages...)
	_, err = runtime.runner.Run(ctx, productionCommand{Name: runtime.paths.RunuserBinary, Args: args, Timeout: 5 * time.Minute})
	return err
}

func (runtime *productionRuntime) verifyManifestArchives(ctx context.Context, manifest productionManifest) error {
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

func (runtime *productionRuntime) verifyManifestInstalled(ctx context.Context, manifest productionManifest, rollback bool) error {
	for index, transition := range []productionPackageTransition{manifest.Go, manifest.Web} {
		name, identity, revision, contract := transition.CandidatePackageName, transition.CandidateIdentity, transition.CandidateGitRevision, transition.CandidateContractRevision
		if rollback {
			name, identity, revision, contract = transition.RollbackPackageName, transition.RollbackIdentity, transition.RollbackGitRevision, transition.RollbackContractRevision
		}
		if err := runtime.verifyInstalledPackage(ctx, name, identity); err != nil {
			return err
		}
		if index == 0 {
			if err := runtime.verifyMemoryPackageOwner(ctx, identity); err != nil {
				return err
			}
		}
		actualRevision, actualContract, err := runtime.readInstalledReleaseMetadata(name)
		if err != nil || actualRevision != revision || actualContract != contract {
			return fmt.Errorf("installed %s Git/contract metadata mismatch", name)
		}
	}
	return nil
}

func (runtime *productionRuntime) apply(ctx context.Context, workspace productionWorkspace, options productionTransactionOptions) (result productionStatus, returnErr error) {
	if !options.GoChanged && !options.WebChanged {
		return productionStatus{}, errors.New("at least one of --go-changed or --web-changed is required")
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
	defer func() {
		if returnErr == nil || awaitingConfirmation {
			return
		}
		if armed {
			if _, rollbackErr := runtime.rollback(ctx, workspace, "activation-failure"); rollbackErr != nil {
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
	if err := runtime.prepareOperatorWorkspace(ctx, workspace, options.OperatorUser, staged); err != nil {
		return productionStatus{}, err
	}
	for _, file := range staged {
		if err := runtime.validateStagedFile(workspace, file.path, file.digest, file.label); err != nil {
			return productionStatus{}, err
		}
	}

	archivedEnvironment, err := runtime.loadRollbackEnvironment(ctx, workspace, options.BackupDir)
	if err != nil {
		return productionStatus{}, err
	}
	if _, err := runtime.runner.Run(ctx, productionCommand{Name: "systemctl", Args: []string{"is-active", "--quiet", runtime.paths.Service}}); err != nil {
		return productionStatus{}, errors.New("pre-upgrade lmm-api service is not active")
	}
	if _, err := runtime.runner.Run(ctx, productionCommand{Name: "systemctl", Args: []string{"is-enabled", "--quiet", runtime.paths.Service}}); err != nil {
		return productionStatus{}, errors.New("pre-upgrade lmm-api service is not enabled")
	}
	if err := runtime.verifyCanonicalOperator(ctx); err != nil {
		return productionStatus{}, err
	}
	if err := verifyProductionMemoryDropIn(filepath.Join(runtime.paths.PackagedDropInDir, productionMemoryFileName)); err != nil {
		return productionStatus{}, err
	}
	if err := retireKnownMemoryOverrides(runtime.paths.DropInDir); err != nil {
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
		revision, contract, err := runtime.readInstalledReleaseMetadata(installed.Name)
		if err != nil || revision != installed.GitRevision || contract != installed.ContractRevision {
			return productionStatus{}, fmt.Errorf("installed %s release metadata does not match rollback package", installed.Name)
		}
	}
	if err := runtime.verifyMemoryPackageOwner(ctx, goRollback.Identity); err != nil {
		return productionStatus{}, err
	}
	probeVersion, err := runtime.runner.Run(ctx, productionCommand{Name: options.ProbeBinary, Args: []string{"version"}})
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
	comparisonOutput, err := runtime.runner.Run(ctx, productionCommand{Name: "vercmp", Args: []string{oldVersion, options.ExpectedVersion}})
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

	deadline := utcSecond(runtime.now().Add(options.RollbackWindow))
	manifest := productionManifest{
		OperatorUser: options.OperatorUser,
		Go:           transitionFromMetadata(options.GoChanged, options.GoPackage, options.GoRollbackPackage, options.GoPackageSHA256, options.GoRollbackSHA256, goCandidate, goRollback),
		Web:          transitionFromMetadata(options.WebChanged, options.WebPackage, options.WebRollbackPackage, options.WebPackageSHA256, options.WebRollbackSHA256, webCandidate, webRollback),
		Frontend:     productionFrontendTransition{OldTarget: oldTarget, NewTarget: newTarget, OldIndexSHA256: oldIndexSHA, NewIndexSHA256: webCandidate.IndexSHA256},
		ProbeBinary:  options.ProbeBinary, ProbeBinarySHA256: options.ProbeBinarySHA256,
		ExpectedVersion: options.ExpectedVersion, OldVersion: oldVersion, BackupDir: options.BackupDir,
		DatabaseSchema: databaseSchema, DeadlineUTC: deadline, EnvironmentRestoreSHA256: environmentRestoreSHA256,
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
	if _, err := runtime.runner.Run(ctx, productionCommand{Name: "systemctl", Args: []string{"stop", runtime.paths.Service}}); err != nil {
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
	if err := runtime.writeStatus(workspace, productionStatus{Phase: "DEPLOYING", Version: options.ExpectedVersion, Previous: oldVersion, RollbackTimer: workspace.timerUnit, DeadlineUTC: deadline}); err != nil {
		return productionStatus{}, err
	}
	if err := runtime.paruInstall(ctx, manifest.OperatorUser, changedPackagePaths(manifest, false)); err != nil {
		return productionStatus{}, fmt.Errorf("install candidate split packages: %w", err)
	}
	if err := runtime.restoreConfiguration(workspace, manifest); err != nil {
		return productionStatus{}, err
	}
	if manifest.NginxEdgeRestoreSHA256 != "" && !manifest.PreserveEdgePolicy {
		if err := runtime.applyEdgePolicyAssets(ctx, runtime.paths.EdgeAssetRoot, filepath.Join(workspace.configRestore, "nginx-edge"), true); err != nil {
			return productionStatus{}, fmt.Errorf("install managed nginx edge policy: %w", err)
		}
	}
	if err := hardenProductionConfiguration(productionHardenOptions{EnvFile: filepath.Join(runtime.paths.ConfigDir, "lmm-api-go.env"), DropInDir: runtime.paths.PackagedDropInDir, OverrideDropInDir: runtime.paths.DropInDir}); err != nil {
		return productionStatus{}, err
	}
	if _, err := runtime.runner.Run(ctx, productionCommand{Name: "systemctl", Args: []string{"daemon-reload"}}); err != nil {
		return productionStatus{}, fmt.Errorf("reload systemd after package installation: %w", err)
	}
	if err := runtime.verifyManifestInstalled(ctx, manifest, false); err != nil {
		return productionStatus{}, err
	}
	installedVersion, err := runtime.runner.Run(ctx, productionCommand{Name: runtime.paths.InstalledBinary, Args: []string{"version"}})
	if err != nil || strings.TrimSpace(string(installedVersion)) != options.ExpectedVersion {
		return productionStatus{}, errors.New("installed binary version mismatch")
	}
	for _, removed := range runtime.paths.RemovedPaths {
		if _, err := os.Lstat(removed); err == nil || !errors.Is(err, os.ErrNotExist) {
			return productionStatus{}, fmt.Errorf("removed split-architecture path remains: %s", removed)
		}
	}
	if _, err := runtime.runner.Run(ctx, productionCommand{Name: "systemctl", Args: []string{"enable", "--now", runtime.paths.Service}}); err != nil {
		return productionStatus{}, fmt.Errorf("start candidate Go service: %w", err)
	}
	if err := verifyFrontendIdentity(runtime.paths.FrontendRoot, manifest.Frontend.NewTarget, manifest.Frontend.NewIndexSHA256); err != nil {
		return productionStatus{}, err
	}
	restartBaseline, err := runtime.readServiceRestarts(ctx)
	if err != nil {
		return productionStatus{}, err
	}
	manifest.ServiceRestartBaseline = restartBaseline
	manifest.ObservationStartedUTC = utcSecond(runtime.now())
	if err := runtime.writeManifest(workspace, manifest); err != nil {
		return productionStatus{}, err
	}
	if err := runtime.probeRelease(ctx, manifest, options.ExpectedVersion, manifest.Frontend.NewIndexSHA256); err != nil {
		return productionStatus{}, fmt.Errorf("candidate release probes failed: %w", err)
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

func (runtime *productionRuntime) observe(ctx context.Context, workspace productionWorkspace, manifest productionManifest, window time.Duration) error {
	deadline := runtime.now().Add(window)
	for {
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
	if manifest.ObservationStartedUTC.IsZero() || runtime.now().Before(manifest.ObservationStartedUTC.Add(2*time.Minute)) {
		return productionStatus{}, errors.New("confirmation requires at least 120 seconds of observation")
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
	if err := runtime.disarmRollbackTimer(ctx, workspace, true); err != nil {
		return productionStatus{}, err
	}
	confirmed := productionStatus{
		Phase: "CONFIRMED", Version: manifest.ExpectedVersion, Previous: manifest.OldVersion,
		Reason: "native-cli-health-gates-passed",
	}
	if err := runtime.writeStatus(workspace, confirmed); err != nil {
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
		if err := runtime.finalizeTransactionFiles(workspace); err != nil {
			return productionStatus{}, err
		}
		return status, nil
	}
	switch status.Phase {
	case "ARMING", "ARMED", "MIGRATING", "DEPLOYING", "AWAITING_CONFIRMATION", "ROLLING_BACK", "ROLLBACK_FAILED":
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
	_, _ = runtime.runner.Run(ctx, productionCommand{Name: "systemctl", Args: []string{"stop", runtime.paths.Service}})
	if err := runtime.paruInstall(ctx, manifest.OperatorUser, changedPackagePaths(manifest, true)); err != nil {
		return fail(fmt.Errorf("install rollback split packages: %w", err))
	}
	oldRelease := strings.TrimPrefix(manifest.Frontend.OldTarget, "releases/")
	if err := executeFrontendDeploy(frontendDeployOptions{Action: "rollback", Root: runtime.paths.FrontendRoot, Release: oldRelease, Keep: productionFrontendReleaseKeep}); err != nil {
		return fail(fmt.Errorf("restore previous frontend link: %w", err))
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
	if _, err := runtime.runner.Run(ctx, productionCommand{Name: "systemctl", Args: []string{"daemon-reload"}}); err != nil {
		return fail(fmt.Errorf("reload systemd for rollback: %w", err))
	}
	if err := runtime.verifyManifestInstalled(ctx, manifest, true); err != nil {
		return fail(err)
	}
	if err := verifyFrontendIdentity(runtime.paths.FrontendRoot, manifest.Frontend.OldTarget, manifest.Frontend.OldIndexSHA256); err != nil {
		return fail(err)
	}
	if _, err := runtime.runner.Run(ctx, productionCommand{Name: "systemctl", Args: []string{"enable", "--now", runtime.paths.Service}}); err != nil {
		return fail(fmt.Errorf("start rolled-back Go service: %w", err))
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
	if _, err := runtime.runner.Run(ctx, productionCommand{Name: "systemctl", Args: []string{"daemon-reload"}}); err != nil {
		_ = os.Remove(workspace.timerPath)
		_ = os.Remove(workspace.rollbackPath)
		_, _ = runtime.runner.Run(ctx, productionCommand{Name: "systemctl", Args: []string{"daemon-reload"}})
		return false, fmt.Errorf("reload rollback units: %w", err)
	}
	if _, err := runtime.runner.Run(ctx, productionCommand{Name: "systemctl", Args: []string{"enable", "--now", workspace.timerUnit}}); err != nil {
		// systemctl may have started the timer before reporting an error. Treat
		// this as armed so the caller performs the release-scoped rollback path
		// and retains the transaction lock if disarming cannot be proven.
		return true, fmt.Errorf("arm rollback timer: %w", err)
	}
	if _, err := runtime.runner.Run(ctx, productionCommand{Name: "systemctl", Args: []string{"is-active", "--quiet", workspace.timerUnit}}); err != nil {
		return true, errors.New("rollback timer did not become active")
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
		if _, err := runtime.runner.Run(ctx, productionCommand{Name: "systemctl", Args: []string{"disable", "--now", workspace.timerUnit}}); err != nil {
			return fmt.Errorf("disable rollback timer: %w", err)
		}
	}
	if rollbackExists && stopRollbackService {
		_, _ = runtime.runner.Run(ctx, productionCommand{Name: "systemctl", Args: []string{"stop", workspace.rollbackUnit}})
	}
	_, _ = runtime.runner.Run(ctx, productionCommand{Name: "systemctl", Args: []string{"reset-failed", workspace.timerUnit, workspace.rollbackUnit}})
	if _, err := runtime.runner.Run(ctx, productionCommand{Name: "systemctl", Args: []string{"is-active", "--quiet", workspace.timerUnit}}); err == nil {
		return errors.New("rollback timer remains active after disable")
	}
	if stopRollbackService {
		if _, err := runtime.runner.Run(ctx, productionCommand{Name: "systemctl", Args: []string{"is-active", "--quiet", workspace.rollbackUnit}}); err == nil {
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
	if _, err := runtime.runner.Run(ctx, productionCommand{Name: "systemctl", Args: []string{"daemon-reload"}}); err != nil {
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
