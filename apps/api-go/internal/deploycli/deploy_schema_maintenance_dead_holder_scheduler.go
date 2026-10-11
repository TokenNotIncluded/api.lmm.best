package deploycli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func schemaMaintenanceDeadHolderControllerNoWorker(root string) error {
	for _, name := range []string{"dispatch-worker.json", "dispatch-worker.complete.json", "dispatch-capture-outcome.json", "dispatch-capture-outcome.complete.json", "dispatch-install-recovery-hook.json", "dispatch-install-recovery-hook.complete.json", "dispatch-start-recovery.json", "dispatch-start-recovery.complete.json", "dispatch-create-abort-binding.json", "dispatch-start-abort-old.json", "dispatch-finish-abort.json"} {
		if _, err := os.Lstat(filepath.Join(root, "state", name)); !errors.Is(err, os.ErrNotExist) {
			return errors.New("dead holder recovery found controller worker/recovery dispatch or uncertain absence")
		}
	}
	return nil
}
func schemaMaintenanceDeadHolderObservation(action string) bool {
	switch action {
	case "dead-prepared", "dead-stopped", "dead-binding", "dead-recovered", "dead-diagnose", "dead-reconcile-owner", "dead-startable", "dead-ready", "dead-status":
		return true
	}
	return false
}
func (runtime *productionReleaseRuntime) deadHolderRemote(ctx context.Context, plan productionSchemaMaintenancePlan, sha string, origin productionSchemaMaintenanceOriginPlan, action string) (schemaMaintenanceRPCReply, error) {
	var reply schemaMaintenanceRPCReply
	if !schemaMaintenanceDeadHolderAction(action) {
		return reply, errors.New("dead holder scheduler action is not fixed")
	}
	budget, err := schemaMaintenanceRPCActionTimeout(action)
	if err != nil {
		return reply, err
	}
	engine := filepath.Join(filepath.Dir(origin.ArtifactCapsule.Path), "tmp", "migrations", "merchant-store-candidate", deployEngineName)
	words := []string{engine, "production", "schema-maintenance", "dead-holder-rpc", "--plan", filepath.Join(origin.Workspace, "schema-maintenance-plan.json"), "--plan-sha256", sha, "--action", action}
	for i := range words {
		words[i] = schemaMaintenanceSSHWord(words[i])
	}
	out, err := runtime.runner.Run(ctx, productionCommand{Name: commandSSH, Args: []string{"-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "-o", "ControlMaster=no", "-o", "ControlPath=none", "-o", "ConnectTimeout=10", origin.Alias, strings.Join(words, " ")}, Timeout: budget + 10*time.Second, OutputLimit: 65536})
	if err != nil {
		return reply, errors.New("dead holder dispatch outcome is unknown; inspect the dedicated original recovery daemon without retry")
	}
	if schemaMaintenanceRPCDecode(bytes.TrimSuffix(out, []byte{'\n'}), &reply) != nil || !reply.OK || reply.Error != "" || reply.Protocol != schemaMaintenanceRPCProtocol || reply.Action != action || reply.Binding.PlanSHA256 != sha || reply.Binding.TransitionID != plan.TransitionID || reply.Binding.DaemonExeSHA256 != plan.Worker.Provider.DeployEngineSHA256 || validateSchemaMaintenanceRPCBinding(reply.Binding) != nil || !productionSHA256Pattern.MatchString(reply.Nonce) {
		return reply, errors.New("dead holder fixed native reply differs")
	}
	return reply, nil
}
func runProductionSchemaMaintenanceDeadHolderPhase(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet(DeployProgramName+" production schema-maintenance dead-holder-phase", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("plan", "", "private original controller plan")
	sha := flags.String("plan-sha256", "", "original plan SHA256")
	action := flags.String("action", "", "fixed dedicated recovery phase")
	confirm := flags.String("confirm", "", "production authorization")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return ExitOK
		}
		return ExitUsage
	}
	if flags.NArg() != 0 || *path == "" || *sha == "" || *confirm != "api.lmm.best" || !schemaMaintenanceDeadHolderAction(*action) {
		return ExitUsage
	}
	fail := func(err error) int { fmt.Fprintf(stderr, "dead-holder-phase: %v\n", err); return ExitError }
	plan, err := loadSchemaMaintenanceControllerPlan(*path, *sha)
	if err != nil {
		return fail(err)
	}
	if sha256MustEqual("/proc/self/exe", plan.Worker.Provider.DeployEngineSHA256) != nil {
		return fail(errors.New("dead holder scheduler is not the exact official signed engine"))
	}
	lock, err := acquireSchemaMaintenanceSchedulerLock(plan.BackupPolicy.ControllerDirectory)
	if err != nil {
		return fail(err)
	}
	defer lock.Close()
	if err := schemaMaintenanceDeadHolderControllerNoWorker(plan.BackupPolicy.ControllerDirectory); err != nil {
		return fail(err)
	}
	runtime := &productionReleaseRuntime{runner: osProductionCommandRunner{}, now: time.Now}
	results, err := runtime.dispatchDeadHolderPhase(context.Background(), plan, *sha, *action)
	if err != nil {
		return fail(err)
	}
	return writeJSONCommandResult(results, stdout, stderr, "dedicated dead holder phase")
}
func (runtime *productionReleaseRuntime) dispatchDeadHolderPhase(ctx context.Context, plan productionSchemaMaintenancePlan, sha, action string) ([]schemaMaintenanceRPCReply, error) {
	var results []schemaMaintenanceRPCReply
	if !schemaMaintenanceDeadHolderAction(action) {
		return nil, errors.New("dead holder phase is not permitted")
	}
	if err := schemaMaintenanceDeadHolderControllerNoWorker(plan.BackupPolicy.ControllerDirectory); err != nil {
		return nil, err
	}
	state := filepath.Join(plan.BackupPolicy.ControllerDirectory, "state")
	mutating := !schemaMaintenanceDeadHolderObservation(action)
	if mutating {
		entries, err := os.ReadDir(state)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), "dead-dispatch-") && strings.HasSuffix(entry.Name(), ".json") && !strings.HasSuffix(entry.Name(), ".complete.json") {
				previous := strings.TrimSuffix(strings.TrimPrefix(entry.Name(), "dead-dispatch-"), ".json")
				if err := schemaMaintenanceDeadHolderCompleted(plan, sha, previous); err != nil {
					return nil, err
				}
			}
		}
		intent := map[string]string{"format": "lmm-schema-maintenance-dead-dispatch-v1", "transition_id": plan.TransitionID, "pre_plan_sha256": sha, "action": action}
		raw, _ := json.MarshalIndent(intent, "", "  ")
		if err := schemaMaintenanceControllerNewFile(filepath.Join(state, "dead-dispatch-"+action+".json"), append(raw, '\n')); err != nil {
			return nil, err
		}
	}
	origins := plan.Origins
	if action == "dead-create-binding" || action == "dead-recover-owner" || action == "dead-diagnose" || action == "dead-reconcile-owner" {
		origins = plan.Origins[:1]
	}
	for _, origin := range origins {
		reply, err := runtime.deadHolderRemote(ctx, plan, sha, origin, action)
		if err != nil {
			return results, err
		}
		results = append(results, reply)
		if mutating {
			raw, err := schemaMaintenanceRPCMarshal(reply)
			if err != nil {
				return results, err
			}
			if err := schemaMaintenanceControllerNewFile(filepath.Join(state, "dead-dispatch-"+action+"-"+origin.Host+".reply"), append(raw, '\n')); err != nil {
				return results, err
			}
		}
	}
	if mutating {
		raw, _ := json.MarshalIndent(results, "", "  ")
		if err := schemaMaintenanceControllerNewFile(filepath.Join(state, "dead-dispatch-"+action+".complete.json"), append(raw, '\n')); err != nil {
			return results, err
		}
	}
	return results, nil
}

func schemaMaintenanceDeadHolderCompleted(plan productionSchemaMaintenancePlan, sha, action string) error {
	if !schemaMaintenanceDeadHolderAction(action) || schemaMaintenanceDeadHolderObservation(action) {
		return errors.New("dead holder prior dispatch action is unknown")
	}
	state := filepath.Join(plan.BackupPolicy.ControllerDirectory, "state")
	intent, err := controllerBackupReadPrivate(filepath.Join(state, "dead-dispatch-"+action+".json"), os.Geteuid(), 65536)
	if err != nil {
		return err
	}
	expected := map[string]string{"format": "lmm-schema-maintenance-dead-dispatch-v1", "transition_id": plan.TransitionID, "pre_plan_sha256": sha, "action": action}
	expectedRaw, _ := json.MarshalIndent(expected, "", "  ")
	if !bytes.Equal(intent, append(expectedRaw, '\n')) {
		return errors.New("dead holder prior intent differs from the exact original plan")
	}
	raw, err := controllerBackupReadPrivate(filepath.Join(state, "dead-dispatch-"+action+".complete.json"), os.Geteuid(), 65536)
	if err != nil {
		return errors.New("a dedicated dead holder dispatch is uncertain; only observations are allowed")
	}
	var replies []schemaMaintenanceRPCReply
	if decodeControllerBackupJSON(raw, &replies) != nil {
		return errors.New("dead holder prior completion is not typed native evidence")
	}
	canonical, _ := json.MarshalIndent(replies, "", "  ")
	origins := plan.Origins
	if action == "dead-create-binding" || action == "dead-recover-owner" || action == "dead-diagnose" || action == "dead-reconcile-owner" {
		origins = origins[:1]
	}
	if !bytes.Equal(raw, append(canonical, '\n')) || len(replies) != len(origins) {
		return errors.New("dead holder prior completion omitted or changed an origin")
	}
	for i, reply := range replies {
		if !reply.OK || reply.Error != "" || reply.Action != action || reply.Protocol != schemaMaintenanceRPCProtocol || reply.Binding.PlanSHA256 != sha || reply.Binding.TransitionID != plan.TransitionID || reply.Binding.DaemonExeSHA256 != plan.Worker.Provider.DeployEngineSHA256 || validateSchemaMaintenanceRPCBinding(reply.Binding) != nil || !productionSHA256Pattern.MatchString(reply.Nonce) {
			return errors.New("dead holder prior completion lost its exact successful native authority")
		}
		actual, err := controllerBackupReadPrivate(filepath.Join(state, "dead-dispatch-"+action+"-"+origins[i].Host+".reply"), os.Geteuid(), 65536)
		want, e := schemaMaintenanceRPCMarshal(reply)
		if err != nil || e != nil || !bytes.Equal(actual, append(want, '\n')) {
			return errors.New("dead holder prior completion changed its exact fixed-origin reply")
		}
	}
	return nil
}
