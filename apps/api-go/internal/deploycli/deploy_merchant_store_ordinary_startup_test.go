//go:build !windows

package deploycli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMerchantStoreOrdinaryLoadedCapsuleReferenceIsClosed(t *testing.T) {
	binary, path, digest := "/usr/lib/lmm-api-deploy/engine", "/var/lib/lmm-api-go-deploy/merchant-capsules/test/capsule.json", strings.Repeat("a", 64)
	command := "{ path=" + binary + " ; argv[]=" + binary + " operator production writer-start --capsule " + path + " --capsule-sha256 " + digest + " ; ignore_errors=no ; }"
	loaded := map[string]string{"ExecStartPre": command}
	actualPath, actualSHA, err := merchantStoreLoadedCapsuleReference(loaded)
	if err != nil || actualPath != path || actualSHA != digest {
		t.Fatalf("exact literal reference rejected: %v", err)
	}
	for name, changed := range map[string]string{
		"ignored error":    strings.Replace(command, "ignore_errors=no", "ignore_errors=yes", 1),
		"extra argument":   strings.Replace(command, " --capsule ", " --foreign --capsule ", 1),
		"extra hook":       command + " " + command,
		"other operation":  strings.Replace(command, "writer-start ", "writer-start-check ", 1),
		"bad digest":       strings.Replace(command, digest, strings.ToUpper(digest), 1),
		"other executable": strings.Replace(command, "argv[]="+binary, "argv[]=/other/engine", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := merchantStoreLoadedCapsuleReference(map[string]string{"ExecStartPre": changed}); err == nil {
				t.Fatal("accepted an unsupported loaded command")
			}
		})
	}
	if _, err := merchantStoreSealedStartCommand(command, binary); err == nil {
		t.Fatal("the legacy workspace parser was broadened to accept portable commands")
	}
}

func TestMerchantStoreOrdinaryManifestRequiresExactLoadedTarget(t *testing.T) {
	f, old, oldSHA, loaded, hook := canonicalOrdinaryStartupTransitionFixture(t, "ubuntu")
	next := cloneMerchantCapsule(old)
	next.DeploymentID = "ordinary-next"
	next.Root = merchantStoreCapsuleRoot(f.runtime.paths, next.DeploymentID)
	next.ControllerPlanSHA256 = strings.Repeat("c", 64)
	oldPath, targetPath := filepath.Join(old.Root, "capsule.json"), filepath.Join(next.Root, "capsule.json")
	next.Writer.StartupPredecessorPath, next.Writer.StartupPredecessorSHA256, next.Writer.StartupTargetPath = oldPath, oldSHA, targetPath
	commands, err := f.runtime.merchantStorePortableStartupCommands(loaded, old.Binary, oldPath, oldSHA)
	if err != nil {
		t.Fatal(err)
	}
	startup, unit, err := f.runtime.merchantStoreStartupTransitionSnapshot(context.Background(), loaded, commands, old.Binary, targetPath)
	if err != nil {
		t.Fatal(err)
	}
	next.Writer.StartupSHA256, next.Writer.SignedUnitSHA256 = startup, unit
	next.ExistingSchemaContract.StartupSHA256, next.ExistingSchemaContract.SignedUnitSHA256 = startup, unit
	if err := os.MkdirAll(next.Root, 0700); err != nil {
		t.Fatal(err)
	}
	raw, err := canonicalMerchantStoreCapsule(next)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	nextSHA := startupContentSHA256(raw)
	f.unitOutput = strings.ReplaceAll(strings.ReplaceAll(f.unitOutput, oldPath, targetPath), oldSHA, nextSHA)
	if err := os.WriteFile(hook, merchantStorePortableHookBytes(old.Binary, targetPath, nextSHA), 0644); err != nil {
		t.Fatal(err)
	}
	loaded, err = f.runtime.loadedExistingSchemaUnit(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	manifest := productionManifest{DeploymentID: next.DeploymentID, SchemaPlanSHA256: next.ControllerPlanSHA256, MerchantStoreWriter: next.Writer, ExistingSchemaContract: next.ExistingSchemaContract}
	values, err := f.runtime.existingSchemaManifestStartup(context.Background(), manifest, loaded)
	if err != nil || values["LMM_DB_MIGRATION_MODE"] != "verify" {
		t.Fatalf("exact actual target rejected: %v", err)
	}
	for name, change := range map[string]func(*productionManifest){
		"foreign plan":       func(m *productionManifest) { m.SchemaPlanSHA256 = strings.Repeat("d", 64) },
		"foreign deployment": func(m *productionManifest) { m.DeploymentID = "foreign" },
		"other target": func(m *productionManifest) {
			m.MerchantStoreWriter.StartupTargetPath = filepath.Join(merchantStoreCapsuleRoot(f.runtime.paths, "foreign"), "capsule.json")
		},
		"different schema": func(m *productionManifest) { m.ExistingSchemaContract.SchemaOID++ },
	} {
		t.Run(name, func(t *testing.T) {
			copy := manifest
			writer, schema := *manifest.MerchantStoreWriter, *manifest.ExistingSchemaContract
			copy.MerchantStoreWriter, copy.ExistingSchemaContract = &writer, &schema
			change(&copy)
			if _, err := f.runtime.existingSchemaManifestStartup(context.Background(), copy, loaded); err == nil {
				t.Fatal("actual capsule from another immutable plan was accepted")
			}
		})
	}
}

func TestMerchantStoreOrdinaryTransitionBelongsToBackendPlan(t *testing.T) {
	paths := defaultProductionPaths()
	c := testMerchantCapsule(t, paths)
	c.Writer.StartupPredecessorPath = filepath.Join(merchantStoreCapsuleRoot(paths, "ordinary-old"), "capsule.json")
	c.Writer.StartupPredecessorSHA256 = strings.Repeat("a", 64)
	c.Writer.StartupTargetPath = filepath.Join(c.Root, "capsule.json")
	packageFor := func(target productionMerchantStoreWriterTarget) productionReleasePackagePlan {
		return productionReleasePackagePlan{MerchantStoreWriterCapability: target.Capability, PackageSHA256: target.PackageSHA256, PayloadSHA256: target.PayloadSHA256, GitRevision: target.SourceRevision, ReleaseAssetSHA256: target.ReleaseAssetSHA256}
	}
	plan := productionReleasePlan{DeploymentID: c.DeploymentID, GoChanged: true, SchemaMode: c.SchemaMode, ExistingSchemaContract: c.ExistingSchemaContract, MerchantStoreWriter: c.Writer, GoCandidate: packageFor(c.Writer.Candidate), GoRollback: packageFor(c.Writer.Rollback)}
	if err := validateMerchantStoreWriterPlan(plan); err != nil {
		t.Fatal(err)
	}
	plan.GoChanged = false
	if validateMerchantStoreWriterPlan(plan) == nil {
		t.Fatal("a frontend-only plan authorized a backend startup transition")
	}
	plan.GoChanged, plan.DeploymentID = true, "foreign-deployment"
	if validateMerchantStoreWriterPlan(plan) == nil {
		t.Fatal("plan selected another deployment's target capsule")
	}
	runner := &fakeProductionRunner{}
	runtime := productionRuntime{runner: runner}
	manifest := productionManifest{DeploymentID: plan.DeploymentID, MerchantStoreWriter: c.Writer, ExistingSchemaContract: c.ExistingSchemaContract}
	if _, err := runtime.existingSchemaManifestStartup(context.Background(), manifest, nil); err == nil || len(runner.commands) != 0 {
		t.Fatal("foreign deployment reached pre-bind environment qualification")
	}
}
