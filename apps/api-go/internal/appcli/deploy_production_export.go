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
	"strings"
	"syscall"
	"time"
)

const productionBackupAttestationFilename = "external-copies.json"

type productionBackupExportOptions struct {
	Workspace        string
	Role             string
	Output           string
	AgeRecipientFile string
}

type productionBackupExportResult struct {
	DeploymentID string `json:"deployment_id"`
	Role         string `json:"role"`
	Output       string `json:"output"`
	Digest       string `json:"digest"`
	Encrypted    bool   `json:"encrypted"`
}

type productionBackupAttestOptions struct {
	Workspace        string
	ControllerDigest string
	OffhostDigest    string
}

type productionBackupVerifyOptions struct {
	Workspace       string
	Target          string
	Controller      string
	Offhost         string
	AgeIdentityFile string
}

type productionBackupVerificationResult struct {
	DeploymentID     string `json:"deployment_id"`
	ControllerDigest string `json:"controller_digest"`
	OffhostDigest    string `json:"offhost_digest"`
	TargetVerified   bool   `json:"target_verified"`
	EncryptedCopies  bool   `json:"encrypted_copies_verified"`
}

type productionBackupAttestation struct {
	Format           int       `json:"format"`
	DeploymentID     string    `json:"deployment_id"`
	ControllerDigest string    `json:"controller_digest"`
	OffhostDigest    string    `json:"offhost_digest"`
	VerifiedUTC      time.Time `json:"verified_utc"`
}

func runProductionBackupExport(args []string, stdout, stderr io.Writer) int {
	options, err := parseProductionBackupExportOptions(args, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return ExitOK
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s deploy production backup export: %v\n", ProgramName, err)
		return ExitUsage
	}
	runtime := defaultProductionRuntime()
	result, err := runtime.exportBackup(context.Background(), options)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s deploy production backup export: %v\n", ProgramName, err)
		return ExitError
	}
	return writeJSONCommandResult(result, stdout, stderr, "production backup export")
}

func runProductionBackupAttest(args []string, stdout, stderr io.Writer) int {
	options, err := parseProductionBackupAttestOptions(args, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return ExitOK
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s deploy production backup attest: %v\n", ProgramName, err)
		return ExitUsage
	}
	runtime := defaultProductionRuntime()
	attestation, err := runtime.attestBackup(context.Background(), options)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s deploy production backup attest: %v\n", ProgramName, err)
		return ExitError
	}
	return writeJSONCommandResult(attestation, stdout, stderr, "production backup attest")
}

func runProductionBackupVerify(args []string, stdout, stderr io.Writer) int {
	options, err := parseProductionBackupVerifyOptions(args, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return ExitOK
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s deploy production backup verify: %v\n", ProgramName, err)
		return ExitUsage
	}
	runtime := &productionRuntime{runner: osProductionCommandRunner{}, now: time.Now, effectiveUID: os.Geteuid}
	result, err := runtime.verifyExternalBackups(context.Background(), options)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s deploy production backup verify: %v\n", ProgramName, err)
		return ExitError
	}
	return writeJSONCommandResult(result, stdout, stderr, "production backup verify")
}

func parseProductionBackupExportOptions(args []string, stderr io.Writer) (productionBackupExportOptions, error) {
	options := productionBackupExportOptions{}
	flags := flag.NewFlagSet("deploy production backup export", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&options.Workspace, "workspace", "", "marker-owned target deployment workspace")
	flags.StringVar(&options.Role, "role", "", "external copy role: controller or off-host")
	flags.StringVar(&options.Output, "output", "", "new protected external-copy directory")
	flags.StringVar(&options.AgeRecipientFile, "age-recipient-file", "", "age or SSH public recipient file")
	flags.Usage = func() { writeDeployUsage(stderr) }
	if err := flags.Parse(args); err != nil {
		return productionBackupExportOptions{}, err
	}
	if flags.NArg() != 0 {
		return productionBackupExportOptions{}, errors.New("unexpected positional arguments")
	}
	if options.Role != "controller" && options.Role != "off-host" {
		return productionBackupExportOptions{}, errors.New("--role must be controller or off-host")
	}
	for label, value := range map[string]*string{
		"--workspace": &options.Workspace, "--output": &options.Output, "--age-recipient-file": &options.AgeRecipientFile,
	} {
		if *value == "" {
			return productionBackupExportOptions{}, fmt.Errorf("%s is required", label)
		}
		clean, err := cleanAbsoluteNonRoot(*value)
		if err != nil {
			return productionBackupExportOptions{}, fmt.Errorf("invalid %s: %w", label, err)
		}
		*value = clean
	}
	if options.Output == "/tmp" || strings.HasPrefix(options.Output, "/tmp/") || options.Output == "/var/tmp" || strings.HasPrefix(options.Output, "/var/tmp/") {
		return productionBackupExportOptions{}, errors.New("external backup output must use persistent storage")
	}
	return options, nil
}

func parseProductionBackupAttestOptions(args []string, stderr io.Writer) (productionBackupAttestOptions, error) {
	options := productionBackupAttestOptions{}
	flags := flag.NewFlagSet("deploy production backup attest", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&options.Workspace, "workspace", "", "marker-owned target deployment workspace")
	flags.StringVar(&options.ControllerDigest, "controller-digest", "", "verified controller-copy digest")
	flags.StringVar(&options.OffhostDigest, "offhost-digest", "", "verified off-host-copy digest")
	flags.Usage = func() { writeDeployUsage(stderr) }
	if err := flags.Parse(args); err != nil {
		return productionBackupAttestOptions{}, err
	}
	if flags.NArg() != 0 {
		return productionBackupAttestOptions{}, errors.New("unexpected positional arguments")
	}
	workspace, err := cleanAbsoluteNonRoot(options.Workspace)
	if err != nil {
		return productionBackupAttestOptions{}, fmt.Errorf("invalid --workspace: %w", err)
	}
	options.Workspace = workspace
	if !productionSHA256Pattern.MatchString(options.ControllerDigest) || !productionSHA256Pattern.MatchString(options.OffhostDigest) {
		return productionBackupAttestOptions{}, errors.New("controller and off-host digests must be lowercase SHA-256 values")
	}
	return options, nil
}

func parseProductionBackupVerifyOptions(args []string, stderr io.Writer) (productionBackupVerifyOptions, error) {
	options := productionBackupVerifyOptions{}
	flags := flag.NewFlagSet("deploy production backup verify", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&options.Workspace, "workspace", "", "marker-owned controller verification workspace")
	flags.StringVar(&options.Target, "target", "", "plain protected target backup copy")
	flags.StringVar(&options.Controller, "controller", "", "encrypted controller backup copy")
	flags.StringVar(&options.Offhost, "offhost", "", "encrypted off-host backup copy")
	flags.StringVar(&options.AgeIdentityFile, "age-identity-file", "", "owner-protected age or SSH private identity")
	flags.Usage = func() { writeDeployUsage(stderr) }
	if err := flags.Parse(args); err != nil {
		return productionBackupVerifyOptions{}, err
	}
	if flags.NArg() != 0 {
		return productionBackupVerifyOptions{}, errors.New("unexpected positional arguments")
	}
	for label, value := range map[string]*string{
		"--workspace": &options.Workspace, "--target": &options.Target,
		"--controller": &options.Controller, "--offhost": &options.Offhost,
		"--age-identity-file": &options.AgeIdentityFile,
	} {
		if *value == "" {
			return productionBackupVerifyOptions{}, fmt.Errorf("%s is required", label)
		}
		clean, err := cleanAbsoluteNonRoot(*value)
		if err != nil {
			return productionBackupVerifyOptions{}, fmt.Errorf("invalid %s: %w", label, err)
		}
		*value = clean
	}
	return options, nil
}

func (runtime *productionRuntime) exportBackup(ctx context.Context, options productionBackupExportOptions) (productionBackupExportResult, error) {
	if err := runtime.assertProductionMutation(); err != nil {
		return productionBackupExportResult{}, err
	}
	workspace, err := runtime.openWorkspace(options.Workspace)
	if err != nil {
		return productionBackupExportResult{}, err
	}
	var result productionBackupExportResult
	err = runtime.withGlobalLock(ctx, func() error {
		if err := runtime.validateTransactionLock(workspace); err != nil {
			return err
		}
		backupDir := filepath.Join(runtime.paths.BackupRoot, workspace.id)
		if _, err := runtime.validateBackupSet(ctx, workspace, backupDir); err != nil {
			return err
		}
		recipientInfo, err := os.Lstat(options.AgeRecipientFile)
		if err != nil || recipientInfo.Mode()&os.ModeSymlink != 0 || !recipientInfo.Mode().IsRegular() || recipientInfo.Size() == 0 {
			return errors.New("age recipient file is missing, empty, or unsafe")
		}
		if _, err := os.Lstat(options.Output); !errors.Is(err, os.ErrNotExist) {
			return errors.New("external backup output already exists or is unsafe")
		}
		parent := filepath.Dir(options.Output)
		if err := requireRealDirectory(parent); err != nil {
			return fmt.Errorf("external backup parent is missing or unsafe: %w", err)
		}
		stage, err := os.MkdirTemp(parent, "."+filepath.Base(options.Output)+".*.stage")
		if err != nil {
			return err
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
		for _, name := range []string{"application.archive", "frontend.archive", "rollback.package"} {
			if err := copyRegularFile(filepath.Join(backupDir, name), filepath.Join(stage, name), 0o600, true); err != nil {
				return err
			}
		}
		for source, destination := range map[string]string{
			"configuration.archive": "configuration.age",
			"database.archive":      "database.age",
		} {
			output := filepath.Join(stage, destination)
			if _, err := runtime.runner.Run(ctx, productionCommand{Name: commandAge, Args: []string{"--encrypt", "--recipients-file", options.AgeRecipientFile, "--output", output, filepath.Join(backupDir, source)},
				Timeout: 10 * time.Minute, Sensitive: true}); err != nil {
				return fmt.Errorf("encrypt %s for %s copy: %w", source, options.Role, err)
			}
			info, err := os.Lstat(output)
			if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() == 0 {
				return fmt.Errorf("age did not produce a safe %s", destination)
			}
			if err := os.Chmod(output, 0o600); err != nil {
				return err
			}
		}
		targetDigest, err := sha256File(filepath.Join(backupDir, "SHA256SUMS"))
		if err != nil {
			return err
		}
		manifest := fmt.Sprintf(
			"format=1\ndeployment_id=%s\ncopy_role=%s\ncreated_at_utc=%s\ntarget_checksum_digest=%s\nconfiguration_encrypted=true\ndatabase_encrypted=true\n",
			workspace.id, options.Role, utcSecond(runtime.now()).Format(time.RFC3339), targetDigest,
		)
		if err := writeAtomicRegularFile(filepath.Join(stage, "manifest.env"), []byte(manifest), 0o600); err != nil {
			return err
		}
		names := []string{"application.archive", "frontend.archive", "rollback.package", "configuration.age", "database.age", "manifest.env"}
		var checksums strings.Builder
		for _, name := range names {
			digest, err := sha256File(filepath.Join(stage, name))
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(&checksums, "%s  %s\n", digest, name)
		}
		if err := writeAtomicRegularFile(filepath.Join(stage, "SHA256SUMS"), []byte(checksums.String()), 0o600); err != nil {
			return err
		}
		copyDigest, err := sha256File(filepath.Join(stage, "SHA256SUMS"))
		if err != nil {
			return err
		}
		if err := os.Rename(stage, options.Output); err != nil {
			return err
		}
		published = true
		if err := syncDirectory(parent); err != nil {
			return err
		}
		result = productionBackupExportResult{
			DeploymentID: workspace.id, Role: options.Role, Output: options.Output,
			Digest: copyDigest, Encrypted: true,
		}
		return nil
	})
	return result, err
}

func (runtime *productionRuntime) attestBackup(ctx context.Context, options productionBackupAttestOptions) (productionBackupAttestation, error) {
	if err := runtime.assertProductionMutation(); err != nil {
		return productionBackupAttestation{}, err
	}
	workspace, err := runtime.openWorkspace(options.Workspace)
	if err != nil {
		return productionBackupAttestation{}, err
	}
	attestation := productionBackupAttestation{
		Format: 1, DeploymentID: workspace.id,
		ControllerDigest: options.ControllerDigest, OffhostDigest: options.OffhostDigest,
		VerifiedUTC: utcSecond(runtime.now()),
	}
	err = runtime.withGlobalLock(ctx, func() error {
		if err := runtime.validateTransactionLock(workspace); err != nil {
			return err
		}
		backupDir := filepath.Join(runtime.paths.BackupRoot, workspace.id)
		if _, err := runtime.validateBackupSet(ctx, workspace, backupDir); err != nil {
			return err
		}
		path := filepath.Join(backupDir, productionBackupAttestationFilename)
		encoded, err := json.MarshalIndent(attestation, "", "  ")
		if err != nil {
			return err
		}
		if existing, err := readPrivateRegularFile(path, 64<<10); err == nil {
			var current productionBackupAttestation
			if json.Unmarshal(existing, &current) != nil || current.DeploymentID != attestation.DeploymentID ||
				current.ControllerDigest != attestation.ControllerDigest || current.OffhostDigest != attestation.OffhostDigest {
				return errors.New("backup attestation already exists with different evidence")
			}
			attestation = current
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return writeAtomicRegularFile(path, append(encoded, '\n'), 0o600)
	})
	return attestation, err
}

func validateBackupAttestation(backupDir, deploymentID string) error {
	content, err := readPrivateRegularFile(filepath.Join(backupDir, productionBackupAttestationFilename), 64<<10)
	if err != nil {
		return fmt.Errorf("external backup attestation is missing or unsafe: %w", err)
	}
	var attestation productionBackupAttestation
	if err := json.Unmarshal(content, &attestation); err != nil {
		return fmt.Errorf("decode external backup attestation: %w", err)
	}
	if attestation.Format != 1 || attestation.DeploymentID != deploymentID ||
		!productionSHA256Pattern.MatchString(attestation.ControllerDigest) ||
		!productionSHA256Pattern.MatchString(attestation.OffhostDigest) || attestation.VerifiedUTC.IsZero() {
		return errors.New("external backup attestation is incomplete or belongs to another deployment")
	}
	return nil
}

func (runtime *productionRuntime) verifyExternalBackups(ctx context.Context, options productionBackupVerifyOptions) (productionBackupVerificationResult, error) {
	if err := validateBuildWorkspace(options.Workspace); err != nil {
		return productionBackupVerificationResult{}, err
	}
	identityInfo, err := os.Lstat(options.AgeIdentityFile)
	if err != nil || identityInfo.Mode()&os.ModeSymlink != 0 || !identityInfo.Mode().IsRegular() || identityInfo.Size() == 0 {
		return productionBackupVerificationResult{}, errors.New("age identity is missing, empty, or unsafe")
	}
	identityStat, ok := identityInfo.Sys().(*syscall.Stat_t)
	if !ok || identityInfo.Mode().Perm()&0o077 != 0 || int(identityStat.Uid) != runtime.effectiveUID() {
		return productionBackupVerificationResult{}, errors.New("age identity must be owner-controlled and inaccessible to group or other users")
	}
	deploymentID, targetChecksumDigest, targetDigests, err := runtime.readTargetBackupProof(ctx, options.Target)
	if err != nil {
		return productionBackupVerificationResult{}, err
	}
	temporaryRoot := filepath.Join(options.Workspace, "tmp")
	if err := ensureRealDirectory(temporaryRoot, 0o700); err != nil {
		return productionBackupVerificationResult{}, err
	}
	verificationRoot, err := os.MkdirTemp(temporaryRoot, "backup-verify.*")
	if err != nil {
		return productionBackupVerificationResult{}, err
	}
	defer os.RemoveAll(verificationRoot)
	copyDigests := make(map[string]string, 2)
	for role, root := range map[string]string{"controller": options.Controller, "off-host": options.Offhost} {
		digest, err := runtime.verifyExternalBackupCopy(ctx, role, root, options.AgeIdentityFile, verificationRoot, deploymentID, targetChecksumDigest, targetDigests)
		if err != nil {
			return productionBackupVerificationResult{}, err
		}
		copyDigests[role] = digest
	}
	return productionBackupVerificationResult{
		DeploymentID: deploymentID, ControllerDigest: copyDigests["controller"], OffhostDigest: copyDigests["off-host"],
		TargetVerified: true, EncryptedCopies: true,
	}, nil
}

func (runtime *productionRuntime) readTargetBackupProof(ctx context.Context, root string) (string, string, map[string]string, error) {
	names := []string{"application.archive", "frontend.archive", "configuration.archive", "database.archive", "rollback.package"}
	digests, err := readNamedChecksums(root, names)
	if err != nil {
		return "", "", nil, fmt.Errorf("read target backup proof: %w", err)
	}
	manifest, err := readPrivateRegularFile(filepath.Join(root, "manifest.env"), 64<<10)
	if err != nil {
		return "", "", nil, err
	}
	values, err := parseSimpleManifest(manifest)
	if err != nil || !productionIDPattern.MatchString(values["deployment_id"]) {
		return "", "", nil, errors.New("target backup manifest has an invalid deployment ID")
	}
	present := 0
	for _, name := range names {
		info, entryErr := os.Lstat(filepath.Join(root, name))
		if entryErr == nil {
			if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() == 0 {
				return "", "", nil, fmt.Errorf("target backup entry is unsafe: %s", name)
			}
			present++
		} else if !errors.Is(entryErr, os.ErrNotExist) {
			return "", "", nil, entryErr
		}
	}
	if present != 0 && present != len(names) {
		return "", "", nil, errors.New("target backup proof contains a partial plaintext backup")
	}
	if present == len(names) {
		if err := verifyNamedChecksums(root, names); err != nil {
			return "", "", nil, fmt.Errorf("verify target backup: %w", err)
		}
		if _, err := runtime.runner.Run(ctx, productionCommand{Name: commandPGRestore, Args: []string{"--list", filepath.Join(root, "database.archive")}}); err != nil {
			return "", "", nil, fmt.Errorf("validate target PostgreSQL backup: %w", err)
		}
		if _, err := environmentFromConfigurationArchive(filepath.Join(root, "configuration.archive")); err != nil {
			return "", "", nil, fmt.Errorf("validate target configuration backup: %w", err)
		}
	}
	checksumDigest, err := sha256File(filepath.Join(root, "SHA256SUMS"))
	if err != nil {
		return "", "", nil, err
	}
	return values["deployment_id"], checksumDigest, digests, nil
}

func (runtime *productionRuntime) verifyExternalBackupCopy(
	ctx context.Context,
	role, root, identity, temporaryRoot, deploymentID, targetChecksumDigest string,
	targetDigests map[string]string,
) (string, error) {
	if err := verifyNamedChecksums(root, []string{
		"application.archive", "frontend.archive", "rollback.package", "configuration.age", "database.age", "manifest.env",
	}); err != nil {
		return "", fmt.Errorf("verify %s backup checksums: %w", role, err)
	}
	manifest, err := readPrivateRegularFile(filepath.Join(root, "manifest.env"), 64<<10)
	if err != nil {
		return "", err
	}
	values, err := parseSimpleManifest(manifest)
	if err != nil || values["deployment_id"] != deploymentID || values["copy_role"] != role ||
		values["target_checksum_digest"] != targetChecksumDigest || values["configuration_encrypted"] != "true" || values["database_encrypted"] != "true" {
		return "", fmt.Errorf("%s backup manifest is incomplete or mismatched", role)
	}
	for _, name := range []string{"application.archive", "frontend.archive", "rollback.package"} {
		copyDigest, err := sha256File(filepath.Join(root, name))
		if err != nil || copyDigest != targetDigests[name] {
			return "", fmt.Errorf("%s backup plaintext artifact mismatch: %s", role, name)
		}
	}
	for encrypted, plain := range map[string]string{"configuration.age": "configuration.archive", "database.age": "database.archive"} {
		output := filepath.Join(temporaryRoot, role+"-"+plain)
		if _, err := runtime.runner.Run(ctx, productionCommand{Name: commandAge, Args: []string{"--decrypt", "--identity", identity, "--output", output, filepath.Join(root, encrypted)},
			Timeout: 10 * time.Minute, Sensitive: true}); err != nil {
			return "", fmt.Errorf("decrypt %s backup %s: %w", role, encrypted, err)
		}
		decryptedDigest, err := sha256File(output)
		if err != nil || decryptedDigest != targetDigests[plain] {
			return "", fmt.Errorf("%s decrypted backup mismatch: %s", role, plain)
		}
		if plain == "database.archive" {
			if _, err := runtime.runner.Run(ctx, productionCommand{Name: commandPGRestore, Args: []string{"--list", output}}); err != nil {
				return "", fmt.Errorf("validate %s PostgreSQL backup: %w", role, err)
			}
		} else if _, err := environmentFromConfigurationArchive(output); err != nil {
			return "", fmt.Errorf("validate %s configuration backup: %w", role, err)
		}
	}
	return sha256File(filepath.Join(root, "SHA256SUMS"))
}
