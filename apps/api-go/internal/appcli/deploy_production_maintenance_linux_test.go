//go:build linux

package appcli

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
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
		reply, _ := json.Marshal(map[string]any{"guardian_pid": os.Getpid(), "protocol": productionMaintenanceLockProtocol, "transition_id": h.TransitionID, "handoff_sha256": h.SHA256})
		if _, _, err := connection.WriteMsgUnix(reply, unix.UnixRights(int(original.Fd())), nil); err != nil {
			done <- err
			return
		}
		_, err = connection.Read(make([]byte, 1))
		done <- err
	}()
	received, lease, err := receiveMaintenanceLock(context.Background(), h, path, uint32(os.Getuid()))
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
