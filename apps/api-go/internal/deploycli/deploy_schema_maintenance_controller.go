package deploycli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/internal/deploymentfence"
)

// Each registered origin owns one signed-engine daemon and one adopted
// three-descriptor host lease. The primary daemon additionally owns the sole PG
// fence. RPC commands never construct a second host or re-adopt those locks.
type productionSchemaMaintenanceController struct {
	actionMu          sync.Mutex
	runtime           *productionRuntime
	ownerContext      context.Context
	plan              productionSchemaMaintenancePlan
	planPath, planSHA string
	origin            productionSchemaMaintenanceOriginPlan
	host              *productionSchemaMaintenanceHost
	fence             *productionSchemaMaintenanceFence
	claimUncertain    bool
	finishUncertain   bool
	finished          bool
	abortFrozen       bool
	abortFinished     bool
	daemonGeneration  schemaMaintenanceRPCBinding
}

func (runtime *productionRuntime) newSchemaMaintenanceController(ctx context.Context, planPath, planSHA, projectionPath, projectionSHA string) (*productionSchemaMaintenanceController, error) {
	plan, err := runtime.loadSchemaMaintenancePlan(planPath, planSHA)
	if err != nil {
		return nil, err
	}
	name, err := runtime.hostname()
	if err != nil {
		return nil, err
	}
	var origin productionSchemaMaintenanceOriginPlan
	for _, candidate := range plan.Origins {
		if candidate.Host == name {
			origin = candidate
		}
	}
	if origin.Host == "" || projectionPath != filepath.Join(origin.Workspace, "state", "host-binding.json") {
		return nil, errors.New("schema controller is not one fixed registered origin")
	}
	raw, err := runtime.readExistingSchemaSealedFile(projectionPath, true)
	if err != nil || startupContentSHA256(raw) != projectionSHA {
		return nil, errors.New("schema controller exact local host projection unavailable")
	}
	var binding productionSchemaMaintenanceHostBinding
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&binding) != nil {
		return nil, errors.New("schema controller host projection invalid")
	}
	canonical, _ := json.MarshalIndent(binding, "", "  ")
	if !bytes.Equal(append(canonical, '\n'), raw) || binding.TransitionID != plan.TransitionID || binding.PlanPath != planPath || binding.PlanSHA256 != planSHA || binding.Host != origin.Host || binding.CapsulePath != origin.OldCapsule.Path || binding.CapsuleSHA256 != origin.OldCapsule.SHA256 || binding.Workspace != origin.Workspace || binding.GuardianSocket != origin.Guardian.Socket || binding.GuardianBindingPath != origin.Guardian.BindingPath || binding.GuardianProtocol != origin.Guardian.Protocol {
		return nil, errors.New("schema controller host projection differs from the canonical pre-change plan")
	}
	c := &productionSchemaMaintenanceController{runtime: runtime, ownerContext: ctx, plan: plan, planPath: planPath, planSHA: planSHA, origin: origin}
	if err := c.checkPlan(ctx); err != nil {
		return nil, err
	}
	artifacts, err := runtime.loadMerchantStoreCapsule(origin.ArtifactCapsule.Path, origin.ArtifactCapsule.SHA256)
	if err != nil {
		return nil, err
	}
	if _, err = runtime.verifyMerchantStoreCapsuleArtifact(ctx, artifacts, false); err != nil {
		return nil, err
	}
	if sha256MustEqual("/proc/self/exe", plan.Worker.Provider.DeployEngineSHA256) != nil {
		return nil, errors.New("schema controller actual signed engine differs from its fixed worker release")
	}
	c.host, err = runtime.newSchemaMaintenanceHost(ctx, binding, c.checkPlan)
	if err != nil {
		return nil, err
	}
	return c, nil
}

func (c *productionSchemaMaintenanceController) checkPlan(ctx context.Context) error {
	actual, err := c.runtime.loadSchemaMaintenancePlan(c.planPath, c.planSHA)
	if err != nil {
		return err
	}
	want, _ := canonicalProductionSchemaMaintenancePlan(c.plan, c.runtime.paths)
	got, _ := canonicalProductionSchemaMaintenancePlan(actual, c.runtime.paths)
	if !bytes.Equal(want, got) {
		return errors.New("schema controller pre-change plan changed")
	}
	old, err := c.runtime.loadMerchantStoreCapsule(c.origin.OldCapsule.Path, c.origin.OldCapsule.SHA256)
	if err != nil {
		return err
	}
	artifacts, err := c.runtime.loadMerchantStoreCapsule(c.origin.ArtifactCapsule.Path, c.origin.ArtifactCapsule.SHA256)
	if err != nil {
		return err
	}
	if old.Format != 1 || old.StartupPolicy != "per-start" || artifacts.Format != 1 || artifacts.StartupPolicy != "held" || artifacts.DeploymentID != c.plan.TransitionID || artifacts.Candidate != c.plan.Worker.Provider || artifacts.Rollback != merchantStoreCapsuleProjection(old.Candidate, "rollback") || old.Candidate != c.origin.Recovery.Candidate || old.Rollback != c.origin.Recovery.Rollback || artifacts.Writer.Role != c.plan.Role || artifacts.Writer.RequiredCapability != c.plan.RequiredCapability {
		return errors.New("schema controller artifact/retained/recovery identities differ from the real plan")
	}
	for _, capsule := range []productionMerchantStoreCapsule{old, artifacts} {
		if capsule.ExistingSchemaContract == nil {
			return errors.New("schema controller real capsule lacks its original physical contract")
		}
		physical := *capsule.ExistingSchemaContract
		physical.StartupSHA256, physical.SignedUnitSHA256 = "", ""
		if physical != c.plan.PreSchema {
			return errors.New("schema controller original physical catalog differs from its real capsules")
		}
	}
	return nil
}

type productionSchemaMaintenanceActiveOwner struct {
	Format         string `json:"format"`
	TransitionID   string `json:"transition_id"`
	PlanSHA256     string `json:"plan_sha256"`
	OwnerSHA256    string `json:"owner_sha256"`
	ProviderSHA256 string `json:"provider_sha256"`
	PID            int    `json:"pid"`
	InvocationID   string `json:"invocation_id"`
}

func (c *productionSchemaMaintenanceController) activeOwner(ctx context.Context) (productionSchemaMaintenanceActiveOwner, error) {
	var active productionSchemaMaintenanceActiveOwner
	if c.origin.Host != c.plan.PrimaryHost || c.fence == nil || c.fence.released {
		return active, errors.New("schema transition has no original primary physical owner")
	}
	if err := c.fence.CheckPhysical(ctx); err != nil {
		return active, err
	}
	return productionSchemaMaintenanceActiveOwner{"lmm-schema-maintenance-active-owner-v1", c.plan.TransitionID, c.planSHA, startupContentSHA256(c.fence.ownerRaw), c.fence.owner.ProviderSHA256, c.fence.owner.HolderPID, c.fence.owner.HolderInvocationID}, nil
}

func (c *productionSchemaMaintenanceController) checkActiveOwner(ctx context.Context) error {
	if c.origin.Host == c.plan.PrimaryHost {
		_, err := c.activeOwner(ctx)
		return err
	}
	// The primary action is a leaf: it checks only its already-held physical
	// owner and local three-FD lease, and never asks this secondary to call back.
	remote := &productionReleaseRuntime{runner: c.runtime.runner, now: c.runtime.now}
	reply, err := remote.schemaMaintenanceRemoteRPC(ctx, c.plan, c.planSHA, c.plan.Origins[0], "check-schema-owner")
	if err != nil {
		return err
	}
	var active productionSchemaMaintenanceActiveOwner
	decoder := json.NewDecoder(bytes.NewReader(reply.Result))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&active) != nil {
		return errors.New("schema primary physical owner reply is invalid")
	}
	ownerPath := filepath.Join(c.origin.Workspace, "state", "schema-owner.json")
	owner, _, err := deploymentfence.ReadSchemaMaintenanceOwner(ownerPath, active.OwnerSHA256)
	if err != nil || active.Format != "lmm-schema-maintenance-active-owner-v1" || active.TransitionID != c.plan.TransitionID || active.PlanSHA256 != c.planSHA || active.ProviderSHA256 != c.plan.Worker.Provider.PayloadSHA256 || active.PID != owner.HolderPID || active.InvocationID != owner.HolderInvocationID || owner.PlanSHA256 != c.planSHA || owner.Host != c.plan.PrimaryHost || owner.DeploymentID != c.plan.TransitionID || owner.ProviderSHA256 != active.ProviderSHA256 {
		return errors.New("schema secondary original staged owner differs from the actual held primary owner")
	}
	typed, _ := json.Marshal(active)
	typedReply := reply
	typedReply.Result = typed
	want, _ := schemaMaintenanceRPCMarshal(typedReply)
	got, _ := schemaMaintenanceRPCMarshal(reply)
	if !bytes.Equal(want, got) {
		return errors.New("schema primary physical owner has ambiguous typed fields")
	}
	return nil
}

func schemaMaintenanceSSHWord(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func (c *productionSchemaMaintenanceController) remoteCapture(ctx context.Context, origin productionSchemaMaintenanceOriginPlan, action string) (productionSchemaMaintenanceHostCapture, error) {
	var capture productionSchemaMaintenanceHostCapture
	// This path is generated by verified capsule extraction. The remote signed
	// client verifies its actual executable and the daemon's native PID generation
	// against the same canonical plan before delivering this fixed action.
	engine := filepath.Join(filepath.Dir(origin.ArtifactCapsule.Path), "tmp", "migrations", "merchant-store-candidate", deployEngineName)
	budget, err := schemaMaintenanceRPCActionTimeout(action)
	if err != nil {
		return capture, err
	}
	words := []string{engine, "production", "schema-maintenance", "rpc", "--plan", c.planPath, "--plan-sha256", c.planSHA, "--action", action}
	for index := range words {
		words[index] = schemaMaintenanceSSHWord(words[index])
	}
	output, err := c.runtime.runner.Run(ctx, productionCommand{Name: commandSSH, Args: []string{"-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "-o", "ControlMaster=no", "-o", "ControlPath=none", "-o", "ConnectTimeout=10", origin.Alias, strings.Join(words, " ")}, Timeout: budget + 10*time.Second, OutputLimit: 65536})
	if err != nil {
		return capture, errors.New("schema controller remote native capture unavailable; reconcile without repeating mutation")
	}
	var reply schemaMaintenanceRPCReply
	if schemaMaintenanceRPCDecode(bytes.TrimSuffix(output, []byte{'\n'}), &reply) != nil || !reply.OK || reply.Protocol != schemaMaintenanceRPCProtocol || !productionSHA256Pattern.MatchString(reply.Nonce) || validateSchemaMaintenanceRPCBinding(reply.Binding) != nil || reply.Action != action || reply.Binding.PlanSHA256 != c.planSHA || reply.Binding.TransitionID != c.plan.TransitionID || reply.Binding.DaemonExeSHA256 != c.plan.Worker.Provider.DeployEngineSHA256 {
		return capture, errors.New("schema controller remote reply differs from the fixed native daemon")
	}
	decoder := json.NewDecoder(bytes.NewReader(reply.Result))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&capture) != nil || capture.Format != 1 || capture.Host != origin.Host || capture.PlanSHA256 != c.planSHA || capture.TransitionID != c.plan.TransitionID || capture.CapsuleSHA256 != origin.OldCapsule.SHA256 || capture.ProviderSHA256 != origin.Recovery.Candidate.PayloadSHA256 {
		return capture, errors.New("schema controller remote capture differs from the immutable fixed origin")
	}
	typedResult, _ := json.Marshal(capture)
	typedReply := reply
	typedReply.Result = typedResult
	canonicalTypedReply, err := schemaMaintenanceRPCMarshal(typedReply)
	if err != nil || !bytes.Equal(canonicalTypedReply, bytes.TrimSuffix(output, []byte{'\n'})) {
		return capture, errors.New("schema controller remote capture has ambiguous typed fields")
	}
	if err := c.validateObservedCapture(capture, origin, action); err != nil {
		return capture, err
	}
	return capture, nil
}

func (c *productionSchemaMaintenanceController) validateObservedCapture(capture productionSchemaMaintenanceHostCapture, origin productionSchemaMaintenanceOriginPlan, action string) error {
	if capture.Format != 1 || capture.TransitionID != c.plan.TransitionID || capture.PlanSHA256 != c.planSHA || capture.Host != origin.Host || capture.CapsuleSHA256 != origin.OldCapsule.SHA256 || capture.ProviderSHA256 != origin.Recovery.Candidate.PayloadSHA256 || capture.PID <= 1 || !existingSchemaInvocationPattern.MatchString(capture.InvocationID) || !productionSHA256Pattern.MatchString(capture.OriginalAdmissionSHA256) || !productionSHA256Pattern.MatchString(capture.ClosedAdmissionSHA256) || capture.NginxPID <= 1 || !existingSchemaInvocationPattern.MatchString(capture.NginxInvocationID) || !productionSHA256Pattern.MatchString(capture.NginxGenerationSHA256) {
		return errors.New("schema origin capture lacks its complete actual bound generation")
	}
	switch action {
	case "capture-closed":
		if capture.Phase != "ADMISSION_CLOSED" && capture.Phase != "STOPPED_EXIT_ONLY" {
			return errors.New("schema origin has no actual closed admission")
		}
	case "capture-stopped":
		if capture.Phase != "WRITER_STOPPED" || capture.StopStartedUTC.IsZero() || !productionSHA256Pattern.MatchString(capture.ShutdownJournalSHA256) {
			return errors.New("schema origin has no exact sealed writer stop")
		}
	case "capture-recovery", "capture-ready", "capture-startable":
		binding, err := c.recoveryBinding()
		if err != nil {
			return err
		}
		matched := false
		for _, post := range binding.Origins {
			if post.Host == origin.Host {
				matched = capture.ConfirmedCapsuleSHA256 == post.Capsule.SHA256 && capture.ConfirmedProviderSHA256 == origin.Recovery.Candidate.PayloadSHA256
			}
		}
		if !matched {
			return errors.New("schema origin qualified a different recovery binding")
		}
		if action == "capture-startable" {
			if capture.Phase != "RECOVERY_READY" && capture.Phase != "RECOVERY_RUNNING" {
				return errors.New("schema start requires stopped or closed running origins on the exact recovery binding")
			}
			if capture.Phase == "RECOVERY_READY" && (capture.StopStartedUTC.IsZero() || !productionSHA256Pattern.MatchString(capture.ShutdownJournalSHA256)) {
				return errors.New("schema startable stopped peer lacks its original sealed stop")
			}
			if capture.Phase == "RECOVERY_RUNNING" && (capture.ConfirmedPID <= 1 || !existingSchemaInvocationPattern.MatchString(capture.ConfirmedInvocationID)) {
				return errors.New("schema startable running peer lacks its actual new writer generation")
			}
		} else if action == "capture-recovery" {
			if capture.Phase != "RECOVERY_READY" || capture.StopStartedUTC.IsZero() || !productionSHA256Pattern.MatchString(capture.ShutdownJournalSHA256) {
				return errors.New("schema origin is not actually stopped on its new recovery capsule")
			}
		} else if (capture.Phase != "RECOVERY_RUNNING" && capture.Phase != "ADMISSION_RESTORED") || capture.ConfirmedPID <= 1 || !existingSchemaInvocationPattern.MatchString(capture.ConfirmedInvocationID) {
			return errors.New("schema origin has no actual recovery writer generation")
		}
	default:
		return errors.New("schema controller capture action is not an observation")
	}
	return nil
}

func (c *productionSchemaMaintenanceController) captureStartable(ctx context.Context) (productionSchemaMaintenanceHostCapture, error) {
	var capture productionSchemaMaintenanceHostCapture
	binding, err := c.recoveryBinding()
	if err != nil {
		return capture, err
	}
	state, err := c.runtime.billingUnitState(ctx, c.runtime.paths.Service)
	if err != nil {
		return capture, err
	}
	if state["ActiveState"] == "active" && state["SubState"] == "running" {
		capture, err = c.host.captureReady(ctx, c.plan, c.planSHA, binding)
	} else {
		capture, err = c.host.captureRecovery(ctx, c.plan, c.planSHA, binding)
	}
	if err != nil {
		return capture, err
	}
	return capture, c.validateObservedCapture(capture, c.origin, "capture-startable")
}

func (c *productionSchemaMaintenanceController) checkStartableOrigins(ctx context.Context) error {
	for _, origin := range c.plan.Origins {
		if origin.Host == c.origin.Host {
			if _, err := c.captureStartable(ctx); err != nil {
				return err
			}
		} else if _, err := c.remoteCapture(ctx, origin, "capture-startable"); err != nil {
			return err
		}
	}
	return nil
}

func (c *productionSchemaMaintenanceController) stoppedOrigins(ctx context.Context) ([]productionSchemaMaintenanceHostCapture, error) {
	captures := make([]productionSchemaMaintenanceHostCapture, 0, len(c.plan.Origins))
	for _, origin := range c.plan.Origins {
		var capture productionSchemaMaintenanceHostCapture
		var err error
		if origin.Host == c.origin.Host {
			capture, err = c.host.captureStopped(ctx)
		} else {
			capture, err = c.remoteCapture(ctx, origin, "capture-stopped")
		}
		if err != nil {
			return nil, err
		}
		if capture.Phase != "WRITER_STOPPED" {
			return nil, errors.New("schema controller origin has no sealed original stop proof")
		}
		captures = append(captures, capture)
	}
	return captures, nil
}
func (c *productionSchemaMaintenanceController) checkStoppedOrigins(ctx context.Context) error {
	// Once the append-only post binding exists, every stopped-origin check must
	// validate its real new hook and catalog. The retained old seal is never
	// rewritten or quietly treated as compatible with that changed startup.
	if _, err := os.Lstat(filepath.Join(c.origin.Workspace, "state", "recovery-binding.json")); err == nil {
		return c.checkRecoveryOrigins(ctx)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	_, err := c.stoppedOrigins(ctx)
	return err
}
func (c *productionSchemaMaintenanceController) checkClosedOrigins(ctx context.Context) error {
	for _, origin := range c.plan.Origins {
		var capture productionSchemaMaintenanceHostCapture
		var err error
		if origin.Host == c.origin.Host {
			capture, err = c.host.captureClosed(ctx)
		} else {
			capture, err = c.remoteCapture(ctx, origin, "capture-closed")
		}
		if err != nil {
			return err
		}
		if capture.Phase != "ADMISSION_CLOSED" && capture.Phase != "STOPPED_EXIT_ONLY" {
			return errors.New("schema controller origin admission is not actually closed")
		}
	}
	return nil
}
func (c *productionSchemaMaintenanceController) checkBackup(ctx context.Context) error {
	stopped, err := c.stoppedOrigins(ctx)
	if err != nil {
		return err
	}
	return c.runtime.verifySchemaMaintenanceBackup(ctx, c.plan, c.planSHA, stopped)
}

func (c *productionSchemaMaintenanceController) captureBackupSource(ctx context.Context) (productionSchemaMaintenanceBackupSource, error) {
	var source productionSchemaMaintenanceBackupSource
	if c.origin.Host != c.plan.PrimaryHost || c.fence != nil {
		return source, errors.New("schema backup source requires the original quiesced primary before native owner claim")
	}
	if err := c.checkStoppedOrigins(ctx); err != nil {
		return source, err
	}
	if err := c.runtime.verifyExistingSchemaContract(ctx, &c.plan.PreSchema); err != nil {
		return source, err
	}
	old, err := c.runtime.loadMerchantStoreCapsule(c.origin.OldCapsule.Path, c.origin.OldCapsule.SHA256)
	if err != nil {
		return source, err
	}
	child, err := c.runtime.merchantStoreCapsuleEnvironment(ctx, old, c.origin.OldCapsule.SHA256, "")
	if err != nil {
		return source, err
	}
	values := map[string]string{}
	for _, entry := range child {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			return source, errors.New("schema backup source environment malformed")
		}
		values[key] = value
	}
	databaseURL, environment, err := productionDatabaseCommand(values)
	if err != nil {
		return source, err
	}
	query, err := schemaMaintenanceBackupSourceQuery(c.plan.PreSchema.Schema)
	if err != nil {
		return source, err
	}
	output, err := c.runtime.runner.Run(ctx, productionCommand{Name: commandPSQL, Args: []string{"-X", "-q", "-v", "ON_ERROR_STOP=1", "--no-align", "--tuples-only", "--command", query, databaseURL}, Env: environment, Sensitive: true, Timeout: 2 * time.Minute, OutputLimit: schemaMaintenanceBackupSourceMaxBytes})
	if err != nil {
		return source, errors.New("schema backup quiesced source snapshot unavailable")
	}
	source, err = decodeSchemaMaintenanceBackupSource(bytes.TrimSpace(output))
	if err != nil {
		return source, err
	}
	if err := schemaMaintenanceBackupSourceIdentity(source, c.plan.PreSchema); err != nil {
		return source, err
	}
	if err := c.checkStoppedOrigins(ctx); err != nil {
		return source, err
	}
	return source, nil
}
func (c *productionSchemaMaintenanceController) recoveryBinding() (productionSchemaMaintenanceRecoveryBinding, error) {
	path := filepath.Join(c.origin.Workspace, "state", "recovery-binding.json")
	raw, err := c.runtime.readExistingSchemaSealedFile(path, true)
	if err != nil {
		return productionSchemaMaintenanceRecoveryBinding{}, err
	}
	return c.runtime.loadSchemaMaintenanceRecoveryBinding(path, startupContentSHA256(raw), c.plan, c.planSHA)
}
func (c *productionSchemaMaintenanceController) checkRecoveryOrigins(ctx context.Context) error {
	binding, err := c.recoveryBinding()
	if err != nil {
		return err
	}
	for _, origin := range c.plan.Origins {
		if origin.Host == c.origin.Host {
			_, err = c.host.captureRecovery(ctx, c.plan, c.planSHA, binding)
		} else {
			_, err = c.remoteCapture(ctx, origin, "capture-recovery")
		}
		if err != nil {
			return err
		}
	}
	return nil
}
func (c *productionSchemaMaintenanceController) checkReadyOrigins(ctx context.Context) error {
	binding, err := c.recoveryBinding()
	if err != nil {
		return err
	}
	for _, origin := range c.plan.Origins {
		if origin.Host == c.origin.Host {
			_, err = c.host.captureReady(ctx, c.plan, c.planSHA, binding)
		} else {
			_, err = c.remoteCapture(ctx, origin, "capture-ready")
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (c *productionSchemaMaintenanceController) checkRestoredOrigins(ctx context.Context) error {
	binding, err := c.recoveryBinding()
	if err != nil {
		return err
	}
	for _, origin := range c.plan.Origins {
		var capture productionSchemaMaintenanceHostCapture
		if origin.Host == c.origin.Host {
			capture, err = c.host.captureReady(ctx, c.plan, c.planSHA, binding)
		} else {
			capture, err = c.remoteCapture(ctx, origin, "capture-ready")
		}
		if err != nil {
			return err
		}
		if capture.Phase != "ADMISSION_RESTORED" {
			return errors.New("schema maintenance finish requires both origins actually reopened on the qualified retained provider")
		}
	}
	return nil
}

func schemaMaintenanceRPCResult(value any, err error) (schemaMaintenanceRPCReply, error) {
	if err != nil {
		return schemaMaintenanceRPCReply{}, err
	}
	raw, err := json.Marshal(value)
	return schemaMaintenanceRPCReply{Result: raw}, err
}
func (c *productionSchemaMaintenanceController) handle(ctx context.Context, request schemaMaintenanceRPCRequest) (schemaMaintenanceRPCReply, error) {
	c.actionMu.Lock()
	defer c.actionMu.Unlock()
	if request.Binding.PlanSHA256 != c.planSHA || request.Binding.TransitionID != c.plan.TransitionID {
		return schemaMaintenanceRPCReply{}, errors.New("schema RPC action is bound to another transition")
	}
	if err := c.checkPlan(ctx); err != nil {
		return schemaMaintenanceRPCReply{}, err
	}
	if err := c.checkAbortActionAllowed(request.Action); err != nil {
		return schemaMaintenanceRPCReply{}, err
	}
	if schemaMaintenanceAbortAction(request.Action) {
		return c.handleAbort(ctx, request)
	}
	switch request.Action {
	case "check-schema-owner":
		return schemaMaintenanceRPCResult(c.activeOwner(ctx))
	case "close":
		return schemaMaintenanceRPCResult(c.host.closeAdmission(ctx))
	case "capture-closed":
		return schemaMaintenanceRPCResult(c.host.captureClosed(ctx))
	case "stop":
		return schemaMaintenanceRPCResult(c.host.stopWriter(ctx, c.checkClosedOrigins))
	case "capture-stopped":
		return schemaMaintenanceRPCResult(c.host.sealStopped(ctx))
	case "capture-backup-source":
		return schemaMaintenanceRPCResult(c.captureBackupSource(ctx))
	case "backup-export":
		return schemaMaintenanceRPCResult(c.collectBackup(ctx))
	case "claim":
		if c.origin.Host != c.plan.PrimaryHost || c.fence != nil || c.claimUncertain {
			return schemaMaintenanceRPCReply{}, errors.New("schema owner claim is primary-only and one-shot")
		}
		artifacts, err := c.runtime.loadMerchantStoreCapsule(c.origin.ArtifactCapsule.Path, c.origin.ArtifactCapsule.SHA256)
		if err != nil {
			return schemaMaintenanceRPCReply{}, err
		}
		old, err := c.runtime.loadMerchantStoreCapsule(c.origin.OldCapsule.Path, c.origin.OldCapsule.SHA256)
		if err != nil {
			return schemaMaintenanceRPCReply{}, err
		}
		authority := productionSchemaMaintenanceAuthority{OwnerContext: c.ownerContext, TransitionID: c.plan.TransitionID, PlanSHA256: c.planSHA, Workspace: c.origin.Workspace, Artifacts: artifacts, OldCapsule: old, OldCapsuleSHA256: c.origin.OldCapsule.SHA256, CheckPlan: c.checkNormalPlan, CheckStoppedOrigins: c.checkStoppedOrigins, CheckRecoveryOrigins: c.checkRecoveryOrigins, CheckBackup: c.checkBackup}
		intent := map[string]string{"format": "lmm-schema-maintenance-claim-intent-v1", "transition_id": c.plan.TransitionID, "plan_sha256": c.planSHA}
		if err := c.runtime.startupBaselineNewFile(filepath.Join(c.origin.Workspace, "state", "schema-claim-intent.json"), intent); err != nil {
			return schemaMaintenanceRPCReply{}, errors.New("schema claim was already attempted; reconcile the exact original owner without repeating claim")
		}
		c.claimUncertain = true
		c.fence, err = c.runtime.claimSchemaMaintenanceFence(ctx, authority, c.host.lease)
		if err != nil {
			return schemaMaintenanceRPCReply{}, err
		}
		c.claimUncertain = false
		return schemaMaintenanceRPCResult(c.fence.owner, nil)
	case "worker":
		return schemaMaintenanceRPCResult(c.runtime.runSchemaMaintenanceWorker(ctx, c.fence))
	case "capture-outcome":
		if c.origin.Host != c.plan.PrimaryHost || c.fence == nil || c.fence.released {
			return schemaMaintenanceRPCReply{}, errors.New("schema outcome requires the original live primary owner")
		}
		stopped, err := c.stoppedOrigins(ctx)
		if err != nil {
			return schemaMaintenanceRPCReply{}, err
		}
		if _, err := c.runtime.captureSchemaMaintenanceOutcome(ctx, c.fence, c.plan, c.planSHA, stopped); err != nil {
			return schemaMaintenanceRPCReply{}, err
		}
		_, sha, err := c.runtime.readSchemaMaintenanceOutcome(c.plan, c.planSHA, startupContentSHA256(c.fence.ownerRaw))
		return schemaMaintenanceRPCResult(productionReleaseFilePlan{Path: filepath.Join(c.origin.Workspace, "state", "schema-outcome.json"), SHA256: sha}, err)
	case "capture-recovery":
		binding, err := c.recoveryBinding()
		if err != nil {
			return schemaMaintenanceRPCReply{}, err
		}
		return schemaMaintenanceRPCResult(c.host.captureRecovery(ctx, c.plan, c.planSHA, binding))
	case "capture-ready":
		binding, err := c.recoveryBinding()
		if err != nil {
			return schemaMaintenanceRPCReply{}, err
		}
		return schemaMaintenanceRPCResult(c.host.captureReady(ctx, c.plan, c.planSHA, binding))
	case "capture-startable":
		return schemaMaintenanceRPCResult(c.captureStartable(ctx))
	case "capture-recovery-profile":
		return schemaMaintenanceRPCResult(c.host.captureRecoveryProfile(ctx, c.plan, c.planSHA, c.checkActiveOwner))
	case "install-recovery-hook":
		binding, err := c.recoveryBinding()
		if err != nil {
			return schemaMaintenanceRPCReply{}, err
		}
		return schemaMaintenanceRPCResult(c.host.installRecoveryHook(ctx, c.plan, c.planSHA, binding, c.checkActiveOwner))
	case "start-recovery":
		binding, err := c.recoveryBinding()
		if err != nil {
			return schemaMaintenanceRPCReply{}, err
		}
		return schemaMaintenanceRPCResult(c.host.startRecovery(ctx, c.plan, c.planSHA, binding, c.checkStartableOrigins))
	case "release":
		if c.origin.Host != c.plan.PrimaryHost || c.fence == nil {
			return schemaMaintenanceRPCReply{}, errors.New("schema owner release has no original same-process primary owner")
		}
		if err := c.fence.Release(ctx); err != nil {
			return schemaMaintenanceRPCReply{}, err
		}
		return schemaMaintenanceRPCResult(map[string]string{"phase": "OWNER_RELEASED", "transition_id": c.plan.TransitionID}, nil)
	case "reopen":
		binding, err := c.recoveryBinding()
		if err != nil {
			return schemaMaintenanceRPCReply{}, err
		}
		for _, origin := range binding.Origins {
			if origin.Host == c.origin.Host {
				return schemaMaintenanceRPCResult(c.host.restoreAdmission(ctx, origin.Capsule.Path, origin.Capsule.SHA256, c.checkReadyOrigins))
			}
		}
		return schemaMaintenanceRPCReply{}, errors.New("schema recovery binding omitted current origin")
	case "finish":
		if c.finishUncertain || c.finished {
			return schemaMaintenanceRPCReply{}, errors.New("schema guardian finish already attempted; reconcile the original exact outcome without repeating it")
		}
		if c.origin.Host == c.plan.PrimaryHost && (c.fence == nil || !c.fence.released || c.claimUncertain) {
			return schemaMaintenanceRPCReply{}, errors.New("schema finish requires the original primary owner release")
		}
		if err := c.checkRestoredOrigins(ctx); err != nil {
			return schemaMaintenanceRPCReply{}, err
		}
		binding, err := c.recoveryBinding()
		if err != nil {
			return schemaMaintenanceRPCReply{}, err
		}
		capture, err := c.host.captureReady(ctx, c.plan, c.planSHA, binding)
		if err != nil || capture.Phase != "ADMISSION_RESTORED" {
			return schemaMaintenanceRPCReply{}, errors.New("schema finish lost its actual local reopened writer proof")
		}
		bindingRaw, err := canonicalProductionSchemaMaintenanceRecoveryBinding(binding, c.plan, c.planSHA, c.runtime.paths)
		if err != nil {
			return schemaMaintenanceRPCReply{}, err
		}
		c.finishUncertain = true // Reserve before the only potentially accepted finish send.
		if err := finishSchemaMaintenanceHostLocks(ctx, c.host.lease); err != nil {
			return schemaMaintenanceRPCReply{}, err
		}
		terminal := productionSchemaMaintenanceHostFinished{Format: "lmm-schema-maintenance-host-finished-v1", TransitionID: c.plan.TransitionID, PlanSHA256: c.planSHA, Host: c.origin.Host, GuardianBindingSHA256: c.host.binding.GuardianBindingSHA256, RecoveryBindingSHA256: startupContentSHA256(bindingRaw), CapsuleSHA256: capture.ConfirmedCapsuleSHA256, ProviderSHA256: capture.ConfirmedProviderSHA256, PID: capture.ConfirmedPID, InvocationID: capture.ConfirmedInvocationID, FinishedUTC: c.runtime.now().UTC()}
		if err := c.runtime.startupBaselineNewFile(filepath.Join(c.origin.Workspace, "state", "host-finished.json"), terminal); err != nil {
			return schemaMaintenanceRPCReply{}, errors.New("schema guardian acknowledged finish but terminal receipt failed; preserve daemon and reconcile without another finish")
		}
		c.finishUncertain, c.finished = false, true
		return schemaMaintenanceRPCResult(terminal, nil)
	case "status":
		if c.abortFrozen {
			return c.abortStatus(ctx)
		}
		if c.claimUncertain {
			return schemaMaintenanceRPCResult(map[string]string{"phase": "CLAIM_UNCERTAIN", "host": c.origin.Host, "transition_id": c.plan.TransitionID, "plan_sha256": c.planSHA}, nil)
		}
		if c.finishUncertain {
			return schemaMaintenanceRPCResult(map[string]string{"phase": "FINISH_UNCERTAIN", "host": c.origin.Host, "transition_id": c.plan.TransitionID, "plan_sha256": c.planSHA}, nil)
		}
		if c.finished {
			binding, err := c.recoveryBinding()
			if err != nil {
				return schemaMaintenanceRPCReply{}, err
			}
			capture, err := c.host.captureReady(ctx, c.plan, c.planSHA, binding)
			if err != nil {
				return schemaMaintenanceRPCReply{}, err
			}
			return schemaMaintenanceRPCResult(map[string]any{"phase": "HOST_FINISHED", "host": c.origin.Host, "transition_id": c.plan.TransitionID, "plan_sha256": c.planSHA, "actual_ready": capture}, nil)
		}
		if err := c.host.check(ctx); err != nil {
			return schemaMaintenanceRPCReply{}, err
		}
		phase := "PREPARED"
		if c.fence != nil {
			if c.fence.released {
				phase = "OWNER_RELEASED"
			} else {
				if err := c.fence.CheckPhysical(ctx); err != nil {
					return schemaMaintenanceRPCReply{}, err
				}
				phase = "OWNER_ACTIVE"
				if _, err := os.Lstat(filepath.Join(c.origin.Workspace, "state", "schema-worker-intent.json")); err == nil {
					if _, err := c.runtime.readSchemaMaintenanceWorkerResult(c.origin.Workspace, c.fence.owner, startupContentSHA256(c.fence.ownerRaw)); err != nil {
						phase = "WORKER_RESULT_UNCERTAIN"
					} else {
						phase = "WORKER_SUCCEEDED"
					}
				} else if !errors.Is(err, os.ErrNotExist) {
					return schemaMaintenanceRPCReply{}, err
				}
			}
		}
		return schemaMaintenanceRPCResult(map[string]string{"phase": phase, "host": c.origin.Host, "transition_id": c.plan.TransitionID, "plan_sha256": c.planSHA}, nil)
	default:
		return schemaMaintenanceRPCReply{}, fmt.Errorf("schema controller has no executable action %q", request.Action)
	}
}
