//go:build !windows

package deploycli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type schemaMaintenanceAbortTestLease struct {
	schemaMaintenanceTestLease
	finishes int
}

func (l *schemaMaintenanceAbortTestLease) Finish(context.Context) error {
	l.finishes++
	return errors.New("unexpected native finish")
}

// These tests qualify local immutable authority and dispatch boundaries. The
// callback is a fixture, not a claim that a live original daemon or PG schema
// has been observed. No systemd service, production connection or DDL is used.
func schemaMaintenanceAbortOldCapsuleFixture(t *testing.T, runtime *productionRuntime, plan productionSchemaMaintenancePlan) productionMerchantStoreCapsule {
	t.Helper()
	origin := plan.Origins[0]
	c := testMerchantCapsule(t, runtime.paths)
	c.DeploymentID, c.Root, c.Host = "old-"+origin.Host, filepath.Dir(origin.OldCapsule.Path), origin.Host
	c.Candidate, c.Rollback = origin.Recovery.Candidate, origin.Recovery.Rollback
	schema := plan.PreSchema
	schema.StartupSHA256, schema.SignedUnitSHA256 = c.Writer.StartupSHA256, c.Writer.SignedUnitSHA256
	c.ExistingSchemaContract = &schema
	c.Writer = &productionMerchantStoreWriterContract{Format: 1, RequiredCapability: plan.RequiredCapability, SystemIdentifier: schema.SystemIdentifier, Database: schema.Database, DatabaseOID: schema.DatabaseOID, Schema: schema.Schema, SchemaOID: schema.SchemaOID, Role: plan.Role, StartupSHA256: schema.StartupSHA256, SignedUnitSHA256: schema.SignedUnitSHA256, RecoveryPolicy: "same-floor-writable", Candidate: schemaMaintenanceRecoveryWriterTarget(c.Candidate), Rollback: schemaMaintenanceRecoveryWriterTarget(c.Rollback)}
	if err := validateSchemaMaintenanceAbortOldCapsule(c, origin, plan, runtime.paths); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestSchemaMaintenanceAbortOldCapsuleRetainsOrdinaryPreSchemaAuthority(t *testing.T) {
	runtime, plan, _ := newSchemaMaintenancePlanFixture(t)
	good := schemaMaintenanceAbortOldCapsuleFixture(t, runtime, plan)
	cases := map[string]func(*productionMerchantStoreCapsule){
		"post-DDL catalog": func(c *productionMerchantStoreCapsule) {
			c.ExistingSchemaContract.MetadataSHA256 = strings.Repeat("f", 64)
		},
		"physical cluster": func(c *productionMerchantStoreCapsule) {
			c.ExistingSchemaContract.SystemIdentifier = "1"
			c.Writer.SystemIdentifier = "1"
		},
		"other role":     func(c *productionMerchantStoreCapsule) { c.Writer.Role = "other" },
		"held startup":   func(c *productionMerchantStoreCapsule) { c.StartupPolicy = "held" },
		"baseline":       func(c *productionMerchantStoreCapsule) { *c = testMerchantStartupBaseline(t, runtime.paths) },
		"missing schema": func(c *productionMerchantStoreCapsule) { c.ExistingSchemaContract = nil },
		"missing writer": func(c *productionMerchantStoreCapsule) { c.Writer = nil },
		"new worker": func(c *productionMerchantStoreCapsule) {
			c.Candidate = plan.Worker.Provider
			c.Writer.Candidate = schemaMaintenanceRecoveryWriterTarget(c.Candidate)
		},
		"other rollback": func(c *productionMerchantStoreCapsule) {
			c.Rollback = schemaMaintenancePlanProvider("0.2.109", "rollback")
			c.Rollback.MerchantStoreWriterCapability = 7
			c.Writer.Rollback = schemaMaintenanceRecoveryWriterTarget(c.Rollback)
		},
		"other engine":    func(c *productionMerchantStoreCapsule) { c.Candidate.DeployEngineSHA256 = strings.Repeat("f", 64) },
		"other signature": func(c *productionMerchantStoreCapsule) { c.Rollback.SignatureBundleSHA256 = strings.Repeat("f", 64) },
		"other host":      func(c *productionMerchantStoreCapsule) { c.Host = plan.Origins[1].Host },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			c := cloneMerchantCapsule(good)
			mutate(&c)
			if validateSchemaMaintenanceAbortOldCapsule(c, plan.Origins[0], plan, runtime.paths) == nil {
				t.Fatal("changed original abort provider/schema accepted")
			}
		})
	}
	cap8 := cloneMerchantCapsule(good)
	cap8.Writer.RequiredCapability = 8
	cap8.Candidate.MerchantStoreWriterCapability = 8
	cap8.Rollback.MerchantStoreWriterCapability = 8
	cap8.Writer.Candidate.Capability = 8
	cap8.Writer.Rollback.Capability = 8
	plan.RequiredCapability = 8
	plan.Origins[0].Recovery.Candidate = cap8.Candidate
	plan.Origins[0].Recovery.Rollback = cap8.Rollback
	if err := validateMerchantStoreCapsule(cap8, runtime.paths); err != nil {
		t.Fatal(err)
	}
	if validateSchemaMaintenanceAbortOldCapsule(cap8, plan.Origins[0], plan, runtime.paths) == nil {
		t.Fatal("abort widened its explicit floor ceiling")
	}
}

func TestSchemaMaintenanceAbortNeverClaimedRejectsEveryHistoricalObject(t *testing.T) {
	names := []string{"schema-claim-intent.json", "schema-owner.json", "schema-worker-intent.json", "schema-worker-result.json", "schema-worker-output.log", "schema-outcome.json", "owner-released.json", "recovery-binding.json", "recovery-startup-profile.json", "recovery-hook-intent.json", "recovery-hook-bound.json", "recovery-start-intent.json", "recovery-started.json", "host-finished.json"}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			h, _ := newSchemaMaintenanceHostEvidenceFixture(t)
			h.lease = &schemaMaintenanceTestLease{}
			h.validate = func(context.Context) error { return nil }
			path := filepath.Join(h.binding.Workspace, "state", name)
			// An empty file is evidence of an attempted operation, never absence.
			schemaMaintenanceRecoveryWriteFile(t, path, nil)
			callbacks := 0
			if h.checkAbortNeverClaimed(context.Background(), func(context.Context) error { callbacks++; return nil }, true) == nil || callbacks != 0 {
				t.Fatal("historical intent reached abort callback")
			}
		})
	}
	for _, kind := range []string{"missing", "symlink", "replaceable"} {
		t.Run("state-"+kind, func(t *testing.T) {
			h, _ := newSchemaMaintenanceHostEvidenceFixture(t)
			h.lease = &schemaMaintenanceTestLease{}
			h.validate = func(context.Context) error { return nil }
			state := filepath.Join(h.binding.Workspace, "state")
			switch kind {
			case "missing":
				if err := os.Remove(state); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Remove(state); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(t.TempDir(), "missing"), state); err != nil {
					t.Fatal(err)
				}
			case "replaceable":
				if err := os.Chmod(state, 0777); err != nil {
					t.Fatal(err)
				}
			}
			cb := 0
			if h.checkAbortNeverClaimed(context.Background(), func(context.Context) error { cb++; return nil }, true) == nil || cb != 0 {
				t.Fatal("unsafe state ancestry mistaken for exact absence")
			}
			if h.requireNewAbortIntent("abort-start-intent.json") == nil {
				t.Fatal("unsafe state allowed new abort intent")
			}
		})
	}
	h, _ := newSchemaMaintenanceHostEvidenceFixture(t)
	h.lease = &schemaMaintenanceTestLease{}
	h.validate = func(context.Context) error { return nil }
	if h.checkAbortNeverClaimed(context.Background(), nil, true) == nil {
		t.Fatal("local absence replaced original daemon history")
	}
	nativeErr := errors.New("native history was claimed")
	if !errors.Is(h.checkAbortNeverClaimed(context.Background(), func(context.Context) error { return nativeErr }, true), nativeErr) {
		t.Fatal("native never-claimed denial ignored")
	}
}

func TestSchemaMaintenanceAbortStartUnknownResultIsDurableAndNeverRetried(t *testing.T) {
	for _, scenario := range []string{"unknown result", "running after intent", "changed original stop", "lease lost after intent"} {
		t.Run(scenario, func(t *testing.T) {
			h, stopped := newSchemaMaintenanceHostEvidenceFixture(t)
			stopped.Phase = "WRITER_STOPPED"
			stopped.ShutdownJournalSHA256 = strings.Repeat("9", 64)
			h.validate = func(context.Context) error { return nil }
			lease := &schemaMaintenanceTestLease{}
			h.lease = lease
			h.runtime.paths.Service = "lmm-api.service"
			calls := 0
			h.runtime.runner = existingSchemaTestRunner{run: func(command productionCommand) ([]byte, error) {
				calls++
				if command.Name != commandSystemctl || !reflect.DeepEqual(command.Args, []string{"start", "lmm-api.service"}) {
					t.Fatal("nonfixed start command")
				}
				return nil, errors.New("lost systemctl response")
			}}
			intent := productionSchemaMaintenanceAbortStartIntent{Format: "lmm-schema-maintenance-abort-start-intent-v1", Stopped: stopped, StartedUTC: time.Unix(2000, 0).UTC()}
			callbacks := 0
			check := func(context.Context) (productionSchemaMaintenanceHostCapture, error) {
				callbacks++
				fresh := stopped
				switch scenario {
				case "running after intent":
					fresh.Phase = "ABORT_RUNNING"
				case "changed original stop":
					fresh.ShutdownJournalSHA256 = strings.Repeat("8", 64)
				case "lease lost after intent":
					lease.err = errors.New("lost")
				}
				return fresh, nil
			}
			if h.dispatchAbortOldStart(context.Background(), h.runtime.paths.Service, intent, check) == nil {
				t.Fatal("ambiguous or changed start succeeded")
			}
			expectedCalls := 0
			if scenario == "unknown result" {
				expectedCalls = 1
			}
			if calls != expectedCalls || callbacks != 1 {
				t.Fatalf("dispatch count=%d callbacks=%d", calls, callbacks)
			}
			path := filepath.Join(h.binding.Workspace, "state", "abort-start-intent.json")
			before, err := os.ReadFile(path)
			if err != nil || len(before) == 0 {
				t.Fatal("uncertain start lost its durable intent")
			}
			if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0600 {
				t.Fatal("intent is not private")
			}
			if h.dispatchAbortOldStart(context.Background(), h.runtime.paths.Service, intent, check) == nil {
				t.Fatal("replayed uncertain start")
			}
			if _, err := h.startAbortOld(context.Background(), productionSchemaMaintenancePlan{}, "", productionSchemaMaintenanceAbortBinding{}, func(context.Context) error { callbacks++; return nil }, func(context.Context) error { callbacks++; return nil }); err == nil {
				t.Fatal("entry replayed uncertain start")
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) || calls != expectedCalls || callbacks != 1 {
				t.Fatal("replay changed evidence or dispatched again")
			}
		})
	}
}

func TestSchemaMaintenanceAbortEntriesRejectAmbiguousPriorEvidenceBeforeCallbacks(t *testing.T) {
	for _, name := range []string{"abort-start-intent.json", "abort-finish-intent.json", "abort-host-finished.json", "abort-started.json"} {
		for _, kind := range []string{"empty", "directory", "dangling link"} {
			t.Run(name+"/"+kind, func(t *testing.T) {
				h, _ := newSchemaMaintenanceHostEvidenceFixture(t)
				h.validate = func(context.Context) error { return nil }
				lease := &schemaMaintenanceAbortTestLease{}
				h.lease = lease
				calls := 0
				h.runtime.runner = existingSchemaTestRunner{run: func(productionCommand) ([]byte, error) { calls++; return nil, errors.New("unexpected command") }}
				path := filepath.Join(h.binding.Workspace, "state", name)
				switch kind {
				case "empty":
					schemaMaintenanceRecoveryWriteFile(t, path, nil)
				case "directory":
					if err := os.Mkdir(path, 0700); err != nil {
						t.Fatal(err)
					}
				case "dangling link":
					if err := os.Symlink(filepath.Join(t.TempDir(), "absent"), path); err != nil {
						t.Fatal(err)
					}
				}
				callbacks := 0
				cb := func(context.Context) error { callbacks++; return nil }
				var err error
				if name == "abort-start-intent.json" || name == "abort-started.json" {
					_, err = h.startAbortOld(context.Background(), productionSchemaMaintenancePlan{}, "", productionSchemaMaintenanceAbortBinding{}, cb, cb)
				} else {
					_, err = h.finishAbortOld(context.Background(), productionSchemaMaintenancePlan{}, "", productionSchemaMaintenanceAbortBinding{}, cb, cb)
				}
				if err == nil || calls != 0 || callbacks != 0 || lease.finishes != 0 {
					t.Fatal("prior abort evidence reached mutation callbacks")
				}
			})
		}
	}
}

type schemaMaintenanceAbortFinishedFixture struct {
	h        *productionSchemaMaintenanceHost
	plan     productionSchemaMaintenancePlan
	preSHA   string
	binding  productionSchemaMaintenanceAbortBinding
	capsule  productionMerchantStoreCapsule
	start    productionSchemaMaintenanceAbortStartIntent
	finished productionSchemaMaintenanceAbortHostFinished
}

func newSchemaMaintenanceAbortFinishedFixture(t *testing.T) schemaMaintenanceAbortFinishedFixture {
	t.Helper()
	runtime, plan, _, prepared, binding := schemaMaintenanceAbortPlanFixture(t)
	capsule := schemaMaintenanceAbortOldCapsuleFixture(t, runtime, plan)
	if err := os.MkdirAll(filepath.Dir(plan.Origins[0].OldCapsule.Path), 0700); err != nil {
		t.Fatal(err)
	}
	capRaw := schemaMaintenanceRecoveryWriteJSON(t, plan.Origins[0].OldCapsule.Path, capsule)
	plan.Origins[0].OldCapsule.SHA256 = startupContentSHA256(capRaw)
	planRaw, err := canonicalProductionSchemaMaintenancePlan(plan, runtime.paths)
	if err != nil {
		t.Fatal(err)
	}
	preSHA := startupContentSHA256(planRaw)
	origin := plan.Origins[0]
	h := &productionSchemaMaintenanceHost{runtime: runtime, lease: &schemaMaintenanceTestLease{err: errors.New("authenticated finish released locks")}, validate: func(context.Context) error { return nil }, binding: productionSchemaMaintenanceHostBinding{TransitionID: plan.TransitionID, PlanSHA256: preSHA, Host: origin.Host, CapsulePath: origin.OldCapsule.Path, CapsuleSHA256: origin.OldCapsule.SHA256, GuardianBindingPath: origin.Guardian.BindingPath, Workspace: origin.Workspace}}
	runtime.now = func() time.Time { return time.Unix(5000, 0).UTC() }
	runtime.hostname = func() (string, error) { return origin.Host, nil }
	stopped := productionSchemaMaintenanceHostCapture{Format: 1, TransitionID: plan.TransitionID, PlanSHA256: preSHA, Host: origin.Host, Phase: "WRITER_STOPPED", CapsuleSHA256: origin.OldCapsule.SHA256, ProviderSHA256: origin.Recovery.Candidate.PayloadSHA256, PID: 12345, InvocationID: strings.Repeat("d", 32), OriginalAdmissionSHA256: strings.Repeat("e", 64), ClosedAdmissionSHA256: strings.Repeat("f", 64), NginxPID: 12346, NginxInvocationID: strings.Repeat("a", 32), NginxGenerationSHA256: strings.Repeat("b", 64), StopStartedUTC: time.Unix(1000, 0).UTC(), ShutdownJournalSHA256: strings.Repeat("9", 64)}
	stopRaw := schemaMaintenanceRecoveryWriteJSON(t, filepath.Join(origin.Workspace, "state", "writer-stopped.json"), stopped)
	schemaMaintenanceRecoveryWriteJSON(t, filepath.Join(origin.Workspace, "state", "close-intent.json"), stopped)
	guardianRaw := schemaMaintenanceRecoveryWriteJSON(t, origin.Guardian.BindingPath, struct {
		Original string `json:"original"`
	}{"native-guardian-binding"})
	h.binding.GuardianBindingSHA256 = startupContentSHA256(guardianRaw)
	binding.PrePlanSHA256 = preSHA
	for i := range prepared {
		prepared[i].PrePlanSHA256 = preSHA
		prepared[i].DaemonGeneration.PlanSHA256 = preSHA
		prepared[i].OldCapsule = plan.Origins[i].OldCapsule
		if i == 0 {
			prepared[i].StoppedCapture.SHA256 = startupContentSHA256(stopRaw)
			prepared[i].GuardianBinding.SHA256 = h.binding.GuardianBindingSHA256
		}
		raw, err := canonicalProductionSchemaMaintenanceAbortPrepared(prepared[i], plan, preSHA, runtime.paths)
		if err != nil {
			t.Fatal(err)
		}
		binding.Origins[i].OldCapsule = prepared[i].OldCapsule
		binding.Origins[i].StoppedCapture = prepared[i].StoppedCapture
		binding.Origins[i].GuardianBinding = prepared[i].GuardianBinding
		binding.Origins[i].Prepared.SHA256 = startupContentSHA256(raw)
		if i == 0 {
			schemaMaintenanceRecoveryWriteFile(t, binding.Origins[i].Prepared.Path, raw)
		}
	}
	bindingRaw, err := canonicalProductionSchemaMaintenanceAbortBinding(binding, plan, preSHA, runtime.paths)
	if err != nil {
		t.Fatal(err)
	}
	schemaMaintenanceRecoveryWriteFile(t, filepath.Join(origin.Workspace, "state", "abort-binding.json"), bindingRaw)
	bound := binding.Origins[0]
	start := productionSchemaMaintenanceAbortStartIntent{Format: "lmm-schema-maintenance-abort-start-intent-v1", TransitionID: plan.TransitionID, PrePlanSHA256: preSHA, Host: origin.Host, AbortBindingSHA256: startupContentSHA256(bindingRaw), PreparedSHA256: bound.Prepared.SHA256, OldCapsule: bound.OldCapsule, StoppedCaptureSHA256: bound.StoppedCapture.SHA256, Stopped: stopped, StartedUTC: time.Unix(2000, 0).UTC()}
	startRaw := schemaMaintenanceRecoveryWriteJSON(t, filepath.Join(origin.Workspace, "state", "abort-start-intent.json"), start)
	restored := stopped
	restored.Phase = "ADMISSION_RESTORED"
	restored.ConfirmedCapsuleSHA256 = bound.OldCapsule.SHA256
	restored.ConfirmedProviderSHA256 = origin.Recovery.Candidate.PayloadSHA256
	restored.ConfirmedPID = 22345
	restored.ConfirmedInvocationID = strings.Repeat("c", 32)
	restoredRaw := schemaMaintenanceRecoveryWriteJSON(t, filepath.Join(origin.Workspace, "state", "admission-restored.json"), restored)
	finished := productionSchemaMaintenanceAbortHostFinished{Format: "lmm-schema-maintenance-abort-host-finished-v1", TransitionID: plan.TransitionID, PrePlanSHA256: preSHA, Host: origin.Host, AbortBindingSHA256: startupContentSHA256(bindingRaw), PreparedSHA256: bound.Prepared.SHA256, GuardianBindingSHA256: bound.GuardianBinding.SHA256, OldCapsule: bound.OldCapsule, ProviderSHA256: origin.Recovery.Candidate.PayloadSHA256, StartIntentSHA256: startupContentSHA256(startRaw), RestoredCaptureSHA256: startupContentSHA256(restoredRaw), PID: restored.ConfirmedPID, InvocationID: restored.ConfirmedInvocationID}
	finishIntent := productionSchemaMaintenanceAbortFinishIntent{Format: "lmm-schema-maintenance-abort-finish-intent-v1", Target: finished, StartedUTC: time.Unix(3000, 0).UTC()}
	finishRaw := schemaMaintenanceRecoveryWriteJSON(t, filepath.Join(origin.Workspace, "state", "abort-finish-intent.json"), finishIntent)
	finished.FinishIntentSHA256 = startupContentSHA256(finishRaw)
	finished.FinishedUTC = time.Unix(4000, 0).UTC()
	schemaMaintenanceRecoveryWriteJSON(t, filepath.Join(origin.Workspace, "state", "abort-host-finished.json"), finished)
	return schemaMaintenanceAbortFinishedFixture{h, plan, preSHA, binding, capsule, start, finished}
}
func (f schemaMaintenanceAbortFinishedFixture) check(ctx context.Context) (*productionSchemaMaintenanceAbortHostFinished, error) {
	return f.h.checkAbortReadyAuthority(ctx, f.plan, f.preSHA, f.binding, func(context.Context) error { return nil })
}

func TestSchemaMaintenanceAbortFinishedAuthorityIsReadOnlyAndPreservesOldHooks(t *testing.T) {
	f := newSchemaMaintenanceAbortFinishedFixture(t)
	ctx := context.Background()
	calls := 0
	f.h.runtime.runner = existingSchemaTestRunner{run: func(productionCommand) ([]byte, error) { calls++; return nil, errors.New("unexpected command") }}
	got, err := f.check(ctx)
	if err != nil || got == nil || *got != f.finished {
		t.Fatalf("independent finished authority: %v", err)
	}
	capsule, index, _, err := f.h.abortOldCapsule(ctx, f.plan, f.preSHA, f.binding, func(context.Context) error { return nil }, true)
	if err != nil || index != 0 || !reflect.DeepEqual(capsule, f.capsule) {
		t.Fatalf("original old capsule after finish: %v", err)
	}
	if f.h.check(ctx) == nil {
		t.Fatal("terminal read authority weakened the mutation lease")
	}
	callbacks := 0
	cb := func(context.Context) error { callbacks++; return nil }
	if _, err := f.h.restoreAbortOldAdmission(ctx, f.plan, f.preSHA, f.binding, cb, cb); err == nil {
		t.Fatal("finished authority reopened admission")
	}
	if calls != 0 || callbacks != 0 {
		t.Fatal("finished mutation reached commands or callbacks")
	}
	// The original capsule's loaded-hook seals remain immutable, even if the
	// replacement is otherwise a valid ordinary capsule with coherent fields.
	changed := cloneMerchantCapsule(f.capsule)
	changed.ExistingSchemaContract.StartupSHA256 = strings.Repeat("a", 64)
	changed.Writer.StartupSHA256 = changed.ExistingSchemaContract.StartupSHA256
	schemaMaintenanceRecoveryWriteJSON(t, f.binding.Origins[0].OldCapsule.Path, changed)
	if _, _, _, err := f.h.abortOldCapsule(ctx, f.plan, f.preSHA, f.binding, cb, true); err == nil {
		t.Fatal("changed original hook capsule accepted")
	}
	if calls != 0 {
		t.Fatal("changed capsule launched a command")
	}
}

func TestSchemaMaintenanceAbortFinishedReceiptRejectsChangedAuthority(t *testing.T) {
	cases := map[string]func(*schemaMaintenanceAbortFinishedFixture){
		"post-schema finish format": func(f *schemaMaintenanceAbortFinishedFixture) {
			f.finished.Format = "lmm-schema-maintenance-host-finished-v1"
		},
		"preplan": func(f *schemaMaintenanceAbortFinishedFixture) { f.finished.PrePlanSHA256 = strings.Repeat("f", 64) },
		"binding": func(f *schemaMaintenanceAbortFinishedFixture) {
			f.finished.AbortBindingSHA256 = strings.Repeat("f", 64)
		},
		"prepared": func(f *schemaMaintenanceAbortFinishedFixture) { f.finished.PreparedSHA256 = strings.Repeat("f", 64) },
		"guardian": func(f *schemaMaintenanceAbortFinishedFixture) {
			f.finished.GuardianBindingSHA256 = strings.Repeat("f", 64)
		},
		"new capsule": func(f *schemaMaintenanceAbortFinishedFixture) {
			f.finished.OldCapsule = f.plan.Origins[0].ArtifactCapsule
		},
		"worker provider": func(f *schemaMaintenanceAbortFinishedFixture) {
			f.finished.ProviderSHA256 = f.plan.Worker.Provider.PayloadSHA256
		},
		"start digest": func(f *schemaMaintenanceAbortFinishedFixture) { f.finished.StartIntentSHA256 = strings.Repeat("f", 64) },
		"finish intent digest": func(f *schemaMaintenanceAbortFinishedFixture) {
			f.finished.FinishIntentSHA256 = strings.Repeat("f", 64)
		},
		"restored digest": func(f *schemaMaintenanceAbortFinishedFixture) {
			f.finished.RestoredCaptureSHA256 = strings.Repeat("f", 64)
		},
		"PID":                 func(f *schemaMaintenanceAbortFinishedFixture) { f.finished.PID++ },
		"invocation":          func(f *schemaMaintenanceAbortFinishedFixture) { f.finished.InvocationID = strings.Repeat("b", 32) },
		"original invocation": func(f *schemaMaintenanceAbortFinishedFixture) { f.finished.InvocationID = f.start.Stopped.InvocationID },
		"zero time":           func(f *schemaMaintenanceAbortFinishedFixture) { f.finished.FinishedUTC = time.Time{} },
		"before start": func(f *schemaMaintenanceAbortFinishedFixture) {
			f.finished.FinishedUTC = f.start.StartedUTC.Add(-time.Second)
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := newSchemaMaintenanceAbortFinishedFixture(t)
			mutate(&f)
			schemaMaintenanceRecoveryWriteJSON(t, filepath.Join(f.h.binding.Workspace, "state", "abort-host-finished.json"), f.finished)
			if _, err := f.check(context.Background()); err == nil {
				t.Fatal("changed independent terminal authority accepted")
			}
		})
	}
}

func TestSchemaMaintenanceAbortFinishedRequiresOriginalPrivateCanonicalFiles(t *testing.T) {
	for _, name := range []string{"abort-binding.json", "abort-prepared.json", "guardian-binding.json", "writer-stopped.json", "abort-start-intent.json", "abort-finish-intent.json", "admission-restored.json", "abort-host-finished.json"} {
		for _, change := range []string{"noncanonical", "public", "symlink", "hardlink", "missing"} {
			t.Run(name+"/"+change, func(t *testing.T) {
				f := newSchemaMaintenanceAbortFinishedFixture(t)
				path := filepath.Join(f.h.binding.Workspace, "state", name)
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				switch change {
				case "noncanonical":
					schemaMaintenanceRecoveryWriteFile(t, path, append([]byte(" "), raw...))
				case "public":
					if err := os.Chmod(path, 0644); err != nil {
						t.Fatal(err)
					}
				case "symlink":
					target := filepath.Join(t.TempDir(), "target")
					schemaMaintenanceRecoveryWriteFile(t, target, raw)
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(target, path); err != nil {
						t.Fatal(err)
					}
				case "hardlink":
					if err := os.Link(path, filepath.Join(t.TempDir(), "link")); err != nil {
						t.Fatal(err)
					}
				case "missing":
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := f.check(context.Background()); err == nil {
					t.Fatal("unsafe or changed terminal evidence accepted")
				}
			})
		}
	}
	f := newSchemaMaintenanceAbortFinishedFixture(t)
	schemaMaintenanceRecoveryWriteFile(t, filepath.Join(f.h.binding.Workspace, "state", "schema-worker-intent.json"), nil)
	if _, err := f.check(context.Background()); err == nil {
		t.Fatal("empty worker intent was ignored after finish")
	}
}

func TestSchemaMaintenanceAbortFinishIntentRequiresExactTargetAndChronology(t *testing.T) {
	for _, change := range []string{"format", "target generation", "missing intent", "before original start", "finish before intent", "unknown field"} {
		t.Run(change, func(t *testing.T) {
			f := newSchemaMaintenanceAbortFinishedFixture(t)
			path := filepath.Join(f.h.binding.Workspace, "state", "abort-finish-intent.json")
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var intent productionSchemaMaintenanceAbortFinishIntent
			if json.Unmarshal(raw, &intent) != nil {
				t.Fatal("fixture intent")
			}
			switch change {
			case "format":
				intent.Format = "lmm-schema-maintenance-host-finished-v1"
			case "target generation":
				intent.Target.PID++
			case "before original start":
				intent.StartedUTC = f.start.StartedUTC.Add(-time.Second)
			case "finish before intent":
				intent.StartedUTC = f.finished.FinishedUTC.Add(time.Second)
			case "missing intent":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
			if change != "missing intent" {
				raw = schemaMaintenanceRecoveryWriteJSON(t, path, intent)
				if change == "unknown field" {
					raw = bytes.Replace(raw, []byte("{\n"), []byte("{\n  \"extra\": true,\n"), 1)
					schemaMaintenanceRecoveryWriteFile(t, path, raw)
				}
				f.finished.FinishIntentSHA256 = startupContentSHA256(raw)
			}
			if f.h.validateAbortFinishIntent(f.finished, f.start.StartedUTC) == nil {
				t.Fatal("ambiguous terminal intent or chronology accepted")
			}
		})
	}
}
