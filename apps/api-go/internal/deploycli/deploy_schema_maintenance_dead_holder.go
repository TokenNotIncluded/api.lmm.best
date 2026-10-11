package deploycli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const schemaMaintenanceDeadHolderMode = "DEAD_DAEMON_PREWORKER"

// This lane preserves the original evidence and never recreates NEVER_CLAIMED
// history. Its fresh authority is confined to one exact dead pre-worker owner.
type productionSchemaMaintenanceDeadHolderPrepared struct {
	Format              string                      `json:"format"`
	TransitionID        string                      `json:"transition_id"`
	PrePlanSHA256       string                      `json:"pre_plan_sha256"`
	Mode                string                      `json:"mode"`
	Host                string                      `json:"host"`
	OwnerMode           string                      `json:"owner_mode"`
	OriginalOwnerSHA256 string                      `json:"original_owner_sha256"`
	OriginalDaemon      schemaMaintenanceRPCBinding `json:"original_daemon"`
	RecoveryDaemon      schemaMaintenanceRPCBinding `json:"recovery_daemon"`
	OriginalDaemonFile  productionReleaseFilePlan   `json:"original_daemon_file"`
	GuardianAdopted     productionReleaseFilePlan   `json:"guardian_adopted"`
	GuardianBinding     productionReleaseFilePlan   `json:"guardian_binding"`
	OldCapsule          productionReleaseFilePlan   `json:"old_capsule"`
	StoppedCapture      productionReleaseFilePlan   `json:"stopped_capture"`
}
type productionSchemaMaintenanceDeadHolderBinding struct {
	Format              string                                          `json:"format"`
	TransitionID        string                                          `json:"transition_id"`
	PrePlanSHA256       string                                          `json:"pre_plan_sha256"`
	Mode                string                                          `json:"mode"`
	OwnerMode           string                                          `json:"owner_mode"`
	OriginalOwnerSHA256 string                                          `json:"original_owner_sha256"`
	Origins             []productionSchemaMaintenanceDeadHolderPrepared `json:"origins"`
}
type productionSchemaMaintenanceDeadHolderController struct {
	base            *productionSchemaMaintenanceController
	original        schemaMaintenanceRPCBinding
	generation      schemaMaintenanceRPCBinding
	ownerSHA        string
	ownerMode       string
	finished        bool
	finishUncertain bool
}

var schemaMaintenanceDeadHolderForbidden = []string{
	"schema-worker-intent.json", "schema-worker-output.log", "schema-worker-result.json", "schema-outcome.json",
	"recovery-binding.json", "recovery-startup-profile.json", "recovery-hook-intent.json", "recovery-hook-bound.json",
	"recovery-start-intent.json", "recovery-started.json", "owner-released.json", "host-finished.json",
	"abort-prepared.json", "abort-binding.json", "abort-start-intent.json", "abort-started.json", "abort-finish-intent.json", "abort-host-finished.json",
}

func schemaMaintenanceDeadHolderNoWorker(workspace string) error {
	for _, name := range schemaMaintenanceDeadHolderForbidden {
		if _, err := os.Lstat(filepath.Join(workspace, "state", name)); !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("dead pre-worker recovery rejects existing or uncertain %s", name)
		}
	}
	return nil
}
func schemaMaintenanceDeadHolderRPCWorkspace(origin productionSchemaMaintenanceOriginPlan, id string) string {
	return filepath.Join("/run/lmm-schema-dead-holder", id)
}
func (d *productionSchemaMaintenanceDeadHolderController) checkLocal(ctx context.Context) error {
	c := d.base
	if err := c.checkPlan(ctx); err != nil {
		return err
	}
	if err := schemaMaintenanceDeadHolderNoWorker(c.origin.Workspace); err != nil {
		return err
	}
	raw, err := c.runtime.readExistingSchemaSealedFile(filepath.Join(c.origin.Workspace, "state", "daemon-binding.json"), true)
	want, e := schemaMaintenanceRPCMarshal(d.original)
	if err != nil || e != nil || !bytes.Equal(raw, append(want, '\n')) {
		return errors.New("dead pre-worker recovery lost the exact original daemon binding")
	}
	unit, err := c.runtime.billingUnitState(ctx, schemaMaintenanceHolderUnit(c.plan.TransitionID))
	if err != nil {
		return errors.New("original schema daemon unit state is unknown")
	}
	if err := schemaMaintenanceDeadHolderProcessGone(d.original, unit); err != nil {
		return err
	}
	if err := schemaMaintenanceRPCProcess(d.generation); err != nil {
		return err
	}
	recoverer, err := c.runtime.billingUnitState(ctx, "lmm-schema-dead-holder-"+c.plan.TransitionID+".service")
	if err != nil || validateSchemaMaintenanceAbortDaemonUnit(d.generation, recoverer) != nil {
		return errors.New("dead holder recovery is not its actual dedicated native process")
	}
	return nil
}
func (d *productionSchemaMaintenanceDeadHolderController) checkLeasedLocal(ctx context.Context) error {
	if err := d.checkLocal(ctx); err != nil {
		return err
	}
	return d.base.host.lease.Check(ctx)
}
func (d *productionSchemaMaintenanceDeadHolderController) prepared(ctx context.Context) (productionSchemaMaintenanceDeadHolderPrepared, error) {
	c := d.base
	var result productionSchemaMaintenanceDeadHolderPrepared
	if d.finished {
		if err := d.checkLocal(ctx); err != nil {
			return result, err
		}
	} else if err := d.checkLeasedLocal(ctx); err != nil {
		return result, err
	}
	raw, err := c.runtime.readExistingSchemaSealedFile(filepath.Join(c.origin.Workspace, "state", "dead-holder-prepared.json"), true)
	if err != nil {
		return result, err
	}
	if err := decodeControllerBackupJSON(raw, &result); err != nil {
		return result, err
	}
	canonical, _ := json.MarshalIndent(result, "", "  ")
	if !bytes.Equal(raw, append(canonical, '\n')) || result.Mode != schemaMaintenanceDeadHolderMode || result.Format != "lmm-schema-maintenance-dead-holder-prepared-v1" || result.Host != c.origin.Host || result.TransitionID != c.plan.TransitionID || result.PrePlanSHA256 != c.planSHA || result.OriginalDaemon != d.original || result.RecoveryDaemon != d.generation || result.OwnerMode != d.ownerMode || result.OriginalOwnerSHA256 != d.ownerSHA || result.OldCapsule != c.origin.OldCapsule || result.StoppedCapture.Path != filepath.Join(c.origin.Workspace, "state", "writer-stopped.json") || result.OriginalDaemonFile.Path != filepath.Join(c.origin.Workspace, "state", "daemon-binding.json") || result.GuardianAdopted.Path != filepath.Join(c.origin.Workspace, "state", "guardian-adopted.json") || result.GuardianBinding.Path != c.host.binding.GuardianBindingPath || result.GuardianBinding.SHA256 != c.host.binding.GuardianBindingSHA256 {
		return result, errors.New("dead holder preparation differs from its exact fresh native authority")
	}
	for _, ref := range []productionReleaseFilePlan{result.OriginalDaemonFile, result.GuardianAdopted, result.GuardianBinding, result.StoppedCapture} {
		raw, err := c.runtime.readExistingSchemaSealedFile(ref.Path, true)
		if err != nil || startupContentSHA256(raw) != ref.SHA256 {
			return result, errors.New("dead holder original immutable evidence changed")
		}
	}
	return result, nil
}
func (d *productionSchemaMaintenanceDeadHolderController) prepare(ctx context.Context) (productionSchemaMaintenanceDeadHolderPrepared, error) {
	c := d.base
	var result productionSchemaMaintenanceDeadHolderPrepared
	if err := d.checkLeasedLocal(ctx); err != nil {
		return result, err
	}
	capture, err := c.host.captureStopped(ctx)
	if err != nil || capture.Phase != "WRITER_STOPPED" {
		return result, errors.New("dead holder recovery requires the exact old sealed stop")
	}
	result = productionSchemaMaintenanceDeadHolderPrepared{Format: "lmm-schema-maintenance-dead-holder-prepared-v1", TransitionID: c.plan.TransitionID, PrePlanSHA256: c.planSHA, Mode: schemaMaintenanceDeadHolderMode, Host: c.origin.Host, OriginalDaemon: d.original, RecoveryDaemon: d.generation, OwnerMode: d.ownerMode, OriginalOwnerSHA256: d.ownerSHA, OldCapsule: c.origin.OldCapsule}
	refs := []*productionReleaseFilePlan{&result.OriginalDaemonFile, &result.GuardianAdopted, &result.GuardianBinding, &result.StoppedCapture}
	paths := []string{filepath.Join(c.origin.Workspace, "state", "daemon-binding.json"), filepath.Join(c.origin.Workspace, "state", "guardian-adopted.json"), c.host.binding.GuardianBindingPath, filepath.Join(c.origin.Workspace, "state", "writer-stopped.json")}
	for i, path := range paths {
		raw, e := c.runtime.readExistingSchemaSealedFile(path, true)
		if e != nil {
			return result, e
		}
		*refs[i] = productionReleaseFilePlan{Path: path, SHA256: startupContentSHA256(raw)}
	}
	if err := d.checkLeasedLocal(ctx); err != nil {
		return result, err
	}
	if err := c.runtime.startupBaselineNewFile(filepath.Join(c.origin.Workspace, "state", "dead-holder-prepared.json"), result); err != nil {
		return result, errors.New("dead holder preparation was attempted; inspect without replacing its authority")
	}
	return d.prepared(ctx)
}
func (d *productionSchemaMaintenanceDeadHolderController) remote(ctx context.Context, origin productionSchemaMaintenanceOriginPlan, action string, result any) error {
	c := d.base
	engine := filepath.Join(filepath.Dir(origin.ArtifactCapsule.Path), "tmp", "migrations", "merchant-store-candidate", deployEngineName)
	budget, err := schemaMaintenanceRPCActionTimeout(action)
	if err != nil {
		return err
	}
	words := []string{engine, "production", "schema-maintenance", "dead-holder-rpc", "--plan", filepath.Join(origin.Workspace, "schema-maintenance-plan.json"), "--plan-sha256", c.planSHA, "--action", action}
	for i := range words {
		words[i] = schemaMaintenanceSSHWord(words[i])
	}
	output, err := c.runtime.runner.Run(ctx, productionCommand{Name: commandSSH, Args: []string{"-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "-o", "ControlMaster=no", "-o", "ControlPath=none", "-o", "ConnectTimeout=10", origin.Alias, strings.Join(words, " ")}, Timeout: budget + 10*time.Second, OutputLimit: 65536})
	if err != nil {
		return errors.New("dead holder fixed peer reply is unknown; inspect without retrying mutation")
	}
	var reply schemaMaintenanceRPCReply
	if schemaMaintenanceRPCDecode(bytes.TrimSuffix(output, []byte{'\n'}), &reply) != nil || !reply.OK || reply.Error != "" || reply.Protocol != schemaMaintenanceRPCProtocol || reply.Action != action || reply.Binding.PlanSHA256 != c.planSHA || reply.Binding.TransitionID != c.plan.TransitionID || reply.Binding.DaemonExeSHA256 != c.plan.Worker.Provider.DeployEngineSHA256 || validateSchemaMaintenanceRPCBinding(reply.Binding) != nil || !productionSHA256Pattern.MatchString(reply.Nonce) {
		return errors.New("dead holder peer native reply differs")
	}
	if err := schemaMaintenanceAbortTypedResult(reply, result); err != nil {
		return err
	}
	if prepared, ok := result.(*productionSchemaMaintenanceDeadHolderPrepared); ok && (prepared.RecoveryDaemon != reply.Binding || prepared.Host != origin.Host) {
		return errors.New("dead holder prepared peer is not its actual native recovery daemon")
	}
	if bound, ok := result.(*productionSchemaMaintenanceDeadHolderBinding); ok {
		found := false
		for _, p := range bound.Origins {
			if p.Host == origin.Host {
				found = p.RecoveryDaemon == reply.Binding
			}
		}
		if !found {
			return errors.New("dead holder binding publisher differs from its actual native generation")
		}
	}
	if recovered, ok := result.(*productionSchemaMaintenanceDeadPreWorkerCASResult); ok && (recovered.RecovererPID != reply.Binding.DaemonPID || recovered.RecovererInvocationID != reply.Binding.DaemonInvocationID || recovered.RecovererEXESHA256 != reply.Binding.DaemonExeSHA256) {
		return errors.New("dead holder CAS publisher differs from its actual native generation")
	}
	if _, err := os.Lstat(filepath.Join(c.origin.Workspace, "state", "dead-holder-binding.json")); err == nil {
		binding, _, err := d.binding(ctx)
		if err != nil {
			return err
		}
		for _, p := range binding.Origins {
			if p.Host == origin.Host && p.RecoveryDaemon != reply.Binding {
				return errors.New("dead holder peer generation changed after its immutable binding")
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
func (d *productionSchemaMaintenanceDeadHolderController) binding(ctx context.Context) (productionSchemaMaintenanceDeadHolderBinding, string, error) {
	c := d.base
	var binding productionSchemaMaintenanceDeadHolderBinding
	prepared, err := d.prepared(ctx)
	if err != nil {
		return binding, "", err
	}
	raw, err := c.runtime.readExistingSchemaSealedFile(filepath.Join(c.origin.Workspace, "state", "dead-holder-binding.json"), true)
	if err != nil {
		return binding, "", err
	}
	if err := decodeControllerBackupJSON(raw, &binding); err != nil {
		return binding, "", err
	}
	canonical, _ := json.MarshalIndent(binding, "", "  ")
	if !bytes.Equal(raw, append(canonical, '\n')) || binding.Format != "lmm-schema-maintenance-dead-holder-binding-v1" || binding.Mode != schemaMaintenanceDeadHolderMode || binding.TransitionID != c.plan.TransitionID || binding.PrePlanSHA256 != c.planSHA || binding.OwnerMode != d.ownerMode || binding.OriginalOwnerSHA256 != d.ownerSHA || len(binding.Origins) != len(c.plan.Origins) {
		return binding, "", errors.New("dead holder binding differs from the immutable pre-worker recovery authority")
	}
	for i, p := range binding.Origins {
		if p.OwnerMode != d.ownerMode || p.OriginalOwnerSHA256 != d.ownerSHA {
			return binding, "", errors.New("dead holder origins disagree on the exact original owner mode")
		}
		if err := validateSchemaMaintenanceDeadHolderPrepared(p, c.plan, c.planSHA, c.plan.Origins[i]); err != nil {
			return binding, "", err
		}
		if p.Host == c.origin.Host {
			a, _ := json.Marshal(p)
			b, _ := json.Marshal(prepared)
			if !bytes.Equal(a, b) {
				return binding, "", errors.New("dead holder local preparation changed")
			}
		}
	}
	return binding, startupContentSHA256(raw), nil
}
func (d *productionSchemaMaintenanceDeadHolderController) createBinding(ctx context.Context) (productionSchemaMaintenanceDeadHolderBinding, error) {
	c := d.base
	binding := productionSchemaMaintenanceDeadHolderBinding{Format: "lmm-schema-maintenance-dead-holder-binding-v1", TransitionID: c.plan.TransitionID, PrePlanSHA256: c.planSHA, Mode: schemaMaintenanceDeadHolderMode, OwnerMode: d.ownerMode, OriginalOwnerSHA256: d.ownerSHA}
	if c.origin.Host != c.plan.PrimaryHost {
		return binding, errors.New("dead holder binding must originate at the fixed primary")
	}
	for _, origin := range c.plan.Origins {
		var p productionSchemaMaintenanceDeadHolderPrepared
		var err error
		if origin.Host == c.origin.Host {
			p, err = d.prepared(ctx)
		} else {
			err = d.remote(ctx, origin, "dead-prepared", &p)
		}
		if err != nil {
			return binding, err
		}
		if p.Host != origin.Host || p.Mode != schemaMaintenanceDeadHolderMode || p.OwnerMode != d.ownerMode || p.OriginalOwnerSHA256 != d.ownerSHA {
			return binding, errors.New("dead holder peer is not prepared under the original locks")
		}
		binding.Origins = append(binding.Origins, p)
	}
	if err := d.checkStoppedOrigins(ctx); err != nil {
		return binding, err
	}
	if err := c.runtime.startupBaselineNewFile(filepath.Join(c.origin.Workspace, "state", "dead-holder-binding.json"), binding); err != nil {
		return binding, err
	}
	actual, _, err := d.binding(ctx)
	return actual, err
}
func (d *productionSchemaMaintenanceDeadHolderController) checkStoppedOrigins(ctx context.Context) error {
	c := d.base
	for _, origin := range c.plan.Origins {
		var capture productionSchemaMaintenanceHostCapture
		var err error
		if origin.Host == c.origin.Host {
			capture, err = c.host.captureStopped(ctx)
		} else {
			err = d.remote(ctx, origin, "dead-stopped", &capture)
		}
		if err != nil {
			return err
		}
		if err := c.validateObservedCapture(capture, origin, "capture-stopped"); err != nil {
			return err
		}
	}
	return d.checkLeasedLocal(ctx)
}
func (d *productionSchemaMaintenanceDeadHolderController) installBinding(ctx context.Context) (productionSchemaMaintenanceDeadHolderBinding, error) {
	var binding productionSchemaMaintenanceDeadHolderBinding
	c := d.base
	if c.origin.Host == c.plan.PrimaryHost {
		binding, _, err := d.binding(ctx)
		return binding, err
	}
	if err := d.checkLeasedLocal(ctx); err != nil {
		return binding, err
	}
	if err := d.remote(ctx, c.plan.Origins[0], "dead-binding", &binding); err != nil {
		return binding, err
	}
	if err := c.runtime.startupBaselineNewFile(filepath.Join(c.origin.Workspace, "state", "dead-holder-binding.json"), binding); err != nil {
		return binding, err
	}
	actual, _, err := d.binding(ctx)
	return actual, err
}

// This checks actual native unit identity without treating a generic /proc
// permission or parsing error as death. PID reuse never authorizes killing it.
func schemaMaintenanceDeadHolderUnitGone(generation schemaMaintenanceRPCBinding, unit map[string]string) error {
	if validateSchemaMaintenanceRPCBinding(generation) != nil || unit["Restart"] != "no" || unit["MainPID"] != "0" || unit["InvocationID"] != generation.DaemonInvocationID || (unit["ActiveState"] != "inactive" && unit["ActiveState"] != "failed") || (unit["SubState"] != "dead" && unit["SubState"] != "failed") {
		return errors.New("original schema daemon has no exact stopped native unit generation")
	}
	if pid, err := strconv.Atoi(unit["ExecMainPID"]); err != nil || pid != generation.DaemonPID {
		return errors.New("original schema daemon unit retained another process generation")
	}
	return nil
}

func validateSchemaMaintenanceDeadHolderPrepared(p productionSchemaMaintenanceDeadHolderPrepared, plan productionSchemaMaintenancePlan, sha string, origin productionSchemaMaintenanceOriginPlan) error {
	if p.Format != "lmm-schema-maintenance-dead-holder-prepared-v1" || p.Mode != schemaMaintenanceDeadHolderMode || p.Host != origin.Host || p.TransitionID != plan.TransitionID || p.PrePlanSHA256 != sha || p.OldCapsule != origin.OldCapsule || validateSchemaMaintenanceRPCBinding(p.OriginalDaemon) != nil || validateSchemaMaintenanceRPCBinding(p.RecoveryDaemon) != nil || p.OriginalDaemon == p.RecoveryDaemon || (p.OwnerMode != schemaMaintenanceDeadPreWorkerExactOwner && p.OwnerMode != schemaMaintenanceDeadPreWorkerZeroOwner) || (p.OwnerMode == schemaMaintenanceDeadPreWorkerExactOwner && !productionSHA256Pattern.MatchString(p.OriginalOwnerSHA256)) || (p.OwnerMode == schemaMaintenanceDeadPreWorkerZeroOwner && p.OriginalOwnerSHA256 != "") {
		return errors.New("dead holder preparation changed a fixed origin or original/fresh generation")
	}
	for _, generation := range []schemaMaintenanceRPCBinding{p.OriginalDaemon, p.RecoveryDaemon} {
		if generation.PlanSHA256 != sha || generation.TransitionID != plan.TransitionID || generation.DaemonExeSHA256 != plan.Worker.Provider.DeployEngineSHA256 {
			return errors.New("dead holder preparation changed a native release generation")
		}
	}
	refs := []productionReleaseFilePlan{p.OriginalDaemonFile, p.GuardianAdopted, p.GuardianBinding, p.StoppedCapture}
	paths := []string{filepath.Join(origin.Workspace, "state", "daemon-binding.json"), filepath.Join(origin.Workspace, "state", "guardian-adopted.json"), origin.Guardian.BindingPath, filepath.Join(origin.Workspace, "state", "writer-stopped.json")}
	for i, ref := range refs {
		if validateSchemaMaintenanceReference(ref) != nil || ref.Path != paths[i] {
			return errors.New("dead holder preparation changed a fixed original evidence reference")
		}
	}
	return nil
}
