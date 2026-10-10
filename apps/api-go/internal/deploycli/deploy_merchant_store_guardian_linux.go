//go:build linux

package deploycli

import (
	"errors"
	"net"

	"golang.org/x/sys/unix"
)

func merchantStoreFencePeer(connection net.Conn, uid uint32, pid int) error {
	stream, ok := connection.(*net.UnixConn)
	if !ok {
		return errors.New("merchant deployment holder peer is not Unix")
	}
	raw, err := stream.SyscallConn()
	if err != nil {
		return err
	}
	var peer *unix.Ucred
	var peerErr error
	if err := raw.Control(func(fd uintptr) { peer, peerErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED) }); err != nil || peerErr != nil || peer == nil || peer.Uid != uid || pid != 0 && int(peer.Pid) != pid {
		return errors.New("merchant deployment holder actual Unix peer differs from the sealed owner")
	}
	return nil
}
