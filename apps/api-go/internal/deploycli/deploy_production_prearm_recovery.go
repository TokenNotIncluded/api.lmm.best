package deploycli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// FAILED_PREARM can leave a late-ready holder alive after apply releases its
// transaction lock. Only prove and close that unchanged transaction; never
// route it through the package reinstall or writer-stop rollback path.
func (runtime *productionRuntime) rollbackFailedPrearm(ctx context.Context, workspace productionWorkspace, manifest productionManifest, status productionStatus, reason string) (productionStatus, error) {
	if !productionReasonPattern.MatchString(reason) {
		return productionStatus{}, errors.New("rollback reason is not audit-safe")
	}
	if status.Reason != "activation-preparation-failed" || status.Version != manifest.ExpectedVersion ||
		!manifest.Go.Changed || manifest.SchemaMode != productionSchemaModeVerifyExisting || manifest.MerchantStoreWriter == nil ||
		manifest.BillingGate != nil || !manifest.ObservationStartedUTC.IsZero() || manifest.MaintenanceHandoff != nil {
		return productionStatus{}, errors.New("failed prearm recovery requires an unchanged ordinary writer before admission")
	}
	marker, err := readPrivateRegularFile(filepath.Join(workspace.root, productionWorkspaceMarker), 16<<10)
	if err != nil {
		return productionStatus{}, err
	}
	values, err := parseSimpleManifest(marker)
	created, parseErr := time.Parse(time.RFC3339, values["created_at_utc"])
	if err != nil || parseErr != nil || values["deployment_id"] != workspace.id || values["role"] != "target" ||
		created.IsZero() || status.UpdatedUTC.IsZero() || created.After(status.UpdatedUTC) || status.UpdatedUTC.After(runtime.now()) {
		return productionStatus{}, errors.New("failed prearm workspace has no original preparation time evidence")
	}
	if err := runtime.reclaimPrearmTransactionLock(workspace); err != nil {
		return productionStatus{}, err
	}
	// Any refusal retains this exact transaction lock and the original phase.
	if err := runtime.verifyRollbackManifestArchives(ctx, manifest); err != nil {
		return productionStatus{}, err
	}
	if err := validateMemoryOverrides(runtime.paths.DropInDir); err != nil {
		return productionStatus{}, err
	}
	if err := runtime.verifyManifestInstalled(ctx, manifest, true); err != nil {
		return productionStatus{}, fmt.Errorf("failed prearm installed N-1 evidence: %w", err)
	}
	currentEnvironment, err := readPrivateRegularFile(filepath.Join(runtime.paths.ConfigDir, "lmm-api-go.env"), 1<<20)
	if err != nil {
		return productionStatus{}, err
	}
	restoredEnvironment, err := readPrivateRegularFile(filepath.Join(workspace.configRestore, "lmm-api-go.env"), 1<<20)
	if err != nil || !bytes.Equal(currentEnvironment, restoredEnvironment) {
		return productionStatus{}, errors.New("failed prearm environment changed")
	}
	if err := verifyFrontendIdentity(runtime.paths.FrontendRoot, manifest.Frontend.OldTarget, manifest.Frontend.OldIndexSHA256); err != nil {
		return productionStatus{}, err
	}
	if err := runtime.verifyExistingSchemaLifecycle(ctx, manifest); err != nil {
		return productionStatus{}, err
	}
	if err := runtime.checkMerchantStoreWriterLifecycle(ctx, workspace, manifest, true, true); err != nil {
		return productionStatus{}, err
	}
	if err := runtime.requestMerchantStoreFence(ctx, workspace, manifest, false); err != nil {
		return productionStatus{}, err
	}
	locations := filepath.Join(runtime.paths.NginxRoot, "lmm-api-locations.conf")
	if err := runtime.requireOwnedSafePath(locations, false); err != nil {
		return productionStatus{}, err
	}
	info, err := os.Lstat(locations)
	if err != nil || info.Mode().Perm() != 0o644 {
		return productionStatus{}, errors.New("failed prearm admission entry is unsafe")
	}
	original, err := os.ReadFile(locations)
	if err != nil {
		return productionStatus{}, err
	}
	// Admission was never closed. Record its actual still-open bytes, without
	// inventing a completed drain, stop or restart. The original holder knows
	// this existing incomplete-admission recovery reason and rechecks it.
	manifest.BillingGate = &productionBillingGate{StartedUTC: created, Sequence: 1,
		OriginalSHA256: startupContentSHA256(original), AdmissionReopened: true}
	if manifest.NginxEdgeRestoreSHA256 == "" {
		return productionStatus{}, errors.New("failed prearm recovery requires the original edge snapshot")
	}
	if err := runtime.verifyPreStopEdgeState(workspace, manifest); err != nil {
		return productionStatus{}, err
	}
	state, err := runtime.billingUnitState(ctx, runtime.paths.Service)
	if err != nil {
		return productionStatus{}, err
	}
	if err := runtime.verifyUnchangedAdmissionWriter(ctx, manifest, state); err != nil {
		return productionStatus{}, err
	}
	pid, _ := strconv.Atoi(state["MainPID"])
	hasher := runtime.billingExecutableSHA256
	if hasher == nil {
		hasher = func(pid int) (string, error) { return sha256File(filepath.Join("/proc", strconv.Itoa(pid), "exe")) }
	}
	if digest, err := hasher(pid); err != nil || digest != manifest.MerchantStoreWriter.Rollback.PayloadSHA256 {
		return productionStatus{}, errors.New("failed prearm running writer differs from its signed N-1")
	}
	// apply removed its private probe token on failure. Select an existing
	// qualified token through the native read-only database inspection again.
	schema, err := runtime.captureDatabaseAccess(ctx, workspace, restoredEnvironment)
	if err != nil || schema != manifest.DatabaseSchema {
		return productionStatus{}, errors.New("failed prearm business probe database changed or is unavailable")
	}
	if err := runtime.probeReleaseWithBinary(ctx, workspace, runtime.paths.InstalledBinary, manifest.OldVersion, manifest.Frontend.OldIndexSHA256); err != nil {
		return productionStatus{}, err
	}
	if err := runtime.verifyExistingSchemaLifecycle(ctx, manifest); err != nil {
		return productionStatus{}, err
	}
	currentState, err := runtime.billingUnitState(ctx, runtime.paths.Service)
	if err != nil || currentState["MainPID"] != state["MainPID"] || currentState["InvocationID"] != state["InvocationID"] {
		return productionStatus{}, errors.New("failed prearm writer changed during recovery")
	}
	if err := runtime.verifyUnchangedAdmissionWriter(ctx, manifest, currentState); err != nil {
		return productionStatus{}, err
	}
	if digest, err := sha256File(locations); err != nil || digest != manifest.BillingGate.OriginalSHA256 {
		return productionStatus{}, errors.New("failed prearm admission changed during recovery")
	}
	backup := filepath.Join(workspace.root, "billing-locations.1")
	if _, err := os.Lstat(backup); !errors.Is(err, os.ErrNotExist) {
		return productionStatus{}, errors.New("failed prearm admission evidence already exists; explicit recovery review required")
	}
	if err := writeAtomicRegularFile(backup, original, 0o600); err != nil {
		return productionStatus{}, err
	}
	if err := runtime.writeManifest(workspace, manifest); err != nil {
		return productionStatus{}, err
	}
	rolledBack := productionStatus{Phase: "ROLLED_BACK", Version: manifest.OldVersion, Previous: manifest.ExpectedVersion, Reason: productionUnchangedAdmissionRecoveryReason}
	if err := runtime.writeStatus(workspace, rolledBack); err != nil {
		return productionStatus{}, err
	}
	if err := runtime.requestMerchantStoreFence(ctx, workspace, manifest, true); err != nil {
		return productionStatus{}, runtime.persistRollbackFailure(workspace, rolledBack, productionUnchangedAdmissionRecoveryReason, err)
	}
	if err := runtime.finalizeTransactionFiles(workspace); err != nil {
		return productionStatus{}, err
	}
	return runtime.readStatus(workspace)
}

func (runtime *productionRuntime) reclaimPrearmTransactionLock(workspace productionWorkspace) error {
	if _, err := os.Lstat(runtime.paths.TransactionLock); err == nil {
		return runtime.validateTransactionLock(workspace)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := runtime.requireOwnedSafePath(filepath.Dir(runtime.paths.TransactionLock), true); err != nil {
		return err
	}
	if err := os.Mkdir(runtime.paths.TransactionLock, 0o700); err != nil {
		return err
	}
	marker := []byte(fmt.Sprintf("format=1\ndeployment_id=%s\nstatus=ACTIVE\n", workspace.id))
	if err := writeAtomicRegularFile(filepath.Join(runtime.paths.TransactionLock, productionTransactionMarker), marker, 0o600); err != nil {
		return err
	}
	return runtime.validateTransactionLock(workspace)
}
