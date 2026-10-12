package deploycli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
)

func runProductionSchemaMaintenance(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "schema-maintenance requires serve, rpc, environment, phase, observe, backup-collect or backup-verify")
		return ExitUsage
	}
	action := args[0]
	if action == "dead-holder-phase" {
		return runProductionSchemaMaintenanceDeadHolderPhase(args[1:], stdout, stderr)
	}
	if action == "dead-holder-serve" || action == "dead-holder-rpc" || action == "preexec-holder-serve" || action == "preexec-holder-rpc" {
		return runProductionSchemaMaintenanceDeadHolder(action, args[1:], stdout, stderr)
	}
	if action == "phase" || action == "observe" || action == "backup-verify" || action == "backup-collect" {
		return runProductionSchemaMaintenanceControllerAction(action, args[1:], stdout, stderr)
	}
	flags := flag.NewFlagSet(DeployProgramName+" production schema-maintenance "+action, flag.ContinueOnError)
	flags.SetOutput(stderr)
	planPath := flags.String("plan", "", "exact canonical target maintenance plan")
	planSHA := flags.String("plan-sha256", "", "exact plan SHA256")
	projectionPath := flags.String("host-binding", "", "exact root-owned local host projection")
	projectionSHA := flags.String("host-binding-sha256", "", "exact local host projection SHA256")
	rpcAction := flags.String("action", "", "fixed native RPC action")
	confirm := flags.String("confirm", "", "production authorization")
	transition := flags.String("transition-id", "", "fixed maintenance transition")
	artifactPath := flags.String("artifact-capsule", "", "actual held official artifact capsule")
	artifactSHA := flags.String("artifact-capsule-sha256", "", "exact official artifact capsule SHA256")
	oldPath := flags.String("old-capsule", "", "actual retained per-start capsule")
	oldSHA := flags.String("old-capsule-sha256", "", "exact retained capsule SHA256")
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return ExitOK
		}
		return ExitUsage
	}
	if flags.NArg() != 0 {
		return ExitUsage
	}
	runtime := defaultProductionRuntime()
	ctx := context.Background()
	fail := func(err error) int { fmt.Fprintf(stderr, "schema-maintenance %s: %v\n", action, err); return ExitError }
	switch action {
	case "environment":
		if *confirm != "api.lmm.best" || *transition == "" || *artifactPath == "" || *artifactSHA == "" || *oldPath == "" || *oldSHA == "" || *planPath != "" || *planSHA != "" || *projectionPath != "" || *projectionSHA != "" || *rpcAction != "" {
			return ExitUsage
		}
		if err := runtime.assertProductionMutation(); err != nil {
			return fail(err)
		}
		artifacts, err := runtime.loadMerchantStoreCapsule(*artifactPath, *artifactSHA)
		if err != nil {
			return fail(err)
		}
		old, err := runtime.loadMerchantStoreCapsule(*oldPath, *oldSHA)
		if err != nil {
			return fail(err)
		}
		if artifacts.DeploymentID != *transition || !productionIDPattern.MatchString(*transition) {
			return fail(errors.New("environment projection transition differs from its held capsule"))
		}
		for _, rollback := range []bool{false, true} {
			if _, err := runtime.verifyMerchantStoreCapsuleArtifact(ctx, artifacts, rollback); err != nil {
				return fail(err)
			}
		}
		child, err := runtime.schemaMaintenanceWorkerEnvironment(ctx, artifacts, old, *oldSHA)
		if err != nil {
			return fail(err)
		}
		raw, err := canonicalSchemaMaintenanceWorkerEnvironment(child)
		if err != nil {
			return fail(err)
		}
		path := filepath.Join(schemaMaintenanceHostWorkspace(runtime.paths, *transition), "state", "schema-worker.env")
		if err := runtime.writeSchemaMaintenanceRawNewFile(path, raw); err != nil {
			return fail(errors.New("secret environment projection already exists or is uncertain; reconcile without overwrite"))
		}
		result, _ := json.Marshal(productionReleaseFilePlan{Path: path, SHA256: startupContentSHA256(raw)})
		stdout.Write(append(result, '\n'))
		return ExitOK
	case "serve":
		if *confirm != "api.lmm.best" || *planPath == "" || *planSHA == "" || *projectionPath == "" || *projectionSHA == "" || *rpcAction != "" || *transition != "" || *artifactPath != "" || *artifactSHA != "" || *oldPath != "" || *oldSHA != "" {
			return ExitUsage
		}
		// The dedicated host controller recognizes both fixed origins in its
		// canonical plan. The ordinary package-controller assertion is Arch-only.
		if runtime.effectiveUID() != 0 {
			return fail(errors.New("schema host daemon requires root"))
		}
		controller, err := runtime.newSchemaMaintenanceController(ctx, *planPath, *planSHA, *projectionPath, *projectionSHA)
		if err != nil {
			return fail(err)
		}
		defer controller.host.Close()
		defer func() {
			if controller.fence != nil {
				_ = controller.fence.Close()
			}
		}()
		binding, err := schemaMaintenanceRPCSelfBinding(controller.plan.TransitionID, *planSHA, controller.plan.Worker.Provider.DeployEngineSHA256)
		if err != nil {
			return fail(err)
		}
		controller.daemonGeneration = binding
		raw, err := schemaMaintenanceRPCMarshal(binding)
		if err != nil {
			return fail(err)
		}
		if err := runtime.writeSchemaMaintenanceRawNewFile(filepath.Join(controller.origin.Workspace, "state", "daemon-binding.json"), append(raw, '\n')); err != nil {
			return fail(errors.New("schema daemon generation already exists; reconcile original process without replacing it"))
		}
		if err := runtime.writeSchemaMaintenanceGuardianAdopted(controller.host, binding); err != nil {
			return fail(err)
		}
		// All mutations execute in this same holder process; an RPC error returns to
		// the caller while this daemon retains its PG session and adopted locks.
		if err := serveSchemaMaintenanceRPC(ctx, controller.origin.Workspace, binding, controller.handle); err != nil {
			return fail(err)
		}
		return ExitOK
	case "rpc":
		if *planPath == "" || *planSHA == "" || *rpcAction == "" || *projectionPath != "" || *projectionSHA != "" || *transition != "" || *artifactPath != "" || *artifactSHA != "" || *oldPath != "" || *oldSHA != "" || *confirm != "" {
			return ExitUsage
		}
		if runtime.effectiveUID() != 0 {
			return fail(errors.New("native schema RPC client requires root"))
		}
		plan, err := runtime.loadSchemaMaintenancePlan(*planPath, *planSHA)
		if err != nil {
			return fail(err)
		}
		if sha256MustEqual("/proc/self/exe", plan.Worker.Provider.DeployEngineSHA256) != nil {
			return fail(errors.New("native schema RPC client is not the exact signed maintenance engine"))
		}
		name, err := runtime.hostname()
		if err != nil {
			return fail(err)
		}
		workspace := ""
		for _, origin := range plan.Origins {
			if origin.Host == name {
				workspace = origin.Workspace
			}
		}
		if workspace == "" {
			return fail(errors.New("native schema RPC client is not a registered origin"))
		}
		raw, err := runtime.readExistingSchemaSealedFile(filepath.Join(workspace, "state", "daemon-binding.json"), true)
		if err != nil {
			return fail(err)
		}
		var binding schemaMaintenanceRPCBinding
		if schemaMaintenanceRPCDecode(bytes.TrimSuffix(raw, []byte{'\n'}), &binding) != nil || binding.PlanSHA256 != *planSHA || binding.TransitionID != plan.TransitionID || binding.DaemonExeSHA256 != plan.Worker.Provider.DeployEngineSHA256 {
			return fail(errors.New("native schema RPC daemon binding differs from its exact canonical plan"))
		}
		reply, err := callSchemaMaintenanceRPC(ctx, workspace, binding, *rpcAction)
		if err != nil {
			return fail(err)
		}
		encoded, err := schemaMaintenanceRPCMarshal(reply)
		if err != nil {
			return fail(err)
		}
		stdout.Write(append(encoded, '\n'))
		if !reply.OK {
			return ExitError
		}
		return ExitOK
	default:
		return ExitUsage
	}
}
