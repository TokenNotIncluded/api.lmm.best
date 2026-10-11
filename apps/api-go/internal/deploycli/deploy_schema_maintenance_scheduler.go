package deploycli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The controller has its own identical, private copy. Its path is deliberately
// distinct from the root-owned target copy; no ordinary release-plan loader is
// tricked into treating either copy as a different plan's provenance.
func loadSchemaMaintenanceControllerPlan(path, digest string) (productionSchemaMaintenancePlan, error) {
	var plan productionSchemaMaintenancePlan
	uid := os.Geteuid()
	if uid == 0 || !productionSHA256Pattern.MatchString(digest) {
		return plan, errors.New("schema scheduler requires the unprivileged controller and exact plan digest")
	}
	raw, err := controllerBackupReadPrivate(path, uid, 1<<20)
	if err != nil || controllerBackupDigest(raw) != digest {
		return plan, errors.New("schema scheduler canonical plan is unavailable or changed")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&plan) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return plan, errors.New("schema scheduler plan has unknown or trailing fields")
	}
	canonical, err := canonicalProductionSchemaMaintenancePlan(plan, defaultProductionPaths())
	if err != nil || !bytes.Equal(raw, canonical) || path != filepath.Join(plan.BackupPolicy.ControllerDirectory, "schema-maintenance-plan.json") || controllerBackupDirectory(plan.BackupPolicy.ControllerDirectory, uid) != nil {
		return plan, errors.New("schema scheduler plan is not the exact private controller copy")
	}
	return plan, nil
}

func schemaMaintenanceControllerNewFile(path string, raw []byte) error {
	if len(raw) == 0 || controllerBackupDirectory(filepath.Dir(path), os.Geteuid()) != nil {
		return errors.New("schema scheduler immutable receipt directory is unsafe")
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return errors.New("schema scheduler receipt already exists; reconcile the original attempt without repeating it")
	}
	_, writeErr := file.Write(raw)
	return errors.Join(writeErr, file.Sync(), file.Close(), syncDirectory(filepath.Dir(path)))
}

func acquireSchemaMaintenanceSchedulerLock(root string) (*os.File, error) {
	uid := os.Geteuid()
	state := filepath.Join(root, "state")
	if uid == 0 || controllerBackupDirectory(root, uid) != nil || controllerBackupDirectory(state, uid) != nil {
		return nil, errors.New("schema scheduler requires its private controller state")
	}
	path := filepath.Join(state, "schema-scheduler.lock")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, os.ErrExist) {
		// The persistent lock file is never unlinked: two dispatchers must not
		// obtain different inodes by replacing one another's lock.
		before, statErr := os.Lstat(path)
		if statErr != nil || !before.Mode().IsRegular() || before.Mode().Perm() != 0600 {
			return nil, errors.New("schema scheduler lock has unsafe existing metadata")
		}
		owner, links, ok := deploymentFileOwnership(before)
		if !ok || int(owner) != uid || links != 1 {
			return nil, errors.New("schema scheduler lock has another owner or link")
		}
		file, err = os.OpenFile(path, os.O_WRONLY, 0)
		if err == nil {
			after, statErr := file.Stat()
			if statErr != nil || !os.SameFile(before, after) {
				file.Close()
				return nil, errors.New("schema scheduler lock changed while opening")
			}
		}
	}
	if err != nil {
		return nil, err
	}
	locked, err := tryDeploymentFileLock(file)
	if err != nil || !locked {
		file.Close()
		return nil, errors.New("another schema dispatcher owns this complete two-origin phase")
	}
	return file, nil
}

func (runtime *productionReleaseRuntime) schemaMaintenanceRemoteRPC(ctx context.Context, plan productionSchemaMaintenancePlan, planSHA string, origin productionSchemaMaintenanceOriginPlan, action string) (schemaMaintenanceRPCReply, error) {
	var reply schemaMaintenanceRPCReply
	budget, err := schemaMaintenanceRPCActionTimeout(action)
	if err != nil {
		return reply, err
	}
	engine := filepath.Join(filepath.Dir(origin.ArtifactCapsule.Path), "tmp", "migrations", "merchant-store-candidate", deployEngineName)
	remotePlan := filepath.Join(origin.Workspace, "schema-maintenance-plan.json")
	words := []string{engine, "production", "schema-maintenance", "rpc", "--plan", remotePlan, "--plan-sha256", planSHA, "--action", action}
	for index := range words {
		words[index] = schemaMaintenanceSSHWord(words[index])
	}
	limit := 65536
	if action == "backup-export" {
		limit = (16 << 20) + 4096
	} else if action == "capture-backup-source" {
		limit = (8 << 20) + 4096
	}
	out, callErr := runtime.runner.Run(ctx, productionCommand{Name: commandSSH, Args: []string{"-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "-o", "ControlMaster=no", "-o", "ControlPath=none", "-o", "ConnectTimeout=10", origin.Alias, strings.Join(words, " ")}, Timeout: budget + 10*time.Second, OutputLimit: limit})
	if callErr != nil || schemaMaintenanceRPCDecode(bytes.TrimSuffix(out, []byte{'\n'}), &reply) != nil || reply.Protocol != schemaMaintenanceRPCProtocol || validateSchemaMaintenanceRPCBinding(reply.Binding) != nil || reply.Action != action || reply.Binding.PlanSHA256 != planSHA || reply.Binding.TransitionID != plan.TransitionID || reply.Binding.DaemonExeSHA256 != plan.Worker.Provider.DeployEngineSHA256 || !productionSHA256Pattern.MatchString(reply.Nonce) || !reply.OK || reply.Error != "" {
		return reply, errors.New("schema native RPC did not return one exact successful result; retain daemons and reconcile without repeating mutation")
	}
	return reply, nil
}

// One local lock spans the complete two-origin phase. Sending two mutating RPCs
// concurrently can deadlock the two serial holders while they observe each
// other. Every attempt is durable before dispatch; a lost reply cannot retry it.
func (runtime *productionReleaseRuntime) dispatchSchemaMaintenancePhase(ctx context.Context, plan productionSchemaMaintenancePlan, planSHA, action string) ([]schemaMaintenanceRPCReply, error) {
	origins, err := schemaMaintenanceDispatchOrigins(plan, action)
	if err != nil {
		return nil, err
	}
	lock, err := acquireSchemaMaintenanceSchedulerLock(plan.BackupPolicy.ControllerDirectory)
	if err != nil {
		return nil, err
	}
	defer lock.Close() // Only the controller's private scheduling lock, never a host lock.
	state := filepath.Join(plan.BackupPolicy.ControllerDirectory, "state")
	if err := schemaMaintenanceCheckDispatchLane(plan, action); err != nil {
		return nil, err
	}
	if err := schemaMaintenanceCheckPreviousDispatches(plan, planSHA); err != nil {
		return nil, err
	}
	intent, _ := json.Marshal(struct {
		Format, TransitionID, PlanSHA256, Action string
		StartedUTC                               time.Time
	}{"lmm-schema-maintenance-dispatch-v1", plan.TransitionID, planSHA, action, runtime.now().UTC()})
	if err := schemaMaintenanceControllerNewFile(filepath.Join(state, "dispatch-"+action+".json"), append(intent, '\n')); err != nil {
		return nil, err
	}
	var results []schemaMaintenanceRPCReply
	for _, origin := range origins {
		reply, err := runtime.schemaMaintenanceRemoteRPC(ctx, plan, planSHA, origin, action)
		if err != nil {
			return results, err
		}
		raw, err := schemaMaintenanceRPCMarshal(reply)
		if err != nil {
			return results, err
		}
		if err := schemaMaintenanceControllerNewFile(filepath.Join(state, "dispatch-"+action+"-"+origin.Host+".json"), append(raw, '\n')); err != nil {
			return results, err
		}
		results = append(results, reply)
	}
	complete := productionSchemaMaintenanceDispatchComplete{Format: "lmm-schema-maintenance-dispatch-complete-v1", TransitionID: plan.TransitionID, PlanSHA256: planSHA, Action: action}
	for index, reply := range results {
		raw, _ := schemaMaintenanceRPCMarshal(reply)
		complete.Replies = append(complete.Replies, productionReleaseFilePlan{Path: filepath.Join(state, "dispatch-"+action+"-"+origins[index].Host+".json"), SHA256: startupContentSHA256(append(raw, '\n'))})
	}
	completeRaw, _ := json.Marshal(complete)
	if err := schemaMaintenanceControllerNewFile(filepath.Join(state, "dispatch-"+action+".complete.json"), append(completeRaw, '\n')); err != nil {
		return results, err
	}
	return results, nil
}

type productionSchemaMaintenanceDispatchComplete struct {
	Format       string                      `json:"format"`
	TransitionID string                      `json:"transition_id"`
	PlanSHA256   string                      `json:"plan_sha256"`
	Action       string                      `json:"action"`
	Replies      []productionReleaseFilePlan `json:"replies"`
}

func schemaMaintenanceNormalDispatchActions() []string {
	return []string{"close", "stop", "claim", "worker", "capture-outcome", "install-recovery-hook", "release", "start-recovery", "reopen", "finish"}
}

func schemaMaintenanceAbortDispatchActions() []string {
	return []string{"prepare-abort", "create-abort-binding", "install-abort-binding", "start-abort-old", "reopen-abort", "finish-abort"}
}

func schemaMaintenanceDispatchOrigins(plan productionSchemaMaintenancePlan, action string) ([]productionSchemaMaintenanceOriginPlan, error) {
	switch action {
	case "close", "stop", "install-recovery-hook", "start-recovery", "reopen", "finish", "prepare-abort", "install-abort-binding", "start-abort-old", "reopen-abort", "finish-abort":
		return plan.Origins, nil
	case "claim", "worker", "release", "capture-outcome", "create-abort-binding":
		return plan.Origins[:1], nil
	default:
		return nil, errors.New("schema dispatcher accepts only fixed mutation phases")
	}
}

func schemaMaintenanceCheckDispatchLane(plan productionSchemaMaintenancePlan, action string) error {
	state := filepath.Join(plan.BackupPolicy.ControllerDirectory, "state")
	abortActions := schemaMaintenanceAbortDispatchActions()
	abortIndex := -1
	for index, candidate := range abortActions {
		if candidate == action {
			abortIndex = index
		}
	}
	for index, candidate := range abortActions {
		_, err := os.Lstat(filepath.Join(state, "dispatch-"+candidate+".json"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if abortIndex < 0 {
			return errors.New("schema abort was attempted; normal mutation phases remain frozen")
		}
		if index >= abortIndex {
			return errors.New("schema abort phase was already attempted or a later abort phase exists; observe without resending mutation")
		}
	}
	if abortIndex < 0 {
		return nil
	}
	// This is only local sequencing. The native controller must independently
	// prove NEVER_CLAIMED; neither local receipts nor this lane authorize DDL.
	for _, candidate := range schemaMaintenanceNormalDispatchActions()[2:] {
		if _, err := os.Lstat(filepath.Join(state, "dispatch-"+candidate+".json")); !errors.Is(err, os.ErrNotExist) {
			return errors.New("schema abort cannot follow an attempted claim or later normal phase")
		}
	}
	prerequisites := append([]string{"close", "stop"}, abortActions[:abortIndex]...)
	for _, candidate := range prerequisites {
		if _, err := os.Lstat(filepath.Join(state, "dispatch-"+candidate+".json")); err != nil {
			return errors.New("schema abort requires completed close, stop and each earlier abort phase")
		}
	}
	// The caller now validates every prerequisite's canonical completion and
	// exact ordered successful native receipts under this same scheduler lock.
	return nil
}

func schemaMaintenanceCheckPreviousDispatches(plan productionSchemaMaintenancePlan, sha string) error {
	state := filepath.Join(plan.BackupPolicy.ControllerDirectory, "state")
	for _, action := range append(schemaMaintenanceNormalDispatchActions(), schemaMaintenanceAbortDispatchActions()...) {
		intentPath := filepath.Join(state, "dispatch-"+action+".json")
		if _, err := os.Lstat(intentPath); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return err
		}
		raw, err := controllerBackupReadPrivate(filepath.Join(state, "dispatch-"+action+".complete.json"), os.Geteuid(), 65536)
		if err != nil {
			return errors.New("an earlier schema dispatch is incomplete or uncertain; only observe and reconcile the original batch")
		}
		var complete productionSchemaMaintenanceDispatchComplete
		if decodeControllerBackupJSON(raw, &complete) != nil || complete.Format != "lmm-schema-maintenance-dispatch-complete-v1" || complete.TransitionID != plan.TransitionID || complete.PlanSHA256 != sha || complete.Action != action {
			return errors.New("schema prior dispatch completion differs from the original plan")
		}
		canonical, _ := json.Marshal(complete)
		if !bytes.Equal(append(canonical, '\n'), raw) {
			return errors.New("schema prior dispatch completion is ambiguous")
		}
		origins, err := schemaMaintenanceDispatchOrigins(plan, action)
		if err != nil {
			return err
		}
		if len(complete.Replies) != len(origins) {
			return errors.New("schema prior dispatch did not complete every required origin")
		}
		for index, ref := range complete.Replies {
			if ref.Path != filepath.Join(state, "dispatch-"+action+"-"+origins[index].Host+".json") || !productionSHA256Pattern.MatchString(ref.SHA256) {
				return errors.New("schema prior dispatch receipt changed its exact origin")
			}
			raw, err := controllerBackupReadPrivate(ref.Path, os.Geteuid(), 65536)
			var reply schemaMaintenanceRPCReply
			if err != nil || startupContentSHA256(raw) != ref.SHA256 || schemaMaintenanceRPCDecode(bytes.TrimSuffix(raw, []byte{'\n'}), &reply) != nil || !reply.OK || reply.Error != "" || reply.Protocol != schemaMaintenanceRPCProtocol || !productionSHA256Pattern.MatchString(reply.Nonce) || reply.Action != action || reply.Binding.PlanSHA256 != sha || reply.Binding.TransitionID != plan.TransitionID || reply.Binding.DaemonExeSHA256 != plan.Worker.Provider.DeployEngineSHA256 || validateSchemaMaintenanceRPCBinding(reply.Binding) != nil {
				return errors.New("schema prior dispatch lost its exact successful native receipt")
			}
		}
	}
	return nil
}
