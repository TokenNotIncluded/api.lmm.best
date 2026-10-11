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

func schemaMaintenanceRecoveryWriterTarget(p productionReleasePackagePlan) productionMerchantStoreWriterTarget {
	return productionMerchantStoreWriterTarget{Capability: p.MerchantStoreWriterCapability, PackageSHA256: p.PackageSHA256, PayloadSHA256: p.PayloadSHA256,
		SourceRevision: p.GitRevision, ReleaseAssetSHA256: p.ReleaseAssetSHA256, StatusSHA256: startupContentSHA256([]byte(p.Version + ":status"))}
}

func newSchemaMaintenanceRecoveryFixture(t *testing.T) (*productionSchemaMaintenanceHost, productionSchemaMaintenancePlan, string, productionSchemaMaintenanceRecoveryBinding, productionMerchantStoreCapsule) {
	t.Helper()
	runtime, plan, planPath := newSchemaMaintenancePlanFixture(t)
	// The transition worker is cap8. The retained recovery pair and its actual
	// floor are cap7; this does not lower a production capability or any row.
	plan.RequiredCapability = 7
	for i := range plan.Origins {
		plan.Origins[i].Recovery.Candidate.MerchantStoreWriterCapability = 7
		plan.Origins[i].Recovery.Rollback.MerchantStoreWriterCapability = 7
	}
	binding, preSHA := schemaMaintenanceRecoveryBindingFixture(t, runtime, plan)
	host, origin := plan.Origins[0], binding.Origins[0]
	h := &productionSchemaMaintenanceHost{runtime: runtime, lease: &schemaMaintenanceTestLease{}, validate: func(context.Context) error { return nil },
		binding: productionSchemaMaintenanceHostBinding{TransitionID: plan.TransitionID, PlanPath: planPath, PlanSHA256: preSHA, Host: host.Host,
			CapsulePath: host.OldCapsule.Path, CapsuleSHA256: host.OldCapsule.SHA256, GuardianSocket: host.Guardian.Socket,
			GuardianBindingPath: host.Guardian.BindingPath, GuardianBindingSHA256: strings.Repeat("6", 64), GuardianProtocol: host.Guardian.Protocol, Workspace: host.Workspace}}
	c := testMerchantCapsule(t, runtime.paths)
	c.DeploymentID, c.Root, c.Host, c.ControllerPlanSHA256 = host.Recovery.DeploymentID, filepath.Dir(origin.Capsule.Path), host.Host, origin.RecoveryPlan.SHA256
	c.Candidate, c.Rollback = host.Recovery.Candidate, host.Recovery.Rollback
	schema := binding.PostSchema
	schema.StartupSHA256, schema.SignedUnitSHA256 = origin.StartupSHA256, origin.SignedUnitSHA256
	c.ExistingSchemaContract = &schema
	c.Writer = &productionMerchantStoreWriterContract{Format: 1, RequiredCapability: plan.RequiredCapability, SystemIdentifier: schema.SystemIdentifier,
		Database: schema.Database, DatabaseOID: schema.DatabaseOID, Schema: schema.Schema, SchemaOID: schema.SchemaOID, Role: plan.Role,
		StartupSHA256: schema.StartupSHA256, SignedUnitSHA256: schema.SignedUnitSHA256, RecoveryPolicy: "same-floor-writable",
		Candidate: schemaMaintenanceRecoveryWriterTarget(c.Candidate), Rollback: schemaMaintenanceRecoveryWriterTarget(c.Rollback)}
	if err := validateSchemaMaintenanceRecoveryCapsule(c, host, origin, plan, binding, runtime.paths); err != nil {
		t.Fatalf("recovery fixture: %v", err)
	}
	return h, plan, preSHA, binding, c
}

func TestSchemaMaintenanceRecoveryCapsuleFixesRetainedPairAndPostCatalog(t *testing.T) {
	h, plan, _, binding, good := newSchemaMaintenanceRecoveryFixture(t)
	cases := map[string]func(*productionMerchantStoreCapsule){
		"latest worker substituted": func(c *productionMerchantStoreCapsule) {
			c.Candidate = plan.Worker.Provider
			c.Writer.Candidate = schemaMaintenanceRecoveryWriterTarget(c.Candidate)
		},
		"rollback substituted": func(c *productionMerchantStoreCapsule) {
			c.Rollback = schemaMaintenancePlanProvider("0.2.109", "rollback")
			c.Rollback.MerchantStoreWriterCapability = 7
			c.Writer.Rollback = schemaMaintenanceRecoveryWriterTarget(c.Rollback)
		},
		"separate engine substituted": func(c *productionMerchantStoreCapsule) { c.Candidate.DeployEngineSHA256 = strings.Repeat("a", 64) },
		"signature substituted":       func(c *productionMerchantStoreCapsule) { c.Rollback.SignatureBundleSHA256 = strings.Repeat("a", 64) },
		"original catalog reused": func(c *productionMerchantStoreCapsule) {
			c.ExistingSchemaContract.MetadataSHA256 = plan.PreSchema.MetadataSHA256
		},
		"foreign physical cluster": func(c *productionMerchantStoreCapsule) {
			c.ExistingSchemaContract.SystemIdentifier, c.Writer.SystemIdentifier = "1", "1"
		},
		"foreign database": func(c *productionMerchantStoreCapsule) {
			c.ExistingSchemaContract.DatabaseOID++
			c.Writer.DatabaseOID++
		},
		"foreign schema": func(c *productionMerchantStoreCapsule) { c.ExistingSchemaContract.SchemaOID++; c.Writer.SchemaOID++ },
		"foreign role":   func(c *productionMerchantStoreCapsule) { c.Writer.Role = "another_role" },
		"different loaded startup": func(c *productionMerchantStoreCapsule) {
			c.ExistingSchemaContract.StartupSHA256, c.Writer.StartupSHA256 = strings.Repeat("a", 64), strings.Repeat("a", 64)
		},
		"different signed unit": func(c *productionMerchantStoreCapsule) {
			c.ExistingSchemaContract.SignedUnitSHA256, c.Writer.SignedUnitSHA256 = strings.Repeat("a", 64), strings.Repeat("a", 64)
		},
		"held startup":        func(c *productionMerchantStoreCapsule) { c.StartupPolicy = "held" },
		"wrong ordinary plan": func(c *productionMerchantStoreCapsule) { c.ControllerPlanSHA256 = strings.Repeat("a", 64) },
		"other host":          func(c *productionMerchantStoreCapsule) { c.Host = plan.Origins[1].Host },
		"old capsule reused": func(c *productionMerchantStoreCapsule) {
			c.DeploymentID = "old-" + c.Host
			c.Root = merchantStoreCapsuleRoot(h.runtime.paths, c.DeploymentID)
		},
		"root redirected":      func(c *productionMerchantStoreCapsule) { c.Root = filepath.Join(t.TempDir(), "other") },
		"baseline substituted": func(c *productionMerchantStoreCapsule) { *c = testMerchantStartupBaseline(t, h.runtime.paths) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			c := cloneMerchantCapsule(good)
			mutate(&c)
			if err := validateSchemaMaintenanceRecoveryCapsule(c, plan.Origins[0], binding.Origins[0], plan, binding, h.runtime.paths); err == nil {
				t.Fatal("accepted a changed recovery provider or post-DDL startup authority")
			}
		})
	}
}

func TestSchemaMaintenanceRecoveryCapsuleRejectsOtherwiseValidCapEightFloor(t *testing.T) {
	h, plan, _, binding, c := newSchemaMaintenanceRecoveryFixture(t)
	plan.RequiredCapability, c.Writer.RequiredCapability = 8, 8
	for i := range plan.Origins {
		plan.Origins[i].Recovery.Candidate.MerchantStoreWriterCapability = 8
		plan.Origins[i].Recovery.Rollback.MerchantStoreWriterCapability = 8
	}
	c.Candidate, c.Rollback = plan.Origins[0].Recovery.Candidate, plan.Origins[0].Recovery.Rollback
	c.Writer.Candidate, c.Writer.Rollback = schemaMaintenanceRecoveryWriterTarget(c.Candidate), schemaMaintenanceRecoveryWriterTarget(c.Rollback)
	if err := validateMerchantStoreCapsule(c, h.runtime.paths); err != nil {
		t.Fatalf("ordinary capsule must remain independently valid: %v", err)
	}
	if err := validateSchemaMaintenanceRecoveryCapsule(c, plan.Origins[0], binding.Origins[0], plan, binding, h.runtime.paths); err == nil {
		t.Fatal("cap8 floor bypassed the explicit N-1 recovery ceiling")
	}
}

func schemaMaintenanceRecoveryReleasePlanFixture(t *testing.T, h *productionSchemaMaintenanceHost, plan productionSchemaMaintenancePlan, binding *productionSchemaMaintenanceRecoveryBinding, c *productionMerchantStoreCapsule) productionReleasePlan {
	t.Helper()
	host := plan.Origins[0]
	release := testExistingSchemaReleasePlan(t)
	release.DeploymentID, release.ControllerWorkspace = host.Recovery.DeploymentID, host.Recovery.ControllerWorkspace
	release.Repository = filepath.Join(release.ControllerWorkspace, "repo")
	release.ExpectedVersion = "0.2.111"
	release.GoCandidate, release.GoRollback = c.Candidate, c.Rollback
	for _, pair := range []struct {
		p    *productionReleasePackagePlan
		role string
	}{{&release.GoCandidate, "candidate"}, {&release.GoRollback, "rollback"}} {
		pair.p.PackagePath = filepath.Join(release.ControllerWorkspace, "go-"+pair.role+".pkg.tar.zst")
		pair.p.ReleaseAsset = filepath.Join(release.ControllerWorkspace, "go-"+pair.role+".tar.gz")
		pair.p.SignatureBundle = filepath.Join(release.ControllerWorkspace, "go-"+pair.role+".sigstore.json")
	}
	release.WebCandidate.ContractRevision, release.WebRollback.ContractRevision = c.Candidate.ContractRevision, c.Rollback.ContractRevision
	release.ProbeBinary = productionReleaseFilePlan{Path: filepath.Join(release.ControllerWorkspace, backendGoName), SHA256: c.Candidate.PayloadSHA256}
	release.OperatorBinary = productionReleaseFilePlan{Path: filepath.Join(release.ControllerWorkspace, deployEngineName), SHA256: c.Candidate.DeployEngineSHA256}
	release.ExistingSchemaContract = c.ExistingSchemaContract
	release.MerchantStoreWriter = c.Writer
	raw, err := canonicalProductionReleasePlan(release)
	if err != nil || validateProductionReleasePlan(release) != nil {
		t.Fatalf("ordinary recovery plan fixture: canonical=%v validation=%v", err, validateProductionReleasePlan(release))
	}
	binding.Origins[0].RecoveryPlan.SHA256, c.ControllerPlanSHA256 = startupContentSHA256(raw), startupContentSHA256(raw)
	schemaMaintenanceRecoveryWriteFile(t, filepath.Join(h.binding.Workspace, "state", "recovery-release-plan.json"), raw)
	return release
}

func schemaMaintenanceRecoveryWriteFile(t *testing.T, path string, raw []byte) {
	t.Helper()
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
}

func schemaMaintenanceRecoveryWriteJSON(t *testing.T, path string, value any) []byte {
	t.Helper()
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, '\n')
	schemaMaintenanceRecoveryWriteFile(t, path, raw)
	return raw
}

func TestSchemaMaintenanceRecoveryRequiresExactStagedOrdinaryPlan(t *testing.T) {
	h, plan, _, binding, c := newSchemaMaintenanceRecoveryFixture(t)
	schemaMaintenanceRecoveryReleasePlanFixture(t, h, plan, &binding, &c)
	if err := h.loadRecoveryReleasePlan(plan.Origins[0], binding.Origins[0], c); err != nil {
		t.Fatalf("exact staged copy of original controller plan: %v", err)
	}
	// Host startup bytes may differ from the controller host. The capsule's
	// observed host binding remains exact; the shared catalog and signed unit do
	// not change. This is the existing ordinary derive projection contract.
	c = cloneMerchantCapsule(c)
	c.ExistingSchemaContract.StartupSHA256, c.Writer.StartupSHA256 = strings.Repeat("a", 64), strings.Repeat("a", 64)
	if err := h.loadRecoveryReleasePlan(plan.Origins[0], binding.Origins[0], c); err != nil {
		t.Fatalf("ordinary per-host startup projection: %v", err)
	}
}

func TestSchemaMaintenanceRecoveryStagedPlanRejectsUnsafeOrRedirectedBytes(t *testing.T) {
	cases := map[string]func(*testing.T, string, *productionReleasePlan, *productionMerchantStoreCapsule, *productionSchemaMaintenanceRecoveryOrigin){
		"missing plan": func(t *testing.T, path string, _ *productionReleasePlan, _ *productionMerchantStoreCapsule, _ *productionSchemaMaintenanceRecoveryOrigin) {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
		},
		"wrong digest": func(_ *testing.T, _ string, _ *productionReleasePlan, _ *productionMerchantStoreCapsule, origin *productionSchemaMaintenanceRecoveryOrigin) {
			origin.RecoveryPlan.SHA256 = strings.Repeat("0", 64)
		},
		"public file": func(t *testing.T, path string, _ *productionReleasePlan, _ *productionMerchantStoreCapsule, _ *productionSchemaMaintenanceRecoveryOrigin) {
			if err := os.Chmod(path, 0644); err != nil {
				t.Fatal(err)
			}
		},
		"hard linked file": func(t *testing.T, path string, _ *productionReleasePlan, _ *productionMerchantStoreCapsule, _ *productionSchemaMaintenanceRecoveryOrigin) {
			if err := os.Link(path, path+".alias"); err != nil {
				t.Fatal(err)
			}
		},
		"symlink file": func(t *testing.T, path string, _ *productionReleasePlan, _ *productionMerchantStoreCapsule, _ *productionSchemaMaintenanceRecoveryOrigin) {
			if err := os.Rename(path, path+".original"); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(path+".original", path); err != nil {
				t.Fatal(err)
			}
		},
		"nil writer": func(t *testing.T, path string, p *productionReleasePlan, c *productionMerchantStoreCapsule, origin *productionSchemaMaintenanceRecoveryOrigin) {
			p.MerchantStoreWriter = nil
			raw := schemaMaintenanceRecoveryWriteJSON(t, path, p)
			origin.RecoveryPlan.SHA256, c.ControllerPlanSHA256 = startupContentSHA256(raw), startupContentSHA256(raw)
		},
		"different original workspace": func(t *testing.T, path string, p *productionReleasePlan, c *productionMerchantStoreCapsule, origin *productionSchemaMaintenanceRecoveryOrigin) {
			p.ControllerWorkspace += "-other"
			raw := schemaMaintenanceRecoveryWriteJSON(t, path, p)
			origin.RecoveryPlan.SHA256, c.ControllerPlanSHA256 = startupContentSHA256(raw), startupContentSHA256(raw)
		},
		"new deployment identity": func(t *testing.T, path string, p *productionReleasePlan, c *productionMerchantStoreCapsule, origin *productionSchemaMaintenanceRecoveryOrigin) {
			p.DeploymentID += "-other"
			raw := schemaMaintenanceRecoveryWriteJSON(t, path, p)
			origin.RecoveryPlan.SHA256, c.ControllerPlanSHA256 = startupContentSHA256(raw), startupContentSHA256(raw)
		},
		"different post catalog": func(t *testing.T, path string, p *productionReleasePlan, c *productionMerchantStoreCapsule, origin *productionSchemaMaintenanceRecoveryOrigin) {
			schema := *p.ExistingSchemaContract
			schema.MetadataSHA256 = strings.Repeat("0", 64)
			p.ExistingSchemaContract = &schema
			raw := schemaMaintenanceRecoveryWriteJSON(t, path, p)
			origin.RecoveryPlan.SHA256, c.ControllerPlanSHA256 = startupContentSHA256(raw), startupContentSHA256(raw)
		},
		"different signed engine": func(t *testing.T, path string, p *productionReleasePlan, c *productionMerchantStoreCapsule, origin *productionSchemaMaintenanceRecoveryOrigin) {
			p.GoCandidate.DeployEngineSHA256, p.OperatorBinary.SHA256 = strings.Repeat("a", 64), strings.Repeat("a", 64)
			raw := schemaMaintenanceRecoveryWriteJSON(t, path, p)
			origin.RecoveryPlan.SHA256, c.ControllerPlanSHA256 = startupContentSHA256(raw), startupContentSHA256(raw)
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			h, plan, _, binding, c := newSchemaMaintenanceRecoveryFixture(t)
			release := schemaMaintenanceRecoveryReleasePlanFixture(t, h, plan, &binding, &c)
			path := filepath.Join(h.binding.Workspace, "state", "recovery-release-plan.json")
			mutate(t, path, &release, &c, &binding.Origins[0])
			if err := h.loadRecoveryReleasePlan(plan.Origins[0], binding.Origins[0], c); err == nil {
				t.Fatal("accepted an unsafe or redirected staged ordinary plan")
			}
		})
	}
	for name, mutate := range map[string]func([]byte) []byte{
		"noncanonical whitespace": func(raw []byte) []byte { return append([]byte(" "), raw...) },
		"duplicate key":           func(raw []byte) []byte { return bytes.Replace(raw, []byte("{\n"), []byte("{\n  \"format\": 7,\n"), 1) },
		"unknown field": func(raw []byte) []byte {
			return bytes.Replace(raw, []byte("{\n"), []byte("{\n  \"approval\": true,\n"), 1)
		},
		"trailing record": func(raw []byte) []byte { return append(raw, []byte("{}\n")...) },
	} {
		t.Run(name, func(t *testing.T) {
			h, plan, _, binding, c := newSchemaMaintenanceRecoveryFixture(t)
			schemaMaintenanceRecoveryReleasePlanFixture(t, h, plan, &binding, &c)
			path := filepath.Join(h.binding.Workspace, "state", "recovery-release-plan.json")
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			raw = mutate(raw)
			schemaMaintenanceRecoveryWriteFile(t, path, raw)
			binding.Origins[0].RecoveryPlan.SHA256, c.ControllerPlanSHA256 = startupContentSHA256(raw), startupContentSHA256(raw)
			if h.loadRecoveryReleasePlan(plan.Origins[0], binding.Origins[0], c) == nil {
				t.Fatal("matching digest allowed ambiguous or noncanonical plan bytes")
			}
		})
	}
}

func newSchemaMaintenanceFinishedReceiptFixture(t *testing.T) (*productionSchemaMaintenanceHost, productionSchemaMaintenancePlan, string, productionSchemaMaintenanceRecoveryBinding, productionSchemaMaintenanceHostFinished, productionSchemaMaintenanceHostCapture) {
	t.Helper()
	h, plan, preSHA, binding, c := newSchemaMaintenanceRecoveryFixture(t)
	h.lease = &schemaMaintenanceTestLease{err: errors.New("native finish closed guardian lease")}
	bindingRaw, err := canonicalProductionSchemaMaintenanceRecoveryBinding(binding, plan, preSHA, h.runtime.paths)
	if err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(h.binding.Workspace, "state")
	schemaMaintenanceRecoveryWriteFile(t, filepath.Join(state, "recovery-binding.json"), bindingRaw)
	guardian := struct {
		Format         string `json:"format"`
		TransitionID   string `json:"transition_id"`
		PlanPath       string `json:"plan_path"`
		PlanSHA256     string `json:"plan_sha256"`
		Host           string `json:"host"`
		Workspace      string `json:"workspace"`
		GuardianSocket string `json:"guardian_socket"`
	}{"lmm-schema-maintenance-lock-binding-v1", plan.TransitionID, h.binding.PlanPath, preSHA, h.binding.Host, h.binding.Workspace, h.binding.GuardianSocket}
	guardianRaw := schemaMaintenanceRecoveryWriteJSON(t, h.binding.GuardianBindingPath, guardian)
	h.binding.GuardianBindingSHA256 = startupContentSHA256(guardianRaw)
	finished := productionSchemaMaintenanceHostFinished{Format: "lmm-schema-maintenance-host-finished-v1", TransitionID: plan.TransitionID, PlanSHA256: preSHA, Host: h.binding.Host,
		GuardianBindingSHA256: h.binding.GuardianBindingSHA256, RecoveryBindingSHA256: startupContentSHA256(bindingRaw), CapsuleSHA256: binding.Origins[0].Capsule.SHA256,
		ProviderSHA256: c.Candidate.PayloadSHA256, PID: 23456, InvocationID: strings.Repeat("a", 32), FinishedUTC: time.Unix(2000, 0).UTC()}
	restored := productionSchemaMaintenanceHostCapture{Format: 1, TransitionID: plan.TransitionID, PlanSHA256: preSHA, Host: h.binding.Host, Phase: "ADMISSION_RESTORED",
		CapsuleSHA256: h.binding.CapsuleSHA256, ProviderSHA256: c.Candidate.PayloadSHA256, PID: 12345, InvocationID: strings.Repeat("b", 32),
		OriginalAdmissionSHA256: strings.Repeat("c", 64), ClosedAdmissionSHA256: strings.Repeat("d", 64), ConfirmedCapsuleSHA256: finished.CapsuleSHA256,
		ConfirmedProviderSHA256: finished.ProviderSHA256, ConfirmedPID: finished.PID, ConfirmedInvocationID: finished.InvocationID}
	schemaMaintenanceRecoveryWriteJSON(t, filepath.Join(state, "host-finished.json"), finished)
	schemaMaintenanceRecoveryWriteJSON(t, filepath.Join(state, "admission-restored.json"), restored)
	return h, plan, preSHA, binding, finished, restored
}

func TestSchemaMaintenanceFinishedReceiptGrantsReadOnlyObservationOnly(t *testing.T) {
	h, plan, preSHA, binding, finished, restored := newSchemaMaintenanceFinishedReceiptFixture(t)
	ctx := context.Background()
	observed, err := h.checkRecoveryReadyAuthority(ctx, plan, preSHA, binding)
	if err != nil || observed == nil || !reflect.DeepEqual(*observed, finished) {
		t.Fatalf("exact native finish receipt: observed=%+v error=%v", observed, err)
	}
	if h.check(ctx) == nil {
		t.Fatal("terminal read-only receipt reopened the mutating guardian authority")
	}
	// No process, network, database, or deployment command is permitted by this
	// receipt-only fixture. Actual ready capture adds those live checks itself.
	commands, callbacks := 0, 0
	h.runtime.runner = existingSchemaTestRunner{run: func(productionCommand) ([]byte, error) { commands++; return nil, errors.New("unexpected command") }}
	restored.Phase = "CLOSE_INTENT"
	schemaMaintenanceRecoveryWriteJSON(t, filepath.Join(h.binding.Workspace, "state", "close-intent.json"), restored)
	callback := func(context.Context) error { callbacks++; return nil }
	if _, err := h.closeAdmission(ctx); err == nil {
		t.Fatal("finished receipt allowed close admission")
	}
	if _, err := h.stopWriter(ctx, callback); err == nil {
		t.Fatal("finished receipt allowed stop writer")
	}
	if _, err := h.restoreAdmission(ctx, binding.Origins[0].Capsule.Path, binding.Origins[0].Capsule.SHA256, callback); err == nil {
		t.Fatal("finished receipt allowed restore admission")
	}
	if _, _, err := h.recoveryCapsule(ctx, plan, preSHA, binding, false); err == nil {
		t.Fatal("finished receipt allowed stopped recovery mutation authority")
	}
	if commands != 0 || callbacks != 0 {
		t.Fatalf("lost lease reached commands=%d callbacks=%d", commands, callbacks)
	}
	if _, err := os.Stat(filepath.Join(h.binding.Workspace, "state", "stop-intent.json")); !os.IsNotExist(err) {
		t.Fatalf("mutation wrote a stop intent: %v", err)
	}
	// The native completion receipt also survives disposing the closed lease
	// object. It still supplies no h.check or mutating authority.
	h.lease = nil
	if observed, err := h.checkRecoveryReadyAuthority(ctx, plan, preSHA, binding); err != nil || observed == nil {
		t.Fatalf("read-only finished observation after lease disposal: %v", err)
	}
	if h.check(ctx) == nil {
		t.Fatal("disposed lease was restored by the terminal receipt")
	}
}

func TestSchemaMaintenanceFinishedReceiptBindsExactAuthorityAndGeneration(t *testing.T) {
	for name, mutate := range map[string]func(*productionSchemaMaintenanceHostFinished, *productionSchemaMaintenanceHostCapture){
		"format": func(f *productionSchemaMaintenanceHostFinished, _ *productionSchemaMaintenanceHostCapture) {
			f.Format = "approval"
		},
		"transition": func(f *productionSchemaMaintenanceHostFinished, _ *productionSchemaMaintenanceHostCapture) {
			f.TransitionID += "-other"
		},
		"preplan": func(f *productionSchemaMaintenanceHostFinished, _ *productionSchemaMaintenanceHostCapture) {
			f.PlanSHA256 = strings.Repeat("0", 64)
		},
		"host": func(f *productionSchemaMaintenanceHostFinished, _ *productionSchemaMaintenanceHostCapture) {
			f.Host = "dmit-ubuntu"
		},
		"guardian hash": func(f *productionSchemaMaintenanceHostFinished, _ *productionSchemaMaintenanceHostCapture) {
			f.GuardianBindingSHA256 = strings.Repeat("0", 64)
		},
		"recovery hash": func(f *productionSchemaMaintenanceHostFinished, _ *productionSchemaMaintenanceHostCapture) {
			f.RecoveryBindingSHA256 = strings.Repeat("0", 64)
		},
		"capsule hash": func(f *productionSchemaMaintenanceHostFinished, _ *productionSchemaMaintenanceHostCapture) {
			f.CapsuleSHA256 = strings.Repeat("0", 64)
		},
		"provider hash": func(f *productionSchemaMaintenanceHostFinished, _ *productionSchemaMaintenanceHostCapture) {
			f.ProviderSHA256 = strings.Repeat("0", 64)
		},
		"invalid PID": func(f *productionSchemaMaintenanceHostFinished, _ *productionSchemaMaintenanceHostCapture) { f.PID = 1 },
		"invalid invocation": func(f *productionSchemaMaintenanceHostFinished, _ *productionSchemaMaintenanceHostCapture) {
			f.InvocationID = "unknown"
		},
		"missing finish time": func(f *productionSchemaMaintenanceHostFinished, _ *productionSchemaMaintenanceHostCapture) {
			f.FinishedUTC = time.Time{}
		},
		"restored phase": func(_ *productionSchemaMaintenanceHostFinished, r *productionSchemaMaintenanceHostCapture) {
			r.Phase = "RECOVERY_RUNNING"
		},
		"restored capsule": func(_ *productionSchemaMaintenanceHostFinished, r *productionSchemaMaintenanceHostCapture) {
			r.ConfirmedCapsuleSHA256 = strings.Repeat("0", 64)
		},
		"restored provider": func(_ *productionSchemaMaintenanceHostFinished, r *productionSchemaMaintenanceHostCapture) {
			r.ConfirmedProviderSHA256 = strings.Repeat("0", 64)
		},
		"restored PID": func(_ *productionSchemaMaintenanceHostFinished, r *productionSchemaMaintenanceHostCapture) {
			r.ConfirmedPID++
		},
		"restored invocation": func(_ *productionSchemaMaintenanceHostFinished, r *productionSchemaMaintenanceHostCapture) {
			r.ConfirmedInvocationID = strings.Repeat("0", 32)
		},
		"restored original capsule": func(_ *productionSchemaMaintenanceHostFinished, r *productionSchemaMaintenanceHostCapture) {
			r.CapsuleSHA256 = strings.Repeat("0", 64)
		},
	} {
		t.Run(name, func(t *testing.T) {
			h, plan, preSHA, binding, finished, restored := newSchemaMaintenanceFinishedReceiptFixture(t)
			mutate(&finished, &restored)
			state := filepath.Join(h.binding.Workspace, "state")
			schemaMaintenanceRecoveryWriteJSON(t, filepath.Join(state, "host-finished.json"), finished)
			schemaMaintenanceRecoveryWriteJSON(t, filepath.Join(state, "admission-restored.json"), restored)
			if _, err := h.checkRecoveryReadyAuthority(context.Background(), plan, preSHA, binding); err == nil {
				t.Fatal("accepted a redirected finish authority or changed restored generation")
			}
		})
	}
}

func TestSchemaMaintenanceFinishedReceiptRequiresPrivateCanonicalActualFiles(t *testing.T) {
	for name, mutate := range map[string]func(*testing.T, string, []byte){
		"missing": func(t *testing.T, path string, _ []byte) {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
		},
		"public": func(t *testing.T, path string, _ []byte) {
			if err := os.Chmod(path, 0644); err != nil {
				t.Fatal(err)
			}
		},
		"hard link": func(t *testing.T, path string, _ []byte) {
			if err := os.Link(path, path+".alias"); err != nil {
				t.Fatal(err)
			}
		},
		"symlink": func(t *testing.T, path string, _ []byte) {
			if err := os.Rename(path, path+".original"); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(path+".original", path); err != nil {
				t.Fatal(err)
			}
		},
		"noncanonical": func(t *testing.T, path string, raw []byte) {
			schemaMaintenanceRecoveryWriteFile(t, path, append([]byte(" "), raw...))
		},
		"trailing": func(t *testing.T, path string, raw []byte) {
			schemaMaintenanceRecoveryWriteFile(t, path, append(raw, []byte("{}\n")...))
		},
		"duplicate": func(t *testing.T, path string, raw []byte) {
			schemaMaintenanceRecoveryWriteFile(t, path, bytes.Replace(raw, []byte("{\n"), []byte("{\n  \"format\": 1,\n"), 1))
		},
		"unknown": func(t *testing.T, path string, raw []byte) {
			schemaMaintenanceRecoveryWriteFile(t, path, bytes.Replace(raw, []byte("{\n"), []byte("{\n  \"approved\": true,\n"), 1))
		},
	} {
		for _, filename := range []string{"host-finished.json", "guardian-binding.json", "recovery-binding.json", "admission-restored.json"} {
			t.Run(filename+"/"+name, func(t *testing.T) {
				h, plan, preSHA, binding, _, _ := newSchemaMaintenanceFinishedReceiptFixture(t)
				path := filepath.Join(h.binding.Workspace, "state", filename)
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				mutate(t, path, raw)
				if _, err := h.checkRecoveryReadyAuthority(context.Background(), plan, preSHA, binding); err == nil {
					t.Fatal("unsafe or ambiguous finish evidence permitted observation authority")
				}
			})
		}
	}
	t.Run("original plan validator fails", func(t *testing.T) {
		h, plan, preSHA, binding, _, _ := newSchemaMaintenanceFinishedReceiptFixture(t)
		h.validate = func(context.Context) error { return errors.New("original plan changed") }
		if _, err := h.checkRecoveryReadyAuthority(context.Background(), plan, preSHA, binding); err == nil {
			t.Fatal("finish receipt bypassed original plan validation")
		}
	})
}
