package appcli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	productionTargetAlias   = "ArchDmit"
	productionOffhostAlias  = "archczy"
	productionOffhostRoot   = "/home/arch/.local/state/lmm-api-production-backups"
	productionBootstrapRoot = "/var/lib/lmm-api-go/bootstrap"
)

type productionReleaseOptions struct {
	Repo               string
	Workspace          string
	AgeRecipientFile   string
	AgeIdentityFile    string
	Confirm            string
	RollbackPackage    string
	ObservationSeconds int
	RollbackSeconds    int
	ManualConfirm      bool
	PreserveEdgePolicy bool
	WithBackups        bool
}

type productionReleaseResult struct {
	DeploymentID     string `json:"deployment_id"`
	Version          string `json:"version"`
	Revision         string `json:"revision"`
	Status           string `json:"status"`
	TargetBackup     string `json:"target_backup"`
	ControllerBackup string `json:"controller_backup"`
	OffhostBackup    string `json:"offhost_backup"`
	RollbackTimer    string `json:"rollback_timer"`
	Workspace        string `json:"workspace"`
}

type productionReleaseRuntime struct {
	runner productionCommandRunner
	now    func() time.Time
}

func runProductionRelease(_ []string, _ io.Writer, stderr io.Writer) int {
	_, _ = fmt.Fprintf(stderr, "%s deploy production release is disabled: source-build and bundled-frontend activation are forbidden; prepare verified split lmm-api-go-bin and lmm-api-web-bin packages and use deploy production apply\n", ProgramName)
	return ExitUsage
}

func parseProductionReleaseOptions(args []string, stderr io.Writer) (productionReleaseOptions, error) {
	options := productionReleaseOptions{ObservationSeconds: 180, RollbackSeconds: 600}
	flags := flag.NewFlagSet("deploy production release", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&options.Repo, "repo", "", "clean api.lmm.best source checkout")
	flags.StringVar(&options.Workspace, "workspace", "", "marker-owned controller workspace")
	flags.StringVar(&options.AgeRecipientFile, "age-recipient-file", "", "age or SSH public recipient file")
	flags.StringVar(&options.AgeIdentityFile, "age-identity-file", "", "owner-protected age or SSH private identity")
	flags.StringVar(&options.Confirm, "confirm", "", "must equal api.lmm.best")
	flags.StringVar(&options.RollbackPackage, "rollback-package", "", "optional bootstrap package matching the currently installed release")
	flags.IntVar(&options.ObservationSeconds, "observation-seconds", options.ObservationSeconds, "automatic stability observation window (120-360)")
	flags.IntVar(&options.RollbackSeconds, "rollback-seconds", options.RollbackSeconds, "fixed automatic rollback deadline (must be 600)")
	flags.BoolVar(&options.ManualConfirm, "manual-confirm", false, "leave a healthy release awaiting an explicit confirm command")
	flags.BoolVar(&options.PreserveEdgePolicy, "preserve-edge-policy", false, "preserve the active nginx edge policy during activation")
	flags.BoolVar(&options.WithBackups, "with-backups", false, "create and verify target, controller, and off-host backups")
	flags.Usage = func() { writeDeployUsage(stderr) }
	if err := flags.Parse(args); err != nil {
		return productionReleaseOptions{}, err
	}
	if flags.NArg() != 0 {
		return productionReleaseOptions{}, errors.New("unexpected positional arguments")
	}
	if options.Confirm != "api.lmm.best" {
		return productionReleaseOptions{}, errors.New("--confirm must equal api.lmm.best")
	}
	for label, value := range map[string]*string{
		"--repo": &options.Repo, "--workspace": &options.Workspace,
	} {
		if *value == "" {
			return productionReleaseOptions{}, fmt.Errorf("%s is required", label)
		}
		clean, err := cleanAbsoluteNonRoot(*value)
		if err != nil {
			return productionReleaseOptions{}, fmt.Errorf("invalid %s: %w", label, err)
		}
		*value = clean
	}
	if options.WithBackups {
		for label, value := range map[string]*string{
			"--age-recipient-file": &options.AgeRecipientFile, "--age-identity-file": &options.AgeIdentityFile,
		} {
			if *value == "" {
				return productionReleaseOptions{}, fmt.Errorf("%s is required with --with-backups", label)
			}
			clean, err := cleanAbsoluteNonRoot(*value)
			if err != nil {
				return productionReleaseOptions{}, fmt.Errorf("invalid %s: %w", label, err)
			}
			*value = clean
		}
	}
	if options.RollbackPackage != "" {
		clean, err := cleanAbsoluteNonRoot(options.RollbackPackage)
		if err != nil {
			return productionReleaseOptions{}, fmt.Errorf("invalid --rollback-package: %w", err)
		}
		options.RollbackPackage = clean
	}
	if options.ObservationSeconds < 120 || options.ObservationSeconds > 360 {
		return productionReleaseOptions{}, errors.New("--observation-seconds must be between 120 and 360")
	}
	if options.RollbackSeconds != 600 {
		return productionReleaseOptions{}, errors.New("--rollback-seconds must be exactly 600")
	}
	return options, nil
}

func (runtime *productionReleaseRuntime) release(ctx context.Context, options productionReleaseOptions) (result productionReleaseResult, returnErr error) {
	if err := validateBuildRepository(options.Repo); err != nil {
		return productionReleaseResult{}, err
	}
	if err := validateBuildWorkspace(options.Workspace); err != nil {
		return productionReleaseResult{}, err
	}
	if options.WithBackups {
		for label, path := range map[string]string{"age recipient": options.AgeRecipientFile, "age identity": options.AgeIdentityFile} {
			info, err := os.Lstat(path)
			if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() == 0 {
				return productionReleaseResult{}, fmt.Errorf("%s file is missing, empty, or unsafe", label)
			}
		}
		identityInfo, _ := os.Stat(options.AgeIdentityFile)
		if identityInfo.Mode().Perm()&0o077 != 0 {
			return productionReleaseResult{}, errors.New("age identity must not grant group or other access")
		}
		identityStat, ok := identityInfo.Sys().(*syscall.Stat_t)
		if !ok || identityStat.Uid != uint32(os.Geteuid()) {
			return productionReleaseResult{}, errors.New("age identity must be owned by the invoking user")
		}
	}
	buildRuntime := &buildDeployRuntime{runner: runtime.runner, now: runtime.now}
	revision, version, _, err := buildRuntime.resolveBuildIdentity(ctx, buildDeployOptions{
		Repo: options.Repo, Workspace: options.Workspace, Production: true,
	})
	if err != nil {
		return productionReleaseResult{}, err
	}
	shortRevision := revision[:9]
	deploymentID := "go-" + shortRevision + "-" + runtime.now().UTC().Format("20060102T150405Z")
	if !productionIDPattern.MatchString(deploymentID) {
		return productionReleaseResult{}, errors.New("generated deployment ID is invalid")
	}
	artifactDir := filepath.Join(options.Workspace, "artifacts", deploymentID)
	build, err := buildRuntime.build(ctx, buildDeployOptions{
		Repo: options.Repo, Workspace: options.Workspace, OutputDir: artifactDir,
		Version: version, Production: true,
	})
	if err != nil {
		return productionReleaseResult{}, err
	}
	if build.Revision != revision || build.Version != version || build.Dirty {
		return productionReleaseResult{}, errors.New("production source identity changed during build")
	}
	if err := runtime.assertRemoteHost(ctx, productionTargetAlias, productionExpectedHost); err != nil {
		return productionReleaseResult{}, err
	}
	if options.WithBackups {
		if err := runtime.assertRemoteHost(ctx, productionOffhostAlias, productionOffhostAlias); err != nil {
			return productionReleaseResult{}, err
		}
	}
	remoteBootstrap := filepath.Join(productionBootstrapRoot, ProgramName+"-"+deploymentID)
	if _, err := runtime.ssh(ctx, productionTargetAlias, 2*time.Minute, "install", "-d", "-m0700", productionBootstrapRoot); err != nil {
		return productionReleaseResult{}, fmt.Errorf("prepare production bootstrap directory: %w", err)
	}
	if err := runtime.scpTo(ctx, build.Binary, productionTargetAlias, remoteBootstrap); err != nil {
		return productionReleaseResult{}, fmt.Errorf("stage production bootstrap CLI: %w", err)
	}
	if _, err := runtime.ssh(ctx, productionTargetAlias, 2*time.Minute, "chmod", "0700", "--", remoteBootstrap); err != nil {
		return productionReleaseResult{}, err
	}
	remoteWorkspace := filepath.Join("/var/lib/lmm-api-go/deploy-work", deploymentID)
	remoteStage := filepath.Join(remoteWorkspace, "staging")
	workspaceCreated := false
	activationDispatched := false
	defer func() {
		if returnErr == nil || !workspaceCreated || activationDispatched {
			return
		}
		_, _ = runtime.ssh(context.Background(), productionTargetAlias, 2*time.Minute,
			remoteBootstrap, "deploy", "production", "workspace", "abort", "--workspace", remoteWorkspace)
	}()
	if _, err := runtime.ssh(ctx, productionTargetAlias, 2*time.Minute,
		remoteBootstrap, "deploy", "production", "workspace", "create", "--deployment-id", deploymentID); err != nil {
		return productionReleaseResult{}, fmt.Errorf("create target deployment workspace: %w", err)
	}
	workspaceCreated = true

	rollbackPackage, rollbackSHA256, err := runtime.obtainRollbackPackage(ctx, options, remoteBootstrap, artifactDir)
	if err != nil {
		return productionReleaseResult{}, err
	}
	remoteCandidate := filepath.Join(remoteStage, filepath.Base(build.Package))
	remoteRollback := filepath.Join(remoteStage, filepath.Base(rollbackPackage))
	remoteProbe := filepath.Join(remoteStage, filepath.Base(build.Binary))
	transfers := []struct {
		local  string
		remote string
	}{
		{local: build.Package, remote: remoteCandidate},
		{local: rollbackPackage, remote: remoteRollback},
		{local: build.Binary, remote: remoteProbe},
	}
	remoteRecipient := filepath.Join(remoteStage, "backup-recipient.txt")
	if options.WithBackups {
		transfers = append(transfers, struct {
			local  string
			remote string
		}{local: options.AgeRecipientFile, remote: remoteRecipient})
	}
	for _, transfer := range transfers {
		if err := runtime.scpTo(ctx, transfer.local, productionTargetAlias, transfer.remote); err != nil {
			return productionReleaseResult{}, fmt.Errorf("stage %s: %w", filepath.Base(transfer.local), err)
		}
	}
	if _, err := runtime.ssh(ctx, productionTargetAlias, 2*time.Minute, "chmod", "0700", "--", remoteProbe); err != nil {
		return productionReleaseResult{}, err
	}
	remoteTargetBackup := ""
	controllerBackup := ""
	offhostBackup := ""
	if options.WithBackups {
		if _, err := runtime.ssh(ctx, productionTargetAlias, 12*time.Minute,
			remoteProbe, "deploy", "production", "backup", "create",
			"--workspace", remoteWorkspace,
			"--rollback-package", remoteRollback, "--rollback-sha256", rollbackSHA256,
			"--candidate-sha256", build.PackageSHA256, "--expected-version", version,
			"--git-revision", revision,
		); err != nil {
			return productionReleaseResult{}, fmt.Errorf("create target production backup: %w", err)
		}
		remoteTargetBackup = filepath.Join("/var/lib/lmm-api-go/deploy-backups", deploymentID)
		remoteControllerCopy := filepath.Join(remoteStage, "controller-copy")
		remoteOffhostCopy := filepath.Join(remoteStage, "offhost-copy")
		for role, output := range map[string]string{"controller": remoteControllerCopy, "off-host": remoteOffhostCopy} {
			if _, err := runtime.ssh(ctx, productionTargetAlias, 12*time.Minute,
				remoteProbe, "deploy", "production", "backup", "export",
				"--workspace", remoteWorkspace, "--role", role, "--output", output,
				"--age-recipient-file", remoteRecipient,
			); err != nil {
				return productionReleaseResult{}, fmt.Errorf("create %s backup copy: %w", role, err)
			}
		}
		backupRoot := filepath.Join(options.Workspace, "backups")
		for _, directory := range []string{backupRoot, filepath.Join(backupRoot, "target"), filepath.Join(backupRoot, "controller"), filepath.Join(backupRoot, "offhost")} {
			if err := ensureRealDirectory(directory, 0o700); err != nil {
				return productionReleaseResult{}, err
			}
		}
		targetMirror := filepath.Join(backupRoot, "target", deploymentID)
		controllerBackup = filepath.Join(backupRoot, "controller", deploymentID)
		offhostMirror := filepath.Join(backupRoot, "offhost", deploymentID)
		for remote, local := range map[string]string{
			remoteTargetBackup: targetMirror, remoteControllerCopy: controllerBackup, remoteOffhostCopy: offhostMirror,
		} {
			if err := runtime.scpFrom(ctx, productionTargetAlias, remote, local); err != nil {
				return productionReleaseResult{}, fmt.Errorf("retrieve backup %s: %w", filepath.Base(local), err)
			}
		}
		verificationRuntime := &productionRuntime{runner: runtime.runner, now: runtime.now, effectiveUID: os.Geteuid}
		verification, err := verificationRuntime.verifyExternalBackups(ctx, productionBackupVerifyOptions{
			Workspace: options.Workspace, Target: targetMirror, Controller: controllerBackup,
			Offhost: offhostMirror, AgeIdentityFile: options.AgeIdentityFile,
		})
		if err != nil {
			return productionReleaseResult{}, fmt.Errorf("verify three production backup copies: %w", err)
		}
		if verification.DeploymentID != deploymentID {
			return productionReleaseResult{}, errors.New("verified backup deployment identity mismatch")
		}
		if _, err := runtime.ssh(ctx, productionOffhostAlias, 2*time.Minute, "install", "-d", "-m0700", productionOffhostRoot); err != nil {
			return productionReleaseResult{}, fmt.Errorf("prepare off-host backup root: %w", err)
		}
		offhostBackup = filepath.Join(productionOffhostRoot, deploymentID)
		if err := runtime.scpToRecursive(ctx, offhostMirror, productionOffhostAlias, offhostBackup); err != nil {
			return productionReleaseResult{}, fmt.Errorf("publish off-host backup: %w", err)
		}
		offhostDigestOutput, err := runtime.ssh(ctx, productionOffhostAlias, 2*time.Minute, "sha256sum", filepath.Join(offhostBackup, "SHA256SUMS"))
		offhostDigestFields := strings.Fields(string(offhostDigestOutput))
		if err != nil || len(offhostDigestFields) == 0 || offhostDigestFields[0] != verification.OffhostDigest {
			return productionReleaseResult{}, errors.New("off-host backup changed during transfer")
		}
		if _, err := runtime.ssh(ctx, productionTargetAlias, 2*time.Minute,
			remoteProbe, "deploy", "production", "backup", "attest", "--workspace", remoteWorkspace,
			"--controller-digest", verification.ControllerDigest, "--offhost-digest", verification.OffhostDigest,
		); err != nil {
			return productionReleaseResult{}, fmt.Errorf("attest verified external backup copies: %w", err)
		}
	}
	deployUnit := "lmm-api-go-deploy-" + deploymentID
	activationDispatched = true
	applyArgs := []string{
		"systemd-run", "--quiet", "--wait", "--collect", "--unit", deployUnit,
		"--property=Type=oneshot", "--property=TimeoutStartSec=18min",
		remoteProbe, "deploy", "production", "apply",
		"--workspace", remoteWorkspace,
		"--package", remoteCandidate, "--package-sha256", build.PackageSHA256,
		"--rollback-package", remoteRollback, "--rollback-sha256", rollbackSHA256,
		"--probe-binary", remoteProbe, "--probe-binary-sha256", build.BinarySHA256,
		"--expected-version", version, "--frontend-index-sha256", build.FrontendIndexSHA256,
		"--activate-bundled-frontend",
		"--rollback-seconds", strconv.Itoa(options.RollbackSeconds),
		"--observation-seconds", strconv.Itoa(options.ObservationSeconds),
	}
	if options.WithBackups {
		applyArgs = append(applyArgs, "--with-backups", "--backup-dir", remoteTargetBackup)
	}
	if options.ManualConfirm {
		applyArgs = append(applyArgs, "--manual-confirm")
	}
	if options.PreserveEdgePolicy {
		applyArgs = append(applyArgs, "--preserve-edge-policy")
	}
	if _, err := runtime.ssh(ctx, productionTargetAlias, 20*time.Minute, applyArgs...); err != nil {
		return productionReleaseResult{}, fmt.Errorf("production activation failed or became transport-ambiguous; transaction retained: %w", err)
	}
	statusOutput, err := runtime.ssh(ctx, productionTargetAlias, 2*time.Minute,
		remoteProbe, "deploy", "production", "status", "--workspace", remoteWorkspace)
	if err != nil {
		return productionReleaseResult{}, fmt.Errorf("read final deployment status: %w", err)
	}
	var status productionStatus
	expectedPhase := "CONFIRMED"
	if options.ManualConfirm {
		expectedPhase = "AWAITING_CONFIRMATION"
	}
	if err := json.Unmarshal(statusOutput, &status); err != nil || status.Phase != expectedPhase || status.Version != version {
		return productionReleaseResult{}, fmt.Errorf("production release did not finish in %s: %s", expectedPhase, strings.TrimSpace(string(statusOutput)))
	}
	if status.Phase == "CONFIRMED" {
		_, _ = runtime.ssh(ctx, productionTargetAlias, 2*time.Minute, "rm", "-f", "--", remoteBootstrap)
	}
	result = productionReleaseResult{
		DeploymentID: deploymentID, Version: version, Revision: revision, Status: status.Phase,
		TargetBackup: remoteTargetBackup, ControllerBackup: controllerBackup, OffhostBackup: offhostBackup,
		RollbackTimer: status.RollbackTimer, Workspace: remoteWorkspace,
	}
	return result, nil
}

func (runtime *productionReleaseRuntime) obtainRollbackPackage(
	ctx context.Context,
	options productionReleaseOptions,
	remoteBootstrap, artifactDir string,
) (string, string, error) {
	installed, err := runtime.remoteGoPackage(ctx)
	if err != nil {
		return "", "", err
	}
	if options.RollbackPackage != "" {
		info, err := os.Lstat(options.RollbackPackage)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() == 0 {
			return "", "", errors.New("bootstrap rollback package is missing, empty, or unsafe")
		}
		identity, err := runtime.runner.Run(ctx, productionCommand{Name: commandPacman, Args: []string{"-Qp", options.RollbackPackage}})
		if err != nil || strings.TrimSpace(string(identity)) != installed {
			return "", "", errors.New("bootstrap rollback package does not match production")
		}
		digest, err := sha256File(options.RollbackPackage)
		return options.RollbackPackage, digest, err
	}
	packageOutput, err := runtime.ssh(ctx, productionTargetAlias, 2*time.Minute,
		remoteBootstrap, "deploy", "production", "package", "current")
	if err != nil {
		return "", "", fmt.Errorf("locate preserved production package; supply --rollback-package for one-time bootstrap if needed: %w", err)
	}
	var current productionPackageResult
	if err := json.Unmarshal(packageOutput, &current); err != nil || current.Identity != installed || !productionSHA256Pattern.MatchString(current.PackageSHA256) {
		return "", "", errors.New("production current-package response is invalid")
	}
	clean, err := cleanAbsoluteNonRoot(current.Package)
	if err != nil || (!pathWithinRoot("/var/lib/lmm-api-go/release-packages", clean) && !pathWithinRoot("/var/cache/pacman/pkg", clean)) {
		return "", "", errors.New("production current-package path is unsafe")
	}
	rollbackDir := filepath.Join(artifactDir, "rollback")
	if err := ensureRealDirectory(rollbackDir, 0o700); err != nil {
		return "", "", err
	}
	local := filepath.Join(rollbackDir, filepath.Base(clean))
	if err := runtime.scpFrom(ctx, productionTargetAlias, clean, local); err != nil {
		return "", "", fmt.Errorf("retrieve preserved rollback package: %w", err)
	}
	digest, err := sha256File(local)
	if err != nil || digest != current.PackageSHA256 {
		return "", "", errors.New("retrieved rollback package checksum mismatch")
	}
	identity, err := runtime.runner.Run(ctx, productionCommand{Name: commandPacman, Args: []string{"-Qp", local}})
	if err != nil || strings.TrimSpace(string(identity)) != installed {
		return "", "", errors.New("retrieved rollback package identity mismatch")
	}
	return local, digest, nil
}

func (runtime *productionReleaseRuntime) remoteGoPackage(ctx context.Context) (string, error) {
	installed := ""
	for _, name := range []string{productionAURPackageName, productionSourcePackageName} {
		output, err := runtime.ssh(ctx, productionTargetAlias, 2*time.Minute, "pacman", "-Q", name)
		if err != nil {
			continue
		}
		_, _, identity, err := parseProductionPackageIdentity(output)
		if err != nil {
			return "", errors.New("production Go package identity is invalid")
		}
		if installed != "" {
			if installed == identity {
				continue
			}
			return "", errors.New("multiple production Go packages are installed")
		}
		installed = identity
	}
	if installed == "" {
		return "", errors.New("production Go package was not found")
	}
	return installed, nil
}

func (runtime *productionReleaseRuntime) assertRemoteHost(ctx context.Context, alias, expected string) error {
	output, err := runtime.ssh(ctx, alias, 2*time.Minute, "hostnamectl", "--static")
	if err != nil {
		return fmt.Errorf("verify %s host identity: %w", alias, err)
	}
	if strings.TrimSpace(string(output)) != expected {
		return fmt.Errorf("%s host identity mismatch: got %q", alias, strings.TrimSpace(string(output)))
	}
	return nil
}

func (runtime *productionReleaseRuntime) ssh(ctx context.Context, alias string, timeout time.Duration, arguments ...string) ([]byte, error) {
	args := []string{"-o", "BatchMode=yes", alias}
	args = append(args, arguments...)
	return runtime.runner.Run(ctx, productionCommand{Name: commandSSH, Args: args, Timeout: timeout})
}

func (runtime *productionReleaseRuntime) scpTo(ctx context.Context, local, alias, remote string) error {
	_, err := runtime.runner.Run(ctx, productionCommand{Name: commandSCP, Args: []string{"-q", "-p", "--", local, alias + ":" + remote}, Timeout: 10 * time.Minute})
	return err
}

func (runtime *productionReleaseRuntime) scpToRecursive(ctx context.Context, local, alias, remote string) error {
	_, err := runtime.runner.Run(ctx, productionCommand{Name: commandSCP, Args: []string{"-q", "-p", "-r", "--", local, alias + ":" + remote}, Timeout: 10 * time.Minute})
	return err
}

func (runtime *productionReleaseRuntime) scpFrom(ctx context.Context, alias, remote, local string) error {
	if _, err := os.Lstat(local); !errors.Is(err, os.ErrNotExist) {
		return errors.New("local transfer destination already exists or is unsafe")
	}
	if err := ensureRealDirectory(filepath.Dir(local), 0o700); err != nil {
		return err
	}
	_, err := runtime.runner.Run(ctx, productionCommand{Name: commandSCP, Args: []string{"-q", "-p", "-r", "--", alias + ":" + remote, local}, Timeout: 10 * time.Minute})
	return err
}
