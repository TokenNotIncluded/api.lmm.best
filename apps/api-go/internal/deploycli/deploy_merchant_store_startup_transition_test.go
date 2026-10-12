//go:build !windows

package deploycli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func ordinaryStartupTransitionFixture(t *testing.T, host string) (*sealedStartupFixture, productionMerchantStoreCapsule, string, map[string]string, string) {
	t.Helper()
	f := realShapeStartupFixture(t, host)
	f.runtime.paths.WorkRoot = filepath.Join(t.TempDir(), "work")
	f.runtime.paths.DropInDir = filepath.Join(filepath.Dir(f.files[0]), "drop-ins")
	if err := os.Mkdir(f.runtime.paths.DropInDir, 0755); err != nil {
		t.Fatal(err)
	}
	old := testMerchantCapsule(t, f.runtime.paths)
	f.runtime.hostname = func() (string, error) { return old.Host, nil }
	oldSHA := strings.Repeat("a", 64)
	oldPath := filepath.Join(old.Root, "capsule.json")
	pre := "{ path=" + old.Binary + " ; argv[]=" + old.Binary + " operator production writer-start --capsule " + oldPath + " --capsule-sha256 " + oldSHA + " ; ignore_errors=no ; }"
	drop := filepath.Join(f.runtime.paths.DropInDir, "90-merchant-startup-baseline.conf")
	if err := os.WriteFile(drop, merchantStorePortableHookBytes(old.Binary, oldPath, oldSHA), 0644); err != nil {
		t.Fatal(err)
	}
	f.unitOutput += "ExecStartPre=" + pre + "\nExecStartPreEx=" + testPortableStartEx(pre) + "\nDropInPaths=" + drop + "\n"
	seal, err := f.runtime.captureMerchantStorePortableStartup(context.Background(), oldPath, oldSHA)
	if err != nil {
		t.Fatal(err)
	}
	old.Writer.StartupSHA256, old.Writer.SignedUnitSHA256 = seal["startup_sha256"], seal["signed_unit_sha256"]
	old.ExistingSchemaContract.StartupSHA256, old.ExistingSchemaContract.SignedUnitSHA256 = seal["startup_sha256"], seal["signed_unit_sha256"]
	loaded, err := f.runtime.loadedExistingSchemaUnit(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return f, old, oldSHA, loaded, drop
}

// Persist the actual canonical old capsule, then bind its literal file digest
// into both loaded command properties and the persistent hook. The seal already
// normalizes that digest alone, so writing the final reference needs no cycle.
func canonicalOrdinaryStartupTransitionFixture(t *testing.T, host string) (*sealedStartupFixture, productionMerchantStoreCapsule, string, map[string]string, string) {
	t.Helper()
	f, old, placeholderSHA, _, drop := ordinaryStartupTransitionFixture(t, host)
	if err := os.MkdirAll(old.Root, 0700); err != nil {
		t.Fatal(err)
	}
	raw, err := canonicalMerchantStoreCapsule(old)
	if err != nil {
		t.Fatal(err)
	}
	oldSHA := startupContentSHA256(raw)
	path := filepath.Join(old.Root, "capsule.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	f.unitOutput = strings.ReplaceAll(f.unitOutput, placeholderSHA, oldSHA)
	if err := os.WriteFile(drop, merchantStorePortableHookBytes(old.Binary, path, oldSHA), 0644); err != nil {
		t.Fatal(err)
	}
	loaded, err := f.runtime.loadedExistingSchemaUnit(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return f, old, oldSHA, loaded, drop
}

func TestProductionMerchantStoreStartupTransitionGenericSealUsesActualCanonicalCapsule(t *testing.T) {
	for _, host := range []string{"arch", "ubuntu"} {
		t.Run(host, func(t *testing.T) {
			f, old, oldSHA, loaded, _ := canonicalOrdinaryStartupTransitionFixture(t, host)
			path, digest, err := merchantStoreLoadedCapsuleReference(loaded)
			if err != nil || path != filepath.Join(old.Root, "capsule.json") || digest != oldSHA {
				t.Fatalf("loaded literal capsule reference: path=%s sha=%s err=%v", path, digest, err)
			}
			values, startup, unit, err := f.runtime.existingSchemaSealedStartup(context.Background(), loaded)
			if err != nil || startup != old.Writer.StartupSHA256 || unit != old.Writer.SignedUnitSHA256 || values["LMM_DB_MIGRATION_MODE"] != "verify" || values["SQL_DSN"] != "postgres://user@127.0.0.1/lmm" {
				t.Fatalf("generic actual portable seal: startup=%s unit=%s err=%v", startup, unit, err)
			}
		})
	}
	for _, drift := range []string{"digest", "environment", "path"} {
		t.Run("reject "+drift, func(t *testing.T) {
			f, old, oldSHA, _, drop := canonicalOrdinaryStartupTransitionFixture(t, "ubuntu")
			path := filepath.Join(old.Root, "capsule.json")
			switch drift {
			case "digest":
				changedSHA := strings.Repeat("b", 64)
				f.unitOutput = strings.ReplaceAll(f.unitOutput, oldSHA, changedSHA)
				if err := os.WriteFile(drop, merchantStorePortableHookBytes(old.Binary, path, changedSHA), 0644); err != nil {
					t.Fatal(err)
				}
			case "environment":
				if err := os.WriteFile(f.files[1], []byte("CACHE_MODE=changed\n"), 0600); err != nil {
					t.Fatal(err)
				}
			case "path":
				otherPath := filepath.Join(merchantStoreCapsuleRoot(f.runtime.paths, "ordinary-other"), "capsule.json")
				if err := os.MkdirAll(filepath.Dir(otherPath), 0700); err != nil {
					t.Fatal(err)
				}
				// Preserve exact bytes/digest and both command representations.
				// The canonical capsule's immutable Root must still reject rebinding.
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(otherPath, raw, 0600); err != nil {
					t.Fatal(err)
				}
				f.unitOutput = strings.ReplaceAll(f.unitOutput, path, otherPath)
				if err := os.WriteFile(drop, merchantStorePortableHookBytes(old.Binary, otherPath, oldSHA), 0644); err != nil {
					t.Fatal(err)
				}
			}
			loaded, err := f.runtime.loadedExistingSchemaUnit(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if _, _, _, err := f.runtime.existingSchemaSealedStartup(context.Background(), loaded); err == nil {
				t.Fatal("generic sealed startup accepted actual capsule/hook drift")
			}
		})
	}
}

func TestProductionMerchantStoreStartupTransitionProjectionMatchesActualTarget(t *testing.T) {
	for _, host := range []string{"arch", "ubuntu"} {
		t.Run(host, func(t *testing.T) {
			f, old, oldSHA, loaded, drop := ordinaryStartupTransitionFixture(t, host)
			commands, err := f.runtime.merchantStorePortableStartupCommands(loaded, old.Binary, filepath.Join(old.Root, "capsule.json"), oldSHA)
			if err != nil {
				t.Fatal(err)
			}
			beforeCommands, _ := json.Marshal(commands)
			beforeLoaded, _ := json.Marshal(loaded)
			beforeHook, _ := os.ReadFile(drop)
			target := filepath.Join(merchantStoreCapsuleRoot(f.runtime.paths, "ordinary-next"), "capsule.json")
			startup, unit, err := f.runtime.merchantStoreStartupTransitionSnapshot(context.Background(), loaded, commands, old.Binary, target)
			if err != nil || startup == old.Writer.StartupSHA256 || unit != old.Writer.SignedUnitSHA256 {
				t.Fatalf("target projection: startup=%s unit=%s err=%v", startup, unit, err)
			}
			afterCommands, _ := json.Marshal(commands)
			afterLoaded, _ := json.Marshal(loaded)
			afterHook, _ := os.ReadFile(drop)
			if !bytes.Equal(beforeCommands, afterCommands) || !bytes.Equal(beforeLoaded, afterLoaded) || !bytes.Equal(beforeHook, afterHook) {
				t.Fatal("projection changed actual predecessor facts")
			}
			// Sanitized loaded-property fixture only: replacing the exact target
			// bytes must make ordinary actual capture equal the prior projection.
			newSHA := strings.Repeat("b", 64)
			f.unitOutput = strings.ReplaceAll(strings.ReplaceAll(f.unitOutput, filepath.Join(old.Root, "capsule.json"), target), oldSHA, newSHA)
			if err := os.WriteFile(drop, merchantStorePortableHookBytes(old.Binary, target, newSHA), 0644); err != nil {
				t.Fatal(err)
			}
			actual, err := f.runtime.captureMerchantStorePortableStartup(context.Background(), target, newSHA)
			if err != nil || actual["startup_sha256"] != startup || actual["signed_unit_sha256"] != unit {
				t.Fatalf("actual target differs from prospective snapshot: %v", err)
			}
			// Ordered private environment bytes still enter the future seal.
			if err := os.WriteFile(f.files[1], []byte("CACHE_MODE=changed\n"), 0600); err != nil {
				t.Fatal(err)
			}
			changed, _, err := f.runtime.merchantStoreStartupTransitionSnapshot(context.Background(), loaded, commands, old.Binary, target)
			if err != nil || changed == startup {
				t.Fatal("future snapshot omitted ordered environment-file bytes")
			}
		})
	}
}

func TestProductionMerchantStoreStartupTransitionFieldsAreClosed(t *testing.T) {
	paths := defaultProductionPaths()
	good := testMerchantWriterContract()
	good.StartupPredecessorPath = filepath.Join(merchantStoreCapsuleRoot(paths, "ordinary-old"), "capsule.json")
	good.StartupPredecessorSHA256 = strings.Repeat("a", 64)
	good.StartupTargetPath = filepath.Join(merchantStoreCapsuleRoot(paths, "ordinary-new"), "capsule.json")
	if err := validateMerchantStoreWriterContract(good); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*productionMerchantStoreWriterContract){
		"missing predecessor": func(w *productionMerchantStoreWriterContract) { w.StartupPredecessorPath = "" },
		"missing digest":      func(w *productionMerchantStoreWriterContract) { w.StartupPredecessorSHA256 = "" },
		"missing target":      func(w *productionMerchantStoreWriterContract) { w.StartupTargetPath = "" },
		"noncanonical digest": func(w *productionMerchantStoreWriterContract) { w.StartupPredecessorSHA256 = strings.Repeat("A", 64) },
		"same capsule":        func(w *productionMerchantStoreWriterContract) { w.StartupTargetPath = w.StartupPredecessorPath },
		"arbitrary file":      func(w *productionMerchantStoreWriterContract) { w.StartupTargetPath = "/tmp/capsule.json" },
		"traversal":           func(w *productionMerchantStoreWriterContract) { w.StartupPredecessorPath += "/../capsule.json" },
	} {
		t.Run(name, func(t *testing.T) {
			w := *good
			change(&w)
			if validateMerchantStoreWriterContract(&w) == nil {
				t.Fatal("accepted incomplete or redirected startup transition")
			}
		})
	}
	legacy := testMerchantWriterContract()
	body, _ := json.Marshal(legacy)
	if bytes.Contains(body, []byte("startup_predecessor")) || bytes.Contains(body, []byte("startup_target")) {
		t.Fatal("empty transition fields changed historical canonical contract bytes")
	}
	// Canonical contracts are portable across configured native path roots;
	// runtime qualification still enforces its actual fixed root separately.
	customPaths := paths
	customPaths.WorkRoot = filepath.Join(t.TempDir(), "work")
	custom := *good
	custom.StartupPredecessorPath = filepath.Join(merchantStoreCapsuleRoot(customPaths, "ordinary-old"), "capsule.json")
	custom.StartupTargetPath = filepath.Join(merchantStoreCapsuleRoot(customPaths, "ordinary-new"), "capsule.json")
	if err := validateMerchantStoreWriterContract(&custom); err != nil {
		t.Fatalf("canonical custom-root contract was refused: %v", err)
	}
	if validateMerchantStoreStartupTransitionPaths(&custom, paths) == nil {
		t.Fatal("portable grammar bypassed the runtime's actual fixed root")
	}
	if err := validateMerchantStoreStartupTransitionPaths(&custom, customPaths); err != nil {
		t.Fatal(err)
	}
}

func TestProductionMerchantStoreStartupTransitionRequiresExactRetainedAndSchema(t *testing.T) {
	old := testMerchantCapsule(t, defaultProductionPaths())
	w := *old.Writer
	w.StartupPredecessorPath = filepath.Join(old.Root, "capsule.json")
	w.StartupPredecessorSHA256 = strings.Repeat("a", 64)
	w.StartupTargetPath = filepath.Join(merchantStoreCapsuleRoot(defaultProductionPaths(), "ordinary-next"), "capsule.json")
	w.StartupSHA256 = strings.Repeat("9", 64)
	schema := *old.ExistingSchemaContract
	schema.StartupSHA256 = w.StartupSHA256
	if err := validateMerchantStoreStartupTransitionBinding(&w, &schema, old); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*productionMerchantStoreWriterContract, *productionExistingSchemaContract){
		"retained package": func(w *productionMerchantStoreWriterContract, _ *productionExistingSchemaContract) {
			w.Rollback.PackageSHA256 = strings.Repeat("2", 64)
		},
		"retained status": func(w *productionMerchantStoreWriterContract, _ *productionExistingSchemaContract) {
			w.Rollback.StatusSHA256 = strings.Repeat("2", 64)
		},
		"retained source": func(w *productionMerchantStoreWriterContract, _ *productionExistingSchemaContract) {
			w.Rollback.SourceRevision = strings.Repeat("2", 40)
		},
		"physical identity": func(w *productionMerchantStoreWriterContract, _ *productionExistingSchemaContract) { w.DatabaseOID++ },
		"floor": func(w *productionMerchantStoreWriterContract, _ *productionExistingSchemaContract) {
			w.RequiredCapability++
		},
		"business role": func(w *productionMerchantStoreWriterContract, _ *productionExistingSchemaContract) { w.Role = "other" },
		"signed unit": func(w *productionMerchantStoreWriterContract, _ *productionExistingSchemaContract) {
			w.SignedUnitSHA256 = strings.Repeat("2", 64)
		},
		"target seal": func(_ *productionMerchantStoreWriterContract, s *productionExistingSchemaContract) {
			s.StartupSHA256 = strings.Repeat("2", 64)
		},
		"catalog": func(_ *productionMerchantStoreWriterContract, s *productionExistingSchemaContract) { s.SchemaOID++ },
	} {
		t.Run(name, func(t *testing.T) {
			writerCopy, schemaCopy := w, schema
			change(&writerCopy, &schemaCopy)
			if validateMerchantStoreStartupTransitionBinding(&writerCopy, &schemaCopy, old) == nil {
				t.Fatal("accepted changed retained writer/schema authority")
			}
		})
	}
}

func TestProductionMerchantStoreStartupTransitionStoppedQualificationIsNotCapture(t *testing.T) {
	f, old, placeholderSHA, _, drop := ordinaryStartupTransitionFixture(t, "ubuntu")
	installedDir := t.TempDir()
	entry := filepath.Join(installedDir, "lmm-api")
	payload := []byte("sanitized stopped provider fixture\n")
	if err := os.WriteFile(filepath.Join(installedDir, backendGoName), payload, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(backendGoName, entry); err != nil {
		t.Fatal(err)
	}
	f.unitOutput = strings.ReplaceAll(f.unitOutput, old.Binary, entry)
	f.unitOutput = strings.ReplaceAll(strings.ReplaceAll(f.unitOutput, "MainPID=1234", "MainPID=0"), "ActiveState=active", "ActiveState=inactive")
	f.runtime.paths.InstalledBinary, old.Binary = entry, entry
	old.Writer.Candidate.PayloadSHA256 = startupContentSHA256(payload)
	old.Candidate.PayloadSHA256 = old.Writer.Candidate.PayloadSHA256
	schema := testExistingSchemaContract(t)
	old.Writer.SystemIdentifier, old.Writer.Database, old.Writer.DatabaseOID = schema.SystemIdentifier, schema.Database, schema.DatabaseOID
	old.Writer.Schema, old.Writer.SchemaOID = schema.Schema, schema.SchemaOID
	old.ExistingSchemaContract = schema
	oldPath := filepath.Join(old.Root, "capsule.json")
	if err := os.WriteFile(drop, merchantStorePortableHookBytes(entry, oldPath, placeholderSHA), 0644); err != nil {
		t.Fatal(err)
	}
	seal, err := f.runtime.captureMerchantStorePortableStartup(context.Background(), oldPath, placeholderSHA)
	if err != nil {
		t.Fatal(err)
	}
	old.Writer.StartupSHA256, old.Writer.SignedUnitSHA256 = seal["startup_sha256"], seal["signed_unit_sha256"]
	schema.StartupSHA256, schema.SignedUnitSHA256 = seal["startup_sha256"], seal["signed_unit_sha256"]
	if err := os.MkdirAll(old.Root, 0700); err != nil {
		t.Fatal(err)
	}
	raw, _ := canonicalMerchantStoreCapsule(old)
	oldSHA := startupContentSHA256(raw)
	if err := os.WriteFile(oldPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	f.unitOutput = strings.ReplaceAll(f.unitOutput, placeholderSHA, oldSHA)
	if err := os.WriteFile(drop, merchantStorePortableHookBytes(entry, oldPath, oldSHA), 0644); err != nil {
		t.Fatal(err)
	}
	baseRunner := f.runtime.runner
	catalogDrift := false
	f.runtime.runner = existingSchemaTestRunner{run: func(command productionCommand) ([]byte, error) {
		if command.Name == commandPSQL {
			if !command.Sensitive || !strings.Contains(strings.Join(command.Args, " "), "BEGIN READ ONLY") {
				return nil, fmt.Errorf("database qualification escaped read-only mode")
			}
			if strings.Contains(strings.Join(command.Args, " "), "lmm-merchant-writer-identity") {
				return []byte(fmt.Sprintf(`{"system_identifier":%q,"database":%q,"database_oid":%d,"schema":%q,"schema_oid":%d,"role":%q,"session_role":%q,"transaction_read_only":"on","floor":"%d"}`, old.Writer.SystemIdentifier, old.Writer.Database, old.Writer.DatabaseOID, old.Writer.Schema, old.Writer.SchemaOID, old.Writer.Role, old.Writer.Role, old.Writer.RequiredCapability)), nil
			}
			if catalogDrift {
				return bytes.Replace(testExistingSchemaSnapshot(), []byte(`"database_oid":16384`), []byte(`"database_oid":16385`), 1), nil
			}
			return testExistingSchemaSnapshot(), nil
		}
		return baseRunner.Run(context.Background(), command)
	}}
	target := filepath.Join(merchantStoreCapsuleRoot(f.runtime.paths, "ordinary-next"), "capsule.json")
	projection, _, _, err := f.runtime.inspectMerchantStoreStartupProjection(context.Background(), oldPath, oldSHA, target, nil, false)
	if err != nil || projection.PID != 0 || projection.StartupSHA256 == old.Writer.StartupSHA256 {
		t.Fatalf("strict stopped pre-bind qualification: %+v err=%v", projection, err)
	}
	if _, err := f.runtime.projectMerchantStoreStartup(context.Background(), oldPath, oldSHA, target); err == nil {
		t.Fatal("stopped pre-bind facts became an active capture receipt")
	}
	catalogDrift = true
	if _, _, _, err := f.runtime.inspectMerchantStoreStartupProjection(context.Background(), oldPath, oldSHA, target, nil, false); err == nil {
		t.Fatal("stopped qualification accepted actual physical/catalog drift")
	}
}

func TestProductionMerchantStoreStartupTransitionPrebindRejectsAnyIntent(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "capsule.json")
	if err := os.Mkdir(filepath.Join(root, "state"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := merchantStoreStartupPrebindIntentAbsent(target); err != nil {
		t.Fatal(err)
	}
	intent := filepath.Join(root, "state", "ordinary-startup-bind-intent.json")
	if err := os.WriteFile(intent, []byte("unknown\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if merchantStoreStartupPrebindIntentAbsent(target) == nil {
		t.Fatal("existing uncertain intent allowed reuse of pre-bind qualification")
	}
	if err := os.Remove(intent); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "missing"), intent); err != nil {
		t.Fatal(err)
	}
	if merchantStoreStartupPrebindIntentAbsent(target) == nil {
		t.Fatal("dangling intent symlink was interpreted as absence")
	}
}
