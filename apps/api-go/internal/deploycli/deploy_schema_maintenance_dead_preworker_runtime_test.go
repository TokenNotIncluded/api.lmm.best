//go:build !windows

package deploycli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// These are pure bindings and real private-file preflight tests. They do not
// qualify systemd death, transferred guardian FDs, official signatures or the
// complete recovery runtime; no database or service command is executed.
type schemaMaintenanceDeadRuntimeFixture struct {
	runtime   *productionRuntime
	authority productionSchemaMaintenanceDeadPreWorkerCASAuthority
	artifacts productionMerchantStoreCapsule
	owner     productionMerchantStoreFenceOwner
}

func newSchemaMaintenanceDeadRuntimeFixture(t *testing.T) schemaMaintenanceDeadRuntimeFixture {
	t.Helper()
	runtime, plan, planPath := newSchemaMaintenancePlanFixture(t)
	runtime.hostname = func() (string, error) { return plan.PrimaryHost, nil }
	old := schemaMaintenanceAbortOldCapsuleFixture(t, runtime, plan)
	artifacts := cloneMerchantCapsule(old)
	artifacts.DeploymentID, artifacts.Root, artifacts.StartupPolicy = plan.TransitionID, filepath.Dir(plan.Origins[0].ArtifactCapsule.Path), "held"
	artifacts.Candidate = plan.Worker.Provider
	artifacts.Rollback = merchantStoreCapsuleProjection(plan.Origins[0].Recovery.Candidate, "rollback")
	artifacts.Writer.Candidate = schemaMaintenanceRecoveryWriterTarget(artifacts.Candidate)
	artifacts.Writer.Rollback = schemaMaintenanceRecoveryWriterTarget(artifacts.Rollback)
	for _, capsule := range []productionMerchantStoreCapsule{old, artifacts} {
		if err := validateMerchantStoreCapsule(capsule, runtime.paths); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(capsule.Root, 0700); err != nil {
			t.Fatal(err)
		}
		raw, err := canonicalMerchantStoreCapsule(capsule)
		if err != nil {
			t.Fatal(err)
		}
		schemaMaintenanceRecoveryWriteFile(t, filepath.Join(capsule.Root, "capsule.json"), raw)
		if capsule.StartupPolicy == "held" {
			plan.Origins[0].ArtifactCapsule.SHA256 = startupContentSHA256(raw)
		} else {
			plan.Origins[0].OldCapsule.SHA256 = startupContentSHA256(raw)
		}
	}
	preSHA, err := runtime.createSchemaMaintenancePlan(planPath, plan)
	if err != nil {
		t.Fatal(err)
	}
	daemon := schemaMaintenanceRPCBinding{TransitionID: plan.TransitionID, PlanSHA256: preSHA, DaemonPID: os.Getpid() + 100, DaemonInvocationID: strings.Repeat("e", 32), DaemonExeSHA256: plan.Worker.Provider.DeployEngineSHA256, DaemonStartTicks: 123}
	owner := testSchemaMaintenanceOwner()
	owner.DeploymentID, owner.PlanSHA256, owner.Host = plan.TransitionID, preSHA, plan.PrimaryHost
	owner.ContractSHA256, owner.ProviderSHA256 = merchantStoreFenceContractSHA(artifacts.Writer), plan.Worker.Provider.PayloadSHA256
	owner.HolderPID, owner.HolderInvocationID, owner.HolderUnit = daemon.DaemonPID, daemon.DaemonInvocationID, schemaMaintenanceHolderUnit(plan.TransitionID)
	owner.SystemIdentifier, owner.Database, owner.DatabaseOID, owner.Schema, owner.SchemaOID, owner.Role = artifacts.Writer.SystemIdentifier, artifacts.Writer.Database, artifacts.Writer.DatabaseOID, artifacts.Writer.Schema, artifacts.Writer.SchemaOID, artifacts.Writer.Role
	owner.Service = runtime.paths.Service
	raw, err := canonicalMerchantStoreFenceOwner(owner)
	if err != nil {
		t.Fatal(err)
	}
	authority := productionSchemaMaintenanceDeadPreWorkerCASAuthority{Plan: plan, PrePlanSHA256: preSHA, OldCapsule: old, OldCapsuleSHA256: plan.Origins[0].OldCapsule.SHA256, OriginalDaemon: daemon, OwnerMode: schemaMaintenanceDeadPreWorkerExactOwner, OriginalOwnerRaw: raw, CheckLocalDeadAuthority: func(context.Context) error { return nil }}
	if err := validateSchemaMaintenanceDeadOriginalOwner(raw, plan, preSHA, daemon, artifacts, runtime.paths.Service); err != nil {
		t.Fatal(err)
	}
	return schemaMaintenanceDeadRuntimeFixture{runtime, authority, artifacts, owner}
}

func TestSchemaMaintenanceDeadRuntimeOriginalOwnerBindsFullKnownTuple(t *testing.T) {
	f := newSchemaMaintenanceDeadRuntimeFixture(t)
	// The reviewed digest and actual PG observation bind these at later
	// stages. This validator cannot invent a previously known backend PID
	// or nonce; both still need a valid canonical wire representation.
	variant := f.owner
	variant.BackendPID++
	variant.Nonce = strings.Repeat("a", 32)
	variantRaw, err := canonicalMerchantStoreFenceOwner(variant)
	if err != nil || validateSchemaMaintenanceDeadOriginalOwner(variantRaw, f.authority.Plan, f.authority.PrePlanSHA256, f.authority.OriginalDaemon, f.artifacts, f.runtime.paths.Service) != nil {
		t.Fatal("wire-only PID/nonce fields were treated as independently known values")
	}
	cases := map[string]func(*productionMerchantStoreFenceOwner){
		"format":     func(o *productionMerchantStoreFenceOwner) { o.Format++ },
		"state":      func(o *productionMerchantStoreFenceOwner) { o.State = "RELEASED" },
		"deployment": func(o *productionMerchantStoreFenceOwner) { o.DeploymentID = "other-schema-owner" },
		"host":       func(o *productionMerchantStoreFenceOwner) { o.Host = f.authority.Plan.Origins[1].Host },
		"preplan":    func(o *productionMerchantStoreFenceOwner) { o.PlanSHA256 = strings.Repeat("f", 64) },
		"contract":   func(o *productionMerchantStoreFenceOwner) { o.ContractSHA256 = strings.Repeat("f", 64) },
		"provider": func(o *productionMerchantStoreFenceOwner) {
			o.ProviderSHA256 = f.authority.Plan.Origins[0].Recovery.Candidate.PayloadSHA256
		},
		"missing nonce":             func(o *productionMerchantStoreFenceOwner) { o.Nonce = "" },
		"holder PID":                func(o *productionMerchantStoreFenceOwner) { o.HolderPID++ },
		"ordinary holder unit":      func(o *productionMerchantStoreFenceOwner) { o.HolderUnit = merchantStoreFenceUnit(o.DeploymentID) },
		"holder invocation":         func(o *productionMerchantStoreFenceOwner) { o.HolderInvocationID = strings.Repeat("a", 32) },
		"missing backend PID":       func(o *productionMerchantStoreFenceOwner) { o.BackendPID = 0 },
		"cluster":                   func(o *productionMerchantStoreFenceOwner) { o.SystemIdentifier = "1" },
		"database":                  func(o *productionMerchantStoreFenceOwner) { o.Database = "another_database" },
		"database OID":              func(o *productionMerchantStoreFenceOwner) { o.DatabaseOID++ },
		"schema":                    func(o *productionMerchantStoreFenceOwner) { o.Schema = "another_schema" },
		"schema OID":                func(o *productionMerchantStoreFenceOwner) { o.SchemaOID++ },
		"business role":             func(o *productionMerchantStoreFenceOwner) { o.Role = "another_role" },
		"purpose":                   func(o *productionMerchantStoreFenceOwner) { o.Purpose = "portable-deploy" },
		"service":                   func(o *productionMerchantStoreFenceOwner) { o.Service = "other.service" },
		"ordinary start invocation": func(o *productionMerchantStoreFenceOwner) { o.StartInvocationID = strings.Repeat("a", 32) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			owner := f.owner
			mutate(&owner)
			raw, err := json.Marshal(owner)
			if err != nil {
				t.Fatal(err)
			}
			if validateSchemaMaintenanceDeadOriginalOwner(raw, f.authority.Plan, f.authority.PrePlanSHA256, f.authority.OriginalDaemon, f.artifacts, f.runtime.paths.Service) == nil {
				t.Fatal("changed original owner wire or known binding accepted")
			}
		})
	}
	for _, raw := range [][]byte{nil, []byte("{"), append([]byte(" "), f.authority.OriginalOwnerRaw...), append(append([]byte{}, f.authority.OriginalOwnerRaw...), '\n'), bytes.Replace(f.authority.OriginalOwnerRaw, []byte(`"format":1`), []byte(`"unknown":1,"format":1`), 1)} {
		if validateSchemaMaintenanceDeadOriginalOwner(raw, f.authority.Plan, f.authority.PrePlanSHA256, f.authority.OriginalDaemon, f.artifacts, f.runtime.paths.Service) == nil {
			t.Fatal("noncanonical/unknown owner authority accepted")
		}
	}
	missing := cloneMerchantCapsule(f.artifacts)
	missing.Writer = nil
	if validateSchemaMaintenanceDeadOriginalOwner(f.authority.OriginalOwnerRaw, f.authority.Plan, f.authority.PrePlanSHA256, f.authority.OriginalDaemon, missing, f.runtime.paths.Service) == nil {
		t.Fatal("owner bound without an artifact writer")
	}
}

func TestSchemaMaintenanceDeadRuntimeArtifactsProjectWorkerAndRetainedRollback(t *testing.T) {
	f := newSchemaMaintenanceDeadRuntimeFixture(t)
	calls := 0
	f.runtime.runner = existingSchemaTestRunner{run: func(productionCommand) ([]byte, error) {
		calls++
		return nil, errors.New("unexpected qualification command")
	}}
	got, err := f.runtime.schemaMaintenanceDeadArtifacts(context.Background(), f.authority.Plan, f.authority.PrePlanSHA256, f.authority.OriginalDaemon)
	if err != nil || got.Candidate != f.authority.Plan.Worker.Provider || got.Rollback != merchantStoreCapsuleProjection(f.authority.Plan.Origins[0].Recovery.Candidate, "rollback") {
		t.Fatalf("exact worker/retained rollback projection: %v", err)
	}
	if calls != 0 {
		t.Fatal("artifact declaration reader unexpectedly executed a command")
	}
	for _, change := range []string{"daemon preplan", "daemon transition", "daemon executable", "daemon PID", "daemon start ticks", "changed plan bytes", "changed capsule bytes"} {
		t.Run(change, func(t *testing.T) {
			f := newSchemaMaintenanceDeadRuntimeFixture(t)
			daemon := f.authority.OriginalDaemon
			switch change {
			case "daemon preplan":
				daemon.PlanSHA256 = strings.Repeat("f", 64)
			case "daemon transition":
				daemon.TransitionID = "other-transition"
			case "daemon executable":
				daemon.DaemonExeSHA256 = f.authority.Plan.Origins[0].Recovery.Candidate.DeployEngineSHA256
			case "daemon PID":
				daemon.DaemonPID = 0
			case "daemon start ticks":
				daemon.DaemonStartTicks = 0
			case "changed plan bytes":
				schemaMaintenanceRecoveryWriteFile(t, filepath.Join(schemaMaintenanceHostWorkspace(f.runtime.paths, f.authority.Plan.TransitionID), "schema-maintenance-plan.json"), []byte("{}\n"))
			case "changed capsule bytes":
				changed := cloneMerchantCapsule(f.artifacts)
				changed.Candidate.DeployEngineSHA256 = strings.Repeat("f", 64)
				schemaMaintenanceRecoveryWriteJSON(t, f.authority.Plan.Origins[0].ArtifactCapsule.Path, changed)
			}
			if _, err := f.runtime.schemaMaintenanceDeadArtifacts(context.Background(), f.authority.Plan, f.authority.PrePlanSHA256, daemon); err == nil {
				t.Fatal("changed immutable artifact/daemon authority accepted")
			}
		})
	}
}

func TestSchemaMaintenanceDeadRuntimePriorCASObjectsStopBeforeAllCallbacks(t *testing.T) {
	for _, name := range []string{"dead-preworker-cas-intent.json", "dead-preworker-cas-result.json", "dead-preworker-cas-reconciled.json"} {
		for _, kind := range []string{"zero byte", "malformed", "directory", "dangling symlink", "symlink", "FIFO"} {
			t.Run(name+"/"+kind, func(t *testing.T) {
				runtime, plan, _ := newSchemaMaintenancePlanFixture(t)
				path := filepath.Join(schemaMaintenanceHostWorkspace(runtime.paths, plan.TransitionID), "state", name)
				switch kind {
				case "zero byte":
					schemaMaintenanceRecoveryWriteFile(t, path, nil)
				case "malformed":
					schemaMaintenanceRecoveryWriteFile(t, path, []byte("{"))
				case "FIFO":
					if err := syscall.Mkfifo(path, 0600); err != nil {
						t.Fatal(err)
					}
				case "directory":
					if err := os.Mkdir(path, 0700); err != nil {
						t.Fatal(err)
					}
				case "dangling symlink":
					if err := os.Symlink(filepath.Join(t.TempDir(), "absent"), path); err != nil {
						t.Fatal(err)
					}
				case "symlink":
					target := filepath.Join(t.TempDir(), "target")
					schemaMaintenanceRecoveryWriteFile(t, target, []byte("{}\n"))
					if err := os.Symlink(target, path); err != nil {
						t.Fatal(err)
					}
				}
				calls, deadChecks, uidChecks, hostChecks, clockChecks := 0, 0, 0, 0, 0
				runtime.runner = existingSchemaTestRunner{run: func(productionCommand) ([]byte, error) { calls++; return nil, errors.New("unexpected command") }}
				runtime.effectiveUID = func() int { uidChecks++; return 0 }
				runtime.hostname = func() (string, error) { hostChecks++; return plan.PrimaryHost, nil }
				runtime.now = func() time.Time { clockChecks++; return time.Now() }
				authority := productionSchemaMaintenanceDeadPreWorkerCASAuthority{Plan: plan, CheckLocalDeadAuthority: func(context.Context) error { deadChecks++; return nil }}
				if _, err := runtime.recoverSchemaMaintenanceDeadPreWorkerOwner(context.Background(), authority); err == nil {
					t.Fatal("existing uncertain CAS object allowed replay")
				}
				if calls != 0 || deadChecks != 0 || uidChecks != 0 || hostChecks != 0 || clockChecks != 0 {
					t.Fatalf("existing intent reached callbacks: commands=%d dead=%d uid=%d host=%d clock=%d", calls, deadChecks, uidChecks, hostChecks, clockChecks)
				}
			})
		}
	}
}

func TestSchemaMaintenanceDeadRuntimeNoWorkerRequiresExactAbsence(t *testing.T) {
	names := []string{"schema-worker-intent.json", "schema-worker-result.json", "schema-worker-output.log", "schema-outcome.json", "owner-released.json", "recovery-binding.json", "recovery-startup-profile.json", "recovery-hook-intent.json", "recovery-hook-bound.json", "recovery-start-intent.json", "recovery-started.json", "host-finished.json", "abort-binding.json", "abort-start-intent.json", "abort-started.json", "abort-host-finished.json", "abort-finish-intent.json"}
	for _, name := range names {
		for _, kind := range []string{"zero byte", "malformed", "directory", "dangling symlink", "FIFO"} {
			t.Run(name+"/"+kind, func(t *testing.T) {
				runtime, plan, _ := newSchemaMaintenancePlanFixture(t)
				workspace := schemaMaintenanceHostWorkspace(runtime.paths, plan.TransitionID)
				if err := runtime.checkSchemaMaintenanceDeadNoWorker(workspace); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(workspace, "state", name)
				switch kind {
				case "zero byte":
					schemaMaintenanceRecoveryWriteFile(t, path, nil)
				case "malformed":
					schemaMaintenanceRecoveryWriteFile(t, path, []byte("{"))
				case "FIFO":
					if err := syscall.Mkfifo(path, 0600); err != nil {
						t.Fatal(err)
					}
				case "directory":
					if err := os.Mkdir(path, 0700); err != nil {
						t.Fatal(err)
					}
				case "dangling symlink":
					if err := os.Symlink(filepath.Join(t.TempDir(), "absent"), path); err != nil {
						t.Fatal(err)
					}
				}
				if runtime.checkSchemaMaintenanceDeadNoWorker(workspace) == nil {
					t.Fatal("unsafe possible worker/recovery evidence classified as absence")
				}
			})
		}
	}
	// These belong to the separate claim/preparation authority. The narrow
	// no-worker predicate must not confuse their presence with work having
	// started; accepting them here does not validate their contents.
	for _, name := range []string{"schema-claim-intent.json", "schema-owner.json", "abort-prepared.json"} {
		t.Run("non-worker/"+name, func(t *testing.T) {
			runtime, plan, _ := newSchemaMaintenancePlanFixture(t)
			workspace := schemaMaintenanceHostWorkspace(runtime.paths, plan.TransitionID)
			schemaMaintenanceRecoveryWriteFile(t, filepath.Join(workspace, "state", name), nil)
			if err := runtime.checkSchemaMaintenanceDeadNoWorker(workspace); err != nil {
				t.Fatal("preparation/claim marker was misclassified as worker evidence")
			}
		})
	}
	for _, kind := range []string{"missing state", "symlink state", "file state", "replaceable state"} {
		t.Run(kind, func(t *testing.T) {
			runtime, plan, _ := newSchemaMaintenancePlanFixture(t)
			workspace := schemaMaintenanceHostWorkspace(runtime.paths, plan.TransitionID)
			state := filepath.Join(workspace, "state")
			if kind == "replaceable state" {
				if err := os.Chmod(state, 0777); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Remove(state); err != nil {
					t.Fatal(err)
				}
				if kind == "symlink state" {
					if err := os.Symlink(filepath.Join(t.TempDir(), "absent"), state); err != nil {
						t.Fatal(err)
					}
				}
				if kind == "file state" {
					schemaMaintenanceRecoveryWriteFile(t, state, nil)
				}
			}
			if runtime.checkSchemaMaintenanceDeadNoWorker(workspace) == nil {
				t.Fatal("unsafe state ancestor classified as worker absence")
			}
		})
	}
}

func TestSchemaMaintenanceDeadRuntimeNewResultRetainsCompleteIntentProjection(t *testing.T) {
	f := newSchemaMaintenanceDeadRuntimeFixture(t)
	invocation := strings.Repeat("a", 32)
	t.Setenv("INVOCATION_ID", invocation)
	now := time.Date(2026, 10, 12, 15, 4, 5, 123, time.FixedZone("fixture", 8*60*60))
	got := newSchemaMaintenanceDeadCASResult(f.authority, now)
	expected := productionSchemaMaintenanceDeadPreWorkerCASResult{Format: "lmm-schema-maintenance-dead-preworker-cas-v1", TransitionID: f.authority.Plan.TransitionID, PrePlanSHA256: f.authority.PrePlanSHA256, Mode: schemaMaintenanceDeadPreWorkerExactOwner, OriginalDaemon: f.authority.OriginalDaemon, OriginalOwnerSHA256: startupContentSHA256(f.authority.OriginalOwnerRaw), OldCapsule: productionReleaseFilePlan{Path: filepath.Join(f.authority.OldCapsule.Root, "capsule.json"), SHA256: f.authority.OldCapsuleSHA256}, PreSchema: f.authority.Plan.PreSchema, ProviderSHA256: f.authority.Plan.Worker.Provider.PayloadSHA256, RecovererEXESHA256: f.authority.Plan.Worker.Provider.DeployEngineSHA256, RecovererPID: os.Getpid(), RecovererInvocationID: invocation, StartedUTC: now.UTC(), Outcome: "CAS_INTENT"}
	if got != expected || got.StartedUTC.Location() != time.UTC {
		t.Fatal("CAS intent lost or invented authority, completion, or current recoverer fields")
	}
	zero := f.authority
	zero.OwnerMode = schemaMaintenanceDeadPreWorkerZeroOwner
	zero.OriginalOwnerRaw = nil
	zeroResult := newSchemaMaintenanceDeadCASResult(zero, now)
	expected.Mode = schemaMaintenanceDeadPreWorkerZeroOwner
	expected.OriginalOwnerSHA256 = ""
	if zeroResult != expected {
		t.Fatal("explicit ZERO intent invented an original owner digest")
	}
}
