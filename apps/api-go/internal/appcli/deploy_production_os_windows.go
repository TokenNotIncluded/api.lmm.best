//go:build windows

package appcli

import (
	"errors"
	"os"
)

var errProductionDeploymentUnsupported = errors.New("production deployment state checks are unsupported on Windows")

func tryDeploymentFileLock(_ *os.File) (bool, error) {
	return false, errProductionDeploymentUnsupported
}

func unlockDeploymentFile(_ *os.File) error {
	return nil
}

func deploymentFileOwnership(_ os.FileInfo) (uint32, uint64, bool) {
	return 0, 0, false
}
