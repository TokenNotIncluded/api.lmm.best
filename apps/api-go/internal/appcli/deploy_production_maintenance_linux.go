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
	request := map[string]any{"protocol": productionMaintenanceLockProtocol, "handoff_sha256": h.SHA256, "handoff_path": h.Path, "transition_id": h.TransitionID, "operation": "adopt"}
	payload, _ := json.Marshal(request)
	if _, err := unixConnection.Write(append(payload, '\n')); err != nil {
		return nil, nil, err
	}
	_ = unixConnection.SetReadDeadline(time.Now().Add(125 * time.Second))
	body, oob := make([]byte, 16384), make([]byte, unix.CmsgSpace(4))
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
	if len(descriptors) != 1 || flags&(unix.MSG_TRUNC|unix.MSG_CTRUNC) != 0 {
		for _, fd := range descriptors {
			_ = unix.Close(fd)
		}
		return nil, nil, errors.New("guardian must transfer exactly one complete lock descriptor")
	}
	file := os.NewFile(uintptr(descriptors[0]), lockPath)
	unix.CloseOnExec(descriptors[0])
	good := false
	defer func() {
		if !good {
			_ = file.Close()
		}
	}()
	var reply struct {
		GuardianPID   int    `json:"guardian_pid"`
		Protocol      string `json:"protocol"`
		HandoffSHA256 string `json:"handoff_sha256"`
		TransitionID  string `json:"transition_id"`
	}
	if json.Unmarshal(body[:n], &reply) != nil || reply.GuardianPID != int(peer.Pid) || reply.Protocol != productionMaintenanceLockProtocol || reply.HandoffSHA256 != h.SHA256 || reply.TransitionID != h.TransitionID {
		return nil, nil, errors.New("guardian lock receipt differs from immutable handoff")
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
	return file, connection, nil
}
