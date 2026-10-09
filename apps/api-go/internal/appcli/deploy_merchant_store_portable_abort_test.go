package appcli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

type portableAbortFixture struct {
	runtime  *productionRuntime
	capsule  productionMerchantStoreCapsule
	work     string
	original []byte
	options  merchantStorePortableAbortOptions
}

// Only local files and flock operations are real. No official artifact,
// PostgreSQL session, or installed systemd service is asserted by this fixture.
func newPortableAbortFixture(t *testing.T) portableAbortFixture {
	t.Helper()
	root := t.TempDir()
	r := &productionRuntime{requiredOwnerUID: uint32(os.Getuid()), now: func() time.Time { return time.Unix(100000, 0) }, portableWorkRoot: filepath.Join(root, "systemd"), paths: productionPaths{GlobalLock: filepath.Join(root, "native.lock"), TransactionLock: filepath.Join(root, "transaction.lock"), FrontendRoot: filepath.Join(root, "web")}}
	c := productionMerchantStoreCapsule{Format: 1, DeploymentID: "abort-test", Host: "dmit-ubuntu", Service: "lmm-api.service", ControllerPlanSHA256: strings.Repeat("a", 64), Candidate: productionReleasePackagePlan{Version: "0.2.101-1"}, Rollback: productionReleasePackagePlan{Version: "0.2.98-1"}, Writer: testMerchantWriterContract()}
	work := filepath.Join(r.portableTransactionRoot(), c.DeploymentID)
	for _, path := range []string{work, filepath.Join(work, "frontend"), r.paths.FrontendRoot} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{r.paths.GlobalLock, filepath.Join(r.portableTransactionRoot(), "lock"), filepath.Join(r.paths.FrontendRoot, ".release.lock")} {
		if err := os.WriteFile(path, nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	provider := []byte("fixture candidate bytes; not an official ELF")
	c.Writer.Candidate.PayloadSHA256 = startupContentSHA256(provider)
	if err := os.WriteFile(filepath.Join(work, backendGoName), provider, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(backendGoName, filepath.Join(work, "lmm-api")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "frontend", "index.html"), []byte("fixture frontend"), 0644); err != nil {
		t.Fatal(err)
	}
	// Golden produced independently by deploy-systemd.py's tree_digest:
	// SHA256(relative + NUL + lowercase content SHA256 + NUL).
	const tree = "dcb4224032577d96c3a693e98b957653dca8c9602ecd3cdc65ceb4dff3ba77db"
	if actual, err := r.portableAbortTree(filepath.Join(work, "frontend")); err != nil || actual != tree {
		t.Fatalf("standalone Python tree golden differs: %s %v", actual, err)
	}
	original, err := merchantStartupJSON(map[string]any{"release": c.DeploymentID, "version": "0.2.101", "sha256": c.Writer.Candidate.PayloadSHA256, "frontend_sha256": tree, "migrate": false, "backup_exclude_tables": []string{}, "phase": "STAGED"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "state.json"), original, 0600); err != nil {
		t.Fatal(err)
	}
	return portableAbortFixture{r, c, work, original, merchantStorePortableAbortOptions{StateSHA256: startupContentSHA256(original), OwnerSHA256: strings.Repeat("b", 64), OldPID: 123, OldInvocation: strings.Repeat("c", 32), OldBootID: "12345678-1234-1234-1234-123456789abc"}}
}

func TestProductionMerchantStorePortableAbortRejectsAnyMutationBoundary(t *testing.T) {
	for _, marker := range []string{"previous-binary", "previous.env", "stop.log", "start.log", "verify-apply.log", "state.next", "logs", "unknown"} {
		t.Run(marker, func(t *testing.T) {
			f := newPortableAbortFixture(t)
			if err := os.WriteFile(filepath.Join(f.work, marker), []byte("must be preserved"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, _, err := f.runtime.portableAbortStaged(f.work, f.capsule, f.options.StateSHA256); err == nil {
				t.Fatal("mutation or unknown evidence accepted")
			}
			current, _ := os.ReadFile(filepath.Join(f.work, "state.json"))
			if !bytesEqual(current, f.original) {
				t.Fatal("rejected original STAGED was changed")
			}
		})
	}
	for _, change := range []string{"phase", "previous_sha256", "migrate", "backup_exclude_tables", "duplicate", "null-excludes", "version"} {
		t.Run(change, func(t *testing.T) {
			f := newPortableAbortFixture(t)
			var state map[string]any
			_ = json.Unmarshal(f.original, &state)
			switch change {
			case "phase":
				state["phase"] = "MUTATION_PENDING"
			case "previous_sha256":
				state["previous_sha256"] = f.capsule.Writer.Rollback.PayloadSHA256
			case "migrate":
				state["migrate"] = true
			case "backup_exclude_tables":
				state["backup_exclude_tables"] = []string{"public.options"}
			case "null-excludes":
				state["backup_exclude_tables"] = nil
			case "version":
				state["version"] = "0.2.99"
			}
			raw, _ := merchantStartupJSON(state)
			if change == "duplicate" {
				raw = []byte(strings.Replace(string(raw), `"phase": "STAGED"`, `"phase": "STAGED", "phase": "STAGED"`, 1))
			}
			if err := os.WriteFile(filepath.Join(f.work, "state.json"), raw, 0600); err != nil {
				t.Fatal(err)
			}
			if _, _, err := f.runtime.portableAbortStaged(f.work, f.capsule, startupContentSHA256(raw)); err == nil {
				t.Fatal("invalid freshly pinned STAGED accepted")
			}
		})
	}
}

func TestProductionMerchantStorePortableAbortUsesAllOriginalOrdinaryLocks(t *testing.T) {
	for _, lockIndex := range []int{0, 1, 2} {
		t.Run(strconv.Itoa(lockIndex), func(t *testing.T) {
			f := newPortableAbortFixture(t)
			paths := []string{f.runtime.paths.GlobalLock, filepath.Join(f.runtime.portableTransactionRoot(), "lock"), filepath.Join(f.runtime.paths.FrontendRoot, ".release.lock")}
			file, err := os.OpenFile(paths[lockIndex], os.O_RDWR, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			if locked, err := tryDeploymentFileLock(file); err != nil || !locked {
				t.Fatal(err)
			}
			if _, err := f.runtime.portableAbortLocks(); err == nil {
				t.Fatal("live ordinary or guardian lock was borrowed")
			}
			current, _ := os.ReadFile(filepath.Join(f.work, "state.json"))
			if !bytesEqual(current, f.original) {
				t.Fatal("busy owner state changed")
			}
		})
	}
	f := newPortableAbortFixture(t)
	if err := os.WriteFile(f.runtime.paths.TransactionLock, []byte("another native owner"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.runtime.portableAbortLocks(); err == nil {
		t.Fatal("native transaction lease ignored")
	}
}

func (f portableAbortFixture) commit(t *testing.T, guard func() error) (string, error) {
	t.Helper()
	raw, state, err := f.runtime.portableAbortStaged(f.work, f.capsule, f.options.StateSHA256)
	if err != nil {
		return "", err
	}
	ownerRaw := []byte("private exact owner fixture; not a live session")
	return f.runtime.commitPortablePreApplyAbort(context.Background(), f.work, f.capsule, strings.Repeat("d", 64), f.options, raw, state, ownerRaw, productionMerchantStoreFenceOwner{HolderPID: 456, HolderInvocationID: strings.Repeat("e", 32)}, "releases/web137", strings.Repeat("f", 64), strings.Repeat("1", 64), guard)
}

func TestProductionMerchantStorePortableAbortSealsOriginalEvidenceBeforeTerminal(t *testing.T) {
	f := newPortableAbortFixture(t)
	guardCalls := 0
	receiptSHA, err := f.commit(t, func() error { guardCalls++; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if guardCalls != 1 {
		t.Fatal("terminal transition skipped final physical guard")
	}
	current, err := f.runtime.readExistingSchemaSealedFile(filepath.Join(f.work, "state.json"), true)
	if err != nil {
		t.Fatal(err)
	}
	var state map[string]json.RawMessage
	_ = json.Unmarshal(current, &state)
	var original map[string]json.RawMessage
	_ = json.Unmarshal(f.original, &original)
	for key, value := range original {
		if key != "phase" && !bytesEqual(state[key], value) {
			t.Fatalf("candidate staging field %s changed", key)
		}
	}
	if string(state["phase"]) != `"ROLLED_BACK"` || string(state["previous_sha256"]) != strconv.Quote(f.capsule.Writer.Rollback.PayloadSHA256) {
		t.Fatal("terminal is not compatible with original holder rollback checks")
	}
	var binding map[string]string
	_ = json.Unmarshal(state["pre_apply_abort"], &binding)
	receipt, err := f.runtime.readExistingSchemaSealedFile(binding["receipt_path"], true)
	if err != nil || binding["receipt_sha256"] != receiptSHA || startupContentSHA256(receipt) != receiptSHA {
		t.Fatal("terminal abort proof is not exact private bytes")
	}
	for _, name := range []string{"previous-binary", "stop.log", "start.log", "rollback-start.log"} {
		if _, err := os.Lstat(filepath.Join(f.work, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("abort pretended to stop, install, or restart a provider")
		}
	}
	backup, err := f.runtime.readExistingSchemaSealedFile(filepath.Join(f.work, "pre-apply-abort.original-state.json"), true)
	if err != nil || !bytesEqual(backup, f.original) {
		t.Fatal("original staging evidence lost")
	}
	// Replay fails before overwriting any terminal proof.
	if _, err := f.commit(t, func() error { return nil }); err == nil {
		t.Fatal("terminal abort replay accepted")
	}
	newCurrent, _ := os.ReadFile(filepath.Join(f.work, "state.json"))
	if !bytesEqual(current, newCurrent) {
		t.Fatal("replay rewrote terminal evidence")
	}
}

func TestProductionMerchantStorePortableAbortLateGuardFailureRetainsStaged(t *testing.T) {
	f := newPortableAbortFixture(t)
	if _, err := f.commit(t, func() error { return errors.New("original physical owner changed") }); err == nil {
		t.Fatal("changed physical owner became terminal")
	}
	current, _ := os.ReadFile(filepath.Join(f.work, "state.json"))
	if !bytesEqual(current, f.original) {
		t.Fatal("failed final guard mutated STAGED")
	}
	if _, err := os.Stat(filepath.Join(f.work, "pre-apply-abort.receipt.json")); err != nil {
		t.Fatal("partial evidence removed instead of retained")
	}
	if _, err := f.commit(t, func() error { return nil }); err == nil {
		t.Fatal("partial evidence was silently adopted on retry")
	}
}

func TestProductionMerchantStorePortableAbortFinalGuardCannotOverwriteMutation(t *testing.T) {
	for _, mutation := range []string{"state", "marker", "receipt"} {
		t.Run(mutation, func(t *testing.T) {
			f := newPortableAbortFixture(t)
			expected := f.original
			_, err := f.commit(t, func() error {
				switch mutation {
				case "state":
					expected = []byte(strings.Replace(string(f.original), `"STAGED"`, `"MUTATION_PENDING"`, 1))
					return os.WriteFile(filepath.Join(f.work, "state.json"), expected, 0600)
				case "marker":
					return os.WriteFile(filepath.Join(f.work, "stop.log"), []byte("unknown mutation must not be hidden"), 0600)
				case "receipt":
					return os.WriteFile(filepath.Join(f.work, "pre-apply-abort.receipt.json"), []byte("changed proof"), 0600)
				}
				return nil
			})
			if err == nil {
				t.Fatal("mutation during final physical guard became terminal")
			}
			actual, _ := os.ReadFile(filepath.Join(f.work, "state.json"))
			if !bytesEqual(actual, expected) {
				t.Fatal("unknown mutation was overwritten by ROLLED_BACK")
			}
		})
	}
}

func TestProductionMerchantStorePortableAbortRejectsStartupBaseline(t *testing.T) {
	f := newPortableAbortFixture(t)
	f.capsule.Format = 2
	if _, err := f.runtime.abortMerchantStorePortablePreApply(context.Background(), f.capsule, strings.Repeat("d", 64), f.options); err == nil {
		t.Fatal("startup baseline became ordinary abort authority")
	}
}

func TestProductionMerchantStorePortableAbortMatchesPythonPathOrdering(t *testing.T) {
	f := newPortableAbortFixture(t)
	frontend := filepath.Join(f.work, "frontend")
	if err := os.Mkdir(filepath.Join(frontend, "a"), 0700); err != nil {
		t.Fatal(err)
	}
	for name, raw := range map[string]string{"a/x": "directory first", "a.z": "sibling file"} {
		if err := os.WriteFile(filepath.Join(frontend, name), []byte(raw), 0644); err != nil {
			t.Fatal(err)
		}
	}
	// Independently generated with sorted(pathlib.Path(...)) in Python.
	const expected = "1e5ff55263542ba9d7b1ac14c3d2d5e234d00306644f53edd87c05937d18ee74"
	actual, err := f.runtime.portableAbortTree(frontend)
	if err != nil || actual != expected {
		t.Fatalf("standalone Python component order differs: %s %v", actual, err)
	}
}

type portableAbortGenerationRunner struct {
	pid        int
	invocation string
	restarts   string
}

func (r portableAbortGenerationRunner) Run(_ context.Context, command productionCommand) ([]byte, error) {
	if command.Name != commandSystemctl || len(command.Args) < 3 || command.Args[0] != "show" || command.Args[1] != "lmm-api.service" {
		return nil, errors.New("no generation mutation is implemented")
	}
	if len(command.Args) == 4 && command.Args[2] == "--property=NRestarts" && command.Args[3] == "--value" {
		return []byte(r.restarts), nil
	}
	if len(command.Args) != 3 || command.Args[2] != "--property=MainPID,ExecMainPID,ExecMainCode,ExecMainStatus,ActiveState,SubState,Result,ControlGroup,Restart,InvocationID" {
		return nil, errors.New("generation fixture requires the actual requested properties")
	}
	// Do not manufacture an unrequested NRestarts field.
	return []byte(fmt.Sprintf("MainPID=%d\nExecMainPID=%d\nExecMainCode=0\nExecMainStatus=0\nActiveState=active\nSubState=running\nResult=success\nControlGroup=/fixture\nRestart=on-failure\nInvocationID=%s\n", r.pid, r.pid, r.invocation)), nil
}

func TestProductionMerchantStorePortableAbortRequiresOriginalRunningGeneration(t *testing.T) {
	f := newPortableAbortFixture(t)
	directory := filepath.Join(t.TempDir(), "bin")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	elf, err := os.ReadFile("/proc/self/exe")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, backendGoName), elf, 0700); err != nil {
		t.Fatal(err)
	}
	f.capsule.Binary = filepath.Join(directory, "lmm-api")
	if err := os.Symlink(backendGoName, f.capsule.Binary); err != nil {
		t.Fatal(err)
	}
	f.capsule.Writer.Rollback.PayloadSHA256 = startupContentSHA256(elf)
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		t.Fatal(err)
	}
	f.options.OldBootID = strings.TrimSpace(string(boot))
	f.options.OldPID = os.Getpid()
	f.runtime.runner = portableAbortGenerationRunner{os.Getpid(), f.options.OldInvocation, "0"}
	if err := f.runtime.portableAbortOldGeneration(context.Background(), f.capsule, f.options); err != nil {
		t.Fatal(err)
	}
	for _, change := range []string{"pid", "invocation", "boot", "elf", "restarts"} {
		t.Run(change, func(t *testing.T) {
			options := f.options
			c := f.capsule
			writer := *c.Writer
			c.Writer = &writer
			r := *f.runtime
			switch change {
			case "pid":
				options.OldPID++
			case "invocation":
				options.OldInvocation = strings.Repeat("9", 32)
			case "boot":
				options.OldBootID = "12345678-1234-1234-1234-123456789abc"
			case "elf":
				c.Writer.Rollback.PayloadSHA256 = strings.Repeat("9", 64)
			case "restarts":
				r.runner = portableAbortGenerationRunner{os.Getpid(), options.OldInvocation, "1"}
			}
			if err := r.portableAbortOldGeneration(context.Background(), c, options); err == nil {
				t.Fatal("changed original rollback generation accepted")
			}
		})
	}
	// The entrypoint must never create a replacement holder when its original
	// physical receipt is missing. This runner implements read-only show only.
	f.capsule.Root = filepath.Join(f.work, "no-original-holder")
	if _, err := f.runtime.abortMerchantStorePortablePreApply(context.Background(), f.capsule, strings.Repeat("d", 64), f.options); err == nil {
		t.Fatal("missing original owner was recreated or cleared")
	}
	state, _ := os.ReadFile(filepath.Join(f.work, "state.json"))
	if !bytesEqual(state, f.original) {
		t.Fatal("missing original holder changed STAGED")
	}
	if _, err := os.Lstat(filepath.Join(f.work, "pre-apply-abort.receipt.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("unverified original owner produced abort evidence")
	}
}
