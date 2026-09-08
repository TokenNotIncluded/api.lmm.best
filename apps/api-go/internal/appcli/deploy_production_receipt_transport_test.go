//go:build !windows

package appcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

type receiptTransportRunner struct {
	files          map[string][]byte
	metadata       map[string]string
	failTransfer   bool
	raceFinal      []byte
	temporaryCount int
	transferCount  int
	commands       []productionCommand
}

func (r *receiptTransportRunner) Run(_ context.Context, command productionCommand) ([]byte, error) {
	r.commands = append(r.commands, command)
	if command.Name == commandSCP {
		if len(command.Args) != 5 {
			return nil, errors.New("unexpected SCP arguments")
		}
		remote, ok := strings.CutPrefix(command.Args[4], productionTargetAlias+":")
		if !ok {
			return nil, errors.New("unexpected SCP host")
		}
		data, err := os.ReadFile(command.Args[3])
		if err != nil {
			return nil, err
		}
		r.transferCount++
		if r.failTransfer {
			r.failTransfer = false
			r.files[remote] = bytes.Clone(data[:len(data)/2])
			return nil, errors.New("injected interrupted metadata transfer")
		}
		r.files[remote] = data
		return nil, nil
	}
	if command.Name != commandSSH || len(command.Args) < 4 || command.Args[2] != productionTargetAlias {
		return nil, errors.New("unexpected transport command or host")
	}
	args := command.Args[3:]
	switch args[0] {
	case "test":
		path := args[len(args)-1]
		_, exists := r.files[path]
		if slices.Contains(args, "-L") {
			return nil, nil
		}
		if exists != slices.Contains(args, "!") {
			return nil, nil
		}
		return nil, errors.New("test predicate is false")
	case "mktemp":
		r.temporaryCount++
		path := strings.TrimSuffix(args[2], "XXXXXXXXXXXX") + fmt.Sprintf("%012d", r.temporaryCount)
		r.files[path] = []byte{}
		return []byte(path + "\n"), nil
	case "stat":
		if len(args) != 5 || args[2] != "%u:%f:%h" {
			return nil, errors.New("receipt metadata must use locale-independent numeric file types")
		}
		path := args[len(args)-1]
		if _, exists := r.files[path]; !exists {
			return nil, os.ErrNotExist
		}
		mode := r.metadata[path]
		if mode == "" {
			mode = "0:8180:1"
		}
		return []byte(mode + "\n"), nil
	case "sha256sum":
		path := args[len(args)-1]
		data, exists := r.files[path]
		if !exists {
			return nil, os.ErrNotExist
		}
		return []byte(controllerBackupDigest(data) + "  " + path + "\n"), nil
	case "head":
		data, exists := r.files[args[len(args)-1]]
		if !exists {
			return nil, os.ErrNotExist
		}
		return bytes.Clone(data), nil
	case "mv":
		from, to := args[len(args)-2], args[len(args)-1]
		if r.raceFinal != nil {
			r.files[to] = bytes.Clone(r.raceFinal)
		}
		if _, exists := r.files[to]; exists && slices.Contains(args, "--no-clobber") {
			return nil, nil
		}
		data, exists := r.files[from]
		if !exists {
			return nil, os.ErrNotExist
		}
		r.files[to] = data
		delete(r.files, from)
		return nil, nil
	default:
		return nil, fmt.Errorf("unexpected remote metadata operation: %s", args[0])
	}
}

func receiptTransportFixture(t *testing.T) (productionFixture, productionReleasePlan, controllerBackupSet, productionReleaseControllerState, *receiptTransportRunner, *productionReleaseRuntime) {
	t.Helper()
	fixture, plan, set := controllerReceiptFixture(t)
	plan.TargetAlias = productionTargetAlias
	state := productionReleaseControllerState{RemoteWorkspace: filepath.Join(defaultProductionPaths().WorkRoot, plan.DeploymentID), PlanSHA256: fixture.options.ControllerBackup.PlanSHA256}
	runner := &receiptTransportRunner{files: make(map[string][]byte), metadata: make(map[string]string)}
	runtime := &productionReleaseRuntime{runner: runner, now: fixture.runtime.now}
	return fixture, plan, set, state, runner, runtime
}

type controllerReceiptIntegrationRunner struct {
	transport *receiptTransportRunner
	archives  *controllerImportRunner
}

func (runner *controllerReceiptIntegrationRunner) Run(ctx context.Context, command productionCommand) ([]byte, error) {
	if command.Name == commandSSH || command.Name == commandSCP {
		return runner.transport.Run(ctx, command)
	}
	return runner.archives.Run(ctx, command)
}

func TestControllerOnlyImportPrepareAndManifestBoundConfirmationFlow(t *testing.T) {
	fixture := newControllerImportFixture(t)
	plan := fixture.plan
	plan.Format, plan.WithBackups, plan.BackupMode = productionReleasePlanFormat, true, "controller-only"
	plan.TargetAlias, plan.ControllerBackupDir, plan.GoChanged, plan.WebChanged = productionTargetAlias, fixture.root, true, true
	plan.GoCandidate.PackageSHA256, plan.WebCandidate.PackageSHA256 = strings.Repeat("a", 64), strings.Repeat("b", 64)
	if err := os.Mkdir(filepath.Join(plan.ControllerWorkspace, "state"), 0o700); err != nil {
		t.Fatal(err)
	}
	publicKey, err := initializeControllerBackupKey(plan.ControllerWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	plan.ControllerBackupPublicKey = publicKey
	state := productionReleaseControllerState{Format: productionReleaseStateFormat, DeploymentID: plan.DeploymentID,
		PlanSHA256: strings.Repeat("c", 64), Phase: productionReleasePhaseStaged,
		RemoteWorkspace: filepath.Join(defaultProductionPaths().WorkRoot, plan.DeploymentID), UpdatedUTC: fixture.runtime.now()}
	transport := &receiptTransportRunner{files: make(map[string][]byte), metadata: make(map[string]string)}
	runner := &controllerReceiptIntegrationRunner{transport: transport, archives: fixture.runner}
	runtime := &productionReleaseRuntime{runner: runner, now: fixture.runtime.now}
	if err := runtime.prepareControllerOnlyBackup(context.Background(), plan, &state, fixture.identity); err != nil {
		t.Fatal(err)
	}
	if state.Phase != productionReleasePhaseBackupsReady || state.TargetBackup != "" || state.OffhostBackup != "" || state.ControllerReceiptSHA256 == "" {
		t.Fatal("invalid prepared controller-only state")
	}
	initialPath := filepath.Join(state.RemoteWorkspace, "state", controllerBackupReceiptName)
	initial, err := decodeSignedControllerBackupReceipt(transport.files[initialPath], publicKey)
	if err != nil {
		t.Fatal(err)
	}
	manifest := productionManifest{Format: productionTransactionFormat, DeploymentID: plan.DeploymentID,
		BackupsEnabled: true, BackupEvidenceFormat: controllerBackupEvidenceFormat,
		ControllerOnlyBackup: &controllerBackupBinding{PublicKey: publicKey, PlanSHA256: state.PlanSHA256, ReceiptPath: initialPath, ReceiptSHA256: state.ControllerReceiptSHA256},
		DatabaseSchema:       initial.DatabaseSchema, DatabaseBackupSHA256: initial.ArchivePlaintexts["database"], ControllerBackupSHA256: initial.BackupSetSHA256,
		EnvironmentRestoreSHA256: initial.EnvironmentSHA256,
		Go:                       productionPackageTransition{CandidateSHA256: plan.GoCandidate.PackageSHA256, RollbackSHA256: plan.GoRollback.PackageSHA256},
		Web:                      productionPackageTransition{CandidateSHA256: plan.WebCandidate.PackageSHA256, RollbackSHA256: plan.WebRollback.PackageSHA256},
		Frontend:                 productionFrontendTransition{OldIndexSHA256: plan.WebRollback.PayloadSHA256}}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(state.RemoteWorkspace, "state", productionManifestFilename)
	transport.files[manifestPath] = append(encoded, '\n')
	state.Phase = "AWAITING_CONFIRMATION"
	if err := runtime.reverifyControllerOnlyBackup(context.Background(), plan, state, fixture.identity); err != nil {
		t.Fatal(err)
	}
	confirmation, err := decodeSignedControllerBackupReceipt(transport.files[filepath.Join(state.RemoteWorkspace, "state", controllerBackupConfirmationName)], publicKey)
	if err != nil {
		t.Fatal(err)
	}
	if confirmation.Purpose != "confirm" || confirmation.DeploymentManifestSHA256 != controllerBackupDigest(transport.files[manifestPath]) || !sameControllerBackupSnapshot(initial, confirmation) {
		t.Fatal("confirmation lost its immutable snapshot/manifest binding")
	}
	if controllerBackupDigest(transport.files[initialPath]) != state.ControllerReceiptSHA256 {
		t.Fatal("confirmation rewrote initial receipt")
	}
	entries, err := os.ReadDir(filepath.Join(plan.ControllerWorkspace, "tmp"))
	if err != nil || len(entries) != 0 {
		t.Fatal("plaintext scratch survived controller confirmation")
	}
	if transport.transferCount != 2 {
		t.Fatalf("expected only two small receipt transfers, got %d", transport.transferCount)
	}
}

func TestControllerReceiptInterruptedTransferIsRetryableAndAtomic(t *testing.T) {
	fixture, plan, _, state, runner, runtime := receiptTransportFixture(t)
	remote := filepath.Join(state.RemoteWorkspace, "state", controllerBackupReceiptName)
	local, digest := fixture.options.ControllerBackup.ReceiptPath, fixture.options.ControllerBackup.ReceiptSHA256
	runner.failTransfer = true
	if err := runtime.stageControllerReceipt(context.Background(), plan, state, local, digest, false); err == nil {
		t.Fatal("interrupted transfer succeeded")
	}
	if _, exists := runner.files[remote]; exists {
		t.Fatal("partial data occupied immutable receipt path")
	}
	if len(runner.files) != 1 {
		t.Fatal("partial metadata was not retained")
	}
	if err := runtime.stageControllerReceipt(context.Background(), plan, state, local, digest, false); err != nil {
		t.Fatal(err)
	}
	if controllerBackupDigest(runner.files[remote]) != digest || runner.temporaryCount != 2 {
		t.Fatal("retry failed to publish the complete original evidence")
	}
	if err := runtime.stageControllerReceipt(context.Background(), plan, state, local, digest, false); err != nil {
		t.Fatal(err)
	}
	if runner.transferCount != 2 {
		t.Fatal("matching immutable receipt was unnecessarily transferred again")
	}
}

func TestControllerReceiptPublishCannotClobberConcurrentInitialEvidence(t *testing.T) {
	fixture, plan, _, state, runner, runtime := receiptTransportFixture(t)
	runner.raceFinal = []byte("concurrently published different evidence")
	if err := runtime.stageControllerReceipt(context.Background(), plan, state, fixture.options.ControllerBackup.ReceiptPath, fixture.options.ControllerBackup.ReceiptSHA256, false); err == nil {
		t.Fatal("concurrent mismatched receipt accepted")
	}
	remote := filepath.Join(state.RemoteWorkspace, "state", controllerBackupReceiptName)
	if !bytes.Equal(runner.files[remote], runner.raceFinal) {
		t.Fatal("immutable receipt overwritten during a publication race")
	}
}

func TestControllerConfirmationRetriesWithoutChangingInitialReceipt(t *testing.T) {
	fixture, plan, set, state, runner, runtime := receiptTransportFixture(t)
	if err := runtime.stageControllerReceipt(context.Background(), plan, state, fixture.options.ControllerBackup.ReceiptPath, fixture.options.ControllerBackup.ReceiptSHA256, false); err != nil {
		t.Fatal(err)
	}
	initial := filepath.Join(state.RemoteWorkspace, "state", controllerBackupReceiptName)
	original := bytes.Clone(runner.files[initial])
	local := filepath.Join(plan.ControllerWorkspace, "state", controllerBackupConfirmationName)
	remote := filepath.Join(state.RemoteWorkspace, "state", controllerBackupConfirmationName)
	for attempt := 0; attempt < 2; attempt++ {
		*fixture.clock = fixture.clock.Add(time.Second)
		writeControllerConfirmation(t, fixture, plan, set, strings.Repeat("a", 64))
		runner.failTransfer = true
		digest := mustHashFile(t, local)
		if err := runtime.stageControllerReceipt(context.Background(), plan, state, local, digest, true); err == nil {
			t.Fatal("interrupted confirmation succeeded")
		}
		if err := runtime.stageControllerReceipt(context.Background(), plan, state, local, digest, true); err != nil {
			t.Fatal(err)
		}
		if controllerBackupDigest(runner.files[remote]) != digest {
			t.Fatal("confirmation was not atomically updated")
		}
	}
	if !bytes.Equal(runner.files[initial], original) {
		t.Fatal("confirmation changed immutable initial evidence")
	}
}

func TestControllerReceiptRejectsUnsafeExistingDestination(t *testing.T) {
	fixture, plan, _, state, runner, runtime := receiptTransportFixture(t)
	remote := filepath.Join(state.RemoteWorkspace, "state", controllerBackupReceiptName)
	runner.files[remote] = []byte("occupied")
	for _, metadata := range []string{"0:8180:2", "0:81a4:1", "1000:8180:1", "0:a180:1", "0:8980:1"} {
		runner.metadata[remote] = metadata
		if err := runtime.stageControllerReceipt(context.Background(), plan, state, fixture.options.ControllerBackup.ReceiptPath, fixture.options.ControllerBackup.ReceiptSHA256, false); err == nil {
			t.Fatalf("accepted unsafe metadata %s", metadata)
		}
	}
	if runner.transferCount != 0 {
		t.Fatal("unsafe destination reached transfer")
	}
}

func TestExpiredPreparationStopsBeforeDecryptOrSSHWithoutRelabeling(t *testing.T) {
	fixture, plan, _, state, runner, runtime := receiptTransportFixture(t)
	original, err := os.ReadFile(fixture.options.ControllerBackup.ReceiptPath)
	if err != nil {
		t.Fatal(err)
	}
	*fixture.clock = fixture.clock.Add(5 * time.Minute)
	err = runtime.prepareControllerOnlyBackup(context.Background(), plan, &state, "unused-private-key")
	if err == nil || !strings.Contains(err.Error(), "new deployment ID") {
		t.Fatalf("expiry recovery message: %v", err)
	}
	if len(runner.commands) != 0 {
		t.Fatal("expired proof caused decryption or remote commands")
	}
	after, err := os.ReadFile(fixture.options.ControllerBackup.ReceiptPath)
	if err != nil || !bytes.Equal(original, after) {
		t.Fatal("expired receipt was relabeled")
	}
}

func TestInitialReceiptPublishLeavesPartialFilesOutsideImmutableName(t *testing.T) {
	fixture, plan, _ := controllerReceiptFixture(t)
	name := fixture.options.ControllerBackup.ReceiptPath
	encoded, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(name); err != nil {
		t.Fatal(err)
	}
	partial := filepath.Join(filepath.Dir(name), ".controller-receipt.partial-failed")
	if err := os.WriteFile(partial, encoded[:7], 0o600); err != nil {
		t.Fatal(err)
	}
	if err := retainInitialControllerReceipt(name, encoded, plan, *fixture.clock); err != nil {
		t.Fatal(err)
	}
	if _, err := readControllerBackupReceipt(name, plan.ControllerBackupPublicKey, fixture.options.ControllerBackup.ReceiptSHA256, uint32(os.Geteuid())); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(partial); err != nil {
		t.Fatal("failed partial metadata was removed")
	}
}

func TestControllerReceiptRejectsNumericPayloadArray(t *testing.T) {
	fixture, plan, _ := controllerReceiptFixture(t)
	encoded, err := os.ReadFile(fixture.options.ControllerBackup.ReceiptPath)
	if err != nil {
		t.Fatal(err)
	}
	var envelope controllerBackupEnvelope
	if err := json.Unmarshal(encoded, &envelope); err != nil {
		t.Fatal(err)
	}
	numbers := make([]int, len(envelope.Payload))
	for i, value := range envelope.Payload {
		numbers[i] = int(value)
	}
	array, err := json.Marshal(map[string]any{"format": 1, "payload": numbers, "signature": envelope.Signature})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeSignedControllerBackupReceipt(array, plan.ControllerBackupPublicKey); err == nil {
		t.Fatal("numeric array accepted instead of cross-language base64 string")
	}
}
