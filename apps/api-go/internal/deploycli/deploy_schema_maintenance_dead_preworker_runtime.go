package deploycli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/internal/deploymentfence"
	"github.com/jackc/pgx/v5"
)

// Worker evidence is never classified by content or an absent owner file.
func (runtime *productionRuntime) checkSchemaMaintenanceDeadNoWorker(workspace string) error {
	if err := runtime.merchantStorePrivateDirectory(filepath.Join(workspace, "state"), false); err != nil {
		return err
	}
	for _, name := range []string{"schema-worker-intent.json", "schema-worker-result.json", "schema-worker-output.log", "schema-outcome.json", "owner-released.json", "recovery-binding.json", "recovery-startup-profile.json", "recovery-hook-intent.json", "recovery-hook-bound.json", "recovery-start-intent.json", "recovery-started.json", "host-finished.json", "abort-binding.json", "abort-start-intent.json", "abort-started.json", "abort-host-finished.json", "abort-finish-intent.json"} {
		if _, err := os.Lstat(filepath.Join(workspace, "state", name)); !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("dead-preworker recovery found possible worker or recovery evidence at %s", name)
		}
	}
	return nil
}

func (runtime *productionRuntime) schemaMaintenanceDeadArtifacts(ctx context.Context, plan productionSchemaMaintenancePlan, preSHA string, daemon schemaMaintenanceRPCBinding) (productionMerchantStoreCapsule, error) {
	var empty productionMerchantStoreCapsule
	if validateSchemaMaintenanceRPCBinding(daemon) != nil || daemon.PlanSHA256 != preSHA || daemon.TransitionID != plan.TransitionID || daemon.DaemonExeSHA256 != plan.Worker.Provider.DeployEngineSHA256 {
		return empty, errors.New("dead-preworker original daemon differs from its signed preplan")
	}
	workspace := schemaMaintenanceHostWorkspace(runtime.paths, plan.TransitionID)
	actual, err := runtime.loadSchemaMaintenancePlan(filepath.Join(workspace, "schema-maintenance-plan.json"), preSHA)
	want, wantErr := canonicalProductionSchemaMaintenancePlan(plan, runtime.paths)
	got, gotErr := canonicalProductionSchemaMaintenancePlan(actual, runtime.paths)
	if err != nil || wantErr != nil || gotErr != nil || !bytes.Equal(want, got) {
		return empty, errors.New("dead-preworker immutable preplan changed")
	}
	for _, origin := range plan.Origins {
		if origin.Host != plan.PrimaryHost {
			continue
		}
		c, err := runtime.loadMerchantStoreCapsule(origin.ArtifactCapsule.Path, origin.ArtifactCapsule.SHA256)
		if err != nil || c.Format != 1 || c.StartupPolicy != "held" || c.DeploymentID != plan.TransitionID || c.Host != plan.PrimaryHost || c.Writer == nil || c.ExistingSchemaContract == nil || c.Candidate != plan.Worker.Provider || c.Rollback != merchantStoreCapsuleProjection(origin.Recovery.Candidate, "rollback") || c.Writer.Role != plan.Role || c.Writer.RequiredCapability != plan.RequiredCapability || c.Writer.RequiredCapability > 7 {
			return empty, errors.New("dead-preworker signed artifact capsule changed")
		}
		physical := *c.ExistingSchemaContract
		physical.StartupSHA256, physical.SignedUnitSHA256 = "", ""
		if physical != plan.PreSchema {
			return empty, errors.New("dead-preworker artifact capsule changed its original physical catalog")
		}
		return c, nil
	}
	return empty, errors.New("dead-preworker primary origin is absent")
}

func validateSchemaMaintenanceDeadOriginalOwner(raw []byte, plan productionSchemaMaintenancePlan, preSHA string, daemon schemaMaintenanceRPCBinding, artifacts productionMerchantStoreCapsule, service string) error {
	owner, err := deploymentfence.ParseSchemaMaintenanceOwner(raw)
	if err != nil || artifacts.Writer == nil {
		return errors.New("dead-preworker original owner is not canonical")
	}
	w := artifacts.Writer
	if owner.DeploymentID != plan.TransitionID || owner.PlanSHA256 != preSHA || owner.Host != plan.PrimaryHost || owner.ContractSHA256 != merchantStoreFenceContractSHA(w) || owner.ProviderSHA256 != plan.Worker.Provider.PayloadSHA256 || owner.HolderPID != daemon.DaemonPID || owner.HolderInvocationID != daemon.DaemonInvocationID || owner.HolderUnit != schemaMaintenanceHolderUnit(plan.TransitionID) || owner.Service != service || owner.SystemIdentifier != w.SystemIdentifier || owner.Database != w.Database || owner.DatabaseOID != w.DatabaseOID || owner.Schema != w.Schema || owner.SchemaOID != w.SchemaOID || owner.Role != w.Role {
		return errors.New("dead-preworker owner differs from the original daemon, provider or physical role")
	}
	return nil
}

func (runtime *productionRuntime) validateSchemaMaintenanceDeadAuthority(ctx context.Context, a productionSchemaMaintenanceDeadPreWorkerCASAuthority) (productionMerchantStoreCapsule, error) {
	var empty productionMerchantStoreCapsule
	if err := runtime.assertProductionMutation(); err != nil {
		return empty, err
	}
	if a.CheckLocalDeadAuthority == nil || runtime.now == nil || !existingSchemaInvocationPattern.MatchString(os.Getenv("INVOCATION_ID")) || a.OriginalDaemon.DaemonPID == os.Getpid() || sha256MustEqual("/proc/self/exe", a.Plan.Worker.Provider.DeployEngineSHA256) != nil {
		return empty, errors.New("dead-preworker recoverer is not its distinct actual signed process")
	}
	c, err := runtime.schemaMaintenanceDeadArtifacts(ctx, a.Plan, a.PrePlanSHA256, a.OriginalDaemon)
	if err != nil {
		return empty, err
	}
	if a.OldCapsule.Host != a.Plan.PrimaryHost || merchantStoreCapsuleProjection(a.OldCapsule.Candidate, "rollback") != c.Rollback {
		return empty, errors.New("dead-preworker recovery changed the retained primary provider")
	}
	if a.OwnerMode == schemaMaintenanceDeadPreWorkerExactOwner {
		if err := validateSchemaMaintenanceDeadOriginalOwner(a.OriginalOwnerRaw, a.Plan, a.PrePlanSHA256, a.OriginalDaemon, c, runtime.paths.Service); err != nil {
			return empty, err
		}
	} else if a.OwnerMode != schemaMaintenanceDeadPreWorkerZeroOwner || len(a.OriginalOwnerRaw) != 0 {
		return empty, errors.New("dead-preworker recovery mode is not explicit")
	}
	workspace := schemaMaintenanceHostWorkspace(runtime.paths, a.Plan.TransitionID)
	if err := runtime.checkSchemaMaintenanceDeadNoWorker(workspace); err != nil {
		return empty, err
	}
	ownerPath := filepath.Join(workspace, "state", "schema-owner.json")
	if _, err := os.Lstat(ownerPath); !errors.Is(err, os.ErrNotExist) {
		raw, readErr := runtime.readExistingSchemaSealedFile(ownerPath, true)
		if readErr != nil || a.OwnerMode != schemaMaintenanceDeadPreWorkerExactOwner || !bytes.Equal(raw, a.OriginalOwnerRaw) {
			return empty, errors.New("dead-preworker known original owner receipt differs or is unsafe")
		}
	}
	if err := a.CheckLocalDeadAuthority(ctx); err != nil {
		return empty, err
	}
	return c, nil
}

func newSchemaMaintenanceDeadCASResult(a productionSchemaMaintenanceDeadPreWorkerCASAuthority, now time.Time) productionSchemaMaintenanceDeadPreWorkerCASResult {
	result := productionSchemaMaintenanceDeadPreWorkerCASResult{Format: "lmm-schema-maintenance-dead-preworker-cas-v1", TransitionID: a.Plan.TransitionID, PrePlanSHA256: a.PrePlanSHA256, Mode: a.OwnerMode, OriginalDaemon: a.OriginalDaemon, OldCapsule: productionReleaseFilePlan{Path: filepath.Join(a.OldCapsule.Root, "capsule.json"), SHA256: a.OldCapsuleSHA256}, PreSchema: a.Plan.PreSchema, ProviderSHA256: a.Plan.Worker.Provider.PayloadSHA256, RecovererEXESHA256: a.Plan.Worker.Provider.DeployEngineSHA256, RecovererPID: os.Getpid(), RecovererInvocationID: os.Getenv("INVOCATION_ID"), StartedUTC: now.UTC(), Outcome: "CAS_INTENT"}
	if len(a.OriginalOwnerRaw) != 0 {
		result.OriginalOwnerSHA256 = startupContentSHA256(a.OriginalOwnerRaw)
	}
	return result
}

func (runtime *productionRuntime) recoverSchemaMaintenanceDeadPreWorkerOwner(ctx context.Context, a productionSchemaMaintenanceDeadPreWorkerCASAuthority) (productionSchemaMaintenanceDeadPreWorkerCASResult, error) {
	var result productionSchemaMaintenanceDeadPreWorkerCASResult
	workspace := schemaMaintenanceHostWorkspace(runtime.paths, a.Plan.TransitionID)
	// Check existence before any callback, qualification or database connection.
	for _, name := range []string{"dead-preworker-cas-intent.json", "dead-preworker-cas-result.json", "dead-preworker-cas-reconciled.json"} {
		if _, err := os.Lstat(filepath.Join(workspace, "state", name)); !errors.Is(err, os.ErrNotExist) {
			return result, errors.New("dead-preworker CAS already has an uncertain one-shot invocation; only read-only reconciliation is permitted")
		}
	}
	artifacts, err := runtime.validateSchemaMaintenanceDeadAuthority(ctx, a)
	if err != nil {
		return result, err
	}
	for _, rollback := range []bool{false, true} {
		if _, err := runtime.verifyMerchantStoreCapsuleArtifact(ctx, artifacts, rollback); err != nil {
			return result, err
		}
	}
	lease, err := runtime.openSchemaMaintenanceOldFence(ctx, a.Plan, a.PrePlanSHA256, a.OldCapsule, a.OldCapsuleSHA256)
	if err != nil {
		return result, err
	}
	defer lease.Close()
	ownerReceipt, ownerReceiptPresent, err := runtime.schemaMaintenanceDeadOwnerReceipt(workspace)
	if err != nil {
		return result, err
	}
	if ownerReceiptPresent && (a.OwnerMode != schemaMaintenanceDeadPreWorkerExactOwner || !bytes.Equal(ownerReceipt, a.OriginalOwnerRaw)) {
		return result, errors.New("dead-preworker original owner receipt changed during qualification")
	}
	result = newSchemaMaintenanceDeadCASResult(a, runtime.now())
	raw, _ := json.MarshalIndent(result, "", "  ")
	raw = append(raw, '\n')
	if err := runtime.writeSchemaMaintenanceRawNewFile(filepath.Join(workspace, "state", "dead-preworker-cas-intent.json"), raw); err != nil {
		return result, errors.New("dead-preworker CAS intent is uncertain; no retry is permitted")
	}
	result.IntentSHA256 = startupContentSHA256(raw)
	guard := func(ctx context.Context) error {
		intent, err := runtime.readExistingSchemaSealedFile(filepath.Join(workspace, "state", "dead-preworker-cas-intent.json"), true)
		if err != nil || !bytes.Equal(intent, raw) {
			return errors.New("dead-preworker sealed CAS intent changed during its transaction")
		}
		receipt, present, err := runtime.schemaMaintenanceDeadOwnerReceipt(workspace)
		if err != nil || present != ownerReceiptPresent || !bytes.Equal(receipt, ownerReceipt) {
			return errors.New("dead-preworker original owner receipt changed during its transaction")
		}
		if sha256MustEqual("/proc/self/exe", a.Plan.Worker.Provider.DeployEngineSHA256) != nil {
			return errors.New("dead-preworker recoverer executable changed")
		}
		if err := runtime.checkSchemaMaintenanceDeadNoWorker(workspace); err != nil {
			return err
		}
		return a.CheckLocalDeadAuthority(ctx)
	}
	if err := lease.recoverSchemaMaintenanceDeadPreWorkerOwner(ctx, a.Plan.PreSchema, a.OriginalOwnerRaw, a.OwnerMode, guard); err != nil {
		return result, err
	}
	result.FinishedUTC = runtime.now().UTC()
	result.Outcome = "ZERO_OWNER_CONFIRMED"
	if a.OwnerMode == schemaMaintenanceDeadPreWorkerExactOwner {
		result.Outcome = "EXACT_OWNER_RELEASED"
	}
	raw, _ = json.MarshalIndent(result, "", "  ")
	if err := runtime.writeSchemaMaintenanceRawNewFile(filepath.Join(workspace, "state", "dead-preworker-cas-result.json"), append(raw, '\n')); err != nil {
		return result, errors.New("dead-preworker CAS result receipt is uncertain; only read-only reconciliation is permitted")
	}
	return result, nil
}

func beginSchemaMaintenanceDeadRead(ctx context.Context, lease *productionMerchantStoreFence) (pgx.Tx, error) {
	tx, err := lease.connection.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, errors.New("dead-preworker read-only transaction is unavailable")
	}
	for _, key := range []int64{merchantStoreDeploymentOwnerClaimKey, 0x4c4d4d4150490001} {
		if _, err := tx.Exec(ctx, "SELECT pg_catalog.pg_advisory_xact_lock($1)", key); err != nil {
			_ = tx.Rollback(context.Background())
			return nil, errors.New("dead-preworker read serialization is unavailable")
		}
	}
	return tx, nil
}

// Resolve the independently reviewed digest to actual canonical PostgreSQL
// bytes. This observer cannot delete or infer an unrecorded claim's history.
func (runtime *productionRuntime) readSchemaMaintenanceDeadPreWorkerOwner(ctx context.Context, plan productionSchemaMaintenancePlan, preSHA string, old productionMerchantStoreCapsule, oldSHA string, daemon schemaMaintenanceRPCBinding, expectedSHA string) ([]byte, error) {
	if !productionSHA256Pattern.MatchString(expectedSHA) {
		return nil, errors.New("dead-preworker original owner needs its independently reviewed digest")
	}
	artifacts, err := runtime.schemaMaintenanceDeadArtifacts(ctx, plan, preSHA, daemon)
	if err != nil {
		return nil, err
	}
	lease, err := runtime.openSchemaMaintenanceOldFence(ctx, plan, preSHA, old, oldSHA)
	if err != nil {
		return nil, err
	}
	defer lease.Close()
	lease.mu.Lock()
	defer lease.mu.Unlock()
	tx, err := beginSchemaMaintenanceDeadRead(ctx, lease)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var count int
	var value *string
	if tx.QueryRow(ctx, `SELECT count(*)::int,min(value) FROM "`+lease.identity.Schema+`".options WHERE `+deploymentfence.PostgreSQLPresencePredicate).Scan(&count, &value) != nil || count != 1 || value == nil || startupContentSHA256([]byte(*value)) != expectedSHA {
		return nil, errors.New("dead-preworker reviewed digest does not identify the sole actual owner")
	}
	raw := []byte(*value)
	if err := validateSchemaMaintenanceDeadOriginalOwner(raw, plan, preSHA, daemon, artifacts, runtime.paths.Service); err != nil {
		return nil, err
	}
	owner, _ := deploymentfence.ParseSchemaMaintenanceOwner(raw)
	if err := checkSchemaMaintenanceDeadPreWorkerTransaction(ctx, tx, lease.identity, plan.PreSchema, owner, raw, schemaMaintenanceDeadPreWorkerExactOwner, false); err != nil {
		return nil, err
	}
	if err := checkSchemaMaintenanceDeadPreWorkerClients(ctx, tx, owner.BackendPID); err != nil {
		return nil, err
	}
	if err := tx.Rollback(ctx); err != nil {
		return nil, errors.New("dead-preworker owner read did not finish cleanly")
	}
	return raw, nil
}

func (runtime *productionRuntime) reconcileSchemaMaintenanceDeadPreWorkerOwner(ctx context.Context, a productionSchemaMaintenanceDeadPreWorkerCASAuthority) (productionSchemaMaintenanceDeadPreWorkerCASResult, error) {
	var result productionSchemaMaintenanceDeadPreWorkerCASResult
	workspace := schemaMaintenanceHostWorkspace(runtime.paths, a.Plan.TransitionID)
	if _, err := runtime.validateSchemaMaintenanceDeadAuthority(ctx, a); err != nil {
		return result, err
	}
	raw, err := runtime.readExistingSchemaSealedFile(filepath.Join(workspace, "state", "dead-preworker-cas-intent.json"), true)
	if err != nil {
		return result, errors.New("dead-preworker reconciliation lacks its sealed original one-shot intent")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(&result) != nil || dec.Decode(&struct{}{}) != io.EOF {
		return result, errors.New("dead-preworker CAS intent is invalid")
	}
	canonical, _ := json.MarshalIndent(result, "", "  ")
	expected := newSchemaMaintenanceDeadCASResult(a, result.StartedUTC)
	if !bytes.Equal(append(canonical, '\n'), raw) || result != expected || result.StartedUTC.IsZero() || result.StartedUTC.Location() != time.UTC || result.StartedUTC.After(runtime.now().UTC()) {
		return result, errors.New("dead-preworker reconciliation changed the actual original recoverer intent")
	}
	result.IntentSHA256 = startupContentSHA256(raw)
	result, err = runtime.observeSchemaMaintenanceDeadPreWorkerOwner(ctx, a, result, raw)
	if err != nil {
		return result, err
	}
	canonical, _ = json.MarshalIndent(result, "", "  ")
	if err := runtime.writeSchemaMaintenanceRawNewFile(filepath.Join(workspace, "state", "dead-preworker-cas-reconciled.json"), append(canonical, '\n')); err != nil {
		return result, errors.New("dead-preworker reconciliation receipt already exists or is uncertain")
	}
	return result, nil
}

func (runtime *productionRuntime) schemaMaintenanceDeadOwnerReceipt(workspace string) ([]byte, bool, error) {
	path := filepath.Join(workspace, "state", "schema-owner.json")
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	} else if err != nil {
		return nil, false, err
	}
	raw, err := runtime.readExistingSchemaSealedFile(path, true)
	return raw, true, err
}

// Diagnose never publishes an authorization receipt. Its observations use the
// same fixed read-only transaction as reconciliation, with no DELETE or retry.
func (runtime *productionRuntime) diagnoseSchemaMaintenanceDeadPreWorkerOwner(ctx context.Context, a productionSchemaMaintenanceDeadPreWorkerCASAuthority) (productionSchemaMaintenanceDeadPreWorkerCASResult, error) {
	var result productionSchemaMaintenanceDeadPreWorkerCASResult
	if _, err := runtime.validateSchemaMaintenanceDeadAuthority(ctx, a); err != nil {
		return result, err
	}
	result = newSchemaMaintenanceDeadCASResult(a, runtime.now())
	result, err := runtime.observeSchemaMaintenanceDeadPreWorkerOwner(ctx, a, result, nil)
	if err == nil && result.Outcome == "OWNER_ABSENCE_RECONCILED" {
		result.Outcome = "OWNER_ABSENCE_OBSERVED"
	}
	return result, err
}

func (runtime *productionRuntime) observeSchemaMaintenanceDeadPreWorkerOwner(ctx context.Context, a productionSchemaMaintenanceDeadPreWorkerCASAuthority, result productionSchemaMaintenanceDeadPreWorkerCASResult, intentRaw []byte) (productionSchemaMaintenanceDeadPreWorkerCASResult, error) {
	workspace := schemaMaintenanceHostWorkspace(runtime.paths, a.Plan.TransitionID)
	ownerReceipt, ownerReceiptPresent, err := runtime.schemaMaintenanceDeadOwnerReceipt(workspace)
	if err != nil || ownerReceiptPresent && (a.OwnerMode != schemaMaintenanceDeadPreWorkerExactOwner || !bytes.Equal(ownerReceipt, a.OriginalOwnerRaw)) {
		return result, errors.New("dead-preworker reconciliation original owner receipt differs")
	}
	lease, err := runtime.openSchemaMaintenanceOldFence(ctx, a.Plan, a.PrePlanSHA256, a.OldCapsule, a.OldCapsuleSHA256)
	if err != nil {
		return result, err
	}
	defer lease.Close()
	lease.mu.Lock()
	defer lease.mu.Unlock()
	tx, err := beginSchemaMaintenanceDeadRead(ctx, lease)
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var count int
	if tx.QueryRow(ctx, `SELECT count(*)::int FROM "`+lease.identity.Schema+`".options WHERE `+deploymentfence.PostgreSQLPresencePredicate).Scan(&count) != nil {
		return result, errors.New("dead-preworker reconciliation owner state is unavailable")
	}
	owner := productionMerchantStoreFenceOwner{}
	if len(a.OriginalOwnerRaw) != 0 {
		owner, _ = deploymentfence.ParseSchemaMaintenanceOwner(a.OriginalOwnerRaw)
	}
	mode, ownerRaw := schemaMaintenanceDeadPreWorkerZeroOwner, []byte(nil)
	result.Outcome = "OWNER_ABSENCE_RECONCILED"
	if count != 0 {
		if a.OwnerMode != schemaMaintenanceDeadPreWorkerExactOwner {
			return result, errors.New("dead-preworker zero-owner reconciliation found a reserved owner")
		}
		mode, ownerRaw = schemaMaintenanceDeadPreWorkerExactOwner, a.OriginalOwnerRaw
		result.Outcome = "EXACT_OWNER_REMAINS"
	}
	if err := checkSchemaMaintenanceDeadPreWorkerTransaction(ctx, tx, lease.identity, a.Plan.PreSchema, owner, ownerRaw, mode, false); err != nil {
		return result, err
	}
	if err := runtime.checkSchemaMaintenanceDeadNoWorker(workspace); err != nil {
		return result, err
	}
	actualReceipt, receiptPresent, receiptErr := runtime.schemaMaintenanceDeadOwnerReceipt(workspace)
	if receiptErr != nil || receiptPresent != ownerReceiptPresent || !bytes.Equal(actualReceipt, ownerReceipt) {
		return result, errors.New("dead-preworker reconciliation original owner receipt changed during observation")
	}
	if len(intentRaw) != 0 {
		actualIntent, intentErr := runtime.readExistingSchemaSealedFile(filepath.Join(workspace, "state", "dead-preworker-cas-intent.json"), true)
		if intentErr != nil || !bytes.Equal(actualIntent, intentRaw) {
			return result, errors.New("dead-preworker reconciliation intent changed during observation")
		}
	}
	if err := a.CheckLocalDeadAuthority(ctx); err != nil {
		return result, err
	}
	if err := checkSchemaMaintenanceDeadPreWorkerClients(ctx, tx, owner.BackendPID); err != nil {
		return result, err
	}
	if err := tx.Rollback(ctx); err != nil {
		return result, errors.New("dead-preworker read-only reconciliation did not finish cleanly")
	}
	result.FinishedUTC = runtime.now().UTC()
	return result, nil
}
