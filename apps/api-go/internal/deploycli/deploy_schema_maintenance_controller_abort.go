package deploycli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
)

// Abort authority belongs to the original, serial native daemon. File absence
// alone never reconstructs that history after a daemon or PG holder loss.
func schemaMaintenanceAbortAction(action string) bool {
	switch action {
	case "prepare-abort", "create-abort-binding", "install-abort-binding", "capture-abort-binding", "check-abort-prestate", "capture-abort-startable", "capture-abort-ready", "start-abort-old", "reopen-abort", "finish-abort":
		return true
	}
	return false
}

func (c *productionSchemaMaintenanceController) checkAbortActionAllowed(action string) error {
	frozen := c.abortFrozen
	if _, err := os.Lstat(filepath.Join(c.origin.Workspace, "state", "abort-prepared.json")); err == nil {
		frozen = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("schema abort preparation has uncertain durable state")
	}
	if !frozen || schemaMaintenanceAbortAction(action) {
		return nil
	}
	switch action {
	case "status", "capture-closed", "capture-stopped":
		return nil
	default:
		return errors.New("schema daemon is irreversibly frozen for never-claimed abort; normal transition actions are disabled")
	}
}

func (c *productionSchemaMaintenanceController) checkNormalPlan(ctx context.Context) error {
	if err := c.checkAbortActionAllowed("claim"); err != nil {
		return err
	}
	return c.checkPlan(ctx)
}

var schemaMaintenanceAbortForbiddenState = []string{
	"schema-claim-intent.json", "schema-owner.json", "schema-worker-intent.json",
	"schema-worker-output.log", "schema-worker-result.json", "schema-outcome.json",
	"recovery-binding.json", "recovery-startup-profile.json", "recovery-hook-intent.json",
	"recovery-hook-bound.json", "recovery-start-intent.json", "recovery-started.json",
	"owner-released.json", "host-finished.json",
}

func (c *productionSchemaMaintenanceController) requireNeverClaimedHistory() error {
	if c.fence != nil || c.claimUncertain || c.finished || validateSchemaMaintenanceRPCBinding(c.daemonGeneration) != nil || c.daemonGeneration.PlanSHA256 != c.planSHA || c.daemonGeneration.TransitionID != c.plan.TransitionID || c.daemonGeneration.DaemonExeSHA256 != c.plan.Worker.Provider.DeployEngineSHA256 {
		return errors.New("never-claimed abort requires the original daemon with no accepted or uncertain owner claim")
	}
	for _, name := range schemaMaintenanceAbortForbiddenState {
		if _, err := os.Lstat(filepath.Join(c.origin.Workspace, "state", name)); !errors.Is(err, os.ErrNotExist) {
			return errors.New("never-claimed abort found prior claim, worker or recovery evidence, or uncertain file absence")
		}
	}
	raw, err := c.runtime.readExistingSchemaSealedFile(filepath.Join(c.origin.Workspace, "state", "daemon-binding.json"), true)
	want, marshalErr := schemaMaintenanceRPCMarshal(c.daemonGeneration)
	if err != nil || marshalErr != nil || !bytes.Equal(raw, append(want, '\n')) || schemaMaintenanceRPCProcess(c.daemonGeneration) != nil {
		return errors.New("never-claimed abort lost its actual original native daemon generation")
	}
	return nil
}

func (c *productionSchemaMaintenanceController) abortPrepared() (productionSchemaMaintenanceAbortPrepared, string, error) {
	var prepared productionSchemaMaintenanceAbortPrepared
	if !c.abortFrozen {
		return prepared, "", errors.New("never-claimed abort was not frozen by this original daemon")
	}
	if err := c.requireNeverClaimedHistory(); err != nil {
		return prepared, "", err
	}
	path := filepath.Join(c.origin.Workspace, "state", "abort-prepared.json")
	raw, err := c.runtime.readExistingSchemaSealedFile(path, true)
	if err != nil {
		return prepared, "", err
	}
	sha := startupContentSHA256(raw)
	prepared, err = c.runtime.loadSchemaMaintenanceAbortPrepared(path, sha, c.plan, c.planSHA)
	if err != nil || prepared.Host != c.origin.Host || prepared.DaemonGeneration != c.daemonGeneration || prepared.GuardianBinding.SHA256 != c.host.binding.GuardianBindingSHA256 {
		return prepared, "", errors.New("never-claimed abort preparation differs from its original local native holder")
	}
	for _, ref := range []productionReleaseFilePlan{prepared.StoppedCapture, prepared.GuardianBinding} {
		raw, err := c.runtime.readExistingSchemaSealedFile(ref.Path, true)
		if err != nil || startupContentSHA256(raw) != ref.SHA256 {
			return prepared, "", errors.New("never-claimed abort changed its original sealed stop or guardian evidence")
		}
	}
	return prepared, sha, nil
}

func (c *productionSchemaMaintenanceController) checkNeverClaimed(ctx context.Context) error {
	if err := c.checkAbortNativeDaemon(ctx); err != nil {
		return err
	}
	if _, _, err := c.abortPrepared(); err != nil {
		return err
	}
	old, err := c.runtime.loadMerchantStoreCapsule(c.origin.OldCapsule.Path, c.origin.OldCapsule.SHA256)
	if err != nil {
		return err
	}
	return c.runtime.checkSchemaMaintenanceAbortPreState(ctx, c.plan, c.planSHA, old, c.origin.OldCapsule.SHA256)
}

func (c *productionSchemaMaintenanceController) checkAbortNativeDaemon(ctx context.Context) error {
	if err := c.requireNeverClaimedHistory(); err != nil {
		return err
	}
	unit, err := c.runtime.billingUnitState(ctx, schemaMaintenanceHolderUnit(c.plan.TransitionID))
	if err != nil || validateSchemaMaintenanceAbortDaemonUnit(c.daemonGeneration, unit) != nil || schemaMaintenanceRPCProcess(c.daemonGeneration) != nil {
		return errors.New("never-claimed abort holder is not its original signed systemd process")
	}
	return nil
}

func validateSchemaMaintenanceAbortDaemonUnit(generation schemaMaintenanceRPCBinding, unit map[string]string) error {
	pid := strconv.Itoa(generation.DaemonPID)
	if validateSchemaMaintenanceRPCBinding(generation) != nil || unit["MainPID"] != pid || unit["ExecMainPID"] != pid || unit["InvocationID"] != generation.DaemonInvocationID || unit["Restart"] != "no" || unit["ActiveState"] != "active" || unit["SubState"] != "running" {
		return errors.New("never-claimed abort systemd holder generation differs")
	}
	return nil
}

func (c *productionSchemaMaintenanceController) prepareAbort(ctx context.Context, generation schemaMaintenanceRPCBinding) (productionSchemaMaintenanceAbortPrepared, error) {
	var prepared productionSchemaMaintenanceAbortPrepared
	if generation != c.daemonGeneration {
		return prepared, errors.New("abort preparation is not bound to this original native daemon")
	}
	if err := c.checkAbortNativeDaemon(ctx); err != nil {
		return prepared, err
	}
	if err := c.host.check(ctx); err != nil {
		return prepared, err
	}
	path := filepath.Join(c.origin.Workspace, "state", "abort-prepared.json")
	if _, err := os.Lstat(path); err == nil {
		// Reconcile only the exact completed preparation; never overwrite or
		// reconstruct a prepared lane in a new daemon.
		prepared, _, err = c.abortPrepared()
		if err != nil {
			return prepared, err
		}
		return prepared, c.checkNeverClaimed(ctx)
	} else if !errors.Is(err, os.ErrNotExist) {
		return prepared, err
	}
	if c.abortFrozen {
		return prepared, errors.New("abort preparation was attempted but its exact receipt is unavailable; retain this daemon and reconcile without repeating it")
	}
	if _, err := c.host.captureStopped(ctx); err != nil {
		return prepared, err
	}
	old, err := c.runtime.loadMerchantStoreCapsule(c.origin.OldCapsule.Path, c.origin.OldCapsule.SHA256)
	if err != nil {
		return prepared, err
	}
	if err := c.runtime.checkSchemaMaintenanceAbortQuiescedPreState(ctx, c.plan, c.planSHA, old, c.origin.OldCapsule.SHA256); err != nil {
		return prepared, err
	}
	stoppedPath := filepath.Join(c.origin.Workspace, "state", "writer-stopped.json")
	stoppedRaw, err := c.runtime.readExistingSchemaSealedFile(stoppedPath, true)
	if err != nil {
		return prepared, err
	}
	if err := c.requireNeverClaimedHistory(); err != nil {
		return prepared, err
	}
	prepared = productionSchemaMaintenanceAbortPrepared{Format: schemaMaintenanceAbortPreparedFormat, TransitionID: c.plan.TransitionID, PrePlanSHA256: c.planSHA, Host: c.origin.Host, DaemonGeneration: c.daemonGeneration, OldCapsule: c.origin.OldCapsule, StoppedCapture: productionReleaseFilePlan{Path: stoppedPath, SHA256: startupContentSHA256(stoppedRaw)}, GuardianBinding: productionReleaseFilePlan{Path: c.host.binding.GuardianBindingPath, SHA256: c.host.binding.GuardianBindingSHA256}}
	c.abortFrozen = true // Irreversible, including any uncertain receipt write.
	_, err = c.runtime.createSchemaMaintenanceAbortPrepared(path, prepared, c.plan, c.planSHA)
	return prepared, err
}

func (c *productionSchemaMaintenanceController) remoteAbortPrepared(ctx context.Context, origin productionSchemaMaintenanceOriginPlan) (productionSchemaMaintenanceAbortPrepared, string, error) {
	var prepared productionSchemaMaintenanceAbortPrepared
	remote := &productionReleaseRuntime{runner: c.runtime.runner, now: c.runtime.now}
	reply, err := remote.schemaMaintenanceRemoteRPC(ctx, c.plan, c.planSHA, origin, "check-abort-prestate")
	if err != nil {
		return prepared, "", err
	}
	if err := schemaMaintenanceAbortTypedResult(reply, &prepared); err != nil {
		return prepared, "", err
	}
	raw, err := canonicalProductionSchemaMaintenanceAbortPrepared(prepared, c.plan, c.planSHA, c.runtime.paths)
	if err != nil || prepared.Host != origin.Host || prepared.DaemonGeneration != reply.Binding {
		return prepared, "", errors.New("abort peer preparation differs from the actual fixed native daemon")
	}
	return prepared, startupContentSHA256(raw), nil
}

func (c *productionSchemaMaintenanceController) createAbortBinding(ctx context.Context) (productionReleaseFilePlan, error) {
	var ref productionReleaseFilePlan
	if c.origin.Host != c.plan.PrimaryHost {
		return ref, errors.New("never-claimed abort binding is created only by the original primary daemon")
	}
	if err := c.host.check(ctx); err != nil {
		return ref, err
	}
	if err := c.checkNeverClaimed(ctx); err != nil {
		return ref, err
	}
	binding := productionSchemaMaintenanceAbortBinding{Format: schemaMaintenanceAbortBindingFormat, TransitionID: c.plan.TransitionID, PrePlanSHA256: c.planSHA, Mode: schemaMaintenanceAbortModeNeverClaimed, PreSchema: c.plan.PreSchema}
	for _, origin := range c.plan.Origins {
		var prepared productionSchemaMaintenanceAbortPrepared
		var sha string
		var err error
		if origin.Host == c.origin.Host {
			prepared, sha, err = c.abortPrepared()
		} else {
			prepared, sha, err = c.remoteAbortPrepared(ctx, origin)
		}
		if err != nil {
			return ref, err
		}
		// Both origins remain actually stopped on their original hook/capsule.
		var capture productionSchemaMaintenanceHostCapture
		if origin.Host == c.origin.Host {
			capture, err = c.host.captureStopped(ctx)
		} else {
			capture, err = c.remoteCapture(ctx, origin, "capture-stopped")
		}
		captureRaw, _ := json.MarshalIndent(capture, "", "  ")
		if err != nil || capture.Phase != "WRITER_STOPPED" || startupContentSHA256(append(captureRaw, '\n')) != prepared.StoppedCapture.SHA256 {
			return ref, errors.New("abort peer no longer has its exact original sealed writer stop")
		}
		binding.Origins = append(binding.Origins, productionSchemaMaintenanceAbortOrigin{Alias: origin.Alias, Host: origin.Host, Prepared: productionReleaseFilePlan{Path: filepath.Join(origin.Workspace, "state", "abort-prepared.json"), SHA256: sha}, OldCapsule: prepared.OldCapsule, StoppedCapture: prepared.StoppedCapture, GuardianBinding: prepared.GuardianBinding})
	}
	if err := c.checkNeverClaimed(ctx); err != nil {
		return ref, err
	}
	ref.Path = filepath.Join(c.origin.Workspace, "state", "abort-binding.json")
	var err error
	ref.SHA256, err = c.runtime.createSchemaMaintenanceAbortBinding(ref.Path, binding, c.plan, c.planSHA)
	return ref, err
}

func (c *productionSchemaMaintenanceController) abortBinding() (productionSchemaMaintenanceAbortBinding, string, error) {
	var binding productionSchemaMaintenanceAbortBinding
	prepared, preparedSHA, err := c.abortPrepared()
	if err != nil {
		return binding, "", err
	}
	path := filepath.Join(c.origin.Workspace, "state", "abort-binding.json")
	raw, err := c.runtime.readExistingSchemaSealedFile(path, true)
	if err != nil {
		return binding, "", err
	}
	sha := startupContentSHA256(raw)
	binding, err = c.runtime.loadSchemaMaintenanceAbortBinding(path, sha, c.plan, c.planSHA)
	if err != nil {
		return binding, "", err
	}
	for _, origin := range binding.Origins {
		if origin.Host == c.origin.Host && (origin.Prepared.SHA256 != preparedSHA || origin.OldCapsule != prepared.OldCapsule || origin.StoppedCapture != prepared.StoppedCapture || origin.GuardianBinding != prepared.GuardianBinding) {
			return binding, "", errors.New("abort binding differs from this original daemon's exact preparation")
		}
	}
	return binding, sha, nil
}

func (c *productionSchemaMaintenanceController) validateLocalAbortPreparation(binding productionSchemaMaintenanceAbortBinding) error {
	prepared, sha, err := c.abortPrepared()
	if err != nil {
		return err
	}
	for _, origin := range binding.Origins {
		if origin.Host == c.origin.Host && origin.Prepared.SHA256 == sha && origin.OldCapsule == prepared.OldCapsule && origin.StoppedCapture == prepared.StoppedCapture && origin.GuardianBinding == prepared.GuardianBinding {
			return nil
		}
	}
	return errors.New("abort binding does not contain this original daemon's exact preparation")
}

func (c *productionSchemaMaintenanceController) installAbortBinding(ctx context.Context) (productionReleaseFilePlan, error) {
	var ref productionReleaseFilePlan
	if err := c.host.check(ctx); err != nil {
		return ref, err
	}
	if err := c.checkNeverClaimed(ctx); err != nil {
		return ref, err
	}
	ref.Path = filepath.Join(c.origin.Workspace, "state", "abort-binding.json")
	if c.origin.Host == c.plan.PrimaryHost {
		_, sha, err := c.abortBinding()
		ref.SHA256 = sha
		return ref, err // Primary only reobserves the exact already-created record.
	}
	remote := &productionReleaseRuntime{runner: c.runtime.runner, now: c.runtime.now}
	reply, err := remote.schemaMaintenanceRemoteRPC(ctx, c.plan, c.planSHA, c.plan.Origins[0], "capture-abort-binding")
	if err != nil {
		return ref, err
	}
	var binding productionSchemaMaintenanceAbortBinding
	if err := schemaMaintenanceAbortTypedResult(reply, &binding); err != nil {
		return ref, err
	}
	if _, err := canonicalProductionSchemaMaintenanceAbortBinding(binding, c.plan, c.planSHA, c.runtime.paths); err != nil {
		return ref, err
	}
	if err := c.validateLocalAbortPreparation(binding); err != nil {
		return ref, err
	}
	if _, err := c.host.captureStopped(ctx); err != nil {
		return ref, err
	}
	if err := c.checkNeverClaimed(ctx); err != nil {
		return ref, err
	}
	ref.SHA256, err = c.runtime.createSchemaMaintenanceAbortBinding(ref.Path, binding, c.plan, c.planSHA)
	return ref, err
}

type productionSchemaMaintenanceAbortCapture struct {
	AbortBindingSHA256 string                                 `json:"abort_binding_sha256"`
	PreparedSHA256     string                                 `json:"prepared_sha256"`
	Capture            productionSchemaMaintenanceHostCapture `json:"capture"`
}

func schemaMaintenanceAbortTypedResult(reply schemaMaintenanceRPCReply, result any) error {
	decoder := json.NewDecoder(bytes.NewReader(reply.Result))
	decoder.DisallowUnknownFields()
	if decoder.Decode(result) != nil {
		return errors.New("abort native reply has invalid typed evidence")
	}
	typed, err := json.Marshal(result)
	if err != nil {
		return err
	}
	copy := reply
	copy.Result = typed
	want, err := schemaMaintenanceRPCMarshal(copy)
	got, marshalErr := schemaMaintenanceRPCMarshal(reply)
	if err != nil || marshalErr != nil || !bytes.Equal(want, got) {
		return errors.New("abort native reply contains ambiguous or additional authority")
	}
	return nil
}

func (c *productionSchemaMaintenanceController) captureAbort(ctx context.Context, ready bool) (productionSchemaMaintenanceAbortCapture, error) {
	var result productionSchemaMaintenanceAbortCapture
	binding, sha, err := c.abortBinding()
	if err != nil {
		return result, err
	}
	_, preparedSHA, err := c.abortPrepared()
	if err != nil {
		return result, err
	}
	result.AbortBindingSHA256, result.PreparedSHA256 = sha, preparedSHA
	if ready {
		result.Capture, err = c.host.captureAbortOldReady(ctx, c.plan, c.planSHA, binding, c.checkNeverClaimed)
	} else {
		result.Capture, err = c.host.captureAbortOldStartable(ctx, c.plan, c.planSHA, binding, c.checkNeverClaimed)
	}
	return result, err
}

func (c *productionSchemaMaintenanceController) checkAbortOrigins(ctx context.Context, ready, restored bool) error {
	binding, sha, err := c.abortBinding()
	if err != nil {
		return err
	}
	action := "capture-abort-startable"
	if ready {
		action = "capture-abort-ready"
	}
	for i, origin := range c.plan.Origins {
		var result productionSchemaMaintenanceAbortCapture
		if origin.Host == c.origin.Host {
			result, err = c.captureAbort(ctx, ready)
		} else {
			remote := &productionReleaseRuntime{runner: c.runtime.runner, now: c.runtime.now}
			var reply schemaMaintenanceRPCReply
			reply, err = remote.schemaMaintenanceRemoteRPC(ctx, c.plan, c.planSHA, origin, action)
			if err == nil {
				err = schemaMaintenanceAbortTypedResult(reply, &result)
			}
		}
		if err != nil {
			return err
		}
		if err := c.validateAbortCapture(result, origin, binding.Origins[i], sha, ready, restored); err != nil {
			return err
		}
	}
	return nil
}

func (c *productionSchemaMaintenanceController) validateAbortCapture(result productionSchemaMaintenanceAbortCapture, origin productionSchemaMaintenanceOriginPlan, bound productionSchemaMaintenanceAbortOrigin, sha string, ready, restored bool) error {
	if result.AbortBindingSHA256 != sha || result.PreparedSHA256 != bound.Prepared.SHA256 {
		return errors.New("abort origin observation differs from the exact binding and native preparation")
	}
	capture := result.Capture
	if capture.Format != 1 || capture.TransitionID != c.plan.TransitionID || capture.PlanSHA256 != c.planSHA || capture.Host != origin.Host || capture.CapsuleSHA256 != origin.OldCapsule.SHA256 || capture.ProviderSHA256 != origin.Recovery.Candidate.PayloadSHA256 || capture.PID <= 1 || !existingSchemaInvocationPattern.MatchString(capture.InvocationID) || capture.NginxPID <= 1 || !existingSchemaInvocationPattern.MatchString(capture.NginxInvocationID) || !productionSHA256Pattern.MatchString(capture.NginxGenerationSHA256) || !productionSHA256Pattern.MatchString(capture.OriginalAdmissionSHA256) || !productionSHA256Pattern.MatchString(capture.ClosedAdmissionSHA256) || capture.StopStartedUTC.IsZero() || !productionSHA256Pattern.MatchString(capture.ShutdownJournalSHA256) {
		return errors.New("abort origin lacks its actual original sealed native generation")
	}
	if capture.Phase == "WRITER_STOPPED" && !ready && !restored {
		return nil
	}
	if capture.ConfirmedCapsuleSHA256 != origin.OldCapsule.SHA256 || capture.ConfirmedProviderSHA256 != origin.Recovery.Candidate.PayloadSHA256 || capture.ConfirmedPID <= 1 || !existingSchemaInvocationPattern.MatchString(capture.ConfirmedInvocationID) || (capture.Phase != "ABORT_RUNNING" && capture.Phase != "ADMISSION_RESTORED") || (!ready && capture.Phase != "ABORT_RUNNING") || (restored && capture.Phase != "ADMISSION_RESTORED") {
		return errors.New("abort origin has no qualified retained writer in the required actual admission phase")
	}
	return nil
}

func (c *productionSchemaMaintenanceController) checkAbortStartableOrigins(ctx context.Context) error {
	return c.checkAbortOrigins(ctx, false, false)
}
func (c *productionSchemaMaintenanceController) checkAbortReadyOrigins(ctx context.Context) error {
	return c.checkAbortOrigins(ctx, true, false)
}
func (c *productionSchemaMaintenanceController) checkAbortRestoredOrigins(ctx context.Context) error {
	return c.checkAbortOrigins(ctx, true, true)
}

func (c *productionSchemaMaintenanceController) handleAbort(ctx context.Context, request schemaMaintenanceRPCRequest) (schemaMaintenanceRPCReply, error) {
	switch request.Action {
	case "prepare-abort":
		return schemaMaintenanceRPCResult(c.prepareAbort(ctx, request.Binding))
	case "check-abort-prestate":
		if err := c.host.check(ctx); err != nil {
			return schemaMaintenanceRPCReply{}, err
		}
		if err := c.checkNeverClaimed(ctx); err != nil {
			return schemaMaintenanceRPCReply{}, err
		}
		prepared, _, err := c.abortPrepared()
		return schemaMaintenanceRPCResult(prepared, err)
	case "create-abort-binding":
		return schemaMaintenanceRPCResult(c.createAbortBinding(ctx))
	case "install-abort-binding":
		return schemaMaintenanceRPCResult(c.installAbortBinding(ctx))
	case "capture-abort-binding":
		if c.origin.Host != c.plan.PrimaryHost {
			return schemaMaintenanceRPCReply{}, errors.New("only the original primary can expose its created abort binding")
		}
		if err := c.host.check(ctx); err != nil {
			return schemaMaintenanceRPCReply{}, err
		}
		if err := c.checkNeverClaimed(ctx); err != nil {
			return schemaMaintenanceRPCReply{}, err
		}
		binding, _, err := c.abortBinding()
		return schemaMaintenanceRPCResult(binding, err) // Leaf: no peer callback.
	case "capture-abort-startable":
		return schemaMaintenanceRPCResult(c.captureAbort(ctx, false))
	case "capture-abort-ready":
		return schemaMaintenanceRPCResult(c.captureAbort(ctx, true))
	}
	binding, _, err := c.abortBinding()
	if err != nil {
		return schemaMaintenanceRPCReply{}, err
	}
	if c.finishUncertain || c.abortFinished {
		return schemaMaintenanceRPCReply{}, errors.New("abort finish was already attempted; retain the original daemon and reconcile read-only")
	}
	switch request.Action {
	case "start-abort-old":
		return schemaMaintenanceRPCResult(c.host.startAbortOld(ctx, c.plan, c.planSHA, binding, c.checkNeverClaimed, c.checkAbortStartableOrigins))
	case "reopen-abort":
		return schemaMaintenanceRPCResult(c.host.restoreAbortOldAdmission(ctx, c.plan, c.planSHA, binding, c.checkNeverClaimed, c.checkAbortReadyOrigins))
	case "finish-abort":
		if err := c.checkAbortRestoredOrigins(ctx); err != nil {
			return schemaMaintenanceRPCReply{}, err
		}
		c.finishUncertain = true
		terminal, err := c.host.finishAbortOld(ctx, c.plan, c.planSHA, binding, c.checkNeverClaimed, c.checkAbortRestoredOrigins)
		if err != nil {
			return schemaMaintenanceRPCReply{}, err
		}
		c.finishUncertain, c.abortFinished = false, true
		return schemaMaintenanceRPCResult(terminal, nil)
	}
	return schemaMaintenanceRPCReply{}, errors.New("abort RPC action is not executable")
}

func (c *productionSchemaMaintenanceController) abortStatus(ctx context.Context) (schemaMaintenanceRPCReply, error) {
	phase := "ABORT_PREPARATION_UNCERTAIN"
	if c.finishUncertain {
		phase = "ABORT_FINISH_UNCERTAIN"
	} else if c.abortFinished {
		capture, err := c.captureAbort(ctx, true)
		if err != nil {
			return schemaMaintenanceRPCReply{}, err
		}
		return schemaMaintenanceRPCResult(map[string]any{"phase": "ABORT_HOST_FINISHED", "host": c.origin.Host, "transition_id": c.plan.TransitionID, "plan_sha256": c.planSHA, "actual_ready": capture}, nil)
	} else if _, _, err := c.abortPrepared(); err == nil {
		phase = "ABORT_PREPARED"
	}
	return schemaMaintenanceRPCResult(map[string]string{"phase": phase, "host": c.origin.Host, "transition_id": c.plan.TransitionID, "plan_sha256": c.planSHA}, nil)
}
