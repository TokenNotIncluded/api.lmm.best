//go:build linux

package deploycli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

func TestMaintenanceAllSCMRightsAdoptionRetainsEveryLockAfterGuardianExit(t *testing.T) {
	root, err := os.MkdirTemp(maintenanceFixtureCache(t), "maint-scm-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	paths := map[string]string{}
	originals := []*os.File{}
	locks := []map[string]any{}
	ordered := []string{}
	for _, name := range []string{"native", "systemd", "frontend"} {
		path := filepath.Join(root, name+".lock")
		paths[name] = path
		ordered = append(ordered, path)
		file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			t.Fatal(err)
		}
		originals = append(originals, file)
		t.Cleanup(func() { file.Close() })
		if ok, err := tryDeploymentFileLock(file); err != nil || !ok {
			t.Fatalf("lock %s: %v", name, err)
		}
		info, _ := file.Stat()
		stat := info.Sys().(*syscall.Stat_t)
		locks = append(locks, map[string]any{"path": path, "device": uint64(stat.Dev), "inode": stat.Ino, "held": true})
	}
	socket := filepath.Join(root, "guardian.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	h := &productionMaintenanceHandoff{GuardianSocket: socket, TransitionID: "fixture", Path: "/fixture/handoff.json", SHA256: "bound"}
	done := make(chan error, 1)
	go func() {
		connection, err := listener.AcceptUnix()
		if err != nil {
			done <- err
			return
		}
		defer connection.Close()
		body, err := bufio.NewReader(connection).ReadBytes('\n')
		if err != nil {
			done <- err
			return
		}
		var request map[string]any
		if json.Unmarshal(body, &request) != nil || request["operation"] != "adopt-all" {
			done <- fmt.Errorf("request=%s", body)
			return
		}
		reply, _ := json.Marshal(map[string]any{"locks": locks, "transferred_paths": ordered, "guardian_pid": os.Getpid(), "protocol": productionMaintenanceLockProtocol, "transition_id": h.TransitionID, "handoff_sha256": h.SHA256})
		_, _, err = connection.WriteMsgUnix(reply, unix.UnixRights(int(originals[0].Fd()), int(originals[1].Fd()), int(originals[2].Fd())), nil)
		if err == nil {
			for _, file := range originals {
				file.Close()
			}
		}
		done <- err
	}()
	files, lease, err := receiveMaintenanceLockSet(context.Background(), h, paths["native"], uint32(os.Getuid()), paths, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	for _, path := range ordered {
		independent, err := os.OpenFile(path, os.O_RDWR, 0)
		if err != nil {
			t.Fatal(err)
		}
		acquired, err := tryDeploymentFileLock(independent)
		independent.Close()
		if err != nil || acquired {
			t.Fatalf("guardian exit released %s: acquired=%v err=%v", path, acquired, err)
		}
	}
	runtime := &productionRuntime{guardianLease: lease, guardianExtraLocks: files[1:]}
	runtime.releaseGlobalLock(files[0])
	for _, path := range ordered {
		independent, err := os.OpenFile(path, os.O_RDWR, 0)
		if err != nil {
			t.Fatal(err)
		}
		acquired, err := tryDeploymentFileLock(independent)
		independent.Close()
		if err != nil || !acquired {
			t.Fatalf("closed receiver still owns %s: %v", path, err)
		}
	}
}

func TestMaintenanceSCMRightsAdoptionClosesWithoutUnlockingGuardian(t *testing.T) {
	root, err := os.MkdirTemp(maintenanceFixtureCache(t), "maint-scm-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	path := filepath.Join(root, "lock")
	original, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer original.Close()
	if err := unix.Flock(int(original.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(root, "guardian.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	h := &productionMaintenanceHandoff{GuardianSocket: socket, TransitionID: "fixture", Path: "/fixture/handoff.json", SHA256: "bound"}
	paths := map[string]string{"native": path, "systemd": filepath.Join(root, "systemd.lock"), "frontend": filepath.Join(root, "frontend.lock")}
	locks := []map[string]any{}
	for name, lockPath := range paths {
		lock := original
		if name != "native" {
			lock, err = os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Close()
			if ok, err := tryDeploymentFileLock(lock); err != nil || !ok {
				t.Fatalf("lock=%s err=%v", name, err)
			}
		}
		info, err := lock.Stat()
		if err != nil {
			t.Fatal(err)
		}
		stat := info.Sys().(*syscall.Stat_t)
		locks = append(locks, map[string]any{"path": lockPath, "device": uint64(stat.Dev), "inode": stat.Ino, "held": true})
	}
	done := make(chan error, 1)
	go func() {
		connection, err := listener.AcceptUnix()
		if err != nil {
			done <- err
			return
		}
		defer connection.Close()
		if _, err := bufio.NewReader(connection).ReadBytes('\n'); err != nil {
			done <- err
			return
		}
		reply, _ := json.Marshal(map[string]any{"locks": locks, "guardian_pid": os.Getpid(), "protocol": productionMaintenanceLockProtocol, "transition_id": h.TransitionID, "handoff_sha256": h.SHA256})
		if _, _, err := connection.WriteMsgUnix(reply, unix.UnixRights(int(original.Fd())), nil); err != nil {
			done <- err
			return
		}
		_, err = connection.Read(make([]byte, 1))
		done <- err
	}()
	received, lease, err := receiveMaintenanceLockForPaths(context.Background(), h, path, uint32(os.Getuid()), paths)
	if err != nil {
		t.Fatal(err)
	}
	runtime := &productionRuntime{guardianLease: lease}
	runtime.releaseGlobalLock(received)
	<-done
	independent, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer independent.Close()
	acquired, err := tryDeploymentFileLock(independent)
	if err != nil {
		t.Fatal(err)
	}
	if acquired {
		t.Fatal("closing adopted descriptor unlocked the guardian's same open file description")
	}
}
