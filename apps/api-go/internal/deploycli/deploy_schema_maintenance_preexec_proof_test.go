//go:build !windows

package deploycli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Real private files, synthetic release/daemon/owner receipts. This exercises
// only the local historical proof; no signature, live daemon, guardian, PG,
// systemd, child provider execution or production qualification is claimed.
type schemaMaintenancePreExecFixture struct {
	runtime  *productionRuntime
	plan     productionSchemaMaintenancePlan
	sha      string
	daemon   schemaMaintenanceRPCBinding
	ownerRaw []byte
	evidence productionSchemaMaintenancePreExecRejectedEvidence
	result   productionSchemaMaintenanceWorkerResult
}

func newSchemaMaintenancePreExecFixture(t *testing.T, primary bool) *schemaMaintenancePreExecFixture {
	t.Helper()
	runtime, plan, planPath := newSchemaMaintenancePlanFixture(t)
	plan.Worker.Provider = schemaMaintenancePreExecRejected117Provider()
	for i := range plan.Origins {
		plan.Origins[i].Recovery.Candidate.ContractRevision = plan.Worker.Provider.ContractRevision
		plan.Origins[i].Recovery.Rollback.ContractRevision = plan.Worker.Provider.ContractRevision
	}
	index := 0
	if !primary {
		index = 1
	}
	origin := plan.Origins[index]
	runtime.hostname = func() (string, error) { return origin.Host, nil }
	runtime.effectiveUID = func() int { return 0 }
	old := schemaMaintenanceAbortOldCapsuleFixture(t, runtime, plan)
	artifacts := cloneMerchantCapsule(old)
	artifacts.Host, artifacts.DeploymentID, artifacts.Root, artifacts.StartupPolicy = origin.Host, plan.TransitionID, filepath.Dir(origin.ArtifactCapsule.Path), "held"
	artifacts.Candidate = plan.Worker.Provider
	artifacts.Rollback = merchantStoreCapsuleProjection(origin.Recovery.Candidate, "rollback")
	artifacts.Writer.Candidate = schemaMaintenanceRecoveryWriterTarget(artifacts.Candidate)
	artifacts.Writer.Rollback = schemaMaintenanceRecoveryWriterTarget(artifacts.Rollback)
	if err := validateMerchantStoreCapsule(artifacts, runtime.paths); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(artifacts.Root, 0700); err != nil {
		t.Fatal(err)
	}
	artifactRaw, err := canonicalMerchantStoreCapsule(artifacts)
	if err != nil {
		t.Fatal(err)
	}
	schemaMaintenanceRecoveryWriteFile(t, origin.ArtifactCapsule.Path, artifactRaw)
	plan.Origins[index].ArtifactCapsule.SHA256 = startupContentSHA256(artifactRaw)
	sha, err := runtime.createSchemaMaintenancePlan(planPath, plan)
	if err != nil {
		t.Fatal(err)
	}
	daemon := schemaMaintenanceRPCBinding{TransitionID: plan.TransitionID, PlanSHA256: sha, DaemonPID: 1234, DaemonInvocationID: strings.Repeat("e", 32), DaemonExeSHA256: plan.Worker.Provider.DeployEngineSHA256, DaemonStartTicks: 321}
	daemonRaw, err := schemaMaintenanceRPCMarshal(daemon)
	if err != nil {
		t.Fatal(err)
	}
	daemonPath := filepath.Join(origin.Workspace, "state", "daemon-binding.json")
	daemonRaw = append(daemonRaw, '\n')
	schemaMaintenanceRecoveryWriteFile(t, daemonPath, daemonRaw)
	f := &schemaMaintenancePreExecFixture{runtime: runtime, plan: plan, sha: sha, daemon: daemon,
		evidence: productionSchemaMaintenancePreExecRejectedEvidence{Plan: productionReleaseFilePlan{Path: planPath, SHA256: sha}, OriginalDaemon: productionReleaseFilePlan{Path: daemonPath, SHA256: startupContentSHA256(daemonRaw)}}}
	if primary {
		owner := testSchemaMaintenanceOwner()
		owner.DeploymentID, owner.PlanSHA256, owner.Host = plan.TransitionID, sha, plan.PrimaryHost
		owner.ContractSHA256, owner.ProviderSHA256 = merchantStoreFenceContractSHA(artifacts.Writer), plan.Worker.Provider.PayloadSHA256
		owner.HolderPID, owner.HolderInvocationID, owner.HolderUnit = daemon.DaemonPID, daemon.DaemonInvocationID, schemaMaintenanceHolderUnit(plan.TransitionID)
		owner.SystemIdentifier, owner.Database, owner.DatabaseOID, owner.Schema, owner.SchemaOID, owner.Role = artifacts.Writer.SystemIdentifier, artifacts.Writer.Database, artifacts.Writer.DatabaseOID, artifacts.Writer.Schema, artifacts.Writer.SchemaOID, artifacts.Writer.Role
		owner.Service = runtime.paths.Service
		f.ownerRaw, err = canonicalMerchantStoreFenceOwner(owner)
		if err != nil {
			t.Fatal(err)
		}
		ownerPath := filepath.Join(origin.Workspace, "state", "schema-owner.json")
		schemaMaintenanceRecoveryWriteFile(t, ownerPath, f.ownerRaw)
		f.evidence.OriginalOwner = &productionReleaseFilePlan{Path: ownerPath, SHA256: startupContentSHA256(f.ownerRaw)}
		f.result = productionSchemaMaintenanceWorkerResult{Format: "lmm-schema-maintenance-worker-v1", TransitionID: plan.TransitionID, PlanSHA256: sha, OwnerSHA256: startupContentSHA256(f.ownerRaw), ProviderSHA256: plan.Worker.Provider.PayloadSHA256, Phase: "WORKER_RESULT_UNCERTAIN", OutputSHA256: startupContentSHA256(nil), StartedUTC: time.Date(2026, 10, 12, 7, 5, 7, 0, time.UTC), FinishedUTC: time.Date(2026, 10, 12, 7, 5, 7, 1000000, time.UTC)}
		f.writePair(t)
	}
	return f
}

func (f *schemaMaintenancePreExecFixture) writePair(t *testing.T) {
	t.Helper()
	state := filepath.Dir(f.evidence.OriginalDaemon.Path)
	write := func(name string, result productionSchemaMaintenanceWorkerResult) *productionReleaseFilePlan {
		raw, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		raw = append(raw, '\n')
		path := filepath.Join(state, name)
		schemaMaintenanceRecoveryWriteFile(t, path, raw)
		return &productionReleaseFilePlan{Path: path, SHA256: startupContentSHA256(raw)}
	}
	f.evidence.WorkerResult = write("schema-worker-result.json", f.result)
	intent := f.result
	intent.Phase, intent.OutputSHA256, intent.FinishedUTC = "WORKER_INTENT", "", time.Time{}
	f.evidence.WorkerIntent = write("schema-worker-intent.json", intent)
}

func (f *schemaMaintenancePreExecFixture) check() error {
	return f.runtime.validateSchemaMaintenancePreExecRejected(f.plan, f.sha, f.daemon, f.ownerRaw, f.evidence)
}

func TestSchemaMaintenancePreExecRejectedOriginalPrimaryAndSecondary(t *testing.T) {
	for _, primary := range []bool{true, false} {
		name := "secondary"
		if primary {
			name = "primary"
		}
		t.Run(name, func(t *testing.T) {
			f := newSchemaMaintenancePreExecFixture(t, primary)
			if err := f.check(); err != nil {
				t.Fatal(err)
			}
			if err := f.check(); err != nil {
				t.Fatal("unchanged repeated local guard:", err)
			}
		})
	}
}

func TestSchemaMaintenancePreExecRejectedResultIsNotEmptyOutputInference(t *testing.T) {
	cases := map[string]func(*productionSchemaMaintenanceWorkerResult){
		"success":       func(r *productionSchemaMaintenanceWorkerResult) { r.Phase = "WORKER_SUCCEEDED" },
		"unfinished":    func(r *productionSchemaMaintenanceWorkerResult) { r.FinishedUTC = time.Time{} },
		"before intent": func(r *productionSchemaMaintenanceWorkerResult) { r.FinishedUTC = r.StartedUTC.Add(-time.Nanosecond) },
		"nonUTC": func(r *productionSchemaMaintenanceWorkerResult) {
			r.StartedUTC = r.StartedUTC.In(time.FixedZone("offset", 3600))
		},
		"other output":     func(r *productionSchemaMaintenanceWorkerResult) { r.OutputSHA256 = strings.Repeat("e", 64) },
		"other owner":      func(r *productionSchemaMaintenanceWorkerResult) { r.OwnerSHA256 = strings.Repeat("a", 64) },
		"other provider":   func(r *productionSchemaMaintenanceWorkerResult) { r.ProviderSHA256 = strings.Repeat("b", 64) },
		"other plan":       func(r *productionSchemaMaintenanceWorkerResult) { r.PlanSHA256 = strings.Repeat("c", 64) },
		"other transition": func(r *productionSchemaMaintenanceWorkerResult) { r.TransitionID = "other-transition" },
		"other format":     func(r *productionSchemaMaintenanceWorkerResult) { r.Format = "other-format" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := newSchemaMaintenancePreExecFixture(t, true)
			mutate(&f.result)
			f.writePair(t)
			if f.check() == nil {
				t.Fatal("accepted altered result despite consistent pair and sealed refs")
			}
		})
	}
}

func TestSchemaMaintenancePreExecRejectedExactReceiptsAndRoles(t *testing.T) {
	cases := map[string]func(*testing.T, *schemaMaintenancePreExecFixture){
		"missing intent ref":  func(t *testing.T, f *schemaMaintenancePreExecFixture) { f.evidence.WorkerIntent = nil },
		"missing owner ref":   func(t *testing.T, f *schemaMaintenancePreExecFixture) { f.evidence.OriginalOwner = nil },
		"changed known owner": func(t *testing.T, f *schemaMaintenancePreExecFixture) { f.ownerRaw = append(f.ownerRaw, '\n') },
		"owner ref escape":    func(t *testing.T, f *schemaMaintenancePreExecFixture) { f.evidence.OriginalOwner.Path += ".other" },
		"daemon generation":   func(t *testing.T, f *schemaMaintenancePreExecFixture) { f.daemon.DaemonStartTicks++ },
		"old engine": func(t *testing.T, f *schemaMaintenancePreExecFixture) {
			f.daemon.DaemonExeSHA256 = strings.Repeat("b", 64)
		},
		"wrong source tuple": func(t *testing.T, f *schemaMaintenancePreExecFixture) {
			f.plan.Worker.Provider.GitRevision = strings.Repeat("a", 40)
		},
		"intent mismatches result": func(t *testing.T, f *schemaMaintenancePreExecFixture) {
			raw, err := os.ReadFile(f.evidence.WorkerIntent.Path)
			if err != nil {
				t.Fatal(err)
			}
			raw = bytes.Replace(raw, []byte("WORKER_INTENT"), []byte("WORKER_SUCCEEDED"), 1)
			schemaMaintenanceRecoveryWriteFile(t, f.evidence.WorkerIntent.Path, raw)
			f.evidence.WorkerIntent.SHA256 = startupContentSHA256(raw)
		},
		"noncanonical result": func(t *testing.T, f *schemaMaintenancePreExecFixture) {
			raw, _ := json.Marshal(f.result)
			schemaMaintenanceRecoveryWriteFile(t, f.evidence.WorkerResult.Path, raw)
			f.evidence.WorkerResult.SHA256 = startupContentSHA256(raw)
		},
		"unknown result field": func(t *testing.T, f *schemaMaintenancePreExecFixture) {
			raw, _ := json.Marshal(f.result)
			raw = append(raw[:len(raw)-1], []byte(",\"extra\":true}")...)
			schemaMaintenanceRecoveryWriteFile(t, f.evidence.WorkerResult.Path, raw)
			f.evidence.WorkerResult.SHA256 = startupContentSHA256(raw)
		},
		"unsafe mode": func(t *testing.T, f *schemaMaintenancePreExecFixture) {
			if err := os.Chmod(f.evidence.WorkerResult.Path, 0644); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := newSchemaMaintenancePreExecFixture(t, true)
			mutate(t, f)
			if f.check() == nil {
				t.Fatal("accepted changed original receipt")
			}
		})
	}
	f := newSchemaMaintenancePreExecFixture(t, false)
	owner := productionReleaseFilePlan{Path: filepath.Join(filepath.Dir(f.evidence.OriginalDaemon.Path), "schema-owner.json"), SHA256: strings.Repeat("a", 64)}
	f.evidence.OriginalOwner = &owner
	if f.check() == nil {
		t.Fatal("secondary accepted primary owner authority")
	}
}

func TestSchemaMaintenancePreExecRejectedRepeatedGuardSeesEveryFootprint(t *testing.T) {
	for _, primary := range []bool{true, false} {
		role := "secondary"
		if primary {
			role = "primary"
		}
		for _, name := range []string{"schema-worker-output.log", "schema-outcome.json", "owner-released.json", "recovery-binding.json", "recovery-startup-profile.json", "recovery-hook-intent.json", "recovery-hook-bound.json", "recovery-start-intent.json", "recovery-started.json", "host-finished.json", "abort-prepared.json", "abort-binding.json", "abort-start-intent.json", "abort-started.json", "abort-finish-intent.json", "abort-host-finished.json", "schema-worker-intent.json", "schema-worker-result.json"} {
			if primary && (name == "schema-worker-intent.json" || name == "schema-worker-result.json") {
				continue
			}
			t.Run(role+"/"+name, func(t *testing.T) {
				f := newSchemaMaintenancePreExecFixture(t, primary)
				if err := f.check(); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(filepath.Dir(f.evidence.OriginalDaemon.Path), name)
				if err := os.WriteFile(path, nil, 0600); err != nil {
					t.Fatal(err)
				}
				if f.check() == nil {
					t.Fatal("empty downstream file ignored after earlier successful check")
				}
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("absent-target", path); err != nil {
					t.Fatal(err)
				}
				if f.check() == nil {
					t.Fatal("dangling downstream symlink treated as ENOENT")
				}
			})
		}
	}
}

func TestSchemaMaintenancePreExecRejectedAdmissionInitialGateDoesNotRejectOwnLaterProgress(t *testing.T) {
	for _, primary := range []bool{true, false} {
		for _, name := range []string{"reopen-intent.json", "admission-restored.json"} {
			t.Run(name+map[bool]string{true: "/primary", false: "/secondary"}[primary], func(t *testing.T) {
				f := newSchemaMaintenancePreExecFixture(t, primary)
				if err := f.runtime.checkSchemaMaintenancePreExecAdmissionUnattempted(f.plan); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(filepath.Dir(f.evidence.OriginalDaemon.Path), name)
				for _, symlink := range []bool{false, true} {
					if symlink {
						if err := os.Symlink("absent-target", path); err != nil {
							t.Fatal(err)
						}
					} else if err := os.WriteFile(path, nil, 0600); err != nil {
						t.Fatal(err)
					}
					if f.runtime.checkSchemaMaintenancePreExecAdmissionUnattempted(f.plan) == nil {
						t.Fatal("initial admission gate ignored existing or uncertain progress")
					}
					if err := f.check(); err != nil {
						t.Fatal("historical guard rejected this lane's later admission progress:", err)
					}
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
				}
			})
		}
	}
}

func TestSchemaMaintenancePreExecRejectedHistorical117TupleOnly(t *testing.T) {
	f := newSchemaMaintenancePreExecFixture(t, true)
	provider := schemaMaintenancePreExecRejected117Provider()
	origin := f.plan.Origins[0]
	if !schemaMaintenance117RejectsWorkerBeforeExec(f.runtime.paths, origin, provider) {
		t.Fatal("historical117 fixed entry not recognized")
	}
	for _, mutate := range []func(*productionReleasePackagePlan){
		func(p *productionReleasePackagePlan) { p.DeployEngineSHA256 = strings.Repeat("a", 64) },
		func(p *productionReleasePackagePlan) { p.PackageSHA256 = strings.Repeat("b", 64) },
		func(p *productionReleasePackagePlan) { p.SignatureBundleSHA256 = strings.Repeat("c", 64) },
		func(p *productionReleasePackagePlan) { p.GitRevision = strings.Repeat("d", 40) },
		func(p *productionReleasePackagePlan) { p.Version = "0.2.118-1" },
		func(p *productionReleasePackagePlan) { p.MerchantStoreWriterCapability = 7 },
		func(p *productionReleasePackagePlan) { p.PackagePath = "assets/rollback.pkg.tar.zst" },
	} {
		p := provider
		mutate(&p)
		if schemaMaintenance117RejectsWorkerBeforeExec(f.runtime.paths, origin, p) {
			t.Fatal("historical fact applied to another release tuple")
		}
	}
	// The validator never calls the configured runner, including later versions
	// that permit capsule paths. A nil runner in both positive fixtures proves it.
	if f.runtime.runner != nil {
		t.Fatal("fixture unexpectedly has a command runner")
	}
	if err := f.check(); err != nil {
		t.Fatal(err)
	}
}
