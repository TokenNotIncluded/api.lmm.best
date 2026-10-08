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

// The test ELF proves only the real systemd privilege boundary. It never
// qualifies itself as an official provider or bypasses artifact verification.
func TestProductionMerchantStoreDynamicUserPreChild(t *testing.T) {
	unit := os.Getenv("LMM_DYNAMIC_OWNED_UNIT")
	if unit == "" {
		t.Skip("owned actual systemd child only")
	}
	if !strings.HasPrefix(unit, "lmm-native-dynamic-owned-") || os.Geteuid() != 0 {
		t.Fatal("typed pre-hook did not run as root")
	}
	private := os.Getenv("LMM_DYNAMIC_OWNED_PRIVATE")
	body, err := os.ReadFile(filepath.Join(private, "capsule.fixture"))
	if err != nil || string(body) != "owned root-private capsule fixture\n" {
		t.Fatal("root-private capsule unavailable")
	}
	runtime := defaultProductionRuntime()
	runtime.paths.Service = unit
	if err := runtime.portableStartControlPID(context.Background(), productionMerchantStoreCapsule{Service: unit}, os.Getenv("INVOCATION_ID")); err != nil {
		t.Fatal(err)
	}
	proof, _ := json.Marshal(map[string]any{"pid": os.Getpid(), "uid": os.Geteuid(), "invocation_id": os.Getenv("INVOCATION_ID"), "root_private_read": true, "root_private_write": true})
	if err := os.WriteFile(filepath.Join(private, "pre.json"), proof, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestProductionMerchantStoreDynamicUserMainChild(t *testing.T) {
	if os.Getenv("LMM_DYNAMIC_OWNED_UNIT") == "" {
		t.Skip("owned actual systemd main only")
	}
	if os.Geteuid() == 0 {
		t.Fatal("serve boundary became root")
	}
	if _, err := os.ReadFile(filepath.Join(os.Getenv("LMM_DYNAMIC_OWNED_PRIVATE"), "capsule.fixture")); err == nil {
		t.Fatal("serve could read root-private capsule")
	}
	proof, _ := json.Marshal(map[string]any{"pid": os.Getpid(), "uid": os.Geteuid(), "invocation_id": os.Getenv("INVOCATION_ID"), "root_private_refused": true})
	if err := os.WriteFile(filepath.Join(os.Getenv("STATE_DIRECTORY"), "main.json"), proof, 0600); err != nil {
		t.Fatal(err)
	}
}

// Use the actual signed unit's hardening bytes and an owned persistent '+'
// drop-in. Only the executable/action and isolated state paths are fixtures.
// Official package->holder->readiness->CAS remains a separate acceptance gate.
func TestProductionMerchantStoreDynamicUserActualPrivilegeBoundary(t *testing.T) {
	if os.Getenv("LMM_TEST_NATIVE_SYSTEMD") != "1" {
		t.Skip("set LMM_TEST_NATIVE_SYSTEMD=1 for owned local units")
	}
	pid1, err := os.ReadFile("/proc/1/comm")
	if err != nil || strings.TrimSpace(string(pid1)) != "systemd" {
		t.Skip("actual systemd unavailable")
	}
	if exec.Command("sudo", "-n", "true").Run() != nil {
		t.Skip("owned unit requires sudo")
	}
	run := func(args ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return exec.CommandContext(ctx, "sudo", append([]string{"-n"}, args...)...).CombinedOutput()
	}
	must := func(args ...string) []byte {
		t.Helper()
		b, e := run(args...)
		if e != nil {
			t.Fatalf("owned %s: %v %s", args[0], e, b)
		}
		return b
	}
	id := fmt.Sprintf("lmm-native-dynamic-owned-%d-%d", os.Getpid(), time.Now().UnixNano())
	// /run may be mounted noexec (including the Ubuntu qualification guest).
	// Keep private data there, but execute the owned test ELF from /opt.
	unit, private, public := id+".service", "/run/"+id+"-private", "/opt/"+id+"-public"
	base, dropDir := "/usr/lib/systemd/system/"+unit, "/etc/systemd/system/"+unit+".d"
	drop := dropDir + "/90-merchant-startup-baseline.conf"
	state := "/var/lib/private/" + id
	cleanup := func() {
		run("/usr/bin/systemctl", "stop", unit)
		run("/usr/bin/rm", "-f", drop, base)
		run("/usr/bin/rmdir", dropDir)
		run("/usr/bin/systemctl", "daemon-reload")
		run("/usr/bin/systemctl", "reset-failed", unit)
		for _, path := range []string{private, public, state} {
			run("/usr/bin/rm", "-rf", "--", path)
		}
		run("/usr/bin/rm", "-f", "--", "/var/lib/"+id)
	}
	t.Cleanup(cleanup)
	must("/usr/bin/install", "-d", "-m", "0700", private)
	must("/usr/bin/install", "-d", "-m", "0755", public, dropDir)
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	guestBinary := public + "/owned-test"
	must("/usr/bin/install", "-m", "0755", binary, guestBinary)
	local := t.TempDir()
	fixture := filepath.Join(local, "capsule.fixture")
	if err := os.WriteFile(fixture, []byte("owned root-private capsule fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	must("/usr/bin/install", "-m", "0600", fixture, private+"/capsule.fixture")
	signed, err := os.ReadFile("../../../../packaging/common/lmm-api/lmm-api.service")
	if err != nil {
		t.Fatal(err)
	}
	profile := string(signed)
	profile = strings.Replace(profile, "Type=simple", "Type=oneshot", 1)
	profile = strings.ReplaceAll(profile, "lmm-api-go", id)
	profile = strings.Replace(profile, "ExecStart=/usr/bin/lmm-api serve", "Environment=LMM_DYNAMIC_OWNED_UNIT="+unit+" LMM_DYNAMIC_OWNED_PRIVATE="+private+"\nExecStart="+guestBinary+" -test.run=^TestProductionMerchantStoreDynamicUserMainChild$ -test.v", 1)
	profile = strings.Replace(profile, "Restart=on-failure", "Restart=no", 1)
	localBase := filepath.Join(local, "unit")
	if err := os.WriteFile(localBase, []byte(profile), 0600); err != nil {
		t.Fatal(err)
	}
	must("/usr/bin/install", "-m", "0644", localBase, base)
	writeHook := func(plus string) {
		hook := "[Service]\nExecStartPre=\nExecStartPre=" + plus + guestBinary + " -test.run=^TestProductionMerchantStoreDynamicUserPreChild$ -test.v\n"
		p := filepath.Join(local, "drop")
		if err := os.WriteFile(p, []byte(hook), 0600); err != nil {
			t.Fatal(err)
		}
		must("/usr/bin/install", "-m", "0644", p, drop)
		must("/usr/bin/systemctl", "daemon-reload")
	}
	writeHook("")
	if _, err := run("/usr/bin/systemctl", "start", unit); err == nil {
		t.Fatal("unprivileged pre-hook unexpectedly read root-private capsule")
	}
	if _, err := run("/usr/bin/cat", private+"/pre.json"); err == nil {
		t.Fatal("unprivileged pre-hook wrote root proof")
	}
	must("/usr/bin/systemctl", "reset-failed", unit)
	writeHook("+")
	for attempt := 0; attempt < 2; attempt++ {
		must("/usr/bin/systemctl", "start", unit)
		pre, main := must("/usr/bin/cat", private+"/pre.json"), must("/usr/bin/cat", state+"/main.json")
		var p, m struct {
			PID        int    `json:"pid"`
			UID        int    `json:"uid"`
			Invocation string `json:"invocation_id"`
		}
		if json.Unmarshal(pre, &p) != nil || json.Unmarshal(main, &m) != nil || p.UID != 0 || m.UID == 0 || p.PID <= 1 || m.PID <= 1 || p.PID == m.PID || p.Invocation != m.Invocation || !existingSchemaInvocationPattern.MatchString(p.Invocation) {
			t.Fatalf("invalid root/dynamic proofs %s %s", pre, main)
		}
		properties := must("/usr/bin/systemctl", "show", unit, "--all", "--property=ExecStartPreEx,DynamicUser,CapabilityBoundingSet,ProtectSystem,ProtectHome,PrivateTmp,DropInPaths")
		if !strings.Contains(string(properties), "flags=privileged ;") || !strings.Contains(string(properties), "DynamicUser=yes") || !strings.Contains(string(properties), "ProtectSystem=strict") || !strings.Contains(string(properties), drop) {
			t.Fatal("actual hardened privileged hook mismatch", string(properties))
		}
		t.Logf("attempt=%d root_pre_pid=%d dynamic_main_pid=%d dynamic_uid=%d invocation=%s actual_properties=%s", attempt, p.PID, m.PID, m.UID, p.Invocation, strings.TrimSpace(string(properties)))
	}
	cleanup()
}
