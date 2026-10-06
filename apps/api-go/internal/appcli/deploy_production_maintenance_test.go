package appcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func maintenanceFixtureCache(t *testing.T) string {
	t.Helper()
	cache := filepath.Join(os.Getenv("HOME"), ".cache")
	if err := os.MkdirAll(cache, 0700); err != nil {
		t.Fatal(err)
	}
	return cache
}

func maintenanceBindingFixture(t *testing.T, stage string) (*productionRuntime, productionWorkspace, *productionMaintenanceHandoff) {
	t.Helper()
	root, err := os.MkdirTemp(maintenanceFixtureCache(t), "maintenance-binding-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	staging := filepath.Join(root, "staging")
	if err := os.Mkdir(staging, 0700); err != nil {
		t.Fatal(err)
	}
	prepared := productionMaintenancePrepareConfig{Format: "lmm-credit-transition-prepare-v1", TransitionID: "fixture", TransitionIntentSHA256: strings.Repeat("a", 64), ProviderSHA256: strings.Repeat("b", 64), TargetCreditsPerUSD: 500000, Database: map[string]any{"system_identifier": "123", "database": "fixture", "database_oid": float64(123), "schema": "public", "server_version_num": float64(170000), "database_user": "fixture"}, Options: map[string]string{"CreditsPerUSD": "400000", "LegacyPricingQuotaPerUnit": "400000", "QuotaPerUnit": "400000", "PublicCreditsPerUSD": "400000", "USDExchangeRate": "7.1"}}
	preparePath := filepath.Join(root, "prepare.json")
	content, _ := json.Marshal(prepared)
	if err := os.WriteFile(preparePath, content, 0600); err != nil {
		t.Fatal(err)
	}
	prepareSHA, _ := sha256File(preparePath)
	h := &productionMaintenanceHandoff{Format: productionMaintenanceHandoffFormat, Stage: stage, DeploymentTool: "native", TransitionID: prepared.TransitionID, TransitionIntentSHA256: prepared.TransitionIntentSHA256, ProviderSHA256: prepared.ProviderSHA256, PrepareConfigPath: preparePath, PrepareConfigSHA256: prepareSHA, GuardianSocket: filepath.Join(root, "guardian.sock")}
	path := filepath.Join(staging, "handoff.json")
	content, _ = json.Marshal(h)
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	digest, _ := sha256File(path)
	loaded, err := loadProductionMaintenanceHandoff(path, digest, uint32(os.Getuid()))
	if err != nil {
		t.Fatal(err)
	}
	return &productionRuntime{requiredOwnerUID: uint32(os.Getuid()), maintenanceHandoff: loaded}, productionWorkspace{root: root, stagingDir: staging}, loaded
}

func TestPostStagingIntentLoadsButEveryActivationRejectsBeforeMutation(t *testing.T) {
	_, _, h := maintenanceBindingFixture(t, "post")
	for _, action := range []string{"apply", "maintenance-retry", "maintenance-capture", "maintenance-close", "maintenance-stop", "confirm", "rollback", "maintenance-release"} {
		t.Run(action, func(t *testing.T) {
			f := newProductionFixture(t)
			f.runtime.maintenanceHandoff = h
			before, _ := os.ReadFile(filepath.Join(f.runtime.paths.TransactionLock, productionTransactionMarker))
			options := f.options
			options.Action = action
			if _, err := f.runtime.executeTransaction(context.Background(), options); err == nil || !strings.Contains(err.Error(), "staging intent") {
				t.Fatalf("err=%v", err)
			}
			after, _ := os.ReadFile(filepath.Join(f.runtime.paths.TransactionLock, productionTransactionMarker))
			if !bytes.Equal(before, after) || len(f.runner.events) != 0 {
				t.Fatalf("premature mutation events=%v", f.runner.events)
			}
			entries, _ := os.ReadDir(f.workspace.stateDir)
			if len(entries) != 0 {
				t.Fatalf("premature state mutation: %v", entries)
			}
		})
	}
}

func TestStoppedRefinementKeepsImmutablePostIntentAndRejectsChanges(t *testing.T) {
	for _, change := range []string{"", "provider", "config", "socket", "stage", "public", "base-bytes", "base-fields", "already-stopped"} {
		t.Run(change, func(t *testing.T) {
			runtime, workspace, base := maintenanceBindingFixture(t, "post")
			next := *base
			next.SHA256 = strings.Repeat("c", 64)
			next.PreviousDeploymentID = "bridge"
			next.StoppedWriter = &productionStoppedWriter{PID: 2147483647, InvocationID: strings.Repeat("a", 32)}
			switch change {
			case "provider":
				next.ProviderSHA256 = strings.Repeat("d", 64)
			case "config":
				next.PrepareConfigPath += ".other"
			case "socket":
				next.GuardianSocket += ".other"
			case "stage":
				next.Stage = "prebridge"
			case "public":
				next.PublicBaseURL = "https://other.invalid"
			case "base-bytes":
				os.WriteFile(base.Path, []byte("changed"), 0600)
			case "base-fields":
				base.ProbeTokenPath = "/other/token"
			case "already-stopped":
				base.StoppedWriter = next.StoppedWriter
			}
			before, _ := os.ReadFile(base.Path)
			err := runtime.verifyStoppedHandoffRefinement(workspace, base, &next)
			if change == "" && err != nil {
				t.Fatal(err)
			}
			if change != "" && err == nil {
				t.Fatalf("accepted %s", change)
			}
			after, _ := os.ReadFile(base.Path)
			if !reflect.DeepEqual(before, after) {
				t.Fatal("refinement rewrote immutable base intent")
			}
		})
	}
}

type maintenanceBodyRunner struct{ body []byte }

func (runner maintenanceBodyRunner) Run(context.Context, productionCommand) ([]byte, error) {
	return runner.body, nil
}

func TestNormalProductionProbesRejectMaintenanceEvenWhenReady(t *testing.T) {
	for _, body := range []string{
		`{"success":true,"ready":true,"live":true,"maintenance":true,"business_enabled":false,"data":{"version":"0.2.83"}}`,
		`{"success":true,"ready":true,"live":true,"business_enabled":false,"data":{"version":"0.2.83"}}`,
	} {
		runtime := &productionRuntime{runner: maintenanceBodyRunner{[]byte(body)}, paths: productionPaths{LocalBaseURL: "http://127.0.0.1:3000"}}
		if _, err := runtime.probeStatus(context.Background(), "/fixture/lmm-api", "http://127.0.0.1:3000", "0.2.83"); err == nil {
			t.Fatal("ordinary status accepted business-disabled maintenance")
		}
		if err := runtime.probeLive(context.Background(), "/fixture/lmm-api"); err == nil {
			t.Fatal("ordinary liveness accepted maintenance as normal release health")
		}
	}
}

func TestBoundMaintenanceProbesRequireEveryFrozenIdentity(t *testing.T) {
	h := &productionMaintenanceHandoff{TransitionID: "transition", TransitionIntentSHA256: strings.Repeat("a", 64), ProviderSHA256: strings.Repeat("b", 64), PrepareConfigSHA256: strings.Repeat("c", 64)}
	body := map[string]any{"success": true, "ready": true, "live": true, "maintenance": true, "business_enabled": false, "data": map[string]any{"version": "0.2.83", "credit_transition": map[string]any{"format": "lmm-credit-transition-prepare-v1", "transition_id": h.TransitionID, "transition_intent_sha256": h.TransitionIntentSHA256, "provider_sha256": h.ProviderSHA256, "prepare_config_sha256": h.PrepareConfigSHA256, "target_credits_per_usd": 500000}}}
	encode := func() []byte { content, _ := json.Marshal(body); return content }
	runtime := &productionRuntime{runner: maintenanceBodyRunner{encode()}, maintenanceHandoff: h, paths: productionPaths{LocalBaseURL: "http://127.0.0.1:3000"}}
	if err := runtime.probeBoundMaintenanceLocal(context.Background(), "/fixture/lmm-api", "0.2.83"); err != nil {
		t.Fatal(err)
	}
	binding := body["data"].(map[string]any)["credit_transition"].(map[string]any)
	for _, key := range []string{"transition_id", "transition_intent_sha256", "provider_sha256", "prepare_config_sha256"} {
		before := binding[key]
		binding[key] = "changed"
		runtime.runner = maintenanceBodyRunner{encode()}
		if err := runtime.probeBoundMaintenanceLocal(context.Background(), "/fixture/lmm-api", "0.2.83"); err == nil {
			t.Fatalf("accepted changed %s", key)
		}
		binding[key] = before
	}
	body["business_enabled"] = true
	runtime.runner = maintenanceBodyRunner{encode()}
	if err := runtime.probeBoundMaintenanceLocal(context.Background(), "/fixture/lmm-api", "0.2.83"); err == nil {
		t.Fatal("maintenance probe accepted enabled business")
	}
}

func TestMaintenanceApplyCannotBypassAllStoppedOrFrontendFreeze(t *testing.T) {
	fixture := newProductionFixture(t)
	fixture.runtime.maintenanceHandoff = &productionMaintenanceHandoff{Stage: "prebridge"}
	if _, err := fixture.runtime.apply(context.Background(), fixture.workspace, fixture.options); err == nil || !strings.Contains(err.Error(), "all-stopped") {
		t.Fatalf("err=%v", err)
	}
	fixture.runtime.maintenanceHandoff.StoppedWriter = &productionStoppedWriter{PID: 123}
	if _, err := fixture.runtime.apply(context.Background(), fixture.workspace, fixture.options); err == nil || !strings.Contains(err.Error(), "frontend") {
		t.Fatalf("err=%v", err)
	}
	if _, err := os.Lstat(fixture.workspace.manifestPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("rejected maintenance apply wrote manifest")
	}
	for _, event := range fixture.runner.events {
		if event == "systemd-stop" || strings.HasPrefix(event, "paru-") {
			t.Fatalf("rejected maintenance apply mutated owner: %s", event)
		}
	}
}

func TestMaintenanceShutdownUsesItsOwnRealEvidence(t *testing.T) {
	if err := validateMaintenanceShutdownJournal([]byte("refund_tasks execution_complete=true accepted=0 finished=0 active=0 failed=0\nserver exited")); err == nil {
		t.Fatal("ordinary refund report substituted for prepare shutdown")
	}
	if err := validateMaintenanceShutdownJournal([]byte("credit_transition_prepare shutdown_complete=true business_enabled=false\nserver exited")); err != nil {
		t.Fatal(err)
	}
	if err := validateMaintenanceShutdownJournal([]byte("credit_transition_prepare shutdown_complete=true business_enabled=false\npanic: failure\nserver exited")); err == nil {
		t.Fatal("panic accepted as clean prepare shutdown")
	}
}

func TestMaintenanceAdmissionBodySurvivesWorkspaceTransfer(t *testing.T) {
	runtime := &productionRuntime{maintenanceHandoff: &productionMaintenanceHandoff{TransitionID: "same-intent"}}
	original := []byte("location @lmm_api_backend { proxy_pass http://127.0.0.1:3000; }\n")
	capture, err := runtime.billingBarrier(original, "capture")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"prebridge", "post"} {
		barrier, err := runtime.billingBarrier(original, id)
		if err != nil || !bytes.Equal(capture, barrier) {
			t.Fatalf("unstable %s barrier: %s %v", id, barrier, err)
		}
	}
	if !bytes.Contains(capture, []byte("lmm-credit-transition:same-intent")) {
		t.Fatal("barrier is not bound to stable transition")
	}
}

func TestMaintenancePrearmFailureKeepsItsTransaction(t *testing.T) {
	f := newProductionFixture(t)
	f.options.WebChanged = false
	f.runtime.maintenanceHandoff = &productionMaintenanceHandoff{Stage: "prebridge", TransitionID: "transition", TransitionIntentSHA256: strings.Repeat("a", 64), ProviderSHA256: strings.Repeat("b", 64), StoppedWriter: &productionStoppedWriter{PID: 999999}, PrepareConfigPath: "/missing/sealed-config"}
	if _, err := f.runtime.apply(context.Background(), f.workspace, f.options); err == nil {
		t.Fatal("missing service-readable plan accepted")
	}
	status, err := f.runtime.readStatus(f.workspace)
	if err != nil || status.Phase != "MAINTENANCE_PREARM_FAILED" || status.ProviderSHA256 != f.runtime.maintenanceHandoff.ProviderSHA256 {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	if err := f.runtime.validateTransactionLock(f.workspace); err != nil {
		t.Fatalf("prearm failure abandoned stopped owner: %v", err)
	}
	for _, event := range f.runner.events {
		if event == "systemd-stop" || strings.HasPrefix(event, "paru-") {
			t.Fatalf("prearm failure mutated: %s", event)
		}
	}
}

func TestMaintenanceReaderRejectsRootOnlyAndUnreadableParents(t *testing.T) {
	root, err := os.MkdirTemp(maintenanceFixtureCache(t), "maintenance-reader-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	if err := os.Chmod(root, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "plan.json")
	if err := os.WriteFile(path, []byte("sealed"), 0600); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	_, gid, _ := maintenanceFileIDs(info)
	if gid == 0 {
		t.Skip("service reader test requires a non-root fixture group")
	}
	if err := validateMaintenanceServiceReaderGIDToRoot(path, gid, root); err == nil {
		t.Fatal("root-only plan accepted for DynamicUser")
	}
	if err := os.Chmod(path, 0640); err != nil {
		t.Fatal(err)
	}
	if err := validateMaintenanceServiceReaderGIDToRoot(path, gid, root); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := validateMaintenanceServiceReaderGIDToRoot(path, gid, root); err == nil {
		t.Fatal("untraversable plan directory accepted")
	}
}

func TestMaintenanceControllerResultMatchesRunnerContract(t *testing.T) {
	h := &productionMaintenanceHandoff{Stage: "post", TransitionID: "transition", TransitionIntentSHA256: strings.Repeat("a", 64), ProviderSHA256: strings.Repeat("b", 64)}
	result := releaseControllerResult(productionReleasePlan{MaintenanceHandoff: h}, productionReleaseControllerState{Phase: productionMaintenanceConfirmedPhase, MaintenanceConfirmation: true})
	if result.Phase != productionMaintenanceConfirmedPhase || result.Status != result.Phase || result.ProviderSHA256 != h.ProviderSHA256 || result.TransitionIntentSHA256 != h.TransitionIntentSHA256 || !result.MaintenanceConfirmation {
		t.Fatalf("result=%+v", result)
	}
}

type maintenanceDispatchStateRunner struct {
	fallback productionCommandRunner
	load     string
}

func (runner maintenanceDispatchStateRunner) Run(ctx context.Context, command productionCommand) ([]byte, error) {
	if command.Name == commandSystemctl && len(command.Args) > 1 && command.Args[0] == "show" && command.Args[1] == "--property=LoadState" {
		return []byte(runner.load), nil
	}
	return runner.fallback.Run(ctx, command)
}

func TestMaintenanceStatusProvesAbsentDispatchAndStagedArtifacts(t *testing.T) {
	for _, failure := range []string{"", "unit", "manifest", "attempt", "package", "plan"} {
		t.Run(failure, func(t *testing.T) {
			f := newProductionFixture(t)
			h := &productionMaintenanceHandoff{Format: productionMaintenanceHandoffFormat, Stage: "prebridge", DeploymentTool: "native", TransitionID: "transition", TransitionIntentSHA256: strings.Repeat("a", 64), ProviderSHA256: f.options.ProbeBinarySHA256, SHA256: strings.Repeat("b", 64), Path: "/sealed/handoff.json"}
			f.runtime.maintenanceHandoff = h
			f.runtime.guardianLease = io.NopCloser(strings.NewReader(""))
			load := "not-found"
			if failure == "unit" {
				load = "loaded"
			}
			f.runtime.runner = maintenanceDispatchStateRunner{f.runner, load}
			plan := testProductionReleasePlan(t, t.TempDir())
			plan.DeploymentID = f.workspace.id
			plan.MaintenanceHandoff = h
			plan.GoCandidate.PackagePath = f.options.GoPackage
			plan.GoCandidate.PackageSHA256 = f.options.GoPackageSHA256
			plan.GoCandidate.PayloadSHA256 = h.ProviderSHA256
			plan.GoRollback.PackagePath = f.options.GoRollbackPackage
			plan.GoRollback.PackageSHA256 = f.options.GoRollbackSHA256
			plan.WebCandidate.PackagePath = f.options.WebPackage
			plan.WebCandidate.PackageSHA256 = f.options.WebPackageSHA256
			plan.WebRollback = plan.WebCandidate
			plan.ProbeBinary.SHA256 = h.ProviderSHA256
			plan.OperatorBinary = plan.ProbeBinary
			content, err := canonicalProductionReleasePlan(plan)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(f.workspace.stagingDir, productionReleasePlanFilename)
			if err := os.WriteFile(path, content, 0600); err != nil {
				t.Fatal(err)
			}
			digest, err := sha256File(path)
			if err != nil {
				t.Fatal(err)
			}
			switch failure {
			case "manifest":
				os.WriteFile(f.workspace.manifestPath, []byte("intent"), 0600)
			case "attempt":
				os.Mkdir(filepath.Join(f.workspace.stateDir, "maintenance-failed-attempt-old"), 0700)
			case "package":
				os.WriteFile(f.options.GoPackage, []byte("tampered"), 0700)
			case "plan":
				os.WriteFile(path, append(content, '\n'), 0600)
			}
			status, err := f.runtime.maintenanceUndispatchedStatus(context.Background(), f.workspace, productionTransactionOptions{StagedPlanPath: path, StagedPlanSHA256: digest})
			if failure != "" {
				if err == nil {
					t.Fatalf("accepted %s dispatch ambiguity", failure)
				}
				return
			}
			if err != nil || status.Phase != "NOT_DISPATCHED" || !status.DispatchVerifiedAbsent || status.PlanSHA256 != digest || status.HandoffSHA256 != h.SHA256 {
				t.Fatalf("status=%+v err=%v", status, err)
			}
			if _, err := os.Lstat(f.workspace.statusPath); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("readonly absent status wrote state")
			}
		})
	}
}
