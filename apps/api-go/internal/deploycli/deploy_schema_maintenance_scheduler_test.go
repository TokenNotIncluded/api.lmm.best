package deploycli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type schemaMaintenanceSchedulerRunner struct {
	plan     productionSchemaMaintenancePlan
	sha      string
	commands []productionCommand
	failAt   int
}

func (r *schemaMaintenanceSchedulerRunner) Run(_ context.Context, command productionCommand) ([]byte, error) {
	r.commands = append(r.commands, command)
	if r.failAt == len(r.commands) {
		return nil, errors.New("reply lost after potentially accepted mutation")
	}
	result, _ := json.Marshal(map[string]string{"phase": "test-only"})
	reply := schemaMaintenanceRPCReply{Protocol: schemaMaintenanceRPCProtocol, Action: "close", Nonce: strings.Repeat("e", 64), OK: true, Result: result, Binding: schemaMaintenanceRPCBinding{PlanSHA256: r.sha, TransitionID: r.plan.TransitionID, DaemonPID: 123, DaemonInvocationID: strings.Repeat("b", 32), DaemonExeSHA256: r.plan.Worker.Provider.DeployEngineSHA256, DaemonStartTicks: 10}}
	raw, err := schemaMaintenanceRPCMarshal(reply)
	return append(raw, '\n'), err
}

func newSchemaMaintenanceSchedulerFixture(t *testing.T) (*productionReleaseRuntime, productionSchemaMaintenancePlan, string, *schemaMaintenanceSchedulerRunner) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("controller scheduling must run under its ordinary UID")
	}
	_, plan, _ := newSchemaMaintenancePlanFixture(t)
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "state"), 0700); err != nil {
		t.Fatal(err)
	}
	plan.BackupPolicy.ControllerDirectory = root
	sha := strings.Repeat("f", 64)
	runner := &schemaMaintenanceSchedulerRunner{plan: plan, sha: sha}
	runtime := &productionReleaseRuntime{runner: runner, now: func() time.Time { return time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC) }}
	return runtime, plan, sha, runner
}

func TestSchemaMaintenanceSchedulerSerializesCompleteTwoOriginPhase(t *testing.T) {
	runtime, plan, sha, runner := newSchemaMaintenanceSchedulerFixture(t)
	lock, err := acquireSchemaMaintenanceSchedulerLock(plan.BackupPolicy.ControllerDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.dispatchSchemaMaintenancePhase(context.Background(), plan, sha, "close"); err == nil || len(runner.commands) != 0 {
		t.Fatal("second dispatcher entered a held two-origin phase")
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	results, err := runtime.dispatchSchemaMaintenancePhase(context.Background(), plan, sha, "close")
	if err != nil || len(results) != 2 || len(runner.commands) != 2 {
		t.Fatalf("ordered close: results=%d commands=%d err=%v", len(results), len(runner.commands), err)
	}
	for i, command := range runner.commands {
		if command.Name != commandSSH || len(command.Args) != 12 || command.Args[10] != plan.Origins[i].Alias || !strings.Contains(command.Args[11], "'--action' 'close'") {
			t.Fatalf("unexpected fixed ordered native SSH command: %#v", command)
		}
		if !strings.Contains(strings.Join(command.Args, " "), "StrictHostKeyChecking=yes") || !strings.Contains(strings.Join(command.Args, " "), "ControlPath=none") {
			t.Fatal("scheduler dropped strict native SSH identity options")
		}
	}
	if _, err := runtime.dispatchSchemaMaintenancePhase(context.Background(), plan, sha, "close"); err == nil || len(runner.commands) != 2 {
		t.Fatal("repeated an already recorded mutation phase")
	}
}

func TestSchemaMaintenanceSchedulerLostReplyStopsRemoteProgressAndCannotRetry(t *testing.T) {
	runtime, plan, sha, runner := newSchemaMaintenanceSchedulerFixture(t)
	runner.failAt = 1
	if _, err := runtime.dispatchSchemaMaintenancePhase(context.Background(), plan, sha, "close"); err == nil || len(runner.commands) != 1 {
		t.Fatal("continued to another origin after uncertain first mutation")
	}
	runner.failAt = 0
	if _, err := runtime.dispatchSchemaMaintenancePhase(context.Background(), plan, sha, "close"); err == nil || len(runner.commands) != 1 {
		t.Fatal("retried an uncertain mutation using a fresh scheduler process")
	}
	if _, err := runtime.dispatchSchemaMaintenancePhase(context.Background(), plan, sha, "stop"); err == nil || len(runner.commands) != 1 {
		t.Fatal("started a different mutation while the prior daemon call remained uncertain")
	}
	if _, err := os.Stat(filepath.Join(plan.BackupPolicy.ControllerDirectory, "state", "dispatch-close.json")); err != nil {
		t.Fatal("lost original durable dispatch intent")
	}
	if _, err := runtime.dispatchSchemaMaintenancePhase(context.Background(), plan, sha, "arbitrary-shell"); err == nil || len(runner.commands) != 1 {
		t.Fatal("scheduler accepted arbitrary dispatch")
	}
}

func TestSchemaMaintenanceSchedulerLockRejectsSymlinkAndHardlink(t *testing.T) {
	_, plan, _, _ := newSchemaMaintenanceSchedulerFixture(t)
	state := filepath.Join(plan.BackupPolicy.ControllerDirectory, "state")
	path := filepath.Join(state, "schema-scheduler.lock")
	other := filepath.Join(state, "other")
	if err := os.WriteFile(other, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, path); err != nil {
		t.Fatal(err)
	}
	if lock, err := acquireSchemaMaintenanceSchedulerLock(plan.BackupPolicy.ControllerDirectory); err == nil {
		lock.Close()
		t.Fatal("followed scheduler lock symlink")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(other, path); err != nil {
		t.Fatal(err)
	}
	if lock, err := acquireSchemaMaintenanceSchedulerLock(plan.BackupPolicy.ControllerDirectory); err == nil {
		lock.Close()
		t.Fatal("accepted scheduler lock with another hardlink")
	}
}

func TestSchemaMaintenanceSchedulerRefusesMissingPriorCompletion(t *testing.T) {
	runtime, plan, sha, runner := newSchemaMaintenanceSchedulerFixture(t)
	if _, err := runtime.dispatchSchemaMaintenancePhase(context.Background(), plan, sha, "close"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(plan.BackupPolicy.ControllerDirectory, "state", "dispatch-close.complete.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.dispatchSchemaMaintenancePhase(context.Background(), plan, sha, "stop"); err == nil || len(runner.commands) != 2 {
		t.Fatal("trusted a prior attempt whose complete two-origin receipt disappeared")
	}
}
