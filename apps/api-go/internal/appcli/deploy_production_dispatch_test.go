package appcli

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

type productionDispatchStatusRunner struct {
	statuses []productionStatus
	calls    int
}

func (runner *productionDispatchStatusRunner) Run(_ context.Context, command productionCommand) ([]byte, error) {
	if command.Name != commandSSH || len(command.Args) < 7 {
		return nil, errors.New("unexpected status test command")
	}
	remote := command.Args[3:]
	if len(remote) >= 4 && remote[1] == "deploy" && remote[2] == "production" && remote[3] == "status" {
		index := runner.calls
		if index >= len(runner.statuses) {
			index = len(runner.statuses) - 1
		}
		runner.calls++
		return json.Marshal(runner.statuses[index])
	}
	return nil, errors.New("unexpected status test remote command")
}

type productionDispatchFaultRunner struct {
	evidence       productionDispatchEvidence
	statusPhase    string
	dispatchCalls  int
	evidenceCalls  int
	statusCalls    int
	secondAccepted bool
}

func (runner *productionDispatchFaultRunner) Run(_ context.Context, command productionCommand) ([]byte, error) {
	if command.Name != commandSSH || len(command.Args) < 4 {
		return nil, errors.New("unexpected dispatch test command")
	}
	remote := command.Args[3:]
	if remote[0] == "systemd-run" {
		runner.dispatchCalls++
		if runner.dispatchCalls == 1 || !runner.secondAccepted {
			return nil, errors.New("injected SSH transport failure")
		}
		return []byte("accepted\n"), nil
	}
	if len(remote) >= 4 && remote[1] == "deploy" && remote[2] == "production" && remote[3] == "dispatch-evidence" {
		runner.evidenceCalls++
		encoded, err := json.Marshal(runner.evidence)
		return append(encoded, '\n'), err
	}
	if len(remote) >= 4 && remote[1] == "deploy" && remote[2] == "production" && remote[3] == "status" && runner.statusPhase != "" {
		runner.statusCalls++
		return json.Marshal(productionStatus{Phase: runner.statusPhase})
	}
	return nil, errors.New("unexpected remote dispatch test command")
}

func TestAwaitRemoteReleaseStatusWaitsForTerminalPhase(t *testing.T) {
	const deploymentID = "prod-20260824T115800Z-status-fixture"
	controllerWorkspace := t.TempDir()
	plan := productionReleasePlan{
		Format: productionReleasePlanFormat, DeploymentID: deploymentID,
		ControllerWorkspace: controllerWorkspace, TargetAlias: productionTargetAlias,
		ProbeBinary:   productionReleaseFilePlan{Path: filepath.Join(controllerWorkspace, "lmm-api")},
		ManualConfirm: true,
	}
	state := productionReleaseControllerState{
		Format: productionReleaseStateFormat, DeploymentID: deploymentID,
		PlanSHA256: strings.Repeat("c", 64), Phase: productionReleasePhaseActivationDispatched,
		RemoteWorkspace: filepath.Join(defaultProductionPaths().WorkRoot, deploymentID),
		ActivationUnit:  productionActivationUnit(deploymentID), DispatchAttempts: 1,
		UpdatedUTC: time.Date(2026, 8, 24, 11, 58, 0, 0, time.UTC),
	}
	runner := &productionDispatchStatusRunner{statuses: []productionStatus{
		{Phase: "PREPARING"},
		{Phase: "DEPLOYING_GO"},
		{Phase: "AWAITING_CONFIRMATION"},
	}}
	waits := 0
	runtime := &productionReleaseRuntime{
		runner: runner,
		now:    func() time.Time { return time.Date(2026, 8, 24, 11, 59, 0, 0, time.UTC) },
		wait:   func(context.Context, time.Duration) error { waits++; return nil },
	}
	status, err := runtime.awaitRemoteReleaseStatus(context.Background(), plan, &state)
	if err != nil {
		t.Fatal(err)
	}
	if status.Phase != "AWAITING_CONFIRMATION" || runner.calls != 3 || waits != 2 {
		t.Fatalf("status=%#v calls=%d waits=%d", status, runner.calls, waits)
	}
}

func TestAwaitRemoteReleaseStatusContinuesAutoConfirmObservation(t *testing.T) {
	const deploymentID = "prod-20260824T115815Z-auto-confirm-fixture"
	controllerWorkspace := t.TempDir()
	plan := productionReleasePlan{
		Format: productionReleasePlanFormat, DeploymentID: deploymentID,
		ControllerWorkspace: controllerWorkspace, TargetAlias: productionTargetAlias,
		ProbeBinary:   productionReleaseFilePlan{Path: filepath.Join(controllerWorkspace, "lmm-api")},
		ManualConfirm: false,
	}
	state := productionReleaseControllerState{
		Format: productionReleaseStateFormat, DeploymentID: deploymentID,
		PlanSHA256: strings.Repeat("f", 64), Phase: productionReleasePhaseActivationDispatched,
		RemoteWorkspace: filepath.Join(defaultProductionPaths().WorkRoot, deploymentID),
		ActivationUnit:  productionActivationUnit(deploymentID), DispatchAttempts: 1,
		UpdatedUTC: time.Date(2026, 8, 24, 11, 58, 15, 0, time.UTC),
	}
	runner := &productionDispatchStatusRunner{statuses: []productionStatus{
		{Phase: "AWAITING_CONFIRMATION", AutoConfirm: true},
		{Phase: "AWAITING_CONFIRMATION", AutoConfirm: true},
		{Phase: "CONFIRMED", AutoConfirm: true},
	}}
	waits := 0
	runtime := &productionReleaseRuntime{
		runner: runner,
		now:    func() time.Time { return time.Date(2026, 8, 24, 11, 59, 15, 0, time.UTC) },
		wait:   func(context.Context, time.Duration) error { waits++; return nil },
	}
	status, err := runtime.awaitRemoteReleaseStatus(context.Background(), plan, &state)
	if err != nil {
		t.Fatal(err)
	}
	if status.Phase != "CONFIRMED" || runner.calls != 3 || waits != 2 {
		t.Fatalf("status=%#v calls=%d waits=%d", status, runner.calls, waits)
	}
}

func TestTerminalRemotePhasesAreValidControllerStatePhases(t *testing.T) {
	const deploymentID = "prod-20260824T115825Z-phase-contract"
	planSHA256 := strings.Repeat("e", 64)
	plan := productionReleasePlan{
		Format: productionReleasePlanFormat, DeploymentID: deploymentID,
		ControllerWorkspace: t.TempDir(),
	}
	for _, phase := range []string{"AWAITING_CONFIRMATION", "CONFIRMED", "ROLLED_BACK", "FAILED_PREARM", "ROLLBACK_FAILED", "ABORTED"} {
		t.Run(phase, func(t *testing.T) {
			if !productionActivationStatusTerminal(phase) {
				t.Fatalf("terminal phase %s is not recognized", phase)
			}
			state := productionReleaseControllerState{
				Format: productionReleaseStateFormat, DeploymentID: deploymentID,
				PlanSHA256: planSHA256, Phase: phase,
				RemoteWorkspace: filepath.Join(defaultProductionPaths().WorkRoot, deploymentID),
				UpdatedUTC:      time.Date(2026, 8, 24, 11, 58, 25, 0, time.UTC),
			}
			if err := validateProductionReleaseControllerState(plan, planSHA256, state); err != nil {
				t.Fatalf("terminal phase %s violates the persisted phase contract: %v", phase, err)
			}
		})
	}
}

func TestAmbiguousDispatchRemoteAbortedPersistsAcrossStatusAndReload(t *testing.T) {
	const deploymentID = "prod-20260824T115850Z-aborted-fixture"
	controllerWorkspace := t.TempDir()
	planSHA256 := strings.Repeat("d", 64)
	plan := productionReleasePlan{
		Format: productionReleasePlanFormat, DeploymentID: deploymentID,
		ControllerWorkspace: controllerWorkspace, TargetAlias: productionTargetAlias,
		ExpectedVersion: "0.1.59",
		ProbeBinary:     productionReleaseFilePlan{Path: filepath.Join(controllerWorkspace, "lmm-api")},
	}
	state := productionReleaseControllerState{
		Format: productionReleaseStateFormat, DeploymentID: deploymentID,
		PlanSHA256: planSHA256, Phase: productionReleasePhaseStaged,
		RemoteWorkspace: filepath.Join(defaultProductionPaths().WorkRoot, deploymentID),
		UpdatedUTC:      time.Date(2026, 8, 24, 11, 58, 30, 0, time.UTC),
	}
	runner := &productionDispatchFaultRunner{
		evidence: productionDispatchEvidence{
			Format: 1, DeploymentID: deploymentID, Unit: productionActivationUnit(deploymentID),
			UnitLoadState: "not-found", StatusPresent: true,
		},
		statusPhase: "ABORTED",
	}
	now := time.Date(2026, 8, 24, 11, 58, 31, 0, time.UTC)
	runtime := &productionReleaseRuntime{runner: runner, now: func() time.Time { return now }}
	if err := runtime.dispatchProductionActivation(context.Background(), plan, &state); err != nil {
		t.Fatal(err)
	}
	status, err := runtime.awaitRemoteReleaseStatus(context.Background(), plan, &state)
	if err != nil {
		t.Fatal(err)
	}
	if status.Phase != "ABORTED" || runner.dispatchCalls != 1 || runner.statusCalls != 1 {
		t.Fatalf("status=%#v dispatches=%d status_calls=%d", status, runner.dispatchCalls, runner.statusCalls)
	}
	if err := persistRemoteReleaseControllerStatus(plan, &state, status, now); err != nil {
		t.Fatal(err)
	}
	if result := releaseControllerResult(plan, state); result.Status != "ABORTED" {
		t.Fatalf("controller result=%#v", result)
	}
	reloaded, exists, err := loadProductionReleaseControllerState(plan, planSHA256)
	if err != nil || !exists {
		t.Fatalf("reload aborted state: exists=%t err=%v", exists, err)
	}
	if reloaded.Phase != "ABORTED" || reloaded.DispatchAttempts != 1 || !reloaded.DispatchObserved {
		t.Fatalf("reloaded state=%#v", reloaded)
	}
}

func TestLoadProductionReleaseControllerStateMigratesLegacyDispatch(t *testing.T) {
	const deploymentID = "prod-20260824T115900Z-legacy-dispatch"
	controllerWorkspace := t.TempDir()
	planSHA256 := strings.Repeat("b", 64)
	plan := productionReleasePlan{
		Format: productionReleasePlanFormat, DeploymentID: deploymentID,
		ControllerWorkspace: controllerWorkspace,
	}
	legacy := productionReleaseControllerState{
		Format: 1, DeploymentID: deploymentID, PlanSHA256: planSHA256,
		Phase:           productionReleasePhaseActivationDispatched,
		RemoteWorkspace: filepath.Join(defaultProductionPaths().WorkRoot, deploymentID),
		UpdatedUTC:      time.Date(2026, 8, 24, 11, 59, 0, 0, time.UTC),
	}
	raw, err := canonicalProductionReleaseControllerState(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(controllerWorkspace, productionReleaseStateFilename), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	migrated, exists, err := loadProductionReleaseControllerState(plan, planSHA256)
	if err != nil || !exists {
		t.Fatalf("load legacy state: exists=%t err=%v", exists, err)
	}
	if migrated.Format != productionReleaseStateFormat || migrated.DispatchAttempts != 1 ||
		migrated.ActivationUnit != productionActivationUnit(deploymentID) || migrated.DispatchObserved {
		t.Fatalf("migrated state=%#v", migrated)
	}
	persisted, err := os.ReadFile(filepath.Join(controllerWorkspace, productionReleaseStateFilename))
	if err != nil || !strings.Contains(string(persisted), `"dispatch_attempts": 1`) {
		t.Fatalf("persisted migrated state=%s err=%v", persisted, err)
	}
}

func TestProductionActivationDispatchFaultReconciliation(t *testing.T) {
	const deploymentID = "prod-20260824T120000Z-dispatch-fixture"
	unit := productionActivationUnit(deploymentID)
	cases := []struct {
		name           string
		evidence       productionDispatchEvidence
		secondAccepted bool
		wantDispatches int
		wantObserved   bool
		wantAttempts   int
	}{
		{
			name: "failure before SSH acceptance redispatches exactly once",
			evidence: productionDispatchEvidence{
				Format: 1, DeploymentID: deploymentID, Unit: unit, UnitLoadState: "not-found",
			},
			secondAccepted: true, wantDispatches: 2, wantObserved: true, wantAttempts: 2,
		},
		{
			name: "failure after unit creation only observes existing job",
			evidence: productionDispatchEvidence{
				Format: 1, DeploymentID: deploymentID, Unit: unit, UnitLoadState: "loaded", UnitPresent: true,
			},
			wantDispatches: 1, wantObserved: true, wantAttempts: 1,
		},
		{
			name: "result transport failure with status only observes existing job",
			evidence: productionDispatchEvidence{
				Format: 1, DeploymentID: deploymentID, Unit: unit, UnitLoadState: "not-found", StatusPresent: true,
			},
			wantDispatches: 1, wantObserved: true, wantAttempts: 1,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			controllerWorkspace := t.TempDir()
			planSHA256 := strings.Repeat("a", 64)
			plan := productionReleasePlan{
				Format: productionReleasePlanFormat, DeploymentID: deploymentID,
				ControllerWorkspace: controllerWorkspace, TargetAlias: productionTargetAlias,
				ProbeBinary: productionReleaseFilePlan{Path: filepath.Join(controllerWorkspace, "lmm-api")},
			}
			state := productionReleaseControllerState{
				Format: productionReleaseStateFormat, DeploymentID: deploymentID,
				PlanSHA256: planSHA256, Phase: productionReleasePhaseStaged,
				RemoteWorkspace: filepath.Join(defaultProductionPaths().WorkRoot, deploymentID),
				UpdatedUTC:      time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC),
			}
			runner := &productionDispatchFaultRunner{evidence: test.evidence, secondAccepted: test.secondAccepted}
			runtime := &productionReleaseRuntime{runner: runner, now: func() time.Time { return time.Date(2026, 8, 24, 12, 1, 0, 0, time.UTC) }}
			if err := runtime.dispatchProductionActivation(context.Background(), plan, &state); err != nil {
				t.Fatal(err)
			}
			if runner.dispatchCalls != test.wantDispatches || state.DispatchAttempts != test.wantAttempts || state.DispatchObserved != test.wantObserved {
				t.Fatalf("dispatches=%d attempts=%d observed=%t state=%#v", runner.dispatchCalls, state.DispatchAttempts, state.DispatchObserved, state)
			}
			persisted, exists, err := loadProductionReleaseControllerState(plan, planSHA256)
			if err != nil || !exists {
				t.Fatalf("load persisted dispatch state: exists=%t err=%v", exists, err)
			}
			if persisted.ActivationUnit != unit || persisted.DispatchAttempts != test.wantAttempts || persisted.DispatchObserved != test.wantObserved {
				t.Fatalf("persisted state=%#v", persisted)
			}
		})
	}
}
