package appcli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNativeProductionWorkspaceCreateClaimsExactTransaction(t *testing.T) {
	root := t.TempDir()
	paths := defaultProductionPaths()
	paths.WorkRoot = filepath.Join(root, "work")
	paths.BackupRoot = filepath.Join(root, "backups")
	paths.GlobalLock = filepath.Join(root, "run", "deploy.lock")
	paths.TransactionLock = filepath.Join(root, "transaction.lock")
	paths.ExpectedHost = productionExpectedHost
	runtime := &productionRuntime{
		paths: paths, runner: &fakeProductionRunner{t: t}, now: func() time.Time {
			return time.Date(2026, 8, 10, 3, 0, 0, 0, time.UTC)
		}, sleep: func(time.Duration) {}, effectiveUID: func() int { return 0 },
		hostname: func() (string, error) { return productionExpectedHost, nil }, probeAttempts: 1, requiredOwnerUID: uint32(os.Getuid()),
	}
	result, err := runtime.createWorkspace(context.Background(), "go-native-workspace-test")
	if err != nil {
		t.Fatal(err)
	}
	if !result.TransactionSet || result.DeploymentID != "go-native-workspace-test" {
		t.Fatalf("workspace result=%#v", result)
	}
	workspace, err := runtime.openWorkspace(result.Workspace)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.validateTransactionLock(workspace); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.createWorkspace(context.Background(), "go-second-workspace-test"); err == nil || !strings.Contains(err.Error(), "transaction lock") {
		t.Fatalf("second workspace error=%v", err)
	}
}

func TestNativeProductionWorkspaceRejectsLegacyDynamicUserAlias(t *testing.T) {
	root := t.TempDir()
	stateRoot := filepath.Join(root, "private", "lmm-api-go")
	aliasRoot := filepath.Join(root, "var", "lib", "lmm-api-go")
	if err := os.MkdirAll(stateRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(aliasRoot), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(stateRoot, aliasRoot); err != nil {
		t.Fatal(err)
	}
	paths := defaultProductionPaths()
	paths.WorkRoot = filepath.Join(aliasRoot, "deploy-work")
	paths.BackupRoot = filepath.Join(aliasRoot, "deploy-backups")
	paths.GlobalLock = filepath.Join(root, "run", "deploy.lock")
	paths.TransactionLock = filepath.Join(aliasRoot, "deploy-transaction.lock")
	runtime := &productionRuntime{
		paths: paths, runner: &fakeProductionRunner{t: t}, now: time.Now,
		sleep: func(time.Duration) {}, effectiveUID: func() int { return 0 },
		hostname: func() (string, error) { return productionExpectedHost, nil }, probeAttempts: 1, requiredOwnerUID: uint32(os.Getuid()),
	}
	if _, err := runtime.createWorkspace(context.Background(), "go-managed-alias-test"); err == nil || (!strings.Contains(err.Error(), "symlink") && !strings.Contains(err.Error(), "deploy root")) {
		t.Fatalf("legacy DynamicUser alias should be rejected before workspace creation: %v", err)
	}
}

func TestNativeProductionWorkspaceRejectsSymlinkedWorkRoot(t *testing.T) {
	root := t.TempDir()
	paths := defaultProductionPaths()
	paths.WorkRoot = filepath.Join(root, "state", "deploy-work")
	paths.BackupRoot = filepath.Join(root, "state", "deploy-backups")
	paths.GlobalLock = filepath.Join(root, "run", "deploy.lock")
	paths.TransactionLock = filepath.Join(root, "state", "deploy-transaction.lock")
	runtime := &productionRuntime{
		paths: paths, runner: &fakeProductionRunner{t: t}, now: time.Now,
		sleep: func(time.Duration) {}, effectiveUID: func() int { return 0 },
		hostname: func() (string, error) { return productionExpectedHost, nil }, probeAttempts: 1, requiredOwnerUID: uint32(os.Getuid()),
	}
	result, err := runtime.createWorkspace(context.Background(), "go-symlink-workroot-test")
	if err != nil {
		t.Fatal(err)
	}
	displacedWorkRoot := filepath.Join(root, "attacker-controlled")
	if err := os.Rename(paths.WorkRoot, displacedWorkRoot); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(displacedWorkRoot, paths.WorkRoot); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.openWorkspace(result.Workspace); err == nil || !strings.Contains(err.Error(), "work root must be a real directory") {
		t.Fatalf("symlinked production work root error=%v", err)
	}
}

func TestNativeProductionBackupCapturesRollbackFrontendConfigAndPostgres(t *testing.T) {
	fixture := newProductionFixture(t)
	if err := os.RemoveAll(fixture.options.BackupDir); err != nil {
		t.Fatal(err)
	}
	result, err := fixture.runtime.createBackup(context.Background(), productionBackupOptions{
		Workspace:       fixture.workspace.root,
		RollbackPackage: fixture.options.GoRollbackPackage,
		RollbackSHA256:  fixture.options.GoRollbackSHA256,
		CandidateSHA256: strings.Repeat("a", 64),
		ExpectedVersion: fixture.options.ExpectedVersion,
		GitRevision:     strings.Repeat("b", 40),
	})
	if err != nil {
		t.Fatal(err)
	}
	expectedFrontendRelease := fixture.runner.oldVersion + "-1.g" + fixture.runner.oldRevision[:12]
	if result.BackupDir != fixture.options.BackupDir || result.FrontendRelease != expectedFrontendRelease || result.DatabaseEngine != "postgres" {
		t.Fatalf("backup result=%#v", result)
	}
	for _, name := range []string{
		"application.archive", "frontend.archive", "configuration.archive", "database.archive",
		"rollback.package", "manifest.env", "SHA256SUMS",
	} {
		info, err := os.Stat(filepath.Join(result.BackupDir, name))
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
			t.Errorf("backup entry %s info=%v err=%v", name, info, err)
		}
	}
	environment, err := fixture.runtime.validateBackupSet(context.Background(), fixture.workspace, result.BackupDir)
	if err != nil {
		t.Fatal(err)
	}
	if string(environment) != string(fixture.environment) {
		t.Fatalf("backed-up environment changed: %q", environment)
	}
	manifest, err := os.ReadFile(filepath.Join(result.BackupDir, "manifest.env"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(manifest), "password") || !strings.Contains(string(manifest), "database_engine=postgres") {
		t.Fatalf("backup manifest is unsafe or incomplete: %q", manifest)
	}
	recipient := filepath.Join(fixture.workspace.stagingDir, "backup-recipient.txt")
	if err := os.WriteFile(recipient, []byte("age1fixture-public-recipient\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	controllerOutput := filepath.Join(fixture.workspace.stagingDir, "controller-copy")
	controllerCopy, err := fixture.runtime.exportBackup(context.Background(), productionBackupExportOptions{
		Workspace: fixture.workspace.root, Role: "controller", Output: controllerOutput, AgeRecipientFile: recipient,
	})
	if err != nil {
		t.Fatal(err)
	}
	offhostOutput := filepath.Join(fixture.workspace.stagingDir, "offhost-copy")
	offhostCopy, err := fixture.runtime.exportBackup(context.Background(), productionBackupExportOptions{
		Workspace: fixture.workspace.root, Role: "off-host", Output: offhostOutput, AgeRecipientFile: recipient,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, copy := range []productionBackupExportResult{controllerCopy, offhostCopy} {
		if !copy.Encrypted || !productionSHA256Pattern.MatchString(copy.Digest) {
			t.Fatalf("external copy=%#v", copy)
		}
		for _, name := range []string{"configuration.age", "database.age", "SHA256SUMS", "manifest.env"} {
			info, err := os.Stat(filepath.Join(copy.Output, name))
			if err != nil || info.Mode().Perm() != 0o600 {
				t.Errorf("external %s info=%v err=%v", name, info, err)
			}
		}
	}
	identity := filepath.Join(fixture.workspace.stagingDir, "backup-identity.txt")
	if err := os.WriteFile(identity, []byte("AGE-SECRET-KEY-FIXTURE\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	productionUID := fixture.runtime.effectiveUID
	fixture.runtime.effectiveUID = os.Geteuid
	verification, err := fixture.runtime.verifyExternalBackups(context.Background(), productionBackupVerifyOptions{
		Workspace: fixture.workspace.root, Target: result.BackupDir,
		Controller: controllerOutput, Offhost: offhostOutput, AgeIdentityFile: identity,
	})
	if err != nil {
		t.Fatal(err)
	}
	proof := filepath.Join(fixture.workspace.stagingDir, "target-proof")
	if err := os.Mkdir(proof, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"manifest.env", "SHA256SUMS"} {
		if err := copyRegularFile(filepath.Join(result.BackupDir, name), filepath.Join(proof, name), 0o600, true); err != nil {
			t.Fatal(err)
		}
	}
	proofVerification, err := fixture.runtime.verifyExternalBackups(context.Background(), productionBackupVerifyOptions{
		Workspace: fixture.workspace.root, Target: proof,
		Controller: controllerOutput, Offhost: offhostOutput, AgeIdentityFile: identity,
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.runtime.effectiveUID = productionUID
	if !verification.TargetVerified || !verification.EncryptedCopies || verification.ControllerDigest != controllerCopy.Digest || verification.OffhostDigest != offhostCopy.Digest {
		t.Fatalf("backup verification=%#v", verification)
	}
	if proofVerification != verification {
		t.Fatalf("proof-only verification=%#v want=%#v", proofVerification, verification)
	}
	attestation, err := fixture.runtime.attestBackup(context.Background(), productionBackupAttestOptions{
		Workspace: fixture.workspace.root, ControllerDigest: verification.ControllerDigest, OffhostDigest: verification.OffhostDigest,
	})
	if err != nil {
		t.Fatal(err)
	}
	if attestation.DeploymentID != fixture.workspace.id || validateBackupAttestation(result.BackupDir, fixture.workspace.id) != nil {
		t.Fatalf("backup attestation=%#v", attestation)
	}
}

func TestProductionPackageCurrentFindsExactLegacyPreservedRelease(t *testing.T) {
	root := t.TempDir()
	legacyRoot := filepath.Join(root, "legacy-release-packages")
	if err := os.Mkdir(legacyRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	version := "0.1.34.r1146.gde02fda27"
	rollback := filepath.Join(legacyRoot, productionAURPackageName+"-"+version+"-1-x86_64.pkg.tar.zst")
	if err := os.WriteFile(rollback, []byte("exact legacy rollback package"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &fakeProductionRunner{
		t: t, goRollback: rollback, oldVersion: version, installedGoVersion: version,
	}
	runtime := productionRuntime{
		paths: productionPaths{
			ReleasePackages:       filepath.Join(root, "release-packages"),
			LegacyReleasePackages: legacyRoot,
			PackageCache:          filepath.Join(root, "package-cache"),
			GlobalLock:            filepath.Join(root, "run", "deploy.lock"),
			ExpectedHost:          "test-production-host",
		},
		runner:       runner,
		now:          time.Now,
		sleep:        func(time.Duration) {},
		effectiveUID: func() int { return 0 },
		hostname:     func() (string, error) { return "test-production-host", nil },
	}
	result, err := runtime.currentPackage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Package != rollback || result.Identity != productionAURPackageName+" "+version+"-1" || result.Source != "legacy-preserved-release" {
		t.Fatalf("legacy current package=%#v", result)
	}
	if digest, err := sha256File(rollback); err != nil || result.PackageSHA256 != digest {
		t.Fatalf("legacy package digest=%q want=%q err=%v", result.PackageSHA256, digest, err)
	}
}

func TestProductionBackupParserRejectsNonReleaseDigests(t *testing.T) {
	_, err := parseProductionBackupOptions([]string{
		"--workspace", "/var/lib/lmm-api-go/deploy-work/test",
		"--rollback-package", "/var/lib/lmm-api-go/deploy-work/test/staging/rollback.pkg.tar.zst",
		"--rollback-sha256", "bad",
		"--candidate-sha256", strings.Repeat("a", 64),
		"--expected-version", "0.1.1",
		"--git-revision", strings.Repeat("b", 40),
	}, os.Stderr)
	if err == nil {
		t.Fatalf("backup parser error=%v", err)
	}
}
