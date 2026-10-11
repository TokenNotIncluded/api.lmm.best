package deploycli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func schemaMaintenanceAbortTestPhases() []string {
	return []string{"prepare-abort", "create-abort-binding", "start-abort-old", "reopen-abort", "finish-abort"}
}

func TestSchemaMaintenanceRPCAbortActionsHaveFixedBudgets(t *testing.T) {
	for action, expected := range map[string]time.Duration{
		"prepare-abort": 20 * time.Minute, "check-abort-prestate": 20 * time.Minute,
		"capture-abort-startable": 20 * time.Minute, "capture-abort-ready": 20 * time.Minute,
		"create-abort-binding": 45 * time.Minute, "start-abort-old": 65 * time.Minute,
		"reopen-abort": 110 * time.Minute, "finish-abort": 65 * time.Minute,
	} {
		actual, err := schemaMaintenanceRPCActionTimeout(action)
		if err != nil || actual != expected {
			t.Fatalf("%s budget = %s, %v; want %s", action, actual, err, expected)
		}
		for _, malformed := range []string{action + " --execute", action + "/tmp", " " + action, strings.ToUpper(action)} {
			if _, err := schemaMaintenanceRPCActionTimeout(malformed); err == nil {
				t.Fatalf("expanded abort action accepted: %q", malformed)
			}
		}
	}
}

func schemaMaintenanceAbortTestDispatch(t *testing.T, runtime *productionReleaseRuntime, plan productionSchemaMaintenancePlan, sha string, runner *schemaMaintenanceSchedulerRunner, action string) []schemaMaintenanceRPCReply {
	t.Helper()
	runner.action = action
	replies, err := runtime.dispatchSchemaMaintenancePhase(context.Background(), plan, sha, action)
	if err != nil {
		t.Fatalf("fixture phase %s failed: %v", action, err)
	}
	return replies
}

func schemaMaintenanceAbortTestStopped(t *testing.T) (*productionReleaseRuntime, productionSchemaMaintenancePlan, string, *schemaMaintenanceSchedulerRunner) {
	t.Helper()
	runtime, plan, sha, runner := newSchemaMaintenanceSchedulerFixture(t)
	for _, action := range []string{"close", "stop"} {
		schemaMaintenanceAbortTestDispatch(t, runtime, plan, sha, runner, action)
	}
	return runtime, plan, sha, runner
}

func TestSchemaMaintenanceAbortSchedulerOrdersFixedOriginsAndAllowsFailedBackupIntent(t *testing.T) {
	runtime, plan, sha, runner := schemaMaintenanceAbortTestStopped(t)
	// Collection has its own root-level one-shot intent. Its unknown export
	// cannot be resent, but must not prevent the independently proved abort.
	if err := os.WriteFile(filepath.Join(plan.BackupPolicy.ControllerDirectory, schemaMaintenanceBackupExportIntentName), []byte("fixture interrupted backup export"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, action := range schemaMaintenanceAbortTestPhases() {
		before := len(runner.commands)
		replies := schemaMaintenanceAbortTestDispatch(t, runtime, plan, sha, runner, action)
		origins := plan.Origins
		if action == "create-abort-binding" {
			origins = origins[:1]
		}
		commands := runner.commands[before:]
		if len(commands) != len(origins) || len(replies) != len(origins) {
			t.Fatalf("%s dispatched %d commands and %d replies, want %d", action, len(commands), len(replies), len(origins))
		}
		for index, command := range commands {
			budget, _ := schemaMaintenanceRPCActionTimeout(action)
			if command.Name != commandSSH || len(command.Args) != 12 || command.Args[10] != origins[index].Alias || !strings.Contains(command.Args[11], "'--action' '"+action+"'") || command.Timeout != budget+10*time.Second || command.OutputLimit != 65536 {
				t.Fatalf("%s escaped its fixed ordered origin or budget: %+v", action, command)
			}
			if command.Args[3] != "StrictHostKeyChecking=yes" || command.Args[5] != "ControlMaster=no" || command.Args[7] != "ControlPath=none" {
				t.Fatalf("%s dropped native strict SSH controls: %+v", action, command.Args)
			}
		}
		if err := schemaMaintenanceCheckPreviousDispatches(plan, sha); err != nil {
			t.Fatalf("%s did not persist exact completion: %v", action, err)
		}
		if _, err := runtime.dispatchSchemaMaintenancePhase(context.Background(), plan, sha, action); err == nil || len(runner.commands) != before+len(origins) {
			t.Fatalf("%s mutation was resent", action)
		}
	}
}

func TestSchemaMaintenanceAbortSchedulerRequiresEveryCompletedPriorPhase(t *testing.T) {
	for _, target := range schemaMaintenanceAbortTestPhases() {
		for _, missing := range []string{"close", "stop"} {
			t.Run(target+"-missing-"+missing, func(t *testing.T) {
				runtime, plan, sha, runner := newSchemaMaintenanceSchedulerFixture(t)
				for _, action := range []string{"close", "stop"} {
					if action != missing {
						schemaMaintenanceAbortTestDispatch(t, runtime, plan, sha, runner, action)
					}
				}
				before := len(runner.commands)
				if _, err := runtime.dispatchSchemaMaintenancePhase(context.Background(), plan, sha, target); err == nil || len(runner.commands) != before {
					t.Fatal("abort accepted a missing closed/stopped origin phase")
				}
			})
		}
	}
	for _, missing := range []string{"dispatch-close.complete.json", "dispatch-stop.complete.json", "dispatch-stop-arch-dmit.json", "dispatch-stop-dmit-ubuntu.json"} {
		t.Run(missing, func(t *testing.T) {
			runtime, plan, sha, runner := schemaMaintenanceAbortTestStopped(t)
			if err := os.Remove(filepath.Join(plan.BackupPolicy.ControllerDirectory, "state", missing)); err != nil {
				t.Fatal(err)
			}
			before := len(runner.commands)
			if _, err := runtime.dispatchSchemaMaintenancePhase(context.Background(), plan, sha, "prepare-abort"); err == nil || len(runner.commands) != before {
				t.Fatal("abort accepted incomplete close/stop native receipts")
			}
		})
	}
	for _, target := range schemaMaintenanceAbortTestPhases()[1:] {
		t.Run("cannot-skip-to-"+target, func(t *testing.T) {
			runtime, plan, sha, runner := schemaMaintenanceAbortTestStopped(t)
			before := len(runner.commands)
			if _, err := runtime.dispatchSchemaMaintenancePhase(context.Background(), plan, sha, target); err == nil || len(runner.commands) != before {
				t.Fatal("abort skipped its preceding phases")
			}
		})
	}
}

func TestSchemaMaintenanceAbortSchedulerFreezesNormalLaneAndRejectsClaimHistory(t *testing.T) {
	for _, abort := range schemaMaintenanceAbortTestPhases() {
		for _, normal := range schemaMaintenanceNormalDispatchActions() {
			t.Run(abort+"-freezes-"+normal, func(t *testing.T) {
				runtime, plan, sha, runner := newSchemaMaintenanceSchedulerFixture(t)
				path := filepath.Join(plan.BackupPolicy.ControllerDirectory, "state", "dispatch-"+abort+".json")
				// Even a damaged or unrelated abort intent freezes normal work.
				if err := os.Symlink("missing-original-intent", path); err != nil {
					t.Fatal(err)
				}
				if _, err := runtime.dispatchSchemaMaintenancePhase(context.Background(), plan, sha, normal); err == nil || len(runner.commands) != 0 {
					t.Fatal("normal mutation bypassed an existing abort intent")
				}
			})
		}
	}
	for _, normal := range schemaMaintenanceNormalDispatchActions()[2:] {
		t.Run("completed-"+normal+"-blocks-never-claimed", func(t *testing.T) {
			runtime, plan, sha, runner := schemaMaintenanceAbortTestStopped(t)
			schemaMaintenanceAbortTestDispatch(t, runtime, plan, sha, runner, normal)
			before := len(runner.commands)
			if _, err := runtime.dispatchSchemaMaintenancePhase(context.Background(), plan, sha, "prepare-abort"); err == nil || len(runner.commands) != before {
				t.Fatal("NEVER_CLAIMED lane accepted attempted claim or later normal work")
			}
		})
	}
}

func TestSchemaMaintenanceAbortSchedulerUnknownPhaseAllowsObservationWithoutResend(t *testing.T) {
	for stage, target := range schemaMaintenanceAbortTestPhases() {
		count := 2
		if target == "create-abort-binding" {
			count = 1
		}
		for failedCall := 1; failedCall <= count; failedCall++ {
			t.Run(fmt.Sprintf("%s-origin-%d", target, failedCall), func(t *testing.T) {
				runtime, plan, sha, runner := schemaMaintenanceAbortTestStopped(t)
				for _, earlier := range schemaMaintenanceAbortTestPhases()[:stage] {
					schemaMaintenanceAbortTestDispatch(t, runtime, plan, sha, runner, earlier)
				}
				before := len(runner.commands)
				runner.action = target
				runner.failAt = before + failedCall
				if _, err := runtime.dispatchSchemaMaintenancePhase(context.Background(), plan, sha, target); err == nil || len(runner.commands) != before+failedCall {
					t.Fatal("unknown mutation did not stop at its first unavailable result")
				}
				runner.failAt = 0
				for _, action := range append(schemaMaintenanceAbortTestPhases(), schemaMaintenanceNormalDispatchActions()...) {
					if _, err := runtime.dispatchSchemaMaintenancePhase(context.Background(), plan, sha, action); err == nil || len(runner.commands) != before+failedCall {
						t.Fatalf("%s advanced or resent mutation after unknown %s", action, target)
					}
				}
				for _, action := range []string{"check-abort-prestate", "capture-abort-startable", "capture-abort-ready"} {
					runner.action = action
					if _, err := runtime.schemaMaintenanceRemoteRPC(context.Background(), plan, sha, plan.Origins[0], action); err != nil {
						t.Fatalf("fixed readonly reconciliation unavailable after unknown %s: %v", target, err)
					}
				}
			})
		}
	}
}

func TestSchemaMaintenanceAbortSchedulerPriorReceiptRequiresCompleteEnvelope(t *testing.T) {
	for name, mutate := range map[string]func(*schemaMaintenanceRPCReply){
		"wrong protocol":     func(reply *schemaMaintenanceRPCReply) { reply.Protocol = "other-protocol" },
		"invalid nonce":      func(reply *schemaMaintenanceRPCReply) { reply.Nonce = "invalid" },
		"success with error": func(reply *schemaMaintenanceRPCReply) { reply.Error = "not success" },
	} {
		t.Run(name, func(t *testing.T) {
			runtime, plan, sha, runner := schemaMaintenanceAbortTestStopped(t)
			state := filepath.Join(plan.BackupPolicy.ControllerDirectory, "state")
			completePath := filepath.Join(state, "dispatch-stop.complete.json")
			raw, err := os.ReadFile(completePath)
			if err != nil {
				t.Fatal(err)
			}
			var complete productionSchemaMaintenanceDispatchComplete
			if err := json.Unmarshal(raw, &complete); err != nil {
				t.Fatal(err)
			}
			replyRaw, err := os.ReadFile(complete.Replies[0].Path)
			if err != nil {
				t.Fatal(err)
			}
			var reply schemaMaintenanceRPCReply
			if err := json.Unmarshal(replyRaw, &reply); err != nil {
				t.Fatal(err)
			}
			mutate(&reply)
			replyRaw, err = schemaMaintenanceRPCMarshal(reply)
			if err != nil {
				t.Fatal(err)
			}
			replyRaw = append(replyRaw, '\n')
			if err := os.WriteFile(complete.Replies[0].Path, replyRaw, 0600); err != nil {
				t.Fatal(err)
			}
			complete.Replies[0].SHA256 = startupContentSHA256(replyRaw)
			raw, err = json.Marshal(complete)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(completePath, append(raw, '\n'), 0600); err != nil {
				t.Fatal(err)
			}
			before := len(runner.commands)
			if _, err := runtime.dispatchSchemaMaintenancePhase(context.Background(), plan, sha, "prepare-abort"); err == nil || len(runner.commands) != before {
				t.Fatal("abort accepted a hash-matched but incomplete successful envelope")
			}
		})
	}
}

func TestSchemaMaintenanceAbortSchedulerAmbiguousSuccessStopsBeforeSecondOrigin(t *testing.T) {
	runtime, plan, sha, runner := schemaMaintenanceAbortTestStopped(t)
	runner.action, runner.replyError = "prepare-abort", "fixture ambiguous successful reply"
	before := len(runner.commands)
	if _, err := runtime.dispatchSchemaMaintenancePhase(context.Background(), plan, sha, "prepare-abort"); err == nil || len(runner.commands) != before+1 {
		t.Fatal("ambiguous successful reply advanced to the second origin")
	}
	path := filepath.Join(plan.BackupPolicy.ControllerDirectory, "state", "dispatch-prepare-abort.complete.json")
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatal("ambiguous successful reply produced a completed phase")
	}
	runner.replyError = ""
	if _, err := runtime.dispatchSchemaMaintenancePhase(context.Background(), plan, sha, "prepare-abort"); err == nil || len(runner.commands) != before+1 {
		t.Fatal("ambiguous successful reply was retried")
	}
}
