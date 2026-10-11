//go:build linux

package deploycli

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestSchemaMaintenanceDeadHolderRequiresActualOriginalProcessExit(t *testing.T) {
	child := exec.Command("/usr/bin/sleep", "30")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
	start, err := schemaMaintenanceGuardianProcess(child.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	generation := schemaMaintenanceRPCBinding{PlanSHA256: strings.Repeat("a", 64), TransitionID: "dead-holder-test", DaemonPID: child.Process.Pid, DaemonInvocationID: strings.Repeat("b", 32), DaemonExeSHA256: strings.Repeat("c", 64), DaemonStartTicks: start}
	unit := map[string]string{"MainPID": "0", "ExecMainPID": strconv.Itoa(generation.DaemonPID), "InvocationID": generation.DaemonInvocationID, "Restart": "no", "ActiveState": "inactive", "SubState": "dead"}
	if err := schemaMaintenanceDeadHolderProcessGone(generation, unit); err == nil {
		t.Fatal("an inactive-looking unit admitted an actually live original process")
	}
	reused := generation
	reused.DaemonStartTicks++
	if err := schemaMaintenanceDeadHolderProcessGone(reused, unit); err == nil {
		t.Fatal("reused PID accepted as dead original authority")
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = child.Wait()
	if err := schemaMaintenanceDeadHolderProcessGone(generation, unit); err != nil {
		t.Fatalf("actual exited child with exact inactive unit rejected: %v", err)
	}
	for _, field := range []string{"MainPID", "ExecMainPID", "InvocationID", "Restart", "ActiveState", "SubState"} {
		changed := map[string]string{}
		for key, value := range unit {
			changed[key] = value
		}
		changed[field] = "unknown"
		if err := schemaMaintenanceDeadHolderProcessGone(generation, changed); err == nil {
			t.Fatalf("unknown native %s accepted as death", field)
		}
	}
}
func TestSchemaMaintenanceDeadHolderRejectsEveryExistingWorkerFootprint(t *testing.T) {
	for _, name := range schemaMaintenanceDeadHolderForbidden {
		for _, kind := range []string{"regular", "directory", "dangling-symlink"} {
			t.Run(name+"/"+kind, func(t *testing.T) {
				root := t.TempDir()
				state := filepath.Join(root, "state")
				if err := os.Mkdir(state, 0700); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(state, name)
				var err error
				switch kind {
				case "regular":
					err = os.WriteFile(path, nil, 0600)
				case "directory":
					err = os.Mkdir(path, 0700)
				case "dangling-symlink":
					err = os.Symlink("absent", path)
				}
				if err != nil {
					t.Fatal(err)
				}
				if err := schemaMaintenanceDeadHolderNoWorker(root); err == nil {
					t.Fatal("unsafe possibly executed worker/recovery object accepted")
				}
				info, err := os.Lstat(path)
				if err != nil {
					t.Fatal("refusal removed original evidence")
				}
				if kind == "regular" && info.Size() != 0 {
					t.Fatal("refusal rewrote original evidence")
				}
			})
		}
	}
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "state"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"schema-claim-intent.json", "schema-owner.json"} {
		if err := os.WriteFile(filepath.Join(root, "state", name), []byte("preserved original"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := schemaMaintenanceDeadHolderNoWorker(root); err != nil {
		t.Fatal("dead owner lane should preserve and separately validate original claim evidence, not classify NEVER_CLAIMED")
	}
}
func TestSchemaMaintenanceDeadHolderPreparedSeparatesOriginalAndRecoveryAuthority(t *testing.T) {
	_, plan, _ := newSchemaMaintenancePlanFixture(t)
	sha := strings.Repeat("a", 64)
	origin := plan.Origins[0]
	original := schemaMaintenanceRPCBinding{PlanSHA256: sha, TransitionID: plan.TransitionID, DaemonPID: 123, DaemonInvocationID: strings.Repeat("b", 32), DaemonExeSHA256: plan.Worker.Provider.DeployEngineSHA256, DaemonStartTicks: 10}
	fresh := original
	fresh.DaemonPID = 456
	fresh.DaemonInvocationID = strings.Repeat("c", 32)
	fresh.DaemonStartTicks = 20
	ref := func(name string) productionReleaseFilePlan {
		return productionReleaseFilePlan{Path: filepath.Join(origin.Workspace, "state", name), SHA256: strings.Repeat("d", 64)}
	}
	prepared := productionSchemaMaintenanceDeadHolderPrepared{Format: "lmm-schema-maintenance-dead-holder-prepared-v1", TransitionID: plan.TransitionID, PrePlanSHA256: sha, Mode: schemaMaintenanceDeadHolderMode, OwnerMode: schemaMaintenanceDeadPreWorkerExactOwner, OriginalOwnerSHA256: strings.Repeat("f", 64), Host: origin.Host, OriginalDaemon: original, RecoveryDaemon: fresh, OriginalDaemonFile: ref("daemon-binding.json"), GuardianAdopted: ref("guardian-adopted.json"), GuardianBinding: ref("guardian-binding.json"), OldCapsule: origin.OldCapsule, StoppedCapture: ref("writer-stopped.json")}
	if err := validateSchemaMaintenanceDeadHolderPrepared(prepared, plan, sha, origin); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*productionSchemaMaintenanceDeadHolderPrepared){func(p *productionSchemaMaintenanceDeadHolderPrepared) { p.RecoveryDaemon = p.OriginalDaemon }, func(p *productionSchemaMaintenanceDeadHolderPrepared) {
		p.Mode = schemaMaintenanceAbortModeNeverClaimed
	}, func(p *productionSchemaMaintenanceDeadHolderPrepared) {
		p.GuardianAdopted.Path = filepath.Join(origin.Workspace, "state", "replacement.json")
	}, func(p *productionSchemaMaintenanceDeadHolderPrepared) {
		p.OriginalDaemon.DaemonExeSHA256 = strings.Repeat("f", 64)
	}, func(p *productionSchemaMaintenanceDeadHolderPrepared) { p.OriginalDaemonFile.SHA256 = "" }, func(p *productionSchemaMaintenanceDeadHolderPrepared) { p.StoppedCapture.Path += ".new" }} {
		changed := prepared
		mutate(&changed)
		if err := validateSchemaMaintenanceDeadHolderPrepared(changed, plan, sha, origin); err == nil {
			t.Fatal("changed original or fresh recovery authority accepted")
		}
	}
}
func TestSchemaMaintenanceDeadHolderSchedulerNeverRetriesUnknownMutation(t *testing.T) {
	runtime, plan, sha, runner := newSchemaMaintenanceSchedulerFixture(t)
	runner.action = "dead-install-binding"
	runner.failAt = 1
	if _, err := runtime.dispatchDeadHolderPhase(context.Background(), plan, sha, runner.action); err == nil || len(runner.commands) != 1 {
		t.Fatal("unknown first origin did not stop entire dead-holder phase")
	}
	runner.failAt = 0
	for _, action := range []string{"dead-install-binding", "dead-recover-owner", "dead-start-old"} {
		runner.action = action
		if _, err := runtime.dispatchDeadHolderPhase(context.Background(), plan, sha, action); err == nil || len(runner.commands) != 1 {
			t.Fatal("unknown dead-holder dispatch admitted a subsequent mutation")
		}
	}
	runner.action = "dead-status"
	if _, err := runtime.dispatchDeadHolderPhase(context.Background(), plan, sha, runner.action); err != nil || len(runner.commands) != 3 {
		t.Fatalf("read-only status blocked after unknown dispatch: %v", err)
	}
}
func TestSchemaMaintenanceDeadHolderSchedulerRejectsConflictingWorkerAndAmbiguousReplies(t *testing.T) {
	for _, kind := range []string{"worker", "ambiguous-success"} {
		t.Run(kind, func(t *testing.T) {
			runtime, plan, sha, runner := newSchemaMaintenanceSchedulerFixture(t)
			runner.action = "dead-recover-owner"
			if kind == "worker" {
				if err := os.Symlink("absent", filepath.Join(plan.BackupPolicy.ControllerDirectory, "state", "dispatch-worker.json")); err != nil {
					t.Fatal(err)
				}
			} else {
				runner.replyError = "response claims both success and error"
			}
			if _, err := runtime.dispatchDeadHolderPhase(context.Background(), plan, sha, runner.action); err == nil {
				t.Fatal("unsafe dead-holder scheduler input accepted")
			}
			if kind == "worker" && len(runner.commands) != 0 {
				t.Fatal("worker dispatch conflict caused remote side effects")
			}
		})
	}
}
func TestSchemaMaintenanceDeadHolderSchedulerBindsExactCompletionAndRoutes(t *testing.T) {
	runtime, plan, sha, runner := newSchemaMaintenanceSchedulerFixture(t)
	runner.action = "dead-install-binding"
	results, err := runtime.dispatchDeadHolderPhase(context.Background(), plan, sha, runner.action)
	if err != nil || len(results) != 2 {
		t.Fatalf("fixed dead-holder sequence failed: %v", err)
	}
	if err := schemaMaintenanceDeadHolderCompleted(plan, sha, runner.action); err != nil {
		t.Fatal(err)
	}
	for i, command := range runner.commands {
		if command.Name != commandSSH || len(command.Args) != 12 || command.Args[10] != plan.Origins[i].Alias || !strings.Contains(command.Args[11], "'dead-holder-rpc'") || !strings.Contains(command.Args[11], "'--action' 'dead-install-binding'") || command.Args[3] != "StrictHostKeyChecking=yes" || command.Args[5] != "ControlMaster=no" || command.Args[7] != "ControlPath=none" {
			t.Fatalf("dedicated route escaped fixed peer: %+v", command)
		}
	}
	path := filepath.Join(plan.BackupPolicy.ControllerDirectory, "state", "dead-dispatch-"+runner.action+".complete.json")
	results[0].Error = "ambiguous"
	raw, _ := json.MarshalIndent(results, "", "  ")
	if err := os.WriteFile(path, append(raw, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	runner.action = "dead-recover-owner"
	if _, err := runtime.dispatchDeadHolderPhase(context.Background(), plan, sha, runner.action); err == nil || len(runner.commands) != 2 {
		t.Fatal("tampered completed evidence admitted a mutation")
	}
}
