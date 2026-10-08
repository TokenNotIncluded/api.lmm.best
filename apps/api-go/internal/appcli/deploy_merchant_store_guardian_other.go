//go:build !linux

package appcli

import (
	"errors"
	"net"
)

func merchantStoreFencePeer(_ net.Conn, _ uint32, _ int) error {
	return errors.New("merchant deployment durable holder requires Linux peer credentials")
}
