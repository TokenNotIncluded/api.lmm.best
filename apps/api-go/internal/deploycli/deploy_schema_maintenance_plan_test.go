package deploycli

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func schemaMaintenancePlanProvider(version, role string) productionReleasePackagePlan {
	hash := func(kind string) string { return startupContentSHA256([]byte(version + ":" + kind)) }
	return productionReleasePackagePlan{
		Name: productionAURPackageName, Version: version + "-1", Identity: productionAURPackageName + " " + version + "-1",
		ReleaseTag: "go-v" + version, Workflow: "release-go.yml", GitRevision: hash("source")[:40], ContractRevision: "contract-v1",
		MerchantStoreWriterCapability: 8, PackageSHA256: hash("package"), PayloadSHA256: hash("payload"), DeployEngineSHA256: hash("engine"),
		ReleaseAssetSHA256: hash("release"), SignatureBundleSHA256: hash("signature"), PackagePath: merchantStoreCapsuleAsset(role, "pkg.tar.zst"),
		ReleaseAsset: merchantStoreCapsuleAsset(role, "release.tar.gz"), SignatureBundle: merchantStoreCapsuleAsset(role, "sigstore.json"),
	}
}

func newSchemaMaintenancePlanFixture(t *testing.T) (*productionRuntime, productionSchemaMaintenancePlan, string) {
	t.Helper()
	paths := defaultProductionPaths()
	paths.WorkRoot = filepath.Join(t.TempDir(), "native")
	runtime := &productionRuntime{paths: paths, requiredOwnerUID: uint32(os.Geteuid())}
	id := "schema-transition-test"
	workspace := schemaMaintenanceHostWorkspace(paths, id)
	if err := os.MkdirAll(filepath.Join(workspace, "state"), 0700); err != nil {
		t.Fatal(err)
	}
	recovery := productionSchemaMaintenanceRecoveryRecipe{
		DeploymentID: "schema-recovery-test", ControllerWorkspace: filepath.Join(workspace, "recovery", "controller-workspace"),
		CapsulePath: filepath.Join(merchantStoreCapsuleRoot(paths, "schema-recovery-test"), "capsule.json"),
		Candidate:   schemaMaintenancePlanProvider("0.2.111", "candidate"), Rollback: schemaMaintenancePlanProvider("0.2.110", "rollback"),
	}
	recovery.PlanPath = filepath.Join(recovery.ControllerWorkspace, productionReleasePlanFilename)
	plan := productionSchemaMaintenancePlan{
		Format: schemaMaintenancePlanFormat, TransitionID: id, PrimaryAlias: "ArchDmit", PrimaryHost: "arch-dmit", PreSchema: *testExistingSchemaContract(t), Role: "lmm", RequiredCapability: 8,
		Worker:       productionSchemaMaintenanceWorkerPlan{Kind: schemaMaintenanceWorkerKind, Provider: schemaMaintenancePlanProvider("0.2.112", "candidate"), RWEnvironment: productionReleaseFilePlan{Path: filepath.Join(workspace, "state", "schema-worker.env"), SHA256: strings.Repeat("a", 64)}},
		BackupPolicy: productionSchemaMaintenanceBackupPolicy{Kind: "controller-only-encrypted-quiesced-v1", ControllerDirectory: "/home/lightjunction/Backups/schema-maintenance/test", ControllerPublicKey: hex.EncodeToString(ed25519.NewKeyFromSeed(bytes.Repeat([]byte{1}, 32)).Public().(ed25519.PublicKey)), RecipientSHA256: strings.Repeat("b", 64), RestoreProofPath: "/home/lightjunction/Backups/schema-maintenance/test/restore-proof.json"},
	}
	for i, registered := range merchantStartupOrigins {
		kind := "package"
		if i == 1 {
			kind = "standalone"
		}
		plan.Origins = append(plan.Origins, productionSchemaMaintenanceOriginPlan{
			Alias: registered.Alias, Host: registered.Host, InstallKind: kind, Workspace: workspace,
			OldCapsule:      productionReleaseFilePlan{Path: filepath.Join(merchantStoreCapsuleRoot(paths, "old-"+registered.Host), "capsule.json"), SHA256: strings.Repeat("c", 64)},
			ArtifactCapsule: productionReleaseFilePlan{Path: filepath.Join(merchantStoreCapsuleRoot(paths, id), "capsule.json"), SHA256: strings.Repeat("d", 64)},
			Guardian:        productionSchemaMaintenanceGuardianRecipe{Protocol: schemaMaintenanceGuardianProtocol, BindingPath: filepath.Join(workspace, "state", "guardian-binding.json"), Socket: filepath.Join(workspace, "state", "guardian.sock"), Locks: schemaMaintenanceNativeLocks()},
			Recovery:        recovery,
		})
	}
	return runtime, plan, filepath.Join(workspace, "schema-maintenance-plan.json")
}

func cloneSchemaMaintenancePlan(t *testing.T, plan productionSchemaMaintenancePlan) productionSchemaMaintenancePlan {
	t.Helper()
	raw, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	var copied productionSchemaMaintenancePlan
	if err := json.Unmarshal(raw, &copied); err != nil {
		t.Fatal(err)
	}
	return copied
}

func TestSchemaMaintenancePlanFixedOriginAndAcyclicRecipes(t *testing.T) {
	runtime, good, _ := newSchemaMaintenancePlanFixture(t)
	if err := validateProductionSchemaMaintenancePlan(good, runtime.paths); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*productionSchemaMaintenancePlan){
		"missing origin":      func(p *productionSchemaMaintenancePlan) { p.Origins = p.Origins[:1] },
		"reordered origins":   func(p *productionSchemaMaintenancePlan) { p.Origins[0], p.Origins[1] = p.Origins[1], p.Origins[0] },
		"foreign primary":     func(p *productionSchemaMaintenancePlan) { p.PrimaryAlias = "DmitUbuntu" },
		"foreign host":        func(p *productionSchemaMaintenancePlan) { p.Origins[0].Host = "another-host" },
		"wrong install owner": func(p *productionSchemaMaintenancePlan) { p.Origins[1].InstallKind = "package" },
		"workspace escape":    func(p *productionSchemaMaintenancePlan) { p.Origins[0].Workspace += "/../other" },
		"old capsule overwrite": func(p *productionSchemaMaintenancePlan) {
			p.Origins[0].OldCapsule.Path = p.Origins[0].ArtifactCapsule.Path
		},
		"unsealed old capsule": func(p *productionSchemaMaintenancePlan) { p.Origins[0].OldCapsule.SHA256 = "" },
		"foreign artifact root": func(p *productionSchemaMaintenancePlan) {
			p.Origins[0].ArtifactCapsule.Path = p.Origins[0].OldCapsule.Path
		},
		"financial guardian":      func(p *productionSchemaMaintenancePlan) { p.Origins[0].Guardian.Protocol = "lmm-financial-handoff-v1" },
		"guardian binding escape": func(p *productionSchemaMaintenancePlan) { p.Origins[0].Guardian.BindingPath += "/../other" },
		"missing native lock": func(p *productionSchemaMaintenancePlan) {
			p.Origins[0].Guardian.Locks = p.Origins[0].Guardian.Locks[:2]
		},
		"swapped native locks": func(p *productionSchemaMaintenancePlan) {
			p.Origins[0].Guardian.Locks[0], p.Origins[0].Guardian.Locks[1] = p.Origins[0].Guardian.Locks[1], p.Origins[0].Guardian.Locks[0]
		},
		"recovery capsule reuse": func(p *productionSchemaMaintenancePlan) { p.Origins[0].Recovery.DeploymentID = p.TransitionID },
		"different recovery": func(p *productionSchemaMaintenancePlan) {
			p.Origins[1].Recovery.Candidate.PayloadSHA256 = strings.Repeat("f", 64)
		},
		"unbound engine":      func(p *productionSchemaMaintenancePlan) { p.Origins[0].Recovery.Candidate.DeployEngineSHA256 = "" },
		"foreign workflow":    func(p *productionSchemaMaintenancePlan) { p.Worker.Provider.Workflow = "arbitrary.yml" },
		"unsealed worker env": func(p *productionSchemaMaintenancePlan) { p.Worker.RWEnvironment.SHA256 = "" },
		"old worker env":      func(p *productionSchemaMaintenancePlan) { p.Worker.RWEnvironment.Path = "/etc/lmm-api/lmm-api.env" },
		"wrong worker kind":   func(p *productionSchemaMaintenancePlan) { p.Worker.Kind = "auto-migrate" },
		"nonnew worker":       func(p *productionSchemaMaintenancePlan) { p.Worker.Provider = p.Origins[0].Recovery.Candidate },
		"rollback worker": func(p *productionSchemaMaintenancePlan) {
			p.Worker.Provider.PayloadSHA256 = p.Origins[0].Recovery.Rollback.PayloadSHA256
		},
		"other worker route":  func(p *productionSchemaMaintenancePlan) { p.Worker.Provider.ContractRevision = "other-route" },
		"future capability":   func(p *productionSchemaMaintenancePlan) { p.RequiredCapability = 9 },
		"invalid physical ID": func(p *productionSchemaMaintenancePlan) { p.PreSchema.SystemIdentifier = "0" },
		"host startup in shared catalog": func(p *productionSchemaMaintenancePlan) {
			p.PreSchema.StartupSHA256, p.PreSchema.SignedUnitSHA256 = strings.Repeat("a", 64), strings.Repeat("b", 64)
		},
		"other backup root": func(p *productionSchemaMaintenancePlan) { p.BackupPolicy.ControllerDirectory = "/tmp/backup" },
		"backup root only": func(p *productionSchemaMaintenancePlan) {
			p.BackupPolicy.ControllerDirectory = "/home/lightjunction/Backups"
			p.BackupPolicy.RestoreProofPath = "/home/lightjunction/Backups/restore-proof.json"
		},
		"unsealed backup recipient": func(p *productionSchemaMaintenancePlan) { p.BackupPolicy.RecipientSHA256 = "" },
		"unsealed backup signer":    func(p *productionSchemaMaintenancePlan) { p.BackupPolicy.ControllerPublicKey = "" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			plan := cloneSchemaMaintenancePlan(t, good)
			mutate(&plan)
			if err := validateProductionSchemaMaintenancePlan(plan, runtime.paths); err == nil {
				t.Fatal("accepted incomplete or redirected schema maintenance recipe")
			}
		})
	}
}

func TestSchemaMaintenancePlanImmutablePrivateCanonicalFile(t *testing.T) {
	runtime, plan, path := newSchemaMaintenancePlanFixture(t)
	digest, err := runtime.createSchemaMaintenancePlan(path, plan)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := runtime.loadSchemaMaintenancePlan(path, digest)
	if err != nil || !reflect.DeepEqual(loaded, plan) {
		t.Fatalf("load actual canonical plan: %v", err)
	}
	if _, err := runtime.createSchemaMaintenancePlan(path, plan); err == nil {
		t.Fatal("overwrote a schema plan")
	}
	if _, err := runtime.loadSchemaMaintenancePlan(path, strings.Repeat("0", 64)); err == nil {
		t.Fatal("accepted a changed plan digest")
	}
	if _, err := runtime.createSchemaMaintenancePlan(filepath.Join(filepath.Dir(path), "other.json"), plan); err == nil {
		t.Fatal("created a plan outside the canonical plan path")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("nonprivate immutable plan: %v %v", info, err)
	}
}

func TestSchemaMaintenancePlanRejectsNonCanonicalAndUnsafeFiles(t *testing.T) {
	cases := map[string]func(string, []byte) []byte{
		"own hash cycle": func(_ string, raw []byte) []byte {
			return bytes.Replace(raw, []byte("{\n"), []byte("{\n  \"sha256\": \""+strings.Repeat("a", 64)+"\",\n"), 1)
		},
		"duplicate key": func(_ string, raw []byte) []byte {
			return bytes.Replace(raw, []byte("{\n"), []byte("{\n  \"format\": \""+schemaMaintenancePlanFormat+"\",\n"), 1)
		},
		"noncanonical whitespace": func(_ string, raw []byte) []byte { return append([]byte(" "), raw...) },
		"trailing record":         func(_ string, raw []byte) []byte { return append(raw, []byte("{}\n")...) },
		"public mode": func(path string, raw []byte) []byte {
			if err := os.Chmod(path, 0644); err != nil {
				panic(err)
			}
			return raw
		},
		"hard link": func(path string, raw []byte) []byte {
			if err := os.Link(path, path+".alias"); err != nil {
				panic(err)
			}
			return raw
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			runtime, plan, path := newSchemaMaintenancePlanFixture(t)
			if _, err := runtime.createSchemaMaintenancePlan(path, plan); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			raw = mutate(path, raw)
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := runtime.loadSchemaMaintenancePlan(path, startupContentSHA256(raw)); err == nil {
				t.Fatal("accepted unsafe or noncanonical bytes even with their matching digest")
			}
		})
	}
}

func schemaMaintenanceRecoveryBindingFixture(t *testing.T, runtime *productionRuntime, plan productionSchemaMaintenancePlan) (productionSchemaMaintenanceRecoveryBinding, string) {
	t.Helper()
	raw, err := canonicalProductionSchemaMaintenancePlan(plan, runtime.paths)
	if err != nil {
		t.Fatal(err)
	}
	planSHA := startupContentSHA256(raw)
	workspace := schemaMaintenanceHostWorkspace(runtime.paths, plan.TransitionID)
	binding := productionSchemaMaintenanceRecoveryBinding{
		Format: schemaMaintenanceRecoveryBindingFormat, TransitionID: plan.TransitionID, PrePlanSHA256: planSHA, OwnerSHA256: strings.Repeat("e", 64), PostSchema: plan.PreSchema,
		Outcome:      productionReleaseFilePlan{Path: filepath.Join(workspace, "state", "schema-outcome.json"), SHA256: strings.Repeat("f", 64)},
		WorkerResult: productionReleaseFilePlan{Path: filepath.Join(workspace, "state", "schema-worker-result.json"), SHA256: strings.Repeat("1", 64)},
	}
	binding.PostSchema.MetadataSHA256 = strings.Repeat("2", 64)
	for _, origin := range plan.Origins {
		binding.Origins = append(binding.Origins, productionSchemaMaintenanceRecoveryOrigin{
			Alias: origin.Alias, Host: origin.Host, RecoveryPlan: productionReleaseFilePlan{Path: origin.Recovery.PlanPath, SHA256: strings.Repeat("3", 64)},
			Capsule: productionReleaseFilePlan{Path: origin.Recovery.CapsulePath, SHA256: strings.Repeat("4", 64)}, StartupSHA256: startupContentSHA256([]byte(origin.Host)), SignedUnitSHA256: strings.Repeat("5", 64),
		})
	}
	return binding, planSHA
}

func TestSchemaMaintenanceRecoveryBindingActualRefsAndPhysicalIdentity(t *testing.T) {
	runtime, plan, _ := newSchemaMaintenancePlanFixture(t)
	good, planSHA := schemaMaintenanceRecoveryBindingFixture(t, runtime, plan)
	if err := validateProductionSchemaMaintenanceRecoveryBinding(good, plan, planSHA, runtime.paths); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*productionSchemaMaintenanceRecoveryBinding){
		"different original plan": func(b *productionSchemaMaintenanceRecoveryBinding) { b.PrePlanSHA256 = strings.Repeat("a", 64) },
		"unsealed physical owner": func(b *productionSchemaMaintenanceRecoveryBinding) { b.OwnerSHA256 = "" },
		"missing worker result":   func(b *productionSchemaMaintenanceRecoveryBinding) { b.WorkerResult.SHA256 = "" },
		"redirected worker":       func(b *productionSchemaMaintenanceRecoveryBinding) { b.WorkerResult.Path = b.Outcome.Path },
		"redirected outcome":      func(b *productionSchemaMaintenanceRecoveryBinding) { b.Outcome.Path += ".other" },
		"other cluster":           func(b *productionSchemaMaintenanceRecoveryBinding) { b.PostSchema.SystemIdentifier = "1" },
		"other database":          func(b *productionSchemaMaintenanceRecoveryBinding) { b.PostSchema.DatabaseOID++ },
		"other schema":            func(b *productionSchemaMaintenanceRecoveryBinding) { b.PostSchema.SchemaOID++ },
		"shared startup seal": func(b *productionSchemaMaintenanceRecoveryBinding) {
			b.PostSchema.StartupSHA256, b.PostSchema.SignedUnitSHA256 = strings.Repeat("a", 64), strings.Repeat("b", 64)
		},
		"missing origin": func(b *productionSchemaMaintenanceRecoveryBinding) { b.Origins = b.Origins[:1] },
		"foreign origin": func(b *productionSchemaMaintenanceRecoveryBinding) { b.Origins[0].Alias = "Other" },
		"old capsule overwrite": func(b *productionSchemaMaintenanceRecoveryBinding) {
			b.Origins[0].Capsule.Path = plan.Origins[0].OldCapsule.Path
		},
		"unsealed new plan":    func(b *productionSchemaMaintenanceRecoveryBinding) { b.Origins[0].RecoveryPlan.SHA256 = "" },
		"unsealed loaded hook": func(b *productionSchemaMaintenanceRecoveryBinding) { b.Origins[0].StartupSHA256 = "" },
		"different signed unit": func(b *productionSchemaMaintenanceRecoveryBinding) {
			b.Origins[1].SignedUnitSHA256 = strings.Repeat("a", 64)
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			raw, _ := json.Marshal(good)
			var binding productionSchemaMaintenanceRecoveryBinding
			if err := json.Unmarshal(raw, &binding); err != nil {
				t.Fatal(err)
			}
			mutate(&binding)
			if err := validateProductionSchemaMaintenanceRecoveryBinding(binding, plan, planSHA, runtime.paths); err == nil {
				t.Fatal("accepted unbound recovery data or a changed physical database")
			}
		})
	}
	// Pure data validation permits a no-op catalog result. Only the controller's
	// actual worker receipt and live checks can establish what happened.
	good.PostSchema.MetadataSHA256 = plan.PreSchema.MetadataSHA256
	if err := validateProductionSchemaMaintenanceRecoveryBinding(good, plan, planSHA, runtime.paths); err != nil {
		t.Fatalf("data validation invented a DDL requirement: %v", err)
	}
}

func TestSchemaMaintenanceRecoveryBindingImmutableCreateLoad(t *testing.T) {
	runtime, plan, _ := newSchemaMaintenancePlanFixture(t)
	binding, planSHA := schemaMaintenanceRecoveryBindingFixture(t, runtime, plan)
	path := filepath.Join(schemaMaintenanceHostWorkspace(runtime.paths, plan.TransitionID), "state", "recovery-binding.json")
	digest, err := runtime.createSchemaMaintenanceRecoveryBinding(path, binding, plan, planSHA)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := runtime.loadSchemaMaintenanceRecoveryBinding(path, digest, plan, planSHA)
	if err != nil || !reflect.DeepEqual(loaded, binding) {
		t.Fatalf("load actual recovery binding: %v", err)
	}
	if _, err := runtime.createSchemaMaintenanceRecoveryBinding(path, binding, plan, planSHA); err == nil {
		t.Fatal("overwrote post-execution recovery binding")
	}
	if _, err := runtime.loadSchemaMaintenanceRecoveryBinding(path, digest, plan, strings.Repeat("a", 64)); err == nil {
		t.Fatal("accepted a binding against another pre-plan digest")
	}
}
