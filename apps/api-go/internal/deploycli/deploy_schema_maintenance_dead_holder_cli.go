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
)

type productionSchemaMaintenanceDeadHolderCLIState struct {
	Format              string                      `json:"format"`
	Mode                string                      `json:"mode"`
	OriginalOwnerSHA256 string                      `json:"original_owner_sha256"`
	Generation          schemaMaintenanceRPCBinding `json:"generation"`
	PreExecRecovery     *productionReleaseFilePlan  `json:"preexec_recovery,omitempty"`
	RecoveryMode        string                      `json:"recovery_mode,omitempty"`
}

func runProductionSchemaMaintenanceDeadHolder(action string, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet(DeployProgramName+" production schema-maintenance "+action, flag.ContinueOnError)
	flags.SetOutput(stderr)
	planPath := flags.String("plan", "", "exact original canonical target plan")
	planSHA := flags.String("plan-sha256", "", "exact original plan SHA256")
	projectionPath := flags.String("host-binding", "", "original host projection path")
	projectionSHA := flags.String("host-binding-sha256", "", "original host projection SHA256")
	mode := flags.String("owner-mode", "", "explicit EXACT_ORIGINAL_OWNER or ZERO_OWNER")
	ownerSHA := flags.String("original-owner-sha256", "", "reviewed canonical original owner SHA256, exact mode only")
	preExecPath := flags.String("preexec-recovery-plan", "", "exact canonical pre-execution rejection recovery plan")
	preExecSHA := flags.String("preexec-recovery-plan-sha256", "", "exact canonical recovery authority SHA256")
	rpcAction := flags.String("action", "", "fixed dedicated recovery action")
	confirm := flags.String("confirm", "", "production authorization")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return ExitOK
		}
		return ExitUsage
	}
	if flags.NArg() != 0 || *planPath == "" || *planSHA == "" {
		return ExitUsage
	}
	preExecEndpoint := action == "preexec-holder-serve" || action == "preexec-holder-rpc"
	if (!preExecEndpoint && (*preExecPath != "" || *preExecSHA != "")) || (preExecEndpoint && (*preExecPath == "" || !productionSHA256Pattern.MatchString(*preExecSHA))) {
		return ExitUsage
	}
	fail := func(err error) int { fmt.Fprintf(stderr, "schema-maintenance %s: %v\n", action, err); return ExitError }
	runtime := defaultProductionRuntime()
	ctx := context.Background()
	if runtime.effectiveUID() != 0 {
		return fail(errors.New("dedicated dead schema holder endpoint requires root"))
	}
	plan, err := runtime.loadSchemaMaintenancePlan(*planPath, *planSHA)
	if err != nil {
		return fail(err)
	}
	name, err := runtime.hostname()
	if err != nil {
		return fail(err)
	}
	var origin productionSchemaMaintenanceOriginPlan
	for _, candidate := range plan.Origins {
		if candidate.Host == name {
			origin = candidate
		}
	}
	if origin.Host == "" {
		return fail(errors.New("dead holder endpoint is not a fixed original host"))
	}
	var preExec *productionReleaseFilePlan
	engineSHA := plan.Worker.Provider.DeployEngineSHA256
	recoveryMode := ""
	if preExecEndpoint {
		if *preExecPath != filepath.Join(origin.Workspace, "state", "preexec-recovery-plan.json") {
			return fail(errors.New("pre-execution recovery is not the fixed original-workspace authority"))
		}
		preExec = &productionReleaseFilePlan{Path: *preExecPath, SHA256: *preExecSHA}
		capsule, err := runtime.checkSchemaMaintenancePreExecRecovery(ctx, plan, *planSHA, *preExec)
		if err != nil {
			return fail(err)
		}
		engineSHA = capsule.Candidate.DeployEngineSHA256
		recoveryMode = schemaMaintenancePreExecRejectedMode
	}
	if sha256MustEqual("/proc/self/exe", engineSHA) != nil {
		return fail(errors.New("dead holder endpoint is not the exact verified signed recovery engine"))
	}
	workspace := schemaMaintenanceDeadHolderRPCWorkspace(origin, plan.TransitionID)
	statePath := filepath.Join(origin.Workspace, "state", "dead-holder-authority.json")
	if action == "dead-holder-rpc" || action == "preexec-holder-rpc" {
		if *rpcAction == "" || *projectionPath != "" || *projectionSHA != "" || *mode != "" || *ownerSHA != "" || *confirm != "" {
			return ExitUsage
		}
		if !schemaMaintenanceDeadHolderAction(*rpcAction) {
			return ExitUsage
		}
		raw, err := runtime.readExistingSchemaSealedFile(statePath, true)
		if err != nil {
			return fail(err)
		}
		var state productionSchemaMaintenanceDeadHolderCLIState
		if decodeControllerBackupJSON(raw, &state) != nil {
			return fail(errors.New("dead holder RPC authority is malformed"))
		}
		canonical, _ := json.MarshalIndent(state, "", "  ")
		if !bytes.Equal(raw, append(canonical, '\n')) || state.Format != "lmm-schema-maintenance-dead-holder-authority-v1" || !sameSchemaMaintenancePreExecReference(state.PreExecRecovery, preExec) || state.RecoveryMode != recoveryMode || state.Generation.PlanSHA256 != *planSHA || state.Generation.TransitionID != plan.TransitionID || state.Generation.DaemonExeSHA256 != engineSHA || (state.Mode != schemaMaintenanceDeadPreWorkerExactOwner && state.Mode != schemaMaintenanceDeadPreWorkerZeroOwner) || (state.Mode == schemaMaintenanceDeadPreWorkerExactOwner && !productionSHA256Pattern.MatchString(state.OriginalOwnerSHA256)) || (state.Mode == schemaMaintenanceDeadPreWorkerZeroOwner && state.OriginalOwnerSHA256 != "") {
			return fail(errors.New("dead holder RPC authority differs from the original plan"))
		}
		reply, err := callSchemaMaintenanceRPC(ctx, workspace, state.Generation, *rpcAction)
		if err != nil {
			return fail(err)
		}
		encoded, err := schemaMaintenanceRPCMarshal(reply)
		if err != nil {
			return fail(err)
		}
		if _, err := stdout.Write(append(encoded, '\n')); err != nil {
			return fail(err)
		}
		return ExitOK
	}
	if (action != "dead-holder-serve" && action != "preexec-holder-serve") || *confirm != "api.lmm.best" || *projectionPath == "" || *projectionSHA == "" || *rpcAction != "" || (*mode != "EXACT_ORIGINAL_OWNER" && *mode != "ZERO_OWNER") || (*mode == "EXACT_ORIGINAL_OWNER" && !productionSHA256Pattern.MatchString(*ownerSHA)) || (*mode == "ZERO_OWNER" && *ownerSHA != "") {
		return ExitUsage
	}
	raw, err := runtime.readExistingSchemaSealedFile(filepath.Join(origin.Workspace, "state", "daemon-binding.json"), true)
	if err != nil {
		return fail(err)
	}
	var original schemaMaintenanceRPCBinding
	if schemaMaintenanceRPCDecode(bytes.TrimSuffix(raw, []byte{'\n'}), &original) != nil || original.PlanSHA256 != *planSHA || original.TransitionID != plan.TransitionID || original.DaemonExeSHA256 != plan.Worker.Provider.DeployEngineSHA256 {
		return fail(errors.New("dead holder original native generation is not exact"))
	}
	unit, err := runtime.billingUnitState(ctx, schemaMaintenanceHolderUnit(plan.TransitionID))
	if err != nil {
		return fail(err)
	}
	if err := schemaMaintenanceDeadHolderProcessGone(original, unit); err != nil {
		return fail(err)
	}
	if preExec == nil {
		if err := schemaMaintenanceDeadHolderNoWorker(origin.Workspace); err != nil {
			return fail(err)
		}
	} else if _, err := runtime.checkSchemaMaintenancePreExecRecovery(ctx, plan, *planSHA, *preExec); err != nil {
		return fail(err)
	}
	if preExec != nil {
		if err := runtime.checkSchemaMaintenancePreExecAdmissionUnattempted(plan); err != nil {
			return fail(err)
		}
	}
	for _, name := range []string{"dead-holder-authority.json", "dead-holder-prepared.json", "dead-holder-original-owner.json", "dead-preworker-cas-intent.json", "dead-preworker-cas-result.json", "dead-preworker-cas-reconciled.json"} {
		if _, err := os.Lstat(filepath.Join(origin.Workspace, "state", name)); !errors.Is(err, os.ErrNotExist) {
			return fail(errors.New("dead holder recovery has prior native authority; preserve its original process and inspect without restart"))
		}
	}
	controller, err := runtime.newSchemaMaintenanceControllerWithPreExec(ctx, *planPath, *planSHA, *projectionPath, *projectionSHA, preExec)
	if err != nil {
		return fail(err)
	}
	defer controller.host.Close()
	if err := runtime.checkSchemaMaintenanceGuardianReadopted(controller.host, original); err != nil {
		return fail(err)
	}
	generation, err := schemaMaintenanceRPCSelfBinding(plan.TransitionID, *planSHA, engineSHA)
	if err != nil {
		return fail(err)
	}
	d := &productionSchemaMaintenanceDeadHolderController{base: controller, original: original, generation: generation, ownerSHA: *ownerSHA, ownerMode: *mode}
	if preExec != nil {
		d.preExecRecovery, d.recoveryEngineSHA256 = preExec, engineSHA
	}
	controller.host.validate = d.checkLocal
	if err := d.checkLeasedLocal(ctx); err != nil {
		return fail(err)
	}
	for _, directory := range []string{"/run/lmm-schema-dead-holder", workspace, filepath.Join(workspace, "state")} {
		if err := runtime.merchantStorePrivateDirectory(directory, true); err != nil {
			return fail(err)
		}
	}
	state := productionSchemaMaintenanceDeadHolderCLIState{Format: "lmm-schema-maintenance-dead-holder-authority-v1", Mode: *mode, OriginalOwnerSHA256: *ownerSHA, Generation: generation, PreExecRecovery: preExec, RecoveryMode: recoveryMode}
	if err := runtime.startupBaselineNewFile(statePath, state); err != nil {
		return fail(errors.New("fresh dead holder authority already exists or is unknown; do not replace its process"))
	}
	if _, err := d.prepare(ctx); err != nil {
		return fail(err)
	}
	if err := serveSchemaMaintenanceRPC(ctx, workspace, generation, d.handle); err != nil {
		return fail(err)
	}
	return ExitOK
}
func schemaMaintenanceDeadHolderAction(action string) bool {
	switch action {
	case "dead-prepared", "dead-stopped", "dead-create-binding", "dead-binding", "dead-install-binding", "dead-recover-owner", "dead-recovered", "dead-install-recovered", "dead-diagnose", "dead-reconcile-owner", "dead-startable", "dead-ready", "dead-start-old", "dead-reopen", "dead-finish", "dead-status":
		return true
	}
	return false
}
