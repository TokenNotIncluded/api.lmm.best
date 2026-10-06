//go:build !linux

package appcli

import (
	"context"
	"errors"
	"net"
	"os"
)

func maintenanceFileIDs(os.FileInfo) (uint32, uint32, bool) { return 0, 0, false }

func receiveMaintenanceLock(context.Context, *productionMaintenanceHandoff, string, uint32) (*os.File, net.Conn, error) {
	return nil, nil, errors.New("maintenance descriptor handoff requires Linux SCM_RIGHTS")
}

func receiveMaintenanceAllLocks(context.Context, *productionMaintenanceHandoff, string, uint32) ([]*os.File, net.Conn, error) {
	return nil, nil, errors.New("maintenance all-lock adoption requires Linux SCM_RIGHTS")
}
