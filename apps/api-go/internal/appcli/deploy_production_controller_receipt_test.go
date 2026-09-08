//go:build !windows

package appcli

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func controllerReceiptFixture(t *testing.T) (productionFixture, productionReleasePlan, controllerBackupSet) {
	t.Helper()
	fixture := newProductionFixture(t)
	// The archive runner models both package binaries with the probe payload.
	// Make the on-disk N-1 fixture match that verified rollback payload too.
	payload, err := os.ReadFile(fixture.runner.probeBinary)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixture.runtime.paths.InstalledBinary, payload, 0o755); err != nil {
		t.Fatal(err)
	}
	public, err := initializeControllerBackupKey(fixture.workspace.root)
	if err != nil {
		t.Fatal(err)
	}
	plan := productionReleasePlan{Format: 6, DeploymentID: fixture.workspace.id, ExpectedHost: productionExpectedHost,
		ControllerWorkspace: fixture.workspace.root, WithBackups: true, BackupMode: "controller-only", ControllerBackupPublicKey: public,
		GoCandidate:  productionReleasePackagePlan{PackageSHA256: fixture.options.GoPackageSHA256},
		GoRollback:   productionReleasePackagePlan{PackageSHA256: fixture.options.GoRollbackSHA256, PayloadSHA256: mustHashFile(t, fixture.runtime.paths.InstalledBinary)},
		WebCandidate: productionReleasePackagePlan{PackageSHA256: fixture.options.WebPackageSHA256},
		WebRollback:  productionReleasePackagePlan{PackageSHA256: fixture.options.WebRollbackSHA256, PayloadSHA256: mustHashFile(t, fixture.runner.oldWebIndex)}}
	set := controllerBackupSet{Format: 1, DeploymentID: plan.DeploymentID, ExpectedHost: plan.ExpectedHost, CapturedUTC: *fixture.clock,
		DatabaseSchema: "public", EnvironmentSHA256: controllerBackupDigest(fixture.environment), Archives: make(map[string]controllerBackupArchive)}
	for _, kind := range controllerBackupKinds {
		set.Archives[kind] = controllerBackupArchive{CiphertextSHA256: controllerBackupDigest([]byte(kind + "cipher")), PlaintextSHA256: controllerBackupDigest([]byte(kind + "plain"))}
	}
	encoded, err := signedControllerBackupReceipt(plan, set, strings.Repeat("e", 64), strings.Repeat("f", 64), "prepare", "", *fixture.clock)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(fixture.workspace.stateDir, controllerBackupReceiptName)
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	fixture.options.BackupDir = ""
	fixture.options.Workspace = fixture.workspace.root
	fixture.options.ControllerBackup = controllerBackupBinding{PublicKey: public, PlanSHA256: strings.Repeat("f", 64), ReceiptPath: path, ReceiptSHA256: controllerBackupDigest(encoded)}
	return fixture, plan, set
}

func writeControllerConfirmation(t *testing.T, fixture productionFixture, plan productionReleasePlan, set controllerBackupSet, manifestHash string) {
	t.Helper()
	encoded, err := signedControllerBackupReceipt(plan, set, strings.Repeat("e", 64), strings.Repeat("f", 64), "confirm", manifestHash, *fixture.clock)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture.workspace.stateDir, controllerBackupConfirmationName), encoded, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestControllerOnlyNativeApplyAndFreshConfirmation(t *testing.T) {
	fixture, plan, set := controllerReceiptFixture(t)
	if _, err := fixture.runtime.apply(context.Background(), fixture.workspace, fixture.options); err != nil {
		t.Fatal(err)
	}
	manifest, err := fixture.runtime.readManifest(fixture.workspace)
	if err != nil || manifest.BackupEvidenceFormat != 3 || manifest.BackupDir != "" || manifest.TargetBackupSHA256 != "" || manifest.OffhostBackupSHA256 != "" {
		t.Fatalf("mixed or invalid manifest: %v", err)
	}
	if _, err := fixture.runtime.confirm(context.Background(), fixture.workspace); err == nil {
		t.Fatal("confirmation succeeded without fresh controller proof")
	}
	writeControllerConfirmation(t, fixture, plan, set, mustHashFile(t, fixture.workspace.manifestPath))
	if _, err := fixture.runtime.confirm(context.Background(), fixture.workspace); err != nil {
		t.Fatal(err)
	}
	for _, command := range fixture.runner.commands {
		if command.Name == commandAge || command.Name == commandPGDump || command.Name == commandPGRestore || command.Name == commandSSH || command.Name == commandSCP {
			t.Fatalf("target performed forbidden backup operation %s", command.Name)
		}
	}
}

func TestControllerOnlyRollbackDoesNotNeedReceiptOrController(t *testing.T) {
	fixture, _, _ := controllerReceiptFixture(t)
	if _, err := fixture.runtime.apply(context.Background(), fixture.workspace, fixture.options); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(fixture.options.ControllerBackup.ReceiptPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(fixture.workspace.stateDir, controllerBackupKeyName)); err != nil {
		t.Fatal(err)
	}
	status, err := fixture.runtime.rollback(context.Background(), fixture.workspace, "test-explicit-operator-request")
	if err != nil || status.Phase != "ROLLED_BACK" {
		t.Fatalf("receipt unavailable blocked manual recovery: status=%s error=%v", status.Phase, err)
	}
}

func TestControllerOnlyHistoricalProofAndFreshnessBoundaries(t *testing.T) {
	fixture, plan, set := controllerReceiptFixture(t)
	if _, err := fixture.runtime.apply(context.Background(), fixture.workspace, fixture.options); err != nil {
		t.Fatal(err)
	}
	manifest, err := fixture.runtime.readManifest(fixture.workspace)
	if err != nil {
		t.Fatal(err)
	}
	*fixture.clock = fixture.clock.Add(6 * time.Minute)
	if err := fixture.runtime.verifyControllerBackupEvidence(fixture.workspace, manifest, false); err != nil {
		t.Fatal(err)
	}
	if err := fixture.runtime.verifyControllerBackupEvidence(fixture.workspace, manifest, true); err == nil {
		t.Fatal("stale initial proof was accepted as fresh preparation")
	}
	writeControllerConfirmation(t, fixture, plan, set, strings.Repeat("0", 64))
	if err := fixture.runtime.verifyControllerBackupConfirmation(fixture.workspace, manifest); err == nil {
		t.Fatal("wrong manifest hash accepted")
	}
	writeControllerConfirmation(t, fixture, plan, set, mustHashFile(t, fixture.workspace.manifestPath))
	if err := fixture.runtime.verifyControllerBackupConfirmation(fixture.workspace, manifest); err != nil {
		t.Fatal(err)
	}
	*fixture.clock = fixture.clock.Add(5 * time.Minute)
	if err := fixture.runtime.verifyControllerBackupConfirmation(fixture.workspace, manifest); err == nil {
		t.Fatal("exact five-minute-old confirmation accepted")
	}
}

func TestControllerOnlyConfirmationCannotRewriteArchiveSnapshot(t *testing.T) {
	fixture, plan, set := controllerReceiptFixture(t)
	if _, err := fixture.runtime.apply(context.Background(), fixture.workspace, fixture.options); err != nil {
		t.Fatal(err)
	}
	manifest, err := fixture.runtime.readManifest(fixture.workspace)
	if err != nil {
		t.Fatal(err)
	}
	archive := set.Archives["application"]
	archive.PlaintextSHA256 = strings.Repeat("1", 64)
	set.Archives["application"] = archive
	writeControllerConfirmation(t, fixture, plan, set, mustHashFile(t, fixture.workspace.manifestPath))
	if err := fixture.runtime.verifyControllerBackupConfirmation(fixture.workspace, manifest); err == nil {
		t.Fatal("validly signed replacement archive was accepted")
	}
}

func TestControllerReceiptStrictSignedJSON(t *testing.T) {
	fixture, plan, _ := controllerReceiptFixture(t)
	original, err := os.ReadFile(fixture.options.ControllerBackup.ReceiptPath)
	if err != nil {
		t.Fatal(err)
	}
	var envelope controllerBackupEnvelope
	if err := json.Unmarshal(original, &envelope); err != nil {
		t.Fatal(err)
	}
	key, err := controllerBackupKey(plan)
	if err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func([]byte) []byte{
		"duplicate": func(data []byte) []byte {
			return bytes.Replace(data, []byte(`{"format":`), []byte(`{"format":1,"format":`), 1)
		},
		"case alias": func(data []byte) []byte { return bytes.Replace(data, []byte(`"purpose"`), []byte(`"Purpose"`), 1) },
		"missing empty field": func(data []byte) []byte {
			return bytes.Replace(data, []byte(`"deployment_manifest_sha256":"",`), nil, 1)
		},
		"null empty field": func(data []byte) []byte {
			return bytes.Replace(data, []byte(`"deployment_manifest_sha256":""`), []byte(`"deployment_manifest_sha256":null`), 1)
		},
		"non UTC":    func(data []byte) []byte { return bytes.Replace(data, []byte(`Z"`), []byte(`+01:00"`), 1) },
		"extra JSON": func(data []byte) []byte { return append(bytes.Clone(data), []byte(` {}`)...) },
	} {
		t.Run(name, func(t *testing.T) {
			payload := change(envelope.Payload)
			if bytes.Equal(payload, envelope.Payload) {
				t.Fatal("test mutation did not change payload")
			}
			forged := controllerBackupEnvelope{Format: 1, Payload: payload, Signature: hex.EncodeToString(ed25519.Sign(key, payload))}
			encoded, err := json.Marshal(forged)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := decodeSignedControllerBackupReceipt(encoded, plan.ControllerBackupPublicKey); err == nil {
				t.Fatal("invalid signed JSON was accepted")
			}
		})
	}
	envelope.Payload = append(envelope.Payload, ' ')
	tampered, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeSignedControllerBackupReceipt(tampered, plan.ControllerBackupPublicKey); err == nil {
		t.Fatal("changed exact payload bytes accepted without resigning")
	}
}

// Explicitly export only public, synthetic evidence for the Rust compatibility
// test. No private key, plaintext configuration or database data is exported.
func TestControllerReceiptExportInterop(t *testing.T) {
	root := os.Getenv("LMM_CONTROLLER_BACKUP_INTEROP_FIXTURE_DIR")
	if root == "" {
		t.Skip("explicit cross-language fixture export not selected")
	}
	if !filepath.IsAbs(root) {
		t.Fatal("interop destination must be absolute")
	}
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	fixture, _, _ := controllerReceiptFixture(t)
	if _, err := fixture.runtime.apply(context.Background(), fixture.workspace, fixture.options); err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{"receipt.json": fixture.options.ControllerBackup.ReceiptPath, "manifest.json": fixture.workspace.manifestPath} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestControllerReceiptRejectsWeakSigningKeys(t *testing.T) {
	for _, key := range []string{strings.Repeat("0", 64), "01" + strings.Repeat("00", 31), "ec" + strings.Repeat("ff", 30) + "7f"} {
		if _, err := controllerBackupVerificationKey(key); err == nil {
			t.Fatalf("accepted small-order Ed25519 key %s", key)
		}
	}
}

func TestControllerReceiptRetainsInitialEvidenceWithoutOverwrite(t *testing.T) {
	fixture, plan, set := controllerReceiptFixture(t)
	initial, err := os.ReadFile(fixture.options.ControllerBackup.ReceiptPath)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := signedControllerBackupReceipt(plan, set, strings.Repeat("e", 64), strings.Repeat("f", 64), "prepare", "", fixture.clock.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := retainInitialControllerReceipt(fixture.options.ControllerBackup.ReceiptPath, encoded, plan, fixture.clock.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(fixture.options.ControllerBackup.ReceiptPath)
	if err != nil || !bytes.Equal(initial, after) {
		t.Fatal("preparation retry overwrote immutable receipt")
	}
}

func TestNewProductionReleasePlansNeverStageArchiveOrPrivateKey(t *testing.T) {
	fixture, keyed, _ := controllerReceiptFixture(t)
	for _, selected := range []bool{false, true} {
		plan := testProductionReleasePlan(t, t.TempDir())
		plan.Format, plan.BackupMode, plan.WithBackups = 6, "disabled", selected
		plan.AgeRecipient = productionReleaseFilePlan{}
		if selected {
			plan.BackupMode, plan.ControllerBackupDir, plan.ControllerBackupPublicKey = "controller-only", filepath.Join(fixture.workspace.root, "collection"), keyed.ControllerBackupPublicKey
		}
		if err := validateProductionReleasePlan(plan); err != nil {
			t.Fatal(err)
		}
		planPath := filepath.Join(plan.ControllerWorkspace, productionReleasePlanFilename)
		if err := os.WriteFile(planPath, []byte("fixture-plan"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(plan.ControllerWorkspace, productionReleasePlanHashFilename), []byte("fixture-digest"), 0o600); err != nil {
			t.Fatal(err)
		}
		files, err := productionReleaseStageFiles(plan, planPath)
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range files {
			if file.Path == "" || strings.HasSuffix(file.Path, ".age") || strings.HasSuffix(file.Path, ".key") || strings.Contains(file.Path, "recipient") {
				t.Fatalf("private or backup file staged in new mode: %q", file.Path)
			}
		}
		state := productionReleaseControllerState{RemoteWorkspace: filepath.Join(defaultProductionPaths().WorkRoot, plan.DeploymentID), PlanSHA256: strings.Repeat("a", 64), ControllerReceiptSHA256: strings.Repeat("b", 64)}
		args := strings.Join((&productionReleaseRuntime{}).productionApplyArguments(plan, state), " ")
		if strings.Contains(args, "--backup-dir") || strings.Contains(args, ".age") || (!selected && strings.Contains(args, "--with-backups")) {
			t.Fatalf("new plan staged backup authority: %s", args)
		}
		if selected && !strings.Contains(args, "--controller-backup-receipt-sha256") {
			t.Fatal("selected plan lost its receipt binding")
		}
	}
}
