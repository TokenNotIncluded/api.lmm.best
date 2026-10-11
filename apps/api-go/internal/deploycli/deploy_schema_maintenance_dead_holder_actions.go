package deploycli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

func (d *productionSchemaMaintenanceDeadHolderController) casAuthority(ctx context.Context, persistOriginalOwner bool) (productionSchemaMaintenanceDeadPreWorkerCASAuthority, error) {
	c := d.base
	a := productionSchemaMaintenanceDeadPreWorkerCASAuthority{Plan: c.plan, PrePlanSHA256: c.planSHA, OldCapsuleSHA256: c.origin.OldCapsule.SHA256, OriginalDaemon: d.original, OwnerMode: d.ownerMode, CheckLocalDeadAuthority: d.checkLeasedLocal}
	if c.origin.Host != c.plan.PrimaryHost {
		return a, errors.New("dead pre-worker owner CAS is fixed-primary only")
	}
	old, err := c.runtime.loadMerchantStoreCapsule(c.origin.OldCapsule.Path, c.origin.OldCapsule.SHA256)
	if err != nil {
		return a, err
	}
	a.OldCapsule = old
	if d.ownerMode == "EXACT_ORIGINAL_OWNER" {
		raw, err := c.runtime.schemaMaintenanceDeadHolderOriginalOwner(c.origin.Workspace, d.ownerSHA, persistOriginalOwner, func() ([]byte, error) {
			return c.runtime.readSchemaMaintenanceDeadPreWorkerOwner(ctx, c.plan, c.planSHA, old, c.origin.OldCapsule.SHA256, d.original, d.ownerSHA)
		})
		if err != nil {
			return a, err
		}
		a.OriginalOwnerRaw = raw
	} else if d.ownerMode != "ZERO_OWNER" || d.ownerSHA != "" {
		return a, errors.New("dead holder owner mode must be explicit and cannot downgrade")
	}
	return a, nil
}
func (d *productionSchemaMaintenanceDeadHolderController) recoveredResult(ctx context.Context) (productionSchemaMaintenanceDeadPreWorkerCASResult, error) {
	c := d.base
	var result productionSchemaMaintenanceDeadPreWorkerCASResult
	binding, _, err := d.binding(ctx)
	if err != nil {
		return result, err
	}
	// An independent native reconciliation can close a lost/partial normal
	// result without replacing it. Its exact old intent and actual PG proof
	// are still mandatory; an unreadable reconciliation never falls back.
	raw, err := c.runtime.readSchemaMaintenanceDeadHolderResult(c.origin.Workspace)
	if err != nil {
		return result, err
	}
	if err := decodeControllerBackupJSON(raw, &result); err != nil {
		return result, err
	}
	canonical, _ := json.MarshalIndent(result, "", "  ")
	primary := binding.Origins[0]
	expectedMode, expectedOutcome := "EXACT_ORIGINAL_OWNER", "EXACT_OWNER_RELEASED"
	if d.ownerMode == "ZERO_OWNER" {
		expectedMode, expectedOutcome = "ZERO_OWNER", "ZERO_OWNER_CONFIRMED"
	}
	if !bytes.Equal(raw, append(canonical, '\n')) || result.Format != "lmm-schema-maintenance-dead-preworker-cas-v1" || result.TransitionID != c.plan.TransitionID || result.PrePlanSHA256 != c.planSHA || result.Mode != expectedMode || (result.Outcome != expectedOutcome && result.Outcome != "OWNER_ABSENCE_RECONCILED") || result.OriginalDaemon != primary.OriginalDaemon || result.OriginalOwnerSHA256 != d.ownerSHA || result.OldCapsule != primary.OldCapsule || result.PreSchema != c.plan.PreSchema || result.ProviderSHA256 != c.plan.Worker.Provider.PayloadSHA256 || result.RecovererEXESHA256 != c.plan.Worker.Provider.DeployEngineSHA256 || result.RecovererPID != primary.RecoveryDaemon.DaemonPID || result.RecovererInvocationID != primary.RecoveryDaemon.DaemonInvocationID || !productionSHA256Pattern.MatchString(result.IntentSHA256) || result.StartedUTC.IsZero() || result.FinishedUTC.Before(result.StartedUTC) {
		return result, errors.New("dead pre-worker recovery has no exact native CAS outcome")
	}
	if c.origin.Host == c.plan.PrimaryHost {
		intent, err := c.runtime.readExistingSchemaSealedFile(filepath.Join(c.origin.Workspace, "state", "dead-preworker-cas-intent.json"), true)
		if err != nil || startupContentSHA256(intent) != result.IntentSHA256 {
			return result, errors.New("dead pre-worker CAS result lost its exact one-shot intent")
		}
	}
	return result, nil
}
func (d *productionSchemaMaintenanceDeadHolderController) checkRecovered(ctx context.Context) error {
	c := d.base
	if _, err := d.recoveredResult(ctx); err != nil {
		return err
	}
	old, err := c.runtime.loadMerchantStoreCapsule(c.origin.OldCapsule.Path, c.origin.OldCapsule.SHA256)
	if err != nil {
		return err
	}
	return c.runtime.checkSchemaMaintenanceAbortPreState(ctx, c.plan, c.planSHA, old, c.origin.OldCapsule.SHA256)
}
func (d *productionSchemaMaintenanceDeadHolderController) recoverOwner(ctx context.Context) (productionSchemaMaintenanceDeadPreWorkerCASResult, error) {
	var result productionSchemaMaintenanceDeadPreWorkerCASResult
	for _, name := range []string{"dead-preworker-cas-intent.json", "dead-preworker-cas-result.json", "dead-preworker-cas-reconciled.json"} {
		if _, err := os.Lstat(filepath.Join(d.base.origin.Workspace, "state", name)); !errors.Is(err, os.ErrNotExist) {
			return result, errors.New("dead holder owner CAS was attempted; only actual read-only diagnosis and reconciliation are allowed")
		}
	}
	if _, _, err := d.binding(ctx); err != nil {
		return result, err
	}
	if err := d.checkStoppedOrigins(ctx); err != nil {
		return result, err
	}
	a, err := d.casAuthority(ctx, true)
	if err != nil {
		return result, err
	}
	return d.base.runtime.recoverSchemaMaintenanceDeadPreWorkerOwner(ctx, a)
}
func (d *productionSchemaMaintenanceDeadHolderController) installRecovered(ctx context.Context) (productionSchemaMaintenanceDeadPreWorkerCASResult, error) {
	c := d.base
	var result productionSchemaMaintenanceDeadPreWorkerCASResult
	if c.origin.Host == c.plan.PrimaryHost {
		return d.recoveredResult(ctx)
	}
	if err := d.checkLeasedLocal(ctx); err != nil {
		return result, err
	}
	if err := d.remote(ctx, c.plan.Origins[0], "dead-recovered", &result); err != nil {
		return result, err
	}
	name := "dead-preworker-cas-result.json"
	if result.Outcome == "OWNER_ABSENCE_RECONCILED" {
		name = "dead-preworker-cas-reconciled.json"
	}
	if err := c.runtime.startupBaselineNewFile(filepath.Join(c.origin.Workspace, "state", name), result); err != nil {
		return result, err
	}
	result, err := d.recoveredResult(ctx)
	if err != nil {
		return result, err
	}
	return result, d.checkRecovered(ctx)
}
func (d *productionSchemaMaintenanceDeadHolderController) handle(ctx context.Context, request schemaMaintenanceRPCRequest) (schemaMaintenanceRPCReply, error) {
	c := d.base
	c.actionMu.Lock()
	defer c.actionMu.Unlock()
	if request.Binding != d.generation {
		return schemaMaintenanceRPCReply{}, errors.New("dead holder request differs from its dedicated fresh authority")
	}
	if err := d.checkLocal(ctx); err != nil {
		return schemaMaintenanceRPCReply{}, err
	}
	if d.finishUncertain && request.Action != "dead-status" && request.Action != "dead-ready" {
		return schemaMaintenanceRPCReply{}, errors.New("dead holder finish is unknown; only read-only status is permitted")
	}
	if d.finished && request.Action != "dead-ready" && request.Action != "dead-status" {
		return schemaMaintenanceRPCReply{}, errors.New("dead holder native lease is finished; only fresh readiness observation is permitted")
	}
	switch request.Action {
	case "dead-prepared":
		return schemaMaintenanceRPCResult(d.prepared(ctx))
	case "dead-stopped":
		if err := d.checkLeasedLocal(ctx); err != nil {
			return schemaMaintenanceRPCReply{}, err
		}
		capture, err := c.host.captureStopped(ctx)
		if err != nil {
			return schemaMaintenanceRPCReply{}, err
		}
		old, err := c.runtime.loadMerchantStoreCapsule(c.origin.OldCapsule.Path, c.origin.OldCapsule.SHA256)
		if err != nil {
			return schemaMaintenanceRPCReply{}, err
		}
		if err := c.runtime.qualifyMerchantStoreCapsule(ctx, old, c.origin.OldCapsule.SHA256, "", true); err != nil {
			return schemaMaintenanceRPCReply{}, err
		}
		return schemaMaintenanceRPCResult(capture, d.checkLeasedLocal(ctx))
	case "dead-create-binding":
		return schemaMaintenanceRPCResult(d.createBinding(ctx))
	case "dead-binding":
		binding, _, err := d.binding(ctx)
		return schemaMaintenanceRPCResult(binding, err)
	case "dead-install-binding":
		return schemaMaintenanceRPCResult(d.installBinding(ctx))
	case "dead-recover-owner":
		return schemaMaintenanceRPCResult(d.recoverOwner(ctx))
	case "dead-diagnose", "dead-reconcile-owner":
		if c.origin.Host != c.plan.PrimaryHost {
			return schemaMaintenanceRPCReply{}, errors.New("dead owner diagnosis is fixed-primary only")
		}
		if _, _, err := d.binding(ctx); err != nil {
			return schemaMaintenanceRPCReply{}, err
		}
		if err := d.checkStoppedOrigins(ctx); err != nil {
			return schemaMaintenanceRPCReply{}, err
		}
		a, err := d.casAuthority(ctx, false)
		if err != nil {
			return schemaMaintenanceRPCReply{}, err
		}
		if request.Action == "dead-diagnose" {
			return schemaMaintenanceRPCResult(c.runtime.diagnoseSchemaMaintenanceDeadPreWorkerOwner(ctx, a))
		}
		return schemaMaintenanceRPCResult(c.runtime.reconcileSchemaMaintenanceDeadPreWorkerOwner(ctx, a))
	case "dead-recovered":
		return schemaMaintenanceRPCResult(d.recoveredResult(ctx))
	case "dead-install-recovered":
		return schemaMaintenanceRPCResult(d.installRecovered(ctx))
	case "dead-startable":
		return schemaMaintenanceRPCResult(d.capture(ctx, false))
	case "dead-ready":
		return schemaMaintenanceRPCResult(d.capture(ctx, true))
	case "dead-start-old":
		return schemaMaintenanceRPCResult(d.startOld(ctx))
	case "dead-reopen":
		return schemaMaintenanceRPCResult(d.reopen(ctx))
	case "dead-finish":
		return schemaMaintenanceRPCResult(d.finish(ctx))
	case "dead-status":
		phase := "DEAD_PREWORKER_PREPARED"
		if d.finishUncertain {
			phase = "FINISH_UNCERTAIN"
		} else if d.finished {
			phase = "DEAD_PREWORKER_FINISHED"
		} else if _, err := os.Lstat(filepath.Join(c.origin.Workspace, "state", "dead-preworker-cas-intent.json")); err == nil {
			phase = "CAS_ATTEMPTED_READ_ONLY_RECONCILIATION_REQUIRED"
		} else if !errors.Is(err, os.ErrNotExist) {
			return schemaMaintenanceRPCReply{}, err
		}
		return schemaMaintenanceRPCResult(map[string]string{"phase": phase, "mode": schemaMaintenanceDeadHolderMode, "host": c.origin.Host, "transition_id": c.plan.TransitionID, "pre_plan_sha256": c.planSHA}, nil)
	default:
		return schemaMaintenanceRPCReply{}, errors.New("dedicated dead holder RPC action is not permitted")
	}
}

// Only the CAS action persists actual original bytes. Diagnostic and readonly
// reconciliation actions must never create an owner capture on cache miss.
func (runtime *productionRuntime) schemaMaintenanceDeadHolderOriginalOwner(workspace, sha string, persist bool, readActual func() ([]byte, error)) ([]byte, error) {
	if !productionSHA256Pattern.MatchString(sha) || readActual == nil {
		return nil, errors.New("dead holder original owner reader lacks its reviewed digest")
	}
	path := filepath.Join(workspace, "state", "dead-holder-original-owner.json")
	var raw []byte
	if _, err := os.Lstat(path); err == nil {
		var err error
		raw, err = runtime.readExistingSchemaSealedFile(path, true)
		if err != nil {
			return nil, err
		}
	} else if errors.Is(err, os.ErrNotExist) {
		var err error
		raw, err = readActual()
		if err != nil {
			return nil, err
		}
		if startupContentSHA256(raw) != sha {
			return nil, errors.New("dead holder actual original owner differs from its reviewed digest")
		}
		if persist {
			if err := runtime.writeSchemaMaintenanceRawNewFile(path, raw); err != nil {
				return nil, errors.New("dead holder original owner capture is unknown; preserve it and inspect")
			}
		}
	} else {
		return nil, err
	}
	if startupContentSHA256(raw) != sha {
		return nil, errors.New("dead holder original owner bytes changed")
	}
	return raw, nil
}
func (runtime *productionRuntime) readSchemaMaintenanceDeadHolderResult(workspace string) ([]byte, error) {
	path := filepath.Join(workspace, "state", "dead-preworker-cas-reconciled.json")
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		path = filepath.Join(workspace, "state", "dead-preworker-cas-result.json")
	} else if err != nil {
		return nil, err
	}
	return runtime.readExistingSchemaSealedFile(path, true)
}
