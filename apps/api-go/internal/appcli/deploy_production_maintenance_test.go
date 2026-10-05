package appcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
	root, err := os.MkdirTemp(filepath.Join(os.Getenv("HOME"), ".cache"), "maintenance-reader-")
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
