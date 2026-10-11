//go:build !linux

package deploycli

import "errors"

func schemaMaintenanceDeadHolderProcessGone(schemaMaintenanceRPCBinding, map[string]string) error {
	return errors.New("dead schema daemon recovery requires actual Linux process identity")
}
