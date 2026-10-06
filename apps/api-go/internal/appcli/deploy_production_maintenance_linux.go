//go:build linux

package appcli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

func maintenanceFileIDs(info os.FileInfo) (uint32, uint32, bool) {
	value, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, false
	}
	return value.Uid, value.Gid, true
}

func receiveMaintenanceLock(ctx context.Context, h *productionMaintenanceHandoff, lockPath string, owner uint32) (*os.File, net.Conn, error) {
	return receiveMaintenanceLockForPaths(ctx, h, lockPath, owner, map[string]string{"native": lockPath, "systemd": "/var/lib/lmm-api-deploy-systemd/lock", "frontend": "/srv/lmm-api-frontend/.release.lock"})
}

func receiveMaintenanceLockForPaths(ctx context.Context, h *productionMaintenanceHandoff, lockPath string, owner uint32, expectedPaths map[string]string) (*os.File, net.Conn, error) {
	files, lease, err := receiveMaintenanceLockSet(ctx, h, lockPath, owner, expectedPaths, false)
	if err != nil {
		return nil, nil, err
	}
	return files[0], lease, nil
}

func receiveMaintenanceAllLocks(ctx context.Context, h *productionMaintenanceHandoff, lockPath string, owner uint32) ([]*os.File, net.Conn, error) {
	return receiveMaintenanceLockSet(ctx, h, lockPath, owner, map[string]string{"native": lockPath, "systemd": "/var/lib/lmm-api-deploy-systemd/lock", "frontend": "/srv/lmm-api-frontend/.release.lock"}, true)
}

func receiveMaintenanceLockSet(ctx context.Context, h *productionMaintenanceHandoff, lockPath string, owner uint32, expectedPaths map[string]string, all bool) ([]*os.File, net.Conn, error) {
	connection, err := (&net.Dialer{Timeout: 125 * time.Second}).DialContext(ctx, "unix", h.GuardianSocket)
	if err != nil {
		return nil, nil, err
	}
	unixConnection, ok := connection.(*net.UnixConn)
	if !ok {
		_ = connection.Close()
		return nil, nil, errors.New("guardian connection is not Unix")
	}
	closeOnFailure := true
	defer func() {
		if closeOnFailure {
			_ = connection.Close()
		}
	}()
	raw, err := unixConnection.SyscallConn()
	if err != nil {
		return nil, nil, err
	}
	var peer *unix.Ucred
	var peerErr error
	if err := raw.Control(func(fd uintptr) { peer, peerErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED) }); err != nil || peerErr != nil || peer == nil || peer.Uid != owner {
		return nil, nil, errors.New("guardian peer owner differs from deployment authority")
	}
	operation := "adopt"
	expectedCount := 1
	if all {
		operation = "adopt-all"
		expectedCount = 3
	}
	request := map[string]any{"protocol": productionMaintenanceLockProtocol, "handoff_sha256": h.SHA256, "handoff_path": h.Path, "transition_id": h.TransitionID, "operation": operation}
	payload, _ := json.Marshal(request)
	if _, err := unixConnection.Write(append(payload, '\n')); err != nil {
		return nil, nil, err
	}
	_ = unixConnection.SetReadDeadline(time.Now().Add(125 * time.Second))
	body, oob := make([]byte, 16384), make([]byte, unix.CmsgSpace(4*expectedCount))
	n, on, flags, _, err := unixConnection.ReadMsgUnix(body, oob)
	if err != nil {
		return nil, nil, err
	}
	messages, err := unix.ParseSocketControlMessage(oob[:on])
	if err != nil {
		return nil, nil, err
	}
	descriptors := []int{}
	for _, message := range messages {
		if message.Header.Level == unix.SOL_SOCKET && message.Header.Type == unix.SCM_RIGHTS {
			received, err := unix.ParseUnixRights(&message)
			if err != nil {
				return nil, nil, err
			}
			descriptors = append(descriptors, received...)
		}
	}
	if len(descriptors) != expectedCount || flags&(unix.MSG_TRUNC|unix.MSG_CTRUNC) != 0 {
		for _, fd := range descriptors {
			_ = unix.Close(fd)
		}
		return nil, nil, errors.New("guardian must transfer the exact complete lock descriptor set")
	}
	transferred := []string{lockPath}
	if all {
		transferred = []string{expectedPaths["native"], expectedPaths["systemd"], expectedPaths["frontend"]}
	}
	files := make([]*os.File, 0, len(descriptors))
	for index, descriptor := range descriptors {
		unix.CloseOnExec(descriptor)
		files = append(files, os.NewFile(uintptr(descriptor), transferred[index]))
	}
	file := files[0]
	good := false
	defer func() {
		if !good {
			for _, file := range files {
				_ = file.Close()
			}
		}
	}()
	var reply struct {
		Locks []struct {
			Path   string `json:"path"`
			Device uint64 `json:"device"`
			Inode  uint64 `json:"inode"`
			Held   bool   `json:"held"`
		} `json:"locks"`
		TransferredPaths []string `json:"transferred_paths"`
		GuardianPID      int      `json:"guardian_pid"`
		Protocol         string   `json:"protocol"`
		HandoffSHA256    string   `json:"handoff_sha256"`
		TransitionID     string   `json:"transition_id"`
	}
	if json.Unmarshal(body[:n], &reply) != nil || reply.GuardianPID != int(peer.Pid) || reply.Protocol != productionMaintenanceLockProtocol || reply.HandoffSHA256 != h.SHA256 || reply.TransitionID != h.TransitionID {
		return nil, nil, errors.New("guardian lock receipt differs from immutable handoff")
	}
	if all {
		if len(reply.TransferredPaths) != 3 {
			return nil, nil, errors.New("guardian did not transfer every frozen owner lock")
		}
		for index, path := range transferred {
			if reply.TransferredPaths[index] != path {
				return nil, nil, errors.New("guardian transferred lock order differs")
			}
			info, err := files[index].Stat()
			if err != nil {
				return nil, nil, err
			}
			current, err := os.Lstat(path)
			if err != nil {
				return nil, nil, err
			}
			uid, links, ok := deploymentFileOwnership(info)
			if !ok || uid != owner || links != 1 || !info.Mode().IsRegular() || !os.SameFile(info, current) || current.Mode()&os.ModeSymlink != 0 {
				return nil, nil, errors.New("guardian transferred lock inode differs from frozen owner")
			}
		}
	}
	if len(reply.Locks) != 3 || len(expectedPaths) != 3 {
		return nil, nil, errors.New("guardian must freeze all three normal owner locks")
	}
	seen := map[string]bool{}
	for _, lock := range reply.Locks {
		known := false
		for _, path := range expectedPaths {
			if path == lock.Path {
				known = true
			}
		}
		if !known || seen[lock.Path] || !lock.Held {
			return nil, nil, errors.New("guardian frozen lock metadata differs from normal owner paths")
		}
		seen[lock.Path] = true
		metadata, err := os.Lstat(lock.Path)
		if err != nil {
			return nil, nil, err
		}
		stat, ok := metadata.Sys().(*syscall.Stat_t)
		if !ok || !metadata.Mode().IsRegular() || stat.Uid != owner || stat.Nlink != 1 || uint64(stat.Dev) != lock.Device || stat.Ino != lock.Inode {
			return nil, nil, errors.New("guardian frozen lock inode changed")
		}
		check, err := unix.Open(lock.Path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return nil, nil, err
		}
		err = unix.Flock(check, unix.LOCK_EX|unix.LOCK_NB)
		_ = unix.Close(check)
		if err == nil {
			return nil, nil, errors.New("guardian freeze is independently acquirable")
		}
		if err != unix.EWOULDBLOCK && err != unix.EAGAIN {
			return nil, nil, err
		}
	}
	info, err := file.Stat()
	if err != nil {
		return nil, nil, err
	}
	pathInfo, err := os.Lstat(lockPath)
	if err != nil {
		return nil, nil, err
	}
	uid, links, valid := deploymentFileOwnership(info)
	if !valid || uid != owner || links != 1 || !info.Mode().IsRegular() || !os.SameFile(info, pathInfo) || pathInfo.Mode()&os.ModeSymlink != 0 {
		return nil, nil, fmt.Errorf("guardian descriptor is not the deployment lock inode")
	}
	_ = unixConnection.SetReadDeadline(time.Time{})
	good, closeOnFailure = true, false
	return files, connection, nil
}
