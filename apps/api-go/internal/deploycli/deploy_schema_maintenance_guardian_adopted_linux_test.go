//go:build linux

package deploycli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func schemaGuardianAdoptedFixture(t *testing.T) (*schemaGuardianFixture, *productionRuntime, *productionSchemaMaintenanceHost, schemaMaintenanceRPCBinding, productionSchemaMaintenanceGuardianAdopted) {
	t.Helper()
	f := newSchemaGuardianFixture(t)
	f.binding.CapsulePath = filepath.Join(f.binding.Workspace, "old-capsule.json")
	f.binding.CapsuleSHA256 = strings.Repeat("a", 64)
	f.serve(t, f.files, false)
	lease, err := f.receive(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lease.Close() })
	runtime := &productionRuntime{requiredOwnerUID: uint32(os.Getuid()), effectiveUID: os.Geteuid}
	host := &productionSchemaMaintenanceHost{runtime: runtime, binding: f.binding, lease: lease, validate: func(context.Context) error { return nil }}
	generation := schemaMaintenanceRPCBinding{PlanSHA256: f.binding.PlanSHA256, TransitionID: f.binding.TransitionID, DaemonPID: 4321, DaemonInvocationID: strings.Repeat("b", 32), DaemonExeSHA256: strings.Repeat("c", 64), DaemonStartTicks: 1234}
	profile, err := runtime.schemaMaintenanceGuardianAdoptedSnapshot(host, generation, f.paths)
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]any{"daemon-binding.json": generation, "guardian-adopted.json": profile} {
		raw, err := schemaMaintenanceRPCMarshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := runtime.writeSchemaMaintenanceRawNewFile(filepath.Join(f.binding.Workspace, "state", name), append(raw, '\n')); err != nil {
			t.Fatal(err)
		}
	}
	return f, runtime, host, generation, profile
}

func schemaGuardianAssertReadOnlyRejection(t *testing.T, f *schemaGuardianFixture, runtime *productionRuntime, host *productionSchemaMaintenanceHost, generation schemaMaintenanceRPCBinding) {
	t.Helper()
	paths := []string{filepath.Join(f.binding.Workspace, "state", "guardian-adopted.json"), filepath.Join(f.binding.Workspace, "state", "daemon-binding.json")}
	before := make([][]byte, len(paths))
	infos := make([]os.FileInfo, len(paths))
	for index, path := range paths {
		before[index], _ = os.ReadFile(path)
		infos[index], _ = os.Lstat(path)
	}
	if err := runtime.checkSchemaMaintenanceGuardianReadoptedForPaths(host, generation, f.paths); err == nil {
		t.Fatal("unsafe guardian recovery was accepted")
	}
	for index, path := range paths {
		after, _ := os.ReadFile(path)
		info, _ := os.Lstat(path)
		if !bytes.Equal(before[index], after) || !schemaMaintenanceSameGuardianFile(infos[index], info) {
			t.Fatal("rejected guardian recovery changed original evidence")
		}
	}
	f.assertIndependentlyLocked(t)
}

func TestSchemaMaintenanceGuardianAdoptedExactOriginalProfile(t *testing.T) {
	f, runtime, host, generation, _ := schemaGuardianAdoptedFixture(t)
	if err := runtime.checkSchemaMaintenanceGuardianReadoptedForPaths(host, generation, f.paths); err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeSchemaMaintenanceGuardianAdoptedForPaths(host, generation, f.paths); err == nil {
		t.Fatal("another process could create the original daemon's adoption record")
	}
	if os.Geteuid() != 0 {
		if err := runtime.checkSchemaMaintenanceGuardianReadopted(host, generation); err == nil {
			t.Fatal("ordinary UID could use production guardian recovery authority")
		}
	}
	if err := runtime.checkSchemaMaintenanceGuardianReadoptedForPaths(nil, generation, f.paths); err == nil {
		t.Fatal("nil host was accepted")
	}
	f.assertIndependentlyLocked(t)
}

func TestSchemaMaintenanceGuardianAdoptedRejectsForgedProfile(t *testing.T) {
	cases := map[string]func(*productionSchemaMaintenanceGuardianAdopted){
		"format":           func(p *productionSchemaMaintenanceGuardianAdopted) { p.Format = "" },
		"host":             func(p *productionSchemaMaintenanceGuardianAdopted) { p.HostBinding.Host += "-other" },
		"old capsule path": func(p *productionSchemaMaintenanceGuardianAdopted) { p.HostBinding.CapsulePath += ".other" },
		"old capsule bytes": func(p *productionSchemaMaintenanceGuardianAdopted) {
			p.HostBinding.CapsuleSHA256 = strings.Repeat("f", 64)
		},
		"host plan": func(p *productionSchemaMaintenanceGuardianAdopted) {
			p.HostBinding.PlanSHA256 = strings.Repeat("f", 64)
		},
		"daemon invocation": func(p *productionSchemaMaintenanceGuardianAdopted) {
			p.DaemonGeneration.DaemonInvocationID = strings.Repeat("f", 32)
		},
		"daemon pid":     func(p *productionSchemaMaintenanceGuardianAdopted) { p.DaemonGeneration.DaemonPID++ },
		"daemon start":   func(p *productionSchemaMaintenanceGuardianAdopted) { p.DaemonGeneration.DaemonStartTicks++ },
		"guardian pid":   func(p *productionSchemaMaintenanceGuardianAdopted) { p.GuardianPID++ },
		"guardian start": func(p *productionSchemaMaintenanceGuardianAdopted) { p.GuardianStartTicks++ },
		"binding inode":  func(p *productionSchemaMaintenanceGuardianAdopted) { p.BindingFile.Inode++ },
		"binding device": func(p *productionSchemaMaintenanceGuardianAdopted) { p.BindingFile.Device++ },
		"socket inode":   func(p *productionSchemaMaintenanceGuardianAdopted) { p.GuardianSocket.Inode++ },
		"socket path":    func(p *productionSchemaMaintenanceGuardianAdopted) { p.GuardianSocket.Path += ".other" },
		"lock omitted":   func(p *productionSchemaMaintenanceGuardianAdopted) { p.Locks = p.Locks[:2] },
		"lock duplicate": func(p *productionSchemaMaintenanceGuardianAdopted) { p.Locks[1] = p.Locks[0] },
		"lock reordered": func(p *productionSchemaMaintenanceGuardianAdopted) { p.Locks[1], p.Locks[2] = p.Locks[2], p.Locks[1] },
		"lock unlocked":  func(p *productionSchemaMaintenanceGuardianAdopted) { p.Locks[0].Held = false },
		"lock inode":     func(p *productionSchemaMaintenanceGuardianAdopted) { p.Locks[0].Inode++ },
		"lock device":    func(p *productionSchemaMaintenanceGuardianAdopted) { p.Locks[0].Device++ },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f, runtime, host, generation, profile := schemaGuardianAdoptedFixture(t)
			mutate(&profile)
			raw, err := schemaMaintenanceRPCMarshal(profile)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(f.binding.Workspace, "state", "guardian-adopted.json"), append(raw, '\n'), 0o600); err != nil {
				t.Fatal(err)
			}
			schemaGuardianAssertReadOnlyRejection(t, f, runtime, host, generation)
		})
	}
}

func TestSchemaMaintenanceGuardianAdoptedRejectsNoncanonicalEvidence(t *testing.T) {
	for _, name := range []string{"duplicate", "case", "unknown", "trailing", "no newline", "indent", "missing", "permission", "hardlink", "symlink"} {
		t.Run(name, func(t *testing.T) {
			f, runtime, host, generation, profile := schemaGuardianAdoptedFixture(t)
			path := filepath.Join(f.binding.Workspace, "state", "guardian-adopted.json")
			raw, _ := os.ReadFile(path)
			switch name {
			case "duplicate":
				raw = bytes.Replace(raw, []byte(`"format":`), []byte(`"format":"duplicate","format":`), 1)
			case "case":
				raw = bytes.Replace(raw, []byte(`"format":`), []byte(`"Format":`), 1)
			case "unknown":
				raw = bytes.Replace(raw, []byte(`"format":`), []byte(`"extra":true,"format":`), 1)
			case "trailing":
				raw = append(raw, []byte("{}\n")...)
			case "no newline":
				raw = bytes.TrimSuffix(raw, []byte{'\n'})
			case "indent":
				raw, _ = json.MarshalIndent(profile, "", "  ")
				raw = append(raw, '\n')
			case "missing":
				raw = bytes.Replace(raw, []byte(`"format":"`+schemaMaintenanceGuardianAdoptedFormat+`",`), nil, 1)
			case "permission":
				if err := os.Chmod(path, 0o644); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(path, path+".linked"); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Rename(path, path+".original"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(path+".original", path); err != nil {
					t.Fatal(err)
				}
			}
			if name != "permission" && name != "hardlink" && name != "symlink" {
				if err := os.WriteFile(path, raw, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			schemaGuardianAssertReadOnlyRejection(t, f, runtime, host, generation)
		})
	}
}

func TestSchemaMaintenanceGuardianAdoptedRejectsNativeDrift(t *testing.T) {
	for _, name := range []string{"binding inode", "socket inode", "lock inode", "unrelated OFD", "profile replaced during check", "daemon replaced during check", "host authority drift"} {
		t.Run(name, func(t *testing.T) {
			f, runtime, host, generation, _ := schemaGuardianAdoptedFixture(t)
			switch name {
			case "binding inode":
				raw, _ := os.ReadFile(f.binding.GuardianBindingPath)
				if err := os.Rename(f.binding.GuardianBindingPath, f.binding.GuardianBindingPath+".old"); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(f.binding.GuardianBindingPath, raw, 0o600); err != nil {
					t.Fatal(err)
				}
			case "socket inode":
				if err := os.Rename(f.binding.GuardianSocket, f.binding.GuardianSocket+".old"); err != nil {
					t.Fatal(err)
				}
				listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: f.binding.GuardianSocket, Net: "unix"})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = listener.Close() })
				if err := os.Chmod(f.binding.GuardianSocket, 0o600); err != nil {
					t.Fatal(err)
				}
			case "lock inode", "unrelated OFD":
				if name == "lock inode" {
					if err := os.Rename(f.paths[0], f.paths[0]+".old"); err != nil {
						t.Fatal(err)
					}
				} else if err := unix.Flock(int(f.files[0].Fd()), unix.LOCK_UN); err != nil {
					t.Fatal(err)
				}
				file, err := os.OpenFile(f.paths[0], os.O_CREATE|os.O_RDWR, 0o600)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = file.Close() })
				if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
					t.Fatal(err)
				}
			case "profile replaced during check", "daemon replaced during check":
				filename := "guardian-adopted.json"
				if name == "daemon replaced during check" {
					filename = "daemon-binding.json"
				}
				called := false
				host.validate = func(context.Context) error {
					if !called {
						called = true
						path := filepath.Join(f.binding.Workspace, "state", filename)
						raw, err := os.ReadFile(path)
						if err != nil {
							return err
						}
						if err := os.Rename(path, path+".old"); err != nil {
							return err
						}
						return os.WriteFile(path, raw, 0o600)
					}
					return nil
				}
				if err := runtime.checkSchemaMaintenanceGuardianReadoptedForPaths(host, generation, f.paths); err == nil {
					t.Fatal("same-byte original evidence inode replacement was accepted")
				}
				f.assertIndependentlyLocked(t)
				return
			case "host authority drift":
				calls := 0
				host.validate = func(context.Context) error {
					calls++
					if calls > 1 {
						return errors.New("native plan changed during capture")
					}
					return nil
				}
			}
			schemaGuardianAssertReadOnlyRejection(t, f, runtime, host, generation)
		})
	}
}

func TestSchemaMaintenanceGuardianAdoptedHelperProcess(t *testing.T) {
	if os.Getenv("LMM_SCHEMA_GUARDIAN_ADOPTER_FIXTURE") == "" {
		return
	}
	var binding productionSchemaMaintenanceHostBinding
	if err := json.Unmarshal([]byte(os.Getenv("LMM_SCHEMA_GUARDIAN_ADOPTER_BINDING")), &binding); err != nil {
		t.Fatal(err)
	}
	var paths []string
	if err := json.Unmarshal([]byte(os.Getenv("LMM_SCHEMA_GUARDIAN_ADOPTER_LOCKS")), &paths); err != nil {
		t.Fatal(err)
	}
	lease, err := receiveSchemaMaintenanceHostLocksForPaths(context.Background(), binding, uint32(os.Getuid()), paths)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	runtime := &productionRuntime{requiredOwnerUID: uint32(os.Getuid()), effectiveUID: os.Geteuid}
	host := &productionSchemaMaintenanceHost{runtime: runtime, binding: binding, lease: lease, validate: func(context.Context) error { return nil }}
	sha, err := sha256File("/proc/self/exe")
	if err != nil {
		t.Fatal(err)
	}
	start, err := schemaMaintenanceGuardianProcess(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	generation := schemaMaintenanceRPCBinding{PlanSHA256: binding.PlanSHA256, TransitionID: binding.TransitionID, DaemonPID: os.Getpid(), DaemonInvocationID: os.Getenv("INVOCATION_ID"), DaemonExeSHA256: sha, DaemonStartTicks: start}
	raw, _ := schemaMaintenanceRPCMarshal(generation)
	if err := runtime.writeSchemaMaintenanceRawNewFile(filepath.Join(binding.Workspace, "state", "daemon-binding.json"), append(raw, '\n')); err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeSchemaMaintenanceGuardianAdoptedForPaths(host, generation, paths); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(binding.Workspace, "state", "guardian-adopted.json")
	before, _ := os.ReadFile(path)
	info, _ := os.Lstat(path)
	if err := runtime.writeSchemaMaintenanceGuardianAdoptedForPaths(host, generation, paths); err == nil {
		t.Fatal("original native adoption was overwritten")
	}
	after, _ := os.ReadFile(path)
	afterInfo, _ := os.Lstat(path)
	if !bytes.Equal(before, after) || !schemaMaintenanceSameGuardianFile(info, afterInfo) {
		t.Fatal("second original adoption modified its record")
	}
	fmt.Fprintln(os.Stdout, "schema-adopter-ready")
	<-time.After(2 * time.Minute)
	t.Fatal("fixture adopter was not killed by its parent")
}

func TestSchemaMaintenanceGuardianAdoptedPythonSurvivesKilledOriginalDaemon(t *testing.T) {
	f := newSchemaGuardianFixture(t)
	if err := f.listener.Close(); err != nil {
		t.Fatal(err)
	}
	for _, file := range f.files {
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
	}
	f.binding.TransitionID = filepath.Base(f.binding.Workspace)
	f.binding.CapsulePath = filepath.Join(f.binding.Workspace, "old-capsule.json")
	f.binding.CapsuleSHA256 = strings.Repeat("a", 64)
	plan, _ := json.Marshal(map[string]string{"transition_id": f.binding.TransitionID})
	if err := os.WriteFile(f.binding.PlanPath, plan, 0o600); err != nil {
		t.Fatal(err)
	}
	f.binding.PlanSHA256 = startupContentSHA256(plan)
	bound, _ := json.Marshal(schemaMaintenanceGuardianBinding{schemaMaintenanceGuardianBindingFormat, f.binding.TransitionID, f.binding.PlanPath, f.binding.PlanSHA256, f.binding.Host, f.binding.Workspace, f.binding.GuardianSocket})
	if err := os.WriteFile(f.binding.GuardianBindingPath, bound, 0o600); err != nil {
		t.Fatal(err)
	}
	f.binding.GuardianBindingSHA256 = startupContentSHA256(bound)
	script, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "scripts", "schema-maintenance-lock-guardian.py"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(script); err != nil {
		t.Fatal(err)
	}
	harness := `import importlib.util, os, sys
from pathlib import Path
spec = importlib.util.spec_from_file_location('schema_guardian_fixture', sys.argv[1])
guardian = importlib.util.module_from_spec(spec)
spec.loader.exec_module(guardian)
guardian.serve(Path(sys.argv[2]), sys.argv[3], uid=os.getuid(),
               lock_paths=dict(zip(('native', 'systemd', 'frontend'), sys.argv[4:])),
               observed_host='fixture-host')
`
	args := []string{"-I", "-B", "-c", harness, script, f.binding.GuardianBindingPath, f.binding.GuardianBindingSHA256}
	args = append(args, f.paths...)
	guardian := exec.Command("python3", args...)
	guardian.Stdout, guardian.Stderr = io.Discard, io.Discard
	if err := guardian.Start(); err != nil {
		t.Fatal(err)
	}
	guardianDone := make(chan error, 1)
	go func() { guardianDone <- guardian.Wait() }()
	guardianWaited := false
	t.Cleanup(func() {
		if !guardianWaited {
			_ = guardian.Process.Kill()
			<-guardianDone
		}
	})
	deadline := time.Now().Add(5 * time.Second)
	for {
		if info, err := os.Lstat(f.binding.GuardianSocket); err == nil && info.Mode()&os.ModeSocket != 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("real Python guardian did not create its native fixture socket")
		}
		time.Sleep(10 * time.Millisecond)
	}
	bindingRaw, _ := json.Marshal(f.binding)
	pathsRaw, _ := json.Marshal(f.paths)
	adopter := exec.Command(os.Args[0], "-test.run=^TestSchemaMaintenanceGuardianAdoptedHelperProcess$", "-test.timeout=90s")
	adopter.Env = append(os.Environ(), "LMM_SCHEMA_GUARDIAN_ADOPTER_FIXTURE=1", "LMM_SCHEMA_GUARDIAN_ADOPTER_BINDING="+string(bindingRaw), "LMM_SCHEMA_GUARDIAN_ADOPTER_LOCKS="+string(pathsRaw), "INVOCATION_ID="+strings.Repeat("d", 32))
	stdout, err := adopter.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	adopter.Stderr = io.Discard
	if err := adopter.Start(); err != nil {
		t.Fatal(err)
	}
	adopterDone := make(chan error, 1)
	go func() { adopterDone <- adopter.Wait() }()
	adopterWaited := false
	t.Cleanup(func() {
		if !adopterWaited {
			_ = adopter.Process.Kill()
			<-adopterDone
		}
	})
	ready := make(chan error, 1)
	go func() {
		line, err := bufio.NewReader(stdout).ReadString('\n')
		if err == nil && line != "schema-adopter-ready\n" {
			err = fmt.Errorf("unexpected adopter output: %s", line)
		}
		ready <- err
	}()
	select {
	case err := <-ready:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("actual adopter did not durably record native original guardian identity")
	}
	runtime := &productionRuntime{requiredOwnerUID: uint32(os.Getuid()), effectiveUID: os.Geteuid}
	raw, err := runtime.readExistingSchemaSealedFile(filepath.Join(f.binding.Workspace, "state", "daemon-binding.json"), true)
	if err != nil {
		t.Fatal(err)
	}
	var generation schemaMaintenanceRPCBinding
	if err := schemaMaintenanceRPCDecode(bytes.TrimSuffix(raw, []byte{'\n'}), &generation); err != nil {
		t.Fatal(err)
	}
	if generation.DaemonPID != adopter.Process.Pid || schemaMaintenanceRPCProcess(generation) != nil {
		t.Fatal("original adopter receipt did not bind its actual live process")
	}
	profilePath := filepath.Join(f.binding.Workspace, "state", "guardian-adopted.json")
	profileBefore, _ := os.ReadFile(profilePath)
	profileInfo, _ := os.Lstat(profilePath)
	f.assertIndependentlyLocked(t)
	if err := adopter.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := <-adopterDone; err == nil {
		t.Fatal("original adopter did not die from SIGKILL")
	}
	adopterWaited = true
	if schemaMaintenanceRPCProcess(generation) == nil {
		t.Fatal("dead original adopter still passed native generation check")
	}
	f.assertIndependentlyLocked(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	lease, err := receiveSchemaMaintenanceHostLocksForPaths(ctx, f.binding, uint32(os.Getuid()), f.paths)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	host := &productionSchemaMaintenanceHost{runtime: runtime, binding: f.binding, lease: lease, validate: func(context.Context) error { return nil }}
	if err := runtime.checkSchemaMaintenanceGuardianReadoptedForPaths(host, generation, f.paths); err != nil {
		t.Fatal(err)
	}
	if lease.(*schemaMaintenanceHostLockLease).pid != guardian.Process.Pid {
		t.Fatal("recovery adopted a different guardian process")
	}
	profileAfter, _ := os.ReadFile(profilePath)
	afterInfo, _ := os.Lstat(profilePath)
	if !bytes.Equal(profileBefore, profileAfter) || !schemaMaintenanceSameGuardianFile(profileInfo, afterInfo) {
		t.Fatal("recovery rewrote the original native guardian record")
	}
	f.assertIndependentlyLocked(t)
	if err := guardian.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	<-guardianDone
	guardianWaited = true
	schemaGuardianAssertReadOnlyRejection(t, f, runtime, host, generation)
}
