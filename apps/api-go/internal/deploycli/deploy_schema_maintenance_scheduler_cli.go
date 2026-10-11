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
	"time"
)

func runProductionSchemaMaintenanceControllerAction(action string, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet(DeployProgramName+" production schema-maintenance "+action, flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("plan", "", "private identical canonical controller maintenance plan")
	sha := flags.String("plan-sha256", "", "exact canonical plan SHA256")
	phase := flags.String("action", "", "fixed native phase or observation")
	confirm := flags.String("confirm", "", "production authorization")
	identity := flags.String("age-identity-file", "", "controller-only age identity for full independent restore")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return ExitOK
		}
		return ExitUsage
	}
	if flags.NArg() != 0 || *path == "" || *sha == "" || *confirm != "api.lmm.best" || (action != "backup-verify" && *identity != "") || ((action == "backup-verify" || action == "backup-collect") && *phase != "") || (action == "backup-verify" && *identity == "") {
		return ExitUsage
	}
	fail := func(err error) int { fmt.Fprintf(stderr, "schema-maintenance %s: %v\n", action, err); return ExitError }
	plan, err := loadSchemaMaintenanceControllerPlan(*path, *sha)
	if err != nil {
		return fail(err)
	}
	if sha256MustEqual("/proc/self/exe", plan.Worker.Provider.DeployEngineSHA256) != nil {
		return fail(errors.New("schema scheduler is not the exact signed maintenance engine"))
	}
	runtime := &productionReleaseRuntime{runner: osProductionCommandRunner{}, now: time.Now}
	ctx := context.Background()
	if action == "phase" {
		results, err := runtime.dispatchSchemaMaintenancePhase(ctx, plan, *sha, *phase)
		if err != nil {
			return fail(err)
		}
		return writeJSONCommandResult(results, stdout, stderr, "schema-maintenance phase")
	}
	lock, err := acquireSchemaMaintenanceSchedulerLock(plan.BackupPolicy.ControllerDirectory)
	if err != nil {
		return fail(err)
	}
	defer lock.Close()
	if action == "observe" {
		switch *phase {
		case "capture-closed", "capture-stopped", "capture-recovery", "capture-ready", "capture-startable", "capture-recovery-profile", "status":
		default:
			return ExitUsage
		}
		var results []schemaMaintenanceRPCReply
		for _, origin := range plan.Origins {
			reply, err := runtime.schemaMaintenanceRemoteRPC(ctx, plan, *sha, origin, *phase)
			if err != nil {
				return fail(err)
			}
			results = append(results, reply)
		}
		return writeJSONCommandResult(results, stdout, stderr, "schema-maintenance observation")
	}
	stopped, err := runtime.schemaMaintenanceControllerStoppedOrigins(ctx, plan, *sha)
	if err != nil {
		return fail(err)
	}
	if action == "backup-collect" {
		set, err := runtime.collectSchemaMaintenanceBackup(ctx, plan, *sha, stopped)
		if err != nil {
			return fail(err)
		}
		raw, err := json.MarshalIndent(set, "", "  ")
		if err != nil {
			return fail(err)
		}
		return writeJSONCommandResult(productionReleaseFilePlan{Path: filepath.Join(plan.BackupPolicy.ControllerDirectory, schemaMaintenanceBackupCollectionName), SHA256: startupContentSHA256(append(raw, '\n'))}, stdout, stderr, "schema-maintenance backup collection")
	}
	proof, err := runtime.prepareSchemaMaintenanceBackup(ctx, plan, *sha, stopped, *identity)
	if err != nil {
		return fail(err)
	}
	// Reobserve the actual closed/stopped generations after the independent
	// restore. Only the signature and metadata leave this controller.
	after, err := runtime.schemaMaintenanceControllerStoppedOrigins(ctx, plan, *sha)
	if err != nil {
		return fail(err)
	}
	beforeRaw, _ := json.Marshal(stopped)
	afterRaw, _ := json.Marshal(after)
	if !bytes.Equal(beforeRaw, afterRaw) {
		return fail(errors.New("schema backup source generations changed during independent restore"))
	}
	if err := schemaMaintenanceControllerNewFile(plan.BackupPolicy.RestoreProofPath, append(proof, '\n')); err != nil {
		return fail(err)
	}
	return writeJSONCommandResult(productionReleaseFilePlan{Path: plan.BackupPolicy.RestoreProofPath, SHA256: startupContentSHA256(append(proof, '\n'))}, stdout, stderr, "schema-maintenance backup verification")
}

func (runtime *productionReleaseRuntime) schemaMaintenanceControllerStoppedOrigins(ctx context.Context, plan productionSchemaMaintenancePlan, sha string) ([]productionSchemaMaintenanceHostCapture, error) {
	var captures []productionSchemaMaintenanceHostCapture
	for _, origin := range plan.Origins {
		reply, err := runtime.schemaMaintenanceRemoteRPC(ctx, plan, sha, origin, "capture-stopped")
		if err != nil {
			return nil, err
		}
		var capture productionSchemaMaintenanceHostCapture
		decoder := json.NewDecoder(bytes.NewReader(reply.Result))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&capture) != nil {
			return nil, errors.New("schema backup stopped-origin native result is invalid")
		}
		typed, _ := json.Marshal(capture)
		typedReply := reply
		typedReply.Result = typed
		canonical, _ := schemaMaintenanceRPCMarshal(typedReply)
		original, _ := schemaMaintenanceRPCMarshal(reply)
		if !bytes.Equal(canonical, original) || capture.Host != origin.Host {
			return nil, errors.New("schema backup stopped-origin typed result is ambiguous")
		}
		captures = append(captures, capture)
	}
	if _, err := schemaMaintenanceBackupStopBindings(plan, sha, captures); err != nil {
		return nil, err
	}
	return captures, nil
}

// Keep the target proof path explicit for review and transfer: it is unrelated
// to an ordinary prepare receipt or a financial handoff.
func schemaMaintenanceTargetBackupProofPath(plan productionSchemaMaintenancePlan) string {
	return filepath.Join(plan.Origins[0].Workspace, "state", schemaMaintenanceBackupProofName)
}
