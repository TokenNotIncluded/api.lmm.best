package deploycli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestProductionMerchantStoreOrdinaryStartupRejectsActivating(t *testing.T) {
	f := realShapeStartupFixture(t, "arch")
	sealFixture(t, f)
	f.unitOutput = strings.ReplaceAll(f.unitOutput, "MainPID=1234", "MainPID=0")
	f.unitOutput = strings.ReplaceAll(f.unitOutput, "ActiveState=active", "ActiveState=activating")
	if err := f.runtime.verifyExistingSchemaStartupMode(context.Background(), productionManifest{SchemaMode: productionSchemaModeVerifyExisting, ExistingSchemaContract: f.contract}); err == nil || !strings.Contains(err.Error(), "process generation is unavailable") {
		t.Fatalf("ordinary startup check must not authorize activating MainPID=0: %v", err)
	}
}

func TestProductionMerchantStoreSealedStartHookOnlyAcceptsExactNativeChecker(t *testing.T) {
	binary := "/usr/bin/lmm-api"
	workspace := filepath.Join(defaultProductionPaths().WorkRoot, "release-start-test")
	good := "{ path=" + binary + " ; argv[]=" + binary + " operator production writer-start-check --workspace " + workspace + " ; ignore_errors=no ; }"
	if actual, err := merchantStoreSealedStartCommand(good, binary); err != nil || actual != binary+"\x00"+binary+" operator production writer-start-check --workspace "+workspace+"\x00no" {
		t.Fatalf("canonical native hook was rejected: %q %v", actual, err)
	}
	for name, command := range map[string]string{
		"shell":               strings.ReplaceAll(good, binary, "/bin/sh"),
		"alternate binary":    strings.ReplaceAll(good, binary, "/tmp/lmm-api"),
		"write operation":     strings.Replace(good, "writer-start-check", "apply", 1),
		"override":            strings.Replace(good, " ; ignore_errors", " --skip-writer-check ; ignore_errors", 1),
		"ignore errors":       strings.Replace(good, "ignore_errors=no", "ignore_errors=yes", 1),
		"unowned workspace":   strings.Replace(good, workspace, "/tmp/release-start-test", 1),
		"relative workspace":  strings.Replace(good, workspace, "work/release-start-test", 1),
		"traversal workspace": strings.Replace(good, workspace, defaultProductionPaths().WorkRoot+"/../release-start-test", 1),
		"duplicate command":   good + "\n" + good,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := merchantStoreSealedStartCommand(command, binary); err == nil {
				t.Fatal("unsafe lifecycle hook was accepted")
			}
		})
	}
}

func TestProductionMerchantStoreCandidateStartHookSealsPrivilegeAndWorkspace(t *testing.T) {
	workspace := filepath.Join(defaultProductionPaths().WorkRoot, "release-candidate-checker")
	entry := merchantStoreHeldStartEntrypoint(workspace)
	args := entry + " operator production writer-start-check --workspace " + workspace
	pre := "{ path=" + entry + " ; argv[]=" + args + " ; ignore_errors=no ; }"
	ex := "{ path=" + entry + " ; argv[]=" + args + " ; flags=privileged ; }"
	loaded := map[string]string{
		"ExecStart":    "{ path=/usr/bin/lmm-api ; argv[]=/usr/bin/lmm-api serve ; ignore_errors=no ; }",
		"ExecStartPre": pre, "ExecStartPreEx": ex,
	}
	commands, err := verifyExistingSchemaSealedCommands(loaded, "/usr/bin/lmm-api")
	if err != nil || commands["ExecStartPreEx"] != "privileged" {
		t.Fatalf("candidate checker privilege was not sealed: %v", err)
	}
	for name, change := range map[string]func(map[string]string){
		"missing privilege": func(m map[string]string) { m["ExecStartPreEx"] = "" },
		"no plus":           func(m map[string]string) { m["ExecStartPreEx"] = strings.Replace(ex, "privileged", "", 1) },
		"ignored errors": func(m map[string]string) {
			m["ExecStartPre"] = strings.Replace(pre, "ignore_errors=no", "ignore_errors=yes", 1)
		},
		"extra flag": func(m map[string]string) {
			m["ExecStartPreEx"] = strings.Replace(ex, "privileged", "privileged ignore-failure", 1)
		},
		"other workspace": func(m map[string]string) {
			m["ExecStartPre"] = strings.Replace(pre, "--workspace "+workspace, "--workspace "+workspace+"-other", 1)
		},
		"rollback checker": func(m map[string]string) {
			m["ExecStartPre"] = strings.ReplaceAll(pre, "merchant-store-candidate", "merchant-store-rollback")
		},
		"alternate copy":    func(m map[string]string) { m["ExecStartPre"] = strings.ReplaceAll(pre, entry, "/tmp/lmm-api") },
		"different ex args": func(m map[string]string) { m["ExecStartPreEx"] = strings.Replace(ex, "writer-start-check", "apply", 1) },
		"extra ex command":  func(m map[string]string) { m["ExecStartPreEx"] = ex + "\n" + ex },
	} {
		t.Run(name, func(t *testing.T) {
			copy := map[string]string{}
			for key, value := range loaded {
				copy[key] = value
			}
			change(copy)
			if _, err := verifyExistingSchemaSealedCommands(copy, "/usr/bin/lmm-api"); err == nil {
				t.Fatal("unsafe candidate checker was sealed")
			}
		})
	}
}

type merchantHeldStartFixture struct {
	startup   *sealedStartupFixture
	held      *merchantStoreHeldStartContext
	manifest  productionManifest
	loaded    map[string]string
	state     map[string]string
	authority *merchantStoreHookAuthorityFixture
	candidate []byte
}

func newMerchantHeldStartFixture(t *testing.T) *merchantHeldStartFixture {
	t.Helper()
	startup := realShapeStartupFixture(t, "arch")
	workspace := productionWorkspace{root: t.TempDir(), id: "held-fixture"}
	workspace.manifestPath = filepath.Join(workspace.root, "manifest.json")
	if err := os.WriteFile(workspace.manifestPath, []byte("immutable fixture manifest\n"), 0600); err != nil {
		t.Fatal(err)
	}
	entry := merchantStoreHeldStartEntrypoint(workspace.root)
	if err := os.MkdirAll(filepath.Dir(entry), 0700); err != nil {
		t.Fatal(err)
	}
	candidate := []byte("qualified candidate checker\n")
	if err := os.WriteFile(filepath.Join(filepath.Dir(entry), backendGoName), candidate, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(backendGoName, entry); err != nil {
		t.Fatal(err)
	}
	installed := filepath.Join(workspace.root, "installed-provider")
	if err := os.WriteFile(installed, candidate, 0700); err != nil {
		t.Fatal(err)
	}
	startup.runtime.paths.InstalledBinary = installed
	startup.runtime.effectiveUID = func() int { return 0 }
	startup.runtime.billingExecutableSHA256 = func(pid int) (string, error) {
		if pid != os.Getpid() {
			t.Fatalf("checker did not hash itself: %d", pid)
		}
		return startupContentSHA256(candidate), nil
	}
	invocation := strings.Repeat("a", 32)
	t.Setenv("INVOCATION_ID", invocation)
	contract := testMerchantWriterContract()
	contract.Candidate.PayloadSHA256 = startupContentSHA256(candidate)
	contract.Rollback.PayloadSHA256 = startupContentSHA256([]byte("retained writer\n"))
	manifest := productionManifest{SchemaMode: productionSchemaModeVerifyExisting, SchemaPlanSHA256: strings.Repeat("b", 64), ExistingSchemaContract: startup.contract, MerchantStoreWriter: contract}
	manifestSHA, err := sha256File(workspace.manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	args := entry + " operator production writer-start-check --workspace " + workspace.root
	f := &merchantHeldStartFixture{
		startup: startup, manifest: manifest, candidate: candidate,
		held:      &merchantStoreHeldStartContext{workspace: workspace, manifest: manifest, manifestSHA: manifestSHA, invocation: invocation},
		loaded:    map[string]string{"InvocationID": invocation, "ActiveState": "activating", "MainPID": "0", "ExecStartPre": "{ path=" + entry + " ; argv[]=" + args + " ; ignore_errors=no ; }", "ExecStartPreEx": "{ path=" + entry + " ; argv[]=" + args + " ; flags=privileged ; }"},
		state:     map[string]string{"InvocationID": invocation, "ActiveState": "activating", "SubState": "start-pre", "MainPID": "0", "ControlPID": strconv.Itoa(os.Getpid()), "TimeoutStartUSec": "90s", "InactiveExitTimestampMonotonic": "1000000"},
		authority: &merchantStoreHookAuthorityFixture{runner: &fakeProductionRunner{}},
	}
	startup.runtime.merchantStoreAuthority = f.authority
	startup.runtime.runner = existingSchemaTestRunner{run: func(command productionCommand) ([]byte, error) {
		if command.Name != commandSystemctl || !strings.Contains(strings.Join(command.Args, " "), "ControlPID") {
			return nil, errors.New("unexpected non-state command")
		}
		var body strings.Builder
		for _, key := range []string{"InvocationID", "ActiveState", "SubState", "MainPID", "ControlPID", "TimeoutStartUSec", "InactiveExitTimestampMonotonic"} {
			fmt.Fprintf(&body, "%s=%s\n", key, f.state[key])
		}
		return []byte(body.String()), nil
	}}
	return f
}

func (f *merchantHeldStartFixture) verify() error {
	return f.startup.runtime.verifyMerchantStoreHeldStart(context.Background(), f.held, f.manifest, f.loaded)
}

// These are process/manifest/holder binding component tests; real systemd and
// signed candidate/retained packages require separate VM qualification.
func TestProductionMerchantStoreHeldStartCandidateAndRetained(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		t.Run(fmt.Sprint(rollback), func(t *testing.T) {
			f := newMerchantHeldStartFixture(t)
			if rollback {
				if err := os.WriteFile(f.startup.runtime.paths.InstalledBinary, []byte("retained writer\n"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			if err := f.verify(); err != nil {
				t.Fatal(err)
			}
			if f.authority.requests != 1 {
				t.Fatal("held check omitted live holder proof")
			}
			f.state["InvocationID"] = strings.Repeat("c", 32)
			if f.verify() == nil {
				t.Fatal("generation change after qualification was accepted")
			}
		})
	}
}

func TestProductionMerchantStoreHeldStartRefusesUnboundContext(t *testing.T) {
	for name, change := range map[string]func(*testing.T, *merchantHeldStartFixture){
		"nonroot": func(t *testing.T, f *merchantHeldStartFixture) {
			f.startup.runtime.effectiveUID = func() int { return 1000 }
		},
		"missing invocation":      func(t *testing.T, f *merchantHeldStartFixture) { t.Setenv("INVOCATION_ID", "") },
		"changed invocation":      func(t *testing.T, f *merchantHeldStartFixture) { f.state["InvocationID"] = strings.Repeat("c", 32) },
		"not control process":     func(t *testing.T, f *merchantHeldStartFixture) { f.state["ControlPID"] = "1" },
		"start post":              func(t *testing.T, f *merchantHeldStartFixture) { f.state["SubState"] = "start-post" },
		"active process":          func(t *testing.T, f *merchantHeldStartFixture) { f.state["ActiveState"] = "active" },
		"has main pid":            func(t *testing.T, f *merchantHeldStartFixture) { f.state["MainPID"] = "1234" },
		"stale loaded generation": func(t *testing.T, f *merchantHeldStartFixture) { f.loaded["InvocationID"] = strings.Repeat("c", 32) },
		"missing privilege":       func(t *testing.T, f *merchantHeldStartFixture) { f.loaded["ExecStartPreEx"] = "" },
		"other workspace": func(t *testing.T, f *merchantHeldStartFixture) {
			f.loaded["ExecStartPre"] = strings.ReplaceAll(f.loaded["ExecStartPre"], f.held.workspace.root, f.held.workspace.root+"-other")
		},
		"installed checker": func(t *testing.T, f *merchantHeldStartFixture) {
			f.loaded["ExecStartPre"] = strings.ReplaceAll(f.loaded["ExecStartPre"], merchantStoreHeldStartEntrypoint(f.held.workspace.root), f.startup.runtime.paths.InstalledBinary)
		},
		"wrong self payload": func(t *testing.T, f *merchantHeldStartFixture) {
			f.startup.runtime.billingExecutableSHA256 = func(int) (string, error) { return strings.Repeat("e", 64), nil }
		},
		"wrong installed writer": func(t *testing.T, f *merchantHeldStartFixture) {
			if err := os.WriteFile(f.startup.runtime.paths.InstalledBinary, []byte("unqualified"), 0700); err != nil {
				t.Fatal(err)
			}
		},
		"changed provider": func(t *testing.T, f *merchantHeldStartFixture) {
			p := filepath.Join(filepath.Dir(merchantStoreHeldStartEntrypoint(f.held.workspace.root)), backendGoName)
			if err := os.WriteFile(p, []byte("changed"), 0700); err != nil {
				t.Fatal(err)
			}
		},
		"provider mode": func(t *testing.T, f *merchantHeldStartFixture) {
			p := filepath.Join(filepath.Dir(merchantStoreHeldStartEntrypoint(f.held.workspace.root)), backendGoName)
			if err := os.Chmod(p, 0755); err != nil {
				t.Fatal(err)
			}
		},
		"entry symlink": func(t *testing.T, f *merchantHeldStartFixture) {
			p := merchantStoreHeldStartEntrypoint(f.held.workspace.root)
			if err := os.Remove(p); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(f.startup.runtime.paths.InstalledBinary, p); err != nil {
				t.Fatal(err)
			}
		},
		"changed manifest": func(t *testing.T, f *merchantHeldStartFixture) {
			if err := os.WriteFile(f.held.workspace.manifestPath, []byte("changed"), 0600); err != nil {
				t.Fatal(err)
			}
		},
		"other plan":     func(t *testing.T, f *merchantHeldStartFixture) { f.manifest.SchemaPlanSHA256 = strings.Repeat("c", 64) },
		"missing holder": func(t *testing.T, f *merchantHeldStartFixture) { f.startup.runtime.merchantStoreAuthority = nil },
		"lost holder":    func(t *testing.T, f *merchantHeldStartFixture) { f.authority.failRequest = 1 },
	} {
		t.Run(name, func(t *testing.T) {
			f := newMerchantHeldStartFixture(t)
			change(t, f)
			if err := f.verify(); err == nil {
				t.Fatal("unbound held startup was accepted")
			}
		})
	}
}

func TestProductionMerchantStoreHeldStartRechecksAfterHolder(t *testing.T) {
	f := newMerchantHeldStartFixture(t)
	original := f.startup.runtime.runner
	reads := 0
	f.startup.runtime.runner = existingSchemaTestRunner{run: func(command productionCommand) ([]byte, error) {
		reads++
		if reads == 2 {
			f.state["ControlPID"] = "1"
		}
		return original.Run(context.Background(), command)
	}}
	if err := f.verify(); err == nil || !strings.Contains(err.Error(), "generation changed during holder inspection") {
		t.Fatalf("holder response outlived its actual control generation: %v", err)
	}
	if f.authority.requests != 1 {
		t.Fatal("test did not cross the live holder response")
	}
}

func TestProductionMerchantStoreHeldStartLostFinalHolder(t *testing.T) {
	f := newMerchantHeldStartFixture(t)
	f.authority.failRequest = 2
	if err := f.verify(); err != nil {
		t.Fatal(err)
	}
	if f.verify() == nil {
		t.Fatal("lost holder after first successful check was accepted")
	}
}
