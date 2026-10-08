//go:build linux

package appcli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProductionMerchantStartupPendingSystemdChild(t *testing.T) {
	unit := os.Getenv("LMM_BASELINE_OWNED_UNIT")
	if unit == "" {
		t.Skip("owned actual systemd child only")
	}
	if !strings.HasPrefix(unit, "lmm-baseline-owned-test-") {
		t.Fatal("invalid own unit")
	}
	r := defaultProductionRuntime()
	r.paths.Service = unit
	inv := os.Getenv("INVOCATION_ID")
	if err := r.portableStartControlPID(context.Background(), productionMerchantStoreCapsule{Service: unit}, inv); err != nil {
		t.Fatal(err)
	}
	// A pending hook runs the real native parser with no capsule present. It
	// MUST fail before ExecStart. This test ELF is not an official provider.
	var stderr bytes.Buffer
	code := runProductionMerchantStorePortableStart([]string{"--capsule", "/var/lib/lmm-api-go-deploy/merchant-capsules/baseline-owned-missing/capsule.json", "--capsule-sha256", strings.Repeat("0", 64)}, &bytes.Buffer{}, &stderr)
	if code != ExitError {
		t.Fatal("pending capsule unexpectedly allowed startup")
	}
	elf, err := sha256File("/proc/self/exe")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"pid": os.Getpid(), "invocation_id": inv, "actual_elf_sha256": elf, "control_pid_proved": true, "pending_capsule_refused": true})
	if err := os.WriteFile(os.Getenv("LMM_BASELINE_OWNED_RESULT"), body, 0600); err != nil {
		t.Fatal(err)
	}
	os.Exit(41)
}

// Actual local systemd proves persistent mask -> loaded pending-hook handoff,
// real ControlPID/invocations, and retry fail-closed for both ordered-file unit
// shapes. This is NOT official package->holder->MainPID/readiness/CAS acceptance;
// that needs the future official artifact and fresh-owned production copy.
func TestProductionMerchantStartupPendingActualSystemd(t *testing.T) {
	if os.Getenv("LMM_TEST_NATIVE_SYSTEMD") != "1" {
		t.Skip("set LMM_TEST_NATIVE_SYSTEMD=1 for owned local units")
	}
	pid1, err := os.ReadFile("/proc/1/comm")
	if err != nil || strings.TrimSpace(string(pid1)) != "systemd" {
		t.Skip("actual systemd PID1 unavailable")
	}
	if exec.Command("sudo", "-n", "true").Run() != nil {
		t.Skip("owned temporary units require sudo")
	}
	root := t.TempDir()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	expected, err := sha256File(binary)
	if err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) ([]byte, error) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return exec.CommandContext(ctx, "sudo", append([]string{"-n"}, args...)...).CombinedOutput()
	}
	must := func(args ...string) []byte {
		t.Helper()
		raw, err := run(args...)
		if err != nil {
			t.Fatalf("owned command %s: %v %s", args[0], err, raw)
		}
		return raw
	}
	for _, shape := range []string{"arch", "ubuntu"} {
		unit := fmt.Sprintf("lmm-baseline-owned-test-%s-%d-%d.service", shape, os.Getpid(), time.Now().UnixNano())
		base := "/usr/lib/systemd/system/" + unit
		dropDir := "/etc/systemd/system/" + unit + ".d"
		drop := dropDir + "/90-baseline-pending.conf"
		result := filepath.Join(root, shape+"-result.json")
		started := filepath.Join(root, shape+"-started")
		mainEnv := filepath.Join(root, shape+".env")
		if os.WriteFile(mainEnv, []byte("LMM_DB_MIGRATION_MODE=verify\n"), 0600) != nil {
			t.Fatal("env")
		}
		profile := "[Unit]\nDescription=Owned native pending baseline test\n[Service]\nType=oneshot\nEnvironmentFile=" + mainEnv + "\nEnvironment=LMM_BASELINE_OWNED_UNIT=" + unit + " LMM_BASELINE_OWNED_RESULT=" + result + "\nExecStart=/usr/bin/touch " + started + "\n"
		if shape == "ubuntu" {
			extra := filepath.Join(root, "ubuntu-extra.env")
			if os.WriteFile(extra, []byte("OWNED_BASELINE_PROFILE=ubuntu\n"), 0600) != nil {
				t.Fatal("extra")
			}
			profile = strings.Replace(profile, "ExecStart=", "EnvironmentFile="+extra+"\nExecStart=", 1)
		}
		localBase := filepath.Join(root, shape+"-unit")
		if os.WriteFile(localBase, []byte(profile), 0600) != nil {
			t.Fatal("unit")
		}
		must("/usr/bin/install", "-m", "0644", localBase, base)
		cleanup := func() {
			run("/usr/bin/systemctl", "stop", unit)
			run("/usr/bin/systemctl", "unmask", unit)
			run("/usr/bin/rm", "-f", drop, base)
			run("/usr/bin/rmdir", dropDir)
			run("/usr/bin/systemctl", "daemon-reload")
			run("/usr/bin/systemctl", "reset-failed", unit)
		}
		t.Cleanup(cleanup)
		must("/usr/bin/systemctl", "daemon-reload")
		must("/usr/bin/systemctl", "mask", unit)
		if _, err := run("/usr/bin/systemctl", "start", unit); err == nil {
			t.Fatal("persistent masked unit started")
		}
		if !strings.Contains(string(must("/usr/bin/systemctl", "show", unit, "--property=LoadState", "--value")), "masked") {
			t.Fatal("mask not loaded")
		}
		must("/usr/bin/mkdir", "-p", dropDir)
		localDrop := filepath.Join(root, shape+"-dropin")
		hook := "[Service]\nExecStartPre=\nExecStartPre=" + binary + " -test.run=^TestProductionMerchantStartupPendingSystemdChild$ -test.v\n"
		if os.WriteFile(localDrop, []byte(hook), 0600) != nil {
			t.Fatal("hook")
		}
		must("/usr/bin/install", "-m", "0644", localDrop, drop)
		must("/usr/bin/systemctl", "unmask", unit)
		must("/usr/bin/systemctl", "daemon-reload")
		if !strings.Contains(string(must("/usr/bin/systemctl", "show", unit, "--all", "--property=DropInPaths", "--value")), drop) {
			t.Fatal("persistent pending hook not actually loaded")
		}
		var previous string
		for attempt := 0; attempt < 2; attempt++ {
			if _, err := run("/usr/bin/systemctl", "start", unit); err == nil {
				t.Fatal("pending hook let main process start")
			}
			proof := must("/usr/bin/cat", result)
			var actual struct {
				PID        int    `json:"pid"`
				Invocation string `json:"invocation_id"`
				ELF        string `json:"actual_elf_sha256"`
				Control    bool   `json:"control_pid_proved"`
				Refused    bool   `json:"pending_capsule_refused"`
			}
			if json.Unmarshal(proof, &actual) != nil || actual.PID <= 1 || !existingSchemaInvocationPattern.MatchString(actual.Invocation) || actual.Invocation == previous || actual.ELF != expected || !actual.Control || !actual.Refused {
				t.Fatalf("invalid actual owned proof=%s", proof)
			}
			if _, err := os.Stat(started); !os.IsNotExist(err) {
				t.Fatal("ExecStart ran past a pending hook")
			}
			main := strings.TrimSpace(string(must("/usr/bin/systemctl", "show", unit, "--property=MainPID", "--value")))
			if main != "0" {
				t.Fatal("pending failure has active MainPID")
			}
			t.Logf("profile=%s attempt=%d real_control_pid=%d invocation=%s pending_refused=true ExecStart_never_ran=true", shape, attempt, actual.PID, actual.Invocation)
			previous = actual.Invocation
			must("/usr/bin/systemctl", "reset-failed", unit)
		}
		cleanup()
	}
}
