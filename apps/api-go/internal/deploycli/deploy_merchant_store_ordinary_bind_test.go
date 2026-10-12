package deploycli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestMerchantStoreOrdinaryBindPriorIntentDoesNotTouchAuthority(t *testing.T) {
	for _, kind := range []string{"empty", "malformed", "symlink", "directory"} {
		t.Run(kind, func(t *testing.T) {
			paths := defaultProductionPaths()
			paths.WorkRoot = filepath.Join(t.TempDir(), "work")
			c := testMerchantCapsule(t, paths)
			c.Writer.StartupPredecessorPath = filepath.Join(merchantStoreCapsuleRoot(paths, "predecessor"), "capsule.json")
			c.Writer.StartupPredecessorSHA256 = strings.Repeat("a", 64)
			c.Writer.StartupTargetPath = filepath.Join(c.Root, "capsule.json")
			state := filepath.Join(c.Root, "state")
			if err := os.MkdirAll(state, 0700); err != nil {
				t.Fatal(err)
			}
			intent := filepath.Join(state, "ordinary-startup-bind-intent.json")
			var err error
			switch kind {
			case "empty":
				err = os.WriteFile(intent, nil, 0600)
			case "malformed":
				err = os.WriteFile(intent, []byte("partial prior attempt"), 0600)
			case "symlink":
				err = os.Symlink(filepath.Join(state, "missing"), intent)
			case "directory":
				err = os.Mkdir(intent, 0700)
			}
			if err != nil {
				t.Fatal(err)
			}
			runner := &fakeProductionRunner{}
			runtime := &productionRuntime{paths: paths, runner: runner, effectiveUID: func() int { return 0 }}
			if runtime.bindMerchantStoreOrdinaryStartup(context.Background(), c, strings.Repeat("b", 64)) == nil {
				t.Fatal("replayed prior intent")
			}
			if len(runner.commands) != 0 {
				t.Fatal("prior intent reached a command or mutation")
			}
			entries, err := os.ReadDir(state)
			if err != nil || len(entries) != 1 {
				t.Fatal("prior intent refusal wrote new evidence")
			}
		})
	}
}

type ordinaryStoppedWriterFixtureRunner struct {
	unit       map[string]string
	journal    []byte
	invocation string
	commands   []productionCommand
}

func (f *ordinaryStoppedWriterFixtureRunner) Run(_ context.Context, command productionCommand) ([]byte, error) {
	f.commands = append(f.commands, command)
	if command.Name == commandSystemctl && len(command.Args) > 1 && command.Args[0] == "show" && command.Args[1] == "lmm-api.service" {
		var out strings.Builder
		for _, key := range []string{"MainPID", "ExecMainPID", "ExecMainCode", "ExecMainStatus", "ActiveState", "SubState", "Result", "ControlGroup", "Restart", "InvocationID"} {
			fmt.Fprintf(&out, "%s=%s\n", key, f.unit[key])
		}
		return []byte(out.String()), nil
	}
	if command.Name == commandJournalctl && strings.Join(command.Args, " ") == "--no-pager -o cat _SYSTEMD_INVOCATION_ID="+f.invocation {
		return f.journal, nil
	}
	return nil, errors.New("fixture rejects every service/database mutation and unknown command")
}

func TestMerchantStoreOrdinaryStoppedWriterBindsActualPrivateEvidence(t *testing.T) {
	// The process really exited; service/journal inventories are fixtures and
	// do not claim to qualify an installed systemd service or official binary.
	process := exec.Command("/usr/bin/true")
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	pid := process.Process.Pid
	if err := process.Wait(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join("/proc", strconv.Itoa(pid))); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("fixture process death is unknown")
	}
	for _, name := range []string{"valid", "running", "wrong invocation", "changed journal", "changed previous", "missing proof", "unsafe proof"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			paths := defaultProductionPaths()
			paths.WorkRoot = filepath.Join(root, "arch-work")
			c := testMerchantCapsule(t, paths)
			c.Candidate.DeployEngineSHA256 = strings.Repeat("b", 64)
			old := cloneMerchantCapsule(c)
			work := filepath.Join(root, "standalone", c.DeploymentID)
			if err := os.MkdirAll(work, 0700); err != nil {
				t.Fatal(err)
			}
			invocation := strings.Repeat("a", 32)
			journal := []byte("received signal: terminated\nrefund_tasks execution_complete=true accepted=0 finished=0 active=0 failed=0 (execution completion is not financial success)\nserver exited\n")
			saved := []byte(strings.TrimSpace(string(journal)))
			proof := merchantStoreOrdinaryStopped{"lmm-ordinary-writer-stopped-v1", c.DeploymentID, old.Candidate.PayloadSHA256, pid, invocation, startupContentSHA256(saved)}
			if name == "changed previous" {
				proof.PreviousSHA256 = strings.Repeat("f", 64)
			}
			proofRaw, _ := json.Marshal(proof)
			proofPath := filepath.Join(work, "ordinary-writer-stopped.json")
			if err := os.WriteFile(proofPath, proofRaw, 0600); err != nil {
				t.Fatal(err)
			}
			state, _ := json.Marshal(map[string]any{"release": c.DeploymentID, "phase": "MUTATION_PENDING", "sha256": c.Candidate.PayloadSHA256, "previous_sha256": old.Candidate.PayloadSHA256, "deploy_engine_sha256": c.Candidate.DeployEngineSHA256, "migrate": false})
			if err := os.WriteFile(filepath.Join(work, "state.json"), state, 0644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(work, "shutdown.log"), saved, 0644); err != nil {
				t.Fatal(err)
			}
			if name == "missing proof" || name == "unsafe proof" {
				if err := os.Remove(proofPath); err != nil {
					t.Fatal(err)
				}
			}
			if name == "unsafe proof" {
				if err := os.Symlink(filepath.Join(work, "state.json"), proofPath); err != nil {
					t.Fatal(err)
				}
			}
			runner := &ordinaryStoppedWriterFixtureRunner{unit: map[string]string{"MainPID": "0", "ExecMainPID": strconv.Itoa(pid), "ExecMainCode": "1", "ExecMainStatus": "0", "ActiveState": "inactive", "SubState": "dead", "Result": "success", "ControlGroup": "", "Restart": "no", "InvocationID": invocation}, journal: journal, invocation: invocation}
			switch name {
			case "running":
				runner.unit["MainPID"] = strconv.Itoa(os.Getpid())
				runner.unit["ActiveState"] = "active"
			case "wrong invocation":
				runner.unit["InvocationID"] = strings.Repeat("c", 32)
			case "changed journal":
				runner.journal = append(append([]byte(nil), journal...), []byte("changed\n")...)
			}
			runtime := &productionRuntime{paths: paths, runner: runner, requiredOwnerUID: uint32(os.Getuid()), portableWorkRoot: filepath.Join(root, "standalone")}
			got, err := runtime.ordinaryStoppedWriter(context.Background(), c, old)
			if name == "valid" {
				if err != nil || got != proof {
					t.Fatalf("actual stopped proof rejected: %v", err)
				}
			} else if err == nil {
				t.Fatal("accepted unsafe stop evidence")
			}
			for _, command := range runner.commands {
				if command.Name != commandJournalctl && (command.Name != commandSystemctl || command.Args[0] != "show") {
					t.Fatal("stop observation dispatched a mutation")
				}
			}
			afterState, _ := os.ReadFile(filepath.Join(work, "state.json"))
			afterLog, _ := os.ReadFile(filepath.Join(work, "shutdown.log"))
			if string(afterState) != string(state) || string(afterLog) != string(saved) {
				t.Fatal("stop proof verification rewrote original files")
			}
			entries, _ := os.ReadDir(work)
			expected := 3
			if name == "missing proof" {
				expected = 2
			}
			if len(entries) != expected {
				t.Fatal("stop proof verification created evidence")
			}
		})
	}
}

func TestMerchantStoreOrdinaryStandaloneHistoricalPrivateFiles(t *testing.T) {
	work := t.TempDir()
	if err := os.Chmod(work, 0700); err != nil {
		t.Fatal(err)
	}
	runtime := &productionRuntime{requiredOwnerUID: uint32(os.Getuid())}
	path := filepath.Join(work, "shutdown.log")
	for _, mode := range []os.FileMode{0600, 0644} {
		if err := os.WriteFile(path, []byte("original shutdown bytes"), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
		got, err := runtime.ordinaryStandaloneFile(work, "shutdown.log")
		if err != nil || string(got) != "original shutdown bytes" {
			t.Fatalf("actual historical mode %o rejected: %v", mode, err)
		}
		info, _ := os.Lstat(path)
		if info.Mode().Perm() != mode {
			t.Fatal("reader rewrote original file mode")
		}
	}
	if err := os.Chmod(path, 0666); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.ordinaryStandaloneFile(work, "shutdown.log"); err == nil {
		t.Fatal("accepted replaceable log")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(work, "missing"), path); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.ordinaryStandaloneFile(work, "shutdown.log"); err == nil {
		t.Fatal("accepted dangling log as missing")
	}
	if _, err := runtime.ordinaryStandaloneFile(work, "../other"); err == nil {
		t.Fatal("accepted arbitrary file")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	large := []byte(strings.Repeat("x", 2<<20))
	if err := os.WriteFile(path, large, 0600); err != nil {
		t.Fatal(err)
	}
	if raw, err := runtime.ordinaryStandaloneFile(work, "shutdown.log"); err != nil || len(raw) != len(large) {
		t.Fatalf("native journal reader disagrees with the wrapper's 8MiB bound: %v", err)
	}
}

func TestMerchantStoreOrdinaryShutdownAuditsActualSuffix(t *testing.T) {
	refund := "refund_tasks execution_complete=true accepted=0 finished=0 active=0 failed=0 (execution completion is not financial success)"
	actual := []byte("quota dashboard flush: persisted=2 failed=0 dropped=0\nquota dashboard flush: persisted=3 failed=0 dropped=0\nreceived signal: terminated\n" + refund + "\nserver exited\n")
	saved := []byte(strings.TrimSpace(string(actual)))
	if err := merchantStoreOrdinaryShutdownJournal(actual, saved); err != nil {
		t.Fatal(err)
	}
	for name, suffix := range map[string]string{
		"refund active":    strings.Replace(string(actual), "active=0", "active=1", 1),
		"no refund":        strings.Replace(string(actual), refund, "", 1),
		"no exit":          strings.Replace(string(actual), "server exited", "", 1),
		"no signal":        strings.Replace(string(actual), "received signal:", "", 1),
		"error":            string(actual) + "panic: rejected\n",
		"duplicate refund": string(actual) + refund + "\n",
		"incomplete batch": string(actual) + "batch update started\n",
	} {
		t.Run(name, func(t *testing.T) {
			raw := []byte(suffix)
			if merchantStoreOrdinaryShutdownJournal(raw, []byte(strings.TrimSpace(suffix))) == nil {
				t.Fatal("accepted unsafe shutdown")
			}
		})
	}
	if merchantStoreOrdinaryShutdownJournal(actual, append(saved, '!')) == nil {
		t.Fatal("accepted changed saved invocation")
	}
}

func TestMerchantStoreOrdinaryParentRequiresCompleteSamePlanTuple(t *testing.T) {
	c := testMerchantCapsule(t, defaultProductionPaths())
	owner := productionMerchantStoreFenceOwner{Format: 1, State: "ACTIVE", DeploymentID: c.DeploymentID, Host: c.Host, PlanSHA256: c.ControllerPlanSHA256, ContractSHA256: merchantStoreFenceContractSHA(c.Writer), ProviderSHA256: c.Writer.Candidate.PayloadSHA256,
		Nonce: strings.Repeat("d", 32), HolderPID: 123, HolderUnit: merchantStoreFenceUnit(c.DeploymentID), HolderInvocationID: strings.Repeat("e", 32), BackendPID: 321,
		SystemIdentifier: c.Writer.SystemIdentifier, Database: c.Writer.Database, DatabaseOID: c.Writer.DatabaseOID, Schema: c.Writer.Schema, SchemaOID: c.Writer.SchemaOID, Role: c.Writer.Role}
	if !merchantStoreOrdinaryOwnerMatches(owner, c, c.ControllerPlanSHA256, "") {
		t.Fatal("exact Arch tuple rejected")
	}
	for name, change := range map[string]func(*productionMerchantStoreFenceOwner){
		"plan":     func(o *productionMerchantStoreFenceOwner) { o.PlanSHA256 = strings.Repeat("f", 64) },
		"host":     func(o *productionMerchantStoreFenceOwner) { o.Host = "other" },
		"contract": func(o *productionMerchantStoreFenceOwner) { o.ContractSHA256 = strings.Repeat("f", 64) },
		"provider": func(o *productionMerchantStoreFenceOwner) { o.ProviderSHA256 = strings.Repeat("f", 64) },
		"physical": func(o *productionMerchantStoreFenceOwner) { o.DatabaseOID++ },
		"role":     func(o *productionMerchantStoreFenceOwner) { o.Role = "other" },
		"purpose": func(o *productionMerchantStoreFenceOwner) {
			o.Purpose = "portable-deploy"
			o.HolderUnit = merchantStorePortableUnit(c.DeploymentID)
			o.Service = c.Service
		},
	} {
		t.Run(name, func(t *testing.T) {
			changed := owner
			change(&changed)
			if merchantStoreOrdinaryOwnerMatches(changed, c, c.ControllerPlanSHA256, "") {
				t.Fatal("accepted foreign parent")
			}
		})
	}
	manifest := productionManifest{DeploymentID: c.DeploymentID, SchemaPlanSHA256: c.ControllerPlanSHA256, SchemaMode: c.SchemaMode, Go: productionPackageTransition{Changed: true}, MerchantStoreWriter: c.Writer, ExistingSchemaContract: c.ExistingSchemaContract, DeployEngineSHA256: c.Candidate.DeployEngineSHA256}
	if !merchantStoreOrdinaryManifestMatches(manifest, c) {
		t.Fatal("exact ordinary manifest rejected")
	}
	manifest.MaintenanceCapture = &productionMaintenanceCapture{}
	if merchantStoreOrdinaryManifestMatches(manifest, c) {
		t.Fatal("accepted schema-maintenance parent")
	}
}
