//go:build linux

package appcli

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

func TestMaintenanceSCMRightsAdoptionClosesWithoutUnlockingGuardian(t *testing.T) {
	root := t.TempDir()
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
