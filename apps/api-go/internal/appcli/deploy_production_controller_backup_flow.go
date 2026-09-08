package appcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// Only this metadata crosses the controller/target boundary. The imported
// archives and private age/signing keys are never part of the stage list.
func (runtime *productionReleaseRuntime) prepareControllerOnlyBackup(ctx context.Context, plan productionReleasePlan, state *productionReleaseControllerState, identity string) error {
	if plan.Format != productionReleasePlanFormat || plan.BackupMode != "controller-only" || !plan.WithBackups || state.DispatchAttempts != 0 {
		return errors.New("controller-only backup preparation requires an undispatched selected plan")
	}
	local := filepath.Join(plan.ControllerWorkspace, "state", controllerBackupReceiptName)
	if err := checkInitialControllerReceiptRetry(local, plan, state.ControllerReceiptSHA256, runtime.now()); err != nil {
		return err
	}
	verification := &productionRuntime{runner: runtime.runner, now: runtime.now, effectiveUID: os.Geteuid}
	set, setSHA, err := verification.verifyControllerBackupSet(ctx, plan, plan.ControllerBackupDir, identity)
	if err != nil {
		return err
	}
	encoded, err := signedControllerBackupReceipt(plan, set, setSHA, state.PlanSHA256, "prepare", "", runtime.now())
	if err != nil {
		return err
	}
	if err := retainInitialControllerReceipt(local, encoded, plan, runtime.now()); err != nil {
		return err
	}
	digest, err := sha256File(local)
	if err != nil {
		return err
	}
	if state.ControllerReceiptSHA256 != "" && state.ControllerReceiptSHA256 != digest {
		return errors.New("controller receipt differs from persisted preparation evidence")
	}
	if err := runtime.stageControllerReceipt(ctx, plan, *state, local, digest, false); err != nil {
		return err
	}
	state.ControllerBackup = plan.ControllerBackupDir
	state.ControllerReceiptSHA256 = digest
	state.Phase = productionReleasePhaseBackupsReady
	state.UpdatedUTC = utcSecond(runtime.now())
	return writeProductionReleaseControllerState(plan, *state)
}

func retainInitialControllerReceipt(name string, encoded []byte, plan productionReleasePlan, now time.Time) error {
	if _, err := os.Lstat(name); err == nil {
		initial, err := readControllerBackupReceipt(name, plan.ControllerBackupPublicKey, "", uint32(os.Geteuid()))
		if err != nil {
			return err
		}
		expected, err := decodeSignedControllerBackupReceipt(encoded, plan.ControllerBackupPublicKey)
		if err != nil {
			return err
		}
		// A retry may reuse, never rewrite, the initial receipt. All bindings
		// except the time spent re-verifying must still agree byte-for-byte.
		expected.VerifiedUTC = initial.VerifiedUTC
		expected.CapturedUTC = expected.CapturedUTC.UTC()
		initial.CapturedUTC = initial.CapturedUTC.UTC()
		left, leftErr := json.Marshal(initial)
		right, rightErr := json.Marshal(expected)
		if leftErr != nil || rightErr != nil || !bytes.Equal(left, right) {
			return errors.New("controller backup evidence changed during preparation retry")
		}
		return validateControllerBackupFreshness(initial, now)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	// Never expose a partially written initial receipt at its immutable name.
	// Unique failed temporary files remain as evidence; a later retry uses a
	// new temporary path rather than overwriting or trusting partial bytes.
	file, err := os.CreateTemp(filepath.Dir(name), ".controller-receipt.partial-*")
	if err != nil {
		return err
	}
	_, writeErr := file.Write(encoded)
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return err
	}
	if err := os.Link(file.Name(), name); err != nil {
		return err
	}
	if err := os.Remove(file.Name()); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(name))
}

func (runtime *productionReleaseRuntime) reverifyControllerOnlyBackup(ctx context.Context, plan productionReleasePlan, state productionReleaseControllerState, identity string) error {
	if plan.Format != productionReleasePlanFormat || plan.BackupMode != "controller-only" || !plan.WithBackups || state.ControllerBackup != plan.ControllerBackupDir || !productionSHA256Pattern.MatchString(state.ControllerReceiptSHA256) {
		return errors.New("controller-only confirmation lacks selected immutable evidence")
	}
	localInitial := filepath.Join(plan.ControllerWorkspace, "state", controllerBackupReceiptName)
	initial, err := readControllerBackupReceipt(localInitial, plan.ControllerBackupPublicKey, state.ControllerReceiptSHA256, uint32(os.Geteuid()))
	if err != nil {
		return err
	}
	verification := &productionRuntime{runner: runtime.runner, now: runtime.now, effectiveUID: os.Geteuid}
	set, setSHA, err := verification.verifyControllerBackupSet(ctx, plan, plan.ControllerBackupDir, identity)
	if err != nil {
		return err
	}
	manifest, manifestSHA, err := runtime.readControllerBackupRemoteManifest(ctx, plan, state, initial)
	if err != nil {
		return err
	}
	encoded, err := signedControllerBackupReceipt(plan, set, setSHA, state.PlanSHA256, "confirm", manifestSHA, runtime.now())
	if err != nil {
		return err
	}
	confirmation, err := decodeSignedControllerBackupReceipt(encoded, plan.ControllerBackupPublicKey)
	if err != nil {
		return err
	}
	if !sameControllerBackupSnapshot(initial, confirmation) {
		return errors.New("confirmation cannot replace the original backup snapshot")
	}
	if err := matchControllerBackupReceipt(confirmation, manifest); err != nil {
		return err
	}
	local := filepath.Join(plan.ControllerWorkspace, "state", controllerBackupConfirmationName)
	if err := writeAtomicRegularFile(local, encoded, 0o600); err != nil {
		return err
	}
	return runtime.stageControllerReceipt(ctx, plan, state, local, controllerBackupDigest(encoded), true)
}

func (runtime *productionReleaseRuntime) readControllerBackupRemoteManifest(ctx context.Context, plan productionReleasePlan, state productionReleaseControllerState, initial controllerBackupReceipt) (productionManifest, string, error) {
	var manifest productionManifest
	remote := filepath.Join(state.RemoteWorkspace, "state", productionManifestFilename)
	digest, err := runtime.remoteFileSHA256(ctx, plan.TargetAlias, remote)
	if err != nil {
		return manifest, "", err
	}
	data, err := runtime.ssh(ctx, plan.TargetAlias, 2*time.Minute, "head", "-c", "1048577", "--", remote)
	if err != nil || len(data) > 1<<20 || controllerBackupDigest(data) != digest || decodeControllerBackupJSON(data, &manifest) != nil {
		return manifest, "", errors.New("remote deployment manifest could not be verified")
	}
	workspace := productionWorkspace{id: plan.DeploymentID, stateDir: filepath.Join(state.RemoteWorkspace, "state")}
	if err := validateControllerBackupBinding(workspace, manifest); err != nil {
		return manifest, "", err
	}
	binding := manifest.ControllerOnlyBackup
	if manifest.Format != productionTransactionFormat || initial.Purpose != "prepare" || initial.PlanSHA256 != state.PlanSHA256 || binding.PublicKey != plan.ControllerBackupPublicKey || binding.ReceiptSHA256 != state.ControllerReceiptSHA256 {
		return manifest, "", errors.New("remote manifest changed the frozen controller backup binding")
	}
	if err := matchControllerBackupReceipt(initial, manifest); err != nil {
		return manifest, "", err
	}
	return manifest, digest, nil
}

func validateControllerOnlyBackupState(plan productionReleasePlan, state productionReleaseControllerState) error {
	if plan.Format == 5 {
		if state.ControllerReceiptSHA256 != "" {
			return errors.New("legacy controller state cannot contain controller-only receipts")
		}
		return nil
	}
	if !plan.WithBackups {
		if state.ControllerBackup != "" || state.ControllerReceiptSHA256 != "" || state.TargetBackup != "" || state.OffhostBackup != "" || state.Phase == productionReleasePhaseBackupsReady {
			return errors.New("disabled backup plan cannot contain backup state")
		}
		return nil
	}
	if state.TargetBackup != "" || state.OffhostBackup != "" {
		return errors.New("controller-only plans forbid target or off-host backup paths")
	}
	ready := state.ControllerBackup != "" || state.ControllerReceiptSHA256 != ""
	if ready && (state.ControllerBackup != plan.ControllerBackupDir || !productionSHA256Pattern.MatchString(state.ControllerReceiptSHA256)) {
		return errors.New("controller-only backup state is incomplete or differs from the plan")
	}
	if (state.DispatchAttempts > 0 || state.Phase == productionReleasePhaseBackupsReady || (state.Phase != productionReleasePhaseWorkspaceCreated && state.Phase != productionReleasePhaseStaged && state.Phase != "ABORTED")) && !ready {
		return errors.New("controller-only activation requires prepared receipt evidence")
	}
	return nil
}
