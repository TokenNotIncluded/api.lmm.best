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

func testMerchantCapsule(t *testing.T, paths productionPaths) productionMerchantStoreCapsule {
	t.Helper()
	w := testMerchantWriterContract()
	s := testExistingSchemaContract(t)
	s.SystemIdentifier, s.Database, s.DatabaseOID, s.Schema, s.SchemaOID = w.SystemIdentifier, w.Database, w.DatabaseOID, w.Schema, w.SchemaOID
	s.StartupSHA256, s.SignedUnitSHA256 = w.StartupSHA256, w.SignedUnitSHA256
	p := productionReleasePackagePlan{Name: productionAURPackageName, Version: "0.2.90-1", Identity: productionAURPackageName + " 0.2.90-1", ReleaseTag: "go-v0.2.90", Workflow: "release-go.yml", MerchantStoreWriterCapability: w.Candidate.Capability,
		PackageSHA256: w.Candidate.PackageSHA256, PayloadSHA256: w.Candidate.PayloadSHA256, GitRevision: w.Candidate.SourceRevision, ContractRevision: "contract-v1", ReleaseAssetSHA256: w.Candidate.ReleaseAssetSHA256, SignatureBundleSHA256: strings.Repeat("2", 64)}
	c := productionMerchantStoreCapsule{Format: 1, DeploymentID: "release-portable-test", ControllerPlanSHA256: strings.Repeat("9", 64), Root: merchantStoreCapsuleRoot(paths, "release-portable-test"), Host: "dmit-ubuntu", Service: paths.Service, Binary: paths.InstalledBinary, ConfigDir: paths.ConfigDir, SchemaMode: productionSchemaModeVerifyExisting, ExistingSchemaContract: s, Writer: w,
		Candidate: merchantStoreCapsuleProjection(p, "candidate"), Rollback: merchantStoreCapsuleProjection(p, "rollback"), StartupPolicy: "per-start"}
	for _, path := range merchantStoreCapsuleSourcePaths {
		c.SourceFiles = append(c.SourceFiles, productionReleaseFilePlan{Path: path, SHA256: strings.Repeat("8", 64)})
	}
	return c
}
func cloneMerchantCapsule(c productionMerchantStoreCapsule) productionMerchantStoreCapsule {
	body, _ := json.Marshal(c)
	var copy productionMerchantStoreCapsule
	_ = json.Unmarshal(body, &copy)
	return copy
}
func TestProductionMerchantStoreCapsuleClosedProjection(t *testing.T) {
	paths := defaultProductionPaths()
	good := testMerchantCapsule(t, paths)
	if err := validateMerchantStoreCapsule(good, paths); err != nil {
		t.Fatal(err)
	}
	phaseSix := cloneMerchantCapsule(good)
	phaseSix.Writer.Candidate.Capability = 6
	phaseSix.Candidate.MerchantStoreWriterCapability = 6
	if err := validateMerchantStoreCapsule(phaseSix, paths); err != nil {
		t.Fatalf("supported phase-six candidate: %v", err)
	}
	phaseSeven := cloneMerchantCapsule(good)
	phaseSeven.Writer.Candidate.Capability = 7
	phaseSeven.Candidate.MerchantStoreWriterCapability = 7
	if err := validateMerchantStoreCapsule(phaseSeven, paths); err != nil {
		t.Fatalf("supported reviewed phase-seven candidate: %v", err)
	}
	phaseEight := cloneMerchantCapsule(good)
	phaseEight.Writer.Candidate.Capability = 8
	phaseEight.Candidate.MerchantStoreWriterCapability = 8
	if err := validateMerchantStoreCapsule(phaseEight, paths); err != nil {
		t.Fatalf("supported reviewed refund-sync candidate: %v", err)
	}
	cases := map[string]func(*productionMerchantStoreCapsule){
		"host injection":    func(c *productionMerchantStoreCapsule) { c.Host = "dmit-ubuntu;anything" },
		"service redirect":  func(c *productionMerchantStoreCapsule) { c.Service = "arbitrary.service" },
		"root traversal":    func(c *productionMerchantStoreCapsule) { c.Root += "/../other" },
		"binary redirect":   func(c *productionMerchantStoreCapsule) { c.Binary = "/tmp/other" },
		"financial mode":    func(c *productionMerchantStoreCapsule) { c.SchemaMode = "apply" },
		"unknown startup":   func(c *productionMerchantStoreCapsule) { c.StartupPolicy = "skip" },
		"physical identity": func(c *productionMerchantStoreCapsule) { c.ExistingSchemaContract.SystemIdentifier = "3" },
		"ordered startup":   func(c *productionMerchantStoreCapsule) { c.Writer.StartupSHA256 = strings.Repeat("4", 64) },
		"future capability": func(c *productionMerchantStoreCapsule) {
			c.Writer.Candidate.Capability = 9
			c.Candidate.MerchantStoreWriterCapability = 9
		},
		"legacy retained": func(c *productionMerchantStoreCapsule) {
			c.Writer.Rollback.Capability = 0
			c.Rollback.MerchantStoreWriterCapability = 0
		},
		"underfloor retained": func(c *productionMerchantStoreCapsule) {
			c.Writer.Rollback.Capability = 3
			c.Rollback.MerchantStoreWriterCapability = 3
		},
		"source unbound":          func(c *productionMerchantStoreCapsule) { c.Candidate.GitRevision = strings.Repeat("4", 40) },
		"official asset unbound":  func(c *productionMerchantStoreCapsule) { c.Candidate.ReleaseAssetSHA256 = strings.Repeat("4", 64) },
		"workflow spoof":          func(c *productionMerchantStoreCapsule) { c.Candidate.Workflow = "arbitrary.yml" },
		"tag spoof":               func(c *productionMerchantStoreCapsule) { c.Candidate.ReleaseTag = "go-v999" },
		"signature omitted":       func(c *productionMerchantStoreCapsule) { c.Candidate.SignatureBundleSHA256 = "" },
		"callback route drift":    func(c *productionMerchantStoreCapsule) { c.Rollback.ContractRevision = "other" },
		"arbitrary asset path":    func(c *productionMerchantStoreCapsule) { c.Candidate.PackagePath = "/elsewhere.pkg" },
		"source closure omitted":  func(c *productionMerchantStoreCapsule) { c.SourceFiles = c.SourceFiles[:2] },
		"source closure override": func(c *productionMerchantStoreCapsule) { c.SourceFiles[2].Path = "scripts/anything.py" },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			c := cloneMerchantCapsule(good)
			change(&c)
			if validateMerchantStoreCapsule(c, paths) == nil {
				t.Fatal("accepted modified capsule authority")
			}
		})
	}
}
func TestProductionMerchantStoreCapsuleCanonicalTamperAndHost(t *testing.T) {
	root := t.TempDir()
	paths := defaultProductionPaths()
	paths.WorkRoot = filepath.Join(root, "work")
	c := testMerchantCapsule(t, paths)
	if err := os.MkdirAll(c.Root, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(c.Root, "capsule.json")
	body, _ := canonicalMerchantStoreCapsule(c)
	runtime := productionRuntime{paths: paths, requiredOwnerUID: uint32(os.Geteuid()), hostname: func() (string, error) { return c.Host, nil }}
	write := func(raw []byte) {
		t.Helper()
		if os.WriteFile(path, raw, 0600) != nil {
			t.Fatal("write capsule")
		}
	}
	write(body)
	if _, err := runtime.loadMerchantStoreCapsule(path, startupContentSHA256(body)); err != nil {
		t.Fatal(err)
	}
	altered := cloneMerchantCapsule(c)
	altered.Writer.Role = "another-role"
	changed, _ := canonicalMerchantStoreCapsule(altered)
	write(changed)
	if _, err := runtime.loadMerchantStoreCapsule(path, startupContentSHA256(body)); err == nil {
		t.Fatal("tampered immutable role retained old digest")
	}
	write(append([]byte(" "), body...))
	if _, err := runtime.loadMerchantStoreCapsule(path, startupContentSHA256(append([]byte(" "), body...))); err == nil {
		t.Fatal("noncanonical JSON accepted")
	}
	duplicate := strings.Replace(string(body), `"format": 1,`, `"format": 1, "format": 1,`, 1)
	write([]byte(duplicate))
	if _, err := runtime.loadMerchantStoreCapsule(path, startupContentSHA256([]byte(duplicate))); err == nil {
		t.Fatal("duplicate JSON accepted")
	}
	write(body)
	runtime.hostname = func() (string, error) { return "arch-dmit", nil }
	if _, err := runtime.loadMerchantStoreCapsule(path, startupContentSHA256(body)); err == nil {
		t.Fatal("capsule crossed actual host binding")
	}
}
func testPortableStartEx(value string) string {
	return strings.Replace(value, "ignore_errors=no", "flags=privileged", 1)
}

func TestProductionMerchantStorePortableHookSelfReferenceIsExact(t *testing.T) {
	binary, path, digest := "/usr/bin/lmm-api", "/var/lib/lmm-api-go-deploy/merchant-capsules/release-test/capsule.json", strings.Repeat("a", 64)
	value := "{ path=" + binary + " ; argv[]=" + binary + " operator production writer-start --capsule " + path + " --capsule-sha256 " + digest + " ; ignore_errors=no ; }"
	normalized, err := merchantStorePortableStartCommand(value, testPortableStartEx(value), binary, path, digest)
	if err != nil || !strings.Contains(normalized, merchantStoreCapsuleHashPlaceholder) || strings.Contains(normalized, digest) {
		t.Fatalf("normalization=%q err=%v", normalized, err)
	}
	for name, bad := range map[string]string{
		"other digest":  strings.Replace(value, digest, strings.Repeat("b", 64), 1),
		"other path":    strings.Replace(value, path, path+".other", 1),
		"shell":         strings.Replace(value, "path="+binary, "path=/bin/sh", 1),
		"apply":         strings.Replace(value, "writer-start", "apply", 1),
		"extra args":    strings.Replace(value, " ; ignore_errors", " --skip ; ignore_errors", 1),
		"ignore errors": strings.Replace(value, "ignore_errors=no", "ignore_errors=yes", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := merchantStorePortableStartCommand(bad, testPortableStartEx(bad), binary, path, digest); err == nil {
				t.Fatal("unsafe per-start hook accepted")
			}
		})
	}
	// The old 95 ordinary parser has not gained a portable/skip fallback.
	if _, err := merchantStoreSealedStartCommand(value, binary); err == nil {
		t.Fatal("old ordinary policy silently accepted new per-start owner")
	}
	for _, extended := range []string{"", value, strings.Replace(testPortableStartEx(value), "privileged", "", 1), strings.Replace(testPortableStartEx(value), "privileged", "privileged ignore-failure", 1), strings.Replace(testPortableStartEx(value), "privileged", "no-env-expand", 1), testPortableStartEx(value) + "\n" + testPortableStartEx(value), strings.Replace(testPortableStartEx(value), digest, strings.Repeat("b", 64), 1)} {
		if _, err := merchantStorePortableStartCommand(value, extended, binary, path, digest); err == nil {
			t.Fatal("unproved or different privilege command accepted", extended)
		}
	}
}
func TestProductionMerchantStoreCapsuleNoSelfIssuedApproval(t *testing.T) {
	plan := testProductionReleasePlan(t, t.TempDir())
	body, _ := canonicalProductionReleasePlan(plan)
	w := testMerchantWriterContract()
	s := testExistingSchemaContract(t)
	calls := 0
	release := productionReleaseRuntime{runner: existingSchemaTestRunner{run: func(command productionCommand) ([]byte, error) {
		calls++
		return nil, errors.New("actual artifact verifier failed")
	}}}
	if _, err := release.deriveMerchantStoreCapsule(context.Background(), plan, startupContentSHA256(body), "dmit-ubuntu", "per-start", s, w); err == nil || calls != 0 {
		t.Fatal("historical apply plan became a no-DDL portable capsule")
	}
	calls = 0
	if _, err := release.deriveMerchantStoreCapsule(context.Background(), plan, strings.Repeat("0", 64), "dmit-ubuntu", "per-start", s, w); err == nil || calls != 0 {
		t.Fatal("plan tamper reached artifact verification")
	}
}

func TestProductionMerchantStoreCapsuleOfficialSignatureFailsBeforeProvider(t *testing.T) {
	paths := defaultProductionPaths()
	paths.WorkRoot = filepath.Join(t.TempDir(), "work")
	c := testMerchantCapsule(t, paths)
	if os.MkdirAll(filepath.Join(c.Root, "assets"), 0700) != nil {
		t.Fatal("assets")
	}
	for _, kind := range []string{"pkg.tar.zst", "release.tar.gz", "sigstore.json"} {
		path := filepath.Join(c.Root, merchantStoreCapsuleAsset("candidate", kind))
		body := []byte(kind)
		if os.WriteFile(path, body, 0600) != nil {
			t.Fatal("asset")
		}
		sha := startupContentSHA256(body)
		switch kind {
		case "pkg.tar.zst":
			c.Candidate.PackageSHA256 = sha
			c.Writer.Candidate.PackageSHA256 = sha
		case "release.tar.gz":
			c.Candidate.ReleaseAssetSHA256 = sha
			c.Writer.Candidate.ReleaseAssetSHA256 = sha
		case "sigstore.json":
			c.Candidate.SignatureBundleSHA256 = sha
		}
	}
	calls := []productionCommand{}
	runtime := productionRuntime{requiredOwnerUID: uint32(os.Geteuid()), runner: existingSchemaTestRunner{run: func(command productionCommand) ([]byte, error) {
		calls = append(calls, command)
		return nil, errors.New("actual official Sigstore verifier refused")
	}}}
	if _, err := runtime.verifyMerchantStoreCapsuleArtifact(context.Background(), c, false); err == nil || len(calls) != 1 || calls[0].Name != commandCosign {
		t.Fatalf("signature failure reached provider: calls=%v error=%v", calls, err)
	}
	expected := productionReleaseIdentity(c.Candidate.ReleaseAssetSHA256, productionAURPackageName, "release-go.yml", c.Candidate.ReleaseTag)
	if !strings.Contains(strings.Join(calls[0].Args, " "), expected) {
		t.Fatal("signature identity is not the official workflow/tag")
	}
}

func TestProductionMerchantStoreCapsuleMutablePathFences(t *testing.T) {
	for _, kind := range []string{"symlink", "writable", "wrong-private-mode"} {
		t.Run(kind, func(t *testing.T) {
			paths := defaultProductionPaths()
			paths.WorkRoot = filepath.Join(t.TempDir(), "work")
			c := testMerchantCapsule(t, paths)
			if err := os.MkdirAll(c.Root, 0700); err != nil {
				t.Fatal(err)
			}
			external := t.TempDir()
			state := filepath.Join(c.Root, "state")
			if kind == "symlink" {
				if err := os.Symlink(external, state); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Mkdir(state, 0700); err != nil {
					t.Fatal(err)
				}
				mode := os.FileMode(0777)
				if kind == "wrong-private-mode" {
					mode = 0755
				}
				if err := os.Chmod(state, mode); err != nil {
					t.Fatal(err)
				}
			}
			runtime := productionRuntime{requiredOwnerUID: uint32(os.Geteuid())}
			if runtime.merchantStoreCapsuleMutableDirectories(c) == nil {
				t.Fatal("unsafe existing state destination was accepted")
			}
			if _, err := os.Lstat(filepath.Join(c.Root, "tmp")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("an earlier extraction directory was created before all paths passed")
			}
			entries, err := os.ReadDir(external)
			if err != nil || len(entries) != 0 {
				t.Fatal("unsafe destination was mutated")
			}
		})
	}
	paths := defaultProductionPaths()
	paths.WorkRoot = filepath.Join(t.TempDir(), "work")
	c := testMerchantCapsule(t, paths)
	if err := os.MkdirAll(c.Root, 0700); err != nil {
		t.Fatal(err)
	}
	runtime := productionRuntime{requiredOwnerUID: uint32(os.Geteuid())}
	if err := runtime.merchantStoreCapsuleMutableDirectories(c); err != nil {
		t.Fatal(err)
	}
	if err := runtime.merchantStoreCapsuleMutableDirectories(c); err != nil {
		t.Fatal("owned private directories should be safely reusable", err)
	}
}

func TestProductionMerchantStoreCapsuleArtifactFileFences(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	runtime := productionRuntime{requiredOwnerUID: uint32(os.Geteuid())}
	file := filepath.Join(root, "provider")
	if err := os.WriteFile(file, []byte("payload"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := runtime.merchantStoreCapsuleFile(file, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(file, filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	if runtime.merchantStoreCapsuleFile(file, 0600) == nil {
		t.Fatal("hard-linked artifact accepted")
	}
	if err := os.Remove(filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(file, 0644); err != nil {
		t.Fatal(err)
	}
	if runtime.merchantStoreCapsuleFile(file, 0600) == nil {
		t.Fatal("public artifact permission accepted")
	}
	if err := os.Chmod(file, 0600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "ancestor")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	if runtime.merchantStoreCapsuleFile(filepath.Join(alias, "provider"), 0600) == nil {
		t.Fatal("symlink ancestor accepted")
	}
}

func TestProductionMerchantStorePortableInstalledProviderFences(t *testing.T) {
	root := t.TempDir()
	c := productionMerchantStoreCapsule{Binary: filepath.Join(root, productionCandidateLinkName)}
	provider := filepath.Join(root, backendGoName)
	body := []byte("explicit signed payload fixture")
	if err := os.WriteFile(provider, body, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(backendGoName, c.Binary); err != nil {
		t.Fatal(err)
	}
	runtime := productionRuntime{requiredOwnerUID: uint32(os.Geteuid())}
	if err := runtime.verifyMerchantStorePortableInstalled(c, startupContentSHA256(body)); err != nil {
		t.Fatal(err)
	}
	if runtime.verifyMerchantStorePortableInstalled(c, strings.Repeat("f", 64)) == nil {
		t.Fatal("installed payload drift accepted")
	}
	if err := os.Remove(c.Binary); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(provider, c.Binary); err != nil {
		t.Fatal(err)
	}
	if runtime.verifyMerchantStorePortableInstalled(c, startupContentSHA256(body)) == nil {
		t.Fatal("noncanonical absolute provider link accepted")
	}
}

func TestProductionMerchantStorePortableOrderedStartupIsStillSealed(t *testing.T) {
	f := realShapeStartupFixture(t, "ubuntu")
	f.runtime.paths.WorkRoot = filepath.Join(t.TempDir(), "work")
	c := testMerchantCapsule(t, f.runtime.paths)
	f.runtime.hostname = func() (string, error) { return c.Host, nil }
	digest := strings.Repeat("a", 64)
	path := filepath.Join(c.Root, "capsule.json")
	pre := "{ path=" + c.Binary + " ; argv[]=" + c.Binary + " operator production writer-start --capsule " + path + " --capsule-sha256 " + digest + " ; ignore_errors=no ; }"
	f.runtime.paths.DropInDir = filepath.Join(filepath.Dir(f.files[0]), "drop-ins")
	if err := os.Mkdir(f.runtime.paths.DropInDir, 0755); err != nil {
		t.Fatal(err)
	}
	drop := filepath.Join(f.runtime.paths.DropInDir, "90-merchant-startup-baseline.conf")
	if err := os.WriteFile(drop, merchantStorePortableHookBytes(c.Binary, path, digest), 0644); err != nil {
		t.Fatal(err)
	}
	f.unitOutput += "ExecStartPre=" + pre + "\nExecStartPreEx=" + testPortableStartEx(pre) + "\nDropInPaths=" + drop + "\n"
	seal, err := f.runtime.captureMerchantStorePortableStartup(context.Background(), path, digest)
	if err != nil {
		t.Fatal(err)
	}
	c.Writer.StartupSHA256, c.Writer.SignedUnitSHA256 = seal["startup_sha256"], seal["signed_unit_sha256"]
	c.ExistingSchemaContract.StartupSHA256, c.ExistingSchemaContract.SignedUnitSHA256 = c.Writer.StartupSHA256, c.Writer.SignedUnitSHA256
	loaded, err := f.runtime.loadedExistingSchemaUnit(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.runtime.merchantStoreCapsuleStartup(context.Background(), c, digest, loaded); err != nil {
		t.Fatal(err)
	}
	for _, mutation := range []string{"no privileged flag", "runtime-only", "plain persistent hook", "changed persistent hook", "writable hook", "missing hook"} {
		t.Run(mutation, func(t *testing.T) {
			copy := map[string]string{}
			for key, value := range loaded {
				copy[key] = value
			}
			switch mutation {
			case "no privileged flag":
				copy["ExecStartPreEx"] = strings.Replace(copy["ExecStartPreEx"], "privileged", "", 1)
			case "runtime-only":
				copy["DropInPaths"] = "/run/systemd/system/lmm-api.service.d/runtime.conf"
			case "plain persistent hook":
				_ = os.WriteFile(drop, bytes.ReplaceAll(merchantStorePortableHookBytes(c.Binary, path, digest), []byte("ExecStartPre=+"), []byte("ExecStartPre=")), 0644)
			case "changed persistent hook":
				_ = os.WriteFile(drop, append(merchantStorePortableHookBytes(c.Binary, path, digest), []byte("Environment=UNSEALED=yes\n")...), 0644)
			case "writable hook":
				_ = os.Chmod(drop, 0664)
			case "missing hook":
				_ = os.Remove(drop)
			}
			if _, err := f.runtime.merchantStoreCapsuleStartup(context.Background(), c, digest, copy); err == nil {
				t.Fatal("persistent/loaded privilege drift accepted")
			}
			if err := os.WriteFile(drop, merchantStorePortableHookBytes(c.Binary, path, digest), 0644); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(drop, 0644); err != nil {
				t.Fatal(err)
			}
		})
	}
	// Finalizing the self-reference does not alter the normalized startup seal,
	// but every invocation must supply that exact literal and actual file hash.
	changed := strings.Repeat("b", 64)
	loaded["ExecStartPre"] = strings.Replace(loaded["ExecStartPre"], digest, changed, 1)
	if _, err := f.runtime.merchantStoreCapsuleStartup(context.Background(), c, digest, loaded); err == nil {
		t.Fatal("wrong literal capsule hash accepted")
	}
	loaded["ExecStartPreEx"] = strings.Replace(loaded["ExecStartPreEx"], digest, changed, 1)
	if err := os.WriteFile(drop, merchantStorePortableHookBytes(c.Binary, path, changed), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := f.runtime.merchantStoreCapsuleStartup(context.Background(), c, changed, loaded); err != nil {
		t.Fatal("self-reference finalization changed seal", err)
	}
	rows := strings.Split(loaded["EnvironmentFiles"], "\n")
	rows[1], rows[2] = rows[2], rows[1]
	loaded["EnvironmentFiles"] = strings.Join(rows, "\n")
	if _, err := f.runtime.merchantStoreCapsuleStartup(context.Background(), c, changed, loaded); err == nil {
		t.Fatal("ordered EnvFiles changed without invalidating startup seal")
	}
}
func TestProductionMerchantStorePortableTerminalJSONIsUnambiguous(t *testing.T) {
	for _, raw := range []string{`{"phase":"CONFIRMED","phase":"ROLLED_BACK"}`, `{"phase":"CONFIRMED","nested":{"id":1,"id":2}}`, `{"phase":"CONFIRMED"}{}`, `{"phase":"CONFIRMED"} garbage`} {
		if merchantStoreUniqueJSON([]byte(raw)) {
			t.Fatalf("ambiguous old owner JSON accepted: %s", raw)
		}
	}
	if !merchantStoreUniqueJSON([]byte(`{"phase":"CONFIRMED","nested":{"id":1},"array":[1,2]}`)) {
		t.Fatal("valid old owner JSON rejected")
	}
}

func TestProductionMerchantStorePortableHashNormalizationOnlyTouchesLiteral(t *testing.T) {
	binary, digest := "/usr/bin/lmm-api", strings.Repeat("a", 64)
	path := "/var/lib/lmm-api-go-deploy/merchant-capsules/" + digest + "/capsule.json"
	value := "{ path=" + binary + " ; argv[]=" + binary + " operator production writer-start --capsule " + path + " --capsule-sha256 " + digest + " ; ignore_errors=no ; }"
	normalized, err := merchantStorePortableStartCommand(value, testPortableStartEx(value), binary, path, digest)
	if err != nil || !strings.Contains(normalized, path) || strings.Count(normalized, merchantStoreCapsuleHashPlaceholder) != 1 || strings.Count(normalized, digest) != 1 {
		t.Fatalf("path normalized as self-reference: %q %v", normalized, err)
	}
}
func TestProductionMerchantStoreCapsuleSourceGitHasNoAmbientOverrides(t *testing.T) {
	env := merchantStoreSourceGitEnvironment()
	for _, key := range []string{"GIT_NO_REPLACE_OBJECTS=1", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null"} {
		if !strings.Contains(strings.Join(env, "\n"), key) {
			t.Fatal("missing source isolation", key)
		}
	}
	for _, key := range []string{"GIT_DIR=", "GIT_WORK_TREE=", "GIT_CONFIG_COUNT=", "GIT_REPLACE_REF_BASE=", "SQL_DSN="} {
		if strings.Contains(strings.Join(env, "\n"), key) {
			t.Fatal("ambient source override", key)
		}
	}
}
