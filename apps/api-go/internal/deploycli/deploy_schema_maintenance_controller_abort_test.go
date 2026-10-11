package deploycli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSchemaMaintenanceAbortFreezeIsIrreversibleForEveryNormalMutation(t *testing.T) {
	normal := []string{"close", "stop", "claim", "worker", "capture-outcome", "capture-backup-source", "backup-export", "capture-recovery-profile", "install-recovery-hook", "capture-recovery", "start-recovery", "capture-ready", "capture-startable", "release", "reopen", "finish", "check-schema-owner"}
	for _, kind := range []string{"in-memory", "regular", "directory", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			state := filepath.Join(root, "state")
			if err := os.Mkdir(state, 0700); err != nil {
				t.Fatal(err)
			}
			c := &productionSchemaMaintenanceController{origin: productionSchemaMaintenanceOriginPlan{Workspace: root}, abortFrozen: kind == "in-memory"}
			path := filepath.Join(state, "abort-prepared.json")
			var err error
			switch kind {
			case "regular":
				err = os.WriteFile(path, []byte("truncated"), 0600)
			case "directory":
				err = os.Mkdir(path, 0700)
			case "symlink":
				err = os.Symlink("absent", path)
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, action := range normal {
				if err := c.checkAbortActionAllowed(action); err == nil {
					t.Fatalf("frozen abort admitted normal %s", action)
				}
			}
			for _, action := range []string{"status", "capture-closed", "capture-stopped", "capture-abort-ready", "capture-abort-startable"} {
				if err := c.checkAbortActionAllowed(action); err != nil {
					t.Fatalf("read-only %s: %v", action, err)
				}
			}
		})
	}
}

func TestSchemaMaintenanceAbortHistoryRefusesEveryExistingUnsafeClaimOrWorkerPath(t *testing.T) {
	runtime, plan, _ := newSchemaMaintenancePlanFixture(t)
	raw, err := canonicalProductionSchemaMaintenancePlan(plan, runtime.paths)
	if err != nil {
		t.Fatal(err)
	}
	sha := startupContentSHA256(raw)
	for _, name := range schemaMaintenanceAbortForbiddenState {
		for _, kind := range []string{"regular", "directory", "dangling-symlink"} {
			t.Run(name+"/"+kind, func(t *testing.T) {
				workspace := t.TempDir()
				state := filepath.Join(workspace, "state")
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
				c := &productionSchemaMaintenanceController{plan: plan, planSHA: sha, origin: productionSchemaMaintenanceOriginPlan{Workspace: workspace}, daemonGeneration: schemaMaintenanceRPCBinding{PlanSHA256: sha, TransitionID: plan.TransitionID, DaemonPID: 123, DaemonInvocationID: strings.Repeat("a", 32), DaemonExeSHA256: plan.Worker.Provider.DeployEngineSHA256, DaemonStartTicks: 456}}
				if err := c.requireNeverClaimedHistory(); err == nil || !strings.Contains(err.Error(), "prior claim, worker or recovery") {
					t.Fatalf("unsafe existing %s was not rejected before native callbacks: %v", path, err)
				}
			})
		}
	}
}

func TestSchemaMaintenanceAbortHistoryNeverConvertsAcceptedOrUnknownClaimToNeverClaimed(t *testing.T) {
	for name, c := range map[string]*productionSchemaMaintenanceController{
		"accepted claim":                {fence: &productionSchemaMaintenanceFence{}},
		"uncertain claim":               {claimUncertain: true},
		"normal finished":               {finished: true},
		"new daemon without generation": {},
	} {
		t.Run(name, func(t *testing.T) {
			if err := c.requireNeverClaimedHistory(); err == nil {
				t.Fatal("claimed, unknown or new daemon accepted as never-claimed")
			}
		})
	}
}

func TestSchemaMaintenanceAbortWrapperRejectsAmbiguousNativeAuthority(t *testing.T) {
	valid := productionSchemaMaintenanceAbortCapture{AbortBindingSHA256: strings.Repeat("a", 64), PreparedSHA256: strings.Repeat("b", 64), Capture: productionSchemaMaintenanceHostCapture{Format: 1}}
	raw, _ := json.Marshal(valid)
	var actual productionSchemaMaintenanceAbortCapture
	if err := schemaMaintenanceAbortTypedResult(schemaMaintenanceRPCReply{Result: raw}, &actual); err != nil {
		t.Fatal(err)
	}
	for name, raw := range map[string]string{
		"extra authority": `{"abort_binding_sha256":"a","prepared_sha256":"b","capture":{},"never_claimed":true}`,
		"case alias":      `{"abort_binding_sha256":"a","prepared_sha256":"b","Capture":{}}`,
		"duplicate":       `{"abort_binding_sha256":"a","abort_binding_sha256":"b","prepared_sha256":"b","capture":{}}`,
		"trailing object": string(raw) + `{}`,
		"null":            `null`,
	} {
		t.Run(name, func(t *testing.T) {
			var result productionSchemaMaintenanceAbortCapture
			if err := schemaMaintenanceAbortTypedResult(schemaMaintenanceRPCReply{Result: json.RawMessage(raw)}, &result); err == nil {
				t.Fatal("ambiguous native wrapper accepted")
			}
		})
	}
}

func TestSchemaMaintenanceAbortNativeDaemonUnitRequiresItsOriginalGeneration(t *testing.T) {
	generation := schemaMaintenanceRPCBinding{PlanSHA256: strings.Repeat("a", 64), TransitionID: "abort-test", DaemonPID: 123, DaemonInvocationID: strings.Repeat("b", 32), DaemonExeSHA256: strings.Repeat("c", 64), DaemonStartTicks: 456}
	unit := map[string]string{"MainPID": "123", "ExecMainPID": "123", "InvocationID": generation.DaemonInvocationID, "Restart": "no", "ActiveState": "active", "SubState": "running"}
	if err := validateSchemaMaintenanceAbortDaemonUnit(generation, unit); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"MainPID", "ExecMainPID", "InvocationID", "Restart", "ActiveState", "SubState"} {
		t.Run(field, func(t *testing.T) {
			copy := make(map[string]string)
			for key, value := range unit {
				copy[key] = value
			}
			copy[field] = "different"
			if err := validateSchemaMaintenanceAbortDaemonUnit(generation, copy); err == nil {
				t.Fatal("changed original systemd holder accepted")
			}
		})
	}
}

func TestSchemaMaintenanceAbortCapturesPermitSequentialOldStartsAndRequireActualReopen(t *testing.T) {
	_, plan, _ := newSchemaMaintenancePlanFixture(t)
	sha := strings.Repeat("a", 64)
	c := &productionSchemaMaintenanceController{plan: plan, planSHA: sha}
	origin := plan.Origins[1]
	bound := productionSchemaMaintenanceAbortOrigin{Prepared: productionReleaseFilePlan{SHA256: strings.Repeat("b", 64)}}
	result := productionSchemaMaintenanceAbortCapture{AbortBindingSHA256: strings.Repeat("c", 64), PreparedSHA256: bound.Prepared.SHA256, Capture: productionSchemaMaintenanceHostCapture{
		Format: 1, TransitionID: plan.TransitionID, PlanSHA256: sha, Host: origin.Host, Phase: "WRITER_STOPPED", CapsuleSHA256: origin.OldCapsule.SHA256, ProviderSHA256: origin.Recovery.Candidate.PayloadSHA256,
		PID: 123, InvocationID: strings.Repeat("d", 32), NginxPID: 456, NginxInvocationID: strings.Repeat("e", 32), NginxGenerationSHA256: strings.Repeat("f", 64), OriginalAdmissionSHA256: strings.Repeat("1", 64), ClosedAdmissionSHA256: strings.Repeat("2", 64), StopStartedUTC: time.Now().UTC(), ShutdownJournalSHA256: strings.Repeat("3", 64),
	}}
	if err := c.validateAbortCapture(result, origin, bound, result.AbortBindingSHA256, false, false); err != nil {
		t.Fatal(err)
	}
	if err := c.validateAbortCapture(result, origin, bound, result.AbortBindingSHA256, true, false); err == nil {
		t.Fatal("stopped peer counted as ready")
	}
	result.Capture.Phase, result.Capture.ConfirmedPID, result.Capture.ConfirmedInvocationID = "ABORT_RUNNING", 789, strings.Repeat("4", 32)
	result.Capture.ConfirmedCapsuleSHA256, result.Capture.ConfirmedProviderSHA256 = origin.OldCapsule.SHA256, origin.Recovery.Candidate.PayloadSHA256
	for _, ready := range []bool{false, true} {
		if err := c.validateAbortCapture(result, origin, bound, result.AbortBindingSHA256, ready, false); err != nil {
			t.Fatalf("running first origin blocked sequential second start or ready: %v", err)
		}
	}
	if err := c.validateAbortCapture(result, origin, bound, result.AbortBindingSHA256, true, true); err == nil {
		t.Fatal("closed barrier counted as restored")
	}
	result.Capture.Phase = "ADMISSION_RESTORED"
	if err := c.validateAbortCapture(result, origin, bound, result.AbortBindingSHA256, true, true); err != nil {
		t.Fatal(err)
	}
	if err := c.validateAbortCapture(result, origin, bound, result.AbortBindingSHA256, false, false); err == nil {
		t.Fatal("reopened peer counted as safe to start another old writer")
	}
	for name, change := range map[string]func(*productionSchemaMaintenanceAbortCapture){
		"binding":      func(r *productionSchemaMaintenanceAbortCapture) { r.AbortBindingSHA256 = sha },
		"preparation":  func(r *productionSchemaMaintenanceAbortCapture) { r.PreparedSHA256 = sha },
		"host":         func(r *productionSchemaMaintenanceAbortCapture) { r.Capture.Host = "other" },
		"capsule":      func(r *productionSchemaMaintenanceAbortCapture) { r.Capture.ConfirmedCapsuleSHA256 = sha },
		"generation":   func(r *productionSchemaMaintenanceAbortCapture) { r.Capture.ConfirmedInvocationID = "missing" },
		"stop journal": func(r *productionSchemaMaintenanceAbortCapture) { r.Capture.ShutdownJournalSHA256 = "missing" },
	} {
		t.Run(name, func(t *testing.T) {
			copy := result
			change(&copy)
			if err := c.validateAbortCapture(copy, origin, bound, result.AbortBindingSHA256, true, true); err == nil {
				t.Fatal("changed native abort authority accepted")
			}
		})
	}
}
