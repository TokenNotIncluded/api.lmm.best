package appcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// These are authority/parser fixtures, not official artifact receipts.
func testMerchantStartupBaseline(t *testing.T, paths productionPaths) productionMerchantStoreCapsule {
	t.Helper()
	old := testMerchantCapsule(t, paths)
	b := &productionMerchantStartupBaseline{Kind: merchantStartupBaselineKind, Provider: old.Candidate, RequiredCapability: 1, Role: old.Writer.Role, StatusSHA256: old.Writer.Candidate.StatusSHA256,
		Closure: productionMerchantStartupClosure{Format: 1, DeploymentID: old.DeploymentID, ProviderSHA256: old.Candidate.PayloadSHA256}}
	for i, origin := range merchantStartupOrigins {
		s := *old.ExistingSchemaContract
		s.StartupSHA256 = strings.Repeat(string(rune('a'+i)), 64)
		h := productionMerchantStartupHostCapture{Format: 1, DeploymentID: old.DeploymentID, Host: origin.Host, ProviderSHA256: old.Candidate.PayloadSHA256,
			AdmissionSHA256: strings.Repeat(string(rune('c'+i)), 64), AdmissionBodySHA256: startupContentSHA256([]byte(merchantStartupAdmissionBody(old.DeploymentID))),
			NginxPID: 100 + i, NginxInvocationID: strings.Repeat("a", 32), NginxGenerationSHA256: strings.Repeat("c", 64), StartupBarrierKind: "sealed-pending-hook", StartupBarrierSHA256: strings.Repeat("d", 64),
			LegacyPID: 200 + i, LegacyInvocationID: strings.Repeat("b", 32), LegacyPayloadSHA256: strings.Repeat("e", 64), LegacyStartupSHA256: strings.Repeat("f", 64), CloseSHA256: strings.Repeat("1", 64), StatusSHA256: b.StatusSHA256,
			GateBefore: "MISSING_PRE_VARIANT", Schema: s, Role: b.Role, RequiredCapability: 1, OtherBusinessClients: 0}
		b.Closure.Hosts = append(b.Closure.Hosts, h)
	}
	s := b.Closure.Hosts[1].Schema
	return productionMerchantStoreCapsule{Format: 2, DeploymentID: old.DeploymentID, ControllerPlanSHA256: merchantStartupAuthoritySHA(b), Root: old.Root, Host: "dmit-ubuntu", Service: old.Service, Binary: old.Binary, ConfigDir: old.ConfigDir, SchemaMode: old.SchemaMode, ExistingSchemaContract: &s, StartupPolicy: "per-start", Baseline: b}
}

func TestProductionMerchantStartupBaselineSingleProviderAndOrdinaryIsolation(t *testing.T) {
	paths := defaultProductionPaths()
	c := testMerchantStartupBaseline(t, paths)
	if err := validateMerchantStoreCapsule(c, paths); err != nil {
		t.Fatal(err)
	}
	body, _ := canonicalMerchantStoreCapsule(c)
	for _, forbidden := range []string{`"candidate":`, `"rollback":`, `"merchant_store_writer":`, `"source_files":`} {
		if bytes.Contains(body, []byte(forbidden)) {
			t.Fatalf("single-provider wire included %s", forbidden)
		}
	}
	w := c.Baseline.writerIdentity(c.Host)
	if w.StartupSHA256 != c.ExistingSchemaContract.StartupSHA256 || w.StartupSHA256 == c.Baseline.Closure.Hosts[0].Schema.StartupSHA256 {
		t.Fatal("Ubuntu projected Arch startup bytes")
	}
	if w.Rollback != (productionMerchantStoreWriterTarget{}) || validateMerchantStoreWriterContract(w) == nil {
		t.Fatal("startup-only identity became ordinary recovery authority")
	}
	if _, err := acquireMerchantStoreDeploymentFence(context.Background(), nil, w); err == nil {
		t.Fatal("ordinary acquisition accepted a single-provider baseline")
	}
	r := productionRuntime{paths: paths}
	if r.portableTerminal(context.Background(), c, strings.Repeat("a", 64)) == nil {
		t.Fatal("baseline became ordinary terminal authority")
	}
	if _, err := r.verifyMerchantStoreCapsuleArtifact(context.Background(), c, true); err == nil {
		t.Fatal("baseline selected rollback")
	}
	if r.ensureMerchantStorePortableFence(context.Background(), c, strings.Repeat("a", 64), "") == nil {
		t.Fatal("baseline acquired ordinary install holder")
	}
}

func TestProductionMerchantStartupBaselineTamperAndProvenance(t *testing.T) {
	paths := defaultProductionPaths()
	good := testMerchantStartupBaseline(t, paths)
	cases := map[string]func(*productionMerchantStoreCapsule){
		"fake ordinary digest": func(c *productionMerchantStoreCapsule) { c.ControllerPlanSHA256 = strings.Repeat("0", 64) },
		"caller rollback":      func(c *productionMerchantStoreCapsule) { c.Rollback = c.Baseline.Provider },
		"caller writer pair":   func(c *productionMerchantStoreCapsule) { c.Writer = testMerchantWriterContract() },
		"one origin":           func(c *productionMerchantStoreCapsule) { c.Baseline.Closure.Hosts = c.Baseline.Closure.Hosts[:1] },
		"reordered origins": func(c *productionMerchantStoreCapsule) {
			h := c.Baseline.Closure.Hosts
			c.Baseline.Closure.Hosts = []productionMerchantStartupHostCapture{h[1], h[0]}
		},
		"remaining worker": func(c *productionMerchantStoreCapsule) { c.Baseline.Closure.Hosts[0].OtherBusinessClients = 1 },
		"runtime mask": func(c *productionMerchantStoreCapsule) {
			c.Baseline.Closure.Hosts[0].StartupBarrierKind = "runtime-mask"
		},
		"nginx disk only":     func(c *productionMerchantStoreCapsule) { c.Baseline.Closure.Hosts[0].NginxGenerationSHA256 = "" },
		"missing old startup": func(c *productionMerchantStoreCapsule) { c.Baseline.Closure.Hosts[0].LegacyStartupSHA256 = "" },
		"physical other DB":   func(c *productionMerchantStoreCapsule) { c.Baseline.Closure.Hosts[0].Schema.DatabaseOID++ },
		"wrong role":          func(c *productionMerchantStoreCapsule) { c.Baseline.Role = "other" },
		"status replay":       func(c *productionMerchantStoreCapsule) { c.Baseline.StatusSHA256 = strings.Repeat("0", 64) },
		"implicit floor follow": func(c *productionMerchantStoreCapsule) {
			c.Baseline.RequiredCapability = 2
			for i := range c.Baseline.Closure.Hosts {
				c.Baseline.Closure.Hosts[i].RequiredCapability = 2
			}
		},
		"missing predecessor": func(c *productionMerchantStoreCapsule) {
			for i := range c.Baseline.Closure.Hosts {
				c.Baseline.Closure.Hosts[i].GateBefore = "CONFIRMED_BASELINE:" + strings.Repeat("a", 64)
			}
		},
		"legacy cap0":           func(c *productionMerchantStoreCapsule) { c.Baseline.Provider.MerchantStoreWriterCapability = 0 },
		"unknown cap6":          func(c *productionMerchantStoreCapsule) { c.Baseline.Provider.MerchantStoreWriterCapability = 6 },
		"official issuer spoof": func(c *productionMerchantStoreCapsule) { c.Baseline.Provider.Workflow = "custom.yml" },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			c := cloneMerchantCapsule(good)
			change(&c)
			if validateMerchantStoreCapsule(c, paths) == nil {
				t.Fatal("tampered startup authority accepted")
			}
		})
	}
	// A successor is explicit provenance, not automatic following of a new floor.
	c := cloneMerchantCapsule(good)
	c.Baseline.PredecessorSHA256 = strings.Repeat("a", 64)
	c.Baseline.RequiredCapability = 4
	for i := range c.Baseline.Closure.Hosts {
		c.Baseline.Closure.Hosts[i].GateBefore = "CONFIRMED_BASELINE:" + c.Baseline.PredecessorSHA256
		c.Baseline.Closure.Hosts[i].RequiredCapability = 4
	}
	c.ControllerPlanSHA256 = merchantStartupAuthoritySHA(c.Baseline)
	if err := validateMerchantStoreCapsule(c, paths); err != nil {
		t.Fatal(err)
	}
}

func TestProductionMerchantStartupBaselineCanonicalHostLoad(t *testing.T) {
	paths := defaultProductionPaths()
	paths.WorkRoot = filepath.Join(t.TempDir(), "work")
	c := testMerchantStartupBaseline(t, paths)
	if err := os.MkdirAll(c.Root, 0700); err != nil {
		t.Fatal(err)
	}
	r := productionRuntime{paths: paths, requiredOwnerUID: uint32(os.Geteuid()), hostname: func() (string, error) { return c.Host, nil }}
	body, _ := canonicalMerchantStoreCapsule(c)
	path := filepath.Join(c.Root, "capsule.json")
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := r.loadMerchantStoreCapsule(path, startupContentSHA256(body))
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Writer == nil || loaded.Candidate != c.Baseline.Provider || loaded.Rollback != (productionReleasePackagePlan{}) || loaded.Writer.StartupSHA256 != c.ExistingSchemaContract.StartupSHA256 {
		t.Fatal("single provider projection differs")
	}
	projected, _ := canonicalMerchantStoreCapsule(loaded)
	if !bytes.Equal(projected, body) {
		t.Fatal("runtime projection altered signed wire")
	}
	malformed := append([]byte(" "), body...)
	if os.WriteFile(path, malformed, 0600) != nil {
		t.Fatal("write")
	}
	if _, err := r.loadMerchantStoreCapsule(path, startupContentSHA256(malformed)); err == nil {
		t.Fatal("noncanonical baseline accepted")
	}
	if os.WriteFile(path, body, 0600) != nil {
		t.Fatal("write")
	}
	r.hostname = func() (string, error) { return "arch-dmit", nil }
	if _, err := r.loadMerchantStoreCapsule(path, startupContentSHA256(body)); err == nil {
		t.Fatal("baseline crossed actual host")
	}
}

func TestProductionMerchantStartupAdmissionCanonicalDirectOrigin(t *testing.T) {
	id := "baseline-test"
	original := []byte("location @lmm_api_backend { proxy_pass http://127.0.0.1:3000; }\n")
	b, err := merchantStartupAdmissionBarrier(original, id)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte("Retry-After '60' always")) || !bytes.HasSuffix(b, original) || bytes.Contains(b, []byte("lmm-credit-transition")) {
		t.Fatal("admission marker/body drift")
	}
	if _, err := merchantStartupAdmissionBarrier(b, id); err == nil {
		t.Fatal("nested closure accepted")
	}
	good := "HTTP/1.1 503 Service Temporarily Unavailable\r\nRetry-After: 60\r\nX-LMM-Startup-Baseline: " + id + "\r\nContent-Type: text/plain\r\n\r\n" + merchantStartupAdmissionBody(id)
	for name, response := range map[string]string{"valid": good, "wrong header": strings.Replace(good, "Retry-After: 60", "Retry-After: 1", 1), "other identity": strings.Replace(good, "X-LMM-Startup-Baseline: "+id, "X-LMM-Startup-Baseline: other", 1), "wrong status": strings.Replace(good, "503", "200", 1), "wrong body": good + "\n", "ambiguous header": strings.Replace(good, "Retry-After: 60", "Retry-After: 60\r\nRetry-After: 60", 1)} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			r := productionRuntime{paths: defaultProductionPaths(), runner: existingSchemaTestRunner{run: func(c productionCommand) ([]byte, error) {
				calls++
				args := strings.Join(c.Args, " ")
				if c.Name != "/usr/bin/curl" || !strings.Contains(args, "--connect-to api.lmm.best:443:127.0.0.1:443") || strings.Contains(args, "--insecure") || strings.Contains(args, "--location") || c.Sensitive {
					t.Fatal("probe did not use fixed direct TLS origin")
				}
				return []byte(response), nil
			}}}
			err := r.probeMerchantStartupAdmission(context.Background(), id)
			if (err == nil) != (name == "valid") {
				t.Fatalf("err=%v", err)
			}
			if name == "valid" && calls != 3 {
				t.Fatal("not all fixed routes were checked")
			}
		})
	}
}

func TestProductionMerchantStartupBaselineMissingProofAndFlags(t *testing.T) {
	paths := defaultProductionPaths()
	paths.WorkRoot = filepath.Join(t.TempDir(), "work")
	r := productionRuntime{paths: paths, requiredOwnerUID: uint32(os.Geteuid()), runner: existingSchemaTestRunner{run: func(productionCommand) ([]byte, error) {
		t.Fatal("missing protected evidence reached external execution")
		return nil, errors.New("unreachable")
	}}}
	if _, err := r.captureMerchantStartupHost(context.Background(), "baseline-missing"); err == nil {
		t.Fatal("missing capture became authority")
	}
	for _, args := range [][]string{{"baseline-capture", "--id", "test", "--approved"}, {"baseline-seal", "--id", "test", "--closure-file", "/tmp/claim"}, {"baseline-capture", "--id", "../test"}, {"baseline-unknown", "--id", "test", "--arbitrary-host", "other"}} {
		if runProductionMerchantStartupBaseline(args, &bytes.Buffer{}, &bytes.Buffer{}) != ExitUsage {
			t.Fatalf("unchecked claim flag accepted: %v", args)
		}
	}
	var c productionMerchantStoreCapsule
	if json.Unmarshal([]byte(`{"format":2,"startup_baseline":{"kind":"lmm-startup-baseline-v1"}}`), &c) != nil {
		t.Fatal("fixture")
	}
	if validateMerchantStoreCapsule(c, paths) == nil {
		t.Fatal("caller kind alone granted authority")
	}
}

func TestProductionMerchantStartupBaselineCompletionExactIdentity(t *testing.T) {
	c := testMerchantStartupBaseline(t, defaultProductionPaths())
	c.Writer = c.Baseline.writerIdentity(c.Host)
	c.Candidate = c.Baseline.Provider
	id, inv, digest := c.DeploymentID, strings.Repeat("c", 32), strings.Repeat("d", 64)
	w := c.Writer
	o := productionMerchantStoreFenceOwner{Format: 1, State: "ACTIVE", DeploymentID: id, Host: c.Host, PlanSHA256: digest, ContractSHA256: merchantStoreFenceContractSHA(w), ProviderSHA256: c.Candidate.PayloadSHA256, Nonce: strings.Repeat("e", 32), HolderPID: 100, HolderUnit: merchantStoreStartUnit(id, inv), HolderInvocationID: strings.Repeat("f", 32), BackendPID: 200, SystemIdentifier: w.SystemIdentifier, Database: w.Database, DatabaseOID: w.DatabaseOID, Schema: w.Schema, SchemaOID: w.SchemaOID, Role: w.Role, Purpose: "start", Service: c.Service, StartInvocationID: inv}
	s := productionMerchantStartupSucceeded{Format: 1, CapsuleSHA256: digest, Owner: o, PID: 300, InvocationID: inv}
	state := map[string]string{"MainPID": strconv.Itoa(s.PID), "InvocationID": inv}
	if err := validateMerchantStartupCompletion(c, digest, state, s); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*productionMerchantStartupSucceeded){"old invocation": func(s *productionMerchantStartupSucceeded) { s.InvocationID = strings.Repeat("a", 32) }, "other role": func(s *productionMerchantStartupSucceeded) { s.Owner.Role = "other" }, "other DB": func(s *productionMerchantStartupSucceeded) { s.Owner.DatabaseOID++ }, "other schema": func(s *productionMerchantStartupSucceeded) { s.Owner.SchemaOID++ }, "other provider": func(s *productionMerchantStartupSucceeded) { s.Owner.ProviderSHA256 = strings.Repeat("a", 64) }, "ordinary owner": func(s *productionMerchantStartupSucceeded) { s.Owner.Purpose = "portable-deploy" }, "other seal": func(s *productionMerchantStartupSucceeded) { s.CapsuleSHA256 = strings.Repeat("a", 64) }} {
		t.Run(name, func(t *testing.T) {
			bad := s
			change(&bad)
			if validateMerchantStartupCompletion(c, digest, state, bad) == nil {
				t.Fatal("unrelated completion accepted")
			}
		})
	}
}

func TestProductionMerchantStartupBaselineRepeatsOfficialSignatureBeforeProvider(t *testing.T) {
	paths := defaultProductionPaths()
	paths.WorkRoot = filepath.Join(t.TempDir(), "work")
	c := testMerchantStartupBaseline(t, paths)
	if err := os.MkdirAll(filepath.Join(c.Root, "assets"), 0700); err != nil {
		t.Fatal(err)
	}
	p := &c.Baseline.Provider
	for _, f := range []struct {
		path string
		hash *string
	}{{p.PackagePath, &p.PackageSHA256}, {p.ReleaseAsset, &p.ReleaseAssetSHA256}, {p.SignatureBundle, &p.SignatureBundleSHA256}} {
		body := []byte("signature failure fixture " + f.path)
		if err := os.WriteFile(filepath.Join(c.Root, f.path), body, 0600); err != nil {
			t.Fatal(err)
		}
		*f.hash = startupContentSHA256(body)
	}
	c.Writer = c.Baseline.writerIdentity(c.Host)
	c.Candidate = *p
	calls := 0
	r := productionRuntime{paths: paths, requiredOwnerUID: uint32(os.Geteuid()), runner: existingSchemaTestRunner{run: func(cmd productionCommand) ([]byte, error) {
		calls++
		if cmd.Name != commandCosign || !strings.Contains(strings.Join(cmd.Args, " "), productionReleaseOIDCIssuer) {
			t.Fatal("reached provider before official issuer")
		}
		return nil, errors.New("official signature failed")
	}}}
	if _, err := r.verifyMerchantStoreCapsuleArtifact(context.Background(), c, false); err == nil || calls != 1 {
		t.Fatal("single provider skipped official issuer or executed retained")
	}
}
