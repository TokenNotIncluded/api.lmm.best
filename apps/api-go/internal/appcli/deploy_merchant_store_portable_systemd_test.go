//go:build linux

package appcli

import (
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

// This child proves the real ExecStartPre ControlPID and InvocationID only.
// It does not claim a synthetic test ELF is an official production provider.
func TestProductionMerchantStorePortableSystemdControlChild(t *testing.T) {
	unit, path := os.Getenv("LMM_NATIVE_OWNED_UNIT"), os.Getenv("LMM_NATIVE_OWNED_RESULT")
	if unit == "" {
		t.Skip("owned real-systemd child only")
	}
	if !strings.HasPrefix(unit, "lmm-native-owned-test-") || path == "" {
		t.Fatal("invalid own unit fixture")
	}
	runtime := defaultProductionRuntime()
	runtime.paths.Service = unit
	c := productionMerchantStoreCapsule{Service: unit}
	invocation := os.Getenv("INVOCATION_ID")
	if runtime.portableStartControlPID(context.Background(), c, strings.Repeat("f", 32)) == nil {
		t.Fatal("wrong service generation accepted")
	}
	if err := runtime.portableStartControlPID(context.Background(), c, invocation); err != nil {
		t.Fatal(err)
	}
	digest, err := sha256File("/proc/self/exe")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"pid": os.Getpid(), "invocation_id": invocation, "actual_elf_sha256": digest, "actual_control_pid_proved": true})
	if os.WriteFile(path, body, 0600) != nil {
		t.Fatal("cannot write own proof")
	}
}
func TestProductionMerchantStorePortableActualSystemdStartGenerations(t *testing.T) {
	if os.Getenv("LMM_TEST_NATIVE_SYSTEMD") != "1" {
		t.Skip("set LMM_TEST_NATIVE_SYSTEMD=1 for own local units")
	}
	pid1, err := os.ReadFile("/proc/1/comm")
	if err != nil || strings.TrimSpace(string(pid1)) != "systemd" {
		t.Skip("actual systemd PID1 unavailable")
	}
	if exec.Command("sudo", "-n", "true").Run() != nil {
		t.Skip("own temporary root units require noninteractive sudo")
	}
	root := t.TempDir()
	unit := fmt.Sprintf("lmm-native-owned-test-%d-%d.service", os.Getpid(), time.Now().UnixNano())
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	expected, err := sha256File(binary)
	if err != nil {
		t.Fatal(err)
	}
	env := filepath.Join(root, "owned.env")
	if os.WriteFile(env, []byte("LMM_DB_MIGRATION_MODE=verify\n"), 0600) != nil {
		t.Fatal("env")
	}
	var previous string
	for attempt := 0; attempt < 2; attempt++ {
		output := filepath.Join(root, fmt.Sprintf("result-%d.json", attempt))
		args := []string{"-n", "/usr/bin/systemd-run", "--quiet", "--wait", "--collect", "--pipe", "--unit", unit, "--property=Type=oneshot", "--property=User=root", "--property=EnvironmentFile=" + env,
			"--property=ExecStartPre=" + binary + " -test.run=^TestProductionMerchantStorePortableSystemdControlChild$ -test.v",
			"--setenv=LMM_NATIVE_OWNED_UNIT=" + unit, "--setenv=LMM_NATIVE_OWNED_RESULT=" + output, "--", "/usr/bin/true"}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		body, err := exec.CommandContext(ctx, "sudo", args...).CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("own actual systemd unit failed: %v %s", err, body)
		}
		proof, err := exec.Command("sudo", "-n", "/usr/bin/cat", "--", output).Output()
		if err != nil {
			t.Fatal(err)
		}
		var result struct {
			PID        int    `json:"pid"`
			Invocation string `json:"invocation_id"`
			ELF        string `json:"actual_elf_sha256"`
			Proved     bool   `json:"actual_control_pid_proved"`
		}
		if json.Unmarshal(proof, &result) != nil || result.PID <= 1 || !existingSchemaInvocationPattern.MatchString(result.Invocation) || result.Invocation == previous || result.ELF != expected || !result.Proved {
			t.Fatalf("invalid actual own generation: %s", proof)
		}
		t.Logf("attempt=%d real_control_pid=%d real_invocation=%s real_elf=%s", attempt, result.PID, result.Invocation, result.ELF)
		previous = result.Invocation
	}
}
