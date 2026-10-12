//go:build linux

package deploycli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Actual private files and native canonical record formats, with synthetic
// daemon/capsule/owner identities. No PG, signature, process or systemd
// qualification is claimed; a command in this pure guard is a test failure.
type schemaPreExecAdmissionFixture struct {
	d       *productionSchemaMaintenanceDeadHolderController
	binding productionSchemaMaintenanceDeadHolderBinding
	start   productionSchemaMaintenanceDeadHolderStart
	started productionSchemaMaintenanceHostCapture
	restore productionSchemaMaintenanceHostCapture
	reopen  schemaPreExecAdmissionReopen
}
type schemaPreExecAdmissionReopen struct {
	Format        int    `json:"format"`
	TransitionID  string `json:"transition_id"`
	PlanSHA256    string `json:"plan_sha256"`
	Host          string `json:"host"`
	CapsulePath   string `json:"capsule_path"`
	CapsuleSHA256 string `json:"capsule_sha256"`
}

func (f *schemaPreExecAdmissionFixture) write(t *testing.T, name string, value any) []byte {
	t.Helper()
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, '\n')
	schemaMaintenanceRecoveryWriteFile(t, filepath.Join(f.d.base.origin.Workspace, "state", name), raw)
	return raw
}
func newSchemaPreExecAdmissionFixture(t *testing.T, index int) *schemaPreExecAdmissionFixture {
	t.Helper()
	runtime, plan, _ := newSchemaMaintenancePlanFixture(t)
	runtime.runner = existingSchemaTestRunner{run: func(productionCommand) ([]byte, error) {
		t.Fatal("pure admission guard executed an external command")
		return nil, nil
	}}
	sha, ownerSHA, engine := strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("e", 64)
	ref := &productionReleaseFilePlan{Path: filepath.Join(plan.Origins[index].Workspace, "state", "preexec-recovery-plan.json"), SHA256: strings.Repeat("f", 64)}
	binding := productionSchemaMaintenanceDeadHolderBinding{Format: "lmm-schema-maintenance-dead-holder-binding-v1", TransitionID: plan.TransitionID, PrePlanSHA256: sha, Mode: schemaMaintenancePreExecRejectedMode, OwnerMode: schemaMaintenanceDeadPreWorkerExactOwner, OriginalOwnerSHA256: ownerSHA, PreExecRecovery: ref}
	for i, origin := range plan.Origins {
		original := schemaMaintenanceRPCBinding{TransitionID: plan.TransitionID, PlanSHA256: sha, DaemonPID: 1000 + i, DaemonInvocationID: strings.Repeat(string(rune('a'+i)), 32), DaemonExeSHA256: plan.Worker.Provider.DeployEngineSHA256, DaemonStartTicks: uint64(100 + i)}
		recoverer := original
		recoverer.DaemonPID, recoverer.DaemonStartTicks, recoverer.DaemonInvocationID, recoverer.DaemonExeSHA256 = 2000+i, uint64(200+i), strings.Repeat(string(rune('c'+i)), 32), engine
		file := func(name string) productionReleaseFilePlan {
			return productionReleaseFilePlan{Path: filepath.Join(origin.Workspace, "state", name), SHA256: strings.Repeat("d", 64)}
		}
		binding.Origins = append(binding.Origins, productionSchemaMaintenanceDeadHolderPrepared{Format: "lmm-schema-maintenance-dead-holder-prepared-v1", TransitionID: plan.TransitionID, PrePlanSHA256: sha, Mode: schemaMaintenancePreExecRejectedMode, Host: origin.Host, OwnerMode: schemaMaintenanceDeadPreWorkerExactOwner, OriginalOwnerSHA256: ownerSHA, OriginalDaemon: original, RecoveryDaemon: recoverer, OriginalDaemonFile: file("daemon-binding.json"), GuardianAdopted: file("guardian-adopted.json"), GuardianBinding: file("guardian-binding.json"), OldCapsule: origin.OldCapsule, StoppedCapture: file("writer-stopped.json"), PreExecRecovery: ref})
	}
	origin, local := plan.Origins[index], binding.Origins[index]
	controller := &productionSchemaMaintenanceController{runtime: runtime, plan: plan, planSHA: sha, origin: origin}
	controller.host = &productionSchemaMaintenanceHost{runtime: runtime, binding: productionSchemaMaintenanceHostBinding{TransitionID: plan.TransitionID, PlanSHA256: sha, Host: origin.Host, CapsuleSHA256: origin.OldCapsule.SHA256, Workspace: origin.Workspace}}
	d := &productionSchemaMaintenanceDeadHolderController{base: controller, original: local.OriginalDaemon, generation: local.RecoveryDaemon, ownerMode: schemaMaintenanceDeadPreWorkerExactOwner, ownerSHA: ownerSHA, preExecRecovery: ref, recoveryEngineSHA256: engine}
	f := &schemaPreExecAdmissionFixture{d: d, binding: binding}
	bindingRaw := f.write(t, "dead-holder-binding.json", binding)
	at := time.Date(2026, 10, 12, 8, 0, 0, 0, time.UTC)
	stopped := productionSchemaMaintenanceHostCapture{Format: 1, TransitionID: plan.TransitionID, PlanSHA256: sha, Host: origin.Host, Phase: "WRITER_STOPPED", CapsuleSHA256: origin.OldCapsule.SHA256, ProviderSHA256: origin.Recovery.Candidate.PayloadSHA256, PID: 3000 + index, InvocationID: strings.Repeat("e", 32), OriginalAdmissionSHA256: strings.Repeat("a", 64), ClosedAdmissionSHA256: strings.Repeat("b", 64), StopStartedUTC: at, ShutdownJournalSHA256: strings.Repeat("c", 64)}
	f.write(t, "writer-stopped.json", stopped)
	f.start = productionSchemaMaintenanceDeadHolderStart{Format: "lmm-schema-maintenance-dead-holder-start-v1", BindingSHA256: startupContentSHA256(bindingRaw), Host: origin.Host, OldCapsule: origin.OldCapsule, Stopped: stopped, StartedUTC: at.Add(time.Second)}
	f.write(t, "dead-holder-start-intent.json", f.start)
	f.started = stopped
	f.started.Phase, f.started.ConfirmedPID, f.started.ConfirmedInvocationID = "DEAD_PREWORKER_RUNNING", 4000+index, strings.Repeat("f", 32)
	f.started.ConfirmedCapsuleSHA256, f.started.ConfirmedProviderSHA256 = origin.OldCapsule.SHA256, origin.Recovery.Candidate.PayloadSHA256
	f.write(t, "dead-holder-started.json", f.started)
	primary := binding.Origins[0]
	result := productionSchemaMaintenanceDeadPreWorkerCASResult{Format: "lmm-schema-maintenance-dead-preworker-cas-v1", TransitionID: plan.TransitionID, PrePlanSHA256: sha, Mode: schemaMaintenanceDeadPreWorkerExactOwner, OriginalDaemon: primary.OriginalDaemon, OriginalOwnerSHA256: ownerSHA, OldCapsule: primary.OldCapsule, PreSchema: plan.PreSchema, ProviderSHA256: plan.Worker.Provider.PayloadSHA256, RecovererEXESHA256: engine, RecovererPID: primary.RecoveryDaemon.DaemonPID, RecovererInvocationID: primary.RecoveryDaemon.DaemonInvocationID, StartedUTC: at.Add(-2 * time.Second), Outcome: "CAS_INTENT"}
	intentRaw := f.write(t, "dead-preworker-cas-intent.json", result)
	result.IntentSHA256, result.FinishedUTC, result.Outcome = startupContentSHA256(intentRaw), at.Add(-time.Second), "EXACT_OWNER_RELEASED"
	f.write(t, "dead-preworker-cas-result.json", result)
	f.reopen = schemaPreExecAdmissionReopen{1, plan.TransitionID, sha, origin.Host, origin.OldCapsule.Path, origin.OldCapsule.SHA256}
	f.restore = f.started
	f.restore.Phase = "ADMISSION_RESTORED"
	// Native restoreAdmission uses close-intent, so these two fields are zero.
	f.restore.StopStartedUTC, f.restore.ShutdownJournalSHA256 = time.Time{}, ""
	return f
}
func TestSchemaMaintenancePreExecAdapterPreparedAuthorityIsDistinct(t *testing.T) {
	f := newSchemaPreExecAdmissionFixture(t, 0)
	d, origin := f.d, f.d.base.origin
	good := f.binding.Origins[0]
	check := func(p productionSchemaMaintenanceDeadHolderPrepared) error {
		return validateSchemaMaintenanceDeadHolderPreparedForRecovery(p, d.base.plan, d.base.planSHA, origin, d.mode(), d.preExecRecovery, d.engineSHA256())
	}
	if err := check(good); err != nil {
		t.Fatal("explicit separate signed-engine authority rejected:", err)
	}
	if err := validateSchemaMaintenanceDeadHolderPrepared(good, d.base.plan, d.base.planSHA, origin); err == nil {
		t.Fatal("legacy never-worker validator accepted a newer pre-exec recovery authority")
	}
	for name, mutate := range map[string]func(*productionSchemaMaintenanceDeadHolderPrepared){
		"legacy mode": func(p *productionSchemaMaintenanceDeadHolderPrepared) { p.Mode = schemaMaintenanceDeadHolderMode },
		"missing ref": func(p *productionSchemaMaintenanceDeadHolderPrepared) { p.PreExecRecovery = nil },
		"foreign ref SHA": func(p *productionSchemaMaintenanceDeadHolderPrepared) {
			r := *p.PreExecRecovery
			r.SHA256 = strings.Repeat("0", 64)
			p.PreExecRecovery = &r
		},
		"foreign ref path": func(p *productionSchemaMaintenanceDeadHolderPrepared) {
			r := *p.PreExecRecovery
			r.Path += ".other"
			p.PreExecRecovery = &r
		},
		"old recoverer engine": func(p *productionSchemaMaintenanceDeadHolderPrepared) {
			p.RecoveryDaemon.DaemonExeSHA256 = d.base.plan.Worker.Provider.DeployEngineSHA256
		},
		"new original engine": func(p *productionSchemaMaintenanceDeadHolderPrepared) {
			p.OriginalDaemon.DaemonExeSHA256 = d.engineSHA256()
		},
		"swapped generations": func(p *productionSchemaMaintenanceDeadHolderPrepared) {
			p.OriginalDaemon, p.RecoveryDaemon = p.RecoveryDaemon, p.OriginalDaemon
		},
	} {
		t.Run(name, func(t *testing.T) {
			p := good
			mutate(&p)
			if check(p) == nil {
				t.Fatal("cross-mode authority accepted")
			}
		})
	}
}
func TestSchemaMaintenancePreExecAdapterOwnAdmissionProgressStaysReadable(t *testing.T) {
	for index := range 2 {
		t.Run(merchantStartupOrigins[index].Host, func(t *testing.T) {
			f := newSchemaPreExecAdmissionFixture(t, index)
			if err := f.d.checkPreExecAdmissionState(); err != nil {
				t.Fatal("initial absence:", err)
			}
			f.write(t, "reopen-intent.json", f.reopen)
			if err := f.d.checkPreExecAdmissionState(); err != nil {
				t.Fatal("own intent before restored self-locked:", err)
			}
			f.write(t, "admission-restored.json", f.restore)
			if err := f.d.checkPreExecAdmissionState(); err != nil {
				t.Fatal("own restored admission self-locked:", err)
			}
			f.d.finished = true
			if err := f.d.checkPreExecAdmissionState(); err != nil {
				t.Fatal("finished first peer can no longer pass its pure readiness guard:", err)
			}
		})
	}
}
func schemaPreExecAdmissionFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.IsDir() {
			out[path] = info.Mode().String()
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			out[path] = info.Mode().String() + target
			return err
		}
		raw, err := os.ReadFile(path)
		out[path] = info.Mode().String() + string(raw)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func TestSchemaMaintenancePreExecAdapterRejectsForeignAdmissionWithoutMutation(t *testing.T) {
	for name, mutate := range map[string]func(*testing.T, *schemaPreExecAdmissionFixture){
		"foreign intent capsule": func(t *testing.T, f *schemaPreExecAdmissionFixture) {
			f.reopen.CapsuleSHA256 = strings.Repeat("0", 64)
			f.write(t, "reopen-intent.json", f.reopen)
		},
		"foreign intent host": func(t *testing.T, f *schemaPreExecAdmissionFixture) {
			f.reopen.Host = "dmit-ubuntu"
			f.write(t, "reopen-intent.json", f.reopen)
		},
		"restored without intent": func(t *testing.T, f *schemaPreExecAdmissionFixture) {
			if err := os.Remove(filepath.Join(f.d.base.origin.Workspace, "state", "reopen-intent.json")); err != nil {
				t.Fatal(err)
			}
		},
		"changed confirmed PID": func(t *testing.T, f *schemaPreExecAdmissionFixture) {
			f.restore.ConfirmedPID++
			f.write(t, "admission-restored.json", f.restore)
		},
		"changed confirmed invocation": func(t *testing.T, f *schemaPreExecAdmissionFixture) {
			f.restore.ConfirmedInvocationID = strings.Repeat("0", 32)
			f.write(t, "admission-restored.json", f.restore)
		},
		"changed confirmed capsule": func(t *testing.T, f *schemaPreExecAdmissionFixture) {
			f.restore.ConfirmedCapsuleSHA256 = strings.Repeat("0", 64)
			f.write(t, "admission-restored.json", f.restore)
		},
		"changed confirmed provider": func(t *testing.T, f *schemaPreExecAdmissionFixture) {
			f.restore.ConfirmedProviderSHA256 = strings.Repeat("0", 64)
			f.write(t, "admission-restored.json", f.restore)
		},
		"wrong restored phase": func(t *testing.T, f *schemaPreExecAdmissionFixture) {
			f.restore.Phase = "DEAD_PREWORKER_RUNNING"
			f.write(t, "admission-restored.json", f.restore)
		},
		"missing own CAS": func(t *testing.T, f *schemaPreExecAdmissionFixture) {
			if err := os.Remove(filepath.Join(f.d.base.origin.Workspace, "state", "dead-preworker-cas-result.json")); err != nil {
				t.Fatal(err)
			}
		},
		"missing own started": func(t *testing.T, f *schemaPreExecAdmissionFixture) {
			if err := os.Remove(filepath.Join(f.d.base.origin.Workspace, "state", "dead-holder-started.json")); err != nil {
				t.Fatal(err)
			}
		},
		"partial intent": func(t *testing.T, f *schemaPreExecAdmissionFixture) {
			f.write(t, "reopen-intent.json.incoming", f.reopen)
		},
		"partial restored": func(t *testing.T, f *schemaPreExecAdmissionFixture) {
			f.write(t, "admission-restored.json.incoming", f.restore)
		},
		"unsafe intent symlink": func(t *testing.T, f *schemaPreExecAdmissionFixture) {
			p := filepath.Join(f.d.base.origin.Workspace, "state", "reopen-intent.json")
			if err := os.Remove(p); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("absent", p); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newSchemaPreExecAdmissionFixture(t, 0)
			f.write(t, "reopen-intent.json", f.reopen)
			f.write(t, "admission-restored.json", f.restore)
			mutate(t, f)
			before := schemaPreExecAdmissionFiles(t, f.d.base.origin.Workspace)
			if f.d.checkPreExecAdmissionState() == nil {
				t.Fatal("foreign/unsafe/partial admission accepted")
			}
			if after := schemaPreExecAdmissionFiles(t, f.d.base.origin.Workspace); !reflect.DeepEqual(before, after) {
				t.Fatal("refusal mutated original native records")
			}
		})
	}
}
